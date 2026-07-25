package nodedata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultMaxConcurrency = 4
	defaultMaxChunkSize   = 256 * 1024
	defaultMaxResponse    = int64(1 << 30)
	defaultRequestBuffer  = 8
	defaultSendQueueSize  = 64
)

var (
	ErrSessionNotFound  = errors.New("node data session not found")
	ErrSessionReplaced  = errors.New("node data session replaced")
	ErrRequestNotFound  = errors.New("node data request not found")
	ErrRequestBacklog   = errors.New("node data consumer is too slow")
	ErrProtocol         = errors.New("invalid node data protocol")
	ErrResponseTooLarge = errors.New("node data response exceeds the configured limit")
)

type Config struct {
	MaxConcurrency   int
	MaxChunkSize     int
	MaxResponseBytes int64
	RequestBuffer    int
	SendQueueSize    int
}

type RegisterInput struct {
	ServerID         uint64
	NodeUUID         string
	BootID           string
	ControlSessionID string
	Protocol         uint32
}

type OpenInput struct {
	Type       string
	Locator    *nodev1.DataLocator
	DeadlineAt time.Time
}

type Response struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

type Registry struct {
	mu       sync.RWMutex
	byServer map[uint64]*Session
	config   Config
}

func NewRegistry(config Config) *Registry {
	if config.MaxConcurrency <= 0 {
		config.MaxConcurrency = defaultMaxConcurrency
	}
	if config.MaxChunkSize <= 0 || config.MaxChunkSize > defaultMaxChunkSize {
		config.MaxChunkSize = defaultMaxChunkSize
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = defaultMaxResponse
	}
	if config.RequestBuffer <= 0 {
		config.RequestBuffer = defaultRequestBuffer
	}
	if config.SendQueueSize <= 0 {
		config.SendQueueSize = defaultSendQueueSize
	}
	return &Registry{byServer: make(map[uint64]*Session), config: config}
}

func (registry *Registry) Config() Config { return registry.config }

func (registry *Registry) Register(parent context.Context, input RegisterInput) *Session {
	ctx, cancel := context.WithCancelCause(parent)
	session := &Session{
		ID: uuid.NewString(), ServerID: input.ServerID, NodeUUID: input.NodeUUID,
		BootID: input.BootID, ControlSessionID: input.ControlSessionID, Protocol: input.Protocol,
		ctx: ctx, cancel: cancel, config: registry.config,
		outgoing: make(chan *nodev1.SystemDataFrame, registry.config.SendQueueSize),
		requests: make(map[string]*dataRequest), slots: make(chan struct{}, registry.config.MaxConcurrency),
	}

	registry.mu.Lock()
	previous := registry.byServer[input.ServerID]
	registry.byServer[input.ServerID] = session
	registry.mu.Unlock()
	if previous != nil {
		previous.close(ErrSessionReplaced)
	}
	go func() {
		<-ctx.Done()
		session.failAll(context.Cause(ctx))
	}()
	return session
}

func (registry *Registry) Get(serverID uint64) (*Session, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	session, ok := registry.byServer[serverID]
	return session, ok
}

func (registry *Registry) Remove(serverID uint64, sessionID string) bool {
	registry.mu.Lock()
	session, ok := registry.byServer[serverID]
	if !ok || session.ID != sessionID {
		registry.mu.Unlock()
		return false
	}
	delete(registry.byServer, serverID)
	registry.mu.Unlock()
	session.close(context.Canceled)
	return true
}

func (registry *Registry) Open(ctx context.Context, serverID uint64, input OpenInput) (*Response, error) {
	session, ok := registry.Get(serverID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	return session.Open(ctx, input)
}

type Session struct {
	ID               string
	ServerID         uint64
	NodeUUID         string
	BootID           string
	ControlSessionID string
	Protocol         uint32

	ctx      context.Context
	cancel   context.CancelCauseFunc
	config   Config
	outgoing chan *nodev1.SystemDataFrame
	slots    chan struct{}

	mu       sync.RWMutex
	requests map[string]*dataRequest
}

func (session *Session) Context() context.Context                 { return session.ctx }
func (session *Session) Outgoing() <-chan *nodev1.SystemDataFrame { return session.outgoing }

func (session *Session) Open(ctx context.Context, input OpenInput) (*Response, error) {
	if strings.TrimSpace(input.Type) == "" || input.Locator == nil {
		return nil, fmt.Errorf("%w: request type and locator are required", ErrProtocol)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-session.ctx.Done():
		return nil, context.Cause(session.ctx)
	case session.slots <- struct{}{}:
	}
	request := newDataRequest(session, ctx)
	session.mu.Lock()
	if err := session.ctx.Err(); err != nil {
		session.mu.Unlock()
		<-session.slots
		return nil, context.Cause(session.ctx)
	}
	session.requests[request.id] = request
	session.mu.Unlock()
	go request.watch()

	deadline := input.DeadlineAt.UTC()
	frame := &nodev1.SystemDataFrame{Payload: &nodev1.SystemDataFrame_Request{Request: &nodev1.SystemDataRequest{
		RequestId: request.id, Type: input.Type, Locator: input.Locator,
	}}}
	if !deadline.IsZero() {
		frame.GetRequest().DeadlineAt = timestamppb.New(deadline)
	}
	select {
	case <-ctx.Done():
		session.cancelRequest(request.id, ctx.Err(), true)
		return nil, ctx.Err()
	case <-session.ctx.Done():
		session.cancelRequest(request.id, context.Cause(session.ctx), false)
		return nil, context.Cause(session.ctx)
	case session.outgoing <- frame:
	}

	select {
	case <-ctx.Done():
		session.cancelRequest(request.id, ctx.Err(), true)
		return nil, ctx.Err()
	case <-session.ctx.Done():
		return nil, context.Cause(session.ctx)
	case <-request.ready:
		request.mu.Lock()
		defer request.mu.Unlock()
		if request.readyErr != nil {
			return nil, request.readyErr
		}
		return &Response{
			StatusCode: request.statusCode,
			Header:     request.header.Clone(),
			Body:       &responseBody{request: request},
		}, nil
	}
}

func (session *Session) Handle(frame *nodev1.NodeDataFrame) error {
	if frame == nil {
		return fmt.Errorf("%w: empty data frame", ErrProtocol)
	}
	switch payload := frame.Payload.(type) {
	case *nodev1.NodeDataFrame_Header:
		return session.handleHeader(payload.Header)
	case *nodev1.NodeDataFrame_Chunk:
		return session.handleChunk(payload.Chunk)
	case *nodev1.NodeDataFrame_End:
		return session.handleEnd(payload.End)
	case *nodev1.NodeDataFrame_Error:
		return session.handleError(payload.Error)
	default:
		return fmt.Errorf("%w: unsupported node data frame", ErrProtocol)
	}
}

func (session *Session) handleHeader(frame *nodev1.NodeDataHeader) error {
	if frame == nil {
		return fmt.Errorf("%w: empty response header", ErrProtocol)
	}
	request, err := session.request(frame.GetRequestId())
	if err != nil {
		return err
	}
	if frame.Status < 100 || frame.Status > 599 || frame.ContentLength < -1 {
		session.cancelRequest(request.id, fmt.Errorf("%w: invalid response header", ErrProtocol), true)
		return ErrProtocol
	}
	header := make(http.Header)
	for key, value := range frame.Headers {
		if strings.TrimSpace(key) != "" && !strings.ContainsAny(key, "\r\n") && !strings.ContainsAny(value, "\r\n") {
			header.Set(key, value)
		}
	}
	if frame.ContentType != "" {
		header.Set("Content-Type", frame.ContentType)
	}
	if frame.ContentDisposition != "" {
		header.Set("Content-Disposition", frame.ContentDisposition)
	}
	if frame.ContentLength >= 0 {
		header.Set("Content-Length", fmt.Sprintf("%d", frame.ContentLength))
	}

	request.mu.Lock()
	defer request.mu.Unlock()
	if request.headerSeen {
		return fmt.Errorf("%w: duplicate response header", ErrProtocol)
	}
	request.headerSeen = true
	request.statusCode = int(frame.Status)
	request.header = header
	request.contentLength = frame.ContentLength
	request.signalReadyLocked(nil)
	return nil
}

func (session *Session) handleChunk(frame *nodev1.NodeDataChunk) error {
	if frame == nil {
		return fmt.Errorf("%w: empty response chunk", ErrProtocol)
	}
	request, err := session.request(frame.GetRequestId())
	if err != nil {
		return err
	}
	request.mu.Lock()
	if !request.headerSeen || request.closed || frame.Sequence != request.nextSequence || len(frame.Data) == 0 || len(frame.Data) > session.config.MaxChunkSize {
		request.mu.Unlock()
		session.cancelRequest(request.id, fmt.Errorf("%w: invalid chunk sequence or size", ErrProtocol), true)
		return ErrProtocol
	}
	if request.totalBytes+uint64(len(frame.Data)) > uint64(session.config.MaxResponseBytes) {
		request.mu.Unlock()
		session.cancelRequest(request.id, ErrResponseTooLarge, true)
		return ErrResponseTooLarge
	}
	request.nextSequence++
	request.totalBytes += uint64(len(frame.Data))
	_, _ = request.hasher.Write(frame.Data)
	chunk := append([]byte(nil), frame.Data...)
	select {
	case request.chunks <- chunk:
		request.mu.Unlock()
		return nil
	default:
		request.mu.Unlock()
		session.cancelRequest(request.id, ErrRequestBacklog, true)
		return ErrRequestBacklog
	}
}

func (session *Session) handleEnd(frame *nodev1.NodeDataEnd) error {
	if frame == nil {
		return fmt.Errorf("%w: empty response end", ErrProtocol)
	}
	request, err := session.request(frame.GetRequestId())
	if err != nil {
		return err
	}
	request.mu.Lock()
	var terminal error
	if !request.headerSeen || request.closed || frame.ChecksumAlgorithm != "sha256" || frame.TotalBytes != request.totalBytes ||
		!bytes.Equal(frame.Checksum, request.hasher.Sum(nil)) || (request.contentLength >= 0 && uint64(request.contentLength) != request.totalBytes) {
		terminal = fmt.Errorf("%w: invalid response terminator", ErrProtocol)
	}
	request.closeLocked(terminal)
	request.mu.Unlock()
	session.unregister(request.id)
	return terminal
}

func (session *Session) handleError(frame *nodev1.NodeDataError) error {
	if frame == nil {
		return fmt.Errorf("%w: empty response error", ErrProtocol)
	}
	request, err := session.request(frame.GetRequestId())
	if err != nil {
		return err
	}
	remote := &RemoteError{StatusCode: int(frame.Status), Code: frame.Code, Message: frame.Message}
	request.mu.Lock()
	request.signalReadyLocked(remote)
	request.closeLocked(remote)
	request.mu.Unlock()
	session.unregister(request.id)
	return nil
}

func (session *Session) request(requestID string) (*dataRequest, error) {
	if strings.TrimSpace(requestID) == "" {
		return nil, ErrRequestNotFound
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	request, ok := session.requests[requestID]
	if !ok {
		return nil, ErrRequestNotFound
	}
	return request, nil
}

func (session *Session) cancelRequest(requestID string, cause error, notifyNode bool) {
	request, err := session.request(requestID)
	if err != nil {
		return
	}
	if cause == nil {
		cause = context.Canceled
	}
	request.mu.Lock()
	request.signalReadyLocked(cause)
	request.closeLocked(cause)
	request.mu.Unlock()
	session.unregister(requestID)
	if notifyNode {
		select {
		case session.outgoing <- &nodev1.SystemDataFrame{Payload: &nodev1.SystemDataFrame_Cancel{Cancel: &nodev1.CancelDataRequest{
			RequestId: requestID, Reason: cause.Error(),
		}}}:
		default:
		}
	}
}

func (session *Session) unregister(requestID string) {
	session.mu.Lock()
	if _, ok := session.requests[requestID]; ok {
		delete(session.requests, requestID)
		<-session.slots
	}
	session.mu.Unlock()
}

func (session *Session) failAll(cause error) {
	if cause == nil {
		cause = context.Canceled
	}
	session.mu.Lock()
	requests := make([]*dataRequest, 0, len(session.requests))
	for _, request := range session.requests {
		requests = append(requests, request)
	}
	session.requests = make(map[string]*dataRequest)
	for range requests {
		<-session.slots
	}
	session.mu.Unlock()
	for _, request := range requests {
		request.mu.Lock()
		request.signalReadyLocked(cause)
		request.closeLocked(cause)
		request.mu.Unlock()
	}
}

func (session *Session) close(cause error) {
	if cause == nil {
		cause = context.Canceled
	}
	session.cancel(cause)
}

type dataRequest struct {
	id      string
	session *Session
	ctx     context.Context
	ready   chan struct{}
	done    chan struct{}
	chunks  chan []byte
	hasher  hashWriter

	mu            sync.Mutex
	readySignaled bool
	readyErr      error
	headerSeen    bool
	statusCode    int
	header        http.Header
	contentLength int64
	nextSequence  uint64
	totalBytes    uint64
	closed        bool
	terminalErr   error
}

type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

func newDataRequest(session *Session, ctx context.Context) *dataRequest {
	return &dataRequest{
		id: uuid.NewString(), session: session, ctx: ctx, ready: make(chan struct{}),
		done: make(chan struct{}), chunks: make(chan []byte, session.config.RequestBuffer), hasher: sha256.New(),
		contentLength: -1, nextSequence: 1,
	}
}

func (request *dataRequest) watch() {
	select {
	case <-request.ctx.Done():
		request.session.cancelRequest(request.id, request.ctx.Err(), true)
	case <-request.session.ctx.Done():
	case <-request.done:
	}
}

func (request *dataRequest) signalReadyLocked(err error) {
	if request.readySignaled {
		return
	}
	request.readySignaled = true
	request.readyErr = err
	close(request.ready)
}

func (request *dataRequest) closeLocked(err error) {
	if request.closed {
		return
	}
	request.closed = true
	request.terminalErr = err
	close(request.chunks)
	close(request.done)
}

type responseBody struct {
	request *dataRequest
	current []byte
	once    sync.Once
}

func (body *responseBody) Read(buffer []byte) (int, error) {
	for len(body.current) == 0 {
		chunk, ok := <-body.request.chunks
		if !ok {
			body.request.mu.Lock()
			err := body.request.terminalErr
			body.request.mu.Unlock()
			if err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		body.current = chunk
	}
	count := copy(buffer, body.current)
	body.current = body.current[count:]
	return count, nil
}

func (body *responseBody) Close() error {
	body.once.Do(func() {
		body.request.session.cancelRequest(body.request.id, context.Canceled, true)
	})
	return nil
}

type RemoteError struct {
	StatusCode int
	Code       string
	Message    string
}

func (err *RemoteError) Error() string {
	return fmt.Sprintf("node data request failed: status=%d code=%s message=%s", err.StatusCode, err.Code, err.Message)
}

package nodedata

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	nodecontract "github.com/ticket/email-node-contract"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
)

var ErrSessionClosed = errors.New("node data dispatcher session is closed")

type Response struct {
	StatusCode    int
	Header        http.Header
	ContentLength int64
	Body          io.ReadCloser
}

type Handler func(context.Context, *nodev1.SystemDataRequest) (*Response, error)
type EmitFunc func(context.Context, *nodev1.NodeDataFrame) error

type Dispatcher struct {
	handler Handler
}

func NewDispatcher(handler Handler) (*Dispatcher, error) {
	if handler == nil {
		return nil, errors.New("node data handler is required")
	}
	return &Dispatcher{handler: handler}, nil
}

func (dispatcher *Dispatcher) NewSession(parent context.Context, maxConcurrency, maxChunkSize int, emit EmitFunc) (*Session, error) {
	if maxConcurrency <= 0 || maxConcurrency > 1024 {
		return nil, errors.New("invalid data stream concurrency limit")
	}
	if maxChunkSize <= 0 || maxChunkSize > nodecontract.MaxDataChunkSize {
		return nil, errors.New("invalid data stream chunk size")
	}
	if emit == nil {
		return nil, errors.New("data stream emitter is required")
	}
	ctx, cancel := context.WithCancel(parent)
	return &Session{
		ctx: ctx, cancel: cancel, handler: dispatcher.handler, emit: emit,
		maxChunkSize: maxChunkSize, slots: make(chan struct{}, maxConcurrency), active: make(map[string]*activeRequest),
	}, nil
}

type Session struct {
	ctx          context.Context
	cancel       context.CancelFunc
	handler      Handler
	emit         EmitFunc
	maxChunkSize int
	slots        chan struct{}

	mu     sync.Mutex
	active map[string]*activeRequest
	wg     sync.WaitGroup
}

type activeRequest struct {
	cancel    context.CancelFunc
	body      io.Closer
	closeOnce sync.Once
}

func (active *activeRequest) closeBody(body io.Closer) {
	if body != nil {
		active.closeOnce.Do(func() { _ = body.Close() })
	}
}

func (session *Session) Accept(request *nodev1.SystemDataRequest) error {
	if request == nil || strings.TrimSpace(request.RequestId) == "" || strings.TrimSpace(request.Type) == "" || request.Locator == nil {
		return session.emitError(context.Background(), requestID(request), http.StatusBadRequest, "invalid_request", "request ID, type, and locator are required")
	}
	requestContext := session.ctx
	cancel := func() {}
	if request.DeadlineAt != nil {
		if request.DeadlineAt.CheckValid() != nil {
			return session.emitError(context.Background(), request.RequestId, http.StatusBadRequest, "invalid_deadline", "request deadline is invalid")
		}
		requestContext, cancel = context.WithDeadline(session.ctx, request.DeadlineAt.AsTime())
	} else {
		requestContext, cancel = context.WithCancel(session.ctx)
	}

	session.mu.Lock()
	if session.ctx.Err() != nil {
		session.mu.Unlock()
		cancel()
		return ErrSessionClosed
	}
	if _, exists := session.active[request.RequestId]; exists {
		session.mu.Unlock()
		cancel()
		return session.emitError(context.Background(), request.RequestId, http.StatusConflict, "duplicate_request", "request ID is already active")
	}
	active := &activeRequest{cancel: cancel}
	session.active[request.RequestId] = active
	session.mu.Unlock()

	session.wg.Add(1)
	go session.execute(requestContext, request, active)
	return nil
}

func (session *Session) Cancel(requestID string) bool {
	session.mu.Lock()
	active, ok := session.active[requestID]
	var body io.Closer
	if ok {
		body = active.body
	}
	session.mu.Unlock()
	if ok {
		active.cancel()
		active.closeBody(body)
	}
	return ok
}

func (session *Session) Close() {
	session.cancel()
	type activeBody struct {
		request *activeRequest
		body    io.Closer
	}
	session.mu.Lock()
	activeRequests := make([]activeBody, 0, len(session.active))
	for _, active := range session.active {
		activeRequests = append(activeRequests, activeBody{request: active, body: active.body})
	}
	session.mu.Unlock()
	for _, active := range activeRequests {
		active.request.cancel()
		active.request.closeBody(active.body)
	}
	session.wg.Wait()
}

func (session *Session) execute(ctx context.Context, request *nodev1.SystemDataRequest, active *activeRequest) {
	defer session.wg.Done()
	defer active.cancel()
	defer session.remove(request.RequestId)

	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			_ = session.emitError(context.Background(), request.RequestId, http.StatusRequestTimeout, "deadline_exceeded", ctx.Err().Error())
		}
		return
	case session.slots <- struct{}{}:
	}
	defer func() { <-session.slots }()

	response, err := session.handler(ctx, request)
	if err != nil {
		if ctx.Err() == nil {
			_ = session.emitError(ctx, request.RequestId, http.StatusInternalServerError, "open_failed", err.Error())
		}
		return
	}
	if response == nil || response.StatusCode < 100 || response.StatusCode > 599 || response.Body == nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		_ = session.emitError(ctx, request.RequestId, http.StatusInternalServerError, "invalid_response", "data handler returned an invalid response")
		return
	}
	defer active.closeBody(response.Body)
	session.mu.Lock()
	if current, ok := session.active[request.RequestId]; ok && current == active {
		active.body = response.Body
	}
	session.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return
	}
	readFinished := make(chan struct{})
	defer close(readFinished)
	go func() {
		select {
		case <-ctx.Done():
			active.closeBody(response.Body)
		case <-readFinished:
		}
	}()
	if response.Header == nil {
		response.Header = make(http.Header)
	}

	contentLength := response.ContentLength
	if contentLength < 0 && response.Header != nil {
		if value := response.Header.Get("Content-Length"); value != "" {
			if parsed, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil && parsed >= 0 {
				contentLength = parsed
			}
		}
	}
	header := make(map[string]string)
	for key, values := range response.Header {
		if len(values) > 0 && strings.TrimSpace(key) != "" && !strings.ContainsAny(key, "\r\n") && !strings.ContainsAny(values[0], "\r\n") {
			header[key] = values[0]
		}
	}
	if err := session.emit(ctx, &nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Header{Header: &nodev1.NodeDataHeader{
		RequestId: request.RequestId, Status: int32(response.StatusCode),
		ContentType: response.Header.Get("Content-Type"), ContentLength: contentLength,
		ContentDisposition: response.Header.Get("Content-Disposition"), Headers: header,
	}}}); err != nil {
		return
	}

	hasher := sha256.New()
	buffer := make([]byte, session.maxChunkSize)
	sequence := uint64(1)
	total := uint64(0)
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			chunk := append([]byte(nil), buffer[:count]...)
			_, _ = hasher.Write(chunk)
			total += uint64(count)
			if err := session.emit(ctx, &nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Chunk{Chunk: &nodev1.NodeDataChunk{
				RequestId: request.RequestId, Sequence: sequence, Data: chunk,
			}}}); err != nil {
				return
			}
			sequence++
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			if ctx.Err() == nil {
				_ = session.emitError(ctx, request.RequestId, http.StatusBadGateway, "read_failed", readErr.Error())
			}
			return
		}
		if count == 0 {
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}
	_ = session.emit(ctx, &nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_End{End: &nodev1.NodeDataEnd{
		RequestId: request.RequestId, ChecksumAlgorithm: "sha256", Checksum: hasher.Sum(nil), TotalBytes: total,
	}}})
}

func (session *Session) emitError(ctx context.Context, requestID string, status int, code, message string) error {
	return session.emit(ctx, &nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Error{Error: &nodev1.NodeDataError{
		RequestId: requestID, Status: int32(status), Code: code, Message: message,
	}}})
}

func (session *Session) remove(requestID string) {
	session.mu.Lock()
	delete(session.active, requestID)
	session.mu.Unlock()
}

func requestID(request *nodev1.SystemDataRequest) string {
	if request == nil {
		return ""
	}
	return request.RequestId
}

func ErrorResponse(status int, code, message string) *Response {
	body := io.NopCloser(strings.NewReader(fmt.Sprintf(`{"code":%q,"message":%q}`, code, message)))
	return &Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, ContentLength: -1, Body: body}
}

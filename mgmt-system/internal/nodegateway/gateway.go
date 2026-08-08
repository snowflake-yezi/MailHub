package nodegateway

import (
	"context"
	"errors"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodedata"
	"github.com/ticket/email-mgmt-system/internal/nodesession"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const currentProtocolVersion uint32 = 1

type Principal struct {
	ServerID            uint64
	NodeUUID            string
	CredentialID        uint64
	CredentialVersion   uint64
	CredentialExpiresAt *time.Time
}

type AuthenticateFunc func(rawCredential, nodeUUID string, usedAt time.Time) (Principal, error)

type StateStore interface {
	GetServer(uint64) (*model.MailServer, error)
	UpdateNodeSessionConnected(uint64, time.Time, string, uint32, []string, string, time.Time, uint64) error
	UpdateNodeControlHeartbeat(uint64, time.Time, int, string, uint64, string, string) error
	UpdateNodeConfigApplied(uint64, uint64, bool, string) error
	MarkNodeSessionDisconnected(uint64, time.Time) error
	ExpireNodeControlLeases(time.Time) error
	RecordNodeSessionAudit(string, uint64, string, string, map[string]any) error
}

type Config struct {
	SupportedProtocolVersions []uint32
	HeartbeatInterval         time.Duration
	LeaseTimeout              time.Duration
	Now                       func() time.Time
	Commands                  CommandManager
	DataSessions              *nodedata.Registry
}

type CommandManager interface {
	NextSequence(uint64) (uint64, error)
	DispatchPending(context.Context, uint64) error
	Received(uint64, *nodev1.CommandReceived) error
	Started(uint64, *nodev1.CommandStarted) error
	Result(uint64, *nodev1.CommandResult) error
}

type Gateway struct {
	nodev1.UnimplementedNodeGatewayServer

	store        StateStore
	sessions     *nodesession.Registry
	dataSessions *nodedata.Registry
	authenticate AuthenticateFunc
	config       Config
}

func New(store StateStore, sessions *nodesession.Registry, authenticate AuthenticateFunc, config Config) (*Gateway, error) {
	if store == nil || sessions == nil || authenticate == nil {
		return nil, errors.New("node gateway store, sessions, and authenticator are required")
	}
	if len(config.SupportedProtocolVersions) == 0 {
		config.SupportedProtocolVersions = []uint32{currentProtocolVersion}
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 30 * time.Second
	}
	if config.LeaseTimeout <= config.HeartbeatInterval {
		config.LeaseTimeout = 3 * config.HeartbeatInterval
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.DataSessions == nil {
		config.DataSessions = nodedata.NewRegistry(nodedata.Config{})
	}
	return &Gateway{
		store: store, sessions: sessions, dataSessions: config.DataSessions,
		authenticate: authenticate, config: config,
	}, nil
}

func (gateway *Gateway) Data(stream grpc.BidiStreamingServer[nodev1.NodeDataFrame, nodev1.SystemDataFrame]) error {
	principal, sourceIP, err := gateway.authenticateStream(stream.Context())
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "receive DataStreamHello: %v", err)
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.FailedPrecondition, "DataStreamHello must be the first data frame")
	}
	controlSession, ok := gateway.sessions.Get(principal.ServerID)
	if !ok {
		return status.Error(codes.FailedPrecondition, "an active ControlStream is required")
	}
	if hello.NodeUuid != principal.NodeUUID || hello.NodeUuid != controlSession.NodeUUID ||
		principal.CredentialID != controlSession.CredentialID ||
		hello.BootId != controlSession.BootID || hello.ControlSessionId != controlSession.ID ||
		hello.ProtocolVersion == 0 || hello.ProtocolVersion != controlSession.Protocol {
		return status.Error(codes.PermissionDenied, "data stream identity does not match the active control session")
	}

	session := gateway.dataSessions.Register(controlSession.Context(), nodedata.RegisterInput{
		ServerID: principal.ServerID, NodeUUID: principal.NodeUUID, BootID: hello.BootId,
		ControlSessionID: controlSession.ID, Protocol: hello.ProtocolVersion,
	})
	dataConfig := gateway.dataSessions.Config()
	welcome := &nodev1.SystemDataFrame{Payload: &nodev1.SystemDataFrame_Welcome{Welcome: &nodev1.DataStreamWelcome{
		DataSessionId: session.ID, MaxConcurrency: uint32(dataConfig.MaxConcurrency),
		MaxChunkSize: uint32(dataConfig.MaxChunkSize), ServerTime: timestamppb.New(gateway.now()),
	}}}
	if err := stream.Send(welcome); err != nil {
		gateway.dataSessions.Remove(session.ServerID, session.ID)
		return err
	}
	_ = gateway.store.RecordNodeSessionAudit("data_session.connect", principal.ServerID, principal.NodeUUID, sourceIP, map[string]any{
		"data_session_id": session.ID, "control_session_id": controlSession.ID,
	})

	err = gateway.runDataSession(stream, session)
	gateway.dataSessions.Remove(session.ServerID, session.ID)
	reason := "stream_closed"
	if err != nil {
		reason = status.Code(err).String()
	}
	_ = gateway.store.RecordNodeSessionAudit("data_session.disconnect", principal.ServerID, principal.NodeUUID, sourceIP, map[string]any{
		"data_session_id": session.ID, "control_session_id": controlSession.ID, "reason": reason,
	})
	return err
}

func (gateway *Gateway) runDataSession(stream grpc.BidiStreamingServer[nodev1.NodeDataFrame, nodev1.SystemDataFrame], session *nodedata.Session) error {
	type receiveResult struct {
		frame *nodev1.NodeDataFrame
		err   error
	}
	received := make(chan receiveResult, 1)
	go func() {
		for {
			frame, err := stream.Recv()
			select {
			case received <- receiveResult{frame: frame, err: err}:
			case <-session.Context().Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-session.Context().Done():
			if errors.Is(context.Cause(session.Context()), nodesession.ErrCredentialExpired) {
				return status.Error(codes.Unauthenticated, nodesession.ErrCredentialExpired.Error())
			}
			return status.Error(codes.Aborted, context.Cause(session.Context()).Error())
		case frame := <-session.Outgoing():
			if err := stream.Send(frame); err != nil {
				return err
			}
		case result := <-received:
			if result.err != nil {
				if errors.Is(result.err, io.EOF) {
					return nil
				}
				return result.err
			}
			switch payload := result.frame.Payload.(type) {
			case *nodev1.NodeDataFrame_Ping:
				if err := stream.Send(&nodev1.SystemDataFrame{Payload: &nodev1.SystemDataFrame_Pong{
					Pong: &nodev1.Pong{Nonce: payload.Ping.Nonce},
				}}); err != nil {
					return err
				}
			case *nodev1.NodeDataFrame_Pong:
			case *nodev1.NodeDataFrame_Hello:
				return status.Error(codes.FailedPrecondition, "DataStreamHello is only allowed as the first frame")
			default:
				handleErr := session.Handle(result.frame)
				if handleErr == nil || errors.Is(handleErr, nodedata.ErrRequestNotFound) ||
					errors.Is(handleErr, nodedata.ErrRequestBacklog) || errors.Is(handleErr, nodedata.ErrResponseTooLarge) {
					continue
				}
				return status.Errorf(codes.FailedPrecondition, "handle node data frame: %v", handleErr)
			}
		}
	}
}

func (gateway *Gateway) Control(stream grpc.BidiStreamingServer[nodev1.NodeControlFrame, nodev1.SystemControlFrame]) error {
	principal, sourceIP, err := gateway.authenticateStream(stream.Context())
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "receive Hello: %v", err)
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.FailedPrecondition, "Hello must be the first control frame")
	}
	protocol, startedAt, capabilities, err := gateway.validateHello(principal, hello)
	if err != nil {
		return err
	}
	server, err := gateway.store.GetServer(principal.ServerID)
	if err != nil {
		return status.Error(codes.NotFound, "authenticated node server not found")
	}
	if hello.DesiredRevision > server.DesiredRevision || hello.AppliedRevision > hello.DesiredRevision {
		return status.Error(codes.FailedPrecondition, "node revision is ahead of management desired revision")
	}
	appliedRevision := hello.AppliedRevision
	if appliedRevision < server.AppliedRevision {
		appliedRevision = server.AppliedRevision
	}

	now := gateway.now()
	session := gateway.sessions.Register(stream.Context(), nodesession.RegisterInput{
		ServerID: principal.ServerID, NodeUUID: principal.NodeUUID, BootID: hello.BootId,
		Protocol: protocol, AgentVersion: hello.AgentVersion, Capabilities: capabilities, ConnectedAt: now,
		CredentialID: principal.CredentialID, CredentialVersion: principal.CredentialVersion,
		CredentialExpiresAt: principal.CredentialExpiresAt,
	})
	leaseExpiresAt := now.Add(gateway.config.LeaseTimeout)
	if err := gateway.store.UpdateNodeSessionConnected(principal.ServerID, leaseExpiresAt, hello.AgentVersion, protocol, capabilities, hello.BootId, startedAt, appliedRevision); err != nil {
		gateway.sessions.Remove(principal.ServerID, session.ID)
		return status.Errorf(codes.Internal, "record connected session: %v", err)
	}
	_ = gateway.store.RecordNodeSessionAudit("session.connect", principal.ServerID, principal.NodeUUID, sourceIP, map[string]any{
		"session_id": session.ID, "boot_id": hello.BootId, "protocol_version": protocol,
	})

	nextCommandSequence := uint64(1)
	if gateway.config.Commands != nil {
		nextCommandSequence, err = gateway.config.Commands.NextSequence(principal.ServerID)
		if err != nil {
			gateway.disconnect(session, sourceIP, err)
			return status.Errorf(codes.Internal, "resolve next command sequence: %v", err)
		}
	}
	welcome := &nodev1.SystemControlFrame{
		FrameId: session.ID + ":welcome", SentAt: timestamppb.New(now),
		Payload: &nodev1.SystemControlFrame_Welcome{Welcome: &nodev1.Welcome{
			SessionId: session.ID, ProtocolVersion: protocol,
			HeartbeatIntervalSeconds: durationSeconds(gateway.config.HeartbeatInterval),
			ServerTime:               timestamppb.New(now), DesiredRevision: server.DesiredRevision,
			NextCommandSequence: nextCommandSequence,
		}},
	}
	if err := stream.Send(welcome); err != nil {
		gateway.disconnect(session, sourceIP, err)
		return err
	}

	err = gateway.runControlSession(stream, session)
	gateway.disconnect(session, sourceIP, err)
	return err
}

func (gateway *Gateway) runControlSession(stream grpc.BidiStreamingServer[nodev1.NodeControlFrame, nodev1.SystemControlFrame], session *nodesession.Session) error {
	type receiveResult struct {
		frame *nodev1.NodeControlFrame
		err   error
	}
	received := make(chan receiveResult, 1)
	dispatchErrors := make(chan error, 1)
	go func() {
		for {
			frame, err := stream.Recv()
			select {
			case received <- receiveResult{frame: frame, err: err}:
			case <-session.Context().Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	if gateway.config.Commands != nil {
		go func() {
			dispatchErrors <- gateway.config.Commands.DispatchPending(session.Context(), session.ServerID)
		}()
	}

	leaseTimer := time.NewTimer(gateway.config.LeaseTimeout)
	defer leaseTimer.Stop()
	var credentialExpiry <-chan time.Time
	var credentialTimer *time.Timer
	if session.CredentialExpiresAt != nil {
		remaining := session.CredentialExpiresAt.Sub(gateway.now())
		if remaining <= 0 {
			return status.Error(codes.Unauthenticated, "node credential rotation overlap expired")
		}
		credentialTimer = time.NewTimer(remaining)
		credentialExpiry = credentialTimer.C
		defer credentialTimer.Stop()
	}
	for {
		select {
		case <-session.Context().Done():
			if errors.Is(context.Cause(session.Context()), nodesession.ErrCredentialExpired) {
				return status.Error(codes.Unauthenticated, nodesession.ErrCredentialExpired.Error())
			}
			return status.Error(codes.Aborted, context.Cause(session.Context()).Error())
		case <-leaseTimer.C:
			return status.Error(codes.DeadlineExceeded, "node heartbeat lease expired")
		case <-credentialExpiry:
			return status.Error(codes.Unauthenticated, "node credential rotation overlap expired")
		case dispatchErr := <-dispatchErrors:
			if dispatchErr != nil && !errors.Is(dispatchErr, nodesession.ErrSessionNotFound) && !errors.Is(dispatchErr, context.Canceled) {
				return status.Errorf(codes.Internal, "dispatch pending commands: %v", dispatchErr)
			}
			dispatchErrors = nil
		case frame := <-session.Outgoing():
			if err := stream.Send(frame); err != nil {
				return err
			}
		case result := <-received:
			if result.err != nil {
				if errors.Is(result.err, io.EOF) {
					return nil
				}
				return result.err
			}
			heartbeat, response, err := gateway.handleNodeFrame(session, result.frame)
			if err != nil {
				return err
			}
			if response != nil {
				if err := stream.Send(response); err != nil {
					return err
				}
			}
			if heartbeat {
				if !leaseTimer.Stop() {
					select {
					case <-leaseTimer.C:
					default:
					}
				}
				leaseTimer.Reset(gateway.config.LeaseTimeout)
			}
		}
	}
}

func (gateway *Gateway) handleNodeFrame(session *nodesession.Session, frame *nodev1.NodeControlFrame) (bool, *nodev1.SystemControlFrame, error) {
	if frame == nil {
		return false, nil, status.Error(codes.InvalidArgument, "empty control frame")
	}
	switch payload := frame.Payload.(type) {
	case *nodev1.NodeControlFrame_Heartbeat:
		heartbeat := payload.Heartbeat
		if heartbeat.NodeUuid != session.NodeUUID || heartbeat.BootId != session.BootID || heartbeat.SessionId != session.ID {
			return false, nil, status.Error(codes.PermissionDenied, "heartbeat identity does not match active session")
		}
		readiness, err := readinessString(heartbeat.Readiness)
		if err != nil {
			return false, nil, err
		}
		server, err := gateway.store.GetServer(session.ServerID)
		if err != nil {
			return false, nil, status.Error(codes.NotFound, "node server not found")
		}
		if heartbeat.AppliedRevision > heartbeat.DesiredRevision || heartbeat.DesiredRevision > server.DesiredRevision {
			return false, nil, status.Error(codes.FailedPrecondition, "invalid heartbeat revision")
		}
		appliedRevision := heartbeat.AppliedRevision
		if appliedRevision < server.AppliedRevision {
			appliedRevision = server.AppliedRevision
		}
		load := int(heartbeat.MailboxCount)
		if heartbeat.MailboxCount > uint64(math.MaxInt) {
			load = math.MaxInt
		}
		if err := gateway.store.UpdateNodeControlHeartbeat(session.ServerID, gateway.now().Add(gateway.config.LeaseTimeout), load, readiness, appliedRevision, heartbeat.LastApplyError, session.BootID); err != nil {
			return false, nil, status.Errorf(codes.Internal, "record heartbeat: %v", err)
		}
		return true, nil, nil
	case *nodev1.NodeControlFrame_ConfigApplied:
		applied := payload.ConfigApplied
		server, err := gateway.store.GetServer(session.ServerID)
		if err != nil || applied.Revision > server.DesiredRevision {
			return false, nil, status.Error(codes.FailedPrecondition, "invalid applied config revision")
		}
		if applied.Revision < server.AppliedRevision {
			return false, nil, nil
		}
		if err := gateway.store.UpdateNodeConfigApplied(session.ServerID, applied.Revision, applied.Succeeded, applied.ErrorMessage); err != nil {
			return false, nil, status.Errorf(codes.Internal, "record applied config: %v", err)
		}
		return false, nil, nil
	case *nodev1.NodeControlFrame_CommandReceived:
		if gateway.config.Commands == nil {
			return false, nil, status.Error(codes.Unimplemented, "command acknowledgements are not enabled")
		}
		if err := gateway.config.Commands.Received(session.ServerID, payload.CommandReceived); err != nil {
			return false, nil, status.Errorf(codes.FailedPrecondition, "record command received: %v", err)
		}
		return false, nil, nil
	case *nodev1.NodeControlFrame_CommandStarted:
		if gateway.config.Commands == nil {
			return false, nil, status.Error(codes.Unimplemented, "command acknowledgements are not enabled")
		}
		if err := gateway.config.Commands.Started(session.ServerID, payload.CommandStarted); err != nil {
			return false, nil, status.Errorf(codes.FailedPrecondition, "record command started: %v", err)
		}
		return false, nil, nil
	case *nodev1.NodeControlFrame_CommandResult:
		if gateway.config.Commands == nil {
			return false, nil, status.Error(codes.Unimplemented, "command results are not enabled")
		}
		if err := gateway.config.Commands.Result(session.ServerID, payload.CommandResult); err != nil {
			return false, nil, status.Errorf(codes.FailedPrecondition, "record command result: %v", err)
		}
		return false, nil, nil
	case *nodev1.NodeControlFrame_Ping:
		now := gateway.now()
		return false, &nodev1.SystemControlFrame{
			FrameId: frame.FrameId + ":pong", SentAt: timestamppb.New(now),
			Payload: &nodev1.SystemControlFrame_Pong{Pong: &nodev1.Pong{Nonce: payload.Ping.Nonce}},
		}, nil
	case *nodev1.NodeControlFrame_Pong, *nodev1.NodeControlFrame_NodeEvent:
		return false, nil, nil
	case *nodev1.NodeControlFrame_Hello:
		return false, nil, status.Error(codes.FailedPrecondition, "Hello is only allowed as the first frame")
	default:
		return false, nil, status.Error(codes.Unimplemented, "unsupported node control frame")
	}
}

func (gateway *Gateway) authenticateStream(ctx context.Context) (Principal, string, error) {
	values, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return Principal{}, "", status.Error(codes.Unauthenticated, "node authentication metadata is required")
	}
	authorization := strings.TrimSpace(firstMetadata(values, "authorization"))
	nodeUUID := strings.TrimSpace(firstMetadata(values, "x-mailhub-node-uuid"))
	const scheme = "Node "
	if !strings.HasPrefix(authorization, scheme) || strings.TrimSpace(strings.TrimPrefix(authorization, scheme)) == "" || nodeUUID == "" {
		return Principal{}, "", status.Error(codes.Unauthenticated, "expected Node credential and node UUID metadata")
	}
	principal, err := gateway.authenticate(strings.TrimSpace(strings.TrimPrefix(authorization, scheme)), nodeUUID, gateway.now())
	if err != nil || principal.ServerID == 0 || principal.NodeUUID != nodeUUID {
		return Principal{}, "", status.Error(codes.Unauthenticated, "invalid or revoked node credential")
	}
	sourceIP := ""
	if remote, ok := peer.FromContext(ctx); ok && remote.Addr != nil {
		sourceIP = remote.Addr.String()
	}
	return principal, sourceIP, nil
}

func (gateway *Gateway) validateHello(principal Principal, hello *nodev1.Hello) (uint32, time.Time, []string, error) {
	if hello.NodeUuid != principal.NodeUUID || strings.TrimSpace(hello.BootId) == "" || len(hello.BootId) > 64 || len(hello.AgentVersion) > 64 {
		return 0, time.Time{}, nil, status.Error(codes.PermissionDenied, "Hello identity does not match authenticated node")
	}
	if hello.StartedAt == nil || hello.StartedAt.CheckValid() != nil {
		return 0, time.Time{}, nil, status.Error(codes.InvalidArgument, "valid node started_at is required")
	}
	protocol, ok := selectProtocol(gateway.config.SupportedProtocolVersions, hello.SupportedProtocolVersions)
	if !ok {
		return 0, time.Time{}, nil, status.Error(codes.FailedPrecondition, "no mutually supported node protocol version")
	}
	if len(hello.Capabilities) > 64 {
		return 0, time.Time{}, nil, status.Error(codes.InvalidArgument, "too many node capabilities")
	}
	capabilitySet := make(map[string]struct{}, len(hello.Capabilities))
	for _, capability := range hello.Capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" || len(capability) > 128 {
			return 0, time.Time{}, nil, status.Error(codes.InvalidArgument, "invalid node capability")
		}
		capabilitySet[capability] = struct{}{}
	}
	capabilities := make([]string, 0, len(capabilitySet))
	for capability := range capabilitySet {
		capabilities = append(capabilities, capability)
	}
	sort.Strings(capabilities)
	return protocol, hello.StartedAt.AsTime().UTC(), capabilities, nil
}

func (gateway *Gateway) disconnect(session *nodesession.Session, sourceIP string, sessionErr error) {
	removed := gateway.sessions.Remove(session.ServerID, session.ID)
	if !removed {
		if current, ok := gateway.sessions.Get(session.ServerID); ok && current.ID != session.ID {
			return
		}
	}
	now := gateway.now()
	_ = gateway.store.MarkNodeSessionDisconnected(session.ServerID, now)
	reason := "stream_closed"
	if sessionErr != nil {
		reason = status.Code(sessionErr).String()
	}
	_ = gateway.store.RecordNodeSessionAudit("session.disconnect", session.ServerID, session.NodeUUID, sourceIP, map[string]any{
		"session_id": session.ID, "reason": reason,
	})
}

func (gateway *Gateway) ReapExpiredLeases(ctx context.Context) {
	interval := gateway.config.HeartbeatInterval
	if interval > gateway.config.LeaseTimeout/2 {
		interval = gateway.config.LeaseTimeout / 2
	}
	if interval <= 0 {
		interval = time.Second
	}
	_ = gateway.store.ExpireNodeControlLeases(gateway.now())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = gateway.store.ExpireNodeControlLeases(gateway.now())
		}
	}
}

func (gateway *Gateway) now() time.Time { return gateway.config.Now().UTC() }

func firstMetadata(values metadata.MD, key string) string {
	items := values.Get(key)
	if len(items) == 0 {
		return ""
	}
	return items[0]
}

func selectProtocol(supported, offered []uint32) (uint32, bool) {
	set := make(map[uint32]struct{}, len(supported))
	for _, version := range supported {
		if version > 0 {
			set[version] = struct{}{}
		}
	}
	var selected uint32
	for _, version := range offered {
		if _, ok := set[version]; ok && version > selected {
			selected = version
		}
	}
	return selected, selected > 0
}

func readinessString(value nodev1.ReadinessState) (string, error) {
	switch value {
	case nodev1.ReadinessState_READINESS_STATE_READY:
		return model.ReadinessReady, nil
	case nodev1.ReadinessState_READINESS_STATE_DEGRADED:
		return model.ReadinessDegraded, nil
	case nodev1.ReadinessState_READINESS_STATE_FAILED:
		return model.ReadinessFailed, nil
	case nodev1.ReadinessState_READINESS_STATE_UNKNOWN:
		return model.ReadinessUnknown, nil
	default:
		return "", status.Error(codes.InvalidArgument, "heartbeat readiness is required")
	}
}

func durationSeconds(value time.Duration) uint32 {
	seconds := value / time.Second
	if seconds < 1 {
		return 1
	}
	if seconds > time.Duration(math.MaxUint32) {
		return math.MaxUint32
	}
	return uint32(seconds)
}

func (principal Principal) String() string {
	return strconv.FormatUint(principal.ServerID, 10) + ":" + principal.NodeUUID
}

var _ nodev1.NodeGatewayServer = (*Gateway)(nil)

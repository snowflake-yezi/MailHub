package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"time"

	nodecommand "github.com/ticket/email-mail-node/internal/command"
	"github.com/ticket/email-mail-node/internal/nodedata"
	nodecontract "github.com/ticket/email-node-contract"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultReconnectMin = time.Second
	defaultReconnectMax = 30 * time.Second
)

type ComponentHealth struct {
	Component string
	State     nodev1.ReadinessState
	Detail    string
	CheckedAt time.Time
}

type HealthSnapshot struct {
	MailboxCount       uint64
	DiskTotalBytes     uint64
	DiskAvailableBytes uint64
	Readiness          nodev1.ReadinessState
	Components         []ComponentHealth
	LastApplyError     string
}

type RevisionFunc func() (desired, applied uint64)
type ConfigRevisionFunc func(context.Context, uint64) (uint64, error)
type FilterRevisionFunc func(context.Context, uint64) error
type DialFunc func(context.Context) (*grpc.ClientConn, error)

type Config struct {
	Address                   string
	CAFile                    string
	NodeUUID                  string
	Credential                string
	BootID                    string
	StartedAt                 time.Time
	AgentVersion              string
	SupportedProtocolVersions []uint32
	Capabilities              []string
	ReconnectMin              time.Duration
	ReconnectMax              time.Duration
	Dial                      DialFunc
	Revisions                 RevisionFunc
	Snapshot                  func() HealthSnapshot
	OnConfigRevision          ConfigRevisionFunc
	OnFilterRevision          FilterRevisionFunc
	Commands                  *nodecommand.Dispatcher
	Data                      *nodedata.Dispatcher
	Logf                      func(string, ...any)
	Rand                      *rand.Rand
}

type Agent struct {
	config Config
}

type configApplyResult struct {
	revision uint64
	err      error
}

func New(config Config) (*Agent, error) {
	if strings.TrimSpace(config.Address) == "" || strings.TrimSpace(config.NodeUUID) == "" ||
		strings.TrimSpace(config.Credential) == "" || strings.TrimSpace(config.BootID) == "" {
		return nil, errors.New("control address, node UUID, credential, and boot ID are required")
	}
	if config.StartedAt.IsZero() {
		return nil, errors.New("node started_at is required")
	}
	if len(config.SupportedProtocolVersions) == 0 {
		config.SupportedProtocolVersions = []uint32{1}
	}
	if config.ReconnectMin <= 0 {
		config.ReconnectMin = defaultReconnectMin
	}
	if config.ReconnectMax < config.ReconnectMin {
		config.ReconnectMax = defaultReconnectMax
	}
	if config.Dial == nil {
		dial, err := TLSDialer(config.Address, config.CAFile, config.AgentVersion)
		if err != nil {
			return nil, err
		}
		config.Dial = dial
	}
	if config.Revisions == nil {
		config.Revisions = func() (uint64, uint64) { return 0, 0 }
	}
	if config.Snapshot == nil {
		config.Snapshot = func() HealthSnapshot {
			return HealthSnapshot{Readiness: nodev1.ReadinessState_READINESS_STATE_UNKNOWN}
		}
	}
	if config.Logf == nil {
		config.Logf = func(string, ...any) {}
	}
	if config.Rand == nil {
		config.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &Agent{config: config}, nil
}

func (agent *Agent) Run(ctx context.Context) {
	backoff := agent.config.ReconnectMin
	for {
		connected, err := agent.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			agent.config.Logf("[control] session ended: %v", err)
		}
		if connected {
			backoff = agent.config.ReconnectMin
		}
		delay := backoff + agent.jitter(backoff/5)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < agent.config.ReconnectMax {
			backoff *= 2
			if backoff > agent.config.ReconnectMax {
				backoff = agent.config.ReconnectMax
			}
		}
	}
}

func (agent *Agent) runOnce(ctx context.Context) (bool, error) {
	sessionContext, cancelSession := context.WithCancel(ctx)
	defer cancelSession()
	conn, err := agent.config.Dial(sessionContext)
	if err != nil {
		return false, fmt.Errorf("dial control gateway: %w", err)
	}
	defer conn.Close()

	streamContext := metadata.NewOutgoingContext(sessionContext, metadata.Pairs(
		"authorization", "Node "+agent.config.Credential,
		"x-mailhub-node-uuid", agent.config.NodeUUID,
	))
	client := nodev1.NewNodeGatewayClient(conn)
	stream, err := client.Control(streamContext)
	if err != nil {
		return false, fmt.Errorf("open ControlStream: %w", err)
	}
	desiredRevision, appliedRevision := agent.config.Revisions()
	lastCompletedSequence := uint64(0)
	if agent.config.Commands != nil {
		lastCompletedSequence = agent.config.Commands.LastCompletedSequence()
	}
	hello := &nodev1.NodeControlFrame{
		FrameId: newFrameID(), SentAt: timestamppb.Now(),
		Payload: &nodev1.NodeControlFrame_Hello{Hello: &nodev1.Hello{
			NodeUuid: agent.config.NodeUUID, BootId: agent.config.BootID,
			StartedAt: timestamppb.New(agent.config.StartedAt.UTC()), AgentVersion: agent.config.AgentVersion,
			SupportedProtocolVersions: append([]uint32(nil), agent.config.SupportedProtocolVersions...),
			Capabilities:              append([]string(nil), agent.config.Capabilities...),
			LastAckedSequence:         lastCompletedSequence,
			DesiredRevision:           desiredRevision, AppliedRevision: appliedRevision,
		}},
	}
	if err := stream.Send(hello); err != nil {
		return false, fmt.Errorf("send Hello: %w", err)
	}
	first, err := stream.Recv()
	if err != nil {
		return false, fmt.Errorf("receive Welcome: %w", err)
	}
	welcome := first.GetWelcome()
	if err := agent.validateWelcome(welcome); err != nil {
		return false, err
	}
	connected := true
	agent.config.Logf("[control] connected session=%s protocol=%d", welcome.SessionId, welcome.ProtocolVersion)

	heartbeatInterval := time.Duration(welcome.HeartbeatIntervalSeconds) * time.Second
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	if err := agent.sendHeartbeat(stream, welcome.SessionId); err != nil {
		return connected, err
	}
	if agent.config.Data != nil {
		go agent.runDataLoop(sessionContext, client, streamContext, welcome.SessionId, welcome.ProtocolVersion)
	}

	type receiveResult struct {
		frame *nodev1.SystemControlFrame
		err   error
	}
	received := make(chan receiveResult, 1)
	go func() {
		for {
			frame, receiveErr := stream.Recv()
			select {
			case received <- receiveResult{frame: frame, err: receiveErr}:
			case <-sessionContext.Done():
				return
			}
			if receiveErr != nil {
				return
			}
		}
	}()

	configRevisions := make(chan uint64, 1)
	filterRevisions := make(chan uint64, 1)
	configResults := make(chan configApplyResult, 1)
	commandFrames := make(chan *nodev1.NodeControlFrame, 64)
	go agent.runConfigWorker(sessionContext, configRevisions, configResults)
	go agent.runFilterWorker(sessionContext, filterRevisions)
	if welcome.DesiredRevision > desiredRevision {
		signalRevision(configRevisions, welcome.DesiredRevision)
	}

	for {
		select {
		case <-sessionContext.Done():
			return connected, sessionContext.Err()
		case <-ticker.C:
			if err := agent.sendHeartbeat(stream, welcome.SessionId); err != nil {
				return connected, err
			}
		case result := <-configResults:
			frame := &nodev1.NodeControlFrame{
				FrameId: newFrameID(), SentAt: timestamppb.Now(),
				Payload: &nodev1.NodeControlFrame_ConfigApplied{ConfigApplied: &nodev1.ConfigApplied{
					Revision: result.revision, Succeeded: result.err == nil,
					ErrorMessage: errorMessage(result.err), AppliedAt: timestamppb.Now(),
				}},
			}
			if err := stream.Send(frame); err != nil {
				return connected, fmt.Errorf("send config apply result: %w", err)
			}
		case frame := <-commandFrames:
			if err := stream.Send(frame); err != nil {
				return connected, fmt.Errorf("send command acknowledgement: %w", err)
			}
		case result := <-received:
			if result.err != nil {
				if errors.Is(result.err, io.EOF) {
					return connected, nil
				}
				return connected, result.err
			}
			switch payload := result.frame.Payload.(type) {
			case *nodev1.SystemControlFrame_ConfigRevisionChanged:
				signalRevision(configRevisions, payload.ConfigRevisionChanged.Revision)
			case *nodev1.SystemControlFrame_FilterRevisionChanged:
				signalRevision(filterRevisions, payload.FilterRevisionChanged.Revision)
			case *nodev1.SystemControlFrame_Ping:
				if err := stream.Send(&nodev1.NodeControlFrame{
					FrameId: newFrameID(), SentAt: timestamppb.Now(),
					Payload: &nodev1.NodeControlFrame_Pong{Pong: &nodev1.Pong{Nonce: payload.Ping.Nonce}},
				}); err != nil {
					return connected, fmt.Errorf("send Pong: %w", err)
				}
			case *nodev1.SystemControlFrame_Pong, *nodev1.SystemControlFrame_DrainNotice:
			case *nodev1.SystemControlFrame_Command:
				if agent.config.Commands == nil {
					return connected, errors.New("received command but command execution is not configured")
				}
				emit := func(frame *nodev1.NodeControlFrame) {
					select {
					case commandFrames <- frame:
					case <-sessionContext.Done():
					}
				}
				frames, acceptErr := agent.config.Commands.Accept(sessionContext, payload.Command, emit)
				if acceptErr != nil {
					return connected, fmt.Errorf("accept command: %w", acceptErr)
				}
				for _, frame := range frames {
					if err := stream.Send(frame); err != nil {
						return connected, fmt.Errorf("send command receipt: %w", err)
					}
				}
			case *nodev1.SystemControlFrame_CancelCommand:
				if agent.config.Commands != nil {
					agent.config.Commands.Cancel(payload.CancelCommand.CommandId)
				}
			case *nodev1.SystemControlFrame_Welcome:
				return connected, errors.New("received duplicate Welcome")
			default:
				return connected, errors.New("received unsupported control frame")
			}
		}
	}
}

func (agent *Agent) runDataLoop(ctx context.Context, client nodev1.NodeGatewayClient, streamContext context.Context, controlSessionID string, protocol uint32) {
	backoff := agent.config.ReconnectMin
	for ctx.Err() == nil {
		err := agent.runDataOnce(client, streamContext, controlSessionID, protocol)
		if ctx.Err() != nil {
			return
		}
		agent.config.Logf("[data] session ended: %v", err)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < agent.config.ReconnectMax {
			backoff *= 2
			if backoff > agent.config.ReconnectMax {
				backoff = agent.config.ReconnectMax
			}
		}
	}
}

func (agent *Agent) runDataOnce(client nodev1.NodeGatewayClient, streamContext context.Context, controlSessionID string, protocol uint32) error {
	dataContext, cancelData := context.WithCancel(streamContext)
	defer cancelData()
	stream, err := client.Data(dataContext)
	if err != nil {
		return fmt.Errorf("open DataStream: %w", err)
	}
	if err := stream.Send(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Hello{Hello: &nodev1.DataStreamHello{
		NodeUuid: agent.config.NodeUUID, BootId: agent.config.BootID,
		ControlSessionId: controlSessionID, ProtocolVersion: protocol,
	}}}); err != nil {
		return fmt.Errorf("send DataStreamHello: %w", err)
	}
	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receive DataStreamWelcome: %w", err)
	}
	welcome := first.GetWelcome()
	if welcome == nil || strings.TrimSpace(welcome.DataSessionId) == "" || welcome.MaxConcurrency == 0 ||
		welcome.MaxChunkSize == 0 || welcome.MaxChunkSize > uint32(nodecontract.MaxDataChunkSize) {
		return errors.New("management did not return a valid DataStreamWelcome")
	}
	agent.config.Logf("[data] connected session=%s concurrency=%d chunk=%d", welcome.DataSessionId, welcome.MaxConcurrency, welcome.MaxChunkSize)

	outgoing := make(chan *nodev1.NodeDataFrame, int(welcome.MaxConcurrency)*2+8)
	dispatchSession, err := agent.config.Data.NewSession(dataContext, int(welcome.MaxConcurrency), int(welcome.MaxChunkSize), func(emitContext context.Context, frame *nodev1.NodeDataFrame) error {
		select {
		case <-emitContext.Done():
			return emitContext.Err()
		case <-dataContext.Done():
			return dataContext.Err()
		case outgoing <- frame:
			return nil
		}
	})
	if err != nil {
		return err
	}
	defer dispatchSession.Close()

	type receiveResult struct {
		frame *nodev1.SystemDataFrame
		err   error
	}
	received := make(chan receiveResult, 1)
	go func() {
		for {
			frame, receiveErr := stream.Recv()
			select {
			case received <- receiveResult{frame: frame, err: receiveErr}:
			case <-dataContext.Done():
				return
			}
			if receiveErr != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-dataContext.Done():
			return dataContext.Err()
		case frame := <-outgoing:
			if err := stream.Send(frame); err != nil {
				return fmt.Errorf("send node data frame: %w", err)
			}
		case result := <-received:
			if result.err != nil {
				return result.err
			}
			switch payload := result.frame.Payload.(type) {
			case *nodev1.SystemDataFrame_Request:
				if err := dispatchSession.Accept(payload.Request); err != nil && !errors.Is(err, context.Canceled) {
					return fmt.Errorf("accept data request: %w", err)
				}
			case *nodev1.SystemDataFrame_Cancel:
				dispatchSession.Cancel(payload.Cancel.RequestId)
			case *nodev1.SystemDataFrame_Ping:
				if err := stream.Send(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Pong{
					Pong: &nodev1.Pong{Nonce: payload.Ping.Nonce},
				}}); err != nil {
					return err
				}
			case *nodev1.SystemDataFrame_Pong:
			case *nodev1.SystemDataFrame_Welcome:
				return errors.New("received duplicate DataStreamWelcome")
			default:
				return errors.New("received unsupported data frame")
			}
		}
	}
}

func (agent *Agent) sendHeartbeat(stream grpc.BidiStreamingClient[nodev1.NodeControlFrame, nodev1.SystemControlFrame], sessionID string) error {
	snapshot := agent.config.Snapshot()
	desiredRevision, appliedRevision := agent.config.Revisions()
	components := make([]*nodev1.ComponentHealth, 0, len(snapshot.Components))
	lastCompletedSequence := uint64(0)
	if agent.config.Commands != nil {
		lastCompletedSequence = agent.config.Commands.LastCompletedSequence()
	}
	for _, component := range snapshot.Components {
		checkedAt := component.CheckedAt
		if checkedAt.IsZero() {
			checkedAt = time.Now()
		}
		components = append(components, &nodev1.ComponentHealth{
			Component: component.Component, State: component.State, Detail: component.Detail,
			CheckedAt: timestamppb.New(checkedAt.UTC()),
		})
	}
	frame := &nodev1.NodeControlFrame{
		FrameId: newFrameID(), SentAt: timestamppb.Now(),
		Payload: &nodev1.NodeControlFrame_Heartbeat{Heartbeat: &nodev1.Heartbeat{
			NodeUuid: agent.config.NodeUUID, BootId: agent.config.BootID, SessionId: sessionID,
			MailboxCount: snapshot.MailboxCount, DiskTotalBytes: snapshot.DiskTotalBytes,
			DiskAvailableBytes: snapshot.DiskAvailableBytes, DesiredRevision: desiredRevision,
			AppliedRevision: appliedRevision, LastApplyError: snapshot.LastApplyError,
			LastCompletedSequence: lastCompletedSequence,
			Readiness:             snapshot.Readiness, Components: components,
		}},
	}
	if err := stream.Send(frame); err != nil {
		return fmt.Errorf("send heartbeat: %w", err)
	}
	return nil
}

func (agent *Agent) runConfigWorker(ctx context.Context, revisions <-chan uint64, results chan<- configApplyResult) {
	for {
		select {
		case <-ctx.Done():
			return
		case revision := <-revisions:
			appliedRevision := revision
			var err error
			if agent.config.OnConfigRevision != nil {
				appliedRevision, err = agent.config.OnConfigRevision(ctx, revision)
			}
			select {
			case results <- configApplyResult{revision: appliedRevision, err: err}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (agent *Agent) runFilterWorker(ctx context.Context, revisions <-chan uint64) {
	for {
		select {
		case <-ctx.Done():
			return
		case revision := <-revisions:
			if agent.config.OnFilterRevision != nil {
				if err := agent.config.OnFilterRevision(ctx, revision); err != nil {
					agent.config.Logf("[control] filter revision %d sync failed: %v", revision, err)
				}
			}
		}
	}
}

func (agent *Agent) validateWelcome(welcome *nodev1.Welcome) error {
	if welcome == nil || strings.TrimSpace(welcome.SessionId) == "" {
		return errors.New("management did not return a valid Welcome")
	}
	if welcome.HeartbeatIntervalSeconds == 0 || welcome.HeartbeatIntervalSeconds > 600 {
		return errors.New("management returned an invalid heartbeat interval")
	}
	for _, protocol := range agent.config.SupportedProtocolVersions {
		if welcome.ProtocolVersion == protocol {
			return nil
		}
	}
	return fmt.Errorf("management selected unsupported protocol %d", welcome.ProtocolVersion)
}

func (agent *Agent) jitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(agent.config.Rand.Int63n(int64(max) + 1))
}

func signalRevision(channel chan uint64, revision uint64) {
	select {
	case channel <- revision:
		return
	default:
	}
	select {
	case previous := <-channel:
		if previous > revision {
			revision = previous
		}
	default:
	}
	select {
	case channel <- revision:
	default:
	}
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func newFrameID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Uint64())
}

package agent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nodecommand "github.com/ticket/email-mail-node/internal/command"
	"github.com/ticket/email-mail-node/internal/nodedata"
	nodecontract "github.com/ticket/email-node-contract"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const agentTestNodeUUID = "6ba7b810-9dad-41d1-80b4-00c04fd430c8"

type testGateway struct {
	nodev1.UnimplementedNodeGatewayServer
	done chan struct{}
	once sync.Once

	mu            sync.Mutex
	hello         *nodev1.Hello
	heartbeat     *nodev1.Heartbeat
	configApplied *nodev1.ConfigApplied
	pong          *nodev1.Pong
}

func (gateway *testGateway) Control(stream grpc.BidiStreamingServer[nodev1.NodeControlFrame, nodev1.SystemControlFrame]) error {
	values, _ := metadata.FromIncomingContext(stream.Context())
	if first(values.Get("authorization")) != "Node node-secret" || first(values.Get("x-mailhub-node-uuid")) != agentTestNodeUUID {
		return status.Error(16, "bad metadata")
	}
	firstFrame, err := stream.Recv()
	if err != nil || firstFrame.GetHello() == nil {
		return errors.New("Hello was not received")
	}
	gateway.mu.Lock()
	gateway.hello = firstFrame.GetHello()
	gateway.mu.Unlock()
	if err := stream.Send(&nodev1.SystemControlFrame{Payload: &nodev1.SystemControlFrame_Welcome{Welcome: &nodev1.Welcome{
		SessionId: "session-1", ProtocolVersion: 1, HeartbeatIntervalSeconds: 1,
		ServerTime: timestamppb.Now(), DesiredRevision: 3,
	}}}); err != nil {
		return err
	}
	heartbeatFrame, err := stream.Recv()
	if err != nil || heartbeatFrame.GetHeartbeat() == nil {
		return errors.New("initial heartbeat was not received")
	}
	gateway.mu.Lock()
	gateway.heartbeat = heartbeatFrame.GetHeartbeat()
	gateway.mu.Unlock()
	for _, frame := range []*nodev1.SystemControlFrame{
		{Payload: &nodev1.SystemControlFrame_ConfigRevisionChanged{ConfigRevisionChanged: &nodev1.ConfigRevisionChanged{Revision: 4}}},
		{Payload: &nodev1.SystemControlFrame_FilterRevisionChanged{FilterRevisionChanged: &nodev1.FilterRevisionChanged{Revision: 5}}},
		{Payload: &nodev1.SystemControlFrame_Ping{Ping: &nodev1.Ping{Nonce: "ping-1"}}},
	} {
		if err := stream.Send(frame); err != nil {
			return err
		}
	}
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		gateway.mu.Lock()
		if frame.GetConfigApplied() != nil {
			gateway.configApplied = frame.GetConfigApplied()
		}
		if frame.GetPong() != nil {
			gateway.pong = frame.GetPong()
		}
		complete := gateway.configApplied != nil && gateway.pong != nil
		gateway.mu.Unlock()
		if complete {
			gateway.once.Do(func() { close(gateway.done) })
			<-stream.Context().Done()
			return stream.Context().Err()
		}
	}
}

func TestAgentReconnectsAndRunsControlProtocol(t *testing.T) {
	gateway := &testGateway{done: make(chan struct{})}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	nodev1.RegisterNodeGatewayServer(server, gateway)
	go func() { _ = server.Serve(listener) }()
	defer func() {
		server.Stop()
		_ = listener.Close()
	}()

	var dialAttempts atomic.Int32
	var filterRevision atomic.Uint64
	agent, err := New(Config{
		Address: "bufnet", NodeUUID: agentTestNodeUUID, Credential: "node-secret",
		BootID: "boot-1", StartedAt: time.Now().Add(-time.Minute), AgentVersion: "test-agent",
		SupportedProtocolVersions: []uint32{1}, Capabilities: []string{"config.revision.v1"},
		ReconnectMin: time.Millisecond, ReconnectMax: 5 * time.Millisecond,
		Dial: func(context.Context) (*grpc.ClientConn, error) {
			if dialAttempts.Add(1) == 1 {
				return nil, errors.New("temporary dial failure")
			}
			return grpc.NewClient("passthrough:///bufnet",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			)
		},
		Revisions: func() (uint64, uint64) { return 3, 3 },
		Snapshot: func() HealthSnapshot {
			return HealthSnapshot{MailboxCount: 12, DiskTotalBytes: 100, DiskAvailableBytes: 40,
				Readiness: nodev1.ReadinessState_READINESS_STATE_READY}
		},
		OnConfigRevision: func(_ context.Context, revision uint64) (uint64, error) { return revision, nil },
		OnFilterRevision: func(_ context.Context, revision uint64) error {
			filterRevision.Store(revision)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		agent.Run(ctx)
		close(runDone)
	}()
	select {
	case <-gateway.done:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("control protocol did not complete")
	}
	filterDeadline := time.Now().Add(time.Second)
	for filterRevision.Load() != 5 && time.Now().Before(filterDeadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("agent did not stop after cancellation")
	}

	gateway.mu.Lock()
	hello, heartbeat, applied, pong := gateway.hello, gateway.heartbeat, gateway.configApplied, gateway.pong
	gateway.mu.Unlock()
	if dialAttempts.Load() < 2 {
		t.Fatalf("dial attempts = %d", dialAttempts.Load())
	}
	if hello == nil || hello.NodeUuid != agentTestNodeUUID || hello.BootId != "boot-1" {
		t.Fatalf("hello = %#v", hello)
	}
	if heartbeat == nil || heartbeat.SessionId != "session-1" || heartbeat.MailboxCount != 12 || heartbeat.Readiness != nodev1.ReadinessState_READINESS_STATE_READY {
		t.Fatalf("heartbeat = %#v", heartbeat)
	}
	if applied == nil || !applied.Succeeded || applied.Revision != 4 {
		t.Fatalf("config applied = %#v", applied)
	}
	if pong == nil || pong.Nonce != "ping-1" {
		t.Fatalf("pong = %#v", pong)
	}
	if filterRevision.Load() != 5 {
		t.Fatalf("filter revision = %d", filterRevision.Load())
	}
}

func TestTLSDialerRejectsURLScheme(t *testing.T) {
	if _, err := TLSDialer("https://control.example:443", "", "test"); err == nil {
		t.Fatal("control URL with scheme was accepted")
	}
}

type commandGateway struct {
	nodev1.UnimplementedNodeGatewayServer
	done chan struct{}
	once sync.Once

	mu          sync.Mutex
	sessions    int
	secondHello *nodev1.Hello
	received    *nodev1.CommandReceived
	started     *nodev1.CommandStarted
	result      *nodev1.CommandResult
}

func (gateway *commandGateway) Control(stream grpc.BidiStreamingServer[nodev1.NodeControlFrame, nodev1.SystemControlFrame]) error {
	helloFrame, err := stream.Recv()
	if err != nil || helloFrame.GetHello() == nil {
		return errors.New("missing Hello")
	}
	gateway.mu.Lock()
	gateway.sessions++
	sessionNumber := gateway.sessions
	if sessionNumber == 2 {
		gateway.secondHello = helloFrame.GetHello()
	}
	gateway.mu.Unlock()
	if err := stream.Send(&nodev1.SystemControlFrame{Payload: &nodev1.SystemControlFrame_Welcome{Welcome: &nodev1.Welcome{
		SessionId: "command-session", ProtocolVersion: 1, HeartbeatIntervalSeconds: 1, ServerTime: timestamppb.Now(),
	}}}); err != nil {
		return err
	}
	if _, err := stream.Recv(); err != nil { // initial heartbeat
		return err
	}
	if sessionNumber == 2 {
		gateway.once.Do(func() { close(gateway.done) })
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	if err := stream.Send(&nodev1.SystemControlFrame{Payload: &nodev1.SystemControlFrame_Command{Command: &nodev1.Command{
		CommandId: "command-7", Sequence: 7, Type: string(nodecontract.CommandMailboxCreate), SchemaVersion: 1,
		IdempotencyKey: "mailbox:create:a@example.com", PayloadJson: []byte(`{"email_address":"a@example.com"}`),
		CreatedAt: timestamppb.Now(), DeadlineAt: timestamppb.New(time.Now().Add(time.Minute)),
	}}}); err != nil {
		return err
	}
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		gateway.mu.Lock()
		if frame.GetCommandReceived() != nil {
			gateway.received = frame.GetCommandReceived()
		}
		if frame.GetCommandStarted() != nil {
			gateway.started = frame.GetCommandStarted()
		}
		if frame.GetCommandResult() != nil {
			gateway.result = frame.GetCommandResult()
		}
		complete := gateway.received != nil && gateway.started != nil && gateway.result != nil
		gateway.mu.Unlock()
		if complete {
			return status.Error(14, "force reconnect after command")
		}
	}
}

func TestAgentExecutesDurableCommandAndReportsSequenceAfterReconnect(t *testing.T) {
	gateway := &commandGateway{done: make(chan struct{})}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	nodev1.RegisterNodeGatewayServer(server, gateway)
	go func() { _ = server.Serve(listener) }()
	defer func() {
		server.Stop()
		_ = listener.Close()
	}()

	journal, err := nodecommand.OpenJournal(t.TempDir(), nodecommand.JournalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	dispatcher, err := nodecommand.NewDispatcher(journal, func(context.Context, *nodev1.Command) nodecommand.StoredResult {
		executions.Add(1)
		return nodecommand.StoredResult{State: nodev1.CommandState_COMMAND_STATE_SUCCEEDED, ResultCode: "http.201", ResultJSON: []byte(`{"status_code":201}`)}
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go dispatcher.Run(ctx)
	controlAgent, err := New(Config{
		Address: "bufnet", NodeUUID: agentTestNodeUUID, Credential: "node-secret",
		BootID: "boot-command", StartedAt: time.Now().Add(-time.Minute), AgentVersion: "test-agent",
		SupportedProtocolVersions: []uint32{1}, Commands: dispatcher,
		ReconnectMin: time.Millisecond, ReconnectMax: 5 * time.Millisecond,
		Dial: func(context.Context) (*grpc.ClientConn, error) {
			return grpc.NewClient("passthrough:///bufnet",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() {
		controlAgent.Run(ctx)
		close(finished)
	}()
	select {
	case <-gateway.done:
	case <-time.After(3 * time.Second):
		t.Fatal("agent did not reconnect after command")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("agent did not stop")
	}

	gateway.mu.Lock()
	secondHello, received, started, result := gateway.secondHello, gateway.received, gateway.started, gateway.result
	gateway.mu.Unlock()
	if executions.Load() != 1 || received == nil || started == nil || result == nil || result.State != nodev1.CommandState_COMMAND_STATE_SUCCEEDED {
		t.Fatalf("executions=%d received=%#v started=%#v result=%#v", executions.Load(), received, started, result)
	}
	if secondHello == nil || secondHello.LastAckedSequence != 7 {
		t.Fatalf("second Hello = %#v", secondHello)
	}
}

type dataGateway struct {
	nodev1.UnimplementedNodeGatewayServer
	dataChunk chan struct{}
	pong      chan struct{}
	onceChunk sync.Once
	oncePong  sync.Once
}

func (gateway *dataGateway) Control(stream grpc.BidiStreamingServer[nodev1.NodeControlFrame, nodev1.SystemControlFrame]) error {
	frame, err := stream.Recv()
	if err != nil || frame.GetHello() == nil {
		return errors.New("missing control Hello")
	}
	if err := stream.Send(&nodev1.SystemControlFrame{Payload: &nodev1.SystemControlFrame_Welcome{Welcome: &nodev1.Welcome{
		SessionId: "data-control-session", ProtocolVersion: 1, HeartbeatIntervalSeconds: 1, ServerTime: timestamppb.Now(),
	}}}); err != nil {
		return err
	}
	if heartbeat, err := stream.Recv(); err != nil || heartbeat.GetHeartbeat() == nil {
		return errors.New("missing initial heartbeat")
	}
	select {
	case <-gateway.dataChunk:
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
	if err := stream.Send(&nodev1.SystemControlFrame{Payload: &nodev1.SystemControlFrame_Ping{Ping: &nodev1.Ping{Nonce: "during-data"}}}); err != nil {
		return err
	}
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		if frame.GetPong().GetNonce() == "during-data" {
			gateway.oncePong.Do(func() { close(gateway.pong) })
			<-stream.Context().Done()
			return stream.Context().Err()
		}
	}
}

func (gateway *dataGateway) Data(stream grpc.BidiStreamingServer[nodev1.NodeDataFrame, nodev1.SystemDataFrame]) error {
	frame, err := stream.Recv()
	hello := frame.GetHello()
	if err != nil || hello == nil || hello.ControlSessionId != "data-control-session" {
		return errors.New("missing or invalid DataStreamHello")
	}
	if err := stream.Send(&nodev1.SystemDataFrame{Payload: &nodev1.SystemDataFrame_Welcome{Welcome: &nodev1.DataStreamWelcome{
		DataSessionId: "data-session", MaxConcurrency: 2, MaxChunkSize: 4, ServerTime: timestamppb.Now(),
	}}}); err != nil {
		return err
	}
	if err := stream.Send(&nodev1.SystemDataFrame{Payload: &nodev1.SystemDataFrame_Request{Request: &nodev1.SystemDataRequest{
		RequestId: "data-request", Type: string(nodecontract.DataRequestMessageRaw), Locator: &nodev1.DataLocator{Mailbox: "a@example.com"},
		DeadlineAt: timestamppb.New(time.Now().Add(time.Minute)),
	}}}); err != nil {
		return err
	}
	header, err := stream.Recv()
	if err != nil || header.GetHeader() == nil {
		return errors.New("missing data header")
	}
	chunk, err := stream.Recv()
	if err != nil || chunk.GetChunk() == nil || string(chunk.GetChunk().Data) != "data" {
		return errors.New("missing first data chunk")
	}
	gateway.onceChunk.Do(func() { close(gateway.dataChunk) })
	if err := stream.Send(&nodev1.SystemDataFrame{Payload: &nodev1.SystemDataFrame_Cancel{Cancel: &nodev1.CancelDataRequest{
		RequestId: "data-request", Reason: "downstream closed",
	}}}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

type cancelAwareReader struct {
	mu     sync.Mutex
	first  bool
	closed chan struct{}
	once   sync.Once
}

func (reader *cancelAwareReader) Read(buffer []byte) (int, error) {
	reader.mu.Lock()
	if !reader.first {
		reader.first = true
		reader.mu.Unlock()
		return copy(buffer, "data"), nil
	}
	reader.mu.Unlock()
	<-reader.closed
	return 0, io.ErrClosedPipe
}

func (reader *cancelAwareReader) Close() error {
	reader.once.Do(func() { close(reader.closed) })
	return nil
}

func TestDataStreamCancellationDoesNotBlockControlStream(t *testing.T) {
	gateway := &dataGateway{dataChunk: make(chan struct{}), pong: make(chan struct{})}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	nodev1.RegisterNodeGatewayServer(server, gateway)
	go func() { _ = server.Serve(listener) }()
	defer func() {
		server.Stop()
		_ = listener.Close()
	}()

	reader := &cancelAwareReader{closed: make(chan struct{})}
	dataDispatcher, err := nodedata.NewDispatcher(func(context.Context, *nodev1.SystemDataRequest) (*nodedata.Response, error) {
		return &nodedata.Response{
			StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/octet-stream"}},
			ContentLength: -1, Body: reader,
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	controlAgent, err := New(Config{
		Address: "bufnet", NodeUUID: agentTestNodeUUID, Credential: "node-secret",
		BootID: "boot-data", StartedAt: time.Now().Add(-time.Minute), AgentVersion: "test-agent",
		SupportedProtocolVersions: []uint32{1}, Data: dataDispatcher,
		ReconnectMin: time.Millisecond, ReconnectMax: 5 * time.Millisecond,
		Dial: func(context.Context) (*grpc.ClientConn, error) {
			return grpc.NewClient("passthrough:///bufnet",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		controlAgent.Run(ctx)
		close(done)
	}()
	for name, signal := range map[string]<-chan struct{}{"data reader cancellation": reader.closed, "control pong": gateway.pong} {
		select {
		case <-signal:
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatalf("did not observe %s", name)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("agent did not stop")
	}
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

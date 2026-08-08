package nodegateway

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodedata"
	"github.com/ticket/email-mgmt-system/internal/nodesession"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testNodeUUID = "6ba7b810-9dad-41d1-80b4-00c04fd430c8"

type fakeStateStore struct {
	mu           sync.Mutex
	server       model.MailServer
	connected    int
	heartbeats   int
	disconnected int
}

func (store *fakeStateStore) GetServer(id uint64) (*model.MailServer, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if id != store.server.ID {
		return nil, errors.New("not found")
	}
	copy := store.server
	return &copy, nil
}

func (store *fakeStateStore) UpdateNodeSessionConnected(_ uint64, lease time.Time, agent string, protocol uint32, capabilities []string, boot string, started time.Time, applied uint64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.connected++
	store.server.ConnectionState = model.ConnectionConnected
	store.server.LeaseExpiresAt = &lease
	store.server.AgentVersion = agent
	store.server.ProtocolVersion = "1"
	store.server.Capabilities = append([]string(nil), capabilities...)
	store.server.LastBootID = boot
	store.server.LastStartedAt = &started
	store.server.AppliedRevision = applied
	return nil
}

func (store *fakeStateStore) UpdateNodeControlHeartbeat(_ uint64, lease time.Time, load int, readiness string, applied uint64, lastError, boot string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.heartbeats++
	store.server.LeaseExpiresAt = &lease
	store.server.CurrentLoad = load
	store.server.ReadinessState = readiness
	store.server.AppliedRevision = applied
	store.server.LastApplyError = lastError
	store.server.LastBootID = boot
	return nil
}

func (store *fakeStateStore) UpdateNodeConfigApplied(_ uint64, revision uint64, succeeded bool, applyError string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if succeeded {
		store.server.AppliedRevision = revision
	}
	store.server.LastApplyError = applyError
	return nil
}

func (store *fakeStateStore) MarkNodeSessionDisconnected(_ uint64, _ time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.disconnected++
	store.server.ConnectionState = model.ConnectionDisconnected
	return nil
}

func (store *fakeStateStore) ExpireNodeControlLeases(time.Time) error { return nil }
func (store *fakeStateStore) RecordNodeSessionAudit(string, uint64, string, string, map[string]any) error {
	return nil
}

func (store *fakeStateStore) counts() (int, int, int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.connected, store.heartbeats, store.disconnected
}

func TestControlStreamHandshakeHeartbeatNotificationAndDisconnect(t *testing.T) {
	store := &fakeStateStore{server: model.MailServer{
		ID: 42, NodeUUID: stringPointer(testNodeUUID), EnrollmentState: model.EnrollmentApproved,
		DesiredRevision: 7, AppliedRevision: 5,
	}}
	sessions := nodesession.NewRegistry()
	gateway, err := New(store, sessions, testAuthenticator, Config{
		HeartbeatInterval: 100 * time.Millisecond, LeaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, cleanup := newTestGatewayClient(t, gateway)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"authorization", "Node node-secret", "x-mailhub-node-uuid", testNodeUUID,
	))
	stream, err := client.Control(ctx)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().Add(-time.Minute).UTC()
	if err := stream.Send(helloFrame(testNodeUUID, startedAt, []uint32{1})); err != nil {
		t.Fatal(err)
	}
	welcomeFrame, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	welcome := welcomeFrame.GetWelcome()
	if welcome == nil || welcome.SessionId == "" || welcome.ProtocolVersion != 1 || welcome.DesiredRevision != 7 {
		t.Fatalf("welcome = %#v", welcome)
	}
	if err := stream.Send(&nodev1.NodeControlFrame{Payload: &nodev1.NodeControlFrame_Heartbeat{Heartbeat: &nodev1.Heartbeat{
		NodeUuid: testNodeUUID, BootId: "boot-1", SessionId: welcome.SessionId,
		MailboxCount: 12, DesiredRevision: 7, AppliedRevision: 7,
		Readiness: nodev1.ReadinessState_READINESS_STATE_READY,
	}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_, heartbeats, _ := store.counts()
		return heartbeats == 1
	})

	notification := &nodev1.SystemControlFrame{Payload: &nodev1.SystemControlFrame_ConfigRevisionChanged{
		ConfigRevisionChanged: &nodev1.ConfigRevisionChanged{Revision: 7},
	}}
	if err := sessions.Send(context.Background(), 42, notification); err != nil {
		t.Fatal(err)
	}
	received, err := stream.Recv()
	if err != nil || received.GetConfigRevisionChanged().GetRevision() != 7 {
		t.Fatalf("notification = %#v, %v", received, err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	_, _ = stream.Recv()
	waitFor(t, func() bool {
		_, _, disconnected := store.counts()
		return disconnected == 1
	})
}

func TestControlStreamRejectsAuthenticationAndProtocolMismatch(t *testing.T) {
	store := &fakeStateStore{server: model.MailServer{
		ID: 42, NodeUUID: stringPointer(testNodeUUID), EnrollmentState: model.EnrollmentApproved,
	}}
	gateway, err := New(store, nodesession.NewRegistry(), testAuthenticator, Config{})
	if err != nil {
		t.Fatal(err)
	}
	client, cleanup := newTestGatewayClient(t, gateway)
	defer cleanup()

	for _, test := range []struct {
		name       string
		credential string
		protocols  []uint32
		wantCode   codes.Code
	}{
		{name: "bad credential", credential: "wrong", protocols: []uint32{1}, wantCode: codes.Unauthenticated},
		{name: "no common protocol", credential: "node-secret", protocols: []uint32{99}, wantCode: codes.FailedPrecondition},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
				"authorization", "Node "+test.credential, "x-mailhub-node-uuid", testNodeUUID,
			))
			stream, err := client.Control(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(helloFrame(testNodeUUID, time.Now().UTC(), test.protocols)); err != nil {
				t.Fatal(err)
			}
			_, err = stream.Recv()
			if status.Code(err) != test.wantCode {
				t.Fatalf("stream error = %v, want %s", err, test.wantCode)
			}
		})
	}
}

func TestControlStreamDisconnectsWhenCredentialOverlapExpires(t *testing.T) {
	store := &fakeStateStore{server: model.MailServer{
		ID: 42, NodeUUID: stringPointer(testNodeUUID), EnrollmentState: model.EnrollmentApproved,
		DesiredRevision: 5, AppliedRevision: 5,
	}}
	expiresAt := time.Now().Add(250 * time.Millisecond)
	authenticate := func(credential, nodeUUID string, _ time.Time) (Principal, error) {
		if credential != "node-secret" || nodeUUID != testNodeUUID {
			return Principal{}, errors.New("invalid credential")
		}
		return Principal{
			ServerID: 42, NodeUUID: testNodeUUID, CredentialID: 7, CredentialVersion: 2,
			CredentialExpiresAt: &expiresAt,
		}, nil
	}
	gateway, err := New(store, nodesession.NewRegistry(), authenticate, Config{
		HeartbeatInterval: time.Second, LeaseTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, cleanup := newTestGatewayClient(t, gateway)
	defer cleanup()

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"authorization", "Node node-secret", "x-mailhub-node-uuid", testNodeUUID,
	))
	stream, err := client.Control(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(helloFrame(testNodeUUID, time.Now().UTC(), []uint32{1})); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("receive welcome: %v", err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("credential expiry stream error = %v, want %s", err, codes.Unauthenticated)
	}
	waitFor(t, func() bool {
		_, _, disconnected := store.counts()
		return disconnected == 1
	})
}

func TestControlStreamAcceptsCredentialExpiryScheduledAfterConnect(t *testing.T) {
	store := &fakeStateStore{server: model.MailServer{
		ID: 42, NodeUUID: stringPointer(testNodeUUID), EnrollmentState: model.EnrollmentApproved,
		DesiredRevision: 5, AppliedRevision: 5,
	}}
	sessions := nodesession.NewRegistry()
	authenticate := func(credential, nodeUUID string, _ time.Time) (Principal, error) {
		if credential != "node-secret" || nodeUUID != testNodeUUID {
			return Principal{}, errors.New("invalid credential")
		}
		return Principal{ServerID: 42, NodeUUID: testNodeUUID, CredentialID: 7, CredentialVersion: 1}, nil
	}
	gateway, err := New(store, sessions, authenticate, Config{
		HeartbeatInterval: time.Second, LeaseTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, cleanup := newTestGatewayClient(t, gateway)
	defer cleanup()

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"authorization", "Node node-secret", "x-mailhub-node-uuid", testNodeUUID,
	))
	stream, err := client.Control(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(helloFrame(testNodeUUID, time.Now().UTC(), []uint32{1})); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("receive welcome: %v", err)
	}
	if !sessions.ExpireCredentialAt(42, 7, time.Now().Add(50*time.Millisecond)) {
		t.Fatal("connected credential expiry was not scheduled")
	}
	if _, err := stream.Recv(); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("scheduled credential expiry stream error = %v, want %s", err, codes.Unauthenticated)
	}
}

func TestDataStreamRequiresAndRoutesThroughActiveControlSession(t *testing.T) {
	store := &fakeStateStore{server: model.MailServer{
		ID: 42, NodeUUID: stringPointer(testNodeUUID), EnrollmentState: model.EnrollmentApproved,
		DesiredRevision: 5, AppliedRevision: 5,
	}}
	controlSessions := nodesession.NewRegistry()
	dataSessions := nodedata.NewRegistry(nodedata.Config{MaxConcurrency: 2, MaxChunkSize: 4})
	gateway, err := New(store, controlSessions, testAuthenticator, Config{
		HeartbeatInterval: time.Second, LeaseTimeout: 5 * time.Second, DataSessions: dataSessions,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, cleanup := newTestGatewayClient(t, gateway)
	defer cleanup()
	ctx, cancel := context.WithCancel(metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"authorization", "Node node-secret", "x-mailhub-node-uuid", testNodeUUID,
	)))
	defer cancel()

	control, err := client.Control(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Send(helloFrame(testNodeUUID, time.Now().UTC(), []uint32{1})); err != nil {
		t.Fatal(err)
	}
	controlWelcome, err := control.Recv()
	if err != nil {
		t.Fatal(err)
	}
	controlSessionID := controlWelcome.GetWelcome().SessionId

	data, err := client.Data(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := data.Send(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Hello{Hello: &nodev1.DataStreamHello{
		NodeUuid: testNodeUUID, BootId: "boot-1", ControlSessionId: controlSessionID, ProtocolVersion: 1,
	}}}); err != nil {
		t.Fatal(err)
	}
	dataWelcome, err := data.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if dataWelcome.GetWelcome() == nil || dataWelcome.GetWelcome().MaxConcurrency != 2 || dataWelcome.GetWelcome().MaxChunkSize != 4 {
		t.Fatalf("data welcome = %#v", dataWelcome)
	}

	result := make(chan openResultForGateway, 1)
	go func() {
		response, openErr := dataSessions.Open(context.Background(), 42, nodedata.OpenInput{
			Type: "message.raw.v1", Locator: &nodev1.DataLocator{Mailbox: "a@example.com"},
		})
		result <- openResultForGateway{response: response, err: openErr}
	}()
	requestFrame, err := data.Recv()
	if err != nil {
		t.Fatal(err)
	}
	requestID := requestFrame.GetRequest().RequestId
	if err := data.Send(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Header{Header: &nodev1.NodeDataHeader{
		RequestId: requestID, Status: 200, ContentType: "text/plain", ContentLength: 4,
	}}}); err != nil {
		t.Fatal(err)
	}
	opened := <-result
	if opened.err != nil {
		t.Fatal(opened.err)
	}
	digest := sha256.Sum256([]byte("data"))
	if err := data.Send(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Chunk{Chunk: &nodev1.NodeDataChunk{
		RequestId: requestID, Sequence: 1, Data: []byte("data"),
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := data.Send(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_End{End: &nodev1.NodeDataEnd{
		RequestId: requestID, ChecksumAlgorithm: "sha256", Checksum: digest[:], TotalBytes: 4,
	}}}); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(opened.response.Body)
	if err != nil || string(body) != "data" {
		t.Fatalf("data body = %q, err=%v", body, err)
	}
}

type openResultForGateway struct {
	response *nodedata.Response
	err      error
}

func newTestGatewayClient(t *testing.T, gateway *Gateway) (nodev1.NodeGatewayClient, func()) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	nodev1.RegisterNodeGatewayServer(server, gateway)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		server.Stop()
		listener.Close()
		t.Fatal(err)
	}
	return nodev1.NewNodeGatewayClient(conn), func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}
}

func helloFrame(nodeUUID string, startedAt time.Time, protocols []uint32) *nodev1.NodeControlFrame {
	return &nodev1.NodeControlFrame{Payload: &nodev1.NodeControlFrame_Hello{Hello: &nodev1.Hello{
		NodeUuid: nodeUUID, BootId: "boot-1", StartedAt: timestamppb.New(startedAt),
		AgentVersion: "test-agent", SupportedProtocolVersions: protocols,
		Capabilities: []string{"config.revision.v1"}, DesiredRevision: 5, AppliedRevision: 5,
	}}}
}

func testAuthenticator(credential, nodeUUID string, _ time.Time) (Principal, error) {
	if credential != "node-secret" || nodeUUID != testNodeUUID {
		return Principal{}, errors.New("invalid credential")
	}
	return Principal{ServerID: 42, NodeUUID: testNodeUUID}, nil
}

func stringPointer(value string) *string { return &value }

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not observed before timeout")
}

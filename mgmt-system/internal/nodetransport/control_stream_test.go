package nodetransport

import (
	"context"
	"errors"
	"testing"

	"github.com/ticket/email-mgmt-system/internal/nodesession"
)

func TestMigrationTransportBlocksLegacyWhenDisabled(t *testing.T) {
	legacy := NewLegacyHTTPTransport("secret")
	transport := NewMigrationTransportWithLegacy(legacy, nil, false)
	_, err := transport.Execute(context.Background(), Target{NodeID: 1, TransportMode: "legacy_http"}, MailboxDelete("a@example.com", 0))
	if !errors.Is(err, ErrLegacyTransportDisabled) {
		t.Fatalf("Execute() error = %v, want legacy disabled", err)
	}
	_, err = transport.Probe(context.Background(), Target{NodeID: 1})
	if !errors.Is(err, ErrLegacyTransportDisabled) {
		t.Fatalf("Probe() error = %v, want legacy disabled", err)
	}
}

func TestControlStreamTransportSendsRevisionNotifications(t *testing.T) {
	sessions := nodesession.NewRegistry()
	session := sessions.Register(context.Background(), nodesession.RegisterInput{ServerID: 17, NodeUUID: "node-a"})
	transport := NewControlStreamTransport(sessions, func(_ context.Context, target Target, _ Notification) (uint64, error) {
		if target.NodeID != 17 {
			t.Fatalf("target = %#v", target)
		}
		return 23, nil
	})

	response, err := transport.Notify(context.Background(), Target{NodeID: 17}, ConfigRevisionChanged(0))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("Notify() = %#v, %v", response, err)
	}
	frame := <-session.Outgoing()
	if frame.GetConfigRevisionChanged().GetRevision() != 23 {
		t.Fatalf("frame = %#v", frame)
	}
}

func TestControlStreamTransportFailsFastWhenNodeIsOffline(t *testing.T) {
	transport := NewControlStreamTransport(nodesession.NewRegistry(), nil)
	_, err := transport.Notify(context.Background(), Target{NodeID: 18}, FilterRevisionChanged())
	if !errors.Is(err, nodesession.ErrSessionNotFound) {
		t.Fatalf("Notify() error = %v", err)
	}
}

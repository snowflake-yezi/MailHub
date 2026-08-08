package nodesession

import (
	"context"
	"errors"
	"testing"
	"time"

	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
)

func TestRegisterReplacesOnlyThePreviousSession(t *testing.T) {
	registry := NewRegistry()
	first := registry.Register(context.Background(), RegisterInput{ServerID: 7, NodeUUID: "node-a"})
	second := registry.Register(context.Background(), RegisterInput{ServerID: 7, NodeUUID: "node-a"})

	<-first.Context().Done()
	if !errors.Is(context.Cause(first.Context()), ErrSessionReplaced) {
		t.Fatalf("first session cause = %v", context.Cause(first.Context()))
	}
	if !registry.IsCurrent(7, second.ID) || registry.Remove(7, first.ID) {
		t.Fatal("stale session changed current ownership")
	}
	if !registry.Remove(7, second.ID) {
		t.Fatal("current session was not removed")
	}
}

func TestCredentialSpecificDisconnectAndScheduledExpiry(t *testing.T) {
	registry := NewRegistry()
	session := registry.Register(context.Background(), RegisterInput{ServerID: 12, NodeUUID: "node-c", CredentialID: 7})
	if registry.DisconnectCredential(12, 8, ErrSessionRevoked) {
		t.Fatal("a different credential disconnected the active session")
	}
	if !registry.ExpireCredentialAt(12, 7, time.Now().Add(20*time.Millisecond)) {
		t.Fatal("active credential expiry was not scheduled")
	}
	select {
	case <-session.Context().Done():
		if !errors.Is(context.Cause(session.Context()), ErrCredentialExpired) {
			t.Fatalf("credential expiry cause = %v", context.Cause(session.Context()))
		}
	case <-time.After(time.Second):
		t.Fatal("credential expiry did not disconnect the session")
	}
	if _, ok := registry.Get(12); ok {
		t.Fatal("expired credential session remains registered")
	}

	reconnected := registry.Register(context.Background(), RegisterInput{ServerID: 12, NodeUUID: "node-c", CredentialID: 9})
	if registry.ExpireCredentialAt(12, 7, time.Now()) {
		t.Fatal("stale credential expiry attached to the reconnected session")
	}
	if !registry.DisconnectCredential(12, 9, ErrSessionRevoked) {
		t.Fatal("matching credential did not disconnect the session")
	}
	<-reconnected.Context().Done()
	if !errors.Is(context.Cause(reconnected.Context()), ErrSessionRevoked) {
		t.Fatalf("credential revocation cause = %v", context.Cause(reconnected.Context()))
	}
}

func TestSendAndCredentialRevocation(t *testing.T) {
	registry := NewRegistry()
	session := registry.Register(context.Background(), RegisterInput{ServerID: 9, NodeUUID: "node-b"})
	frame := &nodev1.SystemControlFrame{Payload: &nodev1.SystemControlFrame_Ping{Ping: &nodev1.Ping{Nonce: "n"}}}
	if err := registry.Send(context.Background(), 9, frame); err != nil {
		t.Fatal(err)
	}
	if got := <-session.Outgoing(); got.GetPing().GetNonce() != "n" {
		t.Fatalf("sent frame = %#v", got)
	}
	if !registry.DisconnectServer(9, ErrSessionRevoked) {
		t.Fatal("active session was not disconnected")
	}
	<-session.Context().Done()
	if !errors.Is(context.Cause(session.Context()), ErrSessionRevoked) {
		t.Fatalf("revocation cause = %v", context.Cause(session.Context()))
	}
	if err := registry.Send(context.Background(), 9, frame); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("send after revocation = %v", err)
	}
}

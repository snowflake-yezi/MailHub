package nodesession

import (
	"context"
	"errors"
	"testing"

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

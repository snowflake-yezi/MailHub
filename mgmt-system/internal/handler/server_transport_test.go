package handler

import (
	"testing"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
)

func TestValidateTransportSwitchRequiresLiveReadyControlSession(t *testing.T) {
	now := time.Now().UTC()
	uuid := "node-uuid"
	base := &model.MailServer{NodeUUID: &uuid, ConnectionState: model.ConnectionConnected, ReadinessState: model.ReadinessReady, LeaseExpiresAt: ptrTime(now.Add(time.Minute))}

	if err := validateTransportSwitch(base, model.TransportDual, true, now); err != nil {
		t.Fatalf("dual transition rejected: %v", err)
	}
	if err := validateTransportSwitch(base, model.TransportControlStream, true, now); err != nil {
		t.Fatalf("control_stream transition rejected: %v", err)
	}

	for _, test := range []struct {
		name string
		edit func(*model.MailServer)
	}{
		{name: "missing uuid", edit: func(server *model.MailServer) { server.NodeUUID = nil }},
		{name: "disconnected", edit: func(server *model.MailServer) { server.ConnectionState = model.ConnectionDisconnected }},
		{name: "expired lease", edit: func(server *model.MailServer) { server.LeaseExpiresAt = ptrTime(now.Add(-time.Second)) }},
		{name: "not ready", edit: func(server *model.MailServer) { server.ReadinessState = model.ReadinessDegraded }},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := *base
			test.edit(&server)
			if err := validateTransportSwitch(&server, model.TransportDual, true, now); err == nil {
				t.Fatal("invalid transition was accepted")
			}
		})
	}
}

func TestValidateTransportSwitchHonorsLegacyDisablement(t *testing.T) {
	if err := validateTransportSwitch(&model.MailServer{}, model.TransportLegacyHTTP, false, time.Now()); err == nil {
		t.Fatal("legacy rollback was accepted while legacy transport was disabled")
	}
	uuid := "node-uuid"
	server := &model.MailServer{NodeUUID: &uuid, ConnectionState: model.ConnectionConnected, ReadinessState: model.ReadinessReady, LeaseExpiresAt: ptrTime(time.Now().Add(time.Minute))}
	if err := validateTransportSwitch(server, model.TransportDual, false, time.Now()); err == nil {
		t.Fatal("dual transition was accepted while legacy fallback was disabled")
	}
	if err := validateTransportSwitch(server, model.TransportControlStream, false, time.Now()); err != nil {
		t.Fatalf("control_stream transition rejected with legacy disabled: %v", err)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

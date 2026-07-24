package model

import (
	"testing"
	"time"
)

func TestApplyLegacyNodeDefaultsMapsCombinedStatus(t *testing.T) {
	tests := []struct {
		status     string
		readiness  string
		allocation string
	}{
		{status: "healthy", readiness: ReadinessReady, allocation: AllocationActive},
		{status: "degraded", readiness: ReadinessDegraded, allocation: AllocationActive},
		{status: "down", readiness: ReadinessFailed, allocation: AllocationActive},
		{status: "draining", readiness: ReadinessReady, allocation: AllocationDraining},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			server := MailServer{Status: tt.status}
			server.ApplyLegacyNodeDefaults()
			if server.EnrollmentState != EnrollmentLegacyApproved || server.ConnectionState != ConnectionUnknown ||
				server.ReadinessState != tt.readiness || server.AllocationState != tt.allocation ||
				server.TransportMode != TransportLegacyHTTP {
				t.Fatalf("legacy defaults = %+v", server)
			}
		})
	}
}

func TestIsAllocatableStateUsesTransportSpecificConnectionRules(t *testing.T) {
	now := time.Now()
	lease := now.Add(time.Minute)
	base := MailServer{
		Status: "healthy", Capacity: 10, CurrentLoad: 1,
		EnrollmentState: EnrollmentApproved, ReadinessState: ReadinessReady,
		AllocationState: AllocationActive,
	}

	legacy := base
	legacy.TransportMode = TransportLegacyHTTP
	legacy.ConnectionState = ConnectionUnknown
	if !legacy.IsAllocatableState(now) {
		t.Fatal("healthy legacy node should remain allocatable without a stream session")
	}

	stream := base
	stream.TransportMode = TransportControlStream
	stream.ConnectionState = ConnectionConnected
	stream.LeaseExpiresAt = &lease
	if !stream.IsAllocatableState(now) {
		t.Fatal("connected control stream with a valid lease should be allocatable")
	}
	expired := now.Add(-time.Second)
	stream.LeaseExpiresAt = &expired
	if stream.IsAllocatableState(now) {
		t.Fatal("expired control stream lease must stop allocation")
	}

	disabled := legacy
	disabled.AllocationState = AllocationDisabled
	if disabled.IsAllocatableState(now) {
		t.Fatal("disabled node must not be allocatable")
	}
}

func TestApplyLegacyAdminStatusKeepsNewDimensionsCoherent(t *testing.T) {
	server := MailServer{TransportMode: TransportLegacyHTTP, Status: "draining", ReadinessState: ReadinessReady, AllocationState: AllocationDraining}
	server.ApplyLegacyAdminStatus("healthy")
	if server.ReadinessState != ReadinessReady || server.AllocationState != AllocationActive {
		t.Fatalf("resumed legacy node = %+v", server)
	}
	server.ApplyLegacyAdminStatus("down")
	if server.ReadinessState != ReadinessFailed || server.AllocationState != AllocationActive {
		t.Fatalf("down legacy node = %+v", server)
	}
}

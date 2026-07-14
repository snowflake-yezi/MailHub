package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ticket/email-mail-node/internal/config"
	"github.com/ticket/email-mail-node/internal/forward"
)

func TestClampHeartbeat(t *testing.T) {
	tests := []struct {
		v, fallback, want int
	}{
		{30, 60, 30},   // 合法值原样返回
		{5, 60, 5},     // 下界
		{600, 60, 600}, // 上界
		{0, 60, 60},    // 非法 → fallback
		{-1, 60, 60},   // 负值 → fallback
		{4, 60, 60},    // 下界以下 → fallback
		{601, 60, 60},  // 上界以上 → fallback
	}
	for _, tc := range tests {
		if got := clampHeartbeat(tc.v, tc.fallback, nil); got != tc.want {
			t.Fatalf("clampHeartbeat(%d, %d) = %d, want %d", tc.v, tc.fallback, got, tc.want)
		}
	}
}

func TestRuntimeConfigSnapshotContract(t *testing.T) {
	values := runtimeConfigSnapshotValues(config.NewRemoteConfig("", ""), forward.ForwardConfig{
		ScanInterval: 5, MaxEmailSize: 10485760, BodyPreviewSize: 65536, TargetAddress: "union@example.com",
	}, 24*time.Hour)
	want := []string{
		"forward.scan_interval", "forward.max_email_size", "forward.body_preview_size", "forward.target_address",
		"forward.smtp_dial_timeout", "forward.tls_insecure_skip", "forward.tls_min_version",
		"lifecycle.trash_retention_hours", "lifecycle.gc_interval_minutes", "lifecycle.drain_timeout_minutes", "lifecycle.drain_poll_interval_ms",
	}
	if len(values) != len(want) {
		t.Fatalf("snapshot keys = %d, want %d", len(values), len(want))
	}
	for _, key := range want {
		if _, ok := values[key]; !ok {
			t.Fatalf("snapshot provider missing %s", key)
		}
	}
}

func TestStartDiscoveryRetryRecoversAfterManagementStarts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts atomic.Int32
	discovered := make(chan uint64, 1)
	go startDiscoveryRetry(ctx, time.Millisecond, func() (uint64, error) {
		if attempts.Add(1) < 3 {
			return 0, errors.New("management unavailable")
		}
		return 42, nil
	}, func(nodeID uint64) {
		discovered <- nodeID
	})

	select {
	case nodeID := <-discovered:
		if nodeID != 42 {
			t.Fatalf("node ID = %d, want 42", nodeID)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery retry did not recover")
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestNewBootIdentityChangesPerProcessStart(t *testing.T) {
	first, firstStarted := newBootIdentity()
	second, secondStarted := newBootIdentity()
	if first == "" || second == "" || first == second {
		t.Fatalf("boot IDs = %q/%q, want distinct non-empty values", first, second)
	}
	if firstStarted.IsZero() || secondStarted.IsZero() {
		t.Fatal("started_at must be populated")
	}
}

func TestStartPeriodicSnapshotReportsUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := make(chan struct{}, 2)
	go startPeriodicSnapshot(ctx, time.Millisecond, func() error {
		calls <- struct{}{}
		return nil
	})
	select {
	case <-calls:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("periodic snapshot was not reported")
	}
}

package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodetransport"
	"github.com/ticket/email-mgmt-system/internal/store"
)

// Dynamic config keys (was hardcoded constants).
const (
	cfgProbeInterval    = "healthcheck.probe_interval_seconds"
	cfgProbeTimeout     = "healthcheck.probe_timeout_seconds"
	cfgDegradeThreshold = "healthcheck.degrade_threshold"
	cfgDownThreshold    = "healthcheck.down_threshold"
	cfgHeartbeatTimeout = "healthcheck.heartbeat_timeout_seconds"
)

type Scheduler struct {
	store        *store.Store
	transport    nodetransport.NodeTransport
	interval     time.Duration
	probeTimeout time.Duration
}

func NewScheduler(s *store.Store, transport nodetransport.NodeTransport, interval, probeTimeout time.Duration) *Scheduler {
	if interval <= 0 {
		interval = time.Duration(s.GetConfigInt(cfgProbeInterval, 30)) * time.Second
	}
	if probeTimeout <= 0 {
		probeTimeout = time.Duration(s.GetConfigInt(cfgProbeTimeout, 5)) * time.Second
	}
	return &Scheduler{
		store:        s,
		transport:    transport,
		interval:     interval,
		probeTimeout: probeTimeout,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	s.ProbeAll()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.ProbeAll()
		}
	}
}

func (s *Scheduler) ProbeAll() {
	servers, err := s.store.ListServers()
	if err != nil {
		log.Printf("healthcheck: list servers failed: %v", err)
		return
	}
	for i := range servers {
		if err := s.probeOne(&servers[i]); err != nil {
			log.Printf("healthcheck: probe server=%s(%d) failed: %v", servers[i].Name, servers[i].ID, err)
		}
	}
}

func (s *Scheduler) probeOne(srv *model.MailServer) error {
	// Control-only nodes are healthy through their active session, lease, and
	// self-reported readiness. Dialing their legacy API would reintroduce the
	// reverse network dependency that the control channel removes.
	if srv.TransportMode == model.TransportControlStream {
		return nil
	}
	ok, err := s.probeHTTP(srv)
	now := time.Now()
	failCount := srv.ProbeFailCount
	status := srv.Status

	// Read thresholds dynamically (cached, 30s TTL) so hot-reload works.
	degradeThresh := s.store.GetConfigInt(cfgDegradeThreshold, 3)
	downThresh := s.store.GetConfigInt(cfgDownThreshold, 5)
	hbTimeout := time.Duration(s.store.GetConfigInt(cfgHeartbeatTimeout, 90)) * time.Second

	if ok {
		failCount = 0
		if status == "down" || status == "degraded" {
			status = "healthy"
		}
		return s.store.UpdateServerProbe(srv.ID, failCount, status, srv.TransportMode)
	}

	failCount++
	if failCount >= downThresh {
		status = "down"
	} else if failCount >= degradeThresh {
		status = "degraded"
	}

	if srv.LastHeartbeat != nil && now.Sub(*srv.LastHeartbeat) > hbTimeout {
		if status == "healthy" {
			status = "degraded"
		}
		if failCount >= degradeThresh {
			status = "down"
		}
	}

	if updateErr := s.store.UpdateServerProbe(srv.ID, failCount, status, srv.TransportMode); updateErr != nil {
		return updateErr
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("unhealthy response")
}

func (s *Scheduler) probeHTTP(server *model.MailServer) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.probeTimeout)
	defer cancel()
	resp, err := s.transport.Probe(ctx, nodetransport.Target{NodeID: server.ID, APIHost: server.APIHost})
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("status=%d body=%s", resp.StatusCode, string(resp.Body))
	}

	var parsed struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return false, err
	}
	if parsed.Code != 0 {
		return false, fmt.Errorf("code=%d body=%s", parsed.Code, string(resp.Body))
	}
	return true, nil
}

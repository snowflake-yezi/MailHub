package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
)

const (
	defaultInterval     = 30 * time.Second
	defaultProbeTimeout = 5 * time.Second
	degradeThreshold    = 3
	downThreshold       = 5
	heartbeatTimeout    = 90 * time.Second
)

type Scheduler struct {
	store        *store.Store
	client       *http.Client
	sharedSecret string
	interval     time.Duration
}

func NewScheduler(s *store.Store, sharedSecret string, interval, probeTimeout time.Duration) *Scheduler {
	if interval <= 0 {
		interval = defaultInterval
	}
	if probeTimeout <= 0 {
		probeTimeout = defaultProbeTimeout
	}
	return &Scheduler{
		store:        s,
		client:       &http.Client{Timeout: probeTimeout},
		sharedSecret: sharedSecret,
		interval:     interval,
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
	ok, err := s.probeHTTP(srv.APIHost)
	now := time.Now()
	failCount := srv.ProbeFailCount
	status := srv.Status

	if ok {
		failCount = 0
		if status == "down" || status == "degraded" {
			status = "healthy"
		}
		return s.store.UpdateServerProbe(srv.ID, failCount, status)
	}

	failCount++
	if failCount >= downThreshold {
		status = "down"
	} else if failCount >= degradeThreshold {
		status = "degraded"
	}

	if srv.LastHeartbeat != nil && now.Sub(*srv.LastHeartbeat) > heartbeatTimeout {
		if status == "healthy" {
			status = "degraded"
		}
		if failCount >= degradeThreshold {
			status = "down"
		}
	}

	if updateErr := s.store.UpdateServerProbe(srv.ID, failCount, status); updateErr != nil {
		return updateErr
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("unhealthy response")
}

func (s *Scheduler) probeHTTP(apiHost string) (bool, error) {
	url := "http://" + strings.TrimRight(apiHost, "/") + "/internal/health"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("X-Internal-Token", s.sharedSecret)

	resp, err := s.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("status=%d body=%s", resp.StatusCode, string(data))
	}

	var parsed struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return false, err
	}
	if parsed.Code != 0 {
		return false, fmt.Errorf("code=%d body=%s", parsed.Code, string(data))
	}
	return true, nil
}

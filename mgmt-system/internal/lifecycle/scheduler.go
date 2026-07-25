package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodetransport"
	"github.com/ticket/email-mgmt-system/internal/store"
)

// Dynamic config keys (was hardcoded constants).
const (
	cfgLifecycleInterval       = "lifecycle.schedule_interval_minutes"
	cfgLifecycleWatchdogMin    = "lifecycle.delete_watchdog_minutes"
	cfgLifecycleDeleteProbeSec = "healthcheck.probe_timeout_seconds" // reuse healthcheck probe timeout
	cfgTrashRetentionHours     = "lifecycle.trash_retention_hours"
	cfgGlobalRetentionDays     = "general.default_retention_days"
)

// Scheduler 负责邮箱生命周期后台任务：
// ① Watchdog: 扫描超时的 deleting 任务并重新下发 mail-node DELETE
// ② Purge: 扫描过期的 soft_deleted 邮箱并标记为 purged
type Scheduler struct {
	store            *store.Store
	transport        nodetransport.NodeTransport
	interval         time.Duration
	operationTimeout time.Duration
}

// NewScheduler 创建生命周期调度器。interval 为 0 时从 DB 配置读取（默认 5 分钟）。
func NewScheduler(s *store.Store, transport nodetransport.NodeTransport, interval time.Duration) *Scheduler {
	if interval <= 0 {
		interval = time.Duration(s.GetConfigInt(cfgLifecycleInterval, 5)) * time.Minute
	}
	probeTimeout := time.Duration(s.GetConfigInt(cfgLifecycleDeleteProbeSec, 10)) * time.Second
	return &Scheduler{
		store:            s,
		transport:        transport,
		interval:         interval,
		operationTimeout: probeTimeout,
	}
}

// Start 启动后台调度循环，启动时立即执行一次，之后按 interval 定时执行。
func (s *Scheduler) Start(ctx context.Context) {
	log.Println("[lifecycle] scheduler started")
	s.run()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("[lifecycle] scheduler stopped")
			return
		case <-ticker.C:
			s.run()
		}
	}
}

func (s *Scheduler) run() {
	s.watchdog()
	s.purgeExpiredMessages()
	s.purgeExpired()
}

func (s *Scheduler) purgeExpiredMessages() {
	mailboxes, err := s.store.ListActiveMailboxesForMessageRetention()
	if err != nil {
		log.Printf("[lifecycle] message retention: list active mailboxes failed: %v", err)
		return
	}
	groups := make(map[uint64][]nodetransport.RetentionItem)
	retentionDays := s.store.GetConfigInt(cfgGlobalRetentionDays, 30)
	if retentionDays <= 0 {
		retentionDays = 30
	}
	for _, mb := range mailboxes {
		groups[mb.ServerID] = append(groups[mb.ServerID], nodetransport.RetentionItem{EmailAddress: mb.EmailAddress, RetentionDays: retentionDays})
	}
	for serverID, items := range groups {
		srv, getErr := s.store.GetServer(serverID)
		if getErr != nil {
			log.Printf("[lifecycle] message retention: get server %d failed: %v", serverID, getErr)
			continue
		}
		deleted, callErr := s.callNodePurgeExpiredMessagesBatch(srv, items)
		if callErr != nil {
			log.Printf("[lifecycle] message retention: purge server %d failed: %v", serverID, callErr)
			continue
		}
		if deleted > 0 {
			log.Printf("[lifecycle] message retention: purged %d messages on server %d", deleted, serverID)
		}
	}
	servers, err := s.store.ListServers()
	if err != nil {
		log.Printf("[lifecycle] quarantine retention: list servers failed: %v", err)
		return
	}
	for _, server := range servers {
		expiredKeys, gcErr := s.callNodePurgeExpiredQuarantines(&server, retentionDays)
		if gcErr != nil {
			log.Printf("[lifecycle] quarantine retention: purge server %d failed: %v", server.ID, gcErr)
			continue
		}
		if err := s.store.MarkFilterQuarantinesExpired(server.ID, expiredKeys); err != nil {
			log.Printf("[lifecycle] quarantine retention: reconcile server %d failed: %v", server.ID, err)
		}
	}
}

func (s *Scheduler) callNodePurgeExpiredQuarantines(server *model.MailServer, retentionDays int) ([]string, error) {
	resp, err := s.transport.Execute(context.Background(), nodeTarget(server), nodetransport.QuarantineGC(retentionDays, s.operationTimeout))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream error: %d - %s", resp.StatusCode, string(resp.Body))
	}
	var result struct {
		Code int `json:"code"`
		Data struct {
			ExpiredKeys []string `json:"expired_keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &result); err != nil || result.Code != 0 {
		return nil, fmt.Errorf("invalid quarantine gc response")
	}
	return result.Data.ExpiredKeys, nil
}

// watchdog 扫描 deleting 超时的任务，重新向 mail-node 下发 DELETE。
// 超时阈值从 DB 配置读取（默认 15 分钟）。
func (s *Scheduler) watchdog() {
	timeout := time.Duration(s.store.GetConfigInt(cfgLifecycleWatchdogMin, 15)) * time.Minute
	stuck, err := s.store.FindStuckDeleting(timeout)
	if err != nil {
		log.Printf("[lifecycle] watchdog: find stuck deleting failed: %v", err)
		return
	}
	for _, mb := range stuck {
		log.Printf("[lifecycle] watchdog: retrying deletion for %s (id=%d, stuck since %v)",
			mb.EmailAddress, mb.ID, mb.DeleteRequestedAt)

		srv, err := s.store.GetServer(mb.ServerID)
		if err != nil {
			log.Printf("[lifecycle] watchdog: get server for %s failed: %v", mb.EmailAddress, err)
			continue
		}

		if err := s.callNodeDelete(srv, mb.EmailAddress); err != nil {
			log.Printf("[lifecycle] watchdog: retry delete %s failed: %v", mb.EmailAddress, err)
			continue
		}

		if err := s.store.ConfirmDeletion(mb.ID); err != nil {
			log.Printf("[lifecycle] watchdog: confirm deletion for %s failed: %v", mb.EmailAddress, err)
			continue
		}
		log.Printf("[lifecycle] watchdog: retry delete %s succeeded", mb.EmailAddress)
	}
}

// purgeExpired 扫描 soft_deleted 且已过保留期的邮箱，标记为 purged。
func (s *Scheduler) purgeExpired() {
	retention := time.Duration(s.store.GetConfigInt(cfgTrashRetentionHours, 24)) * time.Hour
	expired, err := s.store.FindExpiredSoftDeleted(retention)
	if err != nil {
		log.Printf("[lifecycle] purge: find expired failed: %v", err)
		return
	}
	for _, mb := range expired {
		log.Printf("[lifecycle] purge: marking %s (id=%d, recycled_at=%v, retention=%d days)",
			mb.EmailAddress, mb.ID, mb.RecycledAt, mb.RetentionDays)
		if err := s.store.MarkPurged(mb.ID); err != nil {
			log.Printf("[lifecycle] purge: mark purged %s failed: %v", mb.EmailAddress, err)
			continue
		}
		log.Printf("[lifecycle] purge: %s → purged", mb.EmailAddress)
	}
}

func (s *Scheduler) callNodePurgeExpiredMessagesBatch(server *model.MailServer, items []nodetransport.RetentionItem) (int, error) {
	resp, err := s.transport.Execute(context.Background(), nodeTarget(server), nodetransport.MessageRetentionPurge(items, s.operationTimeout))
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("upstream error: %d - %s", resp.StatusCode, string(resp.Body))
	}
	var result struct {
		Code int `json:"code"`
		Data struct {
			Deleted int `json:"deleted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}
	if result.Code != 0 {
		return 0, fmt.Errorf("node returned code %d", result.Code)
	}
	return result.Data.Deleted, nil
}

// callNodeDelete 向 mail-node 发送 DELETE 请求触发 MoveToTrash。
func (s *Scheduler) callNodeDelete(server *model.MailServer, email string) error {
	resp, err := s.transport.Execute(context.Background(), nodeTarget(server), nodetransport.MailboxDelete(email, s.operationTimeout))
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		body := string(resp.Body)
		// 防御：maildir 已在节点上不存在时，视为已删除（幂等）。
		// 正常路径由 mail-node MoveToTrash 幂等返回 200，此分支兜底旧版节点返回 500。
		if strings.Contains(body, "mailbox not found") || strings.Contains(body, "already deleted") {
			return nil
		}
		return fmt.Errorf("upstream error: %d - %s", resp.StatusCode, body)
	}
	return nil
}

func nodeTarget(server *model.MailServer) nodetransport.Target {
	return nodetransport.Target{NodeID: server.ID, APIHost: server.APIHost, TransportMode: server.TransportMode}
}

// Ensure model is used (for future reference to MailboxAccount fields).
var _ = (*model.MailboxAccount)(nil)

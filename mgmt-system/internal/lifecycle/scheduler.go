package lifecycle

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
)

const (
	defaultInterval    = 5 * time.Minute
	deleteTimeout      = 15 * time.Minute // Watchdog: 超过此时间未完成的 deleting 任务重新下发
	deleteProbeTimeout = 10 * time.Second
)

// Scheduler 负责邮箱生命周期后台任务：
// ① Watchdog: 扫描超时的 deleting 任务并重新下发 mail-node DELETE
// ② Purge: 扫描过期的 soft_deleted 邮箱并标记为 purged
type Scheduler struct {
	store        *store.Store
	client       *http.Client
	sharedSecret string
	interval     time.Duration
}

// NewScheduler 创建生命周期调度器。interval 为 0 时使用默认 5 分钟。
func NewScheduler(s *store.Store, sharedSecret string, interval time.Duration) *Scheduler {
	if interval <= 0 {
		interval = defaultInterval
	}
	return &Scheduler{
		store:        s,
		client:       &http.Client{Timeout: deleteProbeTimeout},
		sharedSecret: sharedSecret,
		interval:     interval,
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
	s.purgeExpired()
}

// watchdog 扫描 deleting 超时（>15min）的任务，重新向 mail-node 下发 DELETE。
func (s *Scheduler) watchdog() {
	stuck, err := s.store.FindStuckDeleting(deleteTimeout)
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

		if err := s.callNodeDelete(srv.APIHost, mb.EmailAddress); err != nil {
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
	expired, err := s.store.FindExpiredSoftDeleted()
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

// callNodeDelete 向 mail-node 发送 DELETE 请求触发 MoveToTrash。
func (s *Scheduler) callNodeDelete(apiHost, email string) error {
	url := fmt.Sprintf("http://%s/internal/mailboxes/%s", apiHost, email)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-Internal-Token", s.sharedSecret)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upstream error: %d - %s", resp.StatusCode, string(data))
	}
	return nil
}

// Ensure model is used (for future reference to MailboxAccount fields).
var _ = (*model.MailboxAccount)(nil)

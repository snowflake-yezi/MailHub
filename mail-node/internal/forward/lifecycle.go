package forward

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ticket/email-mail-node/internal/mailbox"
)

// Lifecycle 邮箱生命周期管理：安全软删除 + 垃圾回收 + 重启对账
type Lifecycle struct {
	mgr            *mailbox.Manager
	fwdSvc         *Service // for ActiveJobs() check
	trashBase      string   // <maildirBase>/.trash
	trashRetention time.Duration
}

// NewLifecycle 创建生命周期管理器
func NewLifecycle(mgr *mailbox.Manager, fwdSvc *Service) *Lifecycle {
	trashBase := filepath.Join(mgr.MaildirBase(), ".trash")
	return &Lifecycle{
		mgr:            mgr,
		fwdSvc:         fwdSvc,
		trashBase:      trashBase,
		trashRetention: 24 * time.Hour,
	}
}

// MoveToTrash atomically moves a mailbox Maildir to .trash/<email>-<unix_ts>/.
// Protocol per forwarding-design.md §9.1:
//
//	① Remove from Postfix virtual map + Dovecot users.conf (reject new mail)
//	② Wait for active forwards to drain (max 5min)
//	③ os.Rename to .trash/
//
// Returns the trash path on success.
func (l *Lifecycle) MoveToTrash(email string) (string, error) {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid email: %s", email)
	}
	localPart := parts[0]
	domain := parts[1]

	maildirPath := filepath.Join(l.mgr.MaildirBase(), domain, localPart)

	// Verify the mailbox directory exists
	if _, err := os.Stat(maildirPath); os.IsNotExist(err) {
		return "", fmt.Errorf("mailbox not found: %s", email)
	}

	// ① Remove from Postfix & Dovecot config files to reject new mail.
	// Future mail to this address will bounce rather than land in a
	// directory that's about to move.
	if err := l.mgr.RemoveFromConfigs(email); err != nil {
		log.Printf("[lifecycle] remove configs warning: %v", err)
		// Continue — the rename is the critical step
	}

	// ② Wait for active forwarding jobs to drain.
	// Any job already mid-forward still holds an fd on the file;
	// os.Rename won't break it, but we wait to keep the log clean.
	l.waitForActiveJobs(5 * time.Minute)

	// ③ Atomically move to .trash/
	if err := os.MkdirAll(l.trashBase, 0700); err != nil {
		return "", fmt.Errorf("mkdir .trash: %w", err)
	}

	timestamp := time.Now().Unix()
	trashName := fmt.Sprintf("%s-%d", localPart, timestamp)
	trashPath := filepath.Join(l.trashBase, trashName)

	if err := os.Rename(maildirPath, trashPath); err != nil {
		return "", fmt.Errorf("rename to trash: %w", err)
	}

	log.Printf("[lifecycle] moved to trash: %s → %s", maildirPath, trashPath)
	return trashPath, nil
}

// waitForActiveJobs blocks until fwdSvc.ActiveJobs() reaches 0 or timeout expires.
func (l *Lifecycle) waitForActiveJobs(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	pollInterval := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		if l.fwdSvc.ActiveJobs() == 0 {
			return // drained
		}
		time.Sleep(pollInterval)
	}
	log.Printf("[lifecycle] wait for active jobs timed out after %v (forcing continue)", timeout)
}

// StartGC starts a background goroutine that periodically purges
// directories in .trash/ older than the retention period (24h).
func (l *Lifecycle) StartGC(ctx context.Context) {
	go func() {
		// Run immediately on start, then every hour
		l.purgeExpiredTrash()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Println("[lifecycle] GC stopped")
				return
			case <-ticker.C:
				l.purgeExpiredTrash()
			}
		}
	}()
}

// purgeExpiredTrash removes trash directories older than the retention period.
func (l *Lifecycle) purgeExpiredTrash() {
	entries, err := os.ReadDir(l.trashBase)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[lifecycle] GC read .trash: %v", err)
		}
		return
	}

	cutoff := time.Now().Add(-l.trashRetention)
	purged := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		trashDir := filepath.Join(l.trashBase, entry.Name())

		// Parse timestamp from directory name: <localpart>-<unix_ts>
		ts := parseTrashTimestamp(entry.Name())
		if ts.IsZero() || ts.After(cutoff) {
			continue
		}

		if err := os.RemoveAll(trashDir); err != nil {
			log.Printf("[lifecycle] GC remove %s: %v", trashDir, err)
		} else {
			purged++
			log.Printf("[lifecycle] purged: %s", trashDir)
		}
	}

	if purged > 0 {
		log.Printf("[lifecycle] GC: purged %d directories", purged)
	}
}

// parseTrashTimestamp extracts the unix timestamp from a trash directory name.
// Format: <localpart>-<unix_ts>
func parseTrashTimestamp(name string) time.Time {
	idx := strings.LastIndex(name, "-")
	if idx < 0 {
		return time.Time{}
	}
	tsStr := name[idx+1:]
	var ts int64
	if _, err := fmt.Sscanf(tsStr, "%d", &ts); err != nil {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}

// DeletingTask represents a mailbox in DELETING status from mgmt-system.
type DeletingTask struct {
	ID           uint64 `json:"id"`
	EmailAddress string `json:"email_address"`
}

// syncResponse is the mgmt-system sync endpoint response envelope.
type syncResponse struct {
	Code int    `json:"code"`
	Data []DeletingTask `json:"data"`
}

// PullDeletingTasks queries mgmt-system on boot for DELETING-status mailboxes
// belonging to this node. Each task is resumed per the safe-deletion protocol.
//
// GET /api/v1/internal/sync/deleting?server_id=<nodeID>
func (l *Lifecycle) PullDeletingTasks(mgmtURL string, nodeID uint64) {
	url := fmt.Sprintf("%s/api/v1/internal/sync/deleting?server_id=%d", mgmtURL, nodeID)

	log.Printf("[lifecycle] pull deleting tasks from %s", url)

	resp, err := http.Get(url)
	if err != nil {
		log.Printf("[lifecycle] pull sync failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[lifecycle] pull sync unexpected status %d: %s", resp.StatusCode, string(body))
		return
	}

	var apiResp syncResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		log.Printf("[lifecycle] pull sync decode: %v", err)
		return
	}

	for _, task := range apiResp.Data {
		log.Printf("[lifecycle] resuming deletion: %s", task.EmailAddress)
		trashPath, err := l.MoveToTrash(task.EmailAddress)
		if err != nil {
			log.Printf("[lifecycle] resume deletion failed for %s: %v", task.EmailAddress, err)
		} else {
			log.Printf("[lifecycle] resume deletion ok: %s → %s", task.EmailAddress, trashPath)
		}
	}

	if len(apiResp.Data) == 0 {
		log.Println("[lifecycle] pull sync: no pending deleting tasks")
	}
}

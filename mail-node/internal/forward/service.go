package forward

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ticket/email-mail-node/internal/filter"
	"github.com/ticket/email-mail-node/internal/mailbox"
)

// Action mirrors filter.Action for forward-specific labels.
type Action = filter.Action

const (
	ActionPass  = filter.ActionPass
	ActionBlock = filter.ActionBlock
	ActionFlag  = filter.ActionFlag
)

// ForwardConfig 转发配置
type ForwardConfig struct {
	SMTPHost      string
	SMTPUser      string
	SMTPPass      string
	TargetAddress string
	SubjectPrefix string
	ScanInterval  int   // seconds, default 5
	MaxEmailSize  int64 // bytes, default 10MB
}

// Service 邮件转发服务
type Service struct {
	cfg    ForwardConfig
	engine *filter.Engine
	mgr    *mailbox.Manager

	mu       sync.Mutex
	activeJobs int // count of files currently being processed
}

// New 创建转发服务
func New(cfg ForwardConfig, engine *filter.Engine, mgr *mailbox.Manager) *Service {
	if cfg.ScanInterval <= 0 {
		cfg.ScanInterval = 5
	}
	if cfg.MaxEmailSize <= 0 {
		cfg.MaxEmailSize = maxEmailSizeDefault
	}
	return &Service{
		cfg:    cfg,
		engine: engine,
		mgr:    mgr,
	}
}

// ActiveJobs returns the number of currently processing files.
func (s *Service) ActiveJobs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeJobs
}

// Start 启动后台扫描循环（阻塞，应放在 goroutine 中调用）
func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.cfg.ScanInterval) * time.Second)
	defer ticker.Stop()

	log.Printf("[forward] service started (scan_interval=%ds, max_size=%dMB, target=%s)",
		s.cfg.ScanInterval, s.cfg.MaxEmailSize/(1024*1024), s.cfg.TargetAddress)

	// Immediate first scan
	s.scanAndLog()

	for {
		select {
		case <-ctx.Done():
			log.Println("[forward] service stopped")
			return
		case <-ticker.C:
			s.scanAndLog()
		}
	}
}

func (s *Service) scanAndLog() {
	processed, errors := s.ScanOnce()
	if processed > 0 || errors > 0 {
		log.Printf("[forward] scan: processed=%d errors=%d", processed, errors)
	}
}

// ScanOnce 单次扫描所有邮箱 new/ 目录，处理新邮件。
// 返回 (processed, errors)。
func (s *Service) ScanOnce() (processed int, errors int) {
	// Discover all maildirs: <base>/<domain>/<user>/new/
	base := s.mgr.MaildirBase()

	domains, err := os.ReadDir(base)
	if err != nil {
		log.Printf("[forward] read base dir %s: %v", base, err)
		return 0, 1
	}

	for _, dEnt := range domains {
		if !dEnt.IsDir() || strings.HasPrefix(dEnt.Name(), ".") {
			continue // skip .trash and dotfiles
		}

		domainDir := filepath.Join(base, dEnt.Name())
		users, err := os.ReadDir(domainDir)
		if err != nil {
			continue
		}

		for _, uEnt := range users {
			if !uEnt.IsDir() {
				continue
			}

			newDir := filepath.Join(domainDir, uEnt.Name(), "new")
			files, err := os.ReadDir(newDir)
			if err != nil {
				continue // new/ doesn't exist for this user
			}

			// Build the source address for labeling
			sourceAddr := uEnt.Name() + "@" + dEnt.Name()

			for _, fEnt := range files {
				if fEnt.IsDir() {
					continue
				}
				filePath := filepath.Join(newDir, fEnt.Name())

				s.mu.Lock()
				s.activeJobs++
				s.mu.Unlock()

				if err := s.processFile(filePath, sourceAddr); err != nil {
					log.Printf("[forward] %s: %v", sourceAddr, err)
					errors++
				} else {
					processed++
				}

				s.mu.Lock()
				s.activeJobs--
				s.mu.Unlock()
			}
		}
	}

	return processed, errors
}

// processFile handles a single file in new/: filter → forward or skip → move to cur/.
func (s *Service) processFile(filePath, sourceAddr string) error {
	start := time.Now()

	// 1. Read headers + body preview for filtering
	headers, bodyPreview, err := readForFiltering(filePath, s.cfg.MaxEmailSize)
	if err != nil {
		// Oversized or unparseable → move to cur/ to avoid re-scan
		moveToCur(filePath)
		return fmt.Errorf("parse: %w", err)
	}

	// 2. Anti-loop: detect X-Forwarded-By
	if strings.Contains(headers["x-forwarded-by"], "mail-node") {
		// Already forwarded by us (shouldn't hit for new/, but safe)
		moveToCur(filePath)
		log.Printf("[forward] skipped (loop guard): %s → not re-forwarding", sourceAddr)
		return nil
	}

	// 3. Filter decision
	msg := &filter.EmailMessage{
		From:    headers["from"],
		To:      headers["to"],
		Subject: headers["subject"],
		Body:    bodyPreview,
	}
	result := s.engine.Filter(msg)

	// 4. Block → keep original for LLM API, move to cur/ so we don't re-scan
	if result.Action == filter.ActionBlock {
		moveToCur(filePath)
		log.Printf("[forward] blocked: from=%s to=%s rule=%d reason=%s",
			msg.From, msg.To, result.RuleID, result.Reason)
		return nil
	}

	// 5. Forward (pass or flag) via SMTP
	newSubject := buildSubject(s.cfg.SubjectPrefix, sourceAddr, result.Action, headers["subject"])

	if err := streamToSMTP(s.cfg, filePath, newSubject, sourceAddr); err != nil {
		// SMTP failed → leave in new/ for next-scan retry (natural backoff)
		return fmt.Errorf("smtp: %w", err)
	}

	// 6. Forward success → move to cur/ (Maildir Seen semantics)
	moveToCur(filePath)

	elapsed := time.Since(start).Milliseconds()
	log.Printf("[forward] forwarded: %s → union (action=%s, rule=%d, latency=%dms)",
		sourceAddr, result.Action, result.RuleID, elapsed)

	return nil
}

// moveToCur moves a file from new/ to the sibling cur/ directory.
// On failure, the file stays in new/ and gets retried next scan.
func moveToCur(filePath string) {
	dir := filepath.Dir(filePath)      // .../new
	base := filepath.Base(filePath)    // <timestamp>.<pid>.<host>
	curDir := filepath.Join(filepath.Dir(dir), "cur")

	// Ensure cur/ exists
	if err := os.MkdirAll(curDir, 0700); err != nil {
		log.Printf("[forward] mkdir cur %s: %v", curDir, err)
		return
	}

	// Append Maildir info suffix: ":2,S" = Seen flag
	dest := filepath.Join(curDir, base+":2,S")

	if err := os.Rename(filePath, dest); err != nil {
		// If Rename fails (cross-device), try copy + remove
		if err := copyAndRemove(filePath, dest); err != nil {
			log.Printf("[forward] move to cur failed: %v", err)
		}
	}
}

// copyAndRemove is a fallback for os.Rename across filesystem boundaries.
func copyAndRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := in.WriteTo(out); err != nil {
		os.Remove(dst) // clean up partial
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	// Owner/perms best-effort
	if fi, err := in.Stat(); err == nil {
		os.Chmod(dst, fi.Mode())
	}

	return os.Remove(src)
}

// DiscoverMaildirs returns all (domain, user) pairs found under the maildir base.
// Used by lifecycle and other modules that need to enumerate mailboxes.
func (s *Service) DiscoverMaildirs() []MaildirEntry {
	base := s.mgr.MaildirBase()
	var entries []MaildirEntry

	domains, err := os.ReadDir(base)
	if err != nil {
		return entries
	}

	for _, dEnt := range domains {
		if !dEnt.IsDir() || strings.HasPrefix(dEnt.Name(), ".") {
			continue
		}
		domain := dEnt.Name()
		domainDir := filepath.Join(base, domain)

		users, err := os.ReadDir(domainDir)
		if err != nil {
			continue
		}

		for _, uEnt := range users {
			if !uEnt.IsDir() {
				continue
			}
			entries = append(entries, MaildirEntry{
				Domain:    domain,
				User:      uEnt.Name(),
				EmailAddr: uEnt.Name() + "@" + domain,
				Path:      filepath.Join(domainDir, uEnt.Name()),
			})
		}
	}

	return entries
}

// MaildirEntry represents a discovered mailbox under the Maildir base.
type MaildirEntry struct {
	Domain    string
	User      string
	EmailAddr string
	Path      string
}

// Ensure interface satisfaction
var _ fs.FileInfo = nil

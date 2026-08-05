package forward

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mail-node/internal/config"
	"github.com/ticket/email-mail-node/internal/filter"
	"github.com/ticket/email-mail-node/internal/filterdecision"
	"github.com/ticket/email-mail-node/internal/filteroutbox"
	"github.com/ticket/email-mail-node/internal/filterquarantine"
	"github.com/ticket/email-mail-node/internal/mailbox"
	"github.com/ticket/email-mail-node/internal/mailparse"
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

	// ── 以下为远程动态配置项（0 表示使用默认值） ──
	BodyPreviewSize   int64         // 正文预览大小（bytes），默认 64KB
	SMTPDialTimeout   time.Duration // SMTP 拨号超时，默认 15s
	TLSInsecureSkip   bool          // 跳过 TLS 证书验证，默认 true
	TLSMinVersion     int           // TLS 最低版本（12=1.2），默认 12
	TrashRetention    time.Duration // 回收站保留，默认 24h
	GCInterval        time.Duration // GC 间隔，默认 1h
	DrainTimeout      time.Duration // 删除前排空超时，默认 5min
	DrainPollInterval time.Duration // 排空轮询间隔，默认 500ms
}

// Service 邮件转发服务
type Service struct {
	cfg        ForwardConfig
	engine     *filter.Engine
	mgr        *mailbox.Manager
	remoteCfg  *config.RemoteConfig // 动态配置，用于热加载转发目标 target_address
	scanReset  chan struct{}
	decision   *filterdecision.Engine
	outbox     *filteroutbox.Queue
	quarantine *filterquarantine.Store

	mu         sync.Mutex
	activeJobs int // count of files currently being processed
}

func (s *Service) ConfigurePolicyRuntime(engine *filterdecision.Engine, outbox *filteroutbox.Queue) {
	s.decision = engine
	s.outbox = outbox
}

func (s *Service) ConfigureQuarantine(store *filterquarantine.Store) { s.quarantine = store }

// New 创建转发服务。remoteCfg 用于热加载 forward.target_address（可为 nil）。
func New(cfg ForwardConfig, engine *filter.Engine, mgr *mailbox.Manager, remoteCfg *config.RemoteConfig) *Service {
	if cfg.ScanInterval <= 0 {
		cfg.ScanInterval = 5
	}
	if cfg.MaxEmailSize <= 0 {
		cfg.MaxEmailSize = maxEmailSizeDefault
	}
	if cfg.BodyPreviewSize <= 0 {
		cfg.BodyPreviewSize = bodyPreviewDefault
	}
	if cfg.SMTPDialTimeout <= 0 {
		cfg.SMTPDialTimeout = 15 * time.Second
	}
	if cfg.TLSMinVersion <= 0 {
		cfg.TLSMinVersion = 12 // TLS 1.2
	}
	if cfg.TrashRetention <= 0 {
		cfg.TrashRetention = 24 * time.Hour
	}
	if cfg.GCInterval <= 0 {
		cfg.GCInterval = 1 * time.Hour
	}
	if cfg.DrainTimeout <= 0 {
		cfg.DrainTimeout = 5 * time.Minute
	}
	if cfg.DrainPollInterval <= 0 {
		cfg.DrainPollInterval = 500 * time.Millisecond
	}
	return &Service{
		cfg:       cfg,
		engine:    engine,
		mgr:       mgr,
		remoteCfg: remoteCfg,
		scanReset: make(chan struct{}, 1),
	}
}

func (s *Service) currentScanInterval() time.Duration {
	seconds := s.cfg.ScanInterval
	if s.remoteCfg != nil {
		seconds = s.remoteCfg.GetInt("forward.scan_interval", seconds)
	}
	if seconds <= 0 {
		seconds = 5
	}
	return time.Duration(seconds) * time.Second
}

func (s *Service) currentMaxEmailSize() int64 {
	if s.remoteCfg == nil {
		return s.cfg.MaxEmailSize
	}
	return s.remoteCfg.GetInt64("forward.max_email_size", s.cfg.MaxEmailSize)
}

func (s *Service) currentBodyPreviewSize() int64 {
	if s.remoteCfg == nil {
		return s.cfg.BodyPreviewSize
	}
	return s.remoteCfg.GetInt64("forward.body_preview_size", s.cfg.BodyPreviewSize)
}

func (s *Service) ApplyConfig(current, next map[string]string) error {
	return ValidateForwardConfig(current, next)
}

func ValidateForwardConfig(_, next map[string]string) error {
	for _, key := range []string{"forward.scan_interval", "forward.max_email_size", "forward.body_preview_size", "forward.smtp_dial_timeout"} {
		if value, ok := next[key]; ok {
			number, err := strconv.ParseInt(value, 10, 64)
			if err != nil || number <= 0 {
				return fmt.Errorf("%s must be a positive integer", key)
			}
		}
	}
	if value, ok := next["forward.tls_min_version"]; ok && value != "12" && value != "13" {
		return fmt.Errorf("forward.tls_min_version must be 12 or 13")
	}
	if value, ok := next["forward.tls_insecure_skip"]; ok {
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("forward.tls_insecure_skip must be a boolean")
		}
	}
	if value, ok := next["forward.target_address"]; ok {
		address, err := mail.ParseAddress(strings.TrimSpace(value))
		if err != nil || address.Address != strings.TrimSpace(value) {
			return fmt.Errorf("forward.target_address must be a valid email address")
		}
	}
	return nil
}

func (s *Service) AfterApplyConfig(_, _ uint64) {
	select {
	case s.scanReset <- struct{}{}:
	default:
	}
}

// currentTarget 解析当前生效的转发目标地址：优先读动态配置 forward.target_address，
// 回退到启动配置。每次发送前调用，支持后台热加载（mgmt 改目标后 reload 即时生效）。
func (s *Service) currentTarget() string {
	if s.remoteCfg != nil {
		if t := s.remoteCfg.GetString("forward.target_address", ""); t != "" {
			return t
		}
	}
	return s.cfg.TargetAddress
}

// currentSMTPAuth 解析当前转发 SMTP 认证账号：优先读动态配置中的 active 集成邮箱凭据，
// 回退到启动配置。每次发送前调用，支持后台切换集成邮箱池后热加载生效。
func (s *Service) currentSMTPAuth() (string, string) {
	if s.remoteCfg != nil {
		user := s.remoteCfg.GetString("forward.smtp_user", s.cfg.SMTPUser)
		pass := s.remoteCfg.GetString("forward.smtp_pass", s.cfg.SMTPPass)
		return user, pass
	}
	return s.cfg.SMTPUser, s.cfg.SMTPPass
}

func (s *Service) currentSMTPConfig() ForwardConfig {
	cfg := s.cfg
	if s.remoteCfg == nil {
		return cfg
	}
	cfg.SMTPDialTimeout = s.remoteCfg.GetDurationSeconds("forward.smtp_dial_timeout", cfg.SMTPDialTimeout)
	cfg.TLSInsecureSkip = s.remoteCfg.GetBool("forward.tls_insecure_skip", cfg.TLSInsecureSkip)
	cfg.TLSMinVersion = s.remoteCfg.GetInt("forward.tls_min_version", cfg.TLSMinVersion)
	return cfg
}

// ActiveJobs returns the number of currently processing files.
func (s *Service) ActiveJobs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeJobs
}

// Start 启动后台扫描循环（阻塞，应放在 goroutine 中调用）
func (s *Service) Start(ctx context.Context) {
	timer := time.NewTimer(s.currentScanInterval())
	defer timer.Stop()

	log.Printf("[forward] service started (scan_interval=%ds, max_size=%dMB, target=%s)",
		int(s.currentScanInterval()/time.Second), s.currentMaxEmailSize()/(1024*1024), s.currentTarget())

	// Immediate first scan
	s.scanAndLog()

	for {
		select {
		case <-ctx.Done():
			log.Println("[forward] service stopped")
			return
		case <-timer.C:
			s.scanAndLog()
			timer.Reset(s.currentScanInterval())
		case <-s.scanReset:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(s.currentScanInterval())
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
				if fEnt.IsDir() || !shouldProcessMailFile(fEnt.Name()) {
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
	headers, bodyPreview, err := readForFiltering(filePath, s.currentMaxEmailSize(), s.currentBodyPreviewSize())
	if err != nil {
		// Oversized or unparseable → move to cur/ to avoid re-scan
		if moveErr := moveToCur(filePath); moveErr != nil {
			return fmt.Errorf("parse: %v; move to cur: %w", err, moveErr)
		}
		return fmt.Errorf("parse: %w", err)
	}

	// 2. Anti-loop: detect X-Forwarded-By
	if strings.Contains(headers["x-forwarded-by"], "mail-node") {
		// Already forwarded by us (shouldn't hit for new/, but safe)
		if err := moveToCur(filePath); err != nil {
			return fmt.Errorf("loop guard move to cur: %w", err)
		}
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
	attemptedAction := legacyContractAction(result.Action)
	decisionKey := ""
	quarantineDecision := false
	mode := s.currentEngineMode()
	if mode != filterdecision.EngineModeLegacy && s.decision != nil && s.outbox != nil {
		parsed, parseErr := mailparse.ParseFile(filePath, mailparse.Options{
			Mailbox: sourceAddr, MaildirBase: s.mgr.MaildirBase(), MaildirUniqueName: filepath.Base(filePath),
			ServerID: s.remoteCfg.NodeID(),
		})
		if parseErr != nil {
			log.Printf("[filter] normalize failed, retaining legacy action: mailbox=%s error=%v", sourceAddr, parseErr)
		} else {
			stat, statErr := os.Stat(filePath)
			evaluatedAt := time.Now().UTC()
			if statErr == nil {
				evaluatedAt = stat.ModTime().UTC()
			}
			decision, decisionErr := s.decision.Evaluate(parsed.Features, filterdecision.Options{
				AutoQuarantineEnabled: s.remoteCfg.GetBool(filterdecision.AutoQuarantineConfigKey, false), EvaluatedAt: evaluatedAt,
			})
			if decisionErr != nil {
				log.Printf("[filter] decision failed, retaining legacy action: mailbox=%s error=%v", sourceAddr, decisionErr)
			} else {
				event := filtercontract.OutboxEvent{
					SchemaVersion: filtercontract.SchemaVersionV1, Phase: "staged", NodeID: parsed.Features.ServerID,
					Mailbox: sourceAddr, MessageID: parsed.Features.MessageID, Decision: decision,
				}
				if stageErr := s.outbox.Stage(event); stageErr != nil {
					log.Printf("[filter] HIGH: outbox stage failed, retaining legacy action: message_key=%s error=%v", parsed.Features.MessageKey, stageErr)
				} else {
					decisionKey = decision.DecisionKey
					if mode == filterdecision.EngineModeDualFilter {
						attemptedAction = decision.FinalAction
						result.Action = contractLegacyAction(decision.FinalAction)
						quarantineDecision = decision.FinalAction == filtercontract.ActionQuarantine
						result.Reason = "versioned filter decision " + decision.DecisionKey
						result.RuleID = 0
					}
				}
			}
		}
	}

	// 4. Block → keep original for LLM API, move to cur/ so we don't re-scan
	if result.Action == filter.ActionBlock {
		if quarantineDecision && s.quarantine != nil {
			metadata, quarantineErr := s.quarantine.Quarantine(filePath, sourceAddr, headers["message-id"], decisionKey, time.Time{})
			if quarantineErr != nil {
				s.completeDecisionWithQuarantine(decisionKey, attemptedAction, filtercontract.ActionQuarantine, "", "", quarantineErr)
				return fmt.Errorf("quarantine message: %w", quarantineErr)
			}
			s.completeDecisionWithQuarantine(decisionKey, attemptedAction, filtercontract.ActionQuarantine, metadata.QuarantineKey, metadata.OriginalMaildirKey, nil)
			log.Printf("[forward] quarantined: from=%s to=%s key=%s", msg.From, msg.To, metadata.QuarantineKey)
			return nil
		}
		if err := moveToCur(filePath); err != nil {
			s.completeDecision(decisionKey, attemptedAction, filtercontract.ActionQuarantine, err)
			return fmt.Errorf("blocked message move to cur: %w", err)
		}
		s.completeDecision(decisionKey, attemptedAction, filtercontract.ActionQuarantine, nil)
		log.Printf("[forward] blocked: from=%s to=%s rule=%d reason=%s",
			msg.From, msg.To, result.RuleID, result.Reason)
		return nil
	}

	// 5. Forward (pass or flag) via SMTP. The active integrated mailbox is
	// also scanned, so sending it to itself would create one duplicate.
	target := s.currentTarget()
	if sameMailboxAddress(sourceAddr, target) {
		if err := moveToCur(filePath); err != nil {
			s.completeDecision(decisionKey, attemptedAction, legacyContractAction(result.Action), err)
			return fmt.Errorf("self-target message move to cur: %w", err)
		}
		s.completeDecision(decisionKey, attemptedAction, legacyContractAction(result.Action), nil)
		log.Printf("[forward] skipped (self target): %s -> %s", sourceAddr, target)
		return nil
	}

	newSubject := buildSubject(s.cfg.SubjectPrefix, sourceAddr, result.Action, headers["subject"])
	smtpUser, smtpPass := s.currentSMTPAuth()
	if err := streamToSMTP(s.currentSMTPConfig(), filePath, newSubject, sourceAddr, target, smtpUser, smtpPass); err != nil {
		// SMTP failed → leave in new/ for next-scan retry (natural backoff)
		s.completeDecision(decisionKey, attemptedAction, legacyContractAction(result.Action), err)
		return fmt.Errorf("smtp: %w", err)
	}

	// 6. Forward success → move to cur/ (Maildir Seen semantics)
	if err := commitDeliveredFile(filePath); err != nil {
		s.completeDecision(decisionKey, attemptedAction, legacyContractAction(result.Action), err)
		return err
	}
	s.completeDecision(decisionKey, attemptedAction, legacyContractAction(result.Action), nil)

	elapsed := time.Since(start).Milliseconds()
	log.Printf("[forward] forwarded: %s → %s (action=%s, rule=%d, latency=%dms)",
		sourceAddr, target, result.Action, result.RuleID, elapsed)

	return nil
}

func sameMailboxAddress(source, target string) bool {
	source = strings.TrimSpace(source)
	target = strings.TrimSpace(target)
	return source != "" && target != "" && strings.EqualFold(source, target)
}

func (s *Service) currentEngineMode() string {
	if s.remoteCfg == nil {
		return filterdecision.EngineModeLegacy
	}
	mode := s.remoteCfg.GetString(filterdecision.EngineModeConfigKey, filterdecision.EngineModeLegacy)
	if !filterdecision.ValidMode(mode) {
		return filterdecision.EngineModeLegacy
	}
	return mode
}

func (s *Service) completeDecision(decisionKey, attemptedAction, actualAction string, processingErr error) {
	s.completeDecisionWithQuarantine(decisionKey, attemptedAction, actualAction, "", "", processingErr)
}

func (s *Service) completeDecisionWithQuarantine(decisionKey, attemptedAction, actualAction, quarantineKey, originalMaildirKey string, processingErr error) {
	if decisionKey == "" || s.outbox == nil {
		return
	}
	result := filtercontract.ProcessingResult{
		Status: "succeeded", AttemptedAction: attemptedAction, ActualAction: actualAction,
		QuarantineKey: quarantineKey, OriginalMaildirKey: originalMaildirKey,
	}
	if processingErr != nil {
		result.Status = "failed"
		result.ErrorCode = "processing_failed"
		result.ErrorSummary = processingErr.Error()
	}
	if err := s.outbox.Ready(decisionKey, result); err != nil {
		log.Printf("[filter] HIGH: outbox ready failed: decision_key=%s error=%v", decisionKey, err)
	}
}

// ForwardQuarantined delivers a quarantined original through the same SMTP
// path as normal forwarding. Release idempotency is owned by filterquarantine.
func (s *Service) ForwardQuarantined(filePath, sourceAddr string) (string, error) {
	headers, _, err := readForFiltering(filePath, s.currentMaxEmailSize(), s.currentBodyPreviewSize())
	if err != nil {
		return "", err
	}
	target := s.currentTarget()
	user, pass := s.currentSMTPAuth()
	subject := buildSubject(s.cfg.SubjectPrefix, sourceAddr, filter.ActionPass, headers["subject"])
	if err := streamToSMTP(s.currentSMTPConfig(), filePath, subject, sourceAddr, target, user, pass); err != nil {
		return target, err
	}
	return target, nil
}

func legacyContractAction(action filter.Action) string {
	switch action {
	case filter.ActionFlag:
		return filtercontract.ActionTag
	case filter.ActionBlock:
		return filtercontract.ActionQuarantine
	default:
		return filtercontract.ActionAllow
	}
}

func contractLegacyAction(action string) filter.Action {
	switch action {
	case filtercontract.ActionTag:
		return filter.ActionFlag
	case filtercontract.ActionQuarantine:
		return filter.ActionBlock
	default:
		return filter.ActionPass
	}
}

func commitDeliveredFile(filePath string) error {
	if err := moveToCur(filePath); err != nil {
		quarantined, quarantineErr := quarantineDeliveredFile(filePath)
		if quarantineErr != nil {
			return fmt.Errorf("delivered but commit failed: %v; quarantine failed: %w", err, quarantineErr)
		}
		return fmt.Errorf("delivered but commit failed: %v; quarantined at %s", err, quarantined)
	}
	return nil
}

// moveToCur moves a file from new/ to the sibling cur/ directory.
// On failure, the file stays in new/ and gets retried next scan.
func moveToCur(filePath string) error {
	dir := filepath.Dir(filePath)   // .../new
	base := filepath.Base(filePath) // <timestamp>.<pid>.<host>
	curDir := filepath.Join(filepath.Dir(dir), "cur")

	// Ensure cur/ exists
	if err := os.MkdirAll(curDir, 0700); err != nil {
		return fmt.Errorf("mkdir cur %s: %w", curDir, err)
	}

	// Append Maildir info suffix: ":2,S" = Seen flag
	dest := filepath.Join(curDir, base+":2,S")

	if err := os.Rename(filePath, dest); err != nil {
		// If Rename fails (cross-device), try copy + remove
		if err := copyAndRemove(filePath, dest); err != nil {
			return err
		}
	}
	return nil
}

func shouldProcessMailFile(name string) bool {
	return !strings.HasSuffix(name, ".forwarded-error")
}

func quarantineDeliveredFile(filePath string) (string, error) {
	destination := filePath + ".forwarded-error"
	if err := os.Rename(filePath, destination); err != nil {
		return "", err
	}
	log.Printf("[forward] delivered message quarantined after commit failure: %s", destination)
	return destination, nil
}

// copyAndRemove is a fallback for os.Rename across filesystem boundaries.
func copyAndRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		_ = in.Close()
		return err
	}

	if _, err := in.WriteTo(out); err != nil {
		_ = out.Close()
		_ = in.Close()
		_ = os.Remove(dst) // clean up partial
		return err
	}
	if err := out.Close(); err != nil {
		_ = in.Close()
		return err
	}

	// Owner/perms best-effort
	if fi, err := in.Stat(); err == nil {
		_ = os.Chmod(dst, fi.Mode())
	}
	if err := in.Close(); err != nil {
		return err
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

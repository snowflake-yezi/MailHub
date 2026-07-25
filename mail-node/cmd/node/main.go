package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mail-node/internal/agent"
	nodecommand "github.com/ticket/email-mail-node/internal/command"
	"github.com/ticket/email-mail-node/internal/config"
	"github.com/ticket/email-mail-node/internal/domain"
	"github.com/ticket/email-mail-node/internal/filter"
	"github.com/ticket/email-mail-node/internal/filterdecision"
	"github.com/ticket/email-mail-node/internal/filteroutbox"
	"github.com/ticket/email-mail-node/internal/filterpolicy"
	"github.com/ticket/email-mail-node/internal/filterquarantine"
	"github.com/ticket/email-mail-node/internal/forward"
	"github.com/ticket/email-mail-node/internal/handler"
	"github.com/ticket/email-mail-node/internal/identity"
	"github.com/ticket/email-mail-node/internal/mailbox"
	"github.com/ticket/email-mail-node/internal/middleware"
	"github.com/ticket/email-mail-node/internal/nodedata"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
)

const (
	maxNodeRequestBodyBytes int64 = 16 << 20
	nodeReadHeaderTimeout         = 5 * time.Second
	nodeReadTimeout               = 30 * time.Second
	nodeWriteTimeout              = 2 * time.Minute
	nodeIdleTimeout               = 2 * time.Minute
)

func main() {
	if handled, err := runNodeCommand(os.Args[1:], os.Stdout); handled {
		if err != nil {
			log.Fatalf("Node command failed: %v", err)
		}
		return
	}
	// 加载配置
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 自动发现/注册 server_id：若未配置 node.id，向 mgmt 查询或自动注册。
	if cfg.Node.ID == 0 && cfg.Management.TransportMode != "control_stream" {
		discoveredID, err := discoverServerID(cfg)
		if err != nil {
			log.Printf("[discovery] WARNING: failed to discover server_id: %v — heartbeats & PullDeletingTasks will be skipped", err)
		} else {
			cfg.Node.ID = discoveredID
			log.Printf("[discovery] server_id discovered: %d", discoveredID)
		}
	}

	// 从 mgmt-system 拉取动态配置（覆盖 YAML 中的硬编码默认值）
	remoteCfg := config.NewRemoteConfig(cfg.Management.APIURL, cfg.SharedSecret, cfg.Node.ID)
	bootID, startedAt := newBootIdentity()
	remoteCfg.SetBootIdentity(bootID, startedAt)
	controlEnabled := cfg.Management.TransportMode == "dual" || cfg.Management.TransportMode == "control_stream"
	var controlIdentity identity.Record
	var controlCredential string
	if controlEnabled {
		identityStore := identity.New(cfg.Identity.Directory)
		controlIdentity, err = identityStore.Load()
		if err != nil {
			log.Fatalf("Failed to load enrolled node identity for control channel: %v", err)
		}
		controlCredential, err = identityStore.LoadCredentialFile(cfg.Management.CredentialFile)
		if err != nil {
			log.Fatalf("Failed to load node credential for control channel: %v", err)
		}
		remoteCfg.ConfigureNodeCredential(controlIdentity.NodeUUID, controlCredential)
	}
	remoteCfg.RegisterApplyHook(forward.ValidateForwardConfig)
	remoteCfg.RegisterApplyHook(forward.ValidateLifecycleConfig)
	remoteCfg.RegisterApplyHook(filter.ValidateConfig)
	remoteCfg.RegisterApplyHook(filterdecision.ValidateConfig)
	if err := remoteCfg.PullAll(); err != nil {
		log.Printf("[config] WARNING: failed to pull remote config from mgmt: %v — using YAML/local defaults", err)
	} else {
		log.Printf("[config] remote config pulled: %d keys loaded", len(remoteCfg.Configs()))
	}

	// 初始化过滤引擎（default action 优先用远程配置，fallback YAML）
	engine := newFilterEngine(remoteCfg, cfg.Filter.DefaultAction, cfg.Filter.FlagSubjectPrefix)
	if controlEnabled {
		engine.ConfigureAuthorizer(remoteCfg.Authorize)
	}
	registerFilterConfigAfterApply(remoteCfg, engine)

	// 启动定时同步规则
	engine.StartAutoSync(
		cfg.Management.APIURL,
		configuredFilterSyncInterval(remoteCfg, cfg.Management.FilterSyncInterval),
		func() string {
			if controlEnabled {
				return ""
			}
			return cfg.SharedSecret
		}(),
	)
	decisionEngine := filterdecision.New()
	outbox, err := filteroutbox.New(cfg.Filter.OutboxPath, filteroutbox.DefaultMaxEvents, filteroutbox.DefaultMaxBytes)
	if err != nil {
		log.Fatalf("Failed to initialize filter outbox: %v", err)
	}
	policyClient := filterpolicy.NewClient(cfg.Management.APIURL, cfg.SharedSecret, decisionEngine, func() (uint64, string) {
		boot, _ := remoteCfg.BootIdentity()
		return remoteCfg.NodeID(), boot
	})
	if controlEnabled {
		policyClient.ConfigureNodeCredential(controlIdentity.NodeUUID, controlCredential)
	}
	if err := policyClient.SyncOnce(context.Background()); err != nil {
		log.Printf("[filter] WARNING: initial policy sync failed: %v", err)
	}

	// 初始化邮箱管理器（Maildir 属主 UID/GID 可配置，适配宝塔共存机或独立虚拟用户）
	mailboxMgr := mailbox.NewManager(cfg.Maildir.BasePath, cfg.Maildir.VmailUID, cfg.Maildir.VmailGID)
	domainMgr := domain.NewManager(domain.Config{
		PublicHost:          cfg.PublicHost,
		Selector:            cfg.DKIM.Selector,
		VirtualDomainsFile:  cfg.Postfix.VirtualDomainsFile,
		VmailboxFile:        cfg.Postfix.VmailboxFile,
		DKIMKeyDir:          cfg.DKIM.KeyDir,
		DKIMSigningTable:    cfg.DKIM.SigningTable,
		DKIMKeyTable:        cfg.DKIM.KeyTable,
		EnableDKIMProvision: cfg.DKIM.KeyDir != "" && cfg.DKIM.SigningTable != "" && cfg.DKIM.KeyTable != "",
	})

	// 初始化转发服务（ScanInterval / MaxEmailSize / SMTP 参数优先用远程配置）
	forwardCfg := forward.ForwardConfig{
		SMTPHost:        cfg.Forward.SMTPHost,
		SMTPUser:        cfg.Forward.SMTPUser,
		SMTPPass:        cfg.Forward.SMTPPass,
		TargetAddress:   remoteCfg.GetString("forward.target_address", cfg.Forward.TargetAddress),
		SubjectPrefix:   cfg.Forward.SubjectPrefix,
		ScanInterval:    remoteCfg.GetInt("forward.scan_interval", cfg.Forward.ScanInterval),
		MaxEmailSize:    remoteCfg.GetInt64("forward.max_email_size", cfg.Forward.MaxEmailSize),
		BodyPreviewSize: int64(remoteCfg.GetInt("forward.body_preview_size", 65536)),
		SMTPDialTimeout: remoteCfg.GetDurationSeconds("forward.smtp_dial_timeout", 15*time.Second),
		TLSInsecureSkip: tlsInsecureSkip(remoteCfg),
		TLSMinVersion:   remoteCfg.GetInt("forward.tls_min_version", 12),
	}
	fwdSvc := forward.New(forwardCfg, engine, mailboxMgr, remoteCfg)
	fwdSvc.ConfigurePolicyRuntime(decisionEngine, outbox)
	quarantineBase := remoteCfg.GetString("filter.quarantine_base", cfg.Filter.QuarantineBase)
	quarantineStore, err := filterquarantine.New(quarantineBase, cfg.Maildir.BasePath)
	if err != nil {
		log.Fatalf("Failed to initialize filter quarantine: %v", err)
	}
	fwdSvc.ConfigureQuarantine(quarantineStore)
	if recovered, recoverErr := outbox.RecoverStagedWithResolver(quarantineStore.RecoveryResult); recoverErr != nil {
		log.Printf("[filter] WARNING: outbox recovery incomplete: recovered=%d error=%v", recovered, recoverErr)
	} else if recovered > 0 {
		log.Printf("[filter] recovered %d staged decisions", recovered)
	}

	// 初始化生命周期管理器（安全软删除 + 垃圾回收 + 重启对账）
	// 超时/间隔参数优先用远程配置，0 值触发默认值回退
	trashRetention := remoteCfg.GetDurationHours("lifecycle.trash_retention_hours", 24*time.Hour)
	lifecycle := forward.NewLifecycle(mailboxMgr, fwdSvc,
		trashRetention,
		remoteCfg.GetDurationMinutes("lifecycle.drain_timeout_minutes", 5*time.Minute),
		time.Duration(remoteCfg.GetInt("lifecycle.drain_poll_interval_ms", 500))*time.Millisecond,
		remoteCfg.GetDurationMinutes("lifecycle.gc_interval_minutes", 60*time.Minute),
		remoteCfg,
	)
	remoteCfg.RegisterAfterApplyHook(fwdSvc.AfterApplyConfig)
	remoteCfg.RegisterAfterApplyHook(lifecycle.AfterApplyConfig)

	// Both legacy HTTP and ControlStream commands reuse this application-level
	// handler so Postfix, Dovecot, Maildir, DKIM, and quarantine behavior remain
	// owned by the existing managers.
	nodeH := handler.NewNodeHandler(
		mailboxMgr,
		domainMgr,
		engine,
		lifecycle,
		cfg.Node.ID,
		cfg.Node.Name,
		cfg.Management.APIURL,
		cfg.SharedSecret,
		remoteCfg,
	)
	nodeH.ConfigureQuarantine(quarantineStore, fwdSvc.ForwardQuarantined)

	// 启动后台转发扫描
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remoteCfg.RegisterAfterApplyHook(func(_, _ uint64) {
		if err := reportRuntimeConfigSnapshot(remoteCfg, engine, forwardCfg, trashRetention); err != nil {
			log.Printf("[config] post-apply snapshot failed: %v", err)
		}
	})
	if cfg.Node.ID == 0 && cfg.Management.TransportMode != "control_stream" {
		go startDiscoveryRetry(ctx, 5*time.Second, func() (uint64, error) {
			return discoverServerID(cfg)
		}, func(nodeID uint64) {
			remoteCfg.SetNodeID(nodeID)
			log.Printf("[discovery] server_id recovered: %d", nodeID)
			if err := remoteCfg.Reload(); err != nil {
				log.Printf("[discovery] node config reload after recovery failed: %v", err)
			} else if err := reportRuntimeConfigSnapshot(remoteCfg, engine, forwardCfg, trashRetention); err != nil {
				log.Printf("[discovery] config snapshot after recovery failed: %v", err)
			}
			go lifecycle.PullDeletingTasks(cfg.Management.APIURL, nodeID, cfg.SharedSecret)
		})
	}
	go remoteCfg.StartPolling(ctx, time.Minute, func(err error) {
		log.Printf("[config] periodic pull failed: %v", err)
	})
	go policyClient.Start(ctx, func() time.Duration {
		return time.Duration(remoteCfg.GetInt(filter.SyncIntervalConfigKey, cfg.Management.FilterSyncInterval)) * time.Second
	}, func(err error) {
		log.Printf("[filter] periodic policy sync failed: %v", err)
	})
	uploader := filteroutbox.NewUploader(outbox, cfg.Management.APIURL, func() string {
		if controlEnabled {
			return ""
		}
		return cfg.SharedSecret
	}())
	if controlEnabled {
		uploader.ConfigureAuthorizer(remoteCfg.Authorize)
	}
	go uploader.Start(ctx)
	go fwdSvc.Start(ctx)
	if controlEnabled {
		commandJournal, journalErr := nodecommand.OpenJournal(cfg.Identity.Directory, nodecommand.JournalConfig{})
		if journalErr != nil {
			log.Fatalf("Failed to initialize durable command journal: %v", journalErr)
		}
		commandDispatcher, dispatcherErr := nodecommand.NewDispatcher(commandJournal, nodeH.ExecuteControlCommand, 64)
		if dispatcherErr != nil {
			log.Fatalf("Failed to initialize command dispatcher: %v", dispatcherErr)
		}
		go commandDispatcher.Run(ctx)
		dataDispatcher, dataDispatcherErr := nodedata.NewDispatcher(nodeH.OpenControlData)
		if dataDispatcherErr != nil {
			log.Fatalf("Failed to initialize data stream dispatcher: %v", dataDispatcherErr)
		}
		controlAgent, agentErr := agent.New(agent.Config{
			Address: cfg.Management.ControlURL, CAFile: cfg.Management.CAFile,
			NodeUUID: controlIdentity.NodeUUID, Credential: controlCredential,
			BootID: bootID, StartedAt: startedAt, AgentVersion: nodeAgentVersion(),
			SupportedProtocolVersions: []uint32{1},
			Capabilities:              []string{"heartbeat.v1", "config.revision.v1", "filter.revision.v1", "command.v1", "data.v1"},
			Revisions:                 remoteCfg.Revisions,
			Snapshot: func() agent.HealthSnapshot {
				return controlHealthSnapshot(cfg, mailboxMgr, remoteCfg)
			},
			OnConfigRevision: func(_ context.Context, _ uint64) (uint64, error) {
				reloadErr := remoteCfg.Reload()
				_, appliedRevision := remoteCfg.Revisions()
				return appliedRevision, reloadErr
			},
			OnFilterRevision: func(reloadContext context.Context, _ uint64) error {
				legacyErr := engine.SyncFromManager(cfg.Management.APIURL, "")
				policyErr := policyClient.SyncOnce(reloadContext)
				return errors.Join(legacyErr, policyErr)
			},
			Commands: commandDispatcher,
			Data:     dataDispatcher,
			Logf:     log.Printf,
		})
		if agentErr != nil {
			log.Fatalf("Failed to initialize node control agent: %v", agentErr)
		}
		go controlAgent.Run(ctx)
	}

	// 启动 .trash/ 垃圾回收（24h 后物理清除）
	lifecycle.StartGC(ctx)
	if err := reportRuntimeConfigSnapshot(remoteCfg, engine, forwardCfg, trashRetention); err != nil {
		log.Printf("[config] WARNING: failed to report config snapshot: %v", err)
	}
	go startPeriodicSnapshot(ctx, 5*time.Minute, func() error {
		return reportRuntimeConfigSnapshot(remoteCfg, engine, forwardCfg, trashRetention)
	})

	// 重启自愈：向 mgmt 拉取属于本节点的 DELETING 状态任务并恢复执行
	if cfg.Node.ID != 0 {
		go lifecycle.PullDeletingTasks(cfg.Management.APIURL, cfg.Node.ID, cfg.SharedSecret)
	}

	// 设置 Gin
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()
	r.Use(requestBodyLimit(maxNodeRequestBodyBytes))
	if cfg.Management.TransportMode != "control_stream" {
		registerNodeRoutes(r, nodeH, cfg.SharedSecret, func() (string, string) { return remoteCfg.NodeCredential() })
	}

	// 启动心跳上报（被动心跳：刷新 mgmt last_heartbeat + current_load；status 由 mgmt 主动探测决定）
	if cfg.Management.TransportMode != "control_stream" {
		go startHeartbeat(cfg, mailboxMgr, remoteCfg)
	}

	// 优雅退出
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("Shutting down mail node...")
		os.Exit(0)
	}()

	if cfg.Management.TransportMode == "control_stream" {
		log.Printf("Mail node '%s' running in control_stream mode; local HTTP listener disabled", cfg.Node.Name)
		select {}
	} else {
		addr := fmt.Sprintf(":%d", cfg.Server.Port)
		log.Printf("Starting mail node '%s' on %s", cfg.Node.Name, addr)
		server := newNodeHTTPServer(addr, r)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start: %v", err)
		}
	}
}

func newNodeHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: nodeReadHeaderTimeout,
		ReadTimeout:       nodeReadTimeout,
		WriteTimeout:      nodeWriteTimeout,
		IdleTimeout:       nodeIdleTimeout,
	}
}

func tlsInsecureSkip(remoteCfg *config.RemoteConfig) bool {
	return remoteCfg.GetBool("forward.tls_insecure_skip", false)
}

func newFilterEngine(remoteCfg *config.RemoteConfig, defaultAction, flagPrefix string) *filter.Engine {
	engine := filter.New(filter.Action(defaultAction), flagPrefix)
	engine.UpdateConfig(remoteCfg.Configs())
	return engine
}

func registerFilterConfigAfterApply(remoteCfg *config.RemoteConfig, engine *filter.Engine) {
	remoteCfg.RegisterAfterApplyHook(func(_, _ uint64) {
		engine.UpdateConfig(remoteCfg.Configs())
	})
}

func configuredFilterSyncInterval(remoteCfg *config.RemoteConfig, localFallback int) int {
	return remoteCfg.GetInt(filter.SyncIntervalConfigKey, localFallback)
}

func requestBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

func registerNodeRoutes(r *gin.Engine, nodeH *handler.NodeHandler, sharedSecret string, credentialProviders ...func() (string, string)) {
	internalGroup := r.Group("/internal")
	var credentialProvider func() (string, string)
	if len(credentialProviders) > 0 {
		credentialProvider = credentialProviders[0]
	}
	internalGroup.Use(middleware.NodeAuthRequired(sharedSecret, credentialProvider))
	nodeH.RegisterInternalRoutes(internalGroup)
}

func reportRuntimeConfigSnapshot(remoteCfg *config.RemoteConfig, engine *filter.Engine, forwardCfg forward.ForwardConfig, trashRetention time.Duration) error {
	return remoteCfg.ReportSnapshots(runtimeConfigSnapshotValues(remoteCfg, engine, forwardCfg, trashRetention), time.Now())
}

func runtimeConfigSnapshotValues(remoteCfg *config.RemoteConfig, engine *filter.Engine, forwardCfg forward.ForwardConfig, trashRetention time.Duration) map[string]string {
	return map[string]string{
		filter.SyncIntervalConfigKey:       strconv.Itoa(engine.SyncIntervalSeconds()),
		filterdecision.EngineModeConfigKey: remoteCfg.GetString(filterdecision.EngineModeConfigKey, filterdecision.EngineModeLegacy),
		filterdecision.AutoQuarantineConfigKey: strconv.FormatBool(
			remoteCfg.GetBool(filterdecision.AutoQuarantineConfigKey, false),
		),
		"filter.quarantine_base":           remoteCfg.GetString("filter.quarantine_base", "/var/mail/mailhub-quarantine"),
		"forward.scan_interval":            strconv.Itoa(remoteCfg.GetInt("forward.scan_interval", forwardCfg.ScanInterval)),
		"forward.max_email_size":           strconv.FormatInt(remoteCfg.GetInt64("forward.max_email_size", forwardCfg.MaxEmailSize), 10),
		"forward.body_preview_size":        strconv.FormatInt(remoteCfg.GetInt64("forward.body_preview_size", forwardCfg.BodyPreviewSize), 10),
		"forward.target_address":           remoteCfg.GetString("forward.target_address", forwardCfg.TargetAddress),
		"forward.smtp_dial_timeout":        strconv.Itoa(int(remoteCfg.GetDurationSeconds("forward.smtp_dial_timeout", forwardCfg.SMTPDialTimeout) / time.Second)),
		"forward.tls_insecure_skip":        strconv.FormatBool(remoteCfg.GetBool("forward.tls_insecure_skip", forwardCfg.TLSInsecureSkip)),
		"forward.tls_min_version":          strconv.Itoa(remoteCfg.GetInt("forward.tls_min_version", forwardCfg.TLSMinVersion)),
		"lifecycle.trash_retention_hours":  strconv.Itoa(int(remoteCfg.GetDurationHours("lifecycle.trash_retention_hours", trashRetention) / time.Hour)),
		"lifecycle.gc_interval_minutes":    strconv.Itoa(int(remoteCfg.GetDurationMinutes("lifecycle.gc_interval_minutes", 60*time.Minute) / time.Minute)),
		"lifecycle.drain_timeout_minutes":  strconv.Itoa(int(remoteCfg.GetDurationMinutes("lifecycle.drain_timeout_minutes", 5*time.Minute) / time.Minute)),
		"lifecycle.drain_poll_interval_ms": strconv.Itoa(remoteCfg.GetInt("lifecycle.drain_poll_interval_ms", 500)),
	}
}

func newBootIdentity() (string, time.Time) {
	startedAt := time.Now().UTC()
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("%d-%d", startedAt.UnixNano(), os.Getpid()), startedAt
	}
	return hex.EncodeToString(value), startedAt
}

func startPeriodicSnapshot(ctx context.Context, interval time.Duration, report func() error) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := report(); err != nil {
				log.Printf("[config] periodic snapshot failed: %v", err)
			}
		}
	}
}

// discoverServerID 向 mgmt 查询或自动注册本节点的 server_id。
// 使用 server.advertise_host 作为唯一标识。
func discoverServerID(cfg *config.Config) (uint64, error) {
	advertiseHost := cfg.Server.AdvertiseHost
	if advertiseHost == "" {
		hostname, _ := os.Hostname()
		if hostname == "" {
			return 0, fmt.Errorf("server.advertise_host not configured and hostname detection failed")
		}
		advertiseHost = fmt.Sprintf("%s:%d", hostname, cfg.Server.Port)
		log.Printf("[discovery] advertise_host not set, derived from hostname: %s", advertiseHost)
	}

	mgmtURL := strings.TrimRight(cfg.Management.APIURL, "/") + "/api/v1/internal/servers/discover"
	body, _ := json.Marshal(map[string]string{
		"api_host":  advertiseHost,
		"node_name": cfg.Node.Name,
	})

	req, err := http.NewRequest(http.MethodPost, mgmtURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", cfg.SharedSecret)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("POST mgmt discover: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("mgmt returned %d: %s", resp.StatusCode, string(data))
	}

	var result struct {
		Code int `json:"code"`
		Data struct {
			ServerID uint64 `json:"server_id"`
			Created  bool   `json:"created"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("parse response: %w", err)
	}
	if result.Code != 0 {
		return 0, fmt.Errorf("mgmt error code %d", result.Code)
	}

	if result.Data.Created {
		log.Printf("[discovery] auto-registered as new server: %s (id=%d)", advertiseHost, result.Data.ServerID)
	}
	return result.Data.ServerID, nil
}

func startDiscoveryRetry(ctx context.Context, interval time.Duration, discover func() (uint64, error), onDiscovered func(uint64)) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	backoff := interval
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		nodeID, err := discover()
		if err != nil {
			log.Printf("[discovery] retry failed: %v", err)
			backoff *= 2
			if backoff > time.Minute {
				backoff = time.Minute
			}
			timer.Reset(backoff)
			continue
		}
		if nodeID == 0 {
			log.Printf("[discovery] retry returned empty server_id")
			timer.Reset(backoff)
			continue
		}
		onDiscovered(nodeID)
		return
	}
}

func controlHealthSnapshot(cfg *config.Config, mailboxMgr *mailbox.Manager, remoteCfg *config.RemoteConfig) agent.HealthSnapshot {
	now := time.Now().UTC()
	readiness := nodev1.ReadinessState_READINESS_STATE_READY
	components := []agent.ComponentHealth{
		inspectControlComponent("maildir", now, mailboxMgr.MaildirBase()),
		inspectControlComponent("postfix", now, cfg.Postfix.VirtualDomainsFile, cfg.Postfix.VmailboxFile),
		inspectControlComponent("dovecot", now, "/etc/dovecot/users.conf"),
		inspectControlComponent("opendkim", now, cfg.DKIM.KeyDir, cfg.DKIM.SigningTable, cfg.DKIM.KeyTable),
	}
	lastApplyError := remoteCfg.LastApplyError()
	configState, configDetail := nodev1.ReadinessState_READINESS_STATE_READY, "applied"
	if lastApplyError != "" {
		configState, configDetail = nodev1.ReadinessState_READINESS_STATE_DEGRADED, lastApplyError
		readiness = nodev1.ReadinessState_READINESS_STATE_DEGRADED
	}
	components = append(components, agent.ComponentHealth{Component: "config", State: configState, Detail: configDetail, CheckedAt: now})
	for _, component := range components {
		if component.State == nodev1.ReadinessState_READINESS_STATE_FAILED {
			readiness = nodev1.ReadinessState_READINESS_STATE_FAILED
			break
		}
		if component.State == nodev1.ReadinessState_READINESS_STATE_DEGRADED && readiness == nodev1.ReadinessState_READINESS_STATE_READY {
			readiness = nodev1.ReadinessState_READINESS_STATE_DEGRADED
		}
	}
	totalBytes, availableBytes := agent.DiskUsage(mailboxMgr.MaildirBase())
	mailboxCount := uint64(0)
	if active := mailboxMgr.ActiveCount(); active > 0 {
		mailboxCount = uint64(active)
	}
	return agent.HealthSnapshot{
		MailboxCount: mailboxCount, DiskTotalBytes: totalBytes, DiskAvailableBytes: availableBytes,
		Readiness: readiness, Components: components, LastApplyError: lastApplyError,
	}
}

func inspectControlComponent(name string, checkedAt time.Time, paths ...string) agent.ComponentHealth {
	configured := 0
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		configured++
		if _, err := os.Stat(path); err != nil {
			return agent.ComponentHealth{
				Component: name, State: nodev1.ReadinessState_READINESS_STATE_FAILED,
				Detail: err.Error(), CheckedAt: checkedAt,
			}
		}
	}
	if configured == 0 {
		return agent.ComponentHealth{
			Component: name, State: nodev1.ReadinessState_READINESS_STATE_UNKNOWN,
			Detail: "not configured", CheckedAt: checkedAt,
		}
	}
	return agent.ComponentHealth{
		Component: name, State: nodev1.ReadinessState_READINESS_STATE_READY,
		Detail: "available", CheckedAt: checkedAt,
	}
}

// clampHeartbeat 把心跳间隔约束到合法区间；区间外（含 0/负）返回 fallback（SP-7）。
// 区间边界从远程配置读取，默认 [5, 600] 秒。remoteCfg 为 nil 时使用默认值。
func clampHeartbeat(v, fallback int, remoteCfg *config.RemoteConfig) int {
	minInterval, maxInterval := 5, 600
	if remoteCfg != nil {
		minInterval = remoteCfg.GetInt("heartbeat.interval_min", 5)
		maxInterval = remoteCfg.GetInt("heartbeat.interval_max", 600)
	}
	if v < minInterval || v > maxInterval {
		return fallback
	}
	return v
}

// startHeartbeat 定时向管理系统上报心跳（被动心跳）。
//
// 证明 node 进程存活 + node→mgmt 方向可达，刷新 mgmt 的 last_heartbeat 与 current_load。
// 心跳间隔由 mgmt 在响应里下发（SP-6'），本循环每轮据此动态调整；本地 config 仅作
// 冷启动首次与 mgmt 不可达时的兜底。注意：mgmt 的 status 完全由其主动探测决定，
// 本心跳不参与 status 升降，避免与探测结论打架（见 docs/design/t7-healthcheck-design.md §4.1 / §6）。
func startHeartbeat(cfg *config.Config, mailboxMgr *mailbox.Manager, remoteCfg *config.RemoteConfig) {
	fallback := remoteCfg.GetInt("heartbeat.interval_fallback", 60)
	interval := clampHeartbeat(cfg.Management.HeartbeatInterval, fallback, remoteCfg)
	client := &http.Client{Timeout: 10 * time.Second}
	url := strings.TrimRight(cfg.Management.APIURL, "/") + "/api/v1/internal/servers/heartbeat"

	// beat 上报一次心跳，返回 mgmt 下发的期望间隔；失败或非法时返回 0（调用方沿用当前值）。
	beat := func() int {
		nodeID := remoteCfg.NodeID()
		if nodeID == 0 {
			return 0 // discovery failed, skip heartbeat
		}
		load := 0
		if mailboxMgr != nil {
			load = mailboxMgr.ActiveCount()
		}
		bootID, startedAt := remoteCfg.BootIdentity()
		body, _ := json.Marshal(map[string]interface{}{
			"server_id":        nodeID,
			"status":           "alive", // 仅表示本地进程自检 OK；mgmt 不据此覆盖 status
			"load":             load,
			"node_name":        cfg.Node.Name,
			"last_apply_error": remoteCfg.LastApplyError(),
			"applied_revision": func() uint64 {
				_, applied := remoteCfg.Revisions()
				return applied
			}(),
			"boot_id":    bootID,
			"started_at": startedAt,
		})
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			log.Printf("heartbeat: build request failed: %v", err)
			return 0
		}
		req.Header.Set("Content-Type", "application/json")
		remoteCfg.Authorize(req)

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("heartbeat: POST mgmt failed (node=%s): %v", cfg.Node.Name, err)
			return 0
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Printf("heartbeat: mgmt returned %d for node=%s", resp.StatusCode, cfg.Node.Name)
			return 0
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return 0
		}
		var hr struct {
			Code int `json:"code"`
			Data struct {
				HeartbeatInterval int `json:"heartbeat_interval"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &hr); err != nil {
			return 0
		}
		return hr.Data.HeartbeatInterval
	}

	beat() // 启动后立即上报一次，缩短冷启动空白期
	for {
		timer := time.NewTimer(time.Duration(interval) * time.Second)
		<-timer.C
		if got := beat(); got != 0 {
			interval = clampHeartbeat(got, interval, remoteCfg)
		}
	}
}

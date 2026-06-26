package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mail-node/internal/config"
	"github.com/ticket/email-mail-node/internal/domain"
	"github.com/ticket/email-mail-node/internal/filter"
	"github.com/ticket/email-mail-node/internal/forward"
	"github.com/ticket/email-mail-node/internal/handler"
	"github.com/ticket/email-mail-node/internal/mailbox"
	"github.com/ticket/email-mail-node/internal/middleware"
)

func main() {
	// 加载配置
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 初始化过滤引擎
	engine := filter.New(
		filter.Action(cfg.Filter.DefaultAction),
		cfg.Filter.FlagSubjectPrefix,
	)

	// 启动定时同步规则
	engine.StartAutoSync(
		cfg.Management.APIURL,
		cfg.Management.FilterSyncInterval,
		cfg.SharedSecret,
	)

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

	// 初始化转发服务
	forwardCfg := forward.ForwardConfig{
		SMTPHost:      cfg.Forward.SMTPHost,
		SMTPUser:      cfg.Forward.SMTPUser,
		SMTPPass:      cfg.Forward.SMTPPass,
		TargetAddress: cfg.Forward.TargetAddress,
		SubjectPrefix: cfg.Forward.SubjectPrefix,
		ScanInterval:  cfg.Forward.ScanInterval,
		MaxEmailSize:  cfg.Forward.MaxEmailSize,
	}
	fwdSvc := forward.New(forwardCfg, engine, mailboxMgr)

	// 初始化生命周期管理器（安全软删除 + 垃圾回收 + 重启对账）
	lifecycle := forward.NewLifecycle(mailboxMgr, fwdSvc)

	// 启动后台转发扫描
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fwdSvc.Start(ctx)

	// 启动 .trash/ 垃圾回收（24h 后物理清除）
	lifecycle.StartGC(ctx)

	// 重启自愈：向 mgmt 拉取属于本节点的 DELETING 状态任务并恢复执行
	go lifecycle.PullDeletingTasks(cfg.Management.APIURL, cfg.Node.ID)

	// 初始化 handler（注入 lifecycle 以支持安全删除协议）
	nodeH := handler.NewNodeHandler(
		mailboxMgr,
		domainMgr,
		engine,
		lifecycle,
		cfg.Node.ID,
		cfg.Node.Name,
	)

	// 设置 Gin
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()

	// 注册内部路由（Shared-Secret 鉴权）
	internalGroup := r.Group("/internal")
	internalGroup.Use(middleware.InternalAuthRequired(cfg.SharedSecret))
	nodeH.RegisterInternalRoutes(internalGroup)

	// Deprecated: /smtp/filter is 方案 A (Postfix content_filter)。
	// 当前架构已决策方案 B（Maildir 异步扫描 → forward.Service）。
	r.POST("/smtp/filter", nodeH.SMTPFilter)

	// 启动心跳上报（被动心跳：刷新 mgmt last_heartbeat + current_load；status 由 mgmt 主动探测决定）
	go startHeartbeat(cfg, mailboxMgr)

	// 优雅退出
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("Shutting down mail node...")
		os.Exit(0)
	}()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("Starting mail node '%s' on %s", cfg.Node.Name, addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("Failed to start: %v", err)
	}
}

// startHeartbeat 定时向管理系统上报心跳（被动心跳）。
//
// 证明 node 进程存活 + node→mgmt 方向可达，刷新 mgmt 的 last_heartbeat 与 current_load。
// 注意：mgmt 的 status 完全由其主动探测决定，本心跳不参与 status 升降，
// 避免与探测结论打架（见 docs/design/t7-healthcheck-design.md §4.1 / §6）。
func startHeartbeat(cfg *config.Config, mailboxMgr *mailbox.Manager) {
	interval := cfg.Management.HeartbeatInterval
	if interval <= 0 {
		interval = 60
	}
	client := &http.Client{Timeout: 10 * time.Second}
	url := strings.TrimRight(cfg.Management.APIURL, "/") + "/api/v1/internal/servers/heartbeat"

	beat := func() {
		load := 0
		if mailboxMgr != nil {
			load = mailboxMgr.ActiveCount()
		}
		body, _ := json.Marshal(map[string]interface{}{
			"server_id": cfg.Node.ID,
			"status":    "alive", // 仅表示本地进程自检 OK；mgmt 不据此覆盖 status
			"load":      load,
			"node_name": cfg.Node.Name,
		})
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			log.Printf("heartbeat: build request failed: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", cfg.SharedSecret)

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("heartbeat: POST mgmt failed (node=%s): %v", cfg.Node.Name, err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Printf("heartbeat: mgmt returned %d for node=%s", resp.StatusCode, cfg.Node.Name)
		}
	}

	beat() // 启动后立即上报一次，缩短冷启动空白期
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		beat()
	}
}

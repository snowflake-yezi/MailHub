package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
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

	// 启动心跳上报
	go startHeartbeat(cfg, engine)

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

// startHeartbeat 定时向管理系统上报心跳
func startHeartbeat(cfg *config.Config, engine *filter.Engine) {
	ticker := time.NewTicker(time.Duration(cfg.Management.HeartbeatInterval) * time.Second)
	defer ticker.Stop()

	client := &gin.DefaultWriter // 复用 http client 简化
	_ = client

	for range ticker.C {
		// POST /api/v1/internal/servers/heartbeat
		// 简单实现，Phase 1 用 net/http
		// TODO: 完善心跳上报
		log.Printf("heartbeat: node=%s status=ok", cfg.Node.Name)
	}
}

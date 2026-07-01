package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/config"
	"github.com/ticket/email-mgmt-system/internal/handler"
	"github.com/ticket/email-mgmt-system/internal/healthcheck"
	"github.com/ticket/email-mgmt-system/internal/lifecycle"
	"github.com/ticket/email-mgmt-system/internal/middleware"
	"github.com/ticket/email-mgmt-system/internal/service"
	"github.com/ticket/email-mgmt-system/internal/store"
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
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid config: %v", err)
	}

	// 初始化数据库
	db, err := store.New(cfg.Database.DSN, cfg.Server.Mode)
	if err != nil {
		log.Fatalf("Failed to connect database: %v", err)
	}

	// 种子数据
	var tokenSeeds []struct{ Name, Token, Scopes string }
	for _, t := range cfg.Auth.Tokens {
		scopes := ""
		for i, s := range t.Scopes {
			if i > 0 {
				scopes += ","
			}
			scopes += s
		}
		tokenSeeds = append(tokenSeeds, struct{ Name, Token, Scopes string }{t.Name, t.Token, scopes})
	}
	for _, d := range cfg.Domains {
		db.SeedDefaultData(d.Name, cfg.DefaultRetentionDays, tokenSeeds)
	}

	// 初始化服务
	allocator := service.NewAllocator(db, cfg, cfg.Auth.SharedSecret)
	importRealAccounts(db, cfg)
	if err := db.SeedServerDomainsFromAccounts(); err != nil {
		log.Printf("[WARN] seed server_domains failed: %v", err)
	}

	// 初始化 handler
	mailboxH := handler.NewMailboxHandler(db, allocator, cfg.Auth.SharedSecret)
	emailH := handler.NewEmailHandler(db, cfg.Auth.SharedSecret)
	serverH := handler.NewServerHandler(db, cfg.Auth.SharedSecret)
	filterH := handler.NewFilterHandler(db, cfg.Auth.SharedSecret)
	adminH := handler.NewAdminHandler(db)
	healthH := handler.NewHealthHandler(db)

	// Session 管理器
	sessionMgr := middleware.NewSessionManager()
	authH := handler.NewAuthHandler(cfg.Auth.AdminUser, cfg.Auth.AdminPass, sessionMgr)

	// 设置 Gin
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()

	// 加载 HTML 模板 + 静态资源
	r.LoadHTMLGlob("template/admin/*.html")
	r.Static("/static", "template/static")

	// ---- 健康检查（公开，无鉴权，systemd / 监控探活用） ----
	r.GET("/health", healthH.Health)
	r.GET("/health/ready", healthH.Ready)

	// ---- 公开登录/登出（无需鉴权） ----
	authGroup := r.Group("/admin")
	authGroup.GET("/login", authH.LoginPage)
	authGroup.POST("/login", authH.LoginAction)
	authGroup.GET("/logout", authH.LogoutAction)
	authGroup.POST("/logout", authH.LogoutAction)

	// ---- 管理后台页面（Session 鉴权） ----
	adminAuth := middleware.AdminAuthRequired(sessionMgr)
	protectedPages := r.Group("/admin")
	protectedPages.Use(adminAuth)
	adminH.RegisterProtectedRoutes(protectedPages)

	// ---- 管理后台 API（Session 鉴权） ----
	apiAdmin := r.Group("/api/v1/admin")
	apiAdmin.Use(adminAuth)
	serverH.RegisterAdminRoutes(apiAdmin)
	filterH.RegisterAdminRoutes(apiAdmin)
	mailboxH.RegisterAdminRoutes(apiAdmin)
	emailH.RegisterAdminRoutes(apiAdmin)

	// ---- 外部 API v1（Bearer Token 鉴权 + Scope） ----
	api := r.Group("/api/v1")
	api.Use(middleware.AuthRequired(db))

	// 邮箱生成 & 查询（出票中心 / 大模型系统）
	api.POST("/mailboxes", middleware.RequireScope("mailbox:create"), mailboxH.CreateMailbox)
	api.GET("/mailboxes/:order_id", middleware.RequireScope("mailbox:read"), mailboxH.GetMailbox)
	api.POST("/mailboxes/:order_id/disable", middleware.RequireScope("mailbox:create"), mailboxH.DisableMailbox)

	// 邮件查询（大模型系统）
	emailGroup := api.Group("")
	emailGroup.Use(middleware.RequireScope("email:read"))
	emailH.RegisterRoutes(emailGroup)

	// ---- 内部接口（邮箱服务器调用，Shared-Secret 鉴权） ----
	internal := r.Group("/api/v1/internal")
	internal.Use(middleware.InternalAuthRequired(cfg.Auth.SharedSecret))
	internal.POST("/servers/heartbeat", serverH.Heartbeat)
	internal.POST("/servers/discover", serverH.DiscoverServer)
	internal.GET("/filters", filterH.GetActiveRules)
	internal.GET("/sync/deleting", mailboxH.SyncDeleting)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	healthScheduler := healthcheck.NewScheduler(db, cfg.Auth.SharedSecret, 30*time.Second, 5*time.Second)
	go healthScheduler.Start(ctx)

	lifecycleScheduler := lifecycle.NewScheduler(db, cfg.Auth.SharedSecret, 5*time.Minute)
	go lifecycleScheduler.Start(ctx)

	// 优雅退出
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("Shutting down server...")
		cancel()
		// 这里可以加 DB 关闭逻辑
		os.Exit(0)
	}()

	// 启动
	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Server.Port)
	log.Printf("Starting management system on %s (mode: %s)", addr, cfg.Server.Mode)
	if err := r.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func importRealAccounts(db *store.Store, cfg *config.Config) {
	servers, err := db.ListServers()
	if err != nil {
		log.Printf("[WARN] list servers for account import failed: %v", err)
		return
	}

	importer := service.NewAccountImporter(db)
	for _, srv := range servers {
		if srv.Status == "down" {
			continue
		}
		host := serverSSHHost(srv.APIHost)
		if host == "" || host == "127.0.0.1" || host == "localhost" {
			continue
		}

		cmd := exec.Command("ssh",
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=5",
			"root@"+host,
			"cat /etc/dovecot/users.conf",
		)
		out, err := cmd.Output()
		if err != nil {
			log.Printf("[WARN] import real accounts from %s failed: %v", srv.Name, err)
			continue
		}

		result, err := importer.ImportDovecotUsers(srv.ID, string(out), cfg.DefaultRetentionDays)
		if err != nil {
			log.Printf("[WARN] parse real accounts from %s failed: %v", srv.Name, err)
			continue
		}
		log.Printf("Imported real mailbox accounts from %s: imported=%d skipped=%d errors=%d",
			srv.Name, result.Imported, result.Skipped, len(result.Errors))
	}
}

func serverSSHHost(apiHost string) string {
	for i, r := range apiHost {
		if r == ':' {
			return apiHost[:i]
		}
	}
	return apiHost
}

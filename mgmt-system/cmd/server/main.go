package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/apiregistry"
	"github.com/ticket/email-mgmt-system/internal/config"
	"github.com/ticket/email-mgmt-system/internal/handler"
	"github.com/ticket/email-mgmt-system/internal/healthcheck"
	"github.com/ticket/email-mgmt-system/internal/lifecycle"
	"github.com/ticket/email-mgmt-system/internal/middleware"
	"github.com/ticket/email-mgmt-system/internal/nodecommand"
	"github.com/ticket/email-mgmt-system/internal/nodedata"
	"github.com/ticket/email-mgmt-system/internal/nodegateway"
	"github.com/ticket/email-mgmt-system/internal/nodesession"
	"github.com/ticket/email-mgmt-system/internal/nodetransport"
	"github.com/ticket/email-mgmt-system/internal/service"
	"github.com/ticket/email-mgmt-system/internal/store"
	nodecontract "github.com/ticket/email-node-contract"
	"google.golang.org/grpc"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		if err := runAdminCommand(os.Args[2:]); err != nil {
			log.Fatalf("Admin command failed: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		log.Fatalf("Unknown command %q (expected serve or admin)", os.Args[1])
	}
	// Load config
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

	// Init database
	db, err := store.New(cfg.Database.DSN, cfg.Server.Mode)
	if err != nil {
		log.Fatalf("Failed to connect database: %v", err)
	}
	credentials := service.NewAdminCredentialService(db, cfg.Server.Mode)
	bootstrapped, err := credentials.IsBootstrapped()
	if err != nil {
		log.Fatalf("Failed to read admin bootstrap state: %v", err)
	}
	if cfg.Server.Mode == "release" && !bootstrapped {
		log.Fatal("Admin account is not initialized; run 'mgmt-server admin bootstrap' before serve")
	}

	// Seed data
	var tokenSeeds []store.LegacyAPITokenSeed
	for _, t := range cfg.Auth.Tokens {
		scopes := ""
		for i, s := range t.Scopes {
			if i > 0 {
				scopes += ","
			}
			scopes += s
		}
		tokenSeeds = append(tokenSeeds, store.LegacyAPITokenSeed{Name: t.Name, Token: t.Token, Scopes: scopes})
	}
	for _, d := range cfg.Domains {
		if err := db.SeedDefaultData(d.Name); err != nil {
			log.Fatalf("Failed to seed domain %s: %v", d.Name, err)
		}
	}

	// Init services
	nodeEnrollmentService := service.NewNodeEnrollmentService(db)
	sessionRegistry := nodesession.NewRegistry()
	legacyNodeTransport := nodetransport.NewLegacyHTTPTransport(cfg.Auth.SharedSecret)
	var controlGateway *nodegateway.Gateway
	var commandManager *nodecommand.Manager
	var nodeTransport nodetransport.NodeTransport = legacyNodeTransport
	if cfg.NodeControl.Enabled {
		dataSessionRegistry := nodedata.NewRegistry(nodedata.Config{
			MaxConcurrency: cfg.NodeControl.DataMaxConcurrencyPerNode,
			MaxChunkSize:   cfg.NodeControl.DataChunkSize,
		})
		commandManager, err = nodecommand.New(db, sessionRegistry, nodecommand.Config{
			WaitTimeout: time.Duration(cfg.NodeControl.CommandTimeoutSeconds) * time.Second,
			CommandTTL:  24 * time.Hour,
		})
		if err != nil {
			log.Fatalf("Failed to initialize durable node commands: %v", err)
		}
		controlTransport := nodetransport.NewControlStreamTransport(sessionRegistry, func(ctx context.Context, target nodetransport.Target, notification nodetransport.Notification) (uint64, error) {
			if notification.Type != nodecontract.NotificationConfigRevisionChanged {
				return 0, nil
			}
			server, err := db.GetServer(target.NodeID)
			if err != nil {
				return 0, err
			}
			return server.DesiredRevision, nil
		}, commandManager)
		dataTransport := nodetransport.NewDataStreamTransport(dataSessionRegistry)
		nodeTransport = nodetransport.NewMigrationTransportWithLegacy(legacyNodeTransport, controlTransport, cfg.NodeControl.LegacyHTTPEnabled, dataTransport)
		controlGateway, err = nodegateway.New(db, sessionRegistry, func(rawCredential, nodeUUID string, usedAt time.Time) (nodegateway.Principal, error) {
			principal, authErr := nodeEnrollmentService.AuthenticateCredential(rawCredential, nodeUUID, usedAt)
			if authErr != nil {
				return nodegateway.Principal{}, authErr
			}
			return nodegateway.Principal{ServerID: principal.ServerID, NodeUUID: principal.NodeUUID}, nil
		}, nodegateway.Config{
			HeartbeatInterval: time.Duration(cfg.NodeControl.HeartbeatIntervalSeconds) * time.Second,
			LeaseTimeout:      time.Duration(cfg.NodeControl.LeaseTimeoutSeconds) * time.Second,
			Commands:          commandManager,
			DataSessions:      dataSessionRegistry,
		})
		if err != nil {
			log.Fatalf("Failed to initialize node control gateway: %v", err)
		}
	}
	allocator := service.NewAllocator(db, cfg, nodeTransport)
	importRealAccounts(db, cfg)
	if err := db.SeedServerDomainsFromAccounts(); err != nil {
		log.Printf("[WARN] seed server_domains failed: %v", err)
	}

	// Init handlers
	mailboxH := handler.NewMailboxHandler(db, allocator, nodeTransport)
	emailH := handler.NewEmailHandler(db, nodeTransport)
	serverH := handler.NewServerHandler(db, nodeTransport, cfg.NodeControl.LegacyHTTPEnabled)
	filterH := handler.NewFilterHandler(db, nodeTransport)
	filterPolicyService := service.NewFilterPolicyService(db)
	filterPolicyH := handler.NewFilterPolicyHandler(filterPolicyService)
	filterPolicyH.ConfigureNodeTransport(nodeTransport)
	adminH := handler.NewAdminHandler(db)
	healthH := handler.NewHealthHandler(db)
	configH := handler.NewConfigHandler(db, nodeTransport)
	integratedH := handler.NewIntegratedMailboxHandler(db, nodeTransport)
	externalAccessH := handler.NewExternalAccessHandler(db)
	nodeEnrollmentH := handler.NewNodeEnrollmentHandler(nodeEnrollmentService)
	nodeEnrollmentH.ConfigureSessionRevoker(sessionRegistry)

	// Session manager
	sessionDuration := time.Duration(db.GetConfigInt("session.duration_hours", 24)) * time.Hour
	sessionCookieName := db.GetConfig("session.cookie_name", "mgmt_session")
	sessionGCInterval := time.Duration(db.GetConfigInt("session.gc_interval_minutes", 30)) * time.Minute
	sessionMgr := middleware.NewSessionManager(sessionDuration, sessionCookieName, sessionGCInterval)

	cookieSecure := db.GetConfigBool("session.cookie_secure", false)
	if value := os.Getenv("MAILHUB_SESSION_COOKIE_SECURE"); value != "" {
		cookieSecure = value == "1" || value == "true" || value == "TRUE"
	}
	authH := handler.NewAuthHandler(credentials, sessionMgr, cookieSecure)
	accountH := handler.NewAccountHandler(credentials)

	// Set Gin mode
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()
	r.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<20)
		c.Next()
	})

	// Load static assets (React SPA is at template/static/admin-app/)
	r.LoadHTMLGlob("template/admin/*.html")
	r.Static("/static", "template/static")

	// ---- Health checks (public) ----
	r.GET("/health", healthH.Health)
	r.GET("/health/ready", healthH.Ready)

	// ---- Public login/logout (no auth required) ----
	authGroup := r.Group("/admin")
	authGroup.GET("/login", authH.LoginPage)
	authGroup.POST("/login", authH.LoginAction)
	authGroup.GET("/logout", authH.LogoutAction)
	authGroup.POST("/logout", authH.LogoutAction)

	// ---- Admin pages (Session auth) ----
	adminAuth := middleware.AdminAuthRequired(sessionMgr, credentials)
	protectedPages := r.Group("/admin")
	protectedPages.Use(adminAuth)
	adminH.RegisterProtectedRoutes(protectedPages)

	// ---- Admin API (Session auth) ----
	apiAdmin := r.Group("/api/v1/admin")
	apiAdmin.Use(adminAuth)
	serverH.RegisterAdminRoutes(apiAdmin)
	filterH.RegisterAdminRoutes(apiAdmin)
	filterPolicyH.RegisterAdminRoutes(apiAdmin)
	mailboxH.RegisterAdminRoutes(apiAdmin)
	emailH.RegisterAdminRoutes(apiAdmin)
	integratedH.RegisterAdminRoutes(apiAdmin)
	externalAccessH.RegisterAdminRoutes(apiAdmin)
	nodeEnrollmentH.RegisterAdminRoutes(apiAdmin)
	// Dashboard stats API
	apiAdmin.GET("/dashboard", adminH.DashboardAPI)
	// Domains list (for dropdown filters)
	apiAdmin.GET("/domains", adminH.ListDomainsAPI)
	// System config API
	apiAdmin.GET("/configs", configH.ListConfigs)
	apiAdmin.GET("/configs/:key", configH.GetConfig)
	apiAdmin.PUT("/configs/:key", configH.UpdateConfig)
	apiAdmin.POST("/configs/batch", configH.BatchUpdate)
	apiAdmin.POST("/configs/:key/reset", configH.ResetConfig)
	apiAdmin.POST("/configs/reload", configH.ReloadNode)
	apiAdmin.GET("/servers/:id/configs", configH.GetServerConfigs)
	apiAdmin.PUT("/servers/:id/configs/:key", configH.PutServerConfig)
	apiAdmin.DELETE("/servers/:id/configs/:key", configH.DeleteServerConfig)
	apiAdmin.GET("/account", accountH.Get)
	apiAdmin.PUT("/account", accountH.Update)

	// ---- Node enrollment bootstrap (invitation/request-secret auth) ----
	nodeEnrollmentBootstrap := r.Group("/api/v1/node-enrollments")
	nodeEnrollmentH.RegisterBootstrapRoutes(nodeEnrollmentBootstrap)

	// ---- External API v1 (Bearer Token auth + Scope) ----
	api := r.Group("/api/v1")
	api.Use(middleware.AuthRequired(db))
	externalRegistry := apiregistry.New("/api/v1")

	mailboxH.RegisterExternalRoutes(externalRegistry, api)
	emailH.RegisterExternalRoutes(externalRegistry, api)
	filterPolicyH.RegisterExternalRoutes(externalRegistry, api)

	if err := externalRegistry.Sync(db); err != nil {
		log.Fatalf("Failed to sync external API registry: %v", err)
	}
	if err := db.RetireLegacyAPITokens(tokenSeeds, service.HashAPIToken); err != nil {
		log.Fatalf("Failed to retire legacy API tokens: %v", err)
	}

	// ---- Internal API (mail-node calls, Shared-Secret auth) ----
	internal := r.Group("/api/v1/internal")
	internal.Use(middleware.InternalNodeAuthRequired(cfg.Auth.SharedSecret, nodeEnrollmentService))
	internal.POST("/servers/heartbeat", serverH.Heartbeat)
	internal.POST("/servers/discover", serverH.DiscoverServer)
	filterH.RegisterInternalRoutes(internal)
	filterPolicyH.RegisterInternalRoutes(internal)
	internal.GET("/sync/deleting", mailboxH.SyncDeleting)
	// Dynamic config pull (mail-node)
	internal.GET("/configs", configH.ListConfigsInternal)
	internal.POST("/configs/reload", configH.ReloadNodeInternal)
	internal.POST("/servers/:id/config-snapshot", configH.ReportServerConfigSnapshot)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var grpcServer *grpc.Server
	if controlGateway != nil {
		server, listener, gatewayErr := nodegateway.NewTLSServer(
			cfg.NodeControl.Listen, cfg.NodeControl.TLSCertFile, cfg.NodeControl.TLSKeyFile, controlGateway,
		)
		if gatewayErr != nil {
			log.Fatalf("Failed to start node control gateway: %v", gatewayErr)
		}
		grpcServer = server
		go controlGateway.ReapExpiredLeases(ctx)
		go commandManager.ReapExpired(ctx)
		go func() {
			log.Printf("Starting node control gateway on %s", cfg.NodeControl.Listen)
			if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
				log.Printf("Node control gateway stopped unexpectedly: %v", serveErr)
				cancel()
			}
		}()
	}
	healthScheduler := healthcheck.NewScheduler(db, nodeTransport, 0, 0)
	go healthScheduler.Start(ctx)

	lifecycleScheduler := lifecycle.NewScheduler(db, nodeTransport, 0)
	go lifecycleScheduler.Start(ctx)

	// Graceful shutdown
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("Shutting down server...")
		cancel()
	}()

	// Start
	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Server.Port)
	log.Printf("Starting management system on %s (mode: %s)", addr, cfg.Server.Mode)
	server := &http.Server{
		Addr: addr, Handler: r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
		if grpcServer != nil {
			done := make(chan struct{})
			go func() {
				grpcServer.GracefulStop()
				close(done)
			}()
			select {
			case <-done:
			case <-shutdownCtx.Done():
				grpcServer.Stop()
			}
		}
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}
	cancel()
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

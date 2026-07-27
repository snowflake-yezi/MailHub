package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/mailboxaddr"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodetransport"
	"github.com/ticket/email-mgmt-system/internal/store"
	"gorm.io/gorm"
)

type ServerHandler struct {
	store             *store.Store
	transport         nodetransport.NodeTransport
	legacyHTTPEnabled bool
}

func NewServerHandler(s *store.Store, transport nodetransport.NodeTransport, legacyHTTPEnabled ...bool) *ServerHandler {
	legacyEnabled := true
	if len(legacyHTTPEnabled) > 0 {
		legacyEnabled = legacyHTTPEnabled[0]
	}
	return &ServerHandler{store: s, transport: transport, legacyHTTPEnabled: legacyEnabled}
}

type DNSRecord struct {
	Type  string `json:"type"`
	Host  string `json:"host"`
	Value string `json:"value"`
}

type RemoteDomainSetup struct {
	Domain        string      `json:"domain"`
	PostfixStatus string      `json:"postfix_status"`
	DKIMStatus    string      `json:"dkim_status"`
	DKIMSelector  string      `json:"dkim_selector"`
	DKIMPublicKey string      `json:"dkim_public_key"`
	DKIMError     string      `json:"dkim_error"`
	DNSRecords    []DNSRecord `json:"dns_records"`
}

type nodeResponse struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Data    RemoteDomainSetup `json:"data"`
}

type createServerRequest struct {
	Name              string   `json:"name"`
	APIHost           string   `json:"api_host"`
	SMTPHost          string   `json:"smtp_host"`
	IMAPHost          string   `json:"imap_host"`
	PublicHost        string   `json:"public_host"`
	MailPublicIPs     []string `json:"mail_public_ips"`
	Capacity          int      `json:"capacity"`
	HeartbeatInterval int      `json:"heartbeat_interval"`
}

type updateServerRequest struct {
	Name              *string   `json:"name"`
	APIHost           *string   `json:"api_host"`
	SMTPHost          *string   `json:"smtp_host"`
	IMAPHost          *string   `json:"imap_host"`
	PublicHost        *string   `json:"public_host"`
	MailPublicIPs     *[]string `json:"mail_public_ips"`
	Capacity          *int      `json:"capacity"`
	Status            *string   `json:"status"`
	HeartbeatInterval *int      `json:"heartbeat_interval"`
}

type transportSwitchRequest struct {
	TransportMode string `json:"transport_mode"`
}

type transportPreflightResult struct {
	Ready          bool                 `json:"ready"`
	LegacyEnabled  bool                 `json:"legacy_http_enabled"`
	TotalNodes     int                  `json:"total_nodes"`
	ControlNodes   int                  `json:"control_stream_nodes"`
	BlockingIssues []string             `json:"blocking_issues,omitempty"`
	Nodes          []transportNodeCheck `json:"nodes"`
}

type transportNodeCheck struct {
	ServerID uint64   `json:"server_id"`
	Name     string   `json:"name"`
	Mode     string   `json:"transport_mode"`
	Ready    bool     `json:"ready"`
	Issues   []string `json:"issues,omitempty"`
}

// TransportPreflight reports whether the fleet satisfies the P7 final cutover
// gate. It is intentionally read-only; operators must still switch nodes one
// at a time and retain rollback evidence before changing the global flag.
func (h *ServerHandler) TransportPreflight(c *gin.Context) {
	servers, err := h.store.ListServers()
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list servers")
		return
	}
	result := transportPreflightResult{LegacyEnabled: h.legacyHTTPEnabled, TotalNodes: len(servers)}
	for _, server := range servers {
		server.ApplyLegacyNodeDefaults()
		check := transportNodeCheck{ServerID: server.ID, Name: server.Name, Mode: server.TransportMode}
		var issues []string
		if server.TransportMode != model.TransportControlStream {
			issues = append(issues, "node is not control_stream")
		} else {
			result.ControlNodes++
		}
		if server.ConnectionState != model.ConnectionConnected {
			issues = append(issues, "control connection is not connected")
		}
		if server.LeaseExpiresAt == nil || !server.LeaseExpiresAt.After(time.Now().UTC()) {
			issues = append(issues, "control lease is expired")
		}
		if server.ReadinessState != model.ReadinessReady {
			issues = append(issues, "readiness is not ready")
		}
		check.Issues = issues
		check.Ready = len(issues) == 0
		if !check.Ready {
			result.BlockingIssues = append(result.BlockingIssues, fmt.Sprintf("server %d: %s", server.ID, strings.Join(issues, "; ")))
		}
		result.Nodes = append(result.Nodes, check)
	}
	result.Ready = len(result.BlockingIssues) == 0 && result.TotalNodes > 0
	success(c, "transport preflight completed", result)
}

// RegisterServer 注册新邮箱服务器
// POST /api/v1/admin/servers
func (h *ServerHandler) RegisterServer(c *gin.Context) {
	var req createServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, ErrCodeParamMissing, "invalid server data: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.APIHost) == "" {
		badRequest(c, ErrCodeParamMissing, "name and api_host are required")
		return
	}
	srv := model.MailServer{
		Name: req.Name, APIHost: req.APIHost, SMTPHost: req.SMTPHost, IMAPHost: req.IMAPHost,
		PublicHost: req.PublicHost, MailPublicIPs: req.MailPublicIPs, Capacity: req.Capacity,
		HeartbeatInterval: req.HeartbeatInterval,
	}
	if srv.Capacity == 0 {
		srv.Capacity = h.store.GetConfigInt("general.default_server_capacity", 5000)
	}
	if srv.Capacity <= 0 {
		badRequest(c, ErrCodeParamInvalid, "capacity must be positive")
		return
	}
	if srv.HeartbeatInterval == 0 {
		srv.HeartbeatInterval = 30
	}
	if srv.HeartbeatInterval < 5 || srv.HeartbeatInterval > 600 {
		badRequest(c, ErrCodeParamInvalid, "heartbeat_interval must be between 5 and 600")
		return
	}
	srv.Status = "healthy"
	deriveHostDefaults(&srv)
	if err := normalizeServerAddresses(&srv); err != nil {
		badRequest(c, ErrCodeParamInvalid, err.Error())
		return
	}

	if err := h.store.CreateServer(&srv); err != nil {
		serverError(c, ErrCodeInternal, "failed to register server: "+err.Error())
		return
	}

	created(c, "server registered", srv)
}

// ListServers 服务器列表
// GET /api/v1/admin/servers
func (h *ServerHandler) ListServers(c *gin.Context) {
	list, err := h.store.ListServers()
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list servers")
		return
	}
	if bindings, berr := h.store.ListActiveServerDomains(); berr == nil {
		attachDomains(list, bindings)
	}
	h.store.AttachServerConfigSummaries(list, trashRetentionKey, h.store.GetConfig(trashRetentionKey, "24"))
	success(c, "success", list)
}

// GetServer 单台服务器详情
// GET /api/v1/admin/servers/:id
func (h *ServerHandler) GetServer(c *gin.Context) {
	id := parseUint64(c.Param("id"))
	srv, err := h.store.GetServer(id)
	if err != nil {
		notFound(c, "server not found")
		return
	}
	success(c, "success", srv)
}

// ListServerDomains 某服务器的域名池（含远端同步状态）
// GET /api/v1/admin/servers/:id/domains
func (h *ServerHandler) ListServerDomains(c *gin.Context) {
	id := parseUint64(c.Param("id"))
	if id == 0 {
		badRequest(c, ErrCodeParamMissing, "invalid server id")
		return
	}
	if _, err := h.store.GetServer(id); err != nil {
		notFound(c, "server not found")
		return
	}
	list, err := h.store.ListDomainsByServer(id)
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list server domains")
		return
	}
	for i := range list {
		count, countErr := h.store.CountMailboxesOnServerDomain(id, list[i].DomainID)
		if countErr != nil {
			serverError(c, ErrCodeInternal, "failed to count server domain mailboxes")
			return
		}
		list[i].MailboxCount = count
	}
	success(c, "success", list)
}

// AddServerDomain 将域名添加到指定服务器域名池，并调用 mail-node 配置远端 Postfix/DKIM。
// POST /api/v1/admin/servers/:id/domains
func (h *ServerHandler) AddServerDomain(c *gin.Context) {
	serverID := parseUint64(c.Param("id"))
	srv, err := h.store.GetServer(serverID)
	if err != nil {
		notFound(c, "server not found")
		return
	}

	var req struct {
		Name        string `json:"name" binding:"required"`
		ARecordHost string `json:"a_record_host"`
		MXHost      string `json:"mx_host"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, ErrCodeParamMissing, "domain name required")
		return
	}
	name, err := mailboxaddr.NormalizeDomain(req.Name)
	if err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid domain name")
		return
	}
	aRecordHost := strings.TrimSpace(req.ARecordHost)
	if aRecordHost == "" {
		aRecordHost = strings.TrimSpace(req.MXHost)
	}
	if aRecordHost == "" {
		aRecordHost = strings.TrimSpace(srv.PublicHost)
	}
	mxHost, err := normalizeMXHost(aRecordHost, name)
	if err != nil {
		badRequest(c, ErrCodeParamInvalid, err.Error())
		return
	}

	domain, err := h.store.GetDomainByName(name)
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			serverError(c, ErrCodeInternal, "failed to load domain: "+err.Error())
			return
		}
		domain = &model.Domain{Name: name, MXServer: mxHost, Status: "active"}
		if err := h.store.CreateDomain(domain); err != nil {
			serverError(c, ErrCodeInternal, "failed to create domain: "+err.Error())
			return
		}
	}
	if domain.Status != "active" {
		domain.Status = "active"
	}
	if domain.MXServer != mxHost {
		domain.MXServer = mxHost
		if err := h.store.UpdateDomain(domain); err != nil {
			serverError(c, ErrCodeInternal, "failed to update domain mx host: "+err.Error())
			return
		}
	}

	pending := &model.ServerDomain{
		ServerID:      srv.ID,
		DomainID:      domain.ID,
		Status:        "active",
		SyncStatus:    "pending",
		PostfixStatus: "pending",
		DkimStatus:    "pending",
	}
	if err := h.store.BindServerDomain(pending); err != nil {
		serverError(c, ErrCodeInternal, "failed to bind server domain: "+err.Error())
		return
	}

	setup, err := h.callNodeAddDomain(c.Request.Context(), srv, name)
	if err != nil {
		if acceptedOperation(c, err, "domain setup accepted") {
			return
		}
		_ = h.store.UpdateServerDomainSync(srv.ID, domain.ID, map[string]interface{}{
			"status":         "active",
			"sync_status":    "sync_failed",
			"postfix_status": "sync_failed",
			"dkim_status":    "sync_failed",
			"sync_error":     err.Error(),
		})
		serverError(c, ErrCodeExternalFail, "failed to setup remote domain: "+err.Error())
		return
	}

	syncStatus := "synced"
	syncError := ""
	if setup.PostfixStatus != "synced" {
		syncStatus = "sync_failed"
	} else if setup.DKIMStatus != "synced" {
		syncStatus = "partial"
		syncError = setup.DKIMError
	}
	setup.DNSRecords = dnsRecordsForMXHost(setup.DNSRecords, name, mxHost, srv.MailPublicIPs)
	now := time.Now()
	if err := h.store.UpdateServerDomainSync(srv.ID, domain.ID, map[string]interface{}{
		"status":          "active",
		"sync_status":     syncStatus,
		"postfix_status":  setup.PostfixStatus,
		"dkim_status":     setup.DKIMStatus,
		"dkim_selector":   setup.DKIMSelector,
		"dkim_public_key": setup.DKIMPublicKey,
		"sync_error":      syncError,
		"synced_at":       &now,
	}); err != nil {
		serverError(c, ErrCodeInternal, "failed to update server domain: "+err.Error())
		return
	}

	success(c, "domain added", gin.H{
		"domain":        domain,
		"server_domain": gin.H{"server_id": srv.ID, "domain_id": domain.ID, "sync_status": syncStatus},
		"setup":         setup,
	})
}

func validDomainName(value string) bool {
	_, err := mailboxaddr.NormalizeDomain(value)
	return err == nil
}

// RemoveServerDomain 从服务器域名池移除域名；有邮箱账号时拒绝。
// DELETE /api/v1/admin/servers/:id/domains/:domain_id
func (h *ServerHandler) RemoveServerDomain(c *gin.Context) {
	serverID := parseUint64(c.Param("id"))
	domainID := parseUint64(c.Param("domain_id"))
	if serverID == 0 || domainID == 0 {
		badRequest(c, ErrCodeParamMissing, "invalid server or domain id")
		return
	}
	srv, err := h.store.GetServer(serverID)
	if err != nil {
		notFound(c, "server not found")
		return
	}
	_, err = h.store.GetActiveServerDomain(serverID, domainID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			notFound(c, "active server-domain binding not found")
			return
		}
		serverError(c, ErrCodeInternal, "failed to load server-domain binding")
		return
	}
	domain, err := h.store.GetDomainByID(domainID)
	if err != nil {
		notFound(c, "domain not found")
		return
	}
	count, err := h.store.CountMailboxesOnServerDomain(serverID, domainID)
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to check mailboxes")
		return
	}
	if count > 0 {
		badRequest(c, ErrCodeBusiness, fmt.Sprintf("domain has %d mailboxes on this server", count))
		return
	}
	if err := h.callNodeRemoveDomain(c.Request.Context(), srv, domain.Name); err != nil {
		if acceptedOperation(c, err, "domain removal accepted") {
			return
		}
		serverError(c, ErrCodeExternalFail, "failed to remove remote domain: "+err.Error())
		return
	}
	if err := h.store.MarkServerDomainRemoved(serverID, domainID); err != nil {
		serverError(c, ErrCodeInternal, "failed to update server domain status")
		return
	}
	success(c, "domain removed", nil)
}

// UpdateServer 修改服务器
// PUT /api/v1/admin/servers/:id
func (h *ServerHandler) UpdateServer(c *gin.Context) {
	id := parseUint64(c.Param("id"))
	existing, err := h.store.GetServer(id)
	if err != nil {
		notFound(c, "server not found")
		return
	}

	var update updateServerRequest
	if err := c.ShouldBindJSON(&update); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid update data")
		return
	}

	// 只更新允许的字段
	if update.Name != nil {
		existing.Name = strings.TrimSpace(*update.Name)
		if existing.Name == "" {
			badRequest(c, ErrCodeParamInvalid, "name cannot be empty")
			return
		}
	}
	if update.APIHost != nil {
		existing.APIHost = strings.TrimSpace(*update.APIHost)
		if existing.APIHost == "" {
			badRequest(c, ErrCodeParamInvalid, "api_host cannot be empty")
			return
		}
	}
	if update.SMTPHost != nil {
		existing.SMTPHost = strings.TrimSpace(*update.SMTPHost)
		if existing.SMTPHost == "" {
			badRequest(c, ErrCodeParamInvalid, "smtp_host cannot be empty")
			return
		}
	}
	if update.IMAPHost != nil {
		existing.IMAPHost = strings.TrimSpace(*update.IMAPHost)
		if existing.IMAPHost == "" {
			badRequest(c, ErrCodeParamInvalid, "imap_host cannot be empty")
			return
		}
	}
	if update.PublicHost != nil {
		existing.PublicHost = *update.PublicHost
	}
	if update.MailPublicIPs != nil {
		existing.MailPublicIPs = *update.MailPublicIPs
	}
	if update.Capacity != nil {
		if *update.Capacity <= 0 {
			badRequest(c, ErrCodeParamInvalid, "capacity must be positive")
			return
		}
		existing.Capacity = *update.Capacity
	}
	if update.Status != nil {
		if !validLegacyServerStatus(*update.Status) {
			badRequest(c, ErrCodeParamInvalid, "invalid server status")
			return
		}
		existing.ApplyLegacyAdminStatus(*update.Status)
	}
	if update.HeartbeatInterval != nil {
		if *update.HeartbeatInterval < 5 || *update.HeartbeatInterval > 600 {
			badRequest(c, ErrCodeParamInvalid, "heartbeat_interval must be between 5 and 600")
			return
		}
		existing.HeartbeatInterval = *update.HeartbeatInterval
	}
	if err := normalizeServerAddresses(existing); err != nil {
		badRequest(c, ErrCodeParamInvalid, err.Error())
		return
	}

	if err := h.store.UpdateServer(existing); err != nil {
		serverError(c, ErrCodeInternal, "failed to update server")
		return
	}

	success(c, "updated", existing)
}

// SwitchTransport performs the explicit P7 canary/rollback transition for one node.
// It requires a live control lease before entering dual or control_stream so a
// bad node cannot be marked allocatable merely by changing a database field.
func (h *ServerHandler) SwitchTransport(c *gin.Context) {
	id := parseUint64(c.Param("id"))
	server, err := h.store.GetServer(id)
	if err != nil {
		notFound(c, "server not found")
		return
	}
	var request transportSwitchRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid transport mode")
		return
	}
	mode := strings.TrimSpace(request.TransportMode)
	if err := validateTransportSwitch(server, mode, h.legacyHTTPEnabled, time.Now().UTC()); err != nil {
		badRequest(c, ErrCodeBusiness, err.Error())
		return
	}
	previous := server.TransportMode
	server.TransportMode = mode
	actor := "unknown"
	if value, ok := c.Get("admin_user"); ok {
		if name, ok := value.(string); ok && strings.TrimSpace(name) != "" {
			actor = strings.TrimSpace(name)
		}
	}
	nodeUUID := ""
	if server.NodeUUID != nil {
		nodeUUID = *server.NodeUUID
	}
	if err := h.store.UpdateServerTransportWithAudit(server, nodeUUID, actor, c.ClientIP(), previous, mode); err != nil {
		serverError(c, ErrCodeInternal, "failed to update transport mode")
		return
	}
	success(c, "transport mode updated", server)
}

func validateTransportSwitch(server *model.MailServer, mode string, legacyEnabled bool, now time.Time) error {
	if server == nil {
		return errors.New("server is required")
	}
	switch mode {
	case model.TransportLegacyHTTP:
		if !legacyEnabled {
			return errors.New("legacy HTTP transport is disabled")
		}
	case model.TransportDual, model.TransportControlStream:
		if server.NodeUUID == nil || strings.TrimSpace(*server.NodeUUID) == "" {
			return errors.New("node must be enrolled before enabling outbound transport")
		}
		if server.ConnectionState != model.ConnectionConnected || server.LeaseExpiresAt == nil || !server.LeaseExpiresAt.After(now) {
			return errors.New("node must have an active control lease before enabling outbound transport")
		}
		if server.ReadinessState != model.ReadinessReady {
			return errors.New("node must be ready before enabling outbound transport")
		}
		if mode == model.TransportDual && !legacyEnabled {
			return errors.New("dual transport requires legacy HTTP fallback to remain enabled")
		}
	default:
		return fmt.Errorf("invalid transport mode: %s", mode)
	}
	return nil
}

// DeleteServer 删除服务器
// DELETE /api/v1/admin/servers/:id
func (h *ServerHandler) DeleteServer(c *gin.Context) {
	id := parseUint64(c.Param("id"))
	if id == 0 {
		badRequest(c, ErrCodeParamMissing, "invalid server id")
		return
	}

	if _, err := h.store.GetServer(id); err != nil {
		notFound(c, "server not found")
		return
	}

	// 检查是否有邮箱仍分配在此服务器
	count, err := h.store.CountMailboxesOnServer(id)
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to check mailboxes")
		return
	}
	if count > 0 {
		badRequest(c, ErrCodeBusiness, fmt.Sprintf("server has %d mailboxes, reassign or remove them first", count))
		return
	}

	if err := h.store.DeleteServer(id); err != nil {
		serverError(c, ErrCodeInternal, "failed to delete server")
		return
	}

	success(c, "server deleted", nil)
}

// DiscoverServer mail-node 启动时自动发现/注册自己的 server_id。
// POST /api/v1/internal/servers/discover
// Body: {"api_host": "203.0.113.20:8081", "node_name": "mail-node-01"}
// 按 api_host 匹配已有服务器；未匹配时自动创建（name=node_name, 容量默认 5000）。
func (h *ServerHandler) DiscoverServer(c *gin.Context) {
	var req struct {
		APIHost  string `json:"api_host" binding:"required"`
		NodeName string `json:"node_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, ErrCodeParamMissing, "api_host is required")
		return
	}

	// 查找已有服务器
	existing, err := h.store.GetServerByAPIHost(req.APIHost)
	if err == nil {
		success(c, "found", gin.H{"server_id": existing.ID, "created": false})
		return
	}

	// 未找到 → 自动注册
	name := req.NodeName
	if name == "" {
		name = req.APIHost
	}
	srv := &model.MailServer{
		Name:     name,
		APIHost:  req.APIHost,
		Capacity: h.store.GetConfigInt("general.default_server_capacity", 5000),
		Status:   "healthy",
	}
	deriveHostDefaults(srv)
	if err := h.store.CreateServer(srv); err != nil {
		serverError(c, ErrCodeInternal, "failed to auto-register server: "+err.Error())
		return
	}
	log.Printf("[discovery] auto-registered server: %s (id=%d, api_host=%s)", name, srv.ID, req.APIHost)
	success(c, "created", gin.H{"server_id": srv.ID, "created": true})
}

// Heartbeat 服务器心跳上报
// POST /api/v1/internal/servers/heartbeat
func (h *ServerHandler) Heartbeat(c *gin.Context) {
	var req struct {
		ServerID        uint64    `json:"server_id"`
		Status          string    `json:"status"`
		Load            int       `json:"load"`
		DiskUsage       string    `json:"disk_usage"`
		NodeName        string    `json:"node_name"`
		AppliedRevision uint64    `json:"applied_revision"`
		LastApplyError  string    `json:"last_apply_error"`
		BootID          string    `json:"boot_id"`
		StartedAt       time.Time `json:"started_at"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, ErrCodeParamMissing, "invalid heartbeat data")
		return
	}

	if req.ServerID == 0 {
		badRequest(c, ErrCodeParamMissing, "server_id required")
		return
	}

	server, err := h.store.GetServer(req.ServerID)
	if err != nil {
		notFound(c, "server not found")
		return
	}
	if req.AppliedRevision > server.DesiredRevision {
		badRequest(c, ErrCodeParamInvalid, "applied_revision exceeds desired_revision")
		return
	}
	appliedRevision := req.AppliedRevision
	if appliedRevision < server.AppliedRevision {
		appliedRevision = 0
	}
	bootID := req.BootID
	startedAt := req.StartedAt
	if server.LastStartedAt != nil && !startedAt.IsZero() && startedAt.Before(*server.LastStartedAt) {
		bootID = ""
		startedAt = time.Time{}
	}
	if err := h.store.UpdateServerHeartbeatState(req.ServerID, req.Load, appliedRevision, req.LastApplyError, bootID, startedAt); err != nil {
		serverError(c, ErrCodeInternal, "failed to update heartbeat")
		return
	}

	// 下发期望心跳间隔，mail-node 据此动态调整节拍（SP-6'）。server 不存在或字段缺失时回退 30。
	interval := 30
	if server.HeartbeatInterval >= 5 {
		interval = server.HeartbeatInterval
	}
	success(c, "heartbeat received", gin.H{"heartbeat_interval": interval})
}

// RegisterAdminRoutes registers all server admin API routes on the given (already auth-protected) group.
func (h *ServerHandler) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.POST("/servers", h.RegisterServer)
	r.GET("/servers", h.ListServers)
	r.GET("/servers/transport-preflight", h.TransportPreflight)
	r.GET("/servers/:id", h.GetServer)
	r.PUT("/servers/:id", h.UpdateServer)
	r.POST("/servers/:id/transport", h.SwitchTransport)
	r.DELETE("/servers/:id", h.DeleteServer)
	r.GET("/servers/:id/domains", h.ListServerDomains)
	r.POST("/servers/:id/domains", h.AddServerDomain)
	r.DELETE("/servers/:id/domains/:domain_id", h.RemoveServerDomain)
}

func dnsRecordsForMXHost(records []DNSRecord, domain, mxHost string, publicIPs []string) []DNSRecord {
	out := make([]DNSRecord, 0, len(records)+len(publicIPs))
	for _, value := range publicIPs {
		ip := net.ParseIP(value)
		if ip == nil {
			continue
		}
		recordType := "AAAA"
		if ip.To4() != nil {
			recordType = "A"
		}
		out = append(out, DNSRecord{Type: recordType, Host: mxHost, Value: ip.String()})
	}
	for _, r := range records {
		if strings.EqualFold(r.Type, "A") || strings.EqualFold(r.Type, "AAAA") {
			continue
		}
		if strings.EqualFold(r.Type, "MX") {
			r.Host = domain
			r.Value = mxHost
		}
		out = append(out, r)
	}
	return out
}

func normalizeMXHost(input, domain string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(input, ".")))
	if host == "" {
		host = "mail"
	}
	if host == "@" {
		return domain, nil
	}
	if strings.ContainsAny(host, "/\\@ ") {
		return "", fmt.Errorf("invalid A record host")
	}
	if strings.Contains(host, ".") {
		if !strings.HasSuffix(host, "."+domain) && host != domain {
			return "", fmt.Errorf("A record host must be inside %s", domain)
		}
		return host, nil
	}
	return host + "." + domain, nil
}

func (h *ServerHandler) callNodeAddDomain(ctx context.Context, server *model.MailServer, domain string) (*RemoteDomainSetup, error) {
	resp, err := h.transport.Execute(ctx, nodeTarget(server), nodetransport.DomainApply(domain))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream error: %d - %s", resp.StatusCode, string(resp.Body))
	}
	var nr nodeResponse
	if err := json.Unmarshal(resp.Body, &nr); err != nil {
		return nil, err
	}
	if nr.Code != 0 {
		return nil, fmt.Errorf(nr.Message)
	}
	return &nr.Data, nil
}

func (h *ServerHandler) callNodeRemoveDomain(ctx context.Context, server *model.MailServer, domain string) error {
	resp, err := h.transport.Execute(ctx, nodeTarget(server), nodetransport.DomainRemove(domain))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("upstream error: %d - %s", resp.StatusCode, string(resp.Body))
	}
	return nil
}

// extractHost extracts host from "host:port" addr for auto-registration.
func extractHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// deriveHostDefaults 在 SMTP/IMAP 为空时从 api_host 推导，统一 RegisterServer 与
// DiscoverServer 两条注册路径的 host 处理（SP-1）。
func deriveHostDefaults(srv *model.MailServer) {
	if srv.SMTPHost == "" {
		srv.SMTPHost = extractHost(srv.APIHost)
	}
	if srv.IMAPHost == "" {
		srv.IMAPHost = extractHost(srv.APIHost)
	}
}

func normalizeServerAddresses(srv *model.MailServer) error {
	srv.Name = strings.TrimSpace(srv.Name)
	srv.APIHost = strings.TrimSpace(srv.APIHost)
	srv.SMTPHost = strings.TrimSpace(srv.SMTPHost)
	srv.IMAPHost = strings.TrimSpace(srv.IMAPHost)

	publicHost := strings.TrimSpace(strings.TrimSuffix(srv.PublicHost, "."))
	if publicHost != "" {
		normalized, err := mailboxaddr.NormalizeDomain(publicHost)
		if err != nil {
			return fmt.Errorf("invalid public_host")
		}
		publicHost = normalized
	}
	srv.PublicHost = publicHost

	seen := make(map[string]struct{}, len(srv.MailPublicIPs))
	publicIPs := make([]string, 0, len(srv.MailPublicIPs))
	for _, value := range srv.MailPublicIPs {
		ip := net.ParseIP(strings.TrimSpace(value))
		if ip == nil {
			return fmt.Errorf("invalid mail_public_ips value %q", value)
		}
		canonical := ip.String()
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		publicIPs = append(publicIPs, canonical)
	}
	srv.MailPublicIPs = publicIPs
	return nil
}

func validLegacyServerStatus(status string) bool {
	switch status {
	case "healthy", "degraded", "down", "draining":
		return true
	default:
		return false
	}
}

// attachDomains 把 active 绑定按 server_id 分组装入各 server 的 Domains（SP-5），
// 供服务器列表「关联域名」列展示。
func attachDomains(servers []model.MailServer, bindings []model.ServerDomain) {
	bucket := make(map[uint64][]model.Domain, len(servers))
	for _, b := range bindings {
		bucket[b.ServerID] = append(bucket[b.ServerID], b.Domain)
	}
	for i := range servers {
		servers[i].Domains = bucket[servers[i].ID]
	}
}

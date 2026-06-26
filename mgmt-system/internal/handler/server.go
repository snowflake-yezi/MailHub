package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
	"gorm.io/gorm"
)

type ServerHandler struct {
	store        *store.Store
	client       *http.Client
	sharedSecret string
}

func NewServerHandler(s *store.Store, sharedSecret string) *ServerHandler {
	return &ServerHandler{store: s, client: &http.Client{Timeout: 15 * time.Second}, sharedSecret: sharedSecret}
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

// RegisterServer 注册新邮箱服务器
// POST /api/v1/admin/servers
func (h *ServerHandler) RegisterServer(c *gin.Context) {
	var srv model.MailServer
	if err := c.ShouldBindJSON(&srv); err != nil {
		badRequest(c, ErrCodeParamMissing, "invalid server data: "+err.Error())
		return
	}

	if srv.Name == "" || srv.APIHost == "" {
		badRequest(c, ErrCodeParamMissing, "name and api_host are required")
		return
	}
	if srv.Capacity == 0 {
		srv.Capacity = 5000
	}
	srv.Status = "healthy"

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
	list, err := h.store.ListDomainsByServer(id)
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list server domains")
		return
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
	name := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(req.Name, ".")))
	if name == "" || strings.ContainsAny(name, "/\\@ ") || !strings.Contains(name, ".") {
		badRequest(c, ErrCodeParamInvalid, "invalid domain name")
		return
	}
	aRecordHost := strings.TrimSpace(req.ARecordHost)
	if aRecordHost == "" {
		aRecordHost = strings.TrimSpace(req.MXHost)
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

	setup, err := h.callNodeAddDomain(srv.APIHost, name)
	if err != nil {
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
	setup.DNSRecords = dnsRecordsForMXHost(setup.DNSRecords, name, mxHost, srv.APIHost)
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
	if err := h.callNodeRemoveDomain(srv.APIHost, domain.Name); err != nil {
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

	var update model.MailServer
	if err := c.ShouldBindJSON(&update); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid update data")
		return
	}

	// 只更新允许的字段
	if update.Name != "" {
		existing.Name = update.Name
	}
	if update.APIHost != "" {
		existing.APIHost = update.APIHost
	}
	if update.SMTPHost != "" {
		existing.SMTPHost = update.SMTPHost
	}
	if update.IMAPHost != "" {
		existing.IMAPHost = update.IMAPHost
	}
	if update.Capacity > 0 {
		existing.Capacity = update.Capacity
	}
	if update.Status != "" {
		existing.Status = update.Status
	}

	if err := h.store.UpdateServer(existing); err != nil {
		serverError(c, ErrCodeInternal, "failed to update server")
		return
	}

	success(c, "updated", existing)
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

// Heartbeat 服务器心跳上报
// POST /api/v1/internal/servers/heartbeat
func (h *ServerHandler) Heartbeat(c *gin.Context) {
	var req struct {
		ServerID  uint64 `json:"server_id"`
		Status    string `json:"status"`
		Load      int    `json:"load"`
		DiskUsage string `json:"disk_usage"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, ErrCodeParamMissing, "invalid heartbeat data")
		return
	}

	if req.Status == "" {
		req.Status = "healthy"
	}

	if err := h.store.UpdateServerHeartbeat(req.ServerID, req.Status); err != nil {
		serverError(c, ErrCodeInternal, "failed to update heartbeat")
		return
	}

	// 如果上报了负载，更新负载
	if req.Load > 0 {
		srv, err := h.store.GetServer(req.ServerID)
		if err == nil {
			srv.CurrentLoad = req.Load
			h.store.UpdateServer(srv)
		}
	}

	success(c, "heartbeat received", nil)
}

// RegisterAdminRoutes registers all server admin API routes on the given (already auth-protected) group.
func (h *ServerHandler) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.POST("/servers", h.RegisterServer)
	r.GET("/servers", h.ListServers)
	r.GET("/servers/:id", h.GetServer)
	r.PUT("/servers/:id", h.UpdateServer)
	r.DELETE("/servers/:id", h.DeleteServer)
	r.GET("/servers/:id/domains", h.ListServerDomains)
	r.POST("/servers/:id/domains", h.AddServerDomain)
	r.DELETE("/servers/:id/domains/:domain_id", h.RemoveServerDomain)
}

func dnsRecordsForMXHost(records []DNSRecord, domain, mxHost, apiHost string) []DNSRecord {
	ip := hostIP(apiHost)
	out := make([]DNSRecord, 0, len(records)+1)
	if ip != "" {
		out = append(out, DNSRecord{Type: "A", Host: mxHost, Value: ip})
	}
	for _, r := range records {
		if strings.EqualFold(r.Type, "A") {
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

func hostIP(addr string) string {
	host := strings.TrimSpace(addr)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return ""
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

func (h *ServerHandler) callNodeAddDomain(apiHost, domain string) (*RemoteDomainSetup, error) {
	body, _ := json.Marshal(map[string]string{"domain": domain})
	req, err := http.NewRequest(http.MethodPost, "http://"+apiHost+"/internal/domains", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", h.sharedSecret)
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream error: %d - %s", resp.StatusCode, string(data))
	}
	var nr nodeResponse
	if err := json.Unmarshal(data, &nr); err != nil {
		return nil, err
	}
	if nr.Code != 0 {
		return nil, fmt.Errorf(nr.Message)
	}
	return &nr.Data, nil
}

func (h *ServerHandler) callNodeRemoveDomain(apiHost, domain string) error {
	req, err := http.NewRequest(http.MethodDelete, "http://"+apiHost+"/internal/domains/"+domain, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Internal-Token", h.sharedSecret)
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("upstream error: %d - %s", resp.StatusCode, string(data))
	}
	return nil
}

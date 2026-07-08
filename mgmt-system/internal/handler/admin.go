package handler

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/store"
)

type AdminHandler struct {
	store *store.Store
}

func NewAdminHandler(s *store.Store) *AdminHandler {
	return &AdminHandler{store: s}
}

func (h *AdminHandler) Dashboard(c *gin.Context) {
	servers, _ := h.store.ListServers()
	activeCount := int64(0)
	for _, s := range servers {
		if s.Status == "healthy" {
			activeCount++
		}
	}

	mailboxes, total, _ := h.store.ListMailboxes(1, 1, "active", "")
	todayCreated, _ := h.store.CountMailboxesCreatedToday()

	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"title":           "邮箱管理系统",
		"serverCount":     len(servers),
		"healthyCount":    activeCount,
		"activeMailboxes": total,
		"todayCreated":    todayCreated,
		"servers":         servers,
		"mailboxes":       mailboxes,
	})
}

func (h *AdminHandler) ServersPage(c *gin.Context) {
	servers, _ := h.store.ListServers()
	serversJSON, _ := json.Marshal(servers)

	c.HTML(http.StatusOK, "servers.html", gin.H{
		"title":       "服务器管理",
		"servers":     servers,
		"serversJSON": template.JS(string(serversJSON)),
	})
}

func (h *AdminHandler) FiltersPage(c *gin.Context) {
	rules, _ := h.store.ListAllRules()
	rulesJSON, _ := json.Marshal(rules)

	c.HTML(http.StatusOK, "filters.html", gin.H{
		"title":     "过滤规则管理",
		"rules":     rules,
		"rulesJSON": template.JS(string(rulesJSON)),
	})
}

func (h *AdminHandler) MailboxesPage(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	view := c.DefaultQuery("view", "normal")
	if view != "trash" {
		view = "normal"
	}
	status := c.Query("status")
	search := c.Query("search")
	domainID := parseUint64(c.Query("domain_id"))
	serverID := parseUint64(c.Query("server_id"))

	filter := store.MailboxListFilter{
		Search:   search,
		DomainID: domainID,
		ServerID: serverID,
		Status:   status,
	}
	if view == "trash" {
		// 回收站：只看 soft_deleted(可恢复) + purged(已彻底清除)；忽略单 status 参数避免与 IN 冲突
		filter.Status = ""
		filter.Statuses = []string{"soft_deleted", "purged"}
	} else {
		// 账号集合：默认排除回收站态，使回收站邮箱不与正常邮箱混排
		filter.ExcludeStatuses = []string{"soft_deleted", "purged"}
	}

	list, total, _ := h.store.ListMailboxesWithFilter(page, 20, filter)
	domains, _ := h.store.ListDomains()
	servers, _ := h.store.ListServers()

	c.HTML(http.StatusOK, "mailboxes.html", gin.H{
		"title":      "邮箱管理",
		"items":      list,
		"totalCount": total,
		"domains":    domains,
		"servers":    servers,
		"page":       page,
		"status":     status,
		"search":     search,
		"domainID":   domainID,
		"serverID":   serverID,
		"view":       view,
	})
}

func (h *AdminHandler) EmailsPage(c *gin.Context) {
	mailbox := c.Query("mailbox")
	c.HTML(http.StatusOK, "emails.html", gin.H{
		"title":   "邮件查询",
		"mailbox": mailbox,
	})
}

// ServerDomainsPage 某服务器的「域名池」页面（宝塔式：服务器 → 域名 → 邮箱）。
// T4A 为只读列表（展示绑定与远端同步状态）；添加域名 / 域名下创建邮箱见 T4B/T5。
func (h *AdminHandler) ServerDomainsPage(c *gin.Context) {
	id := parseUint64(c.Param("id"))
	server, err := h.store.GetServer(id)
	if err != nil {
		c.Redirect(http.StatusFound, "/admin/servers")
		return
	}
	bindings, _ := h.store.ListDomainsByServer(id)
	mailboxCounts := map[uint64]int64{}
	for _, b := range bindings {
		cnt, _ := h.store.CountMailboxesOnServerDomain(id, b.DomainID)
		mailboxCounts[b.DomainID] = cnt
	}

	c.HTML(http.StatusOK, "server_domains.html", gin.H{
		"title":         "域名池管理",
		"server":        server,
		"bindings":      bindings,
		"mailboxCounts": mailboxCounts,
	})
}

// ServeSPA serves the React SPA index.html for all admin page routes.
// Client-side React Router handles the actual page routing.
func (h *AdminHandler) ServeSPA(c *gin.Context) {
	c.File("template/static/admin-app/index.html")
}

// DashboardAPI returns aggregated dashboard stats as JSON.
// GET /api/v1/admin/dashboard
func (h *AdminHandler) DashboardAPI(c *gin.Context) {
	servers, _ := h.store.ListServers()
	healthyCount := 0
	for _, s := range servers {
		if s.Status == "healthy" {
			healthyCount++
		}
	}
	_, totalMailboxes, _ := h.store.ListMailboxes(1, 1, "active", "")
	todayCreated, _ := h.store.CountMailboxesCreatedToday()

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"server_count":     len(servers),
			"healthy_count":    healthyCount,
			"active_mailboxes": totalMailboxes,
			"today_created":    todayCreated,
		},
	})
}

// ListDomainsAPI returns all domains (for filter dropdowns).
// GET /api/v1/admin/domains
func (h *AdminHandler) ListDomainsAPI(c *gin.Context) {
	domains, err := h.store.ListDomains()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": "failed to list domains"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": domains})
}

// RegisterProtectedRoutes registers admin page routes on the given (already auth-protected) group.
// Each page route serves the React SPA index.html; React Router handles client-side routing.
func (h *AdminHandler) RegisterProtectedRoutes(rg *gin.RouterGroup) {
	// SPA entry — all admin page paths serve the same index.html
	spa := h.ServeSPA
	rg.GET("/", spa)
	rg.GET("/servers", spa)
	rg.GET("/servers/:id/domains", spa)
	rg.GET("/filters", spa)
	rg.GET("/mailboxes", spa)
	rg.GET("/emails", spa)
	rg.GET("/search", spa)
	rg.GET("/config", spa)
	rg.GET("/config/*path", spa)
}

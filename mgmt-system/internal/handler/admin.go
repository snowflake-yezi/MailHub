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

	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"title":           "邮箱管理系统",
		"serverCount":     len(servers),
		"healthyCount":    activeCount,
		"activeMailboxes": total,
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
	status := c.Query("status")
	search := c.Query("search")
	domainID := parseUint64(c.Query("domain_id"))
	serverID := parseUint64(c.Query("server_id"))

	list, total, _ := h.store.ListMailboxesWithFilter(page, 20, store.MailboxListFilter{
		Status:   status,
		Search:   search,
		DomainID: domainID,
		ServerID: serverID,
	})
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

// RegisterProtectedRoutes registers admin page routes on the given (already auth-protected) group.
func (h *AdminHandler) RegisterProtectedRoutes(rg *gin.RouterGroup) {
	rg.GET("/", h.Dashboard)
	rg.GET("/servers", h.ServersPage)
	rg.GET("/servers/:id/domains", h.ServerDomainsPage)
	rg.GET("/filters", h.FiltersPage)
	rg.GET("/mailboxes", h.MailboxesPage)
}

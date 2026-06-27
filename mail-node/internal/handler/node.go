package handler

import (
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mail-node/internal/domain"
	"github.com/ticket/email-mail-node/internal/filter"
	"github.com/ticket/email-mail-node/internal/forward"
	"github.com/ticket/email-mail-node/internal/mailbox"
)

type NodeHandler struct {
	mailboxMgr *mailbox.Manager
	domainMgr  *domain.Manager
	engine     *filter.Engine
	lifecycle  *forward.Lifecycle
	nodeID     uint64
	nodeName   string
}

func NewNodeHandler(mgr *mailbox.Manager, domainMgr *domain.Manager, eng *filter.Engine, lc *forward.Lifecycle, nodeID uint64, nodeName string) *NodeHandler {
	return &NodeHandler{
		mailboxMgr: mgr,
		domainMgr:  domainMgr,
		engine:     eng,
		lifecycle:  lc,
		nodeID:     nodeID,
		nodeName:   nodeName,
	}
}

// ===== 邮箱管理（管理系统调用） =====

// CreateMailbox 创建邮箱
// POST /internal/mailboxes
func (h *NodeHandler) CreateMailbox(c *gin.Context) {
	var req struct {
		EmailAddress string `json:"email_address" binding:"required"`
		Password     string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"code": 1001, "message": "email_address required"})
		return
	}
	if req.Password == "" {
		req.Password = generatePassword()
	}

	info, err := h.mailboxMgr.Create(req.EmailAddress, req.Password)
	if err != nil {
		c.JSON(500, gin.H{"code": 5000, "message": "failed to create mailbox: " + err.Error()})
		return
	}

	c.JSON(201, gin.H{"code": 0, "message": "created", "data": info})
}

// DeleteMailbox 安全删除邮箱（软删除协议）
// DELETE /internal/mailboxes/:email
//
// 协议：摘除 Postfix/Dovecot → 等待转发排空 → os.Rename 到 .trash/。
// 详见 forwarding-design.md §9.1。
func (h *NodeHandler) DeleteMailbox(c *gin.Context) {
	email := c.Param("email")

	trashPath, err := h.lifecycle.MoveToTrash(email)
	if err != nil {
		c.JSON(500, gin.H{"code": 5000, "message": "failed to delete: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"code":    0,
		"message": "moved to trash",
		"data":    gin.H{"trash_path": trashPath},
	})
}

// UpdateMailboxPassword updates a mailbox password in Dovecot users.conf.
// PUT /internal/mailboxes/:email/password
func (h *NodeHandler) UpdateMailboxPassword(c *gin.Context) {
	email := c.Param("email")

	var req struct {
		EmailAddress string `json:"email_address"`
		Password     string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"code": 1001, "message": "password required"})
		return
	}
	if req.EmailAddress != "" {
		email = req.EmailAddress
	}
	if email == "" {
		c.JSON(400, gin.H{"code": 1001, "message": "email address required"})
		return
	}
	if len(req.Password) < 6 {
		c.JSON(400, gin.H{"code": 1002, "message": "password must be at least 6 characters"})
		return
	}

	if err := h.mailboxMgr.UpdatePassword(email, req.Password); err != nil {
		c.JSON(500, gin.H{"code": 5000, "message": "failed to update password: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{"code": 0, "message": "password updated"})
}

// ===== 域名管理（管理系统调用） =====

// AddDomain 让本 mail-node 开始服务一个虚拟邮箱域。
// POST /internal/domains
func (h *NodeHandler) AddDomain(c *gin.Context) {
	var req struct {
		Domain string `json:"domain" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"code": 1001, "message": "domain required"})
		return
	}

	setup, err := h.domainMgr.AddDomain(req.Domain)
	if err != nil {
		c.JSON(500, gin.H{"code": 5000, "message": "failed to add domain: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"code": 0, "message": "domain added", "data": setup})
}

// ListDomains 列出本节点 Postfix 当前服务的虚拟域。
// GET /internal/domains
func (h *NodeHandler) ListDomains(c *gin.Context) {
	domains, err := h.domainMgr.ListDomains()
	if err != nil {
		c.JSON(500, gin.H{"code": 5000, "message": "failed to list domains: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": gin.H{"domains": domains}})
}

// RemoveDomain 从本节点移除虚拟域；有邮箱账号时拒绝。
// DELETE /internal/domains/:domain
func (h *NodeHandler) RemoveDomain(c *gin.Context) {
	if err := h.domainMgr.RemoveDomain(c.Param("domain")); err != nil {
		c.JSON(500, gin.H{"code": 5000, "message": "failed to remove domain: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"code": 0, "message": "domain removed"})
}

// ===== 邮件查询（管理系统代理） =====

// scanMailboxFiles 扫描邮箱 Maildir 的 new/ 和 cur/，返回全部邮件文件路径。
// Maildir 规范：新邮件落 new/，已读移到 cur/；只扫 cur/ 会漏掉所有新到达邮件。
func (h *NodeHandler) scanMailboxFiles(email string) []string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return nil
	}
	mailboxDir := filepath.Join(h.mailboxMgr.MaildirBase(), parts[1], parts[0])
	var files []string
	for _, sub := range []string{"new", "cur"} {
		entries, err := os.ReadDir(filepath.Join(mailboxDir, sub))
		if err != nil {
			continue // 目录不存在视为空
		}
		for _, e := range entries {
			if !e.IsDir() {
				files = append(files, filepath.Join(mailboxDir, sub, e.Name()))
			}
		}
	}
	return files
}

func sortMailFilesByModTimeDesc(files []string) []string {
	sorted := append([]string(nil), files...)
	sort.Slice(sorted, func(i, j int) bool {
		iStat, iErr := os.Stat(sorted[i])
		jStat, jErr := os.Stat(sorted[j])
		if iErr != nil || jErr != nil {
			return sorted[i] > sorted[j]
		}
		if !iStat.ModTime().Equal(jStat.ModTime()) {
			return iStat.ModTime().After(jStat.ModTime())
		}
		return sorted[i] > sorted[j]
	})
	return sorted
}

func parsePageSize(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return page, size
}

func splitPage(files []string, page, size int) []string {
	if len(files) == 0 {
		return nil
	}
	start := (page - 1) * size
	if start >= len(files) {
		return nil
	}
	end := start + size
	if end > len(files) {
		end = len(files)
	}
	return files[start:end]
}

// GetMessages 获取邮箱的邮件列表
// GET /internal/mailboxes/:email/messages
func (h *NodeHandler) GetMessages(c *gin.Context) {
	email := c.Param("email")
	if parts := strings.SplitN(email, "@", 2); len(parts) != 2 {
		c.JSON(400, gin.H{"code": 1002, "message": "invalid email"})
		return
	}

	page, size := parsePageSize(c)
	allFiles := sortMailFilesByModTimeDesc(h.scanMailboxFiles(email))
	pageFiles := splitPage(allFiles, page, size)
	messages := make([]*parsedMessage, 0, len(pageFiles))
	for _, filePath := range pageFiles {
		if msg, err := parseMaildirMessage(filePath, email, h.mailboxMgr.MaildirBase()); err == nil {
			messages = append(messages, msg)
		}
	}

	c.JSON(200, gin.H{
		"code": 0,
		"data": gin.H{
			"email_address": email,
			"page":          page,
			"size":          size,
			"total":         len(allFiles),
			"messages":      messages,
		},
	})
}

// GetMessageBody 获取单封邮件完整内容
// GET /internal/messages/:message_id?mailbox=xxx@domain
func (h *NodeHandler) GetMessageBody(c *gin.Context) {
	messageID, err := url.PathUnescape(c.Param("message_id"))
	if err != nil {
		messageID = c.Param("message_id")
	}
	email := c.Query("mailbox")

	if parts := strings.SplitN(email, "@", 2); len(parts) != 2 {
		c.JSON(400, gin.H{"code": 1002, "message": "invalid mailbox param"})
		return
	}

	// 在 new/ + cur/ 中找匹配 message_id 的邮件
	for _, filePath := range sortMailFilesByModTimeDesc(h.scanMailboxFiles(email)) {
		msg, err := parseFullMessage(filePath, email, h.mailboxMgr.MaildirBase())
		if err != nil {
			continue
		}
		if msg.MessageID == messageID {
			c.JSON(200, gin.H{"code": 0, "data": msg})
			return
		}
	}

	c.JSON(404, gin.H{"code": 2003, "message": "message not found"})
}

// ===== 健康检查 =====

// Health 健康检查
// GET /internal/health
func (h *NodeHandler) Health(c *gin.Context) {
	// 统计所有邮箱 new/ + cur/ 下的邮件总数
	totalMessages := countAllMessages(h.mailboxMgr.MaildirBase())

	c.JSON(200, gin.H{
		"code": 0,
		"data": gin.H{
			"status":         "ok",
			"node_id":        h.nodeID,
			"node_name":      h.nodeName,
			"total_messages": totalMessages,
			"uptime":         time.Now().Unix(),
		},
	})
}

// ReloadFilters 立即重载过滤规则
// POST /internal/filters/reload
func (h *NodeHandler) ReloadFilters(c *gin.Context) {
	// 由管理系统 URL 从配置中传入，这里简单返回
	c.JSON(200, gin.H{"code": 0, "message": "use GET /internal/filters to fetch latest rules"})
}

// countAllMessages 统计 base 下所有邮箱 new/ + cur/ 的邮件总数
func countAllMessages(base string) int {
	var n int
	domains, err := os.ReadDir(base)
	if err != nil {
		return 0
	}
	for _, d := range domains {
		if !d.IsDir() {
			continue
		}
		mailboxes, err := os.ReadDir(filepath.Join(base, d.Name()))
		if err != nil {
			continue
		}
		for _, mb := range mailboxes {
			if !mb.IsDir() {
				continue
			}
			for _, sub := range []string{"new", "cur"} {
				files, err := os.ReadDir(filepath.Join(base, d.Name(), mb.Name(), sub))
				if err != nil {
					continue
				}
				for _, f := range files {
					if !f.IsDir() {
						n++
					}
				}
			}
		}
	}
	return n
}

func extractHeader(content, header string) string {
	lines := strings.Split(content, "\n")
	prefix := strings.ToLower(header) + ":"
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			value := strings.TrimSpace(trimmed[len(prefix):])
			decoded, err := new(mime.WordDecoder).DecodeHeader(value)
			if err == nil {
				return decoded
			}
			return value
		}
	}
	return ""
}

func generatePassword() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())[:16]
}

// RegisterInternalRoutes registers all /internal/* routes on the given router group.
// The caller is responsible for applying auth middleware to the group.
// /smtp/filter (deprecated) is registered separately on the engine.
func (h *NodeHandler) RegisterInternalRoutes(rg *gin.RouterGroup) {
	// 邮箱管理
	rg.POST("/mailboxes", h.CreateMailbox)
	rg.DELETE("/mailboxes/:email", h.DeleteMailbox)
	rg.PUT("/mailboxes/:email/password", h.UpdateMailboxPassword)

	// 域名管理
	rg.POST("/domains", h.AddDomain)
	rg.GET("/domains", h.ListDomains)
	rg.DELETE("/domains/:domain", h.RemoveDomain)

	// 邮件查询
	rg.GET("/mailboxes/:email/messages", h.GetMessages)
	rg.GET("/messages/:message_id", h.GetMessageBody)

	// 健康 & 维护
	rg.GET("/health", h.Health)
	rg.POST("/filters/reload", h.ReloadFilters)
}

// SMTPFilter is DEPRECATED.
// Postfix before-queue content_filter entry point (方案 A).
// The current architecture uses方案 B (Maildir async scan → forward.Service).
// This endpoint is kept for backward compatibility only — do NOT hook into Postfix.
//
// Deprecated: Use forward.Service (Maildir polling) instead.
func (h *NodeHandler) SMTPFilter(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(400, gin.H{"code": 1002, "message": "cannot read email"})
		return
	}

	content := string(body)
	msg := &filter.EmailMessage{
		From:    extractHeader(content, "from"),
		To:      extractHeader(content, "to"),
		Subject: extractHeader(content, "subject"),
		Body:    content,
	}

	result := h.engine.Filter(msg)

	switch result.Action {
	case filter.ActionBlock:
		// 返回 rejection，Postfix 会退信
		c.JSON(550, gin.H{"action": "reject", "message": "blocked by filter: " + result.Reason})
	case filter.ActionFlag:
		// 修改 subject 添加前缀，放行
		c.JSON(200, gin.H{
			"action":      "modify",
			"reason":      result.Reason,
			"new_subject": h.engine.GetFlagPrefix() + " " + msg.Subject,
		})
	default:
		// pass
		c.JSON(200, gin.H{"action": "pass", "reason": result.Reason})
	}
}

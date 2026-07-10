package handler

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jhillyerd/enmime"
	"github.com/ticket/email-mail-node/internal/config"
	"github.com/ticket/email-mail-node/internal/domain"
	"github.com/ticket/email-mail-node/internal/filter"
	"github.com/ticket/email-mail-node/internal/forward"
	"github.com/ticket/email-mail-node/internal/mailbox"
)

const maxAttachmentPreviewBytes = 10 * 1024 * 1024

type NodeHandler struct {
	mailboxMgr   *mailbox.Manager
	domainMgr    *domain.Manager
	engine       *filter.Engine
	lifecycle    *forward.Lifecycle
	nodeID       uint64
	nodeName     string
	managerURL   string
	sharedSecret string
	remoteCfg    *config.RemoteConfig
}

func NewNodeHandler(mgr *mailbox.Manager, domainMgr *domain.Manager, eng *filter.Engine, lc *forward.Lifecycle, nodeID uint64, nodeName, managerURL, sharedSecret string, remoteCfg *config.RemoteConfig) *NodeHandler {
	return &NodeHandler{
		mailboxMgr:   mgr,
		domainMgr:    domainMgr,
		engine:       eng,
		lifecycle:    lc,
		nodeID:       nodeID,
		nodeName:     nodeName,
		managerURL:   managerURL,
		sharedSecret: sharedSecret,
		remoteCfg:    remoteCfg,
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

	msg := "moved to trash"
	if trashPath == "" {
		msg = "already deleted (maildir absent)"
	}

	c.JSON(200, gin.H{
		"code":    0,
		"message": msg,
		"data":    gin.H{"trash_path": trashPath},
	})
}

// RestoreMailbox 从 .trash 恢复邮箱（MoveToTrash 的逆操作，restore 协议）
// POST /internal/mailboxes/:email/restore
//
// body: {"password": "..."}（mgmt 下发原密码，用于重建 Dovecot 行）
// 409: .trash 无可恢复目录（已被 GC 物理清除/从未删除）或目标路径已存在。
func (h *NodeHandler) RestoreMailbox(c *gin.Context) {
	email := c.Param("email")

	var req struct {
		Password string `json:"password"`
	}
	_ = c.ShouldBindJSON(&req) // body 可选；password 由 mgmt 下发

	maildirPath, err := h.lifecycle.RestoreFromTrash(email, req.Password)
	if err != nil {
		if errors.Is(err, forward.ErrNotInTrash) {
			c.JSON(409, gin.H{"code": 2301, "message": "not in trash or already purged"})
			return
		}
		if strings.Contains(err.Error(), "already exists") {
			c.JSON(409, gin.H{"code": 2302, "message": err.Error()})
			return
		}
		c.JSON(500, gin.H{"code": 5000, "message": "failed to restore: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"code":    0,
		"message": "restored",
		"data":    gin.H{"maildir_path": maildirPath},
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

	msg, _, ok := h.findMessage(email, messageID)
	if !ok {
		c.JSON(404, gin.H{"code": 2003, "message": "message not found"})
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": msg})
}

// normalizeMessageID 去掉 message-id 首尾的 < > 和引号，trim 空白，用于兼容匹配。
func normalizeMessageID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "<\"")
	s = strings.TrimRight(s, ">\"")
	return strings.TrimSpace(s)
}

// findMessage 在邮箱 Maildir 的 new/+cur/ 中按 message_id 定位邮件。
// 返回解析结果 msg（GetMessageBody 直接使用）与文件路径 filePath（GetMessageAttachment 重新打开取附件字节）。
// 匹配语义与原内联实现一致：精确 / 规范化（去 <> 与引号）/ fallback-id 忽略大小写。
func (h *NodeHandler) findMessage(email, messageID string) (msg *parsedMessage, filePath string, ok bool) {
	normalized := normalizeMessageID(messageID)
	for _, fp := range sortMailFilesByModTimeDesc(h.scanMailboxFiles(email)) {
		m, err := parseFullMessage(fp, email, h.mailboxMgr.MaildirBase())
		if err != nil {
			continue
		}
		if matchMessageID(m.MessageID, messageID, normalized) {
			return m, fp, true
		}
	}
	return nil, "", false
}

// matchMessageID 三级兼容匹配：精确 → 规范化（去 <> 与引号）→ fallback-id 忽略大小写。
func matchMessageID(candidate, target, normalizedTarget string) bool {
	if candidate == target {
		return true
	}
	if normalizeMessageID(candidate) == normalizedTarget {
		return true
	}
	if strings.HasPrefix(candidate, "fallback-") && strings.HasPrefix(target, "fallback-") {
		if strings.EqualFold(candidate, target) || strings.EqualFold(normalizeMessageID(candidate), normalizedTarget) {
			return true
		}
	}
	return false
}

// GetMessageAttachment 下载单封邮件的指定附件（按 index），返回原始字节流。
// GET /internal/messages/:message_id/attachments/:index?mailbox=xxx@domain
//
// 例外于统一 JSON 信封——直接返回二进制：Content-Type 取附件真实类型，
// 普通附件 Content-Disposition: attachment，inline part 返回 inline（RFC 5987 兼容中文文件名）。错误路径仍返回 JSON 信封。
// index 与 GetMessages/GetMessageBody 返回的 attachment.index 对齐（先 attachment 后 inline）。
func (h *NodeHandler) GetMessageAttachment(c *gin.Context) {
	part, info, inline, err := h.messageAttachmentPart(c)
	if err != nil {
		return
	}
	dispositionType := "attachment"
	if inline {
		dispositionType = "inline"
	}
	c.Header("Content-Disposition", contentDisposition(dispositionType, info.Filename))
	c.Data(http.StatusOK, info.ContentType, part.Content)
}

// GetMessageAttachmentPreview 预览单封邮件的指定附件（按 index）。
// GET /internal/messages/:message_id/attachments/:index/preview?mailbox=xxx@domain
//
// 只允许浏览器可安全内嵌的图片、PDF 和文本类内容；其余类型继续走下载端点。
func (h *NodeHandler) GetMessageAttachmentPreview(c *gin.Context) {
	part, info, _, err := h.messageAttachmentPart(c)
	if err != nil {
		return
	}
	if len(part.Content) > maxAttachmentPreviewBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"code": 1003, "message": "attachment too large for preview"})
		return
	}
	previewContentType, ok := previewAttachmentContentType(info.ContentType)
	if !ok {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"code": 1004, "message": "attachment type is not previewable"})
		return
	}

	c.Header("Content-Disposition", contentDisposition("inline", info.Filename))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, previewContentType, part.Content)
}

func (h *NodeHandler) messageAttachmentPart(c *gin.Context) (*enmime.Part, inferredPartInfo, bool, error) {
	messageID, err := url.PathUnescape(c.Param("message_id"))
	if err != nil {
		messageID = c.Param("message_id")
	}
	email := c.Query("mailbox")
	if parts := strings.SplitN(email, "@", 2); len(parts) != 2 {
		c.JSON(400, gin.H{"code": 1002, "message": "invalid mailbox param"})
		return nil, inferredPartInfo{}, false, fmt.Errorf("invalid mailbox param")
	}

	index, err := strconv.Atoi(c.Param("index"))
	if err != nil || index < 0 {
		c.JSON(400, gin.H{"code": 1002, "message": "invalid attachment index"})
		return nil, inferredPartInfo{}, false, fmt.Errorf("invalid attachment index")
	}

	_, filePath, ok := h.findMessage(email, messageID)
	if !ok {
		c.JSON(404, gin.H{"code": 2003, "message": "message not found"})
		return nil, inferredPartInfo{}, false, fmt.Errorf("message not found")
	}

	file, err := os.Open(filePath)
	if err != nil {
		c.JSON(500, gin.H{"code": 5000, "message": "failed to open message file"})
		return nil, inferredPartInfo{}, false, err
	}
	defer file.Close()

	envelope, err := enmime.ReadEnvelope(file)
	if err != nil {
		c.JSON(500, gin.H{"code": 5000, "message": "failed to parse message"})
		return nil, inferredPartInfo{}, false, err
	}

	parts := collectAttachmentParts(envelope)
	if index >= len(parts) {
		c.JSON(404, gin.H{"code": 2003, "message": "attachment index out of range"})
		return nil, inferredPartInfo{}, false, fmt.Errorf("attachment index out of range")
	}

	part := parts[index]
	inlineContentIDs := htmlCIDReferences(envelope.HTML)
	inline := index >= len(envelope.Attachments) || isInlinePart(part, inlineContentIDs)
	return part, inferPartInfo(part, index, inline), inline, nil
}

func previewAttachmentContentType(contentType string) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		mediaType = strings.TrimSpace(contentType)
	}
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "image/") {
		if mediaType == "image/svg+xml" {
			return "", false
		}
		return mediaType, true
	}
	if strings.HasPrefix(mediaType, "text/") {
		return "text/plain; charset=utf-8", true
	}
	switch mediaType {
	case "application/pdf":
		return mediaType, true
	case "application/json", "application/xml", "application/xhtml+xml", "application/javascript":
		return "text/plain; charset=utf-8", true
	default:
		if strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml") {
			return "text/plain; charset=utf-8", true
		}
		return "", false
	}
}

// contentDisposition 生成 RFC 5987 兼容的 Content-Disposition 头，
// 同时给出 ASCII 回退（filename=）与 UTF-8 百分号编码（filename*=），兼容中文文件名。
func contentDisposition(dispositionType, filename string) string {
	dispositionType = strings.TrimSpace(strings.ToLower(dispositionType))
	if dispositionType != "inline" {
		dispositionType = "attachment"
	}
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, dispositionType, asciiFilenameFallback(filename), url.PathEscape(filename))
}

// asciiFilenameFallback 将非 ASCII 字符替换为下划线，并剔除会破坏响应头语法的引号/反斜杠，
// 用作 Content-Disposition 中 filename= 的安全回退。
func asciiFilenameFallback(filename string) string {
	var b strings.Builder
	for _, r := range filename {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('_')
		case r < 0x20 || r > 0x7E:
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
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

// Stats 节点运维统计:当前邮箱账号数、邮件总数、Maildir 所在分区的磁盘使用量。
// GET /internal/stats
func (h *NodeHandler) Stats(c *gin.Context) {
	mailboxCount := 0
	if h.mailboxMgr != nil {
		mailboxCount = h.mailboxMgr.ActiveCount()
	}
	totalMessages := countAllMessages(h.mailboxMgr.MaildirBase())
	c.JSON(200, gin.H{
		"code": 0,
		"data": gin.H{
			"node_id":        h.nodeID,
			"node_name":      h.nodeName,
			"mailbox_count":  mailboxCount,
			"total_messages": totalMessages,
			"disk":           diskUsage(h.mailboxMgr.MaildirBase()),
		},
	})
}

// ReloadFilters 立即重载过滤规则
// POST /internal/filters/reload
func (h *NodeHandler) ReloadFilters(c *gin.Context) {
	if h.managerURL == "" {
		c.JSON(400, gin.H{"code": 5000, "message": "manager URL not configured"})
		return
	}
	if err := h.engine.SyncFromManager(h.managerURL, h.sharedSecret); err != nil {
		c.JSON(500, gin.H{"code": 5000, "message": "reload failed: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"code": 0, "message": "filters reloaded"})
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
	rg.POST("/mailboxes/:email/restore", h.RestoreMailbox)
	rg.PUT("/mailboxes/:email/password", h.UpdateMailboxPassword)

	// 域名管理
	rg.POST("/domains", h.AddDomain)
	rg.GET("/domains", h.ListDomains)
	rg.DELETE("/domains/:domain", h.RemoveDomain)

	// 邮件查询
	rg.GET("/mailboxes/:email/messages", h.GetMessages)
	rg.GET("/messages/:message_id", h.GetMessageBody)
	rg.GET("/messages/:message_id/attachments/:index", h.GetMessageAttachment)
	rg.GET("/messages/:message_id/attachments/:index/preview", h.GetMessageAttachmentPreview)

	// 健康 & 维护
	rg.GET("/health", h.Health)
	rg.GET("/stats", h.Stats)
	rg.POST("/filters/reload", h.ReloadFilters)
	rg.POST("/configs/reload", h.ReloadConfigs)
}

// ReloadConfigs 热重载远程配置（由 mgmt-system 配置变更后调用）
// POST /internal/configs/reload
func (h *NodeHandler) ReloadConfigs(c *gin.Context) {
	if h.remoteCfg == nil {
		c.JSON(400, gin.H{"code": 5000, "message": "remote config not available"})
		return
	}
	if err := h.remoteCfg.Reload(); err != nil {
		c.JSON(500, gin.H{"code": 5000, "message": "reload configs failed: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"code": 0, "message": "configs reloaded"})
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

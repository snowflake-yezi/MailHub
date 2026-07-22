package handler

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/apiregistry"
	"github.com/ticket/email-mgmt-system/internal/model"
)

type emailStore interface {
	GetMailboxByEmail(email string) (*model.MailboxAccount, error)
	GetMailboxByOrderID(orderID string) (*model.MailboxAccount, error)
	GetServer(id uint64) (*model.MailServer, error)
	FindServerByEmailDomain(domain string) (*model.MailServer, error)
}

type EmailHandler struct {
	store        emailStore
	sharedSecret string
}

func NewEmailHandler(s emailStore, sharedSecret string) *EmailHandler {
	return &EmailHandler{store: s, sharedSecret: sharedSecret}
}

// GetMailboxMessages 按邮箱查询邮件列表（T8 主线，大模型系统调用）
// GET /api/v1/mailboxes/:email/messages
func (h *EmailHandler) GetMailboxMessages(c *gin.Context) {
	emailAddr := mailboxParam(c)
	if emailAddr == "" {
		badRequest(c, ErrCodeParamMissing, "mailbox is required")
		return
	}

	mb, err := h.store.GetMailboxByEmail(emailAddr)
	if err != nil {
		notFound(c, "mailbox not found: "+emailAddr)
		return
	}

	h.proxyMailboxMessages(c, mb, "")
}

// GetOrderEmails 按订单查询邮件列表（兼容入口）。
// GET /api/v1/orders/:order_id/emails
func (h *EmailHandler) GetOrderEmails(c *gin.Context) {
	orderID := c.Param("order_id")

	mb, err := h.store.GetMailboxByOrderID(orderID)
	if err != nil {
		notFound(c, "mailbox not found for order: "+orderID)
		return
	}

	h.proxyMailboxMessages(c, mb, orderID)
}

func (h *EmailHandler) proxyMailboxMessages(c *gin.Context, mb *model.MailboxAccount, orderID string) {
	srv, err := h.store.GetServer(mb.ServerID)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "mail server not found")
		return
	}

	query := url.Values{}
	query.Set("page", c.DefaultQuery("page", "1"))
	query.Set("size", c.DefaultQuery("size", "20"))
	path := "/internal/mailboxes/" + url.PathEscape(mb.EmailAddress) + "/messages?" + query.Encode()

	data, err := proxyToServer(srv.APIHost, "GET", path, nil, h.sharedSecret)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "failed to fetch emails: "+err.Error())
		return
	}

	writeMailboxMessagesResponse(c, data, mb.EmailAddress, orderID)
}

func writeMailboxMessagesResponse(c *gin.Context, data []byte, emailAddr, orderID string) {
	var rawResp map[string]interface{}
	if err := json.Unmarshal(data, &rawResp); err != nil {
		serverError(c, ErrCodeExternalFail, "failed to parse email response")
		return
	}
	dataMap, ok := rawResp["data"].(map[string]interface{})
	if !ok {
		serverError(c, ErrCodeExternalFail, "invalid email response: data object is required")
		return
	}
	if orderID != "" {
		dataMap["order_id"] = orderID
	}
	dataMap["email_address"] = emailAddr
	if message, ok := rawResp["message"].(string); !ok || message == "" {
		rawResp["message"] = "success"
	}
	rawResp["request_id"] = uuidShort()

	c.JSON(http.StatusOK, rawResp)
}

// GetEmailBody 获取单封邮件完整内容（大模型系统调用）
// GET /api/v1/emails/:message_id/body
func (h *EmailHandler) GetEmailBody(c *gin.Context) {
	h.proxyEmailBody(c, c.Param("message_id"), c.Query("mailbox"))
}

// GetRawEmail streams the original EML without JSON or Base64 wrapping.
// GET /api/v1/emails/:message_id/raw?mailbox=xxx
func (h *EmailHandler) GetRawEmail(c *gin.Context) {
	h.proxyRawEmail(c, c.Param("message_id"), c.Query("mailbox"))
}

func (h *EmailHandler) proxyRawEmail(c *gin.Context, messageID, emailAddr string) {
	if emailAddr == "" {
		badRequest(c, ErrCodeParamMissing, "query parameter 'mailbox' is required")
		return
	}
	mb, err := h.store.GetMailboxByEmail(emailAddr)
	if err != nil {
		notFound(c, "mailbox not found: "+emailAddr)
		return
	}
	srv, err := h.store.GetServer(mb.ServerID)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "mail server not found")
		return
	}
	h.proxyRawEmailDirect(c, srv, messageID, mb.EmailAddress)
}

func (h *EmailHandler) proxyEmailBody(c *gin.Context, messageID, emailAddr string) {
	if emailAddr == "" {
		badRequest(c, ErrCodeParamMissing, "query parameter 'mailbox' is required")
		return
	}

	mb, err := h.store.GetMailboxByEmail(emailAddr)
	if err != nil {
		notFound(c, "mailbox not found: "+emailAddr)
		return
	}

	srv, err := h.store.GetServer(mb.ServerID)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "mail server not found")
		return
	}

	h.proxyEmailBodyDirect(c, srv, messageID, mb.EmailAddress)
}

func mailboxParam(c *gin.Context) string {
	if v := c.Param("email"); v != "" {
		return v
	}
	if v := c.Param("mailbox_ref"); v != "" {
		return v
	}
	if v := c.Param("order_id"); v != "" {
		return v
	}
	return c.Query("mailbox")
}

func (h *EmailHandler) RegisterExternalRoutes(registry *apiregistry.Registry, r *gin.RouterGroup) {
	registry.Register(r, apiregistry.Route{
		Method: http.MethodGet, Path: "/orders/:order_id/emails", PermissionCode: "email:list",
		GroupName: "邮件读取", Name: "查询邮件列表", ResourceName: "按订单查询邮件列表", Description: "按订单号或邮箱查询邮件列表", SortOrder: 110,
		Handler: h.GetOrderEmails,
	})
	registry.Register(r, apiregistry.Route{
		Method: http.MethodGet, Path: "/mailboxes/:mailbox_ref/messages", PermissionCode: "email:list",
		GroupName: "邮件读取", Name: "查询邮件列表", ResourceName: "按邮箱查询邮件列表", Description: "按订单号或邮箱查询邮件列表", SortOrder: 110,
		Handler: h.GetMailboxMessages,
	})
	registry.Register(r, apiregistry.Route{
		Method: http.MethodGet, Path: "/emails/:message_id/body", PermissionCode: "email:body",
		GroupName: "邮件读取", Name: "查看邮件正文", SortOrder: 120, Handler: h.GetEmailBody,
	})
	registry.Register(r, apiregistry.Route{
		Method: http.MethodGet, Path: "/emails/:message_id/attachments/:index", PermissionCode: "email:attachment",
		GroupName: "邮件读取", Name: "下载附件", SortOrder: 130, Handler: h.GetEmailAttachment,
	})
	registry.Register(r, apiregistry.Route{
		Method: http.MethodGet, Path: "/emails/:message_id/raw", PermissionCode: "email:raw",
		GroupName: "邮件读取", Name: "下载原始 EML", Description: "流式下载未经重建的原始邮件字节", SortOrder: 140, Handler: h.GetRawEmail,
	})
}

// AdminGetEmails 管理后台邮件查询（含域名级服务器降级查找）。
// 优先在 mailbox_accounts 中查找；未命中时按邮箱域名定位服务器，让管理员能查询任意账号的邮件。
func (h *EmailHandler) AdminGetEmails(c *gin.Context) {
	emailAddr := c.Query("mailbox")
	if emailAddr == "" {
		badRequest(c, ErrCodeParamMissing, "mailbox is required")
		return
	}

	mb, err := h.store.GetMailboxByEmail(emailAddr)
	if err != nil {
		// 降级：通过域名查找服务器
		_, domainName, ok := splitEmail(emailAddr)
		if !ok {
			notFound(c, "invalid email: "+emailAddr)
			return
		}
		srv, srvErr := h.store.FindServerByEmailDomain(domainName)
		if srvErr != nil {
			notFound(c, "mailbox not found and no server serves domain: "+emailAddr)
			return
		}
		h.proxyMailboxMessagesDirect(c, srv, emailAddr, "")
		return
	}

	h.proxyMailboxMessages(c, mb, "")
}

// AdminGetEmailBody 管理后台邮件正文查询（含域名级服务器降级查找）。
func (h *EmailHandler) AdminGetEmailBody(c *gin.Context) {
	messageID := c.Param("message_id")
	emailAddr := c.Query("mailbox")
	if emailAddr == "" {
		badRequest(c, ErrCodeParamMissing, "query parameter 'mailbox' is required")
		return
	}

	mb, err := h.store.GetMailboxByEmail(emailAddr)
	if err != nil {
		_, domainName, ok := splitEmail(emailAddr)
		if !ok {
			notFound(c, "invalid email: "+emailAddr)
			return
		}
		srv, srvErr := h.store.FindServerByEmailDomain(domainName)
		if srvErr != nil {
			notFound(c, "mailbox not found and no server serves domain: "+emailAddr)
			return
		}
		h.proxyEmailBodyDirect(c, srv, messageID, emailAddr)
		return
	}

	h.proxyEmailBody(c, messageID, mb.EmailAddress)
}

// AdminDeleteEmail permanently deletes one message from the mailbox Maildir.
func (h *EmailHandler) AdminDeleteEmail(c *gin.Context) {
	messageID := c.Param("message_id")
	emailAddr := c.Query("mailbox")
	if emailAddr == "" {
		badRequest(c, ErrCodeParamMissing, "query parameter 'mailbox' is required")
		return
	}

	mb, err := h.store.GetMailboxByEmail(emailAddr)
	if err == nil {
		srv, srvErr := h.store.GetServer(mb.ServerID)
		if srvErr != nil {
			serverError(c, ErrCodeExternalFail, "mail server not found")
			return
		}
		h.proxyDeleteEmailDirect(c, srv, messageID, mb.EmailAddress)
		return
	}
	_, domainName, ok := splitEmail(emailAddr)
	if !ok {
		notFound(c, "invalid email: "+emailAddr)
		return
	}
	srv, srvErr := h.store.FindServerByEmailDomain(domainName)
	if srvErr != nil {
		notFound(c, "mailbox not found and no server serves domain: "+emailAddr)
		return
	}
	h.proxyDeleteEmailDirect(c, srv, messageID, emailAddr)
}

func (h *EmailHandler) proxyDeleteEmailDirect(c *gin.Context, srv *model.MailServer, messageID, emailAddr string) {
	query := url.Values{}
	query.Set("mailbox", emailAddr)
	path := "/internal/messages/" + url.PathEscape(messageID) + "?" + query.Encode()
	data, err := proxyToServer(srv.APIHost, http.MethodDelete, path, nil, h.sharedSecret)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "failed to delete email: "+err.Error())
		return
	}
	var rawResp map[string]interface{}
	if err := json.Unmarshal(data, &rawResp); err != nil {
		serverError(c, ErrCodeExternalFail, "failed to parse delete response")
		return
	}
	rawResp["request_id"] = uuidShort()
	c.JSON(http.StatusOK, rawResp)
}

// proxyMailboxMessagesDirect 直接向指定服务器代理邮件列表请求（跳过 mailbox_accounts 查找）。
func (h *EmailHandler) proxyMailboxMessagesDirect(c *gin.Context, srv *model.MailServer, emailAddr, orderID string) {
	query := url.Values{}
	query.Set("page", c.DefaultQuery("page", "1"))
	query.Set("size", c.DefaultQuery("size", "20"))
	path := "/internal/mailboxes/" + url.PathEscape(emailAddr) + "/messages?" + query.Encode()

	data, err := proxyToServer(srv.APIHost, "GET", path, nil, h.sharedSecret)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "failed to fetch emails: "+err.Error())
		return
	}

	writeMailboxMessagesResponse(c, data, emailAddr, orderID)
}

// proxyEmailBodyDirect 直接向指定服务器代理邮件正文请求（跳过 mailbox_accounts 查找）。
func (h *EmailHandler) proxyEmailBodyDirect(c *gin.Context, srv *model.MailServer, messageID, emailAddr string) {
	query := url.Values{}
	query.Set("mailbox", emailAddr)
	path := "/internal/messages/" + url.PathEscape(messageID) + "?" + query.Encode()
	data, err := proxyToServer(srv.APIHost, "GET", path, nil, h.sharedSecret)
	if err != nil {
		if writeUpstreamJSONError(c, err) {
			return
		}
		serverError(c, ErrCodeExternalFail, "failed to fetch email body: "+err.Error())
		return
	}

	var rawResp map[string]interface{}
	if err := json.Unmarshal(data, &rawResp); err != nil {
		serverError(c, ErrCodeExternalFail, "failed to parse email body response")
		return
	}
	rawResp["request_id"] = uuidShort()
	c.JSON(200, rawResp)
}

// GetEmailAttachment 下载单封邮件的指定附件（对外 API，大模型系统调用）。
// GET /api/v1/emails/:message_id/attachments/:index?mailbox=xxx
// 二进制流透传，例外于统一 JSON 信封。
func (h *EmailHandler) GetEmailAttachment(c *gin.Context) {
	h.proxyEmailAttachment(c, c.Param("message_id"), c.Query("mailbox"))
}

func (h *EmailHandler) proxyEmailAttachment(c *gin.Context, messageID, emailAddr string) {
	if emailAddr == "" {
		badRequest(c, ErrCodeParamMissing, "query parameter 'mailbox' is required")
		return
	}
	mb, err := h.store.GetMailboxByEmail(emailAddr)
	if err != nil {
		notFound(c, "mailbox not found: "+emailAddr)
		return
	}
	srv, err := h.store.GetServer(mb.ServerID)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "mail server not found")
		return
	}
	h.proxyEmailAttachmentDirect(c, srv, messageID, emailAddr)
}

// proxyEmailAttachmentDirect 直接向指定服务器代理附件下载（透传字节流）。
func (h *EmailHandler) proxyEmailAttachmentDirect(c *gin.Context, srv *model.MailServer, messageID, emailAddr string) {
	index := c.Param("index")
	query := url.Values{}
	query.Set("mailbox", emailAddr)
	path := "/internal/messages/" + url.PathEscape(messageID) + "/attachments/" + url.PathEscape(index) + "?" + query.Encode()
	proxyAttachmentToServer(c, srv.APIHost, "GET", path, h.sharedSecret)
}

func (h *EmailHandler) proxyRawEmailDirect(c *gin.Context, srv *model.MailServer, messageID, emailAddr string) {
	query := url.Values{}
	query.Set("mailbox", emailAddr)
	path := "/internal/messages/" + url.PathEscape(messageID) + "/raw?" + query.Encode()
	proxyRawEmailToServer(c, srv.APIHost, http.MethodGet, path, h.sharedSecret)
}

func (h *EmailHandler) proxyEmailAttachmentPreviewDirect(c *gin.Context, srv *model.MailServer, messageID, emailAddr string) {
	index := c.Param("index")
	query := url.Values{}
	query.Set("mailbox", emailAddr)
	path := "/internal/messages/" + url.PathEscape(messageID) + "/attachments/" + url.PathEscape(index) + "/preview?" + query.Encode()
	proxyAttachmentToServer(c, srv.APIHost, "GET", path, h.sharedSecret)
}

// AdminGetEmailAttachment 管理后台附件下载（含域名级服务器降级查找，与 AdminGetEmailBody 同模式）。
// GET /api/v1/admin/emails/:message_id/attachments/:index?mailbox=xxx
func (h *EmailHandler) AdminGetEmailAttachment(c *gin.Context) {
	messageID := c.Param("message_id")
	emailAddr := c.Query("mailbox")
	if emailAddr == "" {
		badRequest(c, ErrCodeParamMissing, "query parameter 'mailbox' is required")
		return
	}
	mb, err := h.store.GetMailboxByEmail(emailAddr)
	if err != nil {
		_, domainName, ok := splitEmail(emailAddr)
		if !ok {
			notFound(c, "invalid email: "+emailAddr)
			return
		}
		srv, srvErr := h.store.FindServerByEmailDomain(domainName)
		if srvErr != nil {
			notFound(c, "mailbox not found and no server serves domain: "+emailAddr)
			return
		}
		h.proxyEmailAttachmentDirect(c, srv, messageID, emailAddr)
		return
	}
	h.proxyEmailAttachment(c, messageID, mb.EmailAddress)
}

// AdminGetEmailAttachmentPreview 管理后台附件预览（图片/PDF/文本；下载端点保持不变）。
// GET /api/v1/admin/emails/:message_id/attachments/:index/preview?mailbox=xxx
func (h *EmailHandler) AdminGetEmailAttachmentPreview(c *gin.Context) {
	messageID := c.Param("message_id")
	emailAddr := c.Query("mailbox")
	if emailAddr == "" {
		badRequest(c, ErrCodeParamMissing, "query parameter 'mailbox' is required")
		return
	}
	mb, err := h.store.GetMailboxByEmail(emailAddr)
	if err != nil {
		_, domainName, ok := splitEmail(emailAddr)
		if !ok {
			notFound(c, "invalid email: "+emailAddr)
			return
		}
		srv, srvErr := h.store.FindServerByEmailDomain(domainName)
		if srvErr != nil {
			notFound(c, "mailbox not found and no server serves domain: "+emailAddr)
			return
		}
		h.proxyEmailAttachmentPreviewDirect(c, srv, messageID, emailAddr)
		return
	}
	h.proxyEmailAttachmentPreviewDirect(c, &mb.Server, messageID, mb.EmailAddress)
}

// splitEmail 将邮箱地址拆分为 local_part 和 domain。
func splitEmail(email string) (localPart, domain string, ok bool) {
	for i := len(email) - 1; i >= 0; i-- {
		if email[i] == '@' {
			return email[:i], email[i+1:], true
		}
	}
	return "", "", false
}

func (h *EmailHandler) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.GET("/emails", h.AdminGetEmails)
	r.GET("/emails/:message_id/body", h.AdminGetEmailBody)
	r.DELETE("/emails/:message_id", h.AdminDeleteEmail)
	r.GET("/emails/:message_id/attachments/:index", h.AdminGetEmailAttachment)
	r.GET("/emails/:message_id/attachments/:index/preview", h.AdminGetEmailAttachmentPreview)
}

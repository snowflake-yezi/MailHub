package handler

import (
	"encoding/json"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
)

type EmailHandler struct {
	store        *store.Store
	sharedSecret string
}

func NewEmailHandler(s *store.Store, sharedSecret string) *EmailHandler {
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

	var rawResp map[string]interface{}
	if err := json.Unmarshal(data, &rawResp); err != nil {
		serverError(c, ErrCodeExternalFail, "failed to parse email response")
		return
	}
	if dataMap, ok := rawResp["data"].(map[string]interface{}); ok {
		if orderID != "" {
			dataMap["order_id"] = orderID
		}
		dataMap["email_address"] = mb.EmailAddress
	}
	rawResp["request_id"] = uuidShort()

	c.JSON(200, rawResp)
}

// GetEmailBody 获取单封邮件完整内容（大模型系统调用）
// GET /api/v1/emails/:message_id/body
func (h *EmailHandler) GetEmailBody(c *gin.Context) {
	h.proxyEmailBody(c, c.Param("message_id"), c.Query("mailbox"))
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

	query := url.Values{}
	query.Set("mailbox", mb.EmailAddress)
	path := "/internal/messages/" + url.PathEscape(messageID) + "?" + query.Encode()
	data, err := proxyToServer(srv.APIHost, "GET", path, nil, h.sharedSecret)
	if err != nil {
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

func mailboxParam(c *gin.Context) string {
	if v := c.Param("email"); v != "" {
		return v
	}
	if v := c.Param("order_id"); v != "" {
		return v
	}
	return c.Query("mailbox")
}

func (h *EmailHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/orders/:order_id/emails", h.GetOrderEmails)
	r.GET("/mailboxes/:order_id/messages", h.GetMailboxMessages)
	r.GET("/emails/:message_id/body", h.GetEmailBody)
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

	var rawResp map[string]interface{}
	if err := json.Unmarshal(data, &rawResp); err != nil {
		serverError(c, ErrCodeExternalFail, "failed to parse email response")
		return
	}
	if dataMap, ok := rawResp["data"].(map[string]interface{}); ok {
		if orderID != "" {
			dataMap["order_id"] = orderID
		}
		dataMap["email_address"] = emailAddr
	}
	rawResp["request_id"] = uuidShort()

	c.JSON(200, rawResp)
}

// proxyEmailBodyDirect 直接向指定服务器代理邮件正文请求（跳过 mailbox_accounts 查找）。
func (h *EmailHandler) proxyEmailBodyDirect(c *gin.Context, srv *model.MailServer, messageID, emailAddr string) {
	query := url.Values{}
	query.Set("mailbox", emailAddr)
	path := "/internal/messages/" + url.PathEscape(messageID) + "?" + query.Encode()
	data, err := proxyToServer(srv.APIHost, "GET", path, nil, h.sharedSecret)
	if err != nil {
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
}

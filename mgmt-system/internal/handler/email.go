package handler

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/store"
)

type EmailHandler struct {
	store        *store.Store
	sharedSecret string
}

func NewEmailHandler(s *store.Store, sharedSecret string) *EmailHandler {
	return &EmailHandler{store: s, sharedSecret: sharedSecret}
}

// GetOrderEmails 按订单查询邮件列表（大模型系统调用）
// GET /api/v1/orders/:order_id/emails
func (h *EmailHandler) GetOrderEmails(c *gin.Context) {
	orderID := c.Param("order_id")

	mb, err := h.store.GetMailboxByOrderID(orderID)
	if err != nil {
		notFound(c, "mailbox not found for order: "+orderID)
		return
	}

	srv, err := h.store.GetServer(mb.ServerID)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "mail server not found")
		return
	}

	// 代理到邮箱服务器
	path := fmt.Sprintf("/internal/mailboxes/%s/messages?page=%s&size=%s",
		mb.EmailAddress,
		c.DefaultQuery("page", "1"),
		c.DefaultQuery("size", "20"),
	)

	data, err := proxyToServer(srv.APIHost, "GET", path, nil, h.sharedSecret)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "failed to fetch emails: "+err.Error())
		return
	}

	// 注入订单上下文
	var rawResp map[string]interface{}
	json.Unmarshal(data, &rawResp)
	if dataMap, ok := rawResp["data"].(map[string]interface{}); ok {
		dataMap["order_id"] = orderID
		dataMap["email_address"] = mb.EmailAddress
	}
	rawResp["request_id"] = uuidShort()

	c.JSON(200, rawResp)
}

// GetEmailBody 获取单封邮件完整内容（大模型系统调用）
// GET /api/v1/emails/:message_id/body
func (h *EmailHandler) GetEmailBody(c *gin.Context) {
	messageID := c.Param("message_id")
	emailAddr := c.Query("mailbox") // 需要指定是哪个邮箱的邮件

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

	path := fmt.Sprintf("/internal/messages/%s?mailbox=%s", messageID, mb.EmailAddress)
	data, err := proxyToServer(srv.APIHost, "GET", path, bytes.NewReader(nil), h.sharedSecret)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "failed to fetch email body: "+err.Error())
		return
	}

	var rawResp map[string]interface{}
	json.Unmarshal(data, &rawResp)
	rawResp["request_id"] = uuidShort()
	c.JSON(200, rawResp)
}

func (h *EmailHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/orders/:order_id/emails", h.GetOrderEmails)
	r.GET("/emails/:message_id/body", h.GetEmailBody)
}

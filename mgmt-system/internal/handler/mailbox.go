package handler

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/service"
	"github.com/ticket/email-mgmt-system/internal/store"
)

type MailboxHandler struct {
	store        *store.Store
	allocator    *service.Allocator
	creator      *service.MailboxCreator
	sharedSecret string
	client       *http.Client
}

func NewMailboxHandler(s *store.Store, alloc *service.Allocator, sharedSecret string) *MailboxHandler {
	return &MailboxHandler{
		store:        s,
		allocator:    alloc,
		creator:      alloc.Creator(),
		sharedSecret: sharedSecret,
		client:       &http.Client{Timeout: 10 * time.Second},
	}
}

type CreateMailboxRequest struct {
	OrderID       string `json:"order_id" binding:"required"`
	DomainID      uint64 `json:"domain_id"`
	RetentionDays int    `json:"retention_days"`
}

func (h *MailboxHandler) CreateMailbox(c *gin.Context) {
	var req CreateMailboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, ErrCodeParamMissing, "order_id is required")
		return
	}

	result, err := h.allocator.Allocate(req.OrderID, req.DomainID, req.RetentionDays)
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to allocate mailbox: "+err.Error())
		return
	}

	msg := "created"
	if result.IsExisting {
		msg = "already_exists"
	}
	created(c, msg, result)
}

type BatchCreateItem struct {
	Prefix   string `json:"prefix" binding:"required"`
	Password string `json:"password"`
	DomainID uint64 `json:"domain_id"`
	ServerID uint64 `json:"server_id"`
}

type BatchCreateResult struct {
	Prefix       string `json:"prefix"`
	EmailAddress string `json:"email_address"`
	Password     string `json:"password,omitempty"`
	Status       string `json:"status"`
	SyncStatus   string `json:"sync_status,omitempty"`
	Error        string `json:"error,omitempty"`
}

func (h *MailboxHandler) CreateMailboxBatch(c *gin.Context) {
	var items []BatchCreateItem
	if err := c.ShouldBindJSON(&items); err != nil {
		badRequest(c, ErrCodeParamMissing, "invalid batch data: "+err.Error())
		return
	}
	if len(items) == 0 {
		badRequest(c, ErrCodeParamMissing, "at least one item required")
		return
	}

	results := h.processBatchCreate(items)
	success(c, "batch completed", batchSummary(items, results))
}

func (h *MailboxHandler) UploadMailboxCSV(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		badRequest(c, ErrCodeParamMissing, "no file uploaded: "+err.Error())
		return
	}
	defer file.Close()

	name := strings.ToLower(header.Filename)
	if !strings.HasSuffix(name, ".csv") && !strings.HasSuffix(name, ".txt") {
		badRequest(c, ErrCodeParamInvalid, "only .csv or .txt files are supported")
		return
	}

	items, err := parseCSV(file)
	if err != nil {
		badRequest(c, ErrCodeParamInvalid, "failed to parse file: "+err.Error())
		return
	}
	if len(items) == 0 {
		badRequest(c, ErrCodeParamMissing, "file is empty or has no valid rows")
		return
	}

	results := h.processBatchCreate(items)
	summary := batchSummary(items, results)
	summary["file"] = header.Filename
	success(c, "upload processed", summary)
}

func (h *MailboxHandler) processBatchCreate(items []BatchCreateItem) []BatchCreateResult {
	results := make([]BatchCreateResult, 0, len(items))
	for _, item := range items {
		item.Prefix = strings.TrimSpace(item.Prefix)
		result := BatchCreateResult{Prefix: item.Prefix}
		if item.Prefix == "" {
			result.Status = "fail"
			result.Error = "prefix is required"
			results = append(results, result)
			continue
		}

		created, err := h.creator.Create(service.MailboxCreateInput{
			OrderID:       item.Prefix,
			LocalPart:     item.Prefix,
			Password:      item.Password,
			DomainID:      item.DomainID,
			ServerID:      item.ServerID,
			RetentionDays: 30,
		})
		if err != nil {
			result.Status = "fail"
			result.Error = err.Error()
			results = append(results, result)
			continue
		}

		result.EmailAddress = created.EmailAddress
		result.Password = created.Password
		result.SyncStatus = created.SyncStatus
		result.Status = "ok"
		results = append(results, result)
	}
	return results
}

func batchSummary(items []BatchCreateItem, results []BatchCreateResult) gin.H {
	okCount := 0
	failCount := 0
	for _, r := range results {
		if r.Status == "ok" {
			okCount++
		} else {
			failCount++
		}
	}

	return gin.H{
		"total":   len(items),
		"success": okCount,
		"failed":  failCount,
		"results": results,
	}
}

func parseCSV(r io.Reader) ([]BatchCreateItem, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	reader.Comment = '#'
	var items []BatchCreateItem
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csv parse error: %w", err)
		}
		if len(record) == 0 || strings.TrimSpace(record[0]) == "" {
			continue
		}

		item := BatchCreateItem{Prefix: strings.TrimSpace(record[0])}
		if len(record) >= 2 {
			item.Password = strings.TrimSpace(record[1])
		}
		items = append(items, item)
	}
	return items, nil
}

func (h *MailboxHandler) GetMailbox(c *gin.Context) {
	orderID := c.Param("order_id")

	mb, err := h.store.GetMailboxByOrderID(orderID)
	if err != nil {
		notFound(c, "mailbox not found for order: "+orderID)
		return
	}

	success(c, "success", mb)
}

func (h *MailboxHandler) DisableMailbox(c *gin.Context) {
	orderID := c.Param("order_id")

	mb, err := h.store.GetMailboxByOrderID(orderID)
	if err != nil {
		notFound(c, "mailbox not found for order: "+orderID)
		return
	}

	h.executeDeletion(c, mb)
}

// RequestDelete 管理后台触发的邮箱删除（四态流转入口）。
// POST /api/v1/admin/mailboxes/:id/delete
func (h *MailboxHandler) RequestDelete(c *gin.Context) {
	id := parseUint64(c.Param("id"))
	if id == 0 {
		badRequest(c, ErrCodeParamMissing, "invalid mailbox id")
		return
	}

	mb, err := h.store.GetMailboxByID(id)
	if err != nil {
		notFound(c, "mailbox not found")
		return
	}

	h.executeDeletion(c, mb)
}

// executeDeletion 执行完整的删除下发流程：
// ① RequestDeletion → deleting + delete_requested_at
// ② proxy mail-node DELETE /internal/mailboxes/{email}
// ③ 成功 → ConfirmDeletion → soft_deleted；失败 → 保持 deleting（Watchdog 重试）
func (h *MailboxHandler) executeDeletion(c *gin.Context, mb *model.MailboxAccount) {
	// ① 标记 deleting
	if err := h.store.RequestDeletion(mb.ID); err != nil {
		serverError(c, ErrCodeInternal, "failed to request deletion: "+err.Error())
		return
	}

	// ② 下发 mail-node 安全销毁
	srv, err := h.store.GetServer(mb.ServerID)
	if err != nil {
		serverError(c, ErrCodeExternalFail, "mail server not found")
		return
	}
	if err := h.callNodeDeleteMailbox(srv.APIHost, mb.EmailAddress); err != nil {
		// 保持 deleting 状态，Watchdog 会重试
		serverError(c, ErrCodeExternalFail, "failed to delete on mail node (will retry): "+err.Error())
		return
	}

	// ③ 成功，标记 soft_deleted
	if err := h.store.ConfirmDeletion(mb.ID); err != nil {
		serverError(c, ErrCodeInternal, "failed to confirm deletion: "+err.Error())
		return
	}

	success(c, "deleted", gin.H{
		"id":            mb.ID,
		"email_address": mb.EmailAddress,
		"status":        "soft_deleted",
	})
}

// callNodeDeleteMailbox 向 mail-node 发送 DELETE 请求，触发 MoveToTrash。
func (h *MailboxHandler) callNodeDeleteMailbox(apiHost, email string) error {
	url := fmt.Sprintf("http://%s/internal/mailboxes/%s", apiHost, email)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-Internal-Token", h.sharedSecret)

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upstream error: %d - %s", resp.StatusCode, string(data))
	}
	return nil
}

func (h *MailboxHandler) ListMailboxes(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	status := c.Query("status")
	search := c.Query("search")
	if search == "" {
		search = c.Query("order_id")
	}
	domainID := parseUint64(c.Query("domain_id"))
	serverID := parseUint64(c.Query("server_id"))

	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}

	list, total, err := h.store.ListMailboxesWithFilter(page, size, store.MailboxListFilter{
		Status:   status,
		Search:   search,
		DomainID: domainID,
		ServerID: serverID,
	})
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list mailboxes")
		return
	}

	success(c, "success", gin.H{
		"items": list,
		"total": total,
		"page":  page,
		"size":  size,
	})
}

// UpdateMailboxPassword updates the password for a mailbox account.
// PUT /api/v1/admin/mailboxes/:id
func (h *MailboxHandler) UpdateMailboxPassword(c *gin.Context) {
	id := parseUint64(c.Param("id"))
	if id == 0 {
		badRequest(c, ErrCodeParamMissing, "invalid mailbox id")
		return
	}

	existing, err := h.store.GetMailboxByID(id)
	if err != nil {
		notFound(c, "mailbox not found")
		return
	}

	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, ErrCodeParamMissing, "password is required")
		return
	}
	if len(req.Password) < 6 {
		badRequest(c, ErrCodeParamInvalid, "password must be at least 6 characters")
		return
	}

	// Update password on remote mail-node first.
	srv, err := h.store.GetServer(existing.ServerID)
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to load mailbox server: "+err.Error())
		return
	}
	if err := h.callNodeUpdatePassword(srv.APIHost, existing.EmailAddress, req.Password); err != nil {
		serverError(c, ErrCodeExternalFail, "failed to update password on mail node: "+err.Error())
		return
	}

	// Update in local database.
	if err := h.store.UpdateMailboxPassword(id, req.Password); err != nil {
		serverError(c, ErrCodeInternal, "failed to update password: "+err.Error())
		return
	}

	success(c, "password updated", gin.H{
		"id":            id,
		"email_address": existing.EmailAddress,
	})
}

// callNodeUpdatePassword sends a password update request to the mail-node.
func (h *MailboxHandler) callNodeUpdatePassword(apiHost, email, newPassword string) error {
	body, _ := json.Marshal(map[string]string{
		"email_address": email,
		"password":      newPassword,
	})
	url := fmt.Sprintf("http://%s/internal/mailboxes/%s/password", apiHost, email)
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", h.sharedSecret)

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upstream error: %d - %s", resp.StatusCode, string(data))
	}
	return nil
}

// RegisterRoutes registers external API routes on the given group.
func (h *MailboxHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/mailboxes", h.CreateMailbox)
	r.GET("/mailboxes/:order_id", h.GetMailbox)
	r.POST("/mailboxes/:order_id/disable", h.DisableMailbox)
}

// SyncDeleting 返回指定服务器上所有 deleting 状态的邮箱列表。
// GET /api/v1/internal/sync/deleting?server_id=<id>
// 由 mail-node 重启时 PullDeletingTasks 调用，用于恢复崩溃/重启前未完成的删除任务。
func (h *MailboxHandler) SyncDeleting(c *gin.Context) {
	serverID := parseUint64(c.Query("server_id"))
	if serverID == 0 {
		badRequest(c, ErrCodeParamMissing, "server_id is required")
		return
	}
	list, err := h.store.ListDeletingByServer(serverID)
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list deleting tasks")
		return
	}
	if list == nil {
		list = []model.MailboxAccount{}
	}
	success(c, "success", list)
}

// RegisterAdminRoutes registers admin API routes on the given (already auth-protected) group.
func (h *MailboxHandler) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.GET("/mailboxes", h.ListMailboxes)
	r.POST("/mailboxes/batch", h.CreateMailboxBatch)
	r.POST("/mailboxes/upload", h.UploadMailboxCSV)
	r.PUT("/mailboxes/:id", h.UpdateMailboxPassword)
	r.POST("/mailboxes/:id/delete", h.RequestDelete)
}

var _ = http.Handler(nil)

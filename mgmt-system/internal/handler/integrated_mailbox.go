package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodetransport"
	"github.com/ticket/email-mgmt-system/internal/store"
)

// IntegratedMailboxHandler 集成邮箱（转发目标池）管理
type IntegratedMailboxHandler struct {
	store     *store.Store
	transport nodetransport.NodeTransport
}

// NewIntegratedMailboxHandler 创建 handler
func NewIntegratedMailboxHandler(s *store.Store, transport nodetransport.NodeTransport) *IntegratedMailboxHandler {
	return &IntegratedMailboxHandler{store: s, transport: transport}
}

// RegisterAdminRoutes 注册 Session 鉴权的 admin API 路由
func (h *IntegratedMailboxHandler) RegisterAdminRoutes(rg *gin.RouterGroup) {
	rg.GET("/integrated-mailboxes", h.List)
	rg.POST("/integrated-mailboxes", h.Create)
	rg.PUT("/integrated-mailboxes/:id", h.Update)
	rg.DELETE("/integrated-mailboxes/:id", h.Delete)
	rg.POST("/integrated-mailboxes/:id/activate", h.Activate)
}

// List 列出全部集成邮箱
func (h *IntegratedMailboxHandler) List(c *gin.Context) {
	list, err := h.store.ListIntegratedMailboxes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": "failed to list: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": list})
}

// Create 新增集成邮箱
func (h *IntegratedMailboxHandler) Create(c *gin.Context) {
	var m model.IntegratedMailbox
	if err := c.ShouldBindJSON(&m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "invalid request: " + err.Error()})
		return
	}
	if m.EmailAddress == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "email_address is required"})
		return
	}
	m.ID = 0
	m.IsActive = false // 新建不直接设为生效，需显式 activate
	if err := h.store.CreateIntegratedMailbox(&m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": "create failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": m})
}

// Update 更新地址与备注
func (h *IntegratedMailboxHandler) Update(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "invalid id"})
		return
	}
	var m model.IntegratedMailbox
	if err := c.ShouldBindJSON(&m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "invalid request: " + err.Error()})
		return
	}
	m.ID = id
	if err := h.store.UpdateIntegratedMailbox(&m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": "update failed: " + err.Error()})
		return
	}
	// 若改的是当前生效项，同步 forward.target_address 并通知节点 reload
	if updated, err := h.store.GetIntegratedMailbox(id); err == nil && updated.IsActive {
		_ = h.store.SetConfig("forward.target_address", updated.EmailAddress)
		h.store.InvalidateConfigCache()
		h.notifyNodesReload()
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "updated"})
}

// Delete 删除集成邮箱（当前生效项禁止删除）
func (h *IntegratedMailboxHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "invalid id"})
		return
	}
	m, err := h.store.GetIntegratedMailbox(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 2003, "message": "integrated mailbox not found"})
		return
	}
	if m.IsActive {
		c.JSON(http.StatusBadRequest, gin.H{"code": 2001, "message": "不能删除当前生效的集成邮箱，请先切换到其他邮箱"})
		return
	}
	if err := h.store.DeleteIntegratedMailbox(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": "delete failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "deleted"})
}

// Activate 设为当前生效转发目标，并联动通知所有 healthy/degraded 节点重载配置。
func (h *IntegratedMailboxHandler) Activate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "invalid id"})
		return
	}
	if err := h.store.SetActiveIntegratedMailbox(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": "activate failed: " + err.Error()})
		return
	}
	h.store.InvalidateConfigCache()

	// 联动通知 mail-node reload，使新 target 即时热加载
	reloaded, failed := h.notifyNodesReload()
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "activated",
		"data":    gin.H{"reloaded": reloaded, "failed": failed},
	})
}

// notifyNodesReload 遍历 healthy/degraded 节点，POST /internal/configs/reload。
// 逻辑同 ConfigHandler.notifyNodeReload（config.go），此处内联以保持 handler 自治。
func (h *IntegratedMailboxHandler) notifyNodesReload() (int, int) {
	servers, err := h.store.ListServers()
	if err != nil {
		return 0, 0
	}
	reloaded, failed := 0, 0
	for _, srv := range servers {
		if !nodeCanReceiveNotification(&srv) {
			continue
		}
		resp, err := h.transport.Notify(context.Background(), nodeTarget(&srv), nodetransport.ConfigRevisionChanged(0))
		if err != nil {
			failed++
			continue
		}
		if resp.StatusCode >= 400 {
			failed++
			continue
		}
		reloaded++
	}
	return reloaded, failed
}

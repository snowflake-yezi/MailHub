package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
)

type FilterHandler struct {
	store *store.Store
}

func NewFilterHandler(s *store.Store) *FilterHandler {
	return &FilterHandler{store: s}
}

// CreateRule 新增过滤规则
// POST /api/v1/admin/filters
func (h *FilterHandler) CreateRule(c *gin.Context) {
	var rule model.FilterRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		badRequest(c, ErrCodeParamMissing, "invalid rule data: "+err.Error())
		return
	}

	if rule.Name == "" || rule.Pattern == "" {
		badRequest(c, ErrCodeParamMissing, "name and pattern are required")
		return
	}
	if rule.RuleType == "" {
		rule.RuleType = "keyword"
	}
	if rule.Action == "" {
		rule.Action = "pass"
	}

	if err := h.store.CreateRule(&rule); err != nil {
		serverError(c, ErrCodeInternal, "failed to create rule: "+err.Error())
		return
	}

	created(c, "rule created", rule)
}

// UpdateRule 修改规则
// PUT /api/v1/admin/filters/:id
func (h *FilterHandler) UpdateRule(c *gin.Context) {
	id := parseUint64(c.Param("id"))

	existing, err := h.store.GetRule(id)
	if err != nil {
		notFound(c, "rule not found")
		return
	}

	var update model.FilterRule
	if err := c.ShouldBindJSON(&update); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid update data")
		return
	}

	if update.Name != "" {
		existing.Name = update.Name
	}
	if update.Pattern != "" {
		existing.Pattern = update.Pattern
	}
	if update.RuleType != "" {
		existing.RuleType = update.RuleType
	}
	if update.Action != "" {
		existing.Action = update.Action
	}
	existing.Priority = update.Priority
	existing.Enabled = update.Enabled

	if err := h.store.UpdateRule(existing); err != nil {
		serverError(c, ErrCodeInternal, "failed to update rule")
		return
	}

	success(c, "updated", existing)
}

// DeleteRule 删除规则
// DELETE /api/v1/admin/filters/:id
func (h *FilterHandler) DeleteRule(c *gin.Context) {
	id := parseUint64(c.Param("id"))

	if err := h.store.DeleteRule(id); err != nil {
		serverError(c, ErrCodeInternal, "failed to delete rule")
		return
	}

	success(c, "deleted", nil)
}

// ListRules 规则列表
// GET /api/v1/admin/filters
func (h *FilterHandler) ListRules(c *gin.Context) {
	list, err := h.store.ListAllRules()
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list rules")
		return
	}
	success(c, "success", list)
}

// GetActiveRules 邮箱服务器拉取启用的规则
// GET /api/v1/internal/filters
func (h *FilterHandler) GetActiveRules(c *gin.Context) {
	list, err := h.store.ListRules()
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list active rules")
		return
	}
	success(c, "success", list)
}

// RegisterAdminRoutes registers all filter admin API routes on the given (already auth-protected) group.
func (h *FilterHandler) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.POST("/filters", h.CreateRule)
	r.GET("/filters", h.ListRules)
	r.PUT("/filters/:id", h.UpdateRule)
	r.DELETE("/filters/:id", h.DeleteRule)
}

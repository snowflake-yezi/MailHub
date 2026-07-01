package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ticket/email-mgmt-system/internal/store"
)

// HealthHandler 服务健康检查端点（liveness / readiness）。
// 公开无鉴权，供 systemd / 负载均衡 / 监控探活。
type HealthHandler struct {
	store *store.Store
}

func NewHealthHandler(s *store.Store) *HealthHandler {
	return &HealthHandler{store: s}
}

// Health liveness 探活：进程可达即健康，不检查依赖。
// GET /health
func (h *HealthHandler) Health(c *gin.Context) {
	success(c, "ok", gin.H{"status": "ok"})
}

// Ready readiness 探活：依赖（数据库）可达才视为就绪，否则 503。
// GET /health/ready
func (h *HealthHandler) Ready(c *gin.Context) {
	if err := h.store.Ping(); err != nil {
		c.JSON(http.StatusServiceUnavailable, Response{
			Code:      ErrCodeInternal,
			Message:   "db not ready: " + err.Error(),
			RequestID: uuid.New().String()[:8],
		})
		return
	}
	success(c, "ready", gin.H{"status": "ready"})
}

package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/configschema"
	"github.com/ticket/email-mgmt-system/internal/store"
)

// ConfigHandler 系统动态配置管理
type ConfigHandler struct {
	store        *store.Store
	sharedSecret string
	httpClient   *http.Client
}

// NewConfigHandler 创建配置 handler
func NewConfigHandler(s *store.Store, sharedSecret string) *ConfigHandler {
	return &ConfigHandler{
		store:        s,
		sharedSecret: sharedSecret,
		httpClient:   &http.Client{Timeout: 5 * time.Second},
	}
}

// groupedConfig 分组配置响应
type groupedConfig struct {
	Category string       `json:"category"`
	Label    string       `json:"label"`
	Items    []configItem `json:"items"`
}

type configItem struct {
	Key          string `json:"key"`
	Value        string `json:"value"`
	ValueType    string `json:"value_type"`
	Label        string `json:"label"`
	Description  string `json:"description"`
	DefaultValue string `json:"default_value"`
	Reloadable   bool   `json:"reloadable"`
	EffectType   string `json:"effect_type"`
}

func configEffectType(key string, reloadable bool) string {
	if key == "general.default_retention_days" {
		return "new_resources"
	}
	if reloadable {
		return "hot_reload"
	}
	return "restart"
}

// ListConfigs 按 category 分组列出全部配置
// GET /api/v1/admin/configs
func (h *ConfigHandler) ListConfigs(c *gin.Context) {
	category := c.Query("category")

	configs, err := h.store.ListConfigsByCategory(category)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 5000, "message": "failed to list configs: " + err.Error(),
		})
		return
	}

	// 按 category 分组
	groupMap := make(map[string]*groupedConfig)
	groupOrder := []string{} // 保持插入顺序

	categoryLabels := map[string]string{
		"forward":     "邮件转发引擎",
		"filter":      "过滤引擎",
		"lifecycle":   "生命周期管理",
		"healthcheck": "健康检查",
		"heartbeat":   "心跳上报",
		"session":     "管理会话",
		"database":    "数据库连接池",
		"maildir":     "邮件存储",
		"general":     "通用参数",
	}

	for _, cfg := range configs {
		g, exists := groupMap[cfg.Category]
		if !exists {
			label := cfg.Category
			if l, ok := categoryLabels[cfg.Category]; ok {
				label = l
			}
			g = &groupedConfig{Category: cfg.Category, Label: label, Items: []configItem{}}
			groupMap[cfg.Category] = g
			groupOrder = append(groupOrder, cfg.Category)
		}
		g.Items = append(g.Items, configItem{
			Key:          cfg.ConfigKey,
			Value:        cfg.ConfigValue,
			ValueType:    cfg.ValueType,
			Label:        cfg.Label,
			Description:  cfg.Description,
			DefaultValue: cfg.DefaultValue,
			Reloadable:   cfg.Reloadable,
			EffectType:   configEffectType(cfg.ConfigKey, cfg.Reloadable),
		})
	}

	groups := make([]groupedConfig, 0, len(groupOrder))
	for _, cat := range groupOrder {
		groups = append(groups, *groupMap[cat])
	}
	if groups == nil {
		groups = []groupedConfig{}
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"groups":       groups,
			"total_groups": len(groups),
		},
	})
}

// GetConfig 获取单个配置
// GET /api/v1/admin/configs/:key
func (h *ConfigHandler) GetConfig(c *gin.Context) {
	key := c.Param("key")
	cfg, err := h.store.GetConfigByKey(key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 2003, "message": "config not found: " + key,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": configItem{
			Key:          cfg.ConfigKey,
			Value:        cfg.ConfigValue,
			ValueType:    cfg.ValueType,
			Label:        cfg.Label,
			Description:  cfg.Description,
			DefaultValue: cfg.DefaultValue,
			Reloadable:   cfg.Reloadable,
			EffectType:   configEffectType(cfg.ConfigKey, cfg.Reloadable),
		},
	})
}

// UpdateConfig 更新单个配置
// PUT /api/v1/admin/configs/:key
func (h *ConfigHandler) UpdateConfig(c *gin.Context) {
	key := c.Param("key")

	var req struct {
		Value string `json:"value" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1001, "message": "value is required",
		})
		return
	}

	if err := h.store.SetConfig(key, req.Value); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 5000, "message": "failed to update config: " + err.Error(),
		})
		return
	}

	// 更新后清除缓存
	h.store.InvalidateConfigCache()

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "config updated"})
}

// BatchUpdate 批量更新配置
// POST /api/v1/admin/configs/batch
func (h *ConfigHandler) BatchUpdate(c *gin.Context) {
	var req struct {
		Updates map[string]string `json:"updates" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1001, "message": "updates map is required",
		})
		return
	}

	if err := h.store.BatchSetConfigs(req.Updates); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 5000, "message": "failed to batch update: " + err.Error(),
		})
		return
	}

	h.store.InvalidateConfigCache()

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "configs updated", "data": gin.H{"count": len(req.Updates)}})
}

// ResetConfig 恢复单个配置为默认值
// POST /api/v1/admin/configs/:key/reset
func (h *ConfigHandler) ResetConfig(c *gin.Context) {
	key := c.Param("key")

	if err := h.store.ResetConfig(key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 5000, "message": "failed to reset config: " + err.Error(),
		})
		return
	}

	h.store.InvalidateConfigCache()

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "config reset to default"})
}

// ReloadNode 通知 mail-node 重载热配置（向所有 healthy/degraded 节点下发）
// POST /api/v1/admin/configs/reload
func (h *ConfigHandler) ReloadNode(c *gin.Context) {
	// 从 shared_secret 获取（已由中间件注入到 context）
	// 这里用 store 直接查 servers
	servers, err := h.store.ListServers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 5000, "message": "failed to list servers: " + err.Error(),
		})
		return
	}

	reloaded := 0
	failed := 0
	for _, srv := range servers {
		if srv.Status != "healthy" && srv.Status != "degraded" {
			_ = h.store.RecordServerReloadResult(srv.ID, fmt.Errorf("node status %s", srv.Status))
			failed++
			continue
		}
		// 调用 mail-node 的 reload 端点
		if err := h.notifyNodeReload(srv.APIHost); err != nil {
			_ = h.store.RecordServerReloadResult(srv.ID, err)
			failed++
		} else {
			_ = h.store.RecordServerReloadResult(srv.ID, nil)
			reloaded++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "reload dispatched",
		"data":    gin.H{"reloaded": reloaded, "failed": failed},
	})
}

// notifyNodeReload 通知单个 mail-node 重载配置
func (h *ConfigHandler) notifyNodeReload(apiHost string) error {
	url := "http://" + strings.TrimRight(apiHost, "/") + "/internal/configs/reload"
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Internal-Token", h.sharedSecret)

	client := h.httpClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}

// ListConfigsInternal 内部接口：mail-node 拉取全量配置
// GET /api/v1/internal/configs
func (h *ConfigHandler) ListConfigsInternal(c *gin.Context) {
	configs, err := h.store.ListConfigsByCategory("")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 5000, "message": "failed to list configs: " + err.Error(),
		})
		return
	}

	// 返回简化的 KV map。内部接口可额外下发敏感运行时配置；这些值不进入 system_configs，
	// 避免在后台配置页暴露邮箱密码。
	result := make(map[string]string, len(configs)+3)
	sources := make(map[string]string, len(configs))
	var desiredRevision uint64
	for _, cfg := range configs {
		result[cfg.ConfigKey] = cfg.ConfigValue
		sources[cfg.ConfigKey] = "global"
	}
	if rawServerID := strings.TrimSpace(c.Query("server_id")); rawServerID != "" {
		serverID, parseErr := strconv.ParseUint(rawServerID, 10, 64)
		if parseErr != nil || serverID == 0 {
			fail(c, http.StatusBadRequest, 1001, "invalid server_id")
			return
		}
		server, serverErr := h.store.GetServer(serverID)
		if serverErr != nil {
			fail(c, http.StatusNotFound, 2001, "server not found")
			return
		}
		desiredRevision = server.DesiredRevision
		overrides, overrideErr := h.store.ListServerConfigOverrides(serverID)
		if overrideErr != nil {
			fail(c, http.StatusInternalServerError, 5000, "failed to list server config overrides")
			return
		}
		for _, override := range overrides {
			if definition, supported := configschema.Get(override.ConfigKey); supported && definition.NodeOverridable {
				result[override.ConfigKey] = override.ConfigValue
				sources[override.ConfigKey] = "server_override"
			}
		}
	}
	if integrated, account, err := h.store.GetActiveIntegratedMailboxCredentials(); err == nil {
		result["forward.target_address"] = integrated.EmailAddress
		result["forward.smtp_user"] = account.EmailAddress
		result["forward.smtp_pass"] = account.Password
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{"configs": result, "sources": sources, "desired_revision": desiredRevision},
	})
}

// ReloadNodeInternal 内部接口：通知 mail-node 重载配置
// POST /api/v1/internal/configs/reload
func (h *ConfigHandler) ReloadNodeInternal(c *gin.Context) {
	// 内部调用只返回成功（实际重载逻辑在 mail-node 端）
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "reload acknowledged"})
}

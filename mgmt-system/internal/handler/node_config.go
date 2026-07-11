package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
)

const trashRetentionKey = "lifecycle.trash_retention_hours"

type nodeConfigDefinition struct {
	Key             string
	Label           string
	ValueType       string
	Min             int
	Max             int
	Reloadable      bool
	RequiresRestart bool
}

var nodeConfigDefinitions = map[string]nodeConfigDefinition{
	trashRetentionKey: {
		Key: trashRetentionKey, Label: "回收站保留时间", ValueType: "int",
		Min: 1, Max: 8760, Reloadable: false, RequiresRestart: true,
	},
}

type nodeConfigItem struct {
	Key             string     `json:"key"`
	Label           string     `json:"label"`
	ValueType       string     `json:"value_type"`
	Unit            string     `json:"unit"`
	GlobalValue     string     `json:"global_value"`
	OverrideValue   *string    `json:"override_value"`
	EffectiveValue  *string    `json:"effective_value"`
	Source          string     `json:"source"`
	Reloadable      bool       `json:"reloadable"`
	RequiresRestart bool       `json:"requires_restart"`
	Status          string     `json:"status"`
	ReportedAt      *time.Time `json:"reported_at,omitempty"`
}

func parseServerID(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		fail(c, http.StatusBadRequest, 1001, "invalid server id")
		return 0, false
	}
	return id, true
}

func (h *ConfigHandler) GetServerConfigs(c *gin.Context) {
	serverID, ok := parseServerID(c)
	if !ok {
		return
	}
	server, err := h.store.GetServer(serverID)
	if err != nil {
		fail(c, http.StatusNotFound, 2001, "server not found")
		return
	}
	items, err := h.nodeConfigItems(serverID)
	if err != nil {
		fail(c, http.StatusInternalServerError, 5000, "failed to load node configs")
		return
	}
	success(c, "success", gin.H{"server_id": server.ID, "server_name": server.Name, "api_host": server.APIHost, "items": items})
}

func (h *ConfigHandler) PutServerConfig(c *gin.Context) {
	serverID, ok := parseServerID(c)
	if !ok {
		return
	}
	key := strings.TrimSpace(c.Param("key"))
	definition, ok := nodeConfigDefinitions[key]
	if !ok {
		fail(c, http.StatusBadRequest, 1002, "unsupported node config key")
		return
	}
	if _, err := h.store.GetServer(serverID); err != nil {
		fail(c, http.StatusNotFound, 2001, "server not found")
		return
	}
	var req struct {
		Value string `json:"value" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, 1001, "value is required")
		return
	}
	value := strings.TrimSpace(req.Value)
	number, err := strconv.Atoi(value)
	if err != nil || number < definition.Min || number > definition.Max {
		fail(c, http.StatusBadRequest, 1001, "value must be an integer between 1 and 8760")
		return
	}
	err = h.store.SetServerConfigOverride(&model.ServerConfigOverride{ServerID: serverID, ConfigKey: key, ConfigValue: value, ValueType: definition.ValueType})
	if err != nil {
		fail(c, http.StatusInternalServerError, 5000, "failed to save node config")
		return
	}
	success(c, "node config saved", gin.H{"requires_restart": definition.RequiresRestart})
}

func (h *ConfigHandler) DeleteServerConfig(c *gin.Context) {
	serverID, ok := parseServerID(c)
	if !ok {
		return
	}
	key := strings.TrimSpace(c.Param("key"))
	definition, ok := nodeConfigDefinitions[key]
	if !ok {
		fail(c, http.StatusBadRequest, 1002, "unsupported node config key")
		return
	}
	if err := h.store.DeleteServerConfigOverride(serverID, key); err != nil {
		fail(c, http.StatusInternalServerError, 5000, "failed to reset node config")
		return
	}
	success(c, "node config reset", gin.H{"requires_restart": definition.RequiresRestart})
}

func (h *ConfigHandler) nodeConfigItems(serverID uint64) ([]nodeConfigItem, error) {
	items := make([]nodeConfigItem, 0, len(nodeConfigDefinitions))
	for _, definition := range nodeConfigDefinitions {
		global := h.store.GetConfig(definition.Key, "24")
		item := nodeConfigItem{Key: definition.Key, Label: definition.Label, ValueType: definition.ValueType, Unit: "小时", GlobalValue: global, Source: "unknown", Status: "unreported", Reloadable: definition.Reloadable, RequiresRestart: definition.RequiresRestart}
		if override, err := h.store.GetServerConfigOverride(serverID, definition.Key); err == nil {
			item.OverrideValue = &override.ConfigValue
		} else if !store.IsNotFound(err) {
			return nil, err
		}
		if snapshot, err := h.store.GetServerConfigSnapshot(serverID, definition.Key); err == nil {
			item.EffectiveValue = &snapshot.EffectiveValue
			item.Source = snapshot.Source
			item.ReportedAt = &snapshot.ReportedAt
			item.Status = "applied"
			desired := global
			desiredSource := "global"
			if item.OverrideValue != nil {
				desired = *item.OverrideValue
				desiredSource = "server_override"
			}
			if snapshot.EffectiveValue != desired || snapshot.Source != desiredSource {
				item.Status = "pending_restart"
			}
		} else if !store.IsNotFound(err) {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (h *ConfigHandler) ReportServerConfigSnapshot(c *gin.Context) {
	serverID, ok := parseServerID(c)
	if !ok {
		return
	}
	var req struct {
		ReportedAt time.Time                    `json:"reported_at"`
		Items      []model.ServerConfigSnapshot `json:"items" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Items) == 0 {
		fail(c, http.StatusBadRequest, 1001, "snapshot items are required")
		return
	}
	if req.ReportedAt.IsZero() {
		req.ReportedAt = time.Now().UTC()
	}
	filtered := make([]model.ServerConfigSnapshot, 0, len(req.Items))
	for _, item := range req.Items {
		definition, supported := nodeConfigDefinitions[item.ConfigKey]
		if !supported {
			continue
		}
		item.Reloadable = definition.Reloadable
		item.RequiresRestart = definition.RequiresRestart
		if item.Source != "global" && item.Source != "server_override" && item.Source != "local_config" {
			item.Source = "unknown"
		}
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		fail(c, http.StatusBadRequest, 1002, "no supported snapshot items")
		return
	}
	if err := h.store.UpsertServerConfigSnapshots(serverID, req.ReportedAt, filtered); err != nil {
		fail(c, http.StatusInternalServerError, 5000, "failed to save config snapshot")
		return
	}
	success(c, "config snapshot saved", nil)
}

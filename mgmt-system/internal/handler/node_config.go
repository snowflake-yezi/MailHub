package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/configschema"
	"github.com/ticket/email-mgmt-system/internal/configstate"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
)

const trashRetentionKey = "lifecycle.trash_retention_hours"

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
	success(c, "success", gin.H{
		"server_id": server.ID, "server_name": server.Name, "api_host": server.APIHost, "items": items,
		"desired_revision": server.DesiredRevision, "applied_revision": server.AppliedRevision,
		"last_apply_error": server.LastApplyError, "last_reload_error": server.LastReloadError,
		"last_boot_id": server.LastBootID, "last_started_at": server.LastStartedAt, "config_changed_at": server.ConfigChangedAt,
	})
}

func (h *ConfigHandler) PutServerConfig(c *gin.Context) {
	serverID, ok := parseServerID(c)
	if !ok {
		return
	}
	key := strings.TrimSpace(c.Param("key"))
	definition, ok := configschema.Get(key)
	if ok && !definition.NodeOverridable {
		ok = false
	}
	if !ok {
		fail(c, http.StatusBadRequest, 1002, "unsupported node config key")
		return
	}
	server, err := h.store.GetServer(serverID)
	if err != nil {
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
	desiredRevision, err := h.store.SetServerConfigOverrideAndBump(&model.ServerConfigOverride{ServerID: serverID, ConfigKey: key, ConfigValue: value, ValueType: definition.ValueType})
	if err != nil {
		fail(c, http.StatusInternalServerError, 5000, "failed to save node config")
		return
	}
	reloadErr := h.notifyNodeReload(server.APIHost)
	_ = h.store.RecordServerReloadResult(serverID, reloadErr)
	success(c, "node config saved", reloadDispatchResult(definition, desiredRevision, reloadErr))
}

func (h *ConfigHandler) DeleteServerConfig(c *gin.Context) {
	serverID, ok := parseServerID(c)
	if !ok {
		return
	}
	key := strings.TrimSpace(c.Param("key"))
	definition, ok := configschema.Get(key)
	if ok && !definition.NodeOverridable {
		ok = false
	}
	if !ok {
		fail(c, http.StatusBadRequest, 1002, "unsupported node config key")
		return
	}
	server, err := h.store.GetServer(serverID)
	if err != nil {
		fail(c, http.StatusNotFound, 2001, "server not found")
		return
	}
	desiredRevision, err := h.store.DeleteServerConfigOverrideAndBump(serverID, key)
	if err != nil {
		fail(c, http.StatusInternalServerError, 5000, "failed to reset node config")
		return
	}
	reloadErr := h.notifyNodeReload(server.APIHost)
	_ = h.store.RecordServerReloadResult(serverID, reloadErr)
	success(c, "node config reset", reloadDispatchResult(definition, desiredRevision, reloadErr))
}

func reloadDispatchResult(definition configschema.Definition, desiredRevision uint64, reloadErr error) gin.H {
	result := gin.H{
		"desired_revision":  desiredRevision,
		"requires_restart":  definition.RequiresRestart(),
		"reload_dispatched": reloadErr == nil,
		"reload_target":     "single",
	}
	if reloadErr != nil {
		result["reload_error"] = reloadErr.Error()
	}
	return result
}

func (h *ConfigHandler) nodeConfigItems(serverID uint64) ([]nodeConfigItem, error) {
	server, err := h.store.GetServer(serverID)
	if err != nil {
		return nil, err
	}
	definitions := configschema.NodeOverrides()
	items := make([]nodeConfigItem, 0, len(definitions))
	for _, definition := range definitions {
		global := h.store.GetConfig(definition.Key, "24")
		item := nodeConfigItem{Key: definition.Key, Label: definition.Label, ValueType: definition.ValueType, Unit: "小时", GlobalValue: global, Source: "unknown", Status: "unreported", Reloadable: definition.Reloadable(), RequiresRestart: definition.RequiresRestart()}
		if override, err := h.store.GetServerConfigOverride(serverID, definition.Key); err == nil {
			item.OverrideValue = &override.ConfigValue
		} else if !store.IsNotFound(err) {
			return nil, err
		}
		var snapshot *model.ServerConfigSnapshot
		if value, err := h.store.GetServerConfigSnapshot(serverID, definition.Key); err == nil {
			snapshot = value
			item.EffectiveValue = &snapshot.EffectiveValue
			item.Source = snapshot.Source
			item.ReportedAt = &snapshot.ReportedAt
			desired := global
			desiredSource := "global"
			if item.OverrideValue != nil {
				desired = *item.OverrideValue
				desiredSource = "server_override"
			}
			item.Status = configstate.Resolve(*server, snapshot, definition, desired, desiredSource, time.Now(), 15*time.Minute)
		} else if !store.IsNotFound(err) {
			return nil, err
		} else {
			desired := global
			desiredSource := "global"
			if item.OverrideValue != nil {
				desired = *item.OverrideValue
				desiredSource = "server_override"
			}
			item.Status = configstate.Resolve(*server, nil, definition, desired, desiredSource, time.Now(), 15*time.Minute)
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
		ReportedAt      time.Time                    `json:"reported_at"`
		DesiredRevision uint64                       `json:"desired_revision"`
		AppliedRevision uint64                       `json:"applied_revision"`
		BootID          string                       `json:"boot_id"`
		StartedAt       time.Time                    `json:"started_at"`
		Items           []model.ServerConfigSnapshot `json:"items" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Items) == 0 {
		fail(c, http.StatusBadRequest, 1001, "snapshot items are required")
		return
	}
	if req.ReportedAt.IsZero() {
		req.ReportedAt = time.Now().UTC()
	}
	server, err := h.store.GetServer(serverID)
	if err != nil {
		fail(c, http.StatusNotFound, 2001, "server not found")
		return
	}
	if req.AppliedRevision > req.DesiredRevision || req.AppliedRevision > server.DesiredRevision || req.DesiredRevision > server.DesiredRevision {
		fail(c, http.StatusBadRequest, 1002, "snapshot revision exceeds desired revision")
		return
	}
	if req.AppliedRevision < server.AppliedRevision {
		fail(c, http.StatusConflict, 2004, "stale config snapshot")
		return
	}
	if server.LastStartedAt != nil && !req.StartedAt.IsZero() && req.StartedAt.Before(*server.LastStartedAt) {
		fail(c, http.StatusConflict, 2004, "stale boot snapshot")
		return
	}
	filtered := make([]model.ServerConfigSnapshot, 0, len(req.Items))
	for _, item := range req.Items {
		definition, supported := configschema.Get(item.ConfigKey)
		if !supported {
			continue
		}
		item.Reloadable = definition.Reloadable()
		item.RequiresRestart = definition.RequiresRestart()
		item.BootID = req.BootID
		if item.Source != "global" && item.Source != "server_override" && item.Source != "local_config" {
			item.Source = "unknown"
		}
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		fail(c, http.StatusBadRequest, 1002, "no supported snapshot items")
		return
	}
	if err := h.store.UpsertServerConfigSnapshots(serverID, req.ReportedAt, req.DesiredRevision, req.AppliedRevision, req.BootID, req.StartedAt, filtered); err != nil {
		fail(c, http.StatusInternalServerError, 5000, "failed to save config snapshot")
		return
	}
	success(c, "config snapshot saved", nil)
}

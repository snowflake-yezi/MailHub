package handler

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/service"
	"github.com/ticket/email-mgmt-system/internal/store"
	"gorm.io/gorm"
)

type ExternalAccessHandler struct {
	store *store.Store
}

func NewExternalAccessHandler(s *store.Store) *ExternalAccessHandler {
	return &ExternalAccessHandler{store: s}
}

type externalApplicationRequest struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Enabled         *bool    `json:"enabled"`
	PermissionCodes []string `json:"permission_codes"`
	CredentialName  string   `json:"credential_name"`
	ExpiresAt       string   `json:"expires_at"`
}

type credentialRequest struct {
	Name      string `json:"name"`
	ExpiresAt string `json:"expires_at"`
}

func parsePageValue(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func parseCredentialExpiry(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	expiresAt, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, errors.New("expires_at must be RFC3339")
	}
	if !expiresAt.After(time.Now()) {
		return nil, errors.New("expires_at must be in the future")
	}
	return &expiresAt, nil
}

func (h *ExternalAccessHandler) ListPermissions(c *gin.Context) {
	permissions, err := h.store.ListAPIPermissions()
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list API permissions")
		return
	}
	success(c, "success", permissions)
}

func (h *ExternalAccessHandler) ListApplications(c *gin.Context) {
	applications, err := h.store.ListAPIApplications()
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list external applications")
		return
	}
	success(c, "success", applications)
}

func (h *ExternalAccessHandler) GetApplication(c *gin.Context) {
	detail, err := h.store.GetAPIApplication(parseUint64(c.Param("id")))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		notFound(c, "external application not found")
		return
	}
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to load external application")
		return
	}
	success(c, "success", detail)
}

func (h *ExternalAccessHandler) CreateApplication(c *gin.Context) {
	var req externalApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid application data")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		badRequest(c, ErrCodeParamMissing, "name is required")
		return
	}
	if len(req.PermissionCodes) == 0 {
		badRequest(c, ErrCodeParamMissing, "at least one permission is required")
		return
	}
	expiresAt, err := parseCredentialExpiry(req.ExpiresAt)
	if err != nil {
		badRequest(c, ErrCodeParamInvalid, err.Error())
		return
	}
	credentialName := strings.TrimSpace(req.CredentialName)
	if credentialName == "" {
		credentialName = "默认凭证"
	}
	issued, err := service.IssueAPICredential(0, credentialName, expiresAt)
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to issue API credential")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	app := model.APIApplication{Name: req.Name, Description: strings.TrimSpace(req.Description), Enabled: enabled}
	if err := h.store.CreateAPIApplication(&app, req.PermissionCodes, &issued.Credential); err != nil {
		badRequest(c, ErrCodeBusiness, "failed to create external application: "+err.Error())
		return
	}
	detail, err := h.store.GetAPIApplication(app.ID)
	if err != nil {
		serverError(c, ErrCodeInternal, "application created but failed to reload it")
		return
	}
	created(c, "external application created", gin.H{"application": detail, "token": issued.Token})
}

func (h *ExternalAccessHandler) UpdateApplication(c *gin.Context) {
	id := parseUint64(c.Param("id"))
	detail, err := h.store.GetAPIApplication(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		notFound(c, "external application not found")
		return
	}
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to load external application")
		return
	}
	var req externalApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid application data")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		badRequest(c, ErrCodeParamMissing, "name is required")
		return
	}
	if len(req.PermissionCodes) == 0 {
		badRequest(c, ErrCodeParamMissing, "at least one permission is required")
		return
	}
	detail.Name = name
	detail.Description = strings.TrimSpace(req.Description)
	if req.Enabled != nil {
		detail.Enabled = *req.Enabled
	}
	if err := h.store.UpdateAPIApplication(&detail.APIApplication, req.PermissionCodes); err != nil {
		badRequest(c, ErrCodeBusiness, "failed to update external application: "+err.Error())
		return
	}
	updated, _ := h.store.GetAPIApplication(id)
	success(c, "external application updated", updated)
}

func (h *ExternalAccessHandler) CreateCredential(c *gin.Context) {
	applicationID := parseUint64(c.Param("id"))
	var req credentialRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid credential data")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		badRequest(c, ErrCodeParamMissing, "name is required")
		return
	}
	expiresAt, err := parseCredentialExpiry(req.ExpiresAt)
	if err != nil {
		badRequest(c, ErrCodeParamInvalid, err.Error())
		return
	}
	issued, err := service.IssueAPICredential(applicationID, name, expiresAt)
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to issue API credential")
		return
	}
	if err := h.store.CreateAPICredential(&issued.Credential); errors.Is(err, gorm.ErrRecordNotFound) {
		notFound(c, "external application not found")
		return
	} else if err != nil {
		badRequest(c, ErrCodeBusiness, "failed to create credential: "+err.Error())
		return
	}
	created(c, "credential created", gin.H{"credential": issued.Credential, "token": issued.Token})
}

func (h *ExternalAccessHandler) RevokeCredential(c *gin.Context) {
	err := h.store.RevokeAPICredential(parseUint64(c.Param("id")), parseUint64(c.Param("credential_id")))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		notFound(c, "credential not found")
		return
	}
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to revoke credential")
		return
	}
	success(c, "credential revoked", nil)
}

func (h *ExternalAccessHandler) DeleteCredential(c *gin.Context) {
	err := h.store.DeleteAPICredential(parseUint64(c.Param("id")), parseUint64(c.Param("credential_id")))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		notFound(c, "credential not found")
		return
	}
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to delete credential")
		return
	}
	success(c, "credential deleted", nil)
}

func (h *ExternalAccessHandler) ListAccessLogs(c *gin.Context) {
	page := parsePageValue(c.Query("page"), 1)
	size := parsePageValue(c.Query("size"), 20)
	logs, total, err := h.store.ListAPIAccessLogs(parseUint64(c.Param("id")), page, size)
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list access logs")
		return
	}
	success(c, "success", gin.H{"page": page, "size": size, "total": total, "items": logs})
}

func (h *ExternalAccessHandler) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.GET("/api-permissions", h.ListPermissions)
	r.GET("/external-applications", h.ListApplications)
	r.POST("/external-applications", h.CreateApplication)
	r.GET("/external-applications/:id", h.GetApplication)
	r.PUT("/external-applications/:id", h.UpdateApplication)
	r.POST("/external-applications/:id/credentials", h.CreateCredential)
	r.DELETE("/external-applications/:id/credentials/:credential_id", h.DeleteCredential)
	r.POST("/external-applications/:id/credentials/:credential_id/revoke", h.RevokeCredential)
	r.GET("/external-applications/:id/logs", h.ListAccessLogs)
}

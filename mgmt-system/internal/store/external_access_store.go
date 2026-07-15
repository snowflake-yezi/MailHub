package store

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidAPICredential = errors.New("invalid API credential")

type APIApplicationDetail struct {
	model.APIApplication
	PermissionCodes []string              `json:"permission_codes"`
	Credentials     []model.APICredential `json:"credentials"`
	LastUsedAt      *time.Time            `json:"last_used_at,omitempty"`
}

type APIPermissionDetail struct {
	model.APIPermission
	Resources []model.APIResource `json:"resources"`
}

type AuthenticatedAPIClient struct {
	Application model.APIApplication
	Credential  model.APICredential
	Permissions []string
}

func normalizePermissionCodes(codes []string) []string {
	seen := make(map[string]struct{}, len(codes))
	result := make([]string, 0, len(codes))
	for _, code := range codes {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		result = append(result, code)
	}
	sort.Strings(result)
	return result
}

func (s *Store) SyncAPIRegistry(permissions []model.APIPermission, resources []model.APIResource) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.APIPermission{}).Where("active = ?", true).Update("active", false).Error; err != nil {
			return err
		}
		for i := range permissions {
			permission := permissions[i]
			permission.Active = true
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "code"}},
				DoUpdates: clause.AssignmentColumns([]string{"group_name", "name", "description", "sort_order", "active", "updated_at"}),
			}).Create(&permission).Error; err != nil {
				return fmt.Errorf("sync permission %s: %w", permission.Code, err)
			}
		}

		if err := tx.Model(&model.APIResource{}).Where("status = ?", "active").Update("status", "retired").Error; err != nil {
			return err
		}
		for i := range resources {
			resource := resources[i]
			resource.Status = "active"
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "method"}, {Name: "path"}},
				DoUpdates: clause.AssignmentColumns([]string{"permission_code", "name", "status", "updated_at"}),
			}).Create(&resource).Error; err != nil {
				return fmt.Errorf("sync API resource %s %s: %w", resource.Method, resource.Path, err)
			}
		}
		return nil
	})
}

func (s *Store) ListAPIPermissions() ([]APIPermissionDetail, error) {
	var permissions []model.APIPermission
	if err := s.db.Where("active = ?", true).Order("sort_order ASC, code ASC").Find(&permissions).Error; err != nil {
		return nil, err
	}
	result := make([]APIPermissionDetail, 0, len(permissions))
	for _, permission := range permissions {
		var resources []model.APIResource
		if err := s.db.Where("permission_code = ? AND status = ?", permission.Code, "active").
			Order("method ASC, path ASC").Find(&resources).Error; err != nil {
			return nil, err
		}
		result = append(result, APIPermissionDetail{APIPermission: permission, Resources: resources})
	}
	return result, nil
}

func (s *Store) validatePermissionCodes(tx *gorm.DB, codes []string) error {
	if len(codes) == 0 {
		return nil
	}
	var count int64
	if err := tx.Model(&model.APIPermission{}).Where("code IN ? AND active = ?", codes, true).Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(codes)) {
		return fmt.Errorf("one or more permission codes are invalid")
	}
	return nil
}

func replaceApplicationPermissions(tx *gorm.DB, applicationID uint64, codes []string) error {
	if err := tx.Where("application_id = ?", applicationID).Delete(&model.APIApplicationPermission{}).Error; err != nil {
		return err
	}
	for _, code := range codes {
		grant := model.APIApplicationPermission{ApplicationID: applicationID, PermissionCode: code}
		if err := tx.Create(&grant).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateAPIApplication(app *model.APIApplication, codes []string, credential *model.APICredential) error {
	codes = normalizePermissionCodes(codes)
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.validatePermissionCodes(tx, codes); err != nil {
			return err
		}
		if err := tx.Create(app).Error; err != nil {
			return err
		}
		if err := replaceApplicationPermissions(tx, app.ID, codes); err != nil {
			return err
		}
		if credential != nil {
			credential.ApplicationID = app.ID
			if err := tx.Create(credential).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) UpdateAPIApplication(app *model.APIApplication, codes []string) error {
	codes = normalizePermissionCodes(codes)
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.validatePermissionCodes(tx, codes); err != nil {
			return err
		}
		var existing model.APIApplication
		if err := tx.Select("id").First(&existing, app.ID).Error; err != nil {
			return err
		}
		result := tx.Model(&model.APIApplication{}).Where("id = ?", app.ID).Updates(map[string]interface{}{
			"name": app.Name, "description": app.Description, "enabled": app.Enabled,
		})
		if result.Error != nil {
			return result.Error
		}
		return replaceApplicationPermissions(tx, app.ID, codes)
	})
}

func (s *Store) getAPIApplicationDetail(id uint64) (*APIApplicationDetail, error) {
	var app model.APIApplication
	if err := s.db.First(&app, id).Error; err != nil {
		return nil, err
	}
	var codes []string
	if err := s.db.Model(&model.APIApplicationPermission{}).Where("application_id = ?", id).
		Order("permission_code ASC").Pluck("permission_code", &codes).Error; err != nil {
		return nil, err
	}
	var credentials []model.APICredential
	if err := s.db.Where("application_id = ?", id).Order("created_at DESC").Find(&credentials).Error; err != nil {
		return nil, err
	}
	var lastUsed *time.Time
	for i := range credentials {
		if credentials[i].LastUsedAt != nil && (lastUsed == nil || credentials[i].LastUsedAt.After(*lastUsed)) {
			value := *credentials[i].LastUsedAt
			lastUsed = &value
		}
	}
	return &APIApplicationDetail{APIApplication: app, PermissionCodes: codes, Credentials: credentials, LastUsedAt: lastUsed}, nil
}

func (s *Store) GetAPIApplication(id uint64) (*APIApplicationDetail, error) {
	return s.getAPIApplicationDetail(id)
}

func (s *Store) ListAPIApplications() ([]APIApplicationDetail, error) {
	var apps []model.APIApplication
	if err := s.db.Order("created_at DESC").Find(&apps).Error; err != nil {
		return nil, err
	}
	result := make([]APIApplicationDetail, 0, len(apps))
	for _, app := range apps {
		detail, err := s.getAPIApplicationDetail(app.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, *detail)
	}
	return result, nil
}

func (s *Store) CreateAPICredential(credential *model.APICredential) error {
	var count int64
	if err := s.db.Model(&model.APIApplication{}).Where("id = ?", credential.ApplicationID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	return s.db.Create(credential).Error
}

func (s *Store) RevokeAPICredential(applicationID, credentialID uint64) error {
	result := s.db.Model(&model.APICredential{}).
		Where("id = ? AND application_id = ?", credentialID, applicationID).
		Update("enabled", false)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store) AuthenticateAPICredential(tokenHash string, now time.Time) (*AuthenticatedAPIClient, error) {
	var credential model.APICredential
	if err := s.db.Preload("Application").Where("token_hash = ? AND enabled = ?", tokenHash, true).First(&credential).Error; err != nil {
		return nil, ErrInvalidAPICredential
	}
	if credential.Application == nil || !credential.Application.Enabled {
		return nil, ErrInvalidAPICredential
	}
	if credential.ExpiresAt != nil && !credential.ExpiresAt.After(now) {
		return nil, ErrInvalidAPICredential
	}
	var permissions []string
	if err := s.db.Model(&model.APIApplicationPermission{}).
		Where("application_id = ?", credential.ApplicationID).
		Pluck("permission_code", &permissions).Error; err != nil {
		return nil, err
	}
	return &AuthenticatedAPIClient{Application: *credential.Application, Credential: credential, Permissions: permissions}, nil
}

func (s *Store) UpdateAPICredentialUsage(id uint64, usedAt time.Time, clientIP string) {
	s.db.Model(&model.APICredential{}).Where("id = ?", id).Updates(map[string]interface{}{
		"last_used_at": usedAt, "last_used_ip": clientIP,
	})
}

func (s *Store) CreateAPIAccessLog(entry *model.APIAccessLog) {
	_ = s.db.Create(entry).Error
}

func (s *Store) ListAPIAccessLogs(applicationID uint64, page, size int) ([]model.APIAccessLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	query := s.db.Model(&model.APIAccessLog{}).Where("application_id = ?", applicationID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []model.APIAccessLog
	if err := query.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// MigrateLegacyAPITokens makes configured legacy callers visible in the new console.
// The legacy plaintext row remains only for rollback compatibility.
func (s *Store) MigrateLegacyAPITokens(hashToken func(string) string) error {
	var tokens []model.ApiToken
	if err := s.db.Where("enabled = ?", true).Find(&tokens).Error; err != nil {
		return err
	}
	for _, token := range tokens {
		token := token
		if err := s.db.Transaction(func(tx *gorm.DB) error {
			var count int64
			if err := tx.Model(&model.APIApplication{}).Where("legacy_token_id = ?", token.ID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return nil
			}
			name := token.Name
			if err := tx.Model(&model.APIApplication{}).Where("name = ?", name).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				name = fmt.Sprintf("%s（迁移 %d）", token.Name, token.ID)
			}
			app := model.APIApplication{Name: name, Description: "由旧 api_tokens 自动迁移", Enabled: true, LegacyTokenID: &token.ID}
			if err := tx.Create(&app).Error; err != nil {
				return err
			}
			codes := LegacyScopesToPermissions(token.Scopes)
			if len(codes) == 1 && codes[0] == "*" {
				if err := tx.Model(&model.APIPermission{}).Where("active = ?", true).
					Order("sort_order ASC").Pluck("code", &codes).Error; err != nil {
					return err
				}
			}
			if err := replaceApplicationPermissions(tx, app.ID, codes); err != nil {
				return err
			}
			prefix := token.Token
			if len(prefix) > 16 {
				prefix = prefix[:16]
			}
			credential := model.APICredential{
				ApplicationID: app.ID, Name: "迁移凭证", TokenPrefix: prefix,
				TokenHash: hashToken(token.Token), Enabled: true, LastUsedAt: token.LastUsedAt,
			}
			return tx.Create(&credential).Error
		}); err != nil {
			return fmt.Errorf("migrate legacy API token %d: %w", token.ID, err)
		}
	}
	return nil
}

func LegacyScopesToPermissions(scopes string) []string {
	result := make([]string, 0, 6)
	for _, scope := range strings.Split(scopes, ",") {
		switch strings.TrimSpace(scope) {
		case "*":
			return []string{"*"}
		case "mailbox:create":
			result = append(result, "mailbox:create", "mailbox:disable")
		case "mailbox:read":
			result = append(result, "mailbox:read")
		case "email:read":
			result = append(result, "email:list", "email:body", "email:attachment")
		}
	}
	return normalizePermissionCodes(result)
}

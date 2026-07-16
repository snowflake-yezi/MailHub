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

// LegacyAPITokenSeed is accepted only as an upgrade input. Its plaintext Token
// is hashed into api_credentials and is never written back to the database.
type LegacyAPITokenSeed struct {
	Name   string
	Token  string
	Scopes string
}

type legacyAPITokenRow struct {
	ID         uint64
	Name       string
	Token      string
	Scopes     string
	Enabled    bool
	LastUsedAt *time.Time
}

type legacyAPITokenCandidate struct {
	LegacyAPITokenSeed
	LastUsedAt *time.Time
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

func (s *Store) DeleteAPICredential(applicationID, credentialID uint64) error {
	result := s.db.Where("id = ? AND application_id = ?", credentialID, applicationID).
		Delete(&model.APICredential{})
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

// RetireLegacyAPITokens imports active plaintext credentials into the hashed
// credential store, verifies every import, and then removes api_tokens.
func (s *Store) RetireLegacyAPITokens(configured []LegacyAPITokenSeed, hashToken func(string) string) error {
	candidates, tableExists, err := s.legacyAPITokenCandidates(configured)
	if err != nil {
		return err
	}
	if !tableExists {
		for _, seed := range configured {
			tokenHash, err := legacyTokenHash(seed.Token, hashToken)
			if err != nil {
				return err
			}
			var count int64
			if err := s.db.Model(&model.APICredential{}).Where("token_hash = ?", tokenHash).Count(&count).Error; err != nil {
				return fmt.Errorf("verify retired auth.tokens credential: %w", err)
			}
			if count != 1 {
				return fmt.Errorf("auth.tokens is retired; create credential %q in External Access and remove it from config", seed.Name)
			}
		}
		return nil
	}

	hashes := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		tokenHash, err := s.importLegacyAPIToken(candidate, hashToken)
		if err != nil {
			return err
		}
		hashes[tokenHash] = struct{}{}
	}
	for tokenHash := range hashes {
		var count int64
		if err := s.db.Model(&model.APICredential{}).Where("token_hash = ?", tokenHash).Count(&count).Error; err != nil {
			return fmt.Errorf("verify imported API credential: %w", err)
		}
		if count != 1 {
			return fmt.Errorf("verify imported API credential: hash %s has %d rows", tokenHash[:12], count)
		}
	}

	if tableExists {
		if err := s.db.Migrator().DropTable("api_tokens"); err != nil {
			return fmt.Errorf("drop legacy api_tokens table: %w", err)
		}
	}
	return nil
}

func (s *Store) legacyAPITokenCandidates(configured []LegacyAPITokenSeed) ([]legacyAPITokenCandidate, bool, error) {
	var tableCount int64
	if err := s.db.Raw(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?",
		"api_tokens",
	).Scan(&tableCount).Error; err != nil {
		return nil, false, fmt.Errorf("inspect legacy api_tokens table: %w", err)
	}
	tableExists := tableCount > 0
	if !tableExists {
		return nil, false, nil
	}
	knownLegacyTokens := make(map[string]bool)
	candidates := make([]legacyAPITokenCandidate, 0, len(configured))
	if tableExists {
		var rows []legacyAPITokenRow
		if err := s.db.Table("api_tokens").Find(&rows).Error; err != nil {
			return nil, true, fmt.Errorf("read legacy api_tokens: %w", err)
		}
		for _, row := range rows {
			knownLegacyTokens[row.Token] = row.Enabled
			if row.Enabled {
				candidates = append(candidates, legacyAPITokenCandidate{
					LegacyAPITokenSeed: LegacyAPITokenSeed{Name: row.Name, Token: row.Token, Scopes: row.Scopes},
					LastUsedAt:         row.LastUsedAt,
				})
			}
		}
	}
	for _, seed := range configured {
		if _, existedInLegacyTable := knownLegacyTokens[seed.Token]; existedInLegacyTable {
			continue
		}
		candidates = append(candidates, legacyAPITokenCandidate{LegacyAPITokenSeed: seed})
	}
	return candidates, tableExists, nil
}

func (s *Store) importLegacyAPIToken(candidate legacyAPITokenCandidate, hashToken func(string) string) (string, error) {
	tokenHash, err := legacyTokenHash(candidate.Token, hashToken)
	if err != nil {
		return "", err
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		var credential model.APICredential
		err := tx.Select("id").Where("token_hash = ?", tokenHash).First(&credential).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		name, err := availableImportedApplicationName(tx, candidate.Name, tokenHash)
		if err != nil {
			return err
		}
		app := model.APIApplication{Name: name, Description: "由旧版明文 Token 一次性导入", Enabled: true}
		if err := tx.Create(&app).Error; err != nil {
			return err
		}
		codes := LegacyScopesToPermissions(candidate.Scopes)
		if len(codes) == 1 && codes[0] == "*" {
			if err := tx.Model(&model.APIPermission{}).Where("active = ?", true).
				Order("sort_order ASC").Pluck("code", &codes).Error; err != nil {
				return err
			}
		}
		if err := replaceApplicationPermissions(tx, app.ID, codes); err != nil {
			return err
		}
		credential = model.APICredential{
			ApplicationID: app.ID, Name: "导入凭证", TokenPrefix: truncateRunes(candidate.Token, 16),
			TokenHash: tokenHash, Enabled: true, LastUsedAt: candidate.LastUsedAt,
		}
		return tx.Create(&credential).Error
	})
	if err != nil {
		return "", fmt.Errorf("import legacy API token %q: %w", candidate.Name, err)
	}
	return tokenHash, nil
}

func legacyTokenHash(token string, hashToken func(string) string) (string, error) {
	if hashToken == nil || strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("legacy API token import requires a non-empty token and hash function")
	}
	tokenHash := hashToken(token)
	if len(tokenHash) < 12 {
		return "", fmt.Errorf("legacy API token import produced an invalid hash")
	}
	return tokenHash, nil
}

func availableImportedApplicationName(tx *gorm.DB, requested, tokenHash string) (string, error) {
	base := strings.TrimSpace(requested)
	if base == "" {
		base = "旧版外部调用方"
	}
	names := []string{truncateRunes(base, 128)}
	for _, hashLength := range []int{8, 16, 32, 64} {
		if hashLength > len(tokenHash) {
			continue
		}
		suffix := fmt.Sprintf("（导入 %s）", tokenHash[:hashLength])
		names = append(names, truncateRunes(base, 128-len([]rune(suffix)))+suffix)
	}
	for _, name := range names {
		var count int64
		if err := tx.Model(&model.APIApplication{}).Where("name = ?", name).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return name, nil
		}
	}
	return "", fmt.Errorf("cannot allocate a unique imported application name")
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
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

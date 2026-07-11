package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const adminBootstrapKey = "admin_bootstrap"

var (
	ErrAdminNotFound      = errors.New("admin user not found")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAlreadyInitialized = errors.New("admin bootstrap already completed")
	ErrNotInitialized     = errors.New("admin bootstrap not completed")
)

type AdminCredentialService struct {
	db   *gorm.DB
	mode string
}

type AccountUpdate struct {
	Username        string
	CurrentPassword string
	NewPassword     string
}

func NewAdminCredentialService(s *store.Store, mode string) *AdminCredentialService {
	return &AdminCredentialService{db: s.DB(), mode: mode}
}

func (s *AdminCredentialService) IsBootstrapped() (bool, error) {
	var state model.SystemState
	err := s.db.Where("`key` = ?", adminBootstrapKey).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil && state.Value == "completed", err
}

func (s *AdminCredentialService) Bootstrap(username, password string, mustChange bool) (*model.AdminUser, error) {
	return s.bootstrap(username, password, mustChange, true)
}

// BootstrapLegacy migrates the explicit legacy config credential once. It is
// intentionally separate from normal bootstrap so release password policy is
// never weakened for new installations or password resets.
func (s *AdminCredentialService) BootstrapLegacy(username, password string) (*model.AdminUser, error) {
	return s.bootstrap(username, password, true, false)
}

func (s *AdminCredentialService) bootstrap(username, password string, mustChange, enforcePolicy bool) (*model.AdminUser, error) {
	username = strings.TrimSpace(username)
	if enforcePolicy {
		if err := ValidateAdminPassword(username, password, s.mode); err != nil {
			return nil, err
		}
	} else if username == "" || password == "" {
		return nil, errors.New("legacy username and password are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	var created model.AdminUser
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var state model.SystemState
		stateErr := tx.Where("`key` = ?", adminBootstrapKey).First(&state).Error
		if stateErr == nil && state.Value == "completed" {
			return ErrAlreadyInitialized
		}
		if stateErr != nil && !errors.Is(stateErr, gorm.ErrRecordNotFound) {
			return stateErr
		}

		now := time.Now()
		created = model.AdminUser{
			Username: username, PasswordHash: string(hash), PasswordAlgo: "bcrypt",
			MustChangePassword: mustChange, CredentialVersion: 1, Status: "active",
			PasswordChangedAt: &now,
		}
		if err := tx.Create(&created).Error; err != nil {
			return fmt.Errorf("create admin user: %w", err)
		}
		return tx.Save(&model.SystemState{Key: adminBootstrapKey, Value: "completed"}).Error
	})
	return &created, err
}

func (s *AdminCredentialService) Verify(username, password string) (*model.AdminUser, error) {
	var user model.AdminUser
	if err := s.db.Where("username = ?", strings.TrimSpace(username)).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Keep the slow path for unknown users close to bcrypt-backed verification.
			_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoO5M8DgSqHFSfGQmAEVYxQp77uYzWkGfa"), []byte(password))
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if user.Status != "active" {
		return nil, ErrInvalidCredentials
	}
	if user.PasswordAlgo != "bcrypt" || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	return &user, nil
}

func (s *AdminCredentialService) GetByID(id uint64) (*model.AdminUser, error) {
	var user model.AdminUser
	if err := s.db.First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdminNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (s *AdminCredentialService) ResetPassword(username, password string, mustChange bool) error {
	bootstrapped, err := s.IsBootstrapped()
	if err != nil {
		return err
	}
	if !bootstrapped {
		return ErrNotInitialized
	}
	var user model.AdminUser
	if err := s.db.Where("username = ?", strings.TrimSpace(username)).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAdminNotFound
		}
		return err
	}
	if err := ValidateAdminPassword(user.Username, password, s.mode); err != nil {
		return err
	}
	return s.setPassword(&user, password, mustChange)
}

func (s *AdminCredentialService) UpdateAccount(userID uint64, update AccountUpdate) (*model.AdminUser, error) {
	user, err := s.GetByID(userID)
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(update.CurrentPassword)) != nil {
		return nil, ErrInvalidCredentials
	}
	username := strings.TrimSpace(update.Username)
	if username == "" {
		username = user.Username
	}
	if update.NewPassword != "" {
		if err := ValidateAdminPassword(username, update.NewPassword, s.mode); err != nil {
			return nil, err
		}
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]interface{}{"username": username}
		credentialsChanged := username != user.Username
		if update.NewPassword != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(update.NewPassword), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			now := time.Now()
			updates["password_hash"] = string(hash)
			updates["password_algo"] = "bcrypt"
			updates["password_changed_at"] = &now
			updates["must_change_password"] = false
			credentialsChanged = true
		}
		if credentialsChanged {
			updates["credential_version"] = gorm.Expr("credential_version + 1")
		}
		return tx.Model(&model.AdminUser{}).Where("id = ?", userID).Updates(updates).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetByID(userID)
}

func (s *AdminCredentialService) setPassword(user *model.AdminUser, password string, mustChange bool) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now()
	return s.db.Model(&model.AdminUser{}).Where("id = ?", user.ID).Updates(map[string]interface{}{
		"password_hash": string(hash), "password_algo": "bcrypt",
		"credential_version":   gorm.Expr("credential_version + 1"),
		"must_change_password": mustChange, "password_changed_at": &now,
	}).Error
}

func ValidateAdminPassword(username, password, mode string) error {
	if password == "" {
		return errors.New("password is required")
	}
	if mode != "release" {
		return nil
	}
	if len([]rune(password)) < 12 {
		return errors.New("password must be at least 12 characters")
	}
	lower := strings.ToLower(password)
	weak := map[string]bool{"admin": true, "password": true, "123456": true, "changeme": true, "change-me-admin-password": true}
	if weak[lower] || strings.EqualFold(strings.TrimSpace(username), password) {
		return errors.New("password is too weak")
	}
	return nil
}

package service

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func newMockCredentialService(t *testing.T, mode string) (*AdminCredentialService, sqlmock.Sqlmock, func()) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm: %v", err)
	}
	return &AdminCredentialService{db: db, mode: mode}, mock, func() { _ = sqlDB.Close() }
}

func TestAdminVerifyUsesBcryptAndRejectsInvalidCredentials(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("StrongPassword!2026"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, status, password string
		wantErr                bool
	}{
		{name: "valid", status: "active", password: "StrongPassword!2026"},
		{name: "wrong password", status: "active", password: "wrong", wantErr: true},
		{name: "disabled", status: "disabled", password: "StrongPassword!2026", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mock, cleanup := newMockCredentialService(t, "release")
			defer cleanup()
			rows := sqlmock.NewRows([]string{"id", "username", "password_hash", "password_algo", "credential_version", "status", "created_at", "updated_at"}).
				AddRow(7, "admin", string(hash), "bcrypt", 2, tt.status, time.Now(), time.Now())
			mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `admin_users` WHERE username = ? ORDER BY `admin_users`.`id` LIMIT ?")).
				WithArgs("admin", 1).WillReturnRows(rows)
			user, err := svc.Verify("admin", tt.password)
			if tt.wantErr && !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("error = %v", err)
			}
			if !tt.wantErr && (err != nil || user.ID != 7) {
				t.Fatalf("user=%#v error=%v", user, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAdminBootstrapDoesNotOverwriteCompletedState(t *testing.T) {
	svc, mock, cleanup := newMockCredentialService(t, "release")
	defer cleanup()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `system_state` WHERE `key` = ? ORDER BY `system_state`.`key` LIMIT ?")).
		WithArgs(adminBootstrapKey, 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updated_at"}).AddRow(adminBootstrapKey, "completed", time.Now()))
	mock.ExpectRollback()
	_, err := svc.Bootstrap("admin", "StrongPassword!2026", false)
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAdminPasswordPolicy(t *testing.T) {
	for _, password := range []string{"", "short", "CHANGE-ME-ADMIN-PASSWORD", "admin"} {
		if err := ValidateAdminPassword("admin", password, "release"); err == nil {
			t.Fatalf("release accepted weak password %q", password)
		}
	}
	if err := ValidateAdminPassword("admin", "short", "debug"); err != nil {
		t.Fatalf("debug rejected explicit password: %v", err)
	}
}

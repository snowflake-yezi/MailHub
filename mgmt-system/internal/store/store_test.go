package store

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestRestoreMailbox(t *testing.T) {
	st, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `mailbox_accounts` SET `delete_requested_at`=?,`recycled_at`=?,`status`=?,`sync_status`=?,`updated_at`=? WHERE id = ? AND status = ?")).
		WithArgs(nil, nil, "active", "synced", sqlmock.AnyArg(), uint64(42), "soft_deleted").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := st.RestoreMailbox(42); err != nil {
		t.Fatalf("RestoreMailbox() error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestRestoreMailboxRejectsNonSoftDeleted(t *testing.T) {
	st, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `mailbox_accounts` SET `delete_requested_at`=?,`recycled_at`=?,`status`=?,`sync_status`=?,`updated_at`=? WHERE id = ? AND status = ?")).
		WithArgs(nil, nil, "active", "synced", sqlmock.AnyArg(), uint64(42), "soft_deleted").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	if err := st.RestoreMailbox(42); !errors.Is(err, ErrInvalidMailboxRestoreState) {
		t.Fatalf("RestoreMailbox() error = %v, want ErrInvalidMailboxRestoreState", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func newMockStore(t *testing.T) (*Store, sqlmock.Sqlmock, func()) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		sqlDB.Close()
		t.Fatalf("gorm open: %v", err)
	}
	return &Store{db: db}, mock, func() { sqlDB.Close() }
}

func TestCountMailboxesOnServerDomainIgnoresPurgedHistory(t *testing.T) {
	st, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `mailbox_accounts` WHERE server_id = \\? AND domain_id = \\? AND status <> \\?").
		WithArgs(uint64(7), uint64(11), "purged").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	count, err := st.CountMailboxesOnServerDomain(7, 11)
	if err != nil {
		t.Fatalf("CountMailboxesOnServerDomain() error: %v", err)
	}
	if count != 2 {
		t.Fatalf("CountMailboxesOnServerDomain() = %d, want 2", count)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestGetActiveServerDomainRequiresPairAndActiveStatus(t *testing.T) {
	st, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery("SELECT \\* FROM `server_domains` WHERE server_id = \\? AND domain_id = \\? AND status = \\?").
		WithArgs(uint64(7), uint64(11), "active", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "server_id", "domain_id", "status"}))

	_, err := st.GetActiveServerDomain(7, 11)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("GetActiveServerDomain() error = %v, want gorm.ErrRecordNotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestMarkServerDomainRemovedRequiresActiveBinding(t *testing.T) {
	st, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `server_domains` SET .* WHERE server_id = \\? AND domain_id = \\? AND status = \\?").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := st.MarkServerDomainRemoved(7, 11)
	if !errors.Is(err, ErrServerDomainBindingNotActive) {
		t.Fatalf("MarkServerDomainRemoved() error = %v, want ErrServerDomainBindingNotActive", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestDefaultConfigReloadabilityMatchesRuntimeBehavior(t *testing.T) {
	configs := make(map[string]seedConfig)
	for _, cfg := range defaultConfigs() {
		configs[cfg.Key] = cfg
	}
	for _, key := range []string{
		"forward.scan_interval",
		"forward.max_email_size",
		"forward.body_preview_size",
		"filter.sync_interval",
		"lifecycle.trash_retention_hours",
		"lifecycle.message_retention_days",
		"lifecycle.gc_interval_minutes",
		"lifecycle.drain_timeout_minutes",
		"lifecycle.drain_poll_interval_ms",
	} {
		if !configs[key].Reloadable {
			t.Fatalf("%s must be reloadable after NC-P2 runtime apply support", key)
		}
	}
}

func TestDefaultConfigsSeedCredentialRotationOverlap(t *testing.T) {
	for _, cfg := range defaultConfigs() {
		if cfg.Key != "node.credential_rotation_overlap_minutes" {
			continue
		}
		if cfg.Value != "30" || cfg.Default != "30" || cfg.Type != "int" || cfg.Category != "node" || !cfg.Reloadable {
			t.Fatalf("credential rotation overlap seed = %#v", cfg)
		}
		return
	}
	t.Fatal("credential rotation overlap seed missing")
}

func TestDefaultConfigVerifiesSMTPTLSCertificates(t *testing.T) {
	for _, cfg := range defaultConfigs() {
		if cfg.Key == "forward.tls_insecure_skip" {
			if cfg.Value != "false" || cfg.Default != "false" {
				t.Fatalf("TLS insecure seed = value %q default %q, want false/false", cfg.Value, cfg.Default)
			}
			return
		}
	}
	t.Fatal("forward.tls_insecure_skip seed missing")
}

func TestDefaultConfigsSeedFilterRuntimeSafetyDefaults(t *testing.T) {
	configs := make(map[string]seedConfig)
	for _, cfg := range defaultConfigs() {
		configs[cfg.Key] = cfg
	}
	tests := map[string]struct {
		value     string
		valueType string
	}{
		"filter.engine_mode":             {value: "legacy", valueType: "string"},
		"filter.auto_quarantine_enabled": {value: "false", valueType: "bool"},
	}
	for key, want := range tests {
		cfg, ok := configs[key]
		if !ok {
			t.Fatalf("%s seed missing", key)
		}
		if cfg.Value != want.value || cfg.Default != want.value || cfg.Type != want.valueType || !cfg.Reloadable {
			t.Fatalf("%s seed = %#v, want value/default %q, type %q, reloadable", key, cfg, want.value, want.valueType)
		}
	}
}

func TestDefaultConfigsSeedMIMEProjectorDefaults(t *testing.T) {
	configs := make(map[string]seedConfig)
	for _, cfg := range defaultConfigs() {
		configs[cfg.Key] = cfg
	}
	for key, want := range map[string]string{
		"mime.body_projector_mode": "legacy",
		"mime.max_message_bytes":   "26214400",
	} {
		cfg, ok := configs[key]
		if !ok || cfg.Value != want || cfg.Default != want || !cfg.Reloadable {
			t.Fatalf("%s seed = %#v, want value/default %q and reloadable", key, cfg, want)
		}
	}
}

func TestBumpAllServerDesiredRevisions(t *testing.T) {
	st, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `mail_servers` SET .*`boot_id_at_change`=last_boot_id.*`desired_revision`=desired_revision \\+ 1.*WHERE 1 = 1").
		WithArgs(sqlmock.AnyArg(), "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	if err := st.BumpAllServerDesiredRevisions(nil); err != nil {
		t.Fatalf("BumpAllServerDesiredRevisions() error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

// TestListMailboxesExcludeStatuses 验证回收站分离：normal 视图排除 soft_deleted/purged。
// 用宽松正则只断言 WHERE 子句含 NOT IN，避免对 gorm 完整 SELECT 的脆弱依赖；
// 空结果不触发 Preload，故只匹配 Count + Find 两条。
func TestListMailboxesExcludeStatuses(t *testing.T) {
	st, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery("status NOT IN").
		WithArgs("soft_deleted", "purged").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("status NOT IN").
		WithArgs("soft_deleted", "purged", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email_address"}))

	list, total, err := st.ListMailboxesWithFilter(1, 20, MailboxListFilter{
		ExcludeStatuses: []string{"soft_deleted", "purged"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("expected empty, got total=%d list=%d", total, len(list))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestListMailboxesStatuses 验证回收站视图：只查 soft_deleted + purged（IN）。
func TestListMailboxesStatuses(t *testing.T) {
	st, mock, cleanup := newMockStore(t)
	defer cleanup()

	mock.ExpectQuery("status IN").
		WithArgs("soft_deleted", "purged").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("status IN").
		WithArgs("soft_deleted", "purged", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email_address"}))

	list, total, err := st.ListMailboxesWithFilter(1, 20, MailboxListFilter{
		Statuses: []string{"soft_deleted", "purged"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("expected empty, got total=%d list=%d", total, len(list))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

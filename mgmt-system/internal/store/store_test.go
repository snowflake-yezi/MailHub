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

func TestDefaultConfigReloadabilityMatchesRuntimeBehavior(t *testing.T) {
	configs := make(map[string]seedConfig)
	for _, cfg := range defaultConfigs() {
		configs[cfg.Key] = cfg
	}
	if configs["forward.scan_interval"].Reloadable {
		t.Fatal("forward.scan_interval must remain non-reloadable until its ticker can be rebuilt")
	}
	if !configs["lifecycle.trash_retention_hours"].Reloadable {
		t.Fatal("lifecycle.trash_retention_hours must be reloadable")
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

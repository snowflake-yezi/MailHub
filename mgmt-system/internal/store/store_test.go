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

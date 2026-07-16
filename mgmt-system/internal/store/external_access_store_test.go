package store

import (
	"reflect"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestLegacyAPITokenCandidatesDoNotResurrectDisabledRows(t *testing.T) {
	s, mock, closeDB := newMockStore(t)
	defer closeDB()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?")).
		WithArgs("api_tokens").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `api_tokens`")).
		WithArgs().WillReturnRows(sqlmock.NewRows([]string{"id", "name", "token", "scopes", "enabled", "last_used_at"}).
		AddRow(1, "active", "active-token", "email:read", true, nil).
		AddRow(2, "disabled", "disabled-token", "*", false, nil))

	candidates, exists, err := s.legacyAPITokenCandidates([]LegacyAPITokenSeed{
		{Name: "disabled-config", Token: "disabled-token", Scopes: "*"},
		{Name: "config-only", Token: "config-token", Scopes: "mailbox:read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("legacy table should exist")
	}
	if len(candidates) != 2 || candidates[0].Token != "active-token" || candidates[1].Token != "config-token" {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImportLegacyAPITokenIsIdempotent(t *testing.T) {
	s, mock, closeDB := newMockStore(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT `id` FROM `api_credentials` WHERE token_hash = \\? ORDER BY `api_credentials`.`id` LIMIT \\?").
		WithArgs("hashed-token", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectCommit()

	hash, err := s.importLegacyAPIToken(legacyAPITokenCandidate{
		LegacyAPITokenSeed: LegacyAPITokenSeed{Name: "legacy", Token: "plaintext", Scopes: "*"},
	}, func(string) string { return "hashed-token" })
	if err != nil || hash != "hashed-token" {
		t.Fatalf("hash=%q err=%v", hash, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRetireLegacyAPITokensVerifiesBeforeDrop(t *testing.T) {
	s, mock, closeDB := newMockStore(t)
	defer closeDB()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?")).
		WithArgs("api_tokens").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `api_tokens`")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "token", "scopes", "enabled", "last_used_at"}).
			AddRow(1, "legacy", "plaintext", "email:read", true, nil))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT `id` FROM `api_credentials` WHERE token_hash = \\? ORDER BY `api_credentials`.`id` LIMIT \\?").
		WithArgs("hashed-token", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `api_credentials` WHERE token_hash = ?")).
		WithArgs("hashed-token").WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(0))

	err := s.RetireLegacyAPITokens(nil, func(string) string { return "hashed-token" })
	if err == nil {
		t.Fatal("verification failure must prevent dropping api_tokens")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRetireLegacyAPITokensDropsTableAfterVerification(t *testing.T) {
	s, mock, closeDB := newMockStore(t)
	defer closeDB()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?")).
		WithArgs("api_tokens").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `api_tokens`")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "token", "scopes", "enabled", "last_used_at"}).
			AddRow(1, "legacy", "plaintext", "email:read", true, nil))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT `id` FROM `api_credentials` WHERE token_hash = \\? ORDER BY `api_credentials`.`id` LIMIT \\?").
		WithArgs("hashed-token", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `api_credentials` WHERE token_hash = ?")).
		WithArgs("hashed-token").WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(1))
	mock.ExpectExec(regexp.QuoteMeta("SET FOREIGN_KEY_CHECKS = 0;")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DROP TABLE IF EXISTS .*api_tokens.*").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("SET FOREIGN_KEY_CHECKS = 1;")).WillReturnResult(sqlmock.NewResult(0, 0))

	if err := s.RetireLegacyAPITokens(nil, func(string) string { return "hashed-token" }); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRetiredAuthTokensCannotProvisionNewCredentials(t *testing.T) {
	for _, tc := range []struct {
		name      string
		count     int64
		wantError bool
	}{
		{name: "already imported", count: 1},
		{name: "new plaintext token rejected", count: 0, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock, closeDB := newMockStore(t)
			defer closeDB()
			mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?")).
				WithArgs("api_tokens").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `api_credentials` WHERE token_hash = ?")).
				WithArgs("hashed-token").WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(tc.count))

			err := s.RetireLegacyAPITokens([]LegacyAPITokenSeed{{Name: "legacy", Token: "plaintext"}}, func(string) string {
				return "hashed-token"
			})
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v wantError=%v", err, tc.wantError)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestImportLegacyAPITokenCreatesHashedCredential(t *testing.T) {
	s, mock, closeDB := newMockStore(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT `id` FROM `api_credentials` WHERE token_hash = \\? ORDER BY `api_credentials`.`id` LIMIT \\?").
		WithArgs("hashed-token", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT count(*) FROM `api_applications` WHERE name = ?")).
		WithArgs("legacy").WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(0))
	mock.ExpectExec("INSERT INTO `api_applications`").
		WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectExec("DELETE FROM `api_application_permissions` WHERE application_id = \\?").
		WithArgs(uint64(7)).WillReturnResult(sqlmock.NewResult(0, 0))
	for _, permission := range []string{"email:attachment", "email:body", "email:list"} {
		mock.ExpectExec("INSERT INTO `api_application_permissions`").
			WithArgs(uint64(7), permission, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
	}
	mock.ExpectExec("INSERT INTO `api_credentials`").
		WillReturnResult(sqlmock.NewResult(11, 1))
	mock.ExpectCommit()

	_, err := s.importLegacyAPIToken(legacyAPITokenCandidate{
		LegacyAPITokenSeed: LegacyAPITokenSeed{Name: "legacy", Token: "plaintext", Scopes: "email:read"},
	}, func(string) string { return "hashed-token" })
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyScopesToPermissions(t *testing.T) {
	tests := []struct {
		name   string
		scopes string
		want   []string
	}{
		{name: "ticket center", scopes: "mailbox:create,mailbox:read", want: []string{"mailbox:create", "mailbox:disable", "mailbox:read"}},
		{name: "email reader", scopes: "email:read", want: []string{"email:attachment", "email:body", "email:list"}},
		{name: "deduplicate", scopes: "email:read, email:read", want: []string{"email:attachment", "email:body", "email:list"}},
		{name: "wildcard", scopes: "mailbox:read,*", want: []string{"*"}},
		{name: "unknown ignored", scopes: "unknown", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LegacyScopesToPermissions(tt.scopes); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("LegacyScopesToPermissions(%q) = %#v, want %#v", tt.scopes, got, tt.want)
			}
		})
	}
}

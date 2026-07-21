package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestValidateFilterDecisionRequiresVersionedArrayPayloads(t *testing.T) {
	valid := validFilterDecisionForTest()
	if err := validateFilterDecision(&valid); err != nil {
		t.Fatalf("valid decision rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*model.FilterDecision)
	}{
		{name: "schema version", mutate: func(d *model.FilterDecision) { d.SchemaVersion = 2 }},
		{name: "missing mailbox", mutate: func(d *model.FilterDecision) { d.MailboxAccountID = 0 }},
		{name: "unknown action", mutate: func(d *model.FilterDecision) { d.FinalAction = "discard" }},
		{name: "object instead of array", mutate: func(d *model.FilterDecision) { d.ReasonsText = `{}` }},
		{name: "trailing JSON", mutate: func(d *model.FilterDecision) { d.AdSymbolsText = `[] []` }},
		{name: "empty payload", mutate: func(d *model.FilterDecision) { d.ParseWarningsText = "" }},
		{name: "null payload", mutate: func(d *model.FilterDecision) { d.ShadowResultsText = `null` }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := valid
			tt.mutate(&decision)
			if err := validateFilterDecision(&decision); !errors.Is(err, ErrInvalidFilterDecision) {
				t.Fatalf("validateFilterDecision() error = %v, want ErrInvalidFilterDecision", err)
			}
		})
	}
}

func TestFilterPolicyStoreRejectsInvalidWritesBeforeSQL(t *testing.T) {
	st, mock, cleanup := newMockStore(t)
	defer cleanup()

	if err := st.CreateManualFilterRevision(&model.ManualFilterRevision{
		Revision: 1, Status: "published", SchemaVersion: model.FilterPolicySchemaVersionV1, CreatedBy: "admin",
	}); !errors.Is(err, ErrInvalidFilterPolicyRevision) {
		t.Fatalf("CreateManualFilterRevision() error = %v", err)
	}
	if err := st.CreateAdFilterRevision(&model.AdFilterRevision{
		Revision: 1, Status: "draft", SchemaVersion: 2, CreatedBy: "admin",
	}); !errors.Is(err, ErrInvalidFilterPolicyRevision) {
		t.Fatalf("CreateAdFilterRevision() error = %v", err)
	}
	if err := st.UpsertFilterNodeState(&model.FilterNodeState{NodeID: 1, PolicyKind: "legacy"}); !errors.Is(err, ErrInvalidFilterNodeState) {
		t.Fatalf("UpsertFilterNodeState() error = %v", err)
	}
	if err := st.CreateFilterAudit(&model.FilterAudit{
		PolicyKind: "manual", Action: "create", EntityType: "revision", EntityID: "1", Actor: "admin", ChangesText: `[]`,
	}); !errors.Is(err, ErrInvalidFilterAudit) {
		t.Fatalf("CreateFilterAudit() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected SQL: %v", err)
	}
}

func TestPublishFilterRevisionUpdatesActiveDesiredAndAudit(t *testing.T) {
	st, mock, cleanup := newMockStore(t)
	defer cleanup()
	checksum := strings.Repeat("a", 64)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `filter_active_states`").
		WillReturnRows(sqlmock.NewRows([]string{"policy_kind", "active_revision", "checksum", "changed_at", "changed_by"}))
	mock.ExpectExec("UPDATE `manual_filter_revisions`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `filter_active_states`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT `id` FROM `mail_servers`").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3).AddRow(7))
	mock.ExpectExec("INSERT INTO `filter_node_states`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `filter_node_states`").
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec("INSERT INTO `filter_audits`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := st.db.Transaction(func(tx *gorm.DB) error {
		return publishFilterRevisionTx(tx, "manual", 4, 12, checksum, "admin", "request-1")
	})
	if err != nil {
		t.Fatalf("publishFilterRevisionTx() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestFilterPolicyDatabaseMigration(t *testing.T) {
	sourceDSN := os.Getenv("MAILHUB_TEST_MYSQL_DSN")
	if sourceDSN == "" {
		t.Skip("MAILHUB_TEST_MYSQL_DSN is not set")
	}

	config, err := drivermysql.ParseDSN(sourceDSN)
	if err != nil {
		t.Fatalf("parse source DSN: %v", err)
	}
	if config.DBName == "" {
		t.Fatal("source DSN must name a database")
	}

	adminConfig := *config
	adminConfig.DBName = "information_schema"
	adminDB, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open MariaDB admin connection: %v", err)
	}
	defer adminDB.Close()
	if err := adminDB.Ping(); err != nil {
		t.Fatalf("ping MariaDB: %v", err)
	}
	var databaseVersion string
	if err := adminDB.QueryRow("SELECT VERSION()").Scan(&databaseVersion); err != nil {
		t.Fatalf("read database version: %v", err)
	}
	t.Logf("database version: %s", databaseVersion)
	if os.Getenv("MAILHUB_REQUIRE_MARIADB_10_5") == "true" &&
		(!strings.Contains(databaseVersion, "MariaDB") || !strings.HasPrefix(databaseVersion, "10.5.")) {
		t.Fatalf("database version %q is not MariaDB 10.5", databaseVersion)
	}
	if os.Getenv("MAILHUB_TEST_INITIALIZE_LEGACY_SCHEMA") == "true" {
		var sourceTableCount int
		if err := adminDB.QueryRow(
			"SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'",
			config.DBName,
		).Scan(&sourceTableCount); err != nil {
			t.Fatalf("count legacy source tables: %v", err)
		}
		if sourceTableCount != 0 {
			t.Fatalf("refusing to initialize non-empty legacy source database %q", config.DBName)
		}
		legacyDB, err := gorm.Open(mysql.Open(sourceDSN), &gorm.Config{})
		if err != nil {
			t.Fatalf("open legacy schema database: %v", err)
		}
		if err := legacyDB.AutoMigrate(legacyMigrationModels()...); err != nil {
			t.Fatalf("initialize legacy schema: %v", err)
		}
		legacySQLDB, err := legacyDB.DB()
		if err != nil {
			t.Fatalf("get legacy sql.DB: %v", err)
		}
		defer legacySQLDB.Close()
	}

	for cycle := 1; cycle <= 2; cycle++ {
		databaseName := fmt.Sprintf("mailhub_filter_s3_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), cycle)
		migrateMariaDBClone(t, adminDB, config, databaseName)
	}
}

func migrateMariaDBClone(t *testing.T, adminDB *sql.DB, sourceConfig *drivermysql.Config, databaseName string) {
	t.Helper()
	if !strings.HasPrefix(databaseName, "mailhub_filter_s3_") {
		t.Fatalf("unsafe temporary database name %q", databaseName)
	}
	quotedDatabase := quoteMySQLIdentifier(databaseName)
	if _, err := adminDB.Exec("CREATE DATABASE " + quotedDatabase + " CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatalf("create temporary database: %v", err)
	}
	defer func() {
		if _, err := adminDB.Exec("DROP DATABASE " + quotedDatabase); err != nil {
			t.Errorf("drop temporary database: %v", err)
		}
	}()

	filterTables := filterPolicyTableNames()
	filterSet := make(map[string]struct{}, len(filterTables))
	for _, table := range filterTables {
		filterSet[table] = struct{}{}
	}
	rows, err := adminDB.Query(
		"SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME",
		sourceConfig.DBName,
	)
	if err != nil {
		t.Fatalf("list source tables: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan source table: %v", err)
		}
		if _, isNewFilterTable := filterSet[table]; isNewFilterTable {
			continue
		}
		statement := "CREATE TABLE " + quotedDatabase + "." + quoteMySQLIdentifier(table) +
			" LIKE " + quoteMySQLIdentifier(sourceConfig.DBName) + "." + quoteMySQLIdentifier(table)
		if _, err := adminDB.Exec(statement); err != nil {
			t.Fatalf("clone old table %s: %v", table, err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate source tables: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close source table rows: %v", err)
	}

	targetConfig := *sourceConfig
	targetConfig.DBName = databaseName
	targetDSN := targetConfig.FormatDSN()
	st, err := New(targetDSN, "release")
	if err != nil {
		t.Fatalf("first migration: %v", err)
	}
	defer closeStoreDB(t, st)

	assertFilterPolicySchema(t, st, filterTables)
	assertNoActiveFilterRevision(t, st)
	if _, err := st.ListDomains(); err != nil {
		t.Fatalf("old model read after migration: %v", err)
	}

	manual := &model.ManualFilterRevision{
		Revision: 1, Status: "draft", SchemaVersion: model.FilterPolicySchemaVersionV1, CreatedBy: "migration-test",
		Rules: []model.ManualFilterRule{{
			LogicalID: "manual-rule-1", Name: "test rule", ScopeType: "global", Action: "allow", Mode: "shadow", Source: "manual",
			Conditions: []model.ManualFilterCondition{{Field: "header_from.domain", Operator: "eq", ValueText: "example.test"}},
		}},
	}
	if err := st.CreateManualFilterRevision(manual); err != nil {
		t.Fatalf("create manual draft: %v", err)
	}
	if err := st.CreateManualFilterRevision(&model.ManualFilterRevision{
		Revision: 1, Status: "draft", SchemaVersion: model.FilterPolicySchemaVersionV1, CreatedBy: "migration-test",
	}); err == nil {
		t.Fatal("duplicate manual revision was accepted")
	}
	if err := st.CreateAdFilterRevision(&model.AdFilterRevision{
		Revision: 1, Status: "draft", SchemaVersion: model.FilterPolicySchemaVersionV1, CreatedBy: "migration-test",
		Detectors: []model.AdFilterDetector{{
			LogicalID: "detector-1", Symbol: "AD_TEST_SIGNAL", Name: "test detector", Mode: "shadow", Source: "local",
			Conditions: []model.AdFilterCondition{{Field: "subject", Operator: "contains", ValueText: "sale"}},
		}},
		Composites: []model.AdFilterComposite{{
			LogicalID: "composite-1", Symbol: "AD_TEST_COMPOSITE", Name: "test composite", Mode: "shadow", ScorePolicy: "keep_inputs",
			Terms: []model.AdFilterCompositeTerm{{GroupKind: "all_of", InputSymbol: "AD_TEST_SIGNAL"}},
		}},
		Weights: []model.AdFilterSymbolWeight{{Symbol: "AD_TEST_SIGNAL", ScoreMilli: 1000}, {Symbol: "AD_TEST_COMPOSITE", ScoreMilli: 2000}},
	}); err != nil {
		t.Fatalf("create ad draft: %v", err)
	}
	loadedManual, err := st.GetManualFilterRevision(1)
	if err != nil || len(loadedManual.Rules) != 1 || len(loadedManual.Rules[0].Conditions) != 1 {
		t.Fatalf("load manual draft graph: revision=%+v error=%v", loadedManual, err)
	}
	loadedAd, err := st.GetAdFilterRevision(1)
	if err != nil || len(loadedAd.Detectors) != 1 || len(loadedAd.Detectors[0].Conditions) != 1 ||
		len(loadedAd.Composites) != 1 || len(loadedAd.Composites[0].Terms) != 1 || len(loadedAd.Weights) != 2 {
		t.Fatalf("load ad draft graph: revision=%+v error=%v", loadedAd, err)
	}

	decision := validFilterDecisionForTest()
	if err := st.SaveFilterDecision(&decision); err != nil {
		t.Fatalf("save decision: %v", err)
	}
	retry := validFilterDecisionForTest()
	if err := st.SaveFilterDecision(&retry); err != nil {
		t.Fatalf("idempotent decision retry: %v", err)
	}
	conflict := validFilterDecisionForTest()
	conflict.FinalAction = "tag"
	if err := st.SaveFilterDecision(&conflict); !errors.Is(err, ErrFilterDecisionConflict) {
		t.Fatalf("conflicting decision retry error = %v", err)
	}

	restarted, err := New(targetDSN, "release")
	if err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	defer closeStoreDB(t, restarted)
	assertNoActiveFilterRevision(t, restarted)
}

func assertFilterPolicySchema(t *testing.T, st *Store, expectedTables []string) {
	t.Helper()
	for _, table := range expectedTables {
		if !st.db.Migrator().HasTable(table) {
			t.Errorf("missing table %s", table)
		}
	}
	for _, column := range []string{"reasons_text", "ad_symbols_text", "shadow_results_text", "parse_warnings_text"} {
		var dataType string
		if err := st.db.Raw(
			"SELECT DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'filter_decisions' AND COLUMN_NAME = ?",
			column,
		).Scan(&dataType).Error; err != nil {
			t.Fatalf("read filter_decisions.%s type: %v", column, err)
		}
		if dataType != "longtext" {
			t.Errorf("filter_decisions.%s type = %q, want longtext", column, dataType)
		}
	}
	indexes := []struct {
		model any
		name  string
	}{
		{&model.ManualFilterRevision{}, "uk_manual_filter_revision"},
		{&model.ManualFilterRule{}, "uk_manual_rule_logical"},
		{&model.AdFilterRevision{}, "uk_ad_filter_revision"},
		{&model.AdFilterDetector{}, "uk_ad_detector_symbol"},
		{&model.AdFilterComposite{}, "uk_ad_composite_symbol"},
		{&model.AdFilterSymbolWeight{}, "uk_ad_weight_symbol"},
		{&model.FilterNodeState{}, "uk_filter_node_policy"},
	}
	for _, index := range indexes {
		if !st.db.Migrator().HasIndex(index.model, index.name) {
			t.Errorf("missing unique index %s", index.name)
		}
	}
}

func assertNoActiveFilterRevision(t *testing.T, st *Store) {
	t.Helper()
	var count int64
	if err := st.db.Model(&model.FilterActiveState{}).Count(&count).Error; err != nil {
		t.Fatalf("count active filter states: %v", err)
	}
	if count != 0 {
		t.Fatalf("active filter state count = %d, want 0", count)
	}
	if _, err := st.GetFilterActiveState("manual"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("manual active state error = %v, want gorm.ErrRecordNotFound", err)
	}
}

func closeStoreDB(t *testing.T, st *Store) {
	t.Helper()
	if st == nil {
		return
	}
	db, err := st.db.DB()
	if err != nil {
		t.Errorf("get store sql.DB: %v", err)
		return
	}
	if err := db.Close(); err != nil {
		t.Errorf("close store sql.DB: %v", err)
	}
}

func quoteMySQLIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func filterPolicyTableNames() []string {
	tables := []string{
		"manual_filter_revisions", "manual_filter_rules", "manual_filter_conditions",
		"ad_filter_revisions", "ad_filter_detectors", "ad_filter_conditions", "ad_filter_composites",
		"ad_filter_composite_terms", "ad_filter_symbol_weights", "filter_decisions", "filter_quarantines",
		"filter_active_states", "filter_node_states", "filter_audits",
	}
	sort.Strings(tables)
	return tables
}

func validFilterDecisionForTest() model.FilterDecision {
	return model.FilterDecision{
		SchemaVersion:     model.FilterPolicySchemaVersionV1,
		DecisionKey:       "decision-key-1",
		MessageKey:        "message-key-1",
		MailboxAccountID:  10,
		NodeID:            20,
		ManualRevision:    0,
		AdRevision:        0,
		ManualAction:      "allow",
		AdAction:          "allow",
		FinalAction:       "allow",
		ReasonsText:       `[]`,
		AdSymbolsText:     `[]`,
		ShadowResultsText: `[]`,
		ParseWarningsText: `[]`,
		EvaluatedAt:       time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	}
}

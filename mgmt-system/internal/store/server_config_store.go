package store

import (
	"errors"
	"time"

	"github.com/ticket/email-mgmt-system/internal/configschema"
	"github.com/ticket/email-mgmt-system/internal/configstate"
	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) GetServerConfigOverride(serverID uint64, key string) (*model.ServerConfigOverride, error) {
	var value model.ServerConfigOverride
	err := s.db.Where("server_id = ? AND config_key = ?", serverID, key).First(&value).Error
	return &value, err
}

func (s *Store) ListServerConfigOverrides(serverID uint64) ([]model.ServerConfigOverride, error) {
	var values []model.ServerConfigOverride
	err := s.db.Where("server_id = ?", serverID).Find(&values).Error
	return values, err
}

func (s *Store) SetServerConfigOverride(value *model.ServerConfigOverride) error {
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "server_id"}, {Name: "config_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"config_value", "value_type", "updated_by", "updated_at"}),
	}).Create(value).Error
}

func (s *Store) SetServerConfigOverrideAndBump(value *model.ServerConfigOverride, actor, oldValue string) (uint64, error) {
	var revision uint64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "server_id"}, {Name: "config_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"config_value", "value_type", "updated_by", "updated_at"}),
		}).Create(value).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.MailServer{}).Where("id = ?", value.ServerID).
			Updates(map[string]interface{}{
				"desired_revision":  gorm.Expr("desired_revision + 1"),
				"config_changed_at": time.Now().UTC(),
				"boot_id_at_change": gorm.Expr("last_boot_id"),
				"last_reload_error": "",
			}).Error; err != nil {
			return err
		}
		var server model.MailServer
		if err := tx.First(&server, value.ServerID).Error; err != nil {
			return err
		}
		revision = server.DesiredRevision
		serverID := value.ServerID
		return tx.Create(&model.ConfigChangeAudit{
			Scope: "server", ServerID: &serverID, ConfigKey: value.ConfigKey, Action: "set",
			OldValue: oldValue, NewValue: value.ConfigValue, Actor: actor, DesiredRevision: revision,
		}).Error
	})
	if err != nil {
		return 0, err
	}
	return revision, nil
}

func (s *Store) DeleteServerConfigOverride(serverID uint64, key string) error {
	return s.db.Where("server_id = ? AND config_key = ?", serverID, key).Delete(&model.ServerConfigOverride{}).Error
}

func (s *Store) DeleteServerConfigOverrideAndBump(serverID uint64, key, actor, oldValue, newValue string) (uint64, error) {
	var revision uint64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("server_id = ? AND config_key = ?", serverID, key).
			Delete(&model.ServerConfigOverride{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.MailServer{}).Where("id = ?", serverID).
			Updates(map[string]interface{}{
				"desired_revision":  gorm.Expr("desired_revision + 1"),
				"config_changed_at": time.Now().UTC(),
				"boot_id_at_change": gorm.Expr("last_boot_id"),
				"last_reload_error": "",
			}).Error; err != nil {
			return err
		}
		var server model.MailServer
		if err := tx.First(&server, serverID).Error; err != nil {
			return err
		}
		revision = server.DesiredRevision
		return tx.Create(&model.ConfigChangeAudit{
			Scope: "server", ServerID: &serverID, ConfigKey: key, Action: "reset",
			OldValue: oldValue, NewValue: newValue, Actor: actor, DesiredRevision: revision,
		}).Error
	})
	if err != nil {
		return 0, err
	}
	return revision, nil
}

func (s *Store) ListServerConfigAudits(serverID uint64, limit int) ([]model.ConfigChangeAudit, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	var values []model.ConfigChangeAudit
	err := s.db.Where("scope = ? AND server_id = ?", "server", serverID).
		Order("id DESC").Limit(limit).Find(&values).Error
	return values, err
}

func (s *Store) BumpAllServerDesiredRevisions(tx *gorm.DB) error {
	if tx == nil {
		tx = s.db
	}
	return tx.Model(&model.MailServer{}).Where("1 = 1").Updates(map[string]interface{}{
		"desired_revision":  gorm.Expr("desired_revision + 1"),
		"config_changed_at": time.Now().UTC(),
		"boot_id_at_change": gorm.Expr("last_boot_id"),
		"last_reload_error": "",
	}).Error
}

func (s *Store) RecordServerReloadResult(serverID uint64, reloadErr error) error {
	message := ""
	if reloadErr != nil {
		message = reloadErr.Error()
	}
	return s.db.Model(&model.MailServer{}).Where("id = ?", serverID).
		Update("last_reload_error", message).Error
}

func (s *Store) GetServerConfigSnapshot(serverID uint64, key string) (*model.ServerConfigSnapshot, error) {
	var value model.ServerConfigSnapshot
	err := s.db.Where("server_id = ? AND config_key = ?", serverID, key).First(&value).Error
	return &value, err
}

func (s *Store) UpsertServerConfigSnapshots(serverID uint64, reportedAt time.Time, desiredRevision, appliedRevision uint64, bootID string, startedAt time.Time, values []model.ServerConfigSnapshot) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for i := range values {
			values[i].ServerID = serverID
			values[i].ReportedAt = reportedAt
			values[i].DesiredRevision = desiredRevision
			values[i].AppliedRevision = appliedRevision
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "server_id"}, {Name: "config_key"}},
				DoUpdates: clause.AssignmentColumns([]string{"effective_value", "source", "reloadable", "requires_restart", "applied_at", "reported_at", "desired_revision", "applied_revision", "boot_id"}),
			}).Create(&values[i]).Error; err != nil {
				return err
			}
		}
		updates := map[string]interface{}{"applied_revision": appliedRevision, "last_apply_error": "", "last_reload_error": ""}
		if bootID != "" {
			updates["last_boot_id"] = bootID
			if !startedAt.IsZero() {
				updates["last_started_at"] = startedAt.UTC()
			}
		}
		return tx.Model(&model.MailServer{}).
			Where("id = ? AND applied_revision <= ?", serverID, appliedRevision).
			Updates(updates).Error
	})
}

func IsNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }

func (s *Store) AttachServerConfigSummaries(servers []model.MailServer, key, globalValue string) {
	for i := range servers {
		summary := &model.ServerConfigSummary{Source: "unknown", Status: "unreported"}
		desiredValue := globalValue
		desiredSource := "global"
		if override, err := s.GetServerConfigOverride(servers[i].ID, key); err == nil {
			summary.HasOverride = true
			desiredValue = override.ConfigValue
			desiredSource = "server_override"
		}
		if snapshot, err := s.GetServerConfigSnapshot(servers[i].ID, key); err == nil {
			summary.EffectiveValue = snapshot.EffectiveValue
			summary.Source = snapshot.Source
			summary.ReportedAt = &snapshot.ReportedAt
			if definition, ok := configschema.Get(key); ok {
				summary.Status = configstate.Resolve(servers[i], snapshot, definition, desiredValue, desiredSource, time.Now(), 15*time.Minute)
			}
		} else if definition, ok := configschema.Get(key); ok {
			summary.Status = configstate.Resolve(servers[i], nil, definition, desiredValue, desiredSource, time.Now(), 15*time.Minute)
		}
		servers[i].ConfigSummary = summary
	}
}

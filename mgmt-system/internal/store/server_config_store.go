package store

import (
	"errors"
	"time"

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

func (s *Store) DeleteServerConfigOverride(serverID uint64, key string) error {
	return s.db.Where("server_id = ? AND config_key = ?", serverID, key).Delete(&model.ServerConfigOverride{}).Error
}

func (s *Store) GetServerConfigSnapshot(serverID uint64, key string) (*model.ServerConfigSnapshot, error) {
	var value model.ServerConfigSnapshot
	err := s.db.Where("server_id = ? AND config_key = ?", serverID, key).First(&value).Error
	return &value, err
}

func (s *Store) UpsertServerConfigSnapshots(serverID uint64, reportedAt time.Time, values []model.ServerConfigSnapshot) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for i := range values {
			values[i].ServerID = serverID
			values[i].ReportedAt = reportedAt
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "server_id"}, {Name: "config_key"}},
				DoUpdates: clause.AssignmentColumns([]string{"effective_value", "source", "reloadable", "requires_restart", "applied_at", "reported_at"}),
			}).Create(&values[i]).Error; err != nil {
				return err
			}
		}
		return nil
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
			summary.Status = "applied"
			if snapshot.EffectiveValue != desiredValue || snapshot.Source != desiredSource {
				summary.Status = "pending_restart"
			}
		}
		servers[i].ConfigSummary = summary
	}
}

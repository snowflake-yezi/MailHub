package store

import (
	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/gorm"
)

// ListIntegratedMailboxes 列出全部集成邮箱（当前生效项排前）
func (s *Store) ListIntegratedMailboxes() ([]model.IntegratedMailbox, error) {
	var list []model.IntegratedMailbox
	err := s.db.Order("is_active DESC, id ASC").Find(&list).Error
	return list, err
}

// GetIntegratedMailbox 按 ID 查询
func (s *Store) GetIntegratedMailbox(id uint64) (*model.IntegratedMailbox, error) {
	var m model.IntegratedMailbox
	if err := s.db.First(&m, id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// CreateIntegratedMailbox 新增集成邮箱
func (s *Store) CreateIntegratedMailbox(m *model.IntegratedMailbox) error {
	return s.db.Create(m).Error
}

// UpdateIntegratedMailbox 更新地址与备注（is_active 由 SetActive 专门管理）
func (s *Store) UpdateIntegratedMailbox(m *model.IntegratedMailbox) error {
	return s.db.Model(&model.IntegratedMailbox{}).Where("id = ?", m.ID).
		Updates(map[string]interface{}{
			"email_address": m.EmailAddress,
			"display_name":  m.DisplayName,
		}).Error
}

// DeleteIntegratedMailbox 删除集成邮箱（调用方应先阻止删除 active 项）
func (s *Store) DeleteIntegratedMailbox(id uint64) error {
	return s.db.Delete(&model.IntegratedMailbox{}, id).Error
}

// SetActiveIntegratedMailbox 将指定集成邮箱设为当前生效转发目标。
// 事务内：① 全表 is_active 清零 ② 指定项 is_active=true ③ 同步 system_configs.forward.target_address。
// 提交后由调用方触发 InvalidateConfigCache + 通知 mail-node reload。
func (s *Store) SetActiveIntegratedMailbox(id uint64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var m model.IntegratedMailbox
		if err := tx.First(&m, id).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.IntegratedMailbox{}).Where("1 = 1").Update("is_active", false).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.IntegratedMailbox{}).Where("id = ?", id).Update("is_active", true).Error; err != nil {
			return err
		}
		// 同步转发目标配置，mail-node 下次 reload 后生效
		if err := tx.Model(&model.SystemConfig{}).
			Where("config_key = ?", "forward.target_address").
			Update("config_value", m.EmailAddress).Error; err != nil {
			return err
		}
		return nil
	})
}

// SeedDefaultIntegratedMailboxes 首次部署种子：表空时插入默认集成邮箱。
// 默认值与 config_store 中 forward.target_address 的 seed 一致。
func (s *Store) SeedDefaultIntegratedMailboxes() error {
	var count int64
	if err := s.db.Model(&model.IntegratedMailbox{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return s.db.Create(&model.IntegratedMailbox{
		EmailAddress: "union@asadad.bond",
		DisplayName:  "主汇总",
		IsActive:     true,
	}).Error
}

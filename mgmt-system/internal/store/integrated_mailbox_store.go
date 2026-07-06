package store

import (
	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/gorm"
)

// activeIntegratedMailbox 取当前生效的集成邮箱记录。若历史数据异常存在多个 active，取 id 最小者。
func (s *Store) activeIntegratedMailbox(tx *gorm.DB) (*model.IntegratedMailbox, error) {
	var m model.IntegratedMailbox
	if err := tx.Where("is_active = ?", true).Order("id ASC").First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// GetActiveIntegratedMailboxCredentials 返回当前生效集成邮箱及其账号凭据。
// 凭据来自 mailbox_accounts，不写入 system_configs，避免在后台配置页暴露密码。
func (s *Store) GetActiveIntegratedMailboxCredentials() (*model.IntegratedMailbox, *model.MailboxAccount, error) {
	m, err := s.activeIntegratedMailbox(s.db)
	if err != nil {
		return nil, nil, err
	}
	var account model.MailboxAccount
	if err := s.db.Where("email_address = ?", m.EmailAddress).First(&account).Error; err != nil {
		return m, nil, err
	}
	return m, &account, nil
}

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
// SMTP 认证账号/密码不写 system_configs，由内部配置接口按 active 集成邮箱从 mailbox_accounts 下发。
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

// ReconcileActiveIntegratedMailboxConfig 以当前 active 集成邮箱为准，修正转发目标动态配置。
// 用于启动自愈：避免 system_configs.forward.target_address 缺失、空值或与 active 记录漂移后，
// mail-node 热加载到错误目标导致转发中断。
func (s *Store) ReconcileActiveIntegratedMailboxConfig() error {
	m, err := s.activeIntegratedMailbox(s.db)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}

	result := s.db.Model(&model.SystemConfig{}).
		Where("config_key = ?", "forward.target_address").
		Update("config_value", m.EmailAddress)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	return s.db.Create(&model.SystemConfig{
		ConfigKey:    "forward.target_address",
		ConfigValue:  m.EmailAddress,
		ValueType:    "string",
		Category:     "forward",
		Label:        "转发目标邮箱",
		Description:  "非垃圾邮件汇总转发的集成邮箱地址（当前生效项，由集成邮箱管理页联动写入）",
		DefaultValue: "union@asadad.bond",
		Reloadable:   true,
	}).Error
}

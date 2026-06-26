package store

import (
	"fmt"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Store 数据库操作封装
type Store struct {
	db *gorm.DB
}

// New 创建数据库连接并自动迁移
func New(dsn string, mode string) (*Store, error) {
	logLevel := logger.Warn
	if mode == "debug" {
		logLevel = logger.Info
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}

	// 连接池配置
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	// 自动迁移
	if err := db.AutoMigrate(
		&model.Domain{},
		&model.MailServer{},
		&model.MailboxAccount{},
		&model.OrderMailboxMapping{},
		&model.OrderMailbox{},
		&model.FilterRule{},
		&model.ApiToken{},
		&model.ServerDomain{},
	); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}

	s := &Store{db: db}
	if err := s.MigrateLegacyOrderMailboxes(); err != nil {
		return nil, fmt.Errorf("migrate legacy order_mailboxes: %w", err)
	}

	return s, nil
}

// DB 返回原始 gorm 实例（供内部使用）
func (s *Store) DB() *gorm.DB {
	return s.db
}

// ===== Domain =====

func (s *Store) CreateDomain(d *model.Domain) error { return s.db.Create(d).Error }
func (s *Store) UpdateDomain(d *model.Domain) error { return s.db.Save(d).Error }
func (s *Store) ListDomains() ([]model.Domain, error) {
	var list []model.Domain
	err := s.db.Where("status = ?", "active").Find(&list).Error
	return list, err
}
func (s *Store) GetDomainByID(id uint64) (*model.Domain, error) {
	var d model.Domain
	err := s.db.First(&d, id).Error
	if err != nil {
		return nil, err
	}
	return &d, nil
}
func (s *Store) GetDomainByName(name string) (*model.Domain, error) {
	var d model.Domain
	err := s.db.Where("name = ?", name).First(&d).Error
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ===== MailServer =====

func (s *Store) CreateServer(srv *model.MailServer) error { return s.db.Create(srv).Error }
func (s *Store) UpdateServer(srv *model.MailServer) error { return s.db.Save(srv).Error }
func (s *Store) GetServer(id uint64) (*model.MailServer, error) {
	var srv model.MailServer
	err := s.db.First(&srv, id).Error
	if err != nil {
		return nil, err
	}
	return &srv, nil
}
func (s *Store) ListServers() ([]model.MailServer, error) {
	var list []model.MailServer
	err := s.db.Order("id ASC").Find(&list).Error
	return list, err
}
func (s *Store) GetHealthyServerWithMinLoad() (*model.MailServer, error) {
	var srv model.MailServer
	err := s.db.Where("status = ?", "healthy").
		Where("current_load < capacity").
		Order("current_load ASC").
		First(&srv).Error
	if err != nil {
		return nil, err
	}
	return &srv, nil
}
func (s *Store) IncrementServerLoad(serverID uint64) error {
	return s.db.Model(&model.MailServer{}).
		Where("id = ?", serverID).
		UpdateColumn("current_load", gorm.Expr("current_load + 1")).Error
}
func (s *Store) DecrementServerLoad(serverID uint64) error {
	return s.db.Model(&model.MailServer{}).
		Where("id = ? AND current_load > 0", serverID).
		UpdateColumn("current_load", gorm.Expr("current_load - 1")).Error
}
func (s *Store) DeleteServer(id uint64) error {
	return s.db.Delete(&model.MailServer{}, id).Error
}
func (s *Store) CountMailboxesOnServer(serverID uint64) (int64, error) {
	var count int64
	err := s.db.Model(&model.MailboxAccount{}).Where("server_id = ?", serverID).Count(&count).Error
	return count, err
}
func (s *Store) UpdateServerHeartbeat(serverID uint64, status string) error {
	now := time.Now()
	return s.db.Model(&model.MailServer{}).Where("id = ?", serverID).
		Updates(map[string]interface{}{
			"last_heartbeat": &now,
			"status":         status,
		}).Error
}

// ===== ServerDomain（服务器-域名 M:N 绑定） =====
// 记录「哪台服务器为哪个域名提供收发服务」及其远端 Postfix/DKIM 同步状态。
// 详见 docs/design/t4-t5-server-domain-pool-design.md。

// GetServerDomain 取单个绑定
func (s *Store) GetServerDomain(serverID, domainID uint64) (*model.ServerDomain, error) {
	var sd model.ServerDomain
	err := s.db.Where("server_id = ? AND domain_id = ?", serverID, domainID).First(&sd).Error
	if err != nil {
		return nil, err
	}
	return &sd, nil
}

// BindServerDomain 建立绑定（按 server_id+domain_id 幂等），返回当前绑定。
// 命中已有记录（如曾被移除留下的 inactive 绑定）时，用 Assign 把绑定状态拉回 active
// 并把远端同步状态重置为 pending、清空旧的 dkim_selector/dkim_public_key，
// 避免重新添加时残留旧的同步/DKIM 字段；最终同步结果由 handler 调 UpdateServerDomainSync 覆盖。
func (s *Store) BindServerDomain(sd *model.ServerDomain) error {
	return s.db.Where("server_id = ? AND domain_id = ?", sd.ServerID, sd.DomainID).
		Assign(map[string]interface{}{
			"status":          "active",
			"sync_status":     "pending",
			"postfix_status":  "pending",
			"dkim_status":     "pending",
			"sync_error":      "",
			"dkim_selector":   "",
			"dkim_public_key": "",
		}).FirstOrCreate(sd).Error
}

// UpdateServerDomainSync 更新远端同步状态字段（sync_status/postfix_status/dkim_status/
// dkim_selector/dkim_public_key/sync_error/synced_at）
func (s *Store) UpdateServerDomainSync(serverID, domainID uint64, fields map[string]interface{}) error {
	return s.db.Model(&model.ServerDomain{}).
		Where("server_id = ? AND domain_id = ?", serverID, domainID).
		Updates(fields).Error
}

// SetServerDomainStatus 切换绑定状态（active/inactive）
func (s *Store) SetServerDomainStatus(serverID, domainID uint64, status string) error {
	return s.db.Model(&model.ServerDomain{}).
		Where("server_id = ? AND domain_id = ?", serverID, domainID).
		Update("status", status).Error
}

// MarkServerDomainRemoved marks a server-domain binding as inactive after the
// remote mail-node has removed the domain from Postfix/OpenDKIM.
func (s *Store) MarkServerDomainRemoved(serverID, domainID uint64) error {
	now := time.Now()
	return s.db.Model(&model.ServerDomain{}).
		Where("server_id = ? AND domain_id = ?", serverID, domainID).
		Updates(map[string]interface{}{
			"status":         "inactive",
			"sync_status":    "partial",
			"postfix_status": "sync_failed",
			"dkim_status":    "sync_failed",
			"sync_error":     "domain removed from remote server",
			"synced_at":      &now,
		}).Error
}

// ListDomainsByServer 列出某服务器绑定的域名（preload Domain + Server）
func (s *Store) ListDomainsByServer(serverID uint64) ([]model.ServerDomain, error) {
	var list []model.ServerDomain
	err := s.db.Preload("Domain").Preload("Server").
		Where("server_id = ?", serverID).
		Order("id ASC").Find(&list).Error
	return list, err
}

// ListServersByDomain 列出服务某域名的服务器（preload Server）
func (s *Store) ListServersByDomain(domainID uint64) ([]model.ServerDomain, error) {
	var list []model.ServerDomain
	err := s.db.Preload("Server").
		Where("domain_id = ?", domainID).
		Order("id ASC").Find(&list).Error
	return list, err
}

// GetHealthyServerForDomain 域名感知分配：在该域已同步 Postfix 的活跃绑定中，
// 选一台 healthy 且有空余容量的最闲服务器。查询条件见设计文档 §4.2。
func (s *Store) GetHealthyServerForDomain(domainID uint64) (*model.MailServer, error) {
	var srv model.MailServer
	err := s.db.Joins("JOIN server_domains ON server_domains.server_id = mail_servers.id").
		Where("server_domains.domain_id = ?", domainID).
		Where("server_domains.status = ?", "active").
		Where("server_domains.sync_status IN ?", []string{"synced", "partial"}).
		Where("server_domains.postfix_status = ?", "synced").
		Where("mail_servers.status = ?", "healthy").
		Where("mail_servers.current_load < mail_servers.capacity").
		Order("mail_servers.current_load ASC").
		First(&srv).Error
	if err != nil {
		return nil, err
	}
	return &srv, nil
}

// CountMailboxesOnServerDomain 统计某服务器某域的邮箱数（移除域名保护检查）
func (s *Store) CountMailboxesOnServerDomain(serverID, domainID uint64) (int64, error) {
	var count int64
	err := s.db.Model(&model.MailboxAccount{}).
		Where("server_id = ? AND domain_id = ?", serverID, domainID).Count(&count).Error
	return count, err
}

// SeedServerDomainsFromAccounts 扫 mailbox_accounts 的 distinct(server_id, domain_id)，
// 为每个真实存在的「服务器-域名」组合补一条 synced 绑定（远端已实际配置该域）。
// 同时为 public_host 为空的服务器按其绑定域名的 mx_server 回填 public_host。
// 必须在真实账号导入（importRealAccounts）之后调用。
func (s *Store) SeedServerDomainsFromAccounts() error {
	type serverDomainPair struct {
		ServerID uint64
		DomainID uint64
	}
	var pairs []serverDomainPair
	if err := s.db.Model(&model.MailboxAccount{}).
		Select("DISTINCT server_id, domain_id").Scan(&pairs).Error; err != nil {
		return err
	}

	now := time.Now()
	serversBackfilled := make(map[uint64]bool)
	for _, p := range pairs {
		sd := &model.ServerDomain{
			ServerID:      p.ServerID,
			DomainID:      p.DomainID,
			Status:        "active",
			SyncStatus:    "synced",
			PostfixStatus: "synced",
			DkimStatus:    "synced",
			DkimSelector:  "mail",
			SyncedAt:      &now,
		}
		if err := s.db.Where("server_id = ? AND domain_id = ?", p.ServerID, p.DomainID).
			FirstOrCreate(sd).Error; err != nil {
			return err
		}

		// 每台服务器只回填一次 public_host，取该域 mx_server（如 mail.example.com）
		if !serversBackfilled[p.ServerID] {
			serversBackfilled[p.ServerID] = true
			var srv model.MailServer
			if err := s.db.First(&srv, p.ServerID).Error; err == nil && srv.PublicHost == "" {
				var d model.Domain
				if err := s.db.First(&d, p.DomainID).Error; err == nil && d.MXServer != "" {
					s.db.Model(&srv).Update("public_host", d.MXServer)
				}
			}
		}
	}
	return nil
}

// ===== MailboxAccount / OrderMailboxMapping =====

func (s *Store) CreateMailboxAccount(account *model.MailboxAccount) error {
	return s.db.Create(account).Error
}

func (s *Store) UpsertMailboxAccount(account *model.MailboxAccount) error {
	var existing model.MailboxAccount
	err := s.db.Where("email_address = ?", account.EmailAddress).First(&existing).Error
	if err == nil {
		updates := map[string]interface{}{
			"local_part":     account.LocalPart,
			"password":       account.Password,
			"domain_id":      account.DomainID,
			"server_id":      account.ServerID,
			"status":         account.Status,
			"sync_status":    account.SyncStatus,
			"sync_error":     account.SyncError,
			"retention_days": account.RetentionDays,
			"synced_at":      account.SyncedAt,
			"expires_at":     account.ExpiresAt,
		}
		if err := s.db.Model(&existing).Updates(updates).Error; err != nil {
			return err
		}
		account.ID = existing.ID
		return nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	return s.db.Create(account).Error
}

func (s *Store) BindOrderMailbox(orderID string, accountID uint64) error {
	mapping := &model.OrderMailboxMapping{
		OrderID:          orderID,
		MailboxAccountID: accountID,
	}
	return s.db.Where("order_id = ? AND mailbox_account_id = ?", orderID, accountID).
		FirstOrCreate(mapping).Error
}

func (s *Store) CreateMailboxAccountWithOrder(account *model.MailboxAccount, orderID string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(account).Error; err != nil {
			return err
		}
		if orderID == "" {
			return nil
		}
		return tx.Where("order_id = ? AND mailbox_account_id = ?", orderID, account.ID).
			FirstOrCreate(&model.OrderMailboxMapping{
				OrderID:          orderID,
				MailboxAccountID: account.ID,
			}).Error
	})
}

func (s *Store) GetMailboxAccountByOrderID(orderID string) (*model.MailboxAccount, error) {
	var mapping model.OrderMailboxMapping
	err := s.db.Preload("MailboxAccount.Domain").Preload("MailboxAccount.Server").
		Where("order_id = ?", orderID).First(&mapping).Error
	if err != nil {
		return nil, err
	}
	return &mapping.MailboxAccount, nil
}

func (s *Store) GetMailboxAccountByEmail(email string) (*model.MailboxAccount, error) {
	var mb model.MailboxAccount
	err := s.db.Preload("Domain").Preload("Server").
		Where("email_address = ?", email).First(&mb).Error
	if err != nil {
		return nil, err
	}
	return &mb, nil
}

type MailboxListFilter struct {
	Status   string
	Search   string
	DomainID uint64
	ServerID uint64
}

func (s *Store) ListMailboxes(page, size int, status, search string) ([]model.MailboxAccount, int64, error) {
	return s.ListMailboxesWithFilter(page, size, MailboxListFilter{
		Status: status,
		Search: search,
	})
}

func (s *Store) ListMailboxesWithFilter(page, size int, filter MailboxListFilter) ([]model.MailboxAccount, int64, error) {
	var list []model.MailboxAccount
	var total int64

	q := s.db.Model(&model.MailboxAccount{})
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		q = q.Where("email_address LIKE ? OR local_part LIKE ?", like, like)
	}
	if filter.DomainID > 0 {
		q = q.Where("domain_id = ?", filter.DomainID)
	}
	if filter.ServerID > 0 {
		q = q.Where("server_id = ?", filter.ServerID)
	}
	q.Count(&total)

	err := q.Preload("Domain").Preload("Server").
		Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error
	return list, total, err
}
// GetMailboxByID returns a mailbox account by ID with Domain and Server preloaded.
func (s *Store) GetMailboxByID(id uint64) (*model.MailboxAccount, error) {
	var mb model.MailboxAccount
	err := s.db.Preload("Domain").Preload("Server").First(&mb, id).Error
	if err != nil {
		return nil, err
	}
	return &mb, nil
}

// UpdateMailboxPassword updates the password for a mailbox account and marks sync as pending.
func (s *Store) UpdateMailboxPassword(id uint64, password string) error {
	return s.db.Model(&model.MailboxAccount{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"password":    password,
			"sync_status": "pending",
		}).Error
}

func (s *Store) DisableMailbox(id uint64) error {
	now := time.Now()
	return s.db.Model(&model.MailboxAccount{}).Where("id = ?", id).
		Updates(map[string]interface{}{"status": "disabled", "disabled_at": &now}).Error
}
func (s *Store) RecycleMailbox(id uint64) error {
	now := time.Now()
	return s.db.Model(&model.MailboxAccount{}).Where("id = ?", id).
		Updates(map[string]interface{}{"status": "recycled", "recycled_at": &now}).Error
}
func (s *Store) UpdateRetentionDays(id uint64, days int) error {
	return s.db.Model(&model.MailboxAccount{}).Where("id = ?", id).
		Update("retention_days", days).Error
}
func (s *Store) FindExpiredMailboxes() ([]model.MailboxAccount, error) {
	var list []model.MailboxAccount
	err := s.db.Where("status = 'active' AND expires_at <= ?", time.Now()).Find(&list).Error
	return list, err
}

func (s *Store) CreateMailbox(mb *model.OrderMailbox) error {
	return s.CreateMailboxAccountWithOrder(legacyToAccount(mb), mb.OrderID)
}

func (s *Store) GetMailboxByOrderID(orderID string) (*model.MailboxAccount, error) {
	return s.GetMailboxAccountByOrderID(orderID)
}

func (s *Store) GetMailboxByEmail(email string) (*model.MailboxAccount, error) {
	return s.GetMailboxAccountByEmail(email)
}

func (s *Store) MigrateLegacyOrderMailboxes() error {
	var legacy []model.OrderMailbox
	if err := s.db.Find(&legacy).Error; err != nil {
		return err
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, old := range legacy {
			account := legacyToAccount(&old)
			if err := tx.Where("email_address = ?", account.EmailAddress).
				FirstOrCreate(account).Error; err != nil {
				return err
			}
			if old.OrderID == "" {
				continue
			}
			if err := tx.Where("order_id = ? AND mailbox_account_id = ?", old.OrderID, account.ID).
				FirstOrCreate(&model.OrderMailboxMapping{
					OrderID:          old.OrderID,
					MailboxAccountID: account.ID,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func legacyToAccount(old *model.OrderMailbox) *model.MailboxAccount {
	return &model.MailboxAccount{
		EmailAddress:  old.EmailAddress,
		LocalPart:     old.LocalPart,
		Password:      old.Password,
		DomainID:      old.DomainID,
		ServerID:      old.ServerID,
		Status:        old.Status,
		SyncStatus:    old.SyncStatus,
		SyncError:     old.SyncError,
		RetentionDays: old.RetentionDays,
		CreatedAt:     old.CreatedAt,
		SyncedAt:      old.SyncedAt,
		ExpiresAt:     old.ExpiresAt,
		DisabledAt:    old.DisabledAt,
		RecycledAt:    old.RecycledAt,
	}
}

// ===== FilterRule =====

func (s *Store) CreateRule(r *model.FilterRule) error { return s.db.Create(r).Error }
func (s *Store) UpdateRule(r *model.FilterRule) error { return s.db.Save(r).Error }
func (s *Store) DeleteRule(id uint64) error           { return s.db.Delete(&model.FilterRule{}, id).Error }
func (s *Store) GetRule(id uint64) (*model.FilterRule, error) {
	var r model.FilterRule
	err := s.db.First(&r, id).Error
	if err != nil {
		return nil, err
	}
	return &r, nil
}
func (s *Store) ListRules() ([]model.FilterRule, error) {
	var list []model.FilterRule
	err := s.db.Where("enabled = ?", true).Order("priority ASC, id ASC").Find(&list).Error
	return list, err
}
func (s *Store) ListAllRules() ([]model.FilterRule, error) {
	var list []model.FilterRule
	err := s.db.Order("priority ASC, id ASC").Find(&list).Error
	return list, err
}

// ===== ApiToken =====

func (s *Store) FindToken(token string) (*model.ApiToken, error) {
	var t model.ApiToken
	err := s.db.Where("token = ? AND enabled = ?", token, true).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (s *Store) UpdateTokenLastUsed(id uint64) {
	now := time.Now()
	s.db.Model(&model.ApiToken{}).Where("id = ?", id).Update("last_used_at", &now)
}

// ===== Seed =====

// SeedDefaultData 初始化默认数据（首次部署时调用）
func (s *Store) SeedDefaultData(domainName string, defaultRetention int, tokens []struct{ Name, Token, Scopes string }) error {
	// 域名
	var count int64
	s.db.Model(&model.Domain{}).Where("name = ?", domainName).Count(&count)
	if count == 0 {
		s.db.Create(&model.Domain{Name: domainName, MXServer: "mail." + domainName, Status: "active"})
	}

	// Token
	for _, t := range tokens {
		s.db.Model(&model.ApiToken{}).Where("token = ?", t.Token).Count(&count)
		if count == 0 {
			s.db.Create(&model.ApiToken{Name: t.Name, Token: t.Token, Scopes: t.Scopes, Enabled: true})
		}
	}

	return nil
}

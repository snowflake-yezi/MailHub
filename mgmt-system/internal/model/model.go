package model

import "time"

// Domain 域名
type Domain struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Name      string    `gorm:"uniqueIndex;size:191;not null" json:"name"`
	MXServer  string    `gorm:"size:255;not null" json:"mx_server"`
	Status    string    `gorm:"type:enum('active','inactive');default:active" json:"status"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// MailServer 邮箱服务器
type MailServer struct {
	ID                uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Name              string     `gorm:"size:128;not null" json:"name"`
	APIHost           string     `gorm:"size:255;not null" json:"api_host"`
	SMTPHost          string     `gorm:"size:255;not null" json:"smtp_host"`
	IMAPHost          string     `gorm:"size:255;not null" json:"imap_host"`
	PublicHost        string     `gorm:"size:255" json:"public_host"`
	Capacity          int        `gorm:"not null;default:5000" json:"capacity"`
	CurrentLoad       int        `gorm:"not null;default:0" json:"current_load"`
	Status            string     `gorm:"type:enum('healthy','degraded','down','draining');default:healthy" json:"status"`
	LastHeartbeat     *time.Time `json:"last_heartbeat"`
	LastProbeAt       *time.Time `json:"last_probe_at"`
	ProbeFailCount    int        `gorm:"not null;default:0" json:"probe_fail_count"`
	HeartbeatInterval int        `gorm:"not null;default:30" json:"heartbeat_interval"`
	CreatedAt         time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt         time.Time  `gorm:"autoUpdateTime" json:"updated_at"`

	// Domains 该服务器绑定的 active 域名，仅用于列表展示，不落库（transient）。
	Domains []Domain `gorm:"-" json:"domains,omitempty"`
}

// MailboxAccount 邮箱账号资产，维度为 server + domain + mailbox + credential。
type MailboxAccount struct {
	ID            uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	EmailAddress  string     `gorm:"uniqueIndex;size:191;not null" json:"email_address"`
	LocalPart     string     `gorm:"size:128;not null" json:"local_part"`
	Password      string     `gorm:"size:255" json:"password"`
	DomainID      uint64     `gorm:"not null" json:"domain_id"`
	ServerID      uint64     `gorm:"not null;index:idx_server" json:"server_id"`
	Status        string     `gorm:"type:enum('active','disabled','recycled','deleting','soft_deleted','purged');default:active;index:idx_status" json:"status"`
	SyncStatus    string     `gorm:"type:enum('pending','synced','sync_failed');default:pending;index:idx_sync_status" json:"sync_status"`
	SyncError     string     `gorm:"type:text" json:"sync_error,omitempty"`
	RetentionDays int        `gorm:"not null;default:30" json:"retention_days"`
	CreatedAt     time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
	SyncedAt      *time.Time `json:"synced_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	DisabledAt    *time.Time `json:"disabled_at"`
	RecycledAt    *time.Time `json:"recycled_at"`
	// DeleteRequestedAt 删除请求发起时间，Watchdog 据此判定超时（>15min）重新下发 DELETE。
	DeleteRequestedAt *time.Time `json:"delete_requested_at,omitempty"`

	// 关联
	Domain Domain     `gorm:"foreignKey:DomainID" json:"domain,omitempty"`
	Server MailServer `gorm:"foreignKey:ServerID" json:"server,omitempty"`
}

// OrderMailboxMapping 订单与邮箱账号的绑定关系。当前主线按 1:1 使用，
// 后续可自然扩展为一个订单绑定多个账号、一个账号绑定多个订单。
type OrderMailboxMapping struct {
	ID               uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	OrderID          string         `gorm:"size:128;not null;index:idx_order;uniqueIndex:uk_order_mailbox" json:"order_id"`
	MailboxAccountID uint64         `gorm:"not null;index:idx_mailbox_account;uniqueIndex:uk_order_mailbox" json:"mailbox_account_id"`
	CreatedAt        time.Time      `gorm:"autoCreateTime" json:"created_at"`
	MailboxAccount   MailboxAccount `gorm:"foreignKey:MailboxAccountID" json:"mailbox_account,omitempty"`
}

// OrderMailbox is the legacy 1:1 table kept for backward compatibility and
// historical data migration. New code should use MailboxAccount plus
// OrderMailboxMapping.
type OrderMailbox struct {
	ID            uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	OrderID       string     `gorm:"uniqueIndex:uk_order;size:128;not null" json:"order_id"`
	EmailAddress  string     `gorm:"size:191;not null;index:idx_email" json:"email_address"`
	LocalPart     string     `gorm:"size:128;not null" json:"local_part"`
	Password      string     `gorm:"size:255" json:"password"`
	DomainID      uint64     `gorm:"not null" json:"domain_id"`
	ServerID      uint64     `gorm:"not null;index:idx_server" json:"server_id"`
	Status        string     `gorm:"type:enum('active','disabled','recycled');default:active;index:idx_status" json:"status"`
	SyncStatus    string     `gorm:"type:enum('pending','synced','sync_failed');default:pending;index:idx_sync_status" json:"sync_status"`
	SyncError     string     `gorm:"type:text" json:"sync_error,omitempty"`
	RetentionDays int        `gorm:"not null;default:30" json:"retention_days"`
	CreatedAt     time.Time  `gorm:"autoCreateTime" json:"created_at"`
	SyncedAt      *time.Time `json:"synced_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	DisabledAt    *time.Time `json:"disabled_at"`
	RecycledAt    *time.Time `json:"recycled_at"`

	Domain Domain     `gorm:"foreignKey:DomainID" json:"domain,omitempty"`
	Server MailServer `gorm:"foreignKey:ServerID" json:"server,omitempty"`
}

// FilterRule 过滤规则
type FilterRule struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Name      string    `gorm:"size:128;not null" json:"name"`
	RuleType  string    `gorm:"type:enum('whitelist_sender','blacklist_sender','keyword','regex');not null" json:"rule_type"`
	Pattern   string    `gorm:"size:512;not null" json:"pattern"`
	Action    string    `gorm:"type:enum('pass','block','flag');not null;default:pass" json:"action"`
	Priority  int       `gorm:"not null;default:0" json:"priority"`
	Enabled   bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// ApiToken API 鉴权令牌
type ApiToken struct {
	ID         uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Name       string     `gorm:"size:128;not null" json:"name"`
	Token      string     `gorm:"uniqueIndex;size:191;not null" json:"token"`
	Scopes     string     `gorm:"size:512;not null;default:*" json:"scopes"`
	Enabled    bool       `gorm:"not null;default:true" json:"enabled"`
	CreatedAt  time.Time  `gorm:"autoCreateTime" json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// ServerDomain 服务器与域名的 M:N 绑定，记录该服务器对该域的远端同步状态
// （Postfix virtual_mailbox_domains + DKIM）。分配器只使用 status=active 且
// postfix_status=synced 的绑定。详见 docs/design/t4-t5-server-domain-pool-design.md。
type ServerDomain struct {
	ID            uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ServerID      uint64     `gorm:"not null;uniqueIndex:uk_srv_dom" json:"server_id"`
	DomainID      uint64     `gorm:"not null;uniqueIndex:uk_srv_dom;index:idx_dom" json:"domain_id"`
	Status        string     `gorm:"type:enum('active','inactive');default:active;index" json:"status"`
	SyncStatus    string     `gorm:"type:enum('pending','synced','partial','sync_failed');default:pending;index" json:"sync_status"`
	SyncError     string     `gorm:"type:text" json:"sync_error,omitempty"`
	DkimSelector  string     `gorm:"size:64" json:"dkim_selector"`
	DkimPublicKey string     `gorm:"type:text" json:"dkim_public_key,omitempty"`
	PostfixStatus string     `gorm:"type:enum('pending','synced','sync_failed');default:pending" json:"postfix_status"`
	DkimStatus    string     `gorm:"type:enum('pending','synced','sync_failed');default:pending" json:"dkim_status"`
	SyncedAt      *time.Time `json:"synced_at"`
	CreatedAt     time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"autoUpdateTime" json:"updated_at"`

	Server MailServer `gorm:"foreignKey:ServerID" json:"server,omitempty"`
	Domain Domain     `gorm:"foreignKey:DomainID" json:"domain,omitempty"`
}

// TableName 指定表名
func (OrderMailbox) TableName() string        { return "order_mailboxes" }
func (MailboxAccount) TableName() string      { return "mailbox_accounts" }
func (OrderMailboxMapping) TableName() string { return "order_mailbox_mappings" }
func (MailServer) TableName() string          { return "mail_servers" }
func (FilterRule) TableName() string          { return "filter_rules" }
func (ApiToken) TableName() string            { return "api_tokens" }
func (Domain) TableName() string              { return "domains" }
func (ServerDomain) TableName() string        { return "server_domains" }

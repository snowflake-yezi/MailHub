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
	ID                 uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Name               string     `gorm:"size:128;not null" json:"name"`
	NodeUUID           *string    `gorm:"type:char(36);uniqueIndex:uk_mail_server_node_uuid" json:"node_uuid,omitempty"`
	APIHost            string     `gorm:"size:255;not null" json:"api_host"`
	SMTPHost           string     `gorm:"size:255;not null" json:"smtp_host"`
	IMAPHost           string     `gorm:"size:255;not null" json:"imap_host"`
	PublicHost         string     `gorm:"size:255" json:"public_host"`
	MailPublicIPs      []string   `gorm:"column:mail_public_ips_json;serializer:json;type:text" json:"mail_public_ips"`
	Capacity           int        `gorm:"not null;default:5000" json:"capacity"`
	CurrentLoad        int        `gorm:"not null;default:0" json:"current_load"`
	Status             string     `gorm:"type:enum('healthy','degraded','down','draining');default:healthy" json:"status"`
	EnrollmentState    string     `gorm:"size:24;not null;default:legacy_approved;index" json:"enrollment_state"`
	ConnectionState    string     `gorm:"size:24;not null;default:unknown;index" json:"connection_state"`
	ReadinessState     string     `gorm:"size:24;not null;default:unknown;index" json:"readiness_state"`
	AllocationState    string     `gorm:"size:24;not null;default:active;index" json:"allocation_state"`
	TransportMode      string     `gorm:"size:24;not null;default:legacy_http;index" json:"transport_mode"`
	LeaseExpiresAt     *time.Time `gorm:"index" json:"lease_expires_at,omitempty"`
	AgentVersion       string     `gorm:"size:64" json:"agent_version,omitempty"`
	ProtocolVersion    string     `gorm:"size:32" json:"protocol_version,omitempty"`
	Capabilities       []string   `gorm:"column:capabilities_json;serializer:json;type:text" json:"capabilities"`
	LastConnectedAt    *time.Time `json:"last_connected_at,omitempty"`
	LastDisconnectedAt *time.Time `json:"last_disconnected_at,omitempty"`
	LastHeartbeat      *time.Time `json:"last_heartbeat"`
	LastProbeAt        *time.Time `json:"last_probe_at"`
	ProbeFailCount     int        `gorm:"not null;default:0" json:"probe_fail_count"`
	HeartbeatInterval  int        `gorm:"not null;default:30" json:"heartbeat_interval"`
	DesiredRevision    uint64     `gorm:"not null;default:0" json:"desired_revision"`
	AppliedRevision    uint64     `gorm:"not null;default:0" json:"applied_revision"`
	LastApplyError     string     `gorm:"type:text" json:"last_apply_error,omitempty"`
	LastBootID         string     `gorm:"size:64" json:"last_boot_id,omitempty"`
	LastStartedAt      *time.Time `json:"last_started_at,omitempty"`
	ConfigChangedAt    *time.Time `json:"config_changed_at,omitempty"`
	BootIDAtChange     string     `gorm:"size:64" json:"boot_id_at_change,omitempty"`
	LastReloadError    string     `gorm:"type:text" json:"last_reload_error,omitempty"`
	CreatedAt          time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"autoUpdateTime" json:"updated_at"`

	// Domains 该服务器绑定的 active 域名，仅用于列表展示，不落库（transient）。
	Domains       []Domain             `gorm:"-" json:"domains,omitempty"`
	ConfigSummary *ServerConfigSummary `gorm:"-" json:"config_summary,omitempty"`
}

// ServerConfigSummary is the node-level configuration status shown in server lists.
type ServerConfigSummary struct {
	EffectiveValue string     `json:"effective_value"`
	Source         string     `json:"source"`
	HasOverride    bool       `json:"has_override"`
	Status         string     `json:"status"`
	ReportedAt     *time.Time `json:"reported_at,omitempty"`
}

// ServerConfigOverride stores an explicit value for one mail-node.
type ServerConfigOverride struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ServerID    uint64    `gorm:"not null;uniqueIndex:uk_server_config" json:"server_id"`
	ConfigKey   string    `gorm:"size:128;not null;uniqueIndex:uk_server_config" json:"config_key"`
	ConfigValue string    `gorm:"type:text;not null" json:"config_value"`
	ValueType   string    `gorm:"size:32;not null" json:"value_type"`
	UpdatedBy   string    `gorm:"size:128" json:"updated_by,omitempty"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// ServerConfigSnapshot stores the last effective value reported by a mail-node.
type ServerConfigSnapshot struct {
	ID              uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ServerID        uint64     `gorm:"not null;uniqueIndex:uk_server_snapshot" json:"server_id"`
	ConfigKey       string     `gorm:"size:128;not null;uniqueIndex:uk_server_snapshot" json:"config_key"`
	EffectiveValue  string     `gorm:"type:text;not null" json:"effective_value"`
	Source          string     `gorm:"size:32;not null" json:"source"`
	Reloadable      bool       `gorm:"not null;default:false" json:"reloadable"`
	RequiresRestart bool       `gorm:"not null;default:false" json:"requires_restart"`
	AppliedAt       *time.Time `json:"applied_at,omitempty"`
	ReportedAt      time.Time  `gorm:"not null;index" json:"reported_at"`
	DesiredRevision uint64     `gorm:"not null;default:0" json:"desired_revision"`
	AppliedRevision uint64     `gorm:"not null;default:0" json:"applied_revision"`
	BootID          string     `gorm:"size:64" json:"boot_id,omitempty"`
}

// ConfigChangeAudit records an administrator change to a global or node-scoped configuration value.
type ConfigChangeAudit struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Scope           string    `gorm:"size:32;not null;index" json:"scope"`
	ServerID        *uint64   `gorm:"index" json:"server_id,omitempty"`
	ConfigKey       string    `gorm:"size:128;not null;index" json:"config_key"`
	Action          string    `gorm:"size:32;not null" json:"action"`
	OldValue        string    `gorm:"type:text" json:"old_value"`
	NewValue        string    `gorm:"type:text" json:"new_value"`
	Actor           string    `gorm:"size:128;not null" json:"actor"`
	DesiredRevision uint64    `gorm:"not null;default:0" json:"desired_revision"`
	CreatedAt       time.Time `gorm:"autoCreateTime;index" json:"created_at"`
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
	RetentionDays int        `gorm:"not null;default:30" json:"retention_days"` // per-message retention; never expires the mailbox account
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

// AdminUser is the database-backed management console identity.
type AdminUser struct {
	ID                 uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Username           string     `gorm:"uniqueIndex;size:191;not null" json:"username"`
	PasswordHash       string     `gorm:"size:255;not null" json:"-"`
	PasswordAlgo       string     `gorm:"size:32;not null;default:bcrypt" json:"-"`
	MustChangePassword bool       `gorm:"not null;default:false" json:"must_change_password"`
	CredentialVersion  int        `gorm:"not null;default:1" json:"-"`
	Status             string     `gorm:"type:enum('active','disabled');not null;default:active;index" json:"status"`
	PasswordChangedAt  *time.Time `json:"password_changed_at"`
	CreatedAt          time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

// SystemState records one-way initialization state separately from user rows.
type SystemState struct {
	Key       string    `gorm:"primaryKey;size:128" json:"key"`
	Value     string    `gorm:"type:text;not null" json:"value"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
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
	MailboxCount  int64      `gorm:"-" json:"mailbox_count"`

	Server MailServer `gorm:"foreignKey:ServerID" json:"server,omitempty"`
	Domain Domain     `gorm:"foreignKey:DomainID" json:"domain,omitempty"`
}

// SystemConfig 系统动态配置（KV 表，替代硬编码）
type SystemConfig struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ConfigKey    string    `gorm:"uniqueIndex;size:128;not null" json:"config_key"`
	ConfigValue  string    `gorm:"type:text;not null" json:"config_value"`
	ValueType    string    `gorm:"size:32;default:string" json:"value_type"` // string | int | bool | duration | json
	Category     string    `gorm:"size:64;default:general;index" json:"category"`
	Label        string    `gorm:"size:128;default:''" json:"label"`
	Description  string    `gorm:"type:text" json:"description"`
	DefaultValue string    `gorm:"type:text" json:"default_value"`
	Reloadable   bool      `gorm:"default:false" json:"reloadable"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// IntegratedMailbox 集成邮箱（转发目标池）——所有非垃圾邮件汇总转发的目标账号。
// 全局唯一 is_active=true 的记录为当前生效转发目标，与 system_configs.forward.target_address 同步。
type IntegratedMailbox struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	EmailAddress string    `gorm:"size:191;uniqueIndex;not null" json:"email_address"`
	DisplayName  string    `gorm:"size:191" json:"display_name"`
	IsActive     bool      `gorm:"not null;default:false;index" json:"is_active"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// TableName 指定表名
func (SystemConfig) TableName() string        { return "system_configs" }
func (OrderMailbox) TableName() string        { return "order_mailboxes" }
func (MailboxAccount) TableName() string      { return "mailbox_accounts" }
func (OrderMailboxMapping) TableName() string { return "order_mailbox_mappings" }
func (MailServer) TableName() string          { return "mail_servers" }
func (FilterRule) TableName() string          { return "filter_rules" }
func (Domain) TableName() string              { return "domains" }
func (ServerDomain) TableName() string        { return "server_domains" }
func (IntegratedMailbox) TableName() string   { return "integrated_mailboxes" }
func (AdminUser) TableName() string           { return "admin_users" }
func (SystemState) TableName() string         { return "system_state" }
func (ConfigChangeAudit) TableName() string   { return "config_change_audits" }

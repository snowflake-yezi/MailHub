package model

import "time"

// APIApplication is an external system that consumes the public API.
type APIApplication struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement;comment:主键ID" json:"id"`
	Name        string    `gorm:"size:128;not null;uniqueIndex;comment:外部访问名称" json:"name"`
	Description string    `gorm:"size:512;comment:业务用途和负责人说明" json:"description"`
	Enabled     bool      `gorm:"not null;default:true;index;comment:是否启用" json:"enabled"`
	CreatedAt   time.Time `gorm:"autoCreateTime;comment:创建时间" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime;comment:更新时间" json:"updated_at"`
}

// APICredential is a revocable credential owned by one external application.
// The complete token is never persisted.
type APICredential struct {
	ID            uint64          `gorm:"primaryKey;autoIncrement;comment:主键ID" json:"id"`
	ApplicationID uint64          `gorm:"not null;index;uniqueIndex:uk_app_credential_name;comment:所属外部应用ID" json:"application_id"`
	Name          string          `gorm:"size:128;not null;uniqueIndex:uk_app_credential_name;comment:凭证名称" json:"name"`
	TokenPrefix   string          `gorm:"size:32;not null;index;comment:可展示的Token前缀" json:"token_prefix"`
	TokenHash     string          `gorm:"size:64;not null;uniqueIndex;comment:完整Token的SHA-256哈希" json:"-"`
	Enabled       bool            `gorm:"not null;default:true;index;comment:是否启用" json:"enabled"`
	ExpiresAt     *time.Time      `gorm:"index;comment:凭证到期时间" json:"expires_at,omitempty"`
	LastUsedAt    *time.Time      `gorm:"comment:最近调用时间" json:"last_used_at,omitempty"`
	LastUsedIP    string          `gorm:"size:64;comment:最近调用IP" json:"last_used_ip,omitempty"`
	CreatedAt     time.Time       `gorm:"autoCreateTime;comment:创建时间" json:"created_at"`
	UpdatedAt     time.Time       `gorm:"autoUpdateTime;comment:更新时间" json:"updated_at"`
	Application   *APIApplication `gorm:"foreignKey:ApplicationID" json:"-"`
}

// APIPermission is a stable business capability shown in the admin console.
type APIPermission struct {
	Code        string    `gorm:"primaryKey;size:128;comment:稳定业务权限编码" json:"code"`
	GroupName   string    `gorm:"size:64;not null;index;comment:管理端功能分组" json:"group_name"`
	Name        string    `gorm:"size:128;not null;comment:权限名称" json:"name"`
	Description string    `gorm:"size:512;comment:权限说明" json:"description"`
	SortOrder   int       `gorm:"not null;default:0;comment:展示顺序" json:"sort_order"`
	Active      bool      `gorm:"not null;default:true;index;comment:是否为当前有效权限" json:"active"`
	CreatedAt   time.Time `gorm:"autoCreateTime;comment:创建时间" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime;comment:更新时间" json:"updated_at"`
}

// APIResource is one concrete external HTTP route discovered from code.
type APIResource struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement;comment:主键ID" json:"id"`
	Method         string    `gorm:"size:16;not null;uniqueIndex:uk_api_resource;comment:HTTP方法" json:"method"`
	Path           string    `gorm:"size:175;not null;uniqueIndex:uk_api_resource;comment:外部API路径" json:"path"`
	PermissionCode string    `gorm:"size:128;not null;index;comment:所需业务权限编码" json:"permission_code"`
	Name           string    `gorm:"size:128;not null;comment:接口功能名称" json:"name"`
	Status         string    `gorm:"size:16;not null;default:active;index;comment:资源状态active或retired" json:"status"`
	CreatedAt      time.Time `gorm:"autoCreateTime;comment:创建时间" json:"created_at"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime;comment:更新时间" json:"updated_at"`
}

// APIApplicationPermission grants one stable capability to an application.
type APIApplicationPermission struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement;comment:主键ID" json:"id"`
	ApplicationID  uint64    `gorm:"not null;uniqueIndex:uk_app_permission;index;comment:外部应用ID" json:"application_id"`
	PermissionCode string    `gorm:"size:128;not null;uniqueIndex:uk_app_permission;index;comment:业务权限编码" json:"permission_code"`
	CreatedAt      time.Time `gorm:"autoCreateTime;comment:授权时间" json:"created_at"`
}

// APIAccessLog records one authenticated external request.
type APIAccessLog struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement;comment:主键ID" json:"id"`
	ApplicationID  uint64    `gorm:"not null;index;comment:外部应用ID" json:"application_id"`
	CredentialID   uint64    `gorm:"not null;index;comment:调用凭证ID" json:"credential_id"`
	PermissionCode string    `gorm:"size:128;index;comment:本次调用的权限编码" json:"permission_code"`
	Method         string    `gorm:"size:16;not null;comment:HTTP方法" json:"method"`
	Path           string    `gorm:"size:255;not null;comment:匹配的接口路径" json:"path"`
	StatusCode     int       `gorm:"not null;index;comment:HTTP响应状态码" json:"status_code"`
	ClientIP       string    `gorm:"size:64;comment:调用方IP" json:"client_ip"`
	DurationMS     int64     `gorm:"not null;default:0;comment:请求耗时毫秒" json:"duration_ms"`
	CreatedAt      time.Time `gorm:"autoCreateTime;index;comment:调用时间" json:"created_at"`
}

func (APIApplication) TableName() string           { return "api_applications" }
func (APICredential) TableName() string            { return "api_credentials" }
func (APIPermission) TableName() string            { return "api_permissions" }
func (APIResource) TableName() string              { return "api_resources" }
func (APIApplicationPermission) TableName() string { return "api_application_permissions" }
func (APIAccessLog) TableName() string             { return "api_access_logs" }

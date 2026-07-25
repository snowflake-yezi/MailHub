package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/ticket/email-mgmt-system/internal/mailboxaddr"
	nodecontract "github.com/ticket/email-node-contract"
	"gopkg.in/yaml.v3"
)

// Config 全局配置结构
type Config struct {
	Server               ServerConfig      `yaml:"server"`
	Database             DatabaseConfig    `yaml:"database"`
	Auth                 AuthConfig        `yaml:"auth"`
	Domains              []DomainConfig    `yaml:"domains"`
	DefaultRetentionDays int               `yaml:"default_retention_days"`
	Filter               FilterConfig      `yaml:"filter"`
	NodeControl          NodeControlConfig `yaml:"node_control"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Mode string `yaml:"mode"`
}

type DatabaseConfig struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

type AuthConfig struct {
	Tokens       []TokenConfig `yaml:"tokens"` // Deprecated: one-time upgrade verification only.
	SharedSecret string        `yaml:"shared_secret"`
	AdminUser    string        `yaml:"admin_user"`
	AdminPass    string        `yaml:"admin_pass"`
}

type TokenConfig struct {
	Name   string   `yaml:"name"`
	Token  string   `yaml:"token"`
	Scopes []string `yaml:"scopes"`
}

type DomainConfig struct {
	Name string `yaml:"name"`
}

type FilterConfig struct {
	ReloadInterval           int    `yaml:"reload_interval"`
	DefaultAction            string `yaml:"default_action"`
	DefaultFlagSubjectPrefix string `yaml:"default_flag_subject_prefix"`
}

type NodeControlConfig struct {
	Enabled                   bool   `yaml:"enabled"`
	Listen                    string `yaml:"listen"`
	PublicURL                 string `yaml:"public_url"`
	TLSCertFile               string `yaml:"tls_cert_file"`
	TLSKeyFile                string `yaml:"tls_key_file"`
	HeartbeatIntervalSeconds  int    `yaml:"heartbeat_interval_seconds"`
	LeaseTimeoutSeconds       int    `yaml:"lease_timeout_seconds"`
	CommandTimeoutSeconds     int    `yaml:"command_timeout_seconds"`
	DataMaxConcurrencyPerNode int    `yaml:"data_max_concurrency_per_node"`
	DataChunkSize             int    `yaml:"data_chunk_size"`
	LegacyHTTPEnabled         bool   `yaml:"legacy_http_enabled"`
}

// Load 从文件加载配置
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := &Config{
		Server: ServerConfig{
			Port: 8080,
			Mode: "release",
		},
		DefaultRetentionDays: 30,
		Filter: FilterConfig{
			ReloadInterval:           30,
			DefaultAction:            "pass",
			DefaultFlagSubjectPrefix: "[疑似]",
		},
		NodeControl: NodeControlConfig{
			Listen:                    ":8443",
			HeartbeatIntervalSeconds:  30,
			LeaseTimeoutSeconds:       90,
			CommandTimeoutSeconds:     15,
			DataMaxConcurrencyPerNode: 4,
			DataChunkSize:             256 * 1024,
			LegacyHTTPEnabled:         true,
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Docker and orchestrated deployments can inject secrets without rendering
	// them into the mounted YAML file. These variables do not include admin
	// credentials; administrator bootstrap remains an explicit CLI operation.
	if value := os.Getenv("MAILHUB_DATABASE_DSN"); value != "" {
		cfg.Database.DSN = value
	}
	if value := os.Getenv("MAILHUB_SHARED_SECRET"); value != "" {
		cfg.Auth.SharedSecret = value
	}

	return cfg, nil
}

// Validate 校验必要配置项
func (c *Config) Validate() error {
	if c.Database.DSN == "" {
		return fmt.Errorf("database.dsn is required")
	}
	if len(c.Domains) == 0 {
		return fmt.Errorf("at least one domain is required")
	}
	for i := range c.Domains {
		if strings.ContainsAny(c.Domains[i].Name, "\r\n\x00") {
			return fmt.Errorf("domains[%d].name is invalid", i)
		}
		name, err := mailboxaddr.NormalizeDomain(c.Domains[i].Name)
		if err != nil {
			return fmt.Errorf("domains[%d].name is invalid: %w", i, err)
		}
		c.Domains[i].Name = name
	}
	if c.DefaultRetentionDays <= 0 {
		return fmt.Errorf("default_retention_days must be positive")
	}
	if c.NodeControl.Enabled {
		if strings.TrimSpace(c.NodeControl.Listen) == "" {
			return fmt.Errorf("node_control.listen is required when node control is enabled")
		}
		if strings.TrimSpace(c.NodeControl.TLSCertFile) == "" || strings.TrimSpace(c.NodeControl.TLSKeyFile) == "" {
			return fmt.Errorf("node_control TLS certificate and key files are required when node control is enabled")
		}
		if c.NodeControl.HeartbeatIntervalSeconds <= 0 || c.NodeControl.LeaseTimeoutSeconds <= c.NodeControl.HeartbeatIntervalSeconds {
			return fmt.Errorf("node_control lease timeout must be greater than its positive heartbeat interval")
		}
		if c.NodeControl.CommandTimeoutSeconds <= 0 {
			return fmt.Errorf("node_control.command_timeout_seconds must be positive")
		}
		if c.NodeControl.DataMaxConcurrencyPerNode <= 0 {
			return fmt.Errorf("node_control.data_max_concurrency_per_node must be positive")
		}
		if c.NodeControl.DataChunkSize <= 0 || c.NodeControl.DataChunkSize > nodecontract.MaxDataChunkSize {
			return fmt.Errorf("node_control.data_chunk_size must be between 1 and %d", nodecontract.MaxDataChunkSize)
		}
	}
	return nil
}

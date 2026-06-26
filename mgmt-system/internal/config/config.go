package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 全局配置结构
type Config struct {
	Server                ServerConfig         `yaml:"server"`
	Database              DatabaseConfig       `yaml:"database"`
	Auth                  AuthConfig           `yaml:"auth"`
	Domains               []DomainConfig       `yaml:"domains"`
	DefaultRetentionDays  int                  `yaml:"default_retention_days"`
	Filter                FilterConfig         `yaml:"filter"`
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
	Tokens       []TokenConfig `yaml:"tokens"`
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
	ReloadInterval            int    `yaml:"reload_interval"`
	DefaultAction             string `yaml:"default_action"`
	DefaultFlagSubjectPrefix  string `yaml:"default_flag_subject_prefix"`
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
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
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
	if c.DefaultRetentionDays <= 0 {
		return fmt.Errorf("default_retention_days must be positive")
	}
	return nil
}

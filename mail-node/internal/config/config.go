package config

import (
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server       ServerConfig     `yaml:"server"`
	Maildir      MaildirConfig    `yaml:"maildir"`
	Management   ManagementConfig `yaml:"management"`
	Forward      ForwardConfig    `yaml:"forward"`
	Filter       FilterConfig     `yaml:"filter"`
	Node         NodeConfig       `yaml:"node"`
	PublicHost   string           `yaml:"public_host"`
	Postfix      PostfixConfig    `yaml:"postfix"`
	DKIM         DKIMConfig       `yaml:"dkim"`
	SharedSecret string           `yaml:"shared_secret"`
}

type ServerConfig struct {
	Port          int    `yaml:"port"`
	Mode          string `yaml:"mode"`
	AdvertiseHost string `yaml:"advertise_host"` // 对外通告地址（ip:port），用于向 mgmt 自动发现/注册
}

type MaildirConfig struct {
	BasePath string `yaml:"base_path"`
	VmailUID int    `yaml:"vmail_uid"` // Maildir 属主 UID，默认 5000（与虚拟用户保持一致）
	VmailGID int    `yaml:"vmail_gid"` // Maildir 属组 GID，默认 5000
}

type ManagementConfig struct {
	APIURL             string `yaml:"api_url"`
	HeartbeatInterval  int    `yaml:"heartbeat_interval"`
	FilterSyncInterval int    `yaml:"filter_sync_interval"`
}

type ForwardConfig struct {
	SMTPHost      string `yaml:"smtp_host"`
	SMTPUser      string `yaml:"smtp_user"`
	SMTPPass      string `yaml:"smtp_pass"`
	TargetAddress string `yaml:"target_address"`
	SubjectPrefix string `yaml:"subject_prefix"`
	ScanInterval  int    `yaml:"scan_interval"`  // seconds, default 5
	MaxEmailSize  int64  `yaml:"max_email_size"` // bytes, default 10MB
}

type FilterConfig struct {
	DefaultAction     string `yaml:"default_action"`
	FlagSubjectPrefix string `yaml:"flag_subject_prefix"`
}

type NodeConfig struct {
	ID   uint64 `yaml:"id"`
	Name string `yaml:"name"`
}

type PostfixConfig struct {
	VirtualDomainsFile string `yaml:"virtual_domains_file"`
	VmailboxFile       string `yaml:"vmailbox_file"`
}

type DKIMConfig struct {
	Selector     string `yaml:"selector"`
	KeyDir       string `yaml:"key_dir"`
	SigningTable string `yaml:"signing_table"`
	KeyTable     string `yaml:"key_table"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{
		Server: ServerConfig{Port: 8081, Mode: "release"},
		Filter: FilterConfig{DefaultAction: "pass", FlagSubjectPrefix: "[疑似]"},
		DKIM:   DKIMConfig{Selector: "mail"},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Maildir 属主兜底：未配置时默认 5000（标准虚拟用户）
	if cfg.Maildir.VmailUID == 0 {
		cfg.Maildir.VmailUID = 5000
	}
	if cfg.Maildir.VmailGID == 0 {
		cfg.Maildir.VmailGID = 5000
	}
	if cfg.PublicHost == "" {
		cfg.PublicHost = hostWithoutPort(cfg.Forward.SMTPHost)
	}

	return cfg, nil
}

func hostWithoutPort(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return host
	}
	return strings.Trim(addr, "[]")
}

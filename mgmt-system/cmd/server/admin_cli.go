package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ticket/email-mgmt-system/internal/config"
	"github.com/ticket/email-mgmt-system/internal/service"
	"github.com/ticket/email-mgmt-system/internal/store"
)

func runAdminCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("missing admin command (bootstrap or reset-password)")
	}
	switch args[0] {
	case "bootstrap":
		return runBootstrap(args[1:])
	case "bootstrap-from-config":
		return runBootstrapFromConfig(args[1:])
	case "reset-password":
		return runResetPassword(args[1:])
	default:
		return fmt.Errorf("unknown admin command %q", args[0])
	}
}

func runBootstrapFromConfig(args []string) error {
	fs := flag.NewFlagSet("admin bootstrap-from-config", flag.ContinueOnError)
	defaultConfig := os.Getenv("CONFIG_PATH")
	if defaultConfig == "" {
		defaultConfig = "config.yaml"
	}
	configPath := fs.String("config", defaultConfig, "configuration file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Auth.AdminUser) == "" || cfg.Auth.AdminPass == "" {
		return errors.New("legacy auth.admin_user and auth.admin_pass are required")
	}
	db, err := store.New(cfg.Database.DSN, cfg.Server.Mode)
	if err != nil {
		return err
	}
	credentials := service.NewAdminCredentialService(db, cfg.Server.Mode)
	_, err = credentials.BootstrapLegacy(cfg.Auth.AdminUser, cfg.Auth.AdminPass)
	if errors.Is(err, service.ErrAlreadyInitialized) {
		fmt.Println("Admin account already initialized; no changes made.")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("Legacy administrator %q migrated; password change is required after login.\n", cfg.Auth.AdminUser)
	return nil
}

type adminCommandOptions struct {
	configPath   string
	username     string
	password     string
	passwordFile string
	mustChange   bool
}

func parseAdminOptions(name string, args []string) (adminCommandOptions, error) {
	var options adminCommandOptions
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	defaultConfig := os.Getenv("CONFIG_PATH")
	if defaultConfig == "" {
		defaultConfig = "config.yaml"
	}
	fs.StringVar(&options.configPath, "config", defaultConfig, "configuration file path")
	fs.StringVar(&options.username, "username", os.Getenv("MAILHUB_BOOTSTRAP_ADMIN_USERNAME"), "administrator username")
	fs.StringVar(&options.password, "password", os.Getenv("MAILHUB_BOOTSTRAP_ADMIN_PASSWORD"), "administrator password (development only)")
	fs.StringVar(&options.passwordFile, "password-file", os.Getenv("MAILHUB_BOOTSTRAP_ADMIN_PASSWORD_FILE"), "file containing administrator password")
	fs.BoolVar(&options.mustChange, "must-change-password", false, "require password change after login")
	if err := fs.Parse(args); err != nil {
		return options, err
	}
	options.username = strings.TrimSpace(options.username)
	if options.username == "" {
		return options, errors.New("--username is required")
	}
	if options.passwordFile != "" {
		data, err := os.ReadFile(options.passwordFile)
		if err != nil {
			return options, fmt.Errorf("read password file: %w", err)
		}
		options.password = strings.TrimRight(string(data), "\r\n")
	}
	if options.password == "" {
		return options, errors.New("--password-file or --password is required")
	}
	return options, nil
}

func openAdminService(options adminCommandOptions) (*service.AdminCredentialService, error) {
	cfg, err := config.Load(options.configPath)
	if err != nil {
		return nil, err
	}
	if cfg.Database.DSN == "" {
		return nil, errors.New("database.dsn is required")
	}
	db, err := store.New(cfg.Database.DSN, cfg.Server.Mode)
	if err != nil {
		return nil, err
	}
	return service.NewAdminCredentialService(db, cfg.Server.Mode), nil
}

func runBootstrap(args []string) error {
	options, err := parseAdminOptions("admin bootstrap", args)
	if err != nil {
		return err
	}
	credentials, err := openAdminService(options)
	if err != nil {
		return err
	}
	_, err = credentials.Bootstrap(options.username, options.password, options.mustChange)
	if errors.Is(err, service.ErrAlreadyInitialized) {
		fmt.Println("Admin account already initialized; no changes made.")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("Admin account %q initialized.\n", options.username)
	return nil
}

func runResetPassword(args []string) error {
	options, err := parseAdminOptions("admin reset-password", args)
	if err != nil {
		return err
	}
	credentials, err := openAdminService(options)
	if err != nil {
		return err
	}
	if err := credentials.ResetPassword(options.username, options.password, options.mustChange); err != nil {
		return err
	}
	fmt.Printf("Password reset for administrator %q.\n", options.username)
	return nil
}

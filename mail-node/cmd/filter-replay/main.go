package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mail-node/internal/filterdecision"
	"github.com/ticket/email-mail-node/internal/mailparse"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("filter-replay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	emlPath := flags.String("eml", "", "path to an EML file")
	manifestPath := flags.String("manifest", "", "path to a labeled replay manifest")
	mailbox := flags.String("mailbox", "", "delivered mailbox address")
	serverID := flags.Uint64("server-id", 0, "mail node ID")
	manualPath := flags.String("manual-bundle", "", "path to a canonical manual bundle")
	adPath := flags.String("ad-bundle", "", "path to a canonical ad bundle")
	autoQuarantine := flags.Bool("auto-quarantine", false, "allow automatic quarantine decisions")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *manifestPath != "" && *emlPath != "" {
		return fmt.Errorf("--manifest and --eml are mutually exclusive")
	}
	if *manifestPath == "" && (*emlPath == "" || *mailbox == "" || *serverID == 0) {
		return fmt.Errorf("--eml, --mailbox and --server-id are required")
	}
	if *manifestPath != "" && (*serverID == 0 || *adPath == "") {
		return fmt.Errorf("--manifest requires --server-id and --ad-bundle")
	}
	engine := filterdecision.New()
	if *manualPath != "" {
		var bundle filtercontract.ManualBundle
		if err := readContract(*manualPath, &bundle); err != nil {
			return fmt.Errorf("manual bundle: %w", err)
		}
		if err := engine.ApplyManual(bundle); err != nil {
			return fmt.Errorf("compile manual bundle: %w", err)
		}
	}
	var adBundle *filtercontract.AdBundle
	if *adPath != "" {
		bundle := &filtercontract.AdBundle{}
		if err := readContract(*adPath, bundle); err != nil {
			return fmt.Errorf("ad bundle: %w", err)
		}
		if err := engine.ApplyAd(*bundle); err != nil {
			return fmt.Errorf("compile ad bundle: %w", err)
		}
		adBundle = bundle
	}
	if *manifestPath != "" {
		return runManifest(engine, *adBundle, *manifestPath, *serverID, output)
	}
	parsed, err := mailparse.ParseFile(*emlPath, mailparse.Options{
		Mailbox: *mailbox, MaildirBase: filepath.Dir(filepath.Dir(*emlPath)),
		MaildirUniqueName: filepath.Base(*emlPath), ServerID: *serverID,
	})
	if err != nil {
		return err
	}
	stat, err := os.Stat(*emlPath)
	if err != nil {
		return err
	}
	decision, err := engine.Evaluate(parsed.Features, filterdecision.Options{
		AutoQuarantineEnabled: *autoQuarantine, EvaluatedAt: stat.ModTime().UTC(),
	})
	if err != nil {
		return err
	}
	data, err := decision.CanonicalJSON()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, string(data))
	return err
}

func readContract(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return filtercontract.DecodeStrict(data, target)
}

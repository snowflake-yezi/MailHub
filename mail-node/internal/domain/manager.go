package domain

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Config struct {
	PublicHost          string
	Selector            string
	VirtualDomainsFile  string
	VmailboxFile        string
	DKIMKeyDir          string
	DKIMSigningTable    string
	DKIMKeyTable        string
	EnableDKIMProvision bool
}

type Manager struct {
	cfg Config
}

var (
	runOpenDKIMGenkey = func(keyDir, domain, selector string) error {
		cmd := exec.Command("opendkim-genkey", "-D", keyDir, "-d", domain, "-s", selector)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("generate dkim key: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	chownPathRecursive = func(path, owner string) error {
		cmd := exec.Command("chown", "-R", owner, path)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("chown dkim key dir: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	reloadOpenDKIMService = func() error {
		cmd := exec.Command("systemctl", "reload", "opendkim")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("reload opendkim: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
)

type DNSRecord struct {
	Type  string `json:"type"`
	Host  string `json:"host"`
	Value string `json:"value"`
}

type DomainSetup struct {
	Domain        string      `json:"domain"`
	PostfixStatus string      `json:"postfix_status"`
	DKIMStatus    string      `json:"dkim_status"`
	DKIMSelector  string      `json:"dkim_selector"`
	DKIMPublicKey string      `json:"dkim_public_key,omitempty"`
	DKIMError     string      `json:"dkim_error,omitempty"`
	DNSRecords    []DNSRecord `json:"dns_records"`
}

func NewManager(cfg Config) *Manager {
	if cfg.Selector == "" {
		cfg.Selector = "mail"
	}
	if cfg.VmailboxFile == "" {
		cfg.VmailboxFile = "/etc/postfix/vmailbox"
	}
	return &Manager{cfg: cfg}
}

func (m *Manager) AddDomain(name string) (*DomainSetup, error) {
	domain, err := normalizeDomain(name)
	if err != nil {
		return nil, err
	}

	domains, err := m.ListDomains()
	if err != nil {
		return nil, err
	}
	if !containsDomain(domains, domain) {
		domains = append(domains, domain)
		sort.Strings(domains)
		if err := m.writeDomains(domains); err != nil {
			return nil, fmt.Errorf("update postfix virtual domains: %w", err)
		}
	}

	setup := &DomainSetup{
		Domain:        domain,
		PostfixStatus: "synced",
		DKIMStatus:    "pending",
		DKIMSelector:  m.cfg.Selector,
	}

	if m.cfg.EnableDKIMProvision {
		publicKey, dkimErr := m.provisionDKIM(domain)
		if dkimErr != nil {
			setup.DKIMStatus = "sync_failed"
			setup.DKIMError = dkimErr.Error()
		} else {
			setup.DKIMStatus = "synced"
			setup.DKIMPublicKey = publicKey
		}
	} else {
		setup.DKIMStatus = "sync_failed"
		setup.DKIMError = "dkim provisioning disabled"
	}
	setup.DNSRecords = m.dnsRecords(domain, setup.DKIMSelector, setup.DKIMPublicKey)
	return setup, nil
}

func (m *Manager) ListDomains() ([]string, error) {
	if m.cfg.VirtualDomainsFile != "" {
		return m.readDomainsFile()
	}
	out, err := exec.Command("postconf", "-h", "virtual_mailbox_domains").Output()
	if err != nil {
		return nil, err
	}
	return parseDomains(string(out)), nil
}

func (m *Manager) RemoveDomain(name string) error {
	domain, err := normalizeDomain(name)
	if err != nil {
		return err
	}
	if has, err := m.hasMailboxForDomain(domain); err != nil {
		return err
	} else if has {
		return fmt.Errorf("domain has mailbox accounts")
	}

	domains, err := m.ListDomains()
	if err != nil {
		return err
	}
	next := make([]string, 0, len(domains))
	for _, d := range domains {
		if !strings.EqualFold(d, domain) {
			next = append(next, d)
		}
	}
	if len(next) != len(domains) {
		if err := m.writeDomains(next); err != nil {
			return err
		}
	}
	if m.cfg.EnableDKIMProvision {
		return m.removeDKIM(domain)
	}
	return nil
}

func (m *Manager) readDomainsFile() ([]string, error) {
	data, err := os.ReadFile(m.cfg.VirtualDomainsFile)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return parseDomains(string(data)), nil
}

func (m *Manager) writeDomains(domains []string) error {
	if m.cfg.VirtualDomainsFile != "" {
		if err := os.MkdirAll(filepath.Dir(m.cfg.VirtualDomainsFile), 0755); err != nil {
			return err
		}
		return os.WriteFile(m.cfg.VirtualDomainsFile, []byte(strings.Join(domains, "\n")+"\n"), 0644)
	}
	value := strings.Join(domains, ", ")
	if err := exec.Command("postconf", "-e", "virtual_mailbox_domains = "+value).Run(); err != nil {
		return err
	}
	return exec.Command("postfix", "reload").Run()
}

func (m *Manager) hasMailboxForDomain(domain string) (bool, error) {
	f, err := os.Open(m.cfg.VmailboxFile)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()

	suffix := "@" + strings.ToLower(domain)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.HasSuffix(strings.ToLower(fields[0]), suffix) {
			return true, nil
		}
	}
	return false, scanner.Err()
}

func (m *Manager) provisionDKIM(domain string) (string, error) {
	if err := m.validateDKIMConfig(); err != nil {
		return "", err
	}

	selector := m.cfg.Selector
	domainKeyDir := filepath.Join(m.cfg.DKIMKeyDir, domain)
	privateKey := filepath.Join(domainKeyDir, selector+".private")
	txtFile := filepath.Join(domainKeyDir, selector+".txt")

	if err := os.MkdirAll(domainKeyDir, 0750); err != nil {
		return "", fmt.Errorf("create dkim key dir: %w", err)
	}

	if !fileExists(privateKey) || !fileExists(txtFile) {
		if err := runOpenDKIMGenkey(domainKeyDir, domain, selector); err != nil {
			return "", err
		}
	}

	publicKey, err := readOpenDKIMTXT(txtFile)
	if err != nil {
		return "", err
	}

	if err := upsertTableLine(m.cfg.DKIMSigningTable, "*@"+domain, selector+"._domainkey."+domain); err != nil {
		return "", fmt.Errorf("update SigningTable: %w", err)
	}
	if err := upsertTableLine(m.cfg.DKIMKeyTable, selector+"._domainkey."+domain, domain+":"+selector+":"+privateKey); err != nil {
		return "", fmt.Errorf("update KeyTable: %w", err)
	}

	if err := chownRecursive(domainKeyDir, "opendkim:opendkim"); err != nil {
		return "", err
	}
	if err := reloadOpenDKIM(); err != nil {
		return "", err
	}
	return publicKey, nil
}

func (m *Manager) removeDKIM(domain string) error {
	if err := m.validateDKIMConfig(); err != nil {
		return err
	}
	selector := m.cfg.Selector
	if err := removeTableKeys(m.cfg.DKIMSigningTable, "*@"+domain); err != nil {
		return fmt.Errorf("update SigningTable: %w", err)
	}
	if err := removeTableKeys(m.cfg.DKIMKeyTable, selector+"._domainkey."+domain); err != nil {
		return fmt.Errorf("update KeyTable: %w", err)
	}
	return reloadOpenDKIM()
}

func (m *Manager) validateDKIMConfig() error {
	missing := []string{}
	if strings.TrimSpace(m.cfg.DKIMKeyDir) == "" {
		missing = append(missing, "dkim.key_dir")
	}
	if strings.TrimSpace(m.cfg.DKIMSigningTable) == "" {
		missing = append(missing, "dkim.signing_table")
	}
	if strings.TrimSpace(m.cfg.DKIMKeyTable) == "" {
		missing = append(missing, "dkim.key_table")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing DKIM config: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (m *Manager) dnsRecords(domain, selector, publicKey string) []DNSRecord {
	publicHost := strings.TrimSpace(m.cfg.PublicHost)
	if publicHost == "" {
		publicHost = "mail." + domain
	}
	records := []DNSRecord{
		{Type: "MX", Host: domain, Value: publicHost},
		{Type: "TXT", Host: domain, Value: "v=spf1 a mx ~all"},
		{Type: "TXT", Host: "_dmarc." + domain, Value: "v=DMARC1; p=quarantine"},
	}
	if publicKey != "" {
		records = append(records, DNSRecord{Type: "TXT", Host: selector + "._domainkey." + domain, Value: publicKey})
	} else {
		records = append(records, DNSRecord{Type: "TXT", Host: selector + "._domainkey." + domain, Value: "DKIM public key pending"})
	}
	return records
}

func normalizeDomain(name string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(name))
	d = strings.TrimSuffix(d, ".")
	if d == "" || strings.ContainsAny(d, "/\\@ ") || !strings.Contains(d, ".") {
		return "", fmt.Errorf("invalid domain")
	}
	return d, nil
}

func parseDomains(raw string) []string {
	seen := map[string]bool{}
	var domains []string
	split := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	for _, item := range split {
		d, err := normalizeDomain(item)
		if err != nil || seen[d] {
			continue
		}
		seen[d] = true
		domains = append(domains, d)
	}
	sort.Strings(domains)
	return domains
}

func containsDomain(domains []string, domain string) bool {
	for _, d := range domains {
		if strings.EqualFold(d, domain) {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readOpenDKIMTXT(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read dkim txt: %w", err)
	}
	value := concatQuotedStrings(string(data))
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("dkim txt has no quoted value")
	}
	return value, nil
}

func concatQuotedStrings(raw string) string {
	var b strings.Builder
	inQuote := false
	escaped := false
	for _, r := range raw {
		switch {
		case escaped:
			if inQuote {
				b.WriteRune(r)
			}
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case inQuote:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func upsertTableLine(path, key, value string) error {
	return rewriteTable(path, func(line string) (string, bool) {
		if tableLineKey(line) == key {
			return key + " " + value, true
		}
		return line, true
	}, key+" "+value)
}

func removeTableKeys(path string, keys ...string) error {
	keySet := map[string]bool{}
	for _, key := range keys {
		keySet[key] = true
	}
	return rewriteTable(path, func(line string) (string, bool) {
		if keySet[tableLineKey(line)] {
			return "", false
		}
		return line, true
	}, "")
}

func rewriteTable(path string, fn func(string) (string, bool), appendLine string) error {
	lines, err := readLines(path)
	if err != nil {
		return err
	}

	found := false
	next := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		if appendLine != "" && tableLineKey(line) == tableLineKey(appendLine) {
			found = true
		}
		rewritten, keep := fn(line)
		if keep {
			next = append(next, rewritten)
		}
	}
	if appendLine != "" && !found {
		next = append(next, appendLine)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	content := strings.Join(next, "\n")
	if content != "" {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0644)
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func tableLineKey(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return ""
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func chownRecursive(path, owner string) error {
	return chownPathRecursive(path, owner)
}

func reloadOpenDKIM() error {
	return reloadOpenDKIMService()
}

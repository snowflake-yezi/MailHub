package domain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddDomainProvisionsDKIM(t *testing.T) {
	tmp := t.TempDir()
	restore := stubDKIMCommands(t, "v=DKIM1; k=rsa; p=ABCDEF123")
	defer restore()

	m := NewManager(Config{
		PublicHost:          "mail.example.net",
		Selector:            "mail-s2",
		VirtualDomainsFile:  filepath.Join(tmp, "postfix", "virtual_domains"),
		VmailboxFile:        filepath.Join(tmp, "postfix", "vmailbox"),
		DKIMKeyDir:          filepath.Join(tmp, "opendkim", "keys"),
		DKIMSigningTable:    filepath.Join(tmp, "opendkim", "SigningTable"),
		DKIMKeyTable:        filepath.Join(tmp, "opendkim", "KeyTable"),
		EnableDKIMProvision: true,
	})

	setup, err := m.AddDomain("Example.COM.")
	if err != nil {
		t.Fatalf("AddDomain() error = %v", err)
	}
	if setup.Domain != "example.com" {
		t.Fatalf("domain = %q", setup.Domain)
	}
	if setup.DKIMStatus != "synced" {
		t.Fatalf("DKIMStatus = %q", setup.DKIMStatus)
	}
	if setup.DKIMPublicKey != "v=DKIM1; k=rsa; p=ABCDEF123" {
		t.Fatalf("DKIMPublicKey = %q", setup.DKIMPublicKey)
	}

	assertFileContains(t, filepath.Join(tmp, "opendkim", "SigningTable"), "*@example.com mail-s2._domainkey.example.com")
	assertFileContains(t, filepath.Join(tmp, "opendkim", "KeyTable"), "mail-s2._domainkey.example.com example.com:mail-s2:"+filepath.Join(tmp, "opendkim", "keys", "example.com", "mail-s2.private"))
	assertDNSRecord(t, setup.DNSRecords, "MX", "example.com", "mail.example.net")
	assertDNSRecord(t, setup.DNSRecords, "TXT", "mail-s2._domainkey.example.com", "v=DKIM1; k=rsa; p=ABCDEF123")
}

func TestAddDomainDKIMTableUpsertIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	restore := stubDKIMCommands(t, "v=DKIM1; k=rsa; p=FIRST")
	defer restore()

	signingTable := filepath.Join(tmp, "SigningTable")
	keyTable := filepath.Join(tmp, "KeyTable")
	m := NewManager(Config{
		Selector:            "mail-s2",
		VirtualDomainsFile:  filepath.Join(tmp, "virtual_domains"),
		DKIMKeyDir:          filepath.Join(tmp, "keys"),
		DKIMSigningTable:    signingTable,
		DKIMKeyTable:        keyTable,
		EnableDKIMProvision: true,
	})

	if _, err := m.AddDomain("example.com"); err != nil {
		t.Fatalf("first AddDomain() error = %v", err)
	}
	if _, err := m.AddDomain("example.com"); err != nil {
		t.Fatalf("second AddDomain() error = %v", err)
	}

	if count := strings.Count(readFile(t, signingTable), "*@example.com "); count != 1 {
		t.Fatalf("SigningTable has %d example rows", count)
	}
	if count := strings.Count(readFile(t, keyTable), "mail-s2._domainkey.example.com "); count != 1 {
		t.Fatalf("KeyTable has %d example rows", count)
	}
}

func TestRemoveDomainRemovesDKIMTables(t *testing.T) {
	tmp := t.TempDir()
	restore := stubDKIMCommands(t, "v=DKIM1; k=rsa; p=ABCDEF123")
	defer restore()

	signingTable := filepath.Join(tmp, "SigningTable")
	keyTable := filepath.Join(tmp, "KeyTable")
	m := NewManager(Config{
		Selector:            "mail-s2",
		VirtualDomainsFile:  filepath.Join(tmp, "virtual_domains"),
		VmailboxFile:        filepath.Join(tmp, "vmailbox"),
		DKIMKeyDir:          filepath.Join(tmp, "keys"),
		DKIMSigningTable:    signingTable,
		DKIMKeyTable:        keyTable,
		EnableDKIMProvision: true,
	})

	if _, err := m.AddDomain("example.com"); err != nil {
		t.Fatalf("AddDomain() error = %v", err)
	}
	if err := m.RemoveDomain("example.com"); err != nil {
		t.Fatalf("RemoveDomain() error = %v", err)
	}
	if strings.Contains(readFile(t, signingTable), "example.com") {
		t.Fatalf("SigningTable still contains example.com")
	}
	if strings.Contains(readFile(t, keyTable), "example.com") {
		t.Fatalf("KeyTable still contains example.com")
	}
}

func TestRemoveDomainRejectsDomainWithMailboxes(t *testing.T) {
	tmp := t.TempDir()
	restore := stubDKIMCommands(t, "v=DKIM1; k=rsa; p=ABCDEF123")
	defer restore()

	vmailbox := filepath.Join(tmp, "vmailbox")
	m := NewManager(Config{
		Selector:            "mail-s2",
		VirtualDomainsFile:  filepath.Join(tmp, "virtual_domains"),
		VmailboxFile:        vmailbox,
		DKIMKeyDir:          filepath.Join(tmp, "keys"),
		DKIMSigningTable:    filepath.Join(tmp, "SigningTable"),
		DKIMKeyTable:        filepath.Join(tmp, "KeyTable"),
		EnableDKIMProvision: true,
	})

	if _, err := m.AddDomain("example.com"); err != nil {
		t.Fatalf("AddDomain() error = %v", err)
	}
	if err := os.WriteFile(vmailbox, []byte("user@example.com example.com/user/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := m.RemoveDomain("example.com")
	if err == nil || !strings.Contains(err.Error(), "domain has mailbox accounts") {
		t.Fatalf("RemoveDomain() error = %v, want mailbox rejection", err)
	}
	assertFileContains(t, filepath.Join(tmp, "virtual_domains"), "example.com")
	assertFileContains(t, filepath.Join(tmp, "SigningTable"), "*@example.com mail-s2._domainkey.example.com")
}

func TestRemoveDomainIsIdempotentAndStillCleansDKIMResidue(t *testing.T) {
	tmp := t.TempDir()
	restore := stubDKIMCommands(t, "v=DKIM1; k=rsa; p=ABCDEF123")
	defer restore()

	signingTable := filepath.Join(tmp, "SigningTable")
	keyTable := filepath.Join(tmp, "KeyTable")
	if err := os.WriteFile(signingTable, []byte("*@example.com mail-s2._domainkey.example.com\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyTable, []byte("mail-s2._domainkey.example.com example.com:mail-s2:/tmp/mail-s2.private\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(Config{
		Selector:            "mail-s2",
		VirtualDomainsFile:  filepath.Join(tmp, "virtual_domains"),
		VmailboxFile:        filepath.Join(tmp, "vmailbox"),
		DKIMKeyDir:          filepath.Join(tmp, "keys"),
		DKIMSigningTable:    signingTable,
		DKIMKeyTable:        keyTable,
		EnableDKIMProvision: true,
	})

	if err := m.RemoveDomain("example.com"); err != nil {
		t.Fatalf("RemoveDomain() error = %v", err)
	}
	if strings.Contains(readFile(t, signingTable), "example.com") {
		t.Fatalf("SigningTable still contains example.com")
	}
	if strings.Contains(readFile(t, keyTable), "example.com") {
		t.Fatalf("KeyTable still contains example.com")
	}
}

func TestReadOpenDKIMTXTFoldsQuotedChunks(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "mail-s2.txt")
	content := `mail-s2._domainkey IN TXT ( "v=DKIM1; k=rsa; "
	"p=ABC"
	"DEF" ) ; ----- DKIM key mail-s2 for example.com`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := readOpenDKIMTXT(path)
	if err != nil {
		t.Fatalf("readOpenDKIMTXT() error = %v", err)
	}
	if got != "v=DKIM1; k=rsa; p=ABCDEF" {
		t.Fatalf("readOpenDKIMTXT() = %q", got)
	}
}

func stubDKIMCommands(t *testing.T, publicKey string) func() {
	t.Helper()
	oldGenkey := runOpenDKIMGenkey
	oldChown := chownPathRecursive
	oldReload := reloadOpenDKIMService

	runOpenDKIMGenkey = func(keyDir, domain, selector string) error {
		if err := os.MkdirAll(keyDir, 0750); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(keyDir, selector+".private"), []byte("private"), 0600); err != nil {
			return err
		}
		txt := selector + "._domainkey IN TXT ( \"" + publicKey + "\" )"
		return os.WriteFile(filepath.Join(keyDir, selector+".txt"), []byte(txt), 0644)
	}
	chownPathRecursive = func(path, owner string) error { return nil }
	reloadOpenDKIMService = func() error { return nil }

	return func() {
		runOpenDKIMGenkey = oldGenkey
		chownPathRecursive = oldChown
		reloadOpenDKIMService = oldReload
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	if !strings.Contains(readFile(t, path), want) {
		t.Fatalf("%s does not contain %q", path, want)
	}
}

func assertDNSRecord(t *testing.T, records []DNSRecord, typ, host, value string) {
	t.Helper()
	for _, r := range records {
		if r.Type == typ && r.Host == host && r.Value == value {
			return
		}
	}
	t.Fatalf("missing DNS record %s %s %s in %#v", typ, host, value, records)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

package handler

import (
	"testing"

	"github.com/ticket/email-mgmt-system/internal/model"
)

func TestDNSRecordsForMXHostUsesOnlyManagedPublicIPs(t *testing.T) {
	records := []DNSRecord{
		{Type: "MX", Host: "example.com", Value: "mail.example.com"},
		{Type: "TXT", Host: "example.com", Value: "v=spf1 a mx ~all"},
	}

	got := dnsRecordsForMXHost(records, "example.com", "mx.example.com", []string{"203.0.113.10", "2001:db8::10"})

	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	if got[0] != (DNSRecord{Type: "A", Host: "mx.example.com", Value: "203.0.113.10"}) {
		t.Fatalf("first record = %#v", got[0])
	}
	if got[1] != (DNSRecord{Type: "AAAA", Host: "mx.example.com", Value: "2001:db8::10"}) {
		t.Fatalf("second record = %#v", got[1])
	}
	if got[2] != (DNSRecord{Type: "MX", Host: "example.com", Value: "mx.example.com"}) {
		t.Fatalf("mx record = %#v", got[2])
	}
}

func TestDNSRecordsForMXHostDoesNotInferAddressWhenPublicIPsAreEmpty(t *testing.T) {
	records := []DNSRecord{
		{Type: "MX", Host: "example.com", Value: "mail.example.com"},
	}

	got := dnsRecordsForMXHost(records, "example.com", "mx.example.com", nil)

	if len(got) != len(records) {
		t.Fatalf("len = %d, want %d", len(got), len(records))
	}
	if got[0] != (DNSRecord{Type: "MX", Host: "example.com", Value: "mx.example.com"}) {
		t.Fatalf("mx record = %#v", got[0])
	}
}

func TestNormalizeServerAddressesCanonicalizesManagedMailAddresses(t *testing.T) {
	server := &model.MailServer{
		Name: " node-a ", APIHost: " 10.0.0.2:8081 ", SMTPHost: " smtp.example.com ", IMAPHost: " imap.example.com ",
		PublicHost: "MAIL.Example.COM.", MailPublicIPs: []string{"203.0.113.10", "2001:0db8::10", "203.0.113.10"},
	}
	if err := normalizeServerAddresses(server); err != nil {
		t.Fatal(err)
	}
	if server.Name != "node-a" || server.APIHost != "10.0.0.2:8081" || server.PublicHost != "mail.example.com" {
		t.Fatalf("normalized server = %+v", server)
	}
	if len(server.MailPublicIPs) != 2 || server.MailPublicIPs[0] != "203.0.113.10" || server.MailPublicIPs[1] != "2001:db8::10" {
		t.Fatalf("public IPs = %#v", server.MailPublicIPs)
	}
}

func TestNormalizeServerAddressesRejectsControlAddressAsPublicIP(t *testing.T) {
	server := &model.MailServer{MailPublicIPs: []string{"10.0.0.2:8081"}}
	if err := normalizeServerAddresses(server); err == nil {
		t.Fatal("host:port was accepted as a mail public IP")
	}
}

func TestNormalizeMXHost(t *testing.T) {
	tests := map[string]string{
		"":                 "mail.example.com",
		"mail":             "mail.example.com",
		"mx":               "mx.example.com",
		"mail.example.com": "mail.example.com",
		"@":                "example.com",
	}
	for input, want := range tests {
		got, err := normalizeMXHost(input, "example.com")
		if err != nil {
			t.Fatalf("normalizeMXHost(%q) error: %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeMXHost(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestValidDomainNameRejectsPathAndConfigInjection(t *testing.T) {
	for _, value := range []string{"example.com", "mail.example.com", "a-b.example"} {
		if !validDomainName(value) {
			t.Fatalf("valid domain %q rejected", value)
		}
	}
	for _, value := range []string{"..", ".example.com", "example..com", "example.com\n", "example.com\nother.com", "bad_domain.com", "-bad.example"} {
		if validDomainName(value) {
			t.Fatalf("invalid domain %q accepted", value)
		}
	}
}

func TestNormalizeMXHostRejectsExternalHost(t *testing.T) {
	if _, err := normalizeMXHost("mail.other.com", "example.com"); err == nil {
		t.Fatal("expected error")
	}
}

func TestDeriveHostDefaults(t *testing.T) {
	// 空 SMTP/IMAP 从 api_host 推导
	srv := &model.MailServer{APIHost: "10.0.0.2:8081"}
	deriveHostDefaults(srv)
	if srv.SMTPHost != "10.0.0.2" {
		t.Fatalf("SMTPHost = %q, want 10.0.0.2", srv.SMTPHost)
	}
	if srv.IMAPHost != "10.0.0.2" {
		t.Fatalf("IMAPHost = %q, want 10.0.0.2", srv.IMAPHost)
	}

	// 已有值不被覆盖
	srv2 := &model.MailServer{APIHost: "10.0.0.2:8081", SMTPHost: "mail.example.com"}
	deriveHostDefaults(srv2)
	if srv2.SMTPHost != "mail.example.com" {
		t.Fatalf("SMTPHost overwritten = %q, want mail.example.com", srv2.SMTPHost)
	}
	if srv2.IMAPHost != "10.0.0.2" {
		t.Fatalf("IMAPHost = %q, want 10.0.0.2", srv2.IMAPHost)
	}
}

func TestAttachDomains(t *testing.T) {
	servers := []model.MailServer{{ID: 1}, {ID: 2}, {ID: 3}}
	bindings := []model.ServerDomain{
		{ServerID: 1, Domain: model.Domain{Name: "a.example.com"}},
		{ServerID: 1, Domain: model.Domain{Name: "b.example.com"}},
		{ServerID: 2, Domain: model.Domain{Name: "c.example.com"}},
		// server 3 无绑定
	}
	attachDomains(servers, bindings)
	if len(servers[0].Domains) != 2 {
		t.Fatalf("server 1 domains = %d, want 2", len(servers[0].Domains))
	}
	if servers[0].Domains[0].Name != "a.example.com" {
		t.Fatalf("server 1 first domain = %q", servers[0].Domains[0].Name)
	}
	if len(servers[1].Domains) != 1 {
		t.Fatalf("server 2 domains = %d, want 1", len(servers[1].Domains))
	}
	if servers[2].Domains != nil {
		t.Fatalf("server 3 domains = %v, want nil", servers[2].Domains)
	}
}

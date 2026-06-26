package handler

import "testing"

func TestDNSRecordsForMXHostPrependsARecordAndRewritesMX(t *testing.T) {
	records := []DNSRecord{
		{Type: "MX", Host: "example.com", Value: "mail.example.com"},
		{Type: "TXT", Host: "example.com", Value: "v=spf1 a mx ~all"},
	}

	got := dnsRecordsForMXHost(records, "example.com", "mx.example.com", "203.0.113.10:8081")

	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0] != (DNSRecord{Type: "A", Host: "mx.example.com", Value: "203.0.113.10"}) {
		t.Fatalf("first record = %#v", got[0])
	}
	if got[1] != (DNSRecord{Type: "MX", Host: "example.com", Value: "mx.example.com"}) {
		t.Fatalf("mx record = %#v", got[1])
	}
}

func TestDNSRecordsForMXHostSkipsARecordForNonIPAPIHost(t *testing.T) {
	records := []DNSRecord{
		{Type: "MX", Host: "example.com", Value: "mail.example.com"},
	}

	got := dnsRecordsForMXHost(records, "example.com", "mx.example.com", "mail-node.example.com:8081")

	if len(got) != len(records) {
		t.Fatalf("len = %d, want %d", len(got), len(records))
	}
	if got[0] != (DNSRecord{Type: "MX", Host: "example.com", Value: "mx.example.com"}) {
		t.Fatalf("mx record = %#v", got[0])
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

func TestNormalizeMXHostRejectsExternalHost(t *testing.T) {
	if _, err := normalizeMXHost("mail.other.com", "example.com"); err == nil {
		t.Fatal("expected error")
	}
}

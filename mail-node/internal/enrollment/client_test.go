package enrollment

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientRequiresHTTPS(t *testing.T) {
	if _, err := NewClient("http://management.example", ""); err == nil {
		t.Fatal("plain HTTP enrollment URL was accepted")
	}
}

func TestClientUsesBodyTokenRequestSecretAndPinnedCA(t *testing.T) {
	const requestSecret = "mhr_request-secret"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/node-enrollments/claim":
			if strings.Contains(request.URL.RawQuery, "enrollment-secret") || request.Header.Get("Authorization") != "" {
				t.Error("claim leaked token into URL/header")
			}
			payload, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(payload), `"token":"enrollment-secret"`) {
				t.Errorf("claim body = %s", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"request": map[string]any{"id": "request-1", "state": "pending"}, "request_secret": requestSecret,
			}})
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/node-enrollments/requests/request-1":
			if request.Header.Get("Authorization") != "Request "+requestSecret {
				t.Errorf("status auth = %q", request.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"id": "request-1", "state": "approved"}})
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/node-enrollments/requests/request-1/complete":
			if request.Header.Get("Authorization") != "Request "+requestSecret {
				t.Errorf("complete auth = %q", request.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"credential": "mhn_runtime", "metadata": map[string]any{"credential_prefix": "mhn_runtime", "version": 1},
			}})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	certificate, err := x509.ParseCertificate(server.Certificate().Raw)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(t.TempDir(), "management-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(server.URL, caPath)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := client.Claim(context.Background(), ClaimInput{Token: "enrollment-secret", NodeUUID: "uuid", Name: "node"})
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(context.Background(), claim.Request.ID, claim.RequestSecret)
	if err != nil || status.State != "approved" {
		t.Fatalf("status = %+v, error = %v", status, err)
	}
	completed, err := client.Complete(context.Background(), claim.Request.ID, claim.RequestSecret)
	if err != nil || completed.Credential != "mhn_runtime" {
		t.Fatalf("complete = %+v, error = %v", completed, err)
	}
}

package filterpolicy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mail-node/internal/filterdecision"
)

func TestSyncOnceAppliesBothBundlesAndReportsState(t *testing.T) {
	manual, ad := policyBundles(t)
	var mu sync.Mutex
	states := []nodeState{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/manual"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": manual})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/ad"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": ad})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/filter-node-states"):
			var state nodeState
			_ = json.NewDecoder(r.Body).Decode(&state)
			mu.Lock()
			states = append(states, state)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	engine := filterdecision.New()
	client := NewClient(server.URL, "secret", engine, func() (uint64, string) { return 7, "boot" })
	if err := client.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if engine.State(filtercontract.PolicyManual).Revision != manual.Revision || engine.State(filtercontract.PolicyAd).Revision != ad.Revision {
		t.Fatalf("states = %#v / %#v", engine.State(filtercontract.PolicyManual), engine.State(filtercontract.PolicyAd))
	}
	secondEngine := filterdecision.New()
	secondClient := NewClient(server.URL, "secret", secondEngine, func() (uint64, string) { return 8, "second-boot" })
	if err := secondClient.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{filtercontract.PolicyManual, filtercontract.PolicyAd} {
		if engine.State(kind) != secondEngine.State(kind) {
			t.Fatalf("%s checksum diverged across clients: %#v / %#v", kind, engine.State(kind), secondEngine.State(kind))
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(states) != 4 || states[0].NodeID != 7 || states[0].BootID != "boot" || states[0].LastError != "" || states[2].NodeID != 8 {
		t.Fatalf("reports = %#v", states)
	}
}

func TestInvalidRefreshReportsErrorAndKeepsSnapshot(t *testing.T) {
	manual, _ := policyBundles(t)
	bad := manual
	bad.Revision++
	bad.Checksum = strings.Repeat("0", 64)
	serveBad := false
	lastError := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var state nodeState
			_ = json.NewDecoder(r.Body).Decode(&state)
			if state.PolicyKind == filtercontract.PolicyManual {
				lastError = state.LastError
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/ad") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		value := manual
		if serveBad {
			value = bad
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": value})
	}))
	defer server.Close()
	engine := filterdecision.New()
	client := NewClient(server.URL, "secret", engine, func() (uint64, string) { return 7, "boot" })
	if err := client.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	serveBad = true
	if err := client.SyncOnce(context.Background()); err == nil {
		t.Fatal("invalid refresh succeeded")
	}
	if engine.State(filtercontract.PolicyManual).Revision != manual.Revision || lastError == "" {
		t.Fatalf("state/error = %#v / %q", engine.State(filtercontract.PolicyManual), lastError)
	}
}

func policyBundles(t *testing.T) (filtercontract.ManualBundle, filtercontract.AdBundle) {
	t.Helper()
	manual := filtercontract.ManualBundle{
		SchemaVersion: 1, PolicyKind: filtercontract.PolicyManual, Revision: 2,
		Rules: []filtercontract.ManualRule{{LogicalID: "manual", Name: "Manual", ScopeType: "global", Action: filtercontract.ActionAllow, Priority: 1, Mode: filtercontract.ModeEnforce, Source: "manual", Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("ok"), Position: 0}}}},
	}
	manual.Checksum, _ = manual.CalculatedChecksum()
	ad := filtercontract.AdBundle{
		SchemaVersion: 1, PolicyKind: filtercontract.PolicyAd, Revision: 4,
		TagThreshold: filtercontract.Score(2000), QuarantineThreshold: filtercontract.Score(5000),
		Detectors:  []filtercontract.AdDetector{{LogicalID: "sale", Name: "Sale", Symbol: "AD_SALE", Mode: filtercontract.ModeEnforce, Source: "local", Conditions: []filtercontract.Condition{{Field: "subject", Operator: "contains", Value: filtercontract.StringValue("sale"), Position: 0}}}},
		Composites: []filtercontract.AdComposite{}, Weights: []filtercontract.SymbolWeight{{Symbol: "AD_SALE", Score: filtercontract.Score(3000)}},
	}
	ad.Checksum, _ = ad.CalculatedChecksum()
	return manual, ad
}

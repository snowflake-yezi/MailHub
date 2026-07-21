package filteroutbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	filtercontract "github.com/ticket/email-filter-contract"
)

func TestTwoPhaseQueuePersistsAndUploadsReadyEvents(t *testing.T) {
	root := t.TempDir()
	queue, err := New(root, 10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	event := testEvent()
	if err := queue.Stage(event); err != nil {
		t.Fatal(err)
	}
	if staged, ready, _, _ := queue.Pending(); staged != 1 || ready != 0 {
		t.Fatalf("pending after stage = %d/%d", staged, ready)
	}
	if err := queue.Ready(event.Decision.DecisionKey, filtercontract.ProcessingResult{Status: "succeeded", AttemptedAction: "tag", ActualAction: "allow"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(root, 10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if staged, ready, _, _ := reopened.Pending(); staged != 0 || ready != 1 {
		t.Fatalf("pending after reopen = %d/%d", staged, ready)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("X-Internal-Token") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	uploader := NewUploader(reopened, server.URL, "secret")
	if uploaded, err := uploader.UploadOnce(context.Background()); err != nil || uploaded != 1 {
		t.Fatalf("upload = %d/%v", uploaded, err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
	if _, ready, _, _ := reopened.Pending(); ready != 0 {
		t.Fatal("ready event was not removed after 2xx")
	}
}

func TestUploadFailureKeepsReadyAndRecoveryFinalizesStaged(t *testing.T) {
	queue, err := New(t.TempDir(), 10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	event := testEvent()
	if err := queue.Stage(event); err != nil {
		t.Fatal(err)
	}
	if recovered, err := queue.RecoverStaged(); err != nil || recovered != 1 {
		t.Fatalf("recover = %d/%v", recovered, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	uploader := NewUploader(queue, server.URL, "secret")
	if _, err := uploader.UploadOnce(context.Background()); err == nil {
		t.Fatal("upload failure was ignored")
	}
	if _, ready, _, _ := queue.Pending(); ready != 1 {
		t.Fatal("failed upload removed the ready event")
	}
}

func TestQueueRejectsCapacityAndUnsafeKeys(t *testing.T) {
	queue, err := New(t.TempDir(), 1, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	event := testEvent()
	if err := queue.Stage(event); err != nil {
		t.Fatal(err)
	}
	second := testEvent()
	second.Decision.DecisionKey = strings.Repeat("b", 64)
	if err := queue.Stage(second); err == nil {
		t.Fatal("capacity limit was ignored")
	}
	second.Decision.DecisionKey = "../escape"
	if err := queue.Stage(second); err == nil {
		t.Fatal("unsafe decision key was accepted")
	}
}

func TestReadyRejectsResultThatExceedsByteCapacity(t *testing.T) {
	queue, err := New(t.TempDir(), 10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	event := testEvent()
	if err := queue.Stage(event); err != nil {
		t.Fatal(err)
	}
	_, _, used, err := queue.Pending()
	if err != nil {
		t.Fatal(err)
	}
	queue.maxBytes = used + 1
	result := filtercontract.ProcessingResult{
		Status: "failed", AttemptedAction: "tag", ActualAction: "allow",
		ErrorCode: "processing_failed", ErrorSummary: strings.Repeat("x", 128),
	}
	if err := queue.Ready(event.Decision.DecisionKey, result); err == nil {
		t.Fatal("ready result exceeded byte capacity")
	}
	if staged, ready, _, _ := queue.Pending(); staged != 1 || ready != 0 {
		t.Fatalf("pending after rejected ready = %d/%d", staged, ready)
	}
}

func testEvent() filtercontract.OutboxEvent {
	return filtercontract.OutboxEvent{
		SchemaVersion: 1, Phase: "staged", NodeID: 7, Mailbox: "inbox@example.com", MessageID: "<fixture@example.com>",
		Decision: filtercontract.FilterDecision{
			SchemaVersion: 1, DecisionKey: strings.Repeat("a", 64), MessageKey: strings.Repeat("c", 64),
			ManualAction: "allow", AdAction: "tag", FinalAction: "tag", Reasons: []filtercontract.DecisionReason{},
			AdSymbols: []filtercontract.AdSymbolResult{}, ShadowResults: []filtercontract.ShadowResult{}, ParseWarnings: []string{},
			EvaluatedAt: time.Unix(100, 0).UTC(),
		},
	}
}

package store

import (
	"errors"
	"testing"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type nodeCommandTestServer struct {
	ID uint64 `gorm:"primaryKey"`
}

func (nodeCommandTestServer) TableName() string { return "mail_servers" }

func newNodeCommandTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&nodeCommandTestServer{}, &model.NodeCommand{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&nodeCommandTestServer{ID: 7}).Error; err != nil {
		t.Fatalf("create server: %v", err)
	}
	return &Store{db: db}
}

func TestNodeCommandSequenceIdempotencyAndStateMachine(t *testing.T) {
	store := newNodeCommandTestStore(t)
	now := time.Now().UTC()
	first, created, err := store.EnqueueNodeCommand(EnqueueNodeCommandInput{
		ServerID: 7, CommandType: "mailbox.create.v1", IdempotencyKey: "mailbox:create:a@example.com",
		PayloadJSON: []byte(`{"email_address":"a@example.com"}`), DeadlineAt: now.Add(time.Minute),
	})
	if err != nil || !created || first.Sequence != 1 || first.State != model.NodeCommandQueued {
		t.Fatalf("first enqueue = %#v, created=%v, err=%v", first, created, err)
	}
	repeated, created, err := store.EnqueueNodeCommand(EnqueueNodeCommandInput{
		ServerID: 7, CommandType: "mailbox.create.v1", IdempotencyKey: "mailbox:create:a@example.com",
		PayloadJSON: []byte(`{"email_address":"a@example.com"}`), DeadlineAt: now.Add(2 * time.Minute),
	})
	if err != nil || created || repeated.CommandID != first.CommandID {
		t.Fatalf("repeat enqueue = %#v, created=%v, err=%v", repeated, created, err)
	}
	if _, _, err := store.EnqueueNodeCommand(EnqueueNodeCommandInput{
		ServerID: 7, CommandType: "mailbox.create.v1", IdempotencyKey: "mailbox:create:a@example.com",
		PayloadJSON: []byte(`{"email_address":"other@example.com"}`), DeadlineAt: now.Add(time.Minute),
	}); !errors.Is(err, ErrNodeCommandPayloadConflict) {
		t.Fatalf("payload conflict error = %v", err)
	}
	second, _, err := store.EnqueueNodeCommand(EnqueueNodeCommandInput{
		ServerID: 7, CommandType: "mailbox.delete.v1", IdempotencyKey: "mailbox:delete:a@example.com",
		PayloadJSON: []byte(`{"email_address":"a@example.com"}`), DeadlineAt: now.Add(time.Minute),
	})
	if err != nil || second.Sequence != 2 {
		t.Fatalf("second enqueue = %#v, err=%v", second, err)
	}

	if err := store.MarkNodeCommandDelivered(first.CommandID, 7, 1, now); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	if err := store.MarkNodeCommandReceived(first.CommandID, 7, 1, now.Add(time.Second)); err != nil {
		t.Fatalf("mark received: %v", err)
	}
	if err := store.MarkNodeCommandStarted(first.CommandID, 7, 1, now.Add(2*time.Second)); err != nil {
		t.Fatalf("mark started: %v", err)
	}
	result := []byte(`{"status_code":201}`)
	if err := store.CompleteNodeCommand(first.CommandID, 7, 1, model.NodeCommandSucceeded, "http.201", result, "", now.Add(3*time.Second)); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := store.CompleteNodeCommand(first.CommandID, 7, 1, model.NodeCommandSucceeded, "http.201", result, "", now.Add(4*time.Second)); err != nil {
		t.Fatalf("duplicate result: %v", err)
	}
	if err := store.CompleteNodeCommand(first.CommandID, 7, 1, model.NodeCommandFailed, "failed", nil, "late", now.Add(4*time.Second)); !errors.Is(err, ErrNodeCommandTerminal) {
		t.Fatalf("conflicting result error = %v", err)
	}
	completed, err := store.GetNodeCommand(first.CommandID)
	if err != nil || completed.State != model.NodeCommandSucceeded || completed.AttemptCount != 1 || completed.ReceivedAt == nil || completed.StartedAt == nil || completed.FinishedAt == nil {
		t.Fatalf("completed command = %#v, err=%v", completed, err)
	}
}

func TestNodeCommandDispatchAndDeadline(t *testing.T) {
	store := newNodeCommandTestStore(t)
	now := time.Now().UTC()
	expired, _, err := store.EnqueueNodeCommand(EnqueueNodeCommandInput{
		ServerID: 7, CommandType: "mailbox.delete.v1", IdempotencyKey: "expired",
		PayloadJSON: []byte(`{}`), DeadlineAt: now.Add(-time.Second),
	})
	if err != nil {
		t.Fatalf("enqueue expired: %v", err)
	}
	active, _, err := store.EnqueueNodeCommand(EnqueueNodeCommandInput{
		ServerID: 7, CommandType: "mailbox.delete.v1", IdempotencyKey: "active",
		PayloadJSON: []byte(`{}`), DeadlineAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("enqueue active: %v", err)
	}
	commands, err := store.ListNodeCommandsForDispatch(7, now, 10)
	if err != nil || len(commands) != 1 || commands[0].CommandID != active.CommandID {
		t.Fatalf("dispatchable = %#v, err=%v", commands, err)
	}
	count, err := store.ExpireNodeCommands(now)
	if err != nil || count != 1 {
		t.Fatalf("expire = %d, %v", count, err)
	}
	value, _ := store.GetNodeCommand(expired.CommandID)
	if value.State != model.NodeCommandExpired || value.FinishedAt == nil {
		t.Fatalf("expired command = %#v", value)
	}
	retry, created, err := store.EnqueueNodeCommand(EnqueueNodeCommandInput{
		ServerID: 7, CommandType: "mailbox.delete.v1", IdempotencyKey: "expired",
		PayloadJSON: []byte(`{}`), DeadlineAt: now.Add(time.Minute),
	})
	if err != nil || !created || retry.Sequence != 3 || retry.IdempotencyKey != "expired:retry:3" {
		t.Fatalf("retry command = %#v, created=%v, err=%v", retry, created, err)
	}
	next, err := store.NextNodeCommandSequence(7)
	if err != nil || next != 4 {
		t.Fatalf("next sequence = %d, %v", next, err)
	}
}

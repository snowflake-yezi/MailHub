package command

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func testCommand(id, key, payload string, sequence uint64) *nodev1.Command {
	return &nodev1.Command{
		CommandId: id, Sequence: sequence, Type: "mailbox.create.v1", SchemaVersion: 1,
		IdempotencyKey: key, PayloadJson: []byte(payload), CreatedAt: timestamppb.Now(),
		DeadlineAt: timestamppb.New(time.Now().Add(time.Minute)),
	}
}

func TestDispatcherPersistsResultBeforeDuplicateDelivery(t *testing.T) {
	directory := t.TempDir()
	journal, err := OpenJournal(directory, JournalConfig{})
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	var executions atomic.Int32
	dispatcher, err := NewDispatcher(journal, func(context.Context, *nodev1.Command) StoredResult {
		executions.Add(1)
		return StoredResult{State: nodev1.CommandState_COMMAND_STATE_SUCCEEDED, ResultCode: "http.201", ResultJSON: []byte(`{"status_code":201}`)}
	}, 4)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go dispatcher.Run(ctx)

	command := testCommand("command-1", "mailbox:create:a@example.com", `{"email_address":"a@example.com"}`, 1)
	emitted := make(chan *nodev1.NodeControlFrame, 4)
	frames, err := dispatcher.Accept(ctx, command, func(frame *nodev1.NodeControlFrame) { emitted <- frame })
	if err != nil || len(frames) != 1 || frames[0].GetCommandReceived() == nil {
		t.Fatalf("accept frames = %#v, err=%v", frames, err)
	}
	var result *nodev1.CommandResult
	for result == nil {
		select {
		case frame := <-emitted:
			result = frame.GetCommandResult()
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for result")
		}
	}
	if result.State != nodev1.CommandState_COMMAND_STATE_SUCCEEDED || executions.Load() != 1 {
		t.Fatalf("result=%#v executions=%d", result, executions.Load())
	}

	reopened, err := OpenJournal(directory, JournalConfig{})
	if err != nil {
		t.Fatalf("reopen journal: %v", err)
	}
	replay, err := NewDispatcher(reopened, func(context.Context, *nodev1.Command) StoredResult {
		executions.Add(1)
		return StoredResult{}
	}, 4)
	if err != nil {
		t.Fatalf("new replay dispatcher: %v", err)
	}
	frames, err = replay.Accept(ctx, command, nil)
	if err != nil || len(frames) != 2 || frames[1].GetCommandResult() == nil || executions.Load() != 1 {
		t.Fatalf("replay frames=%#v executions=%d err=%v", frames, executions.Load(), err)
	}
	if reopened.LastCompletedSequence() != 1 {
		t.Fatalf("last completed sequence = %d", reopened.LastCompletedSequence())
	}
}

func TestJournalRejectsConflictingCommandAndRecoversRunningEntry(t *testing.T) {
	directory := t.TempDir()
	journal, err := OpenJournal(directory, JournalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	command := testCommand("command-1", "same-key", `{"value":1}`, 1)
	if _, err := journal.Begin(command); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := journal.MarkRunning(command.CommandId); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	conflict := testCommand("command-2", "same-key", `{"value":2}`, 2)
	if _, err := journal.Begin(conflict); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("conflict error = %v", err)
	}

	reopened, err := OpenJournal(directory, JournalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var executions atomic.Int32
	dispatcher, _ := NewDispatcher(reopened, func(context.Context, *nodev1.Command) StoredResult {
		executions.Add(1)
		return StoredResult{State: nodev1.CommandState_COMMAND_STATE_SUCCEEDED, ResultCode: "ok"}
	}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go dispatcher.Run(ctx)
	emitted := make(chan *nodev1.NodeControlFrame, 2)
	frames, err := dispatcher.Accept(ctx, command, func(frame *nodev1.NodeControlFrame) { emitted <- frame })
	if err != nil || len(frames) != 1 {
		t.Fatalf("recover accept frames=%#v err=%v", frames, err)
	}
	select {
	case frame := <-emitted:
		if frame.GetCommandStarted() == nil {
			select {
			case frame = <-emitted:
			case <-time.After(time.Second):
				t.Fatal("missing recovered result")
			}
		}
	case <-time.After(time.Second):
		t.Fatal("missing recovered execution")
	}
	deadline := time.Now().Add(time.Second)
	for executions.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if executions.Load() != 1 {
		t.Fatalf("recovered executions = %d", executions.Load())
	}
}

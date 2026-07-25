package nodetransport

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodecommand"
	"github.com/ticket/email-mgmt-system/internal/nodesession"
	storepkg "github.com/ticket/email-mgmt-system/internal/store"
	nodecontract "github.com/ticket/email-node-contract"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeCommandStore struct {
	mu      sync.Mutex
	command *model.NodeCommand
}

func (store *fakeCommandStore) EnqueueNodeCommand(input storepkg.EnqueueNodeCommandInput) (*model.NodeCommand, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.command == nil {
		store.command = &model.NodeCommand{
			CommandID: "command-1", ServerID: input.ServerID, Sequence: 1, CommandType: input.CommandType,
			SchemaVersion: input.SchemaVersion, IdempotencyKey: input.IdempotencyKey,
			PayloadJSON: string(input.PayloadJSON), State: model.NodeCommandQueued,
			DeadlineAt: input.DeadlineAt, CreatedAt: time.Now().UTC(),
		}
		clone := *store.command
		return &clone, true, nil
	}
	clone := *store.command
	return &clone, false, nil
}

func (store *fakeCommandStore) GetNodeCommand(string) (*model.NodeCommand, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.command == nil {
		return nil, storepkg.ErrNodeCommandNotFound
	}
	clone := *store.command
	return &clone, nil
}

func (store *fakeCommandStore) NextNodeCommandSequence(uint64) (uint64, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.command == nil {
		return 1, nil
	}
	return store.command.Sequence + 1, nil
}

func (store *fakeCommandStore) ListRecentNodeCommands(_ uint64, _ string, _ int) ([]model.NodeCommand, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.command == nil {
		return nil, nil
	}
	return []model.NodeCommand{*store.command}, nil
}

func (store *fakeCommandStore) ListNodeCommandsForDispatch(_ uint64, now time.Time, _ int) ([]model.NodeCommand, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.command == nil || model.IsTerminalNodeCommandState(store.command.State) || !store.command.DeadlineAt.After(now) {
		return nil, nil
	}
	return []model.NodeCommand{*store.command}, nil
}

func (store *fakeCommandStore) MarkNodeCommandDelivered(_ string, _, _ uint64, _ time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.command.State = model.NodeCommandDelivered
	store.command.AttemptCount++
	return nil
}

func (store *fakeCommandStore) MarkNodeCommandReceived(_ string, _, _ uint64, at time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.command.State, store.command.ReceivedAt = model.NodeCommandReceived, &at
	return nil
}

func (store *fakeCommandStore) MarkNodeCommandStarted(_ string, _, _ uint64, at time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.command.State, store.command.StartedAt = model.NodeCommandRunning, &at
	return nil
}

func (store *fakeCommandStore) CompleteNodeCommand(_ string, _, _ uint64, state, code string, result []byte, message string, at time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.command.State, store.command.ResultCode = state, code
	store.command.ResultJSON, store.command.ErrorMessage, store.command.FinishedAt = string(result), message, &at
	return nil
}

func (store *fakeCommandStore) ExpireNodeCommands(time.Time) (int64, error) { return 0, nil }

func TestControlStreamExecutePersistsDispatchesAndWaitsForResult(t *testing.T) {
	commandStore := &fakeCommandStore{}
	sessions := nodesession.NewRegistry()
	session := sessions.Register(context.Background(), nodesession.RegisterInput{ServerID: 7, NodeUUID: "node-7"})
	manager, err := nodecommand.New(commandStore, sessions, nodecommand.Config{WaitTimeout: time.Second, CommandTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	transport := NewControlStreamTransport(sessions, nil, manager)

	type outcome struct {
		response *Response
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		response, executeErr := transport.Execute(context.Background(), Target{NodeID: 7}, MailboxCreate("a@example.com", "secret"))
		done <- outcome{response: response, err: executeErr}
	}()
	frame := <-session.Outgoing()
	command := frame.GetCommand()
	if command == nil || command.Sequence != 1 || command.Type != string(nodecontract.CommandMailboxCreate) {
		t.Fatalf("dispatched frame = %#v", frame)
	}
	if err := manager.Received(7, &nodev1.CommandReceived{CommandId: command.CommandId, Sequence: 1, ReceivedAt: timestamppb.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Started(7, &nodev1.CommandStarted{CommandId: command.CommandId, Sequence: 1, StartedAt: timestamppb.Now()}); err != nil {
		t.Fatal(err)
	}
	resultJSON, _ := json.Marshal(nodecontract.CommandResponse{StatusCode: 201, Body: json.RawMessage(`{"code":0}`)})
	if err := manager.Result(7, &nodev1.CommandResult{
		CommandId: command.CommandId, Sequence: 1, State: nodev1.CommandState_COMMAND_STATE_SUCCEEDED,
		ResultCode: "http.201", ResultJson: resultJSON, CompletedAt: timestamppb.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || result.response.StatusCode != 201 || string(result.response.Body) != `{"code":0}` {
			t.Fatalf("execute result = %#v, %v", result.response, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not return final result")
	}
	stored, _ := commandStore.GetNodeCommand(command.CommandId)
	if stored.State != model.NodeCommandSucceeded || stored.AttemptCount != 1 {
		t.Fatalf("stored command = %#v", stored)
	}
}

func TestControlStreamExecuteOfflineReturnsPendingCommandID(t *testing.T) {
	commandStore := &fakeCommandStore{}
	manager, _ := nodecommand.New(commandStore, nodesession.NewRegistry(), nodecommand.Config{
		WaitTimeout: 10 * time.Millisecond, CommandTTL: time.Minute,
	})
	transport := NewControlStreamTransport(nodesession.NewRegistry(), nil, manager)
	_, err := transport.Execute(context.Background(), Target{NodeID: 9}, MailboxDelete("a@example.com", time.Second))
	var pending *nodecommand.PendingError
	if !errors.As(err, &pending) || pending.CommandID != "command-1" {
		t.Fatalf("pending error = %#v", err)
	}
	stored, _ := commandStore.GetNodeCommand("command-1")
	if stored.State != model.NodeCommandQueued {
		t.Fatalf("offline command state = %s", stored.State)
	}
	restartedSessions := nodesession.NewRegistry()
	restartedSession := restartedSessions.Register(context.Background(), nodesession.RegisterInput{ServerID: 9, NodeUUID: "node-9"})
	restartedManager, _ := nodecommand.New(commandStore, restartedSessions, nodecommand.Config{WaitTimeout: time.Second, CommandTTL: time.Minute})
	if err := restartedManager.DispatchPending(context.Background(), 9); err != nil {
		t.Fatalf("dispatch after management restart: %v", err)
	}
	replayed := <-restartedSession.Outgoing()
	if replayed.GetCommand().GetCommandId() != "command-1" {
		t.Fatalf("replayed command = %#v", replayed)
	}
}

func TestControlStreamQueriesDurableQuarantineReleaseStatus(t *testing.T) {
	receiptBody := json.RawMessage(`{"code":0,"data":{"status":"completed"}}`)
	resultJSON, _ := json.Marshal(nodecontract.CommandResponse{StatusCode: 200, Body: receiptBody})
	commandStore := &fakeCommandStore{command: &model.NodeCommand{
		CommandID: "release-1", ServerID: 7, Sequence: 3,
		CommandType: string(nodecontract.CommandQuarantineRelease), SchemaVersion: 1,
		IdempotencyKey: "operation-7", PayloadJSON: `{"operation_id":"operation-7","quarantine_key":"q-7"}`,
		State: model.NodeCommandSucceeded, ResultJSON: string(resultJSON), DeadlineAt: time.Now().Add(time.Minute),
	}}
	manager, _ := nodecommand.New(commandStore, nodesession.NewRegistry(), nodecommand.Config{})
	transport := NewControlStreamTransport(nodesession.NewRegistry(), nil, manager)
	response, err := transport.Query(context.Background(), Target{NodeID: 7}, QuarantineReleaseStatus("q-7"))
	if err != nil || response.StatusCode != 200 || string(response.Body) != string(receiptBody) {
		t.Fatalf("release status = %#v, %v", response, err)
	}

	commandStore.mu.Lock()
	commandStore.command.State = model.NodeCommandRunning
	commandStore.command.ResultJSON = ""
	commandStore.mu.Unlock()
	response, err = transport.Query(context.Background(), Target{NodeID: 7}, QuarantineReleaseStatus("q-7"))
	if err != nil || response.StatusCode != 202 || !json.Valid(response.Body) {
		t.Fatalf("pending release status = %#v, %v", response, err)
	}
}

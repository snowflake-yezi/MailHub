package nodecommand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodesession"
	"github.com/ticket/email-mgmt-system/internal/store"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultDispatchLimit = 256
const defaultCommandTTL = 24 * time.Hour

type Store interface {
	EnqueueNodeCommand(store.EnqueueNodeCommandInput) (*model.NodeCommand, bool, error)
	GetNodeCommand(string) (*model.NodeCommand, error)
	NextNodeCommandSequence(uint64) (uint64, error)
	ListRecentNodeCommands(uint64, string, int) ([]model.NodeCommand, error)
	ListNodeCommandsForDispatch(uint64, time.Time, int) ([]model.NodeCommand, error)
	MarkNodeCommandDelivered(string, uint64, uint64, time.Time) error
	MarkNodeCommandReceived(string, uint64, uint64, time.Time) error
	MarkNodeCommandStarted(string, uint64, uint64, time.Time) error
	CompleteNodeCommand(string, uint64, uint64, string, string, []byte, string, time.Time) error
	ExpireNodeCommands(time.Time) (int64, error)
}

type Config struct {
	WaitTimeout time.Duration
	CommandTTL  time.Duration
	Now         func() time.Time
}

type SubmitInput struct {
	ServerID       uint64
	CommandType    string
	SchemaVersion  uint32
	IdempotencyKey string
	PayloadJSON    []byte
	TraceID        string
	RequestedBy    string
}

type PendingError struct {
	CommandID string
	Cause     error
}

func (err *PendingError) Error() string {
	return fmt.Sprintf("node command %s is still pending: %v", err.CommandID, err.Cause)
}

func (err *PendingError) Unwrap() error       { return err.Cause }
func (err *PendingError) OperationID() string { return err.CommandID }

type Manager struct {
	store    Store
	sessions *nodesession.Registry
	config   Config

	mu      sync.Mutex
	waiters map[string]map[chan struct{}]struct{}
}

func New(store Store, sessions *nodesession.Registry, config Config) (*Manager, error) {
	if store == nil || sessions == nil {
		return nil, errors.New("node command store and session registry are required")
	}
	if config.WaitTimeout <= 0 {
		config.WaitTimeout = 15 * time.Second
	}
	if config.CommandTTL <= config.WaitTimeout {
		config.CommandTTL = defaultCommandTTL
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Manager{store: store, sessions: sessions, config: config, waiters: make(map[string]map[chan struct{}]struct{})}, nil
}

func (manager *Manager) SubmitAndWait(ctx context.Context, input SubmitInput) (*model.NodeCommand, error) {
	now := manager.now()
	command, _, err := manager.store.EnqueueNodeCommand(store.EnqueueNodeCommandInput{
		ServerID: input.ServerID, CommandType: input.CommandType, SchemaVersion: input.SchemaVersion,
		IdempotencyKey: input.IdempotencyKey, PayloadJSON: input.PayloadJSON,
		DeadlineAt: now.Add(manager.config.CommandTTL), TraceID: input.TraceID, RequestedBy: input.RequestedBy,
	})
	if err != nil {
		return nil, err
	}
	if model.IsTerminalNodeCommandState(command.State) {
		return command, nil
	}

	waiter := manager.addWaiter(command.CommandID)
	defer manager.removeWaiter(command.CommandID, waiter)
	if err := manager.DispatchPending(ctx, input.ServerID); err != nil && !errors.Is(err, nodesession.ErrSessionNotFound) {
		return nil, err
	}

	waitContext, cancel := context.WithTimeout(ctx, manager.config.WaitTimeout)
	defer cancel()
	for {
		current, err := manager.store.GetNodeCommand(command.CommandID)
		if err != nil {
			return nil, err
		}
		if model.IsTerminalNodeCommandState(current.State) {
			return current, nil
		}
		select {
		case <-waitContext.Done():
			return nil, &PendingError{CommandID: command.CommandID, Cause: waitContext.Err()}
		case <-waiter:
		}
	}
}

func (manager *Manager) NextSequence(serverID uint64) (uint64, error) {
	return manager.store.NextNodeCommandSequence(serverID)
}

func (manager *Manager) FindByPayloadField(serverID uint64, commandType, field, value string) (*model.NodeCommand, error) {
	commands, err := manager.store.ListRecentNodeCommands(serverID, commandType, 256)
	if err != nil {
		return nil, err
	}
	for index := range commands {
		var payload map[string]json.RawMessage
		if json.Unmarshal([]byte(commands[index].PayloadJSON), &payload) != nil {
			continue
		}
		var candidate string
		if json.Unmarshal(payload[field], &candidate) == nil && candidate == value {
			return &commands[index], nil
		}
	}
	return nil, store.ErrNodeCommandNotFound
}

// DispatchPending replays every non-terminal command in sequence order. State
// is advanced only after the frame enters the active session send queue.
func (manager *Manager) DispatchPending(ctx context.Context, serverID uint64) error {
	commands, err := manager.store.ListNodeCommandsForDispatch(serverID, manager.now(), defaultDispatchLimit)
	if err != nil {
		return err
	}
	for _, command := range commands {
		frame := commandFrame(command)
		if err := manager.sessions.Send(ctx, serverID, frame); err != nil {
			return err
		}
		if err := manager.store.MarkNodeCommandDelivered(command.CommandID, serverID, command.Sequence, manager.now()); err != nil && !errors.Is(err, store.ErrNodeCommandTerminal) {
			return err
		}
	}
	return nil
}

func (manager *Manager) Received(serverID uint64, received *nodev1.CommandReceived) error {
	if received == nil {
		return errors.New("empty command received acknowledgement")
	}
	return manager.store.MarkNodeCommandReceived(received.CommandId, serverID, received.Sequence, timestampOrNow(received.ReceivedAt, manager.now()))
}

func (manager *Manager) Started(serverID uint64, started *nodev1.CommandStarted) error {
	if started == nil {
		return errors.New("empty command started acknowledgement")
	}
	return manager.store.MarkNodeCommandStarted(started.CommandId, serverID, started.Sequence, timestampOrNow(started.StartedAt, manager.now()))
}

func (manager *Manager) Result(serverID uint64, result *nodev1.CommandResult) error {
	if result == nil {
		return errors.New("empty command result")
	}
	state, err := resultState(result.State)
	if err != nil {
		return err
	}
	if err := manager.store.CompleteNodeCommand(
		result.CommandId, serverID, result.Sequence, state, result.ResultCode,
		result.ResultJson, result.ErrorMessage, timestampOrNow(result.CompletedAt, manager.now()),
	); err != nil {
		return err
	}
	manager.notify(result.CommandId)
	return nil
}

func (manager *Manager) ReapExpired(ctx context.Context) {
	interval := manager.config.WaitTimeout
	if interval > time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = manager.store.ExpireNodeCommands(manager.now())
		}
	}
}

func (manager *Manager) now() time.Time { return manager.config.Now().UTC() }

func (manager *Manager) addWaiter(commandID string) chan struct{} {
	channel := make(chan struct{}, 1)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.waiters[commandID] == nil {
		manager.waiters[commandID] = make(map[chan struct{}]struct{})
	}
	manager.waiters[commandID][channel] = struct{}{}
	return channel
}

func (manager *Manager) removeWaiter(commandID string, channel chan struct{}) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	delete(manager.waiters[commandID], channel)
	if len(manager.waiters[commandID]) == 0 {
		delete(manager.waiters, commandID)
	}
}

func (manager *Manager) notify(commandID string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for channel := range manager.waiters[commandID] {
		select {
		case channel <- struct{}{}:
		default:
		}
	}
}

func commandFrame(command model.NodeCommand) *nodev1.SystemControlFrame {
	return &nodev1.SystemControlFrame{
		FrameId: uuid.NewString(), SentAt: timestamppb.Now(),
		Payload: &nodev1.SystemControlFrame_Command{Command: &nodev1.Command{
			CommandId: command.CommandID, Sequence: command.Sequence, Type: command.CommandType,
			SchemaVersion: command.SchemaVersion, IdempotencyKey: command.IdempotencyKey,
			PayloadJson: []byte(command.PayloadJSON), CreatedAt: timestamppb.New(command.CreatedAt.UTC()),
			DeadlineAt: timestamppb.New(command.DeadlineAt.UTC()), TraceId: command.TraceID,
		}},
	}
}

func resultState(state nodev1.CommandState) (string, error) {
	switch state {
	case nodev1.CommandState_COMMAND_STATE_SUCCEEDED:
		return model.NodeCommandSucceeded, nil
	case nodev1.CommandState_COMMAND_STATE_SUCCEEDED_WITH_WARNING:
		return model.NodeCommandSucceededWithWarning, nil
	case nodev1.CommandState_COMMAND_STATE_FAILED:
		return model.NodeCommandFailed, nil
	case nodev1.CommandState_COMMAND_STATE_REJECTED:
		return model.NodeCommandRejected, nil
	case nodev1.CommandState_COMMAND_STATE_EXPIRED:
		return model.NodeCommandExpired, nil
	default:
		return "", fmt.Errorf("invalid terminal command result state %s", state)
	}
}

func timestampOrNow(value *timestamppb.Timestamp, fallback time.Time) time.Time {
	if value == nil || value.CheckValid() != nil {
		return fallback.UTC()
	}
	return value.AsTime().UTC()
}

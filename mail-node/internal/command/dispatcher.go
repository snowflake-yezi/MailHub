package command

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Executor func(context.Context, *nodev1.Command) StoredResult
type EmitFunc func(*nodev1.NodeControlFrame)

type queuedCommand struct {
	command *nodev1.Command
	emit    EmitFunc
}

type Dispatcher struct {
	journal  *Journal
	executor Executor
	queue    chan queuedCommand

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewDispatcher(journal *Journal, executor Executor, queueSize int) (*Dispatcher, error) {
	if journal == nil || executor == nil {
		return nil, errors.New("command journal and executor are required")
	}
	if queueSize <= 0 {
		queueSize = 64
	}
	return &Dispatcher{journal: journal, executor: executor, queue: make(chan queuedCommand, queueSize), cancels: make(map[string]context.CancelFunc)}, nil
}

func (dispatcher *Dispatcher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-dispatcher.queue:
			dispatcher.execute(ctx, job)
		}
	}
}

// Accept durably records a command before returning the Received frame. Cached
// results are returned immediately and never execute the business operation.
func (dispatcher *Dispatcher) Accept(ctx context.Context, command *nodev1.Command, emit EmitFunc) ([]*nodev1.NodeControlFrame, error) {
	cached, err := dispatcher.journal.Begin(command)
	if err != nil {
		return nil, err
	}
	frames := []*nodev1.NodeControlFrame{receivedFrame(command)}
	if cached != nil {
		return append(frames, resultFrame(command, *cached)), nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case dispatcher.queue <- queuedCommand{command: cloneCommand(command), emit: emit}:
		return frames, nil
	}
}

func (dispatcher *Dispatcher) Cancel(commandID string) bool {
	dispatcher.mu.Lock()
	cancel, ok := dispatcher.cancels[commandID]
	dispatcher.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func (dispatcher *Dispatcher) LastCompletedSequence() uint64 {
	return dispatcher.journal.LastCompletedSequence()
}

func (dispatcher *Dispatcher) execute(parent context.Context, job queuedCommand) {
	command := job.command
	if cached, err := dispatcher.journal.Begin(command); err != nil || cached != nil {
		if cached != nil && job.emit != nil {
			job.emit(resultFrame(command, *cached))
		}
		return
	}
	if err := dispatcher.journal.MarkRunning(command.CommandId); err != nil {
		return
	}
	if job.emit != nil {
		job.emit(startedFrame(command))
	}

	executionContext := parent
	cancel := func() {}
	if command.DeadlineAt != nil && command.DeadlineAt.CheckValid() == nil {
		executionContext, cancel = context.WithDeadline(parent, command.DeadlineAt.AsTime())
	} else {
		executionContext, cancel = context.WithCancel(parent)
	}
	dispatcher.mu.Lock()
	dispatcher.cancels[command.CommandId] = cancel
	dispatcher.mu.Unlock()
	defer func() {
		cancel()
		dispatcher.mu.Lock()
		delete(dispatcher.cancels, command.CommandId)
		dispatcher.mu.Unlock()
	}()

	var result StoredResult
	if err := executionContext.Err(); err != nil {
		result = StoredResult{State: nodev1.CommandState_COMMAND_STATE_EXPIRED, ResultCode: "deadline_exceeded", ErrorMessage: err.Error()}
	} else {
		result = dispatcher.executor(executionContext, command)
		if !terminalState(result.State) {
			result = StoredResult{State: nodev1.CommandState_COMMAND_STATE_FAILED, ResultCode: "invalid_executor_result", ErrorMessage: "command executor returned a non-terminal state"}
		}
		if executionContext.Err() != nil && result.State == nodev1.CommandState_COMMAND_STATE_FAILED {
			result.State = nodev1.CommandState_COMMAND_STATE_EXPIRED
			result.ResultCode = "deadline_exceeded"
			result.ErrorMessage = executionContext.Err().Error()
		}
	}
	result.CompletedAt = time.Now().UTC()
	if err := dispatcher.journal.Complete(command.CommandId, result); err != nil {
		return
	}
	if job.emit != nil {
		job.emit(resultFrame(command, result))
	}
}

func receivedFrame(command *nodev1.Command) *nodev1.NodeControlFrame {
	return &nodev1.NodeControlFrame{
		FrameId: command.CommandId + ":received", SentAt: timestamppb.Now(),
		Payload: &nodev1.NodeControlFrame_CommandReceived{CommandReceived: &nodev1.CommandReceived{
			CommandId: command.CommandId, Sequence: command.Sequence, ReceivedAt: timestamppb.Now(),
		}},
	}
}

func startedFrame(command *nodev1.Command) *nodev1.NodeControlFrame {
	return &nodev1.NodeControlFrame{
		FrameId: command.CommandId + ":started", SentAt: timestamppb.Now(),
		Payload: &nodev1.NodeControlFrame_CommandStarted{CommandStarted: &nodev1.CommandStarted{
			CommandId: command.CommandId, Sequence: command.Sequence, StartedAt: timestamppb.Now(),
		}},
	}
}

func resultFrame(command *nodev1.Command, result StoredResult) *nodev1.NodeControlFrame {
	completedAt := result.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	return &nodev1.NodeControlFrame{
		FrameId: command.CommandId + ":result", SentAt: timestamppb.Now(),
		Payload: &nodev1.NodeControlFrame_CommandResult{CommandResult: &nodev1.CommandResult{
			CommandId: command.CommandId, Sequence: command.Sequence, State: result.State,
			ResultCode: result.ResultCode, ResultJson: result.ResultJSON,
			ErrorMessage: result.ErrorMessage, CompletedAt: timestamppb.New(completedAt.UTC()),
		}},
	}
}

func cloneCommand(command *nodev1.Command) *nodev1.Command {
	if command == nil {
		return nil
	}
	return proto.Clone(command).(*nodev1.Command)
}

func RejectedResult(code string, err error) StoredResult {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return StoredResult{State: nodev1.CommandState_COMMAND_STATE_REJECTED, ResultCode: code, ErrorMessage: message}
}

func FailedResult(code string, err error) StoredResult {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return StoredResult{State: nodev1.CommandState_COMMAND_STATE_FAILED, ResultCode: code, ErrorMessage: message}
}

func ValidateType(command *nodev1.Command, expected string) error {
	if command == nil || command.Type != expected {
		return fmt.Errorf("unexpected command type")
	}
	return nil
}

package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ticket/email-mgmt-system/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNodeCommandNotFound        = errors.New("node command not found")
	ErrNodeCommandIdentity        = errors.New("node command identity mismatch")
	ErrNodeCommandTerminal        = errors.New("node command is already terminal")
	ErrNodeCommandInvalidResult   = errors.New("invalid node command result state")
	ErrNodeCommandPayloadConflict = errors.New("node command idempotency key payload conflict")
)

type EnqueueNodeCommandInput struct {
	ServerID       uint64
	CommandType    string
	SchemaVersion  uint32
	IdempotencyKey string
	PayloadJSON    []byte
	DeadlineAt     time.Time
	TraceID        string
	RequestedBy    string
}

// EnqueueNodeCommand serializes sequence allocation on the owning mail_server
// row. The command row is committed before callers are allowed to dispatch it.
func (s *Store) EnqueueNodeCommand(input EnqueueNodeCommandInput) (*model.NodeCommand, bool, error) {
	if input.ServerID == 0 || strings.TrimSpace(input.CommandType) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return nil, false, errors.New("server, command type, and idempotency key are required")
	}
	if input.SchemaVersion == 0 {
		input.SchemaVersion = 1
	}
	if input.DeadlineAt.IsZero() {
		return nil, false, errors.New("node command deadline is required")
	}

	var command model.NodeCommand
	created := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var exact model.NodeCommand
		exactErr := tx.Where("server_id = ? AND idempotency_key = ?", input.ServerID, input.IdempotencyKey).First(&exact).Error
		if exactErr == nil && (exact.CommandType != input.CommandType || exact.SchemaVersion != input.SchemaVersion || exact.PayloadJSON != string(input.PayloadJSON)) {
			return ErrNodeCommandPayloadConflict
		}
		if exactErr != nil && !errors.Is(exactErr, gorm.ErrRecordNotFound) {
			return exactErr
		}
		var candidates []model.NodeCommand
		err := tx.Where("server_id = ? AND command_type = ? AND payload_json = ?", input.ServerID, input.CommandType, string(input.PayloadJSON)).
			Order("sequence DESC").Find(&candidates).Error
		if err != nil {
			return err
		}
		var existing *model.NodeCommand
		for index := range candidates {
			if candidates[index].IdempotencyKey == input.IdempotencyKey || strings.HasPrefix(candidates[index].IdempotencyKey, input.IdempotencyKey+":retry:") {
				existing = &candidates[index]
				break
			}
		}
		if existing != nil {
			if existing.CommandType != input.CommandType || existing.SchemaVersion != input.SchemaVersion || existing.PayloadJSON != string(input.PayloadJSON) {
				return ErrNodeCommandPayloadConflict
			}
			if existing.State != model.NodeCommandFailed && existing.State != model.NodeCommandRejected &&
				existing.State != model.NodeCommandExpired && existing.State != model.NodeCommandSucceededWithWarning {
				command = *existing
				return nil
			}
		}

		var server model.MailServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&server, input.ServerID).Error; err != nil {
			return fmt.Errorf("lock node server: %w", err)
		}
		var maxSequence uint64
		if err := tx.Model(&model.NodeCommand{}).Where("server_id = ?", input.ServerID).
			Select("COALESCE(MAX(sequence), 0)").Scan(&maxSequence).Error; err != nil {
			return err
		}
		command = model.NodeCommand{
			CommandID: uuid.NewString(), ServerID: input.ServerID, Sequence: maxSequence + 1,
			CommandType: input.CommandType, SchemaVersion: input.SchemaVersion,
			IdempotencyKey: input.IdempotencyKey, PayloadJSON: string(input.PayloadJSON),
			State: model.NodeCommandQueued, DeadlineAt: input.DeadlineAt.UTC(),
			TraceID: input.TraceID, RequestedBy: input.RequestedBy,
		}
		if existing != nil {
			command.IdempotencyKey = fmt.Sprintf("%s:retry:%d", input.IdempotencyKey, command.Sequence)
		}
		if err := tx.Create(&command).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &command, created, nil
}

func (s *Store) GetNodeCommand(commandID string) (*model.NodeCommand, error) {
	var command model.NodeCommand
	if err := s.db.First(&command, "command_id = ?", commandID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNodeCommandNotFound
		}
		return nil, err
	}
	return &command, nil
}

func (s *Store) NextNodeCommandSequence(serverID uint64) (uint64, error) {
	var maxSequence uint64
	if err := s.db.Model(&model.NodeCommand{}).Where("server_id = ?", serverID).
		Select("COALESCE(MAX(sequence), 0)").Scan(&maxSequence).Error; err != nil {
		return 0, err
	}
	return maxSequence + 1, nil
}

func (s *Store) ListRecentNodeCommands(serverID uint64, commandType string, limit int) ([]model.NodeCommand, error) {
	if limit <= 0 {
		limit = 100
	}
	var commands []model.NodeCommand
	err := s.db.Where("server_id = ? AND command_type = ?", serverID, commandType).
		Order("sequence DESC").Limit(limit).Find(&commands).Error
	return commands, err
}

func (s *Store) ListNodeCommandsForDispatch(serverID uint64, now time.Time, limit int) ([]model.NodeCommand, error) {
	if limit <= 0 {
		limit = 256
	}
	var commands []model.NodeCommand
	err := s.db.Where("server_id = ? AND state IN ? AND deadline_at > ?", serverID, []string{
		model.NodeCommandQueued, model.NodeCommandDelivered, model.NodeCommandReceived, model.NodeCommandRunning,
	}, now.UTC()).Order("sequence ASC").Limit(limit).Find(&commands).Error
	return commands, err
}

func (s *Store) MarkNodeCommandDelivered(commandID string, serverID, sequence uint64, deliveredAt time.Time) error {
	result := s.db.Model(&model.NodeCommand{}).
		Where("command_id = ? AND server_id = ? AND sequence = ? AND state IN ? AND deadline_at > ?", commandID, serverID, sequence, []string{
			model.NodeCommandQueued, model.NodeCommandDelivered, model.NodeCommandReceived, model.NodeCommandRunning,
		}, deliveredAt.UTC()).Updates(map[string]any{
		"state":         gorm.Expr("CASE WHEN state = ? THEN ? ELSE state END", model.NodeCommandQueued, model.NodeCommandDelivered),
		"attempt_count": gorm.Expr("attempt_count + 1"),
		"updated_at":    deliveredAt.UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return s.nodeCommandMutationError(commandID, serverID, sequence)
	}
	return nil
}

func (s *Store) MarkNodeCommandReceived(commandID string, serverID, sequence uint64, receivedAt time.Time) error {
	result := s.db.Model(&model.NodeCommand{}).
		Where("command_id = ? AND server_id = ? AND sequence = ? AND state IN ?", commandID, serverID, sequence, []string{
			model.NodeCommandQueued, model.NodeCommandDelivered, model.NodeCommandReceived,
		}).Updates(map[string]any{"state": model.NodeCommandReceived, "received_at": receivedAt.UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		command, err := s.GetNodeCommand(commandID)
		if err == nil && command.ServerID == serverID && command.Sequence == sequence && (command.State == model.NodeCommandRunning || model.IsTerminalNodeCommandState(command.State)) {
			return nil
		}
		return s.nodeCommandMutationError(commandID, serverID, sequence)
	}
	return nil
}

func (s *Store) MarkNodeCommandStarted(commandID string, serverID, sequence uint64, startedAt time.Time) error {
	result := s.db.Model(&model.NodeCommand{}).
		Where("command_id = ? AND server_id = ? AND sequence = ? AND state IN ?", commandID, serverID, sequence, []string{
			model.NodeCommandQueued, model.NodeCommandDelivered, model.NodeCommandReceived, model.NodeCommandRunning,
		}).Updates(map[string]any{
		"state":       model.NodeCommandRunning,
		"received_at": gorm.Expr("COALESCE(received_at, ?)", startedAt.UTC()),
		"started_at":  gorm.Expr("COALESCE(started_at, ?)", startedAt.UTC()),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		command, err := s.GetNodeCommand(commandID)
		if err == nil && command.ServerID == serverID && command.Sequence == sequence && model.IsTerminalNodeCommandState(command.State) {
			return nil
		}
		return s.nodeCommandMutationError(commandID, serverID, sequence)
	}
	return nil
}

func (s *Store) CompleteNodeCommand(commandID string, serverID, sequence uint64, state, resultCode string, resultJSON []byte, errorMessage string, finishedAt time.Time) error {
	if !model.IsTerminalNodeCommandState(state) {
		return ErrNodeCommandInvalidResult
	}
	result := s.db.Model(&model.NodeCommand{}).
		Where("command_id = ? AND server_id = ? AND sequence = ? AND state IN ?", commandID, serverID, sequence, []string{
			model.NodeCommandQueued, model.NodeCommandDelivered, model.NodeCommandReceived, model.NodeCommandRunning,
		}).Updates(map[string]any{
		"state": state, "result_code": resultCode, "result_json": string(resultJSON),
		"error_message": errorMessage, "finished_at": finishedAt.UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	command, err := s.GetNodeCommand(commandID)
	if err != nil {
		return err
	}
	if command.ServerID != serverID || command.Sequence != sequence {
		return ErrNodeCommandIdentity
	}
	if command.State == state && command.ResultCode == resultCode && command.ResultJSON == string(resultJSON) && command.ErrorMessage == errorMessage {
		return nil
	}
	return ErrNodeCommandTerminal
}

func (s *Store) ExpireNodeCommands(now time.Time) (int64, error) {
	result := s.db.Model(&model.NodeCommand{}).
		Where("state IN ? AND deadline_at <= ?", []string{
			model.NodeCommandQueued, model.NodeCommandDelivered, model.NodeCommandReceived, model.NodeCommandRunning,
		}, now.UTC()).Updates(map[string]any{
		"state": model.NodeCommandExpired, "finished_at": now.UTC(),
		"result_code": "deadline_exceeded", "error_message": "command deadline exceeded",
	})
	return result.RowsAffected, result.Error
}

func (s *Store) nodeCommandMutationError(commandID string, serverID, sequence uint64) error {
	command, err := s.GetNodeCommand(commandID)
	if err != nil {
		return err
	}
	if command.ServerID != serverID || command.Sequence != sequence {
		return ErrNodeCommandIdentity
	}
	if model.IsTerminalNodeCommandState(command.State) {
		return ErrNodeCommandTerminal
	}
	return errors.New("node command state transition was not applied")
}

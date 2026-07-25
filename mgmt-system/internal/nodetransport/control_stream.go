package nodetransport

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodecommand"
	"github.com/ticket/email-mgmt-system/internal/nodesession"
	"github.com/ticket/email-mgmt-system/internal/store"
	nodecontract "github.com/ticket/email-node-contract"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var ErrControlOperationUnsupported = errors.New("operation is not available on ControlStream before its migration phase")
var ErrLegacyTransportDisabled = errors.New("legacy HTTP transport is disabled")

type NotificationRevisionResolver func(context.Context, Target, Notification) (uint64, error)

type ControlStreamTransport struct {
	sessions        *nodesession.Registry
	resolveRevision NotificationRevisionResolver
	commands        *nodecommand.Manager
}

func NewControlStreamTransport(sessions *nodesession.Registry, resolver NotificationRevisionResolver, commands ...*nodecommand.Manager) *ControlStreamTransport {
	transport := &ControlStreamTransport{sessions: sessions, resolveRevision: resolver}
	if len(commands) > 0 {
		transport.commands = commands[0]
	}
	return transport
}

func (transport *ControlStreamTransport) Execute(ctx context.Context, target Target, command Command) (*Response, error) {
	if transport.commands == nil {
		return nil, ErrControlOperationUnsupported
	}
	completed, err := transport.commands.SubmitAndWait(ctx, nodecommand.SubmitInput{
		ServerID: target.NodeID, CommandType: string(command.Type), SchemaVersion: command.SchemaVersion,
		IdempotencyKey: command.IdempotencyKey, PayloadJSON: command.PayloadJSON, TraceID: uuid.NewString(),
	})
	if err != nil {
		return nil, err
	}
	if completed.State == model.NodeCommandExpired {
		return nil, fmt.Errorf("node command %s expired before execution", completed.CommandID)
	}
	var result nodecontract.CommandResponse
	if err := json.Unmarshal([]byte(completed.ResultJSON), &result); err != nil {
		return nil, fmt.Errorf("decode node command %s result: %w", completed.CommandID, err)
	}
	if result.StatusCode <= 0 {
		return nil, fmt.Errorf("node command %s returned no status code", completed.CommandID)
	}
	return &Response{StatusCode: result.StatusCode, Header: http.Header(result.Header), Body: result.Body}, nil
}

func (transport *ControlStreamTransport) Notify(ctx context.Context, target Target, notification Notification) (*Response, error) {
	if transport.sessions == nil {
		return nil, nodesession.ErrSessionNotFound
	}
	revision := uint64(0)
	if transport.resolveRevision != nil {
		value, err := transport.resolveRevision(ctx, target, notification)
		if err != nil {
			return nil, fmt.Errorf("resolve node notification revision: %w", err)
		}
		revision = value
	}
	now := time.Now().UTC()
	frame := &nodev1.SystemControlFrame{FrameId: uuid.NewString(), SentAt: timestamppb.New(now)}
	switch notification.Type {
	case nodecontract.NotificationConfigRevisionChanged:
		frame.Payload = &nodev1.SystemControlFrame_ConfigRevisionChanged{
			ConfigRevisionChanged: &nodev1.ConfigRevisionChanged{Revision: revision},
		}
	case nodecontract.NotificationFilterRevisionChanged:
		frame.Payload = &nodev1.SystemControlFrame_FilterRevisionChanged{
			FilterRevisionChanged: &nodev1.FilterRevisionChanged{Revision: revision},
		}
	default:
		return nil, fmt.Errorf("%w: notification %s", ErrControlOperationUnsupported, notification.Type)
	}
	if err := transport.sessions.Send(ctx, target.NodeID, frame); err != nil {
		return nil, err
	}
	return &Response{StatusCode: http.StatusOK, Header: make(http.Header)}, nil
}

func (transport *ControlStreamTransport) Query(_ context.Context, target Target, request DataRequest) (*Response, error) {
	if transport.commands == nil || request.Type != dataRequestQuarantineReleaseStatus {
		return nil, ErrControlOperationUnsupported
	}
	command, err := transport.commands.FindByPayloadField(target.NodeID, string(nodecontract.CommandQuarantineRelease), "quarantine_key", request.Metadata["quarantine_key"])
	if err != nil {
		if errors.Is(err, store.ErrNodeCommandNotFound) {
			body, _ := json.Marshal(map[string]any{"code": 2003, "message": "release operation not found"})
			return &Response{StatusCode: http.StatusNotFound, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: body}, nil
		}
		return nil, err
	}
	if model.IsTerminalNodeCommandState(command.State) && command.ResultJSON != "" {
		var result nodecontract.CommandResponse
		if err := json.Unmarshal([]byte(command.ResultJSON), &result); err != nil {
			return nil, fmt.Errorf("decode quarantine release command result: %w", err)
		}
		return &Response{StatusCode: result.StatusCode, Header: http.Header(result.Header), Body: result.Body}, nil
	}
	body, _ := json.Marshal(map[string]any{
		"code": 0, "message": "release pending",
		"data": map[string]any{"operation_id": command.IdempotencyKey, "command_id": command.CommandID, "state": command.State},
	})
	return &Response{StatusCode: http.StatusAccepted, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: body}, nil
}

func (transport *ControlStreamTransport) OpenData(context.Context, Target, DataRequest) (*DataResponse, error) {
	return nil, ErrControlOperationUnsupported
}

func (transport *ControlStreamTransport) Probe(context.Context, Target) (*Response, error) {
	return nil, ErrControlOperationUnsupported
}

type MigrationTransport struct {
	legacy         NodeTransport
	control        NodeTransport
	data           NodeTransport
	legacyEnabled  bool
	shadowObserver func(ShadowComparison)
}

type ShadowComparison struct {
	NodeID        uint64
	RequestType   string
	PrimaryOK     bool
	LegacyOK      bool
	StatusMatch   bool
	BodyHashMatch bool
	Error         string
}

func (transport *MigrationTransport) SetShadowObserver(observer func(ShadowComparison)) {
	transport.shadowObserver = observer
}

func NewMigrationTransport(legacy, control NodeTransport, data ...NodeTransport) *MigrationTransport {
	return NewMigrationTransportWithLegacy(legacy, control, true, data...)
}

func NewMigrationTransportWithLegacy(legacy, control NodeTransport, legacyEnabled bool, data ...NodeTransport) *MigrationTransport {
	dataTransport := control
	if len(data) > 0 && data[0] != nil {
		dataTransport = data[0]
	}
	return &MigrationTransport{legacy: legacy, control: control, data: dataTransport, legacyEnabled: legacyEnabled}
}

func (transport *MigrationTransport) legacyOrError() (NodeTransport, error) {
	if !transport.legacyEnabled {
		return nil, ErrLegacyTransportDisabled
	}
	return transport.legacy, nil
}

func (transport *MigrationTransport) Execute(ctx context.Context, target Target, command Command) (*Response, error) {
	if target.TransportMode == model.TransportControlStream || target.TransportMode == model.TransportDual {
		return transport.control.Execute(ctx, target, command)
	}
	legacy, err := transport.legacyOrError()
	if err != nil {
		return nil, err
	}
	return legacy.Execute(ctx, target, command)
}

func (transport *MigrationTransport) Notify(ctx context.Context, target Target, notification Notification) (*Response, error) {
	if transport.control != nil && (target.TransportMode == model.TransportControlStream || target.TransportMode == model.TransportDual || target.TransportMode == "") {
		response, err := transport.control.Notify(ctx, target, notification)
		if err == nil {
			return response, nil
		}
		if target.TransportMode == model.TransportControlStream || !errors.Is(err, nodesession.ErrSessionNotFound) {
			return nil, err
		}
	}
	legacy, err := transport.legacyOrError()
	if err != nil {
		return nil, err
	}
	return legacy.Notify(ctx, target, notification)
}

func (transport *MigrationTransport) Query(ctx context.Context, target Target, request DataRequest) (*Response, error) {
	if request.Type == dataRequestQuarantineReleaseStatus && (target.TransportMode == model.TransportControlStream || target.TransportMode == model.TransportDual) {
		return transport.control.Query(ctx, target, request)
	}
	if target.TransportMode == model.TransportControlStream {
		return transport.data.Query(ctx, target, request)
	}
	if target.TransportMode == model.TransportDual {
		response, err := transport.data.Query(ctx, target, request)
		if err == nil {
			transport.shadowQuery(target, request, response)
			return response, nil
		}
	}
	legacy, err := transport.legacyOrError()
	if err != nil {
		return nil, err
	}
	return legacy.Query(ctx, target, request)
}

func (transport *MigrationTransport) shadowQuery(target Target, request DataRequest, primary *Response) {
	if transport.shadowObserver == nil || !transport.legacyEnabled || transport.legacy == nil || primary == nil {
		return
	}
	go func() {
		shadowCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		legacyResponse, err := transport.legacy.Query(shadowCtx, target, request)
		comparison := ShadowComparison{NodeID: target.NodeID, RequestType: string(request.Type), PrimaryOK: true}
		if err != nil {
			comparison.Error = err.Error()
			transport.shadowObserver(comparison)
			return
		}
		comparison.LegacyOK = legacyResponse != nil
		if legacyResponse != nil {
			comparison.StatusMatch = primary.StatusCode == legacyResponse.StatusCode
			comparison.BodyHashMatch = sha256.Sum256(primary.Body) == sha256.Sum256(legacyResponse.Body)
		}
		transport.shadowObserver(comparison)
	}()
}

func (transport *MigrationTransport) OpenData(ctx context.Context, target Target, request DataRequest) (*DataResponse, error) {
	if target.TransportMode == model.TransportControlStream {
		return transport.data.OpenData(ctx, target, request)
	}
	if target.TransportMode == model.TransportDual {
		response, err := transport.data.OpenData(ctx, target, request)
		if err == nil {
			return response, nil
		}
	}
	legacy, err := transport.legacyOrError()
	if err != nil {
		return nil, err
	}
	return legacy.OpenData(ctx, target, request)
}

func (transport *MigrationTransport) Probe(ctx context.Context, target Target) (*Response, error) {
	legacy, err := transport.legacyOrError()
	if err != nil {
		return nil, err
	}
	return legacy.Probe(ctx, target)
}

var _ NodeTransport = (*ControlStreamTransport)(nil)
var _ NodeTransport = (*MigrationTransport)(nil)

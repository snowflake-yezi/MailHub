package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	nodecommand "github.com/ticket/email-mail-node/internal/command"
	nodecontract "github.com/ticket/email-node-contract"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
)

// ExecuteControlCommand adapts the existing node handlers to the durable
// ControlStream command contract. Business operations stay owned by the same
// managers as the legacy HTTP routes.
func (h *NodeHandler) ExecuteControlCommand(ctx context.Context, command *nodev1.Command) nodecommand.StoredResult {
	if command == nil || command.SchemaVersion != 1 {
		return nodecommand.RejectedResult("unsupported_schema", fmt.Errorf("only command schema version 1 is supported"))
	}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	method, path := http.MethodPost, "/internal/control-command"
	var execute func(*gin.Context)
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(command.PayloadJson, &payload); err != nil {
		return nodecommand.RejectedResult("invalid_payload", fmt.Errorf("invalid command payload"))
	}
	stringValue := func(key string) string {
		var value string
		_ = json.Unmarshal(payload[key], &value)
		return value
	}
	setParam := func(name, value string) {
		ginContext.Params = append(ginContext.Params, gin.Param{Key: name, Value: value})
	}

	switch nodecontract.CommandType(command.Type) {
	case nodecontract.CommandMailboxCreate:
		execute = h.CreateMailbox
	case nodecontract.CommandMailboxDelete:
		method, execute = http.MethodDelete, h.DeleteMailbox
		setParam("email", stringValue("email_address"))
	case nodecontract.CommandMailboxRestore:
		execute = h.RestoreMailbox
		setParam("email", stringValue("email_address"))
	case nodecontract.CommandMailboxPassword:
		method, execute = http.MethodPut, h.UpdateMailboxPassword
		setParam("email", stringValue("email_address"))
	case nodecontract.CommandDomainApply:
		execute = h.AddDomain
	case nodecontract.CommandDomainRemove:
		method, execute = http.MethodDelete, h.RemoveDomain
		setParam("domain", stringValue("domain"))
	case nodecontract.CommandDomainInspect:
		method, execute = http.MethodGet, h.ListDomains
	case nodecontract.CommandMessageDelete:
		method, execute = http.MethodDelete, h.DeleteMessage
		setParam("message_id", stringValue("message_id"))
		path += "?" + url.Values{"mailbox": []string{stringValue("mailbox")}}.Encode()
	case nodecontract.CommandMessageRetentionPurge:
		execute = h.PurgeExpiredMessagesBatch
	case nodecontract.CommandQuarantineRelease:
		execute = h.ReleaseQuarantine
		setParam("quarantine_key", stringValue("quarantine_key"))
	case nodecontract.CommandQuarantineGC:
		execute = h.PurgeExpiredQuarantines
	default:
		return nodecommand.RejectedResult("unsupported_command", fmt.Errorf("unsupported command type %s", command.Type))
	}

	request := httptest.NewRequest(method, path, bytes.NewReader(command.PayloadJson)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	ginContext.Request = request
	execute(ginContext)

	statusCode := recorder.Code
	body := append([]byte(nil), recorder.Body.Bytes()...)
	// A redelivered deletion after the side effect completed is equivalent to
	// success even when the legacy handler can no longer locate the message.
	if nodecontract.CommandType(command.Type) == nodecontract.CommandMessageDelete && statusCode == http.StatusNotFound {
		statusCode = http.StatusOK
		body = []byte(`{"code":0,"message":"message already absent"}`)
	}
	if nodecontract.CommandType(command.Type) == nodecontract.CommandMailboxRestore && statusCode == http.StatusConflict && strings.Contains(string(body), "restore target already exists") {
		statusCode = http.StatusOK
		body = []byte(`{"code":0,"message":"mailbox already restored"}`)
	}
	envelope, err := json.Marshal(nodecontract.CommandResponse{
		StatusCode: statusCode, Header: map[string][]string(recorder.Header().Clone()), Body: json.RawMessage(body),
	})
	if err != nil {
		return nodecommand.FailedResult("encode_result", err)
	}
	state := nodev1.CommandState_COMMAND_STATE_SUCCEEDED
	if statusCode >= 400 && statusCode < 500 {
		state = nodev1.CommandState_COMMAND_STATE_REJECTED
	} else if statusCode >= 500 {
		state = nodev1.CommandState_COMMAND_STATE_FAILED
	} else if nodecontract.CommandType(command.Type) == nodecontract.CommandDomainApply && domainApplyHasWarning(body) {
		state = nodev1.CommandState_COMMAND_STATE_SUCCEEDED_WITH_WARNING
	}
	result := nodecommand.StoredResult{State: state, ResultCode: fmt.Sprintf("http.%d", statusCode), ResultJSON: envelope}
	if state == nodev1.CommandState_COMMAND_STATE_FAILED || state == nodev1.CommandState_COMMAND_STATE_REJECTED {
		result.ErrorMessage = fmt.Sprintf("command returned HTTP %d", statusCode)
	}
	return result
}

func domainApplyHasWarning(body []byte) bool {
	var response struct {
		Data struct {
			PostfixStatus string `json:"postfix_status"`
			DKIMStatus    string `json:"dkim_status"`
		} `json:"data"`
	}
	return json.Unmarshal(body, &response) == nil && response.Data.PostfixStatus == "synced" && response.Data.DKIMStatus == "sync_failed"
}

package nodetransport

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	nodecontract "github.com/ticket/email-node-contract"
)

const dataRequestQuarantineReleaseStatus nodecontract.DataRequestType = "quarantine.release.status.v1"

type RetentionItem struct {
	EmailAddress  string `json:"email_address"`
	RetentionDays int    `json:"retention_days"`
}

func command(commandType nodecontract.CommandType, idempotencyKey string, payload any, method, path string, body any, timeout time.Duration, jsonContent bool) Command {
	payloadJSON, _ := json.Marshal(payload)
	var legacyBody []byte
	if body != nil {
		legacyBody, _ = json.Marshal(body)
	}
	return Command{
		Type: commandType, SchemaVersion: 1, IdempotencyKey: idempotencyKey, PayloadJSON: payloadJSON,
		legacy: legacyRequest{Method: method, Path: path, Body: legacyBody, JSON: jsonContent, Timeout: timeout},
	}
}

func MailboxCreate(email, password string) Command {
	payload := map[string]string{"email_address": email, "password": password}
	return command(nodecontract.CommandMailboxCreate, "mailbox:create:"+email, payload, http.MethodPost, "/internal/mailboxes", payload, 10*time.Second, true)
}

func MailboxDelete(email string, timeout time.Duration) Command {
	payload := map[string]string{"email_address": email}
	return command(nodecontract.CommandMailboxDelete, "mailbox:delete:"+email, payload, http.MethodDelete, "/internal/mailboxes/"+url.PathEscape(email), nil, timeout, false)
}

func MailboxRestore(email, password string) Command {
	payload := map[string]string{"email_address": email, "password": password}
	body := map[string]string{"password": password}
	return command(nodecontract.CommandMailboxRestore, "mailbox:restore:"+email+":"+payloadDigest(payload), payload, http.MethodPost, "/internal/mailboxes/"+url.PathEscape(email)+"/restore", body, 10*time.Second, true)
}

func MailboxPassword(email, password string) Command {
	payload := map[string]string{"email_address": email, "password": password}
	body := map[string]string{"password": password}
	return command(nodecontract.CommandMailboxPassword, "mailbox:password:"+email+":"+payloadDigest(payload), payload, http.MethodPut, "/internal/mailboxes/"+url.PathEscape(email)+"/password", body, 10*time.Second, true)
}

func DomainApply(domain string) Command {
	payload := map[string]string{"domain": domain}
	return command(nodecontract.CommandDomainApply, "domain:apply:"+domain, payload, http.MethodPost, "/internal/domains", payload, 15*time.Second, true)
}

func DomainRemove(domain string) Command {
	payload := map[string]string{"domain": domain}
	return command(nodecontract.CommandDomainRemove, "domain:remove:"+domain, payload, http.MethodDelete, "/internal/domains/"+url.PathEscape(domain), nil, 15*time.Second, false)
}

func MessageDelete(messageID, mailbox string) Command {
	query := url.Values{"mailbox": []string{mailbox}}
	payload := map[string]string{"message_id": messageID, "mailbox": mailbox}
	return command(nodecontract.CommandMessageDelete, "message:delete:"+mailbox+":"+messageID, payload, http.MethodDelete, "/internal/messages/"+url.PathEscape(messageID)+"?"+query.Encode(), nil, 10*time.Second, true)
}

func MessageRetentionPurge(items []RetentionItem, timeout time.Duration) Command {
	payload := map[string]any{"items": items}
	window := time.Now().UTC().Format("2006-01-02T15")
	return command(nodecontract.CommandMessageRetentionPurge, "retention:"+window+":"+payloadDigest(payload), payload, http.MethodPost, "/internal/messages/retention/purge", payload, timeout, true)
}

func QuarantineRelease(key, operationID string) Command {
	payload := map[string]string{"quarantine_key": key, "operation_id": operationID}
	body := map[string]string{"operation_id": operationID}
	return command(nodecontract.CommandQuarantineRelease, operationID, payload, http.MethodPost, "/internal/filter-quarantines/"+url.PathEscape(key)+"/release", body, 10*time.Second, true)
}

func QuarantineGC(retentionDays int, timeout time.Duration) Command {
	payload := map[string]int{"retention_days": retentionDays}
	window := time.Now().UTC().Format("2006-01-02T15")
	return command(nodecontract.CommandQuarantineGC, fmt.Sprintf("quarantine-gc:%d:%s", retentionDays, window), payload, http.MethodPost, "/internal/filter-quarantines/gc", payload, timeout, true)
}

func DomainInspect(domain, requestID string) Command {
	payload := map[string]string{"domain": domain}
	return command(nodecontract.CommandDomainInspect, "domain:inspect:"+domain+":"+requestID, payload, http.MethodGet, "/internal/domains", nil, 10*time.Second, false)
}

func payloadDigest(payload any) string {
	value, _ := json.Marshal(payload)
	digest := sha256.Sum256(value)
	return fmt.Sprintf("%x", digest[:8])
}

func notification(notificationType nodecontract.NotificationType, timeout time.Duration, path string) Notification {
	return Notification{
		Type: notificationType, PayloadJSON: []byte("{}"),
		legacy: legacyRequest{Method: http.MethodPost, Path: path, Timeout: timeout},
	}
}

func ConfigRevisionChanged(timeout time.Duration) Notification {
	return notification(nodecontract.NotificationConfigRevisionChanged, timeout, "/internal/configs/reload")
}

func FilterRevisionChanged() Notification {
	return notification(nodecontract.NotificationFilterRevisionChanged, 10*time.Second, "/internal/filters/reload")
}

func dataRequest(requestType nodecontract.DataRequestType, metadata map[string]string, path string, timeout time.Duration, raw bool, jsonContent bool) DataRequest {
	return DataRequest{
		Type: requestType, Metadata: metadata,
		legacy: legacyRequest{Method: http.MethodGet, Path: path, Timeout: timeout, RawHeaderTimeout: raw, JSON: jsonContent},
	}
}

func MessageList(mailbox, page, size string) DataRequest {
	query := url.Values{"page": []string{page}, "size": []string{size}}
	path := "/internal/mailboxes/" + url.PathEscape(mailbox) + "/messages?" + query.Encode()
	return dataRequest(nodecontract.DataRequestMessageList, map[string]string{"mailbox": mailbox, "page": page, "size": size}, path, 10*time.Second, false, true)
}

func MessageBody(messageID, mailbox string) DataRequest {
	query := url.Values{"mailbox": []string{mailbox}}
	path := "/internal/messages/" + url.PathEscape(messageID) + "?" + query.Encode()
	return dataRequest(nodecontract.DataRequestMessageBody, map[string]string{"message_id": messageID, "mailbox": mailbox}, path, 10*time.Second, false, true)
}

func MessageRaw(messageID, mailbox string) DataRequest {
	query := url.Values{"mailbox": []string{mailbox}}
	path := "/internal/messages/" + url.PathEscape(messageID) + "/raw?" + query.Encode()
	return dataRequest(nodecontract.DataRequestMessageRaw, map[string]string{"message_id": messageID, "mailbox": mailbox}, path, 0, true, false)
}

func MessageAttachment(messageID, mailbox, index string) DataRequest {
	query := url.Values{"mailbox": []string{mailbox}}
	path := "/internal/messages/" + url.PathEscape(messageID) + "/attachments/" + url.PathEscape(index) + "?" + query.Encode()
	metadata := map[string]string{"message_id": messageID, "mailbox": mailbox, "attachment_index": index}
	return dataRequest(nodecontract.DataRequestMessageAttachment, metadata, path, 60*time.Second, false, false)
}

func MessageAttachmentPreview(messageID, mailbox, index string) DataRequest {
	query := url.Values{"mailbox": []string{mailbox}}
	path := "/internal/messages/" + url.PathEscape(messageID) + "/attachments/" + url.PathEscape(index) + "/preview?" + query.Encode()
	metadata := map[string]string{"message_id": messageID, "mailbox": mailbox, "attachment_index": index}
	return dataRequest(nodecontract.DataRequestMessageAttachmentPreview, metadata, path, 60*time.Second, false, false)
}

func QuarantineMessage(key string) DataRequest {
	return dataRequest(nodecontract.DataRequestQuarantineMessage, map[string]string{"quarantine_key": key}, "/internal/filter-quarantines/"+url.PathEscape(key)+"/message", 10*time.Second, false, true)
}

func QuarantineAttachment(key, index string) DataRequest {
	metadata := map[string]string{"quarantine_key": key, "attachment_index": index}
	path := "/internal/filter-quarantines/" + url.PathEscape(key) + "/attachments/" + url.PathEscape(index)
	return dataRequest(nodecontract.DataRequestQuarantineAttachment, metadata, path, 60*time.Second, false, false)
}

func QuarantineReleaseStatus(key string) DataRequest {
	path := "/internal/filter-quarantines/" + url.PathEscape(key) + "/release-status"
	return dataRequest(dataRequestQuarantineReleaseStatus, map[string]string{"quarantine_key": key}, path, 10*time.Second, false, true)
}

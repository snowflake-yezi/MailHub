package nodecontract

import (
	"encoding/json"
	"net/http"
)

const (
	ProtocolVersionV1 uint32 = 1

	AuthorizationMetadataKey = "authorization"
	NodeUUIDMetadataKey      = "x-mailhub-node-uuid"
	NodeAuthorizationScheme  = "Node"

	MaxDataChunkSize = 256 * 1024
)

type CommandType string

const (
	CommandDomainApply           CommandType = "domain.apply.v1"
	CommandDomainRemove          CommandType = "domain.remove.v1"
	CommandDomainInspect         CommandType = "domain.inspect.v1"
	CommandMailboxCreate         CommandType = "mailbox.create.v1"
	CommandMailboxPassword       CommandType = "mailbox.password.v1"
	CommandMailboxDelete         CommandType = "mailbox.delete.v1"
	CommandMailboxRestore        CommandType = "mailbox.restore.v1"
	CommandMessageDelete         CommandType = "message.delete.v1"
	CommandMessageRetentionPurge CommandType = "message.retention.purge.v1"
	CommandQuarantineRelease     CommandType = "quarantine.release.v1"
	CommandQuarantineGC          CommandType = "quarantine.gc.v1"
)

var CommandTypesV1 = []CommandType{
	CommandDomainApply,
	CommandDomainRemove,
	CommandDomainInspect,
	CommandMailboxCreate,
	CommandMailboxPassword,
	CommandMailboxDelete,
	CommandMailboxRestore,
	CommandMessageDelete,
	CommandMessageRetentionPurge,
	CommandQuarantineRelease,
	CommandQuarantineGC,
}

// CommandResponse preserves the legacy HTTP-shaped command result while the
// command itself is delivered over ControlStream.
type CommandResponse struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header,omitempty"`
	Body       json.RawMessage     `json:"body,omitempty"`
}

type NotificationType string

const (
	NotificationConfigRevisionChanged NotificationType = "config.revision.changed.v1"
	NotificationFilterRevisionChanged NotificationType = "filter.revision.changed.v1"
)

var NotificationTypesV1 = []NotificationType{
	NotificationConfigRevisionChanged,
	NotificationFilterRevisionChanged,
}

type DataRequestType string

const (
	DataRequestMessageList              DataRequestType = "message.list.v1"
	DataRequestMessageBody              DataRequestType = "message.body.v1"
	DataRequestMessageRaw               DataRequestType = "message.raw.v1"
	DataRequestMessageAttachment        DataRequestType = "message.attachment.v1"
	DataRequestMessageAttachmentPreview DataRequestType = "message.attachment.preview.v1"
	DataRequestQuarantineMessage        DataRequestType = "quarantine.message.v1"
	DataRequestQuarantineAttachment     DataRequestType = "quarantine.attachment.v1"
)

var DataRequestTypesV1 = []DataRequestType{
	DataRequestMessageList,
	DataRequestMessageBody,
	DataRequestMessageRaw,
	DataRequestMessageAttachment,
	DataRequestMessageAttachmentPreview,
	DataRequestQuarantineMessage,
	DataRequestQuarantineAttachment,
}

const (
	AdminNodeEnrollmentsRoute              = "/api/v1/admin/node-enrollments"
	AdminNodeEnrollmentRevokeRoute         = "/api/v1/admin/node-enrollments/:id/revoke"
	AdminNodeEnrollmentRequestsRoute       = "/api/v1/admin/node-enrollment-requests"
	AdminNodeEnrollmentRequestRoute        = "/api/v1/admin/node-enrollment-requests/:id"
	AdminNodeEnrollmentRequestApproveRoute = "/api/v1/admin/node-enrollment-requests/:id/approve"
	AdminNodeEnrollmentRequestRejectRoute  = "/api/v1/admin/node-enrollment-requests/:id/reject"
	AdminNodeCredentialRotateRoute         = "/api/v1/admin/servers/:id/credentials/rotate"
	AdminNodeCredentialRevokeRoute         = "/api/v1/admin/servers/:id/credentials/revoke"
	NodeEnrollmentClaimRoute               = "/api/v1/node-enrollments/claim"
	NodeEnrollmentRequestRoute             = "/api/v1/node-enrollments/requests/:id"
	NodeEnrollmentRequestCompleteRoute     = "/api/v1/node-enrollments/requests/:id/complete"
)

type HTTPRoute struct {
	Method string
	Path   string
}

var EnrollmentRoutesV1 = []HTTPRoute{
	{Method: http.MethodPost, Path: AdminNodeEnrollmentsRoute},
	{Method: http.MethodGet, Path: AdminNodeEnrollmentsRoute},
	{Method: http.MethodPost, Path: AdminNodeEnrollmentRevokeRoute},
	{Method: http.MethodGet, Path: AdminNodeEnrollmentRequestsRoute},
	{Method: http.MethodGet, Path: AdminNodeEnrollmentRequestRoute},
	{Method: http.MethodPost, Path: AdminNodeEnrollmentRequestApproveRoute},
	{Method: http.MethodPost, Path: AdminNodeEnrollmentRequestRejectRoute},
	{Method: http.MethodPost, Path: AdminNodeCredentialRotateRoute},
	{Method: http.MethodPost, Path: AdminNodeCredentialRevokeRoute},
	{Method: http.MethodPost, Path: NodeEnrollmentClaimRoute},
	{Method: http.MethodGet, Path: NodeEnrollmentRequestRoute},
	{Method: http.MethodPost, Path: NodeEnrollmentRequestCompleteRoute},
}

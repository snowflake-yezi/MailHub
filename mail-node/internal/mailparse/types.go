package mailparse

import (
	"time"

	"github.com/ticket/email-filter-contract"
)

const TextPreviewLimit = 300

const (
	ParserVersion = "body-projector.v2"
	PolicyVersion = "mime-policy.v2"

	defaultMaxMessageBytes   = 25 * 1024 * 1024
	defaultMaxTextBytes      = 1024 * 1024
	defaultMaxHTMLBytes      = 2 * 1024 * 1024
	defaultMaxURLs           = 200
	defaultMaxAttachments    = 100
	defaultMaxHeaderValues   = 1000
	defaultMaxWarnings       = 100
	defaultMaxParts          = 1000
	defaultMaxDepth          = 64
	defaultMaxReferences     = 4096
	defaultMaxReferenceBytes = 2048
)

type ProjectorMode string

const (
	ProjectorLegacy  ProjectorMode = "legacy"
	ProjectorShadow  ProjectorMode = "shadow"
	ProjectorEnforce ProjectorMode = "enforce"
)

type PartPath []int

type PartRole string

const (
	RoleBodyPlain       PartRole = "body_plain"
	RoleBodyHTML        PartRole = "body_html"
	RoleRelatedResource PartRole = "related_resource"
	RoleAttachment      PartRole = "attachment"
	RoleEmbeddedMessage PartRole = "embedded_message"
	RoleReport          PartRole = "report"
	RoleSignature       PartRole = "signature"
	RoleEncrypted       PartRole = "encrypted"
	RoleUnknown         PartRole = "unknown"
)

type BodyView struct {
	GroupID      string   `json:"group_id"`
	PlainPath    PartPath `json:"plain_path"`
	HTMLPath     PartPath `json:"html_path"`
	SelectedPath PartPath `json:"selected_path"`
	RelatedRoot  PartPath `json:"related_root"`
}

type ProjectedPart struct {
	Path                PartPath   `json:"path"`
	ParentPath          PartPath   `json:"parent_path"`
	Role                PartRole   `json:"role"`
	DeclaredContentType string     `json:"declared_content_type"`
	Disposition         string     `json:"disposition,omitempty"`
	Filename            string     `json:"filename,omitempty"`
	ContentID           string     `json:"content_id,omitempty"`
	ContentLocation     string     `json:"content_location,omitempty"`
	DecodedSize         int64      `json:"decoded_size"`
	ExternalIndex       *int       `json:"external_index,omitempty"`
	ReferencedBy        []PartPath `json:"referenced_by"`
}

type ParseWarning struct {
	Code string   `json:"code"`
	Path PartPath `json:"path"`
}

type ParseStatus string

const (
	ParseOK       ParseStatus = "ok"
	ParsePartial  ParseStatus = "partial"
	ParseFailed   ParseStatus = "failed"
	ParseTooLarge ParseStatus = "too_large"
)

type ParsedAttachment struct {
	Index       int    `json:"index"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Disposition string `json:"disposition"`
	ContentID   string `json:"content_id,omitempty"`
	Inline      bool   `json:"inline"`
}

type ParsedMessage struct {
	MessageID        string             `json:"message_id"`
	Mailbox          string             `json:"mailbox"`
	Subject          string             `json:"subject"`
	From             string             `json:"from"`
	To               []string           `json:"to,omitempty"`
	Cc               []string           `json:"cc,omitempty"`
	Date             *time.Time         `json:"date,omitempty"`
	ReceivedAt       *time.Time         `json:"received_at,omitempty"`
	TextBody         string             `json:"text_body,omitempty"`
	HTMLBody         string             `json:"html_body,omitempty"`
	TextPreview      string             `json:"text_preview,omitempty"`
	HasAttachments   bool               `json:"has_attachments"`
	AttachmentsCount int                `json:"attachments_count"`
	Attachments      []ParsedAttachment `json:"attachments"`
	Headers          map[string]string  `json:"headers,omitempty"`
	ParseStatus      string             `json:"parse_status"`
	ParseError       string             `json:"parse_error,omitempty"`
}

type PartInfo struct {
	Filename    string
	ContentType string
}

type ParsedPart struct {
	Content []byte
	Info    PartInfo
	Inline  bool
}

type Limits struct {
	MaxMessageBytes   int64
	MaxPartBytes      int64
	MaxDecodedBytes   int64
	MaxTextBytes      int
	MaxHTMLBytes      int
	MaxURLs           int
	MaxAttachments    int
	MaxHeaderValues   int
	MaxWarnings       int
	MaxParts          int
	MaxDepth          int
	MaxReferences     int
	MaxReferenceBytes int
}

func DefaultLimits() Limits {
	return Limits{
		MaxMessageBytes:   defaultMaxMessageBytes,
		MaxPartBytes:      defaultMaxMessageBytes,
		MaxDecodedBytes:   2 * defaultMaxMessageBytes,
		MaxTextBytes:      defaultMaxTextBytes,
		MaxHTMLBytes:      defaultMaxHTMLBytes,
		MaxURLs:           defaultMaxURLs,
		MaxAttachments:    defaultMaxAttachments,
		MaxHeaderValues:   defaultMaxHeaderValues,
		MaxWarnings:       defaultMaxWarnings,
		MaxParts:          defaultMaxParts,
		MaxDepth:          defaultMaxDepth,
		MaxReferences:     defaultMaxReferences,
		MaxReferenceBytes: defaultMaxReferenceBytes,
	}
}

type Options struct {
	Mailbox           string
	MaildirBase       string
	MaildirUniqueName string
	ServerID          uint64
	Limits            Limits
	ProjectorMode     ProjectorMode
}

type ParseResult struct {
	ParserVersion string                      `json:"parser_version"`
	PolicyVersion string                      `json:"policy_version"`
	Status        ParseStatus                 `json:"status"`
	ErrorCode     string                      `json:"error_code,omitempty"`
	Message       *ParsedMessage              `json:"message"`
	PrimaryView   *BodyView                   `json:"primary_view,omitempty"`
	BodyViews     []BodyView                  `json:"body_views"`
	Parts         []ProjectedPart             `json:"parts"`
	Warnings      []ParseWarning              `json:"warnings"`
	Features      filtercontract.MailFeatures `json:"features"`
}

type Result = ParseResult

type MailFeatures = filtercontract.MailFeatures
type MailAddress = filtercontract.MailAddress
type URLFeature = filtercontract.URLFeature
type AttachmentFeature = filtercontract.AttachmentFeature

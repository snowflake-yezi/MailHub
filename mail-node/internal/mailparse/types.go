package mailparse

import (
	"time"

	"github.com/ticket/email-mail-node/internal/filtercontract"
)

const TextPreviewLimit = 300

const (
	defaultMaxMessageBytes = 25 * 1024 * 1024
	defaultMaxPartBytes    = 10 * 1024 * 1024
	defaultMaxTextBytes    = 1024 * 1024
	defaultMaxHTMLBytes    = 2 * 1024 * 1024
	defaultMaxURLs         = 200
	defaultMaxAttachments  = 100
	defaultMaxHeaderValues = 1000
	defaultMaxWarnings     = 100
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

type Limits struct {
	MaxMessageBytes int64
	MaxPartBytes    int64
	MaxTextBytes    int
	MaxHTMLBytes    int
	MaxURLs         int
	MaxAttachments  int
	MaxHeaderValues int
	MaxWarnings     int
}

func DefaultLimits() Limits {
	return Limits{
		MaxMessageBytes: defaultMaxMessageBytes,
		MaxPartBytes:    defaultMaxPartBytes,
		MaxTextBytes:    defaultMaxTextBytes,
		MaxHTMLBytes:    defaultMaxHTMLBytes,
		MaxURLs:         defaultMaxURLs,
		MaxAttachments:  defaultMaxAttachments,
		MaxHeaderValues: defaultMaxHeaderValues,
		MaxWarnings:     defaultMaxWarnings,
	}
}

type Options struct {
	Mailbox           string
	MaildirBase       string
	MaildirUniqueName string
	ServerID          uint64
	Limits            Limits
}

type Result struct {
	Message  *ParsedMessage
	Features filtercontract.MailFeatures
}

type MailFeatures = filtercontract.MailFeatures
type MailAddress = filtercontract.MailAddress
type URLFeature = filtercontract.URLFeature
type AttachmentFeature = filtercontract.AttachmentFeature

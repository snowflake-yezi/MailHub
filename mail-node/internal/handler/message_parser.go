package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jhillyerd/enmime"
)

const textPreviewLimit = 300

type parsedAttachment struct {
	Index       int    `json:"index"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Disposition string `json:"disposition"`
	ContentID   string `json:"content_id,omitempty"`
	Inline      bool   `json:"inline"`
}

type parsedMessage struct {
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
	Attachments      []parsedAttachment `json:"attachments"`
	Headers          map[string]string  `json:"headers,omitempty"`
	ParseStatus      string             `json:"parse_status"`
	ParseError       string             `json:"parse_error,omitempty"`
}

func parseMaildirMessage(filePath, mailbox, maildirBase string) (*parsedMessage, error) {
	msg, err := parseMessageFile(filePath, mailbox, maildirBase, false)
	if err != nil {
		return nil, err
	}
	msg.TextBody = ""
	msg.HTMLBody = ""
	msg.Headers = nil
	return msg, nil
}

func parseFullMessage(filePath, mailbox, maildirBase string) (*parsedMessage, error) {
	return parseMessageFile(filePath, mailbox, maildirBase, true)
}

func parseMessageFile(filePath, mailbox, maildirBase string, includeBody bool) (*parsedMessage, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	receivedAt := stat.ModTime()

	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	envelope, err := enmime.ReadEnvelope(file)
	if err != nil {
		fallbackID := fallbackMessageID(filePath, maildirBase, stat)
		return &parsedMessage{
			MessageID:   fallbackID,
			Mailbox:     mailbox,
			ReceivedAt:  &receivedAt,
			Attachments: []parsedAttachment{},
			ParseStatus: "failed",
			ParseError:  err.Error(),
		}, nil
	}

	messageID := strings.TrimSpace(envelope.GetHeader("Message-ID"))
	if messageID == "" {
		messageID = fallbackMessageID(filePath, maildirBase, stat)
	}

	date := parseEnvelopeDate(envelope)
	attachments := collectAttachments(envelope)
	textBody := strings.TrimSpace(envelope.Text)
	htmlBody := strings.TrimSpace(envelope.HTML)
	if textBody == "" && htmlBody != "" {
		textBody = htmlToPlainText(htmlBody)
	}

	msg := &parsedMessage{
		MessageID:        messageID,
		Mailbox:          mailbox,
		Subject:          strings.TrimSpace(envelope.GetHeader("Subject")),
		From:             strings.TrimSpace(envelope.GetHeader("From")),
		To:               addressStrings(envelope, "To"),
		Cc:               addressStrings(envelope, "Cc"),
		Date:             date,
		ReceivedAt:       &receivedAt,
		TextPreview:      truncateRunes(textBody, textPreviewLimit),
		HasAttachments:   len(attachments) > 0,
		AttachmentsCount: len(attachments),
		Attachments:      attachments,
		ParseStatus:      "ok",
	}

	if includeBody {
		msg.TextBody = textBody
		msg.HTMLBody = htmlBody
		msg.Headers = envelopeHeaders(envelope)
	}
	if len(envelope.Errors) > 0 {
		msg.ParseStatus = "partial"
		msg.ParseError = envelope.Errors[0].Error()
	}
	return msg, nil
}

func fallbackMessageID(filePath, maildirBase string, stat os.FileInfo) string {
	rel, err := filepath.Rel(maildirBase, filePath)
	if err != nil {
		rel = filepath.Base(filePath)
	}
	seed := fmt.Sprintf("%s|%d|%d", filepath.ToSlash(rel), stat.Size(), stat.ModTime().UnixNano())
	sum := sha256.Sum256([]byte(seed))
	return "fallback-" + hex.EncodeToString(sum[:])
}

func parseEnvelopeDate(envelope *enmime.Envelope) *time.Time {
	date, err := envelope.Date()
	if err != nil || date.IsZero() {
		return nil
	}
	return &date
}

func collectAttachments(envelope *enmime.Envelope) []parsedAttachment {
	attachments := make([]parsedAttachment, 0, len(envelope.Attachments)+len(envelope.Inlines))
	for _, part := range envelope.Attachments {
		attachments = append(attachments, attachmentFromPart(len(attachments), part, false))
	}
	for _, part := range envelope.Inlines {
		attachments = append(attachments, attachmentFromPart(len(attachments), part, true))
	}
	return attachments
}

func attachmentFromPart(index int, part *enmime.Part, inline bool) parsedAttachment {
	filename := strings.TrimSpace(part.FileName)
	if filename == "" {
		filename = fmt.Sprintf("attachment-%d", index)
	}
	disposition := strings.TrimSpace(part.Disposition)
	if disposition == "" && inline {
		disposition = "inline"
	}
	return parsedAttachment{
		Index:       index,
		Filename:    filename,
		ContentType: part.ContentType,
		Size:        int64(len(part.Content)),
		Disposition: disposition,
		ContentID:   strings.Trim(part.ContentID, "<>"),
		Inline:      inline,
	}
}

func addressStrings(envelope *enmime.Envelope, key string) []string {
	addresses, err := envelope.AddressList(key)
	if err != nil || len(addresses) == 0 {
		return []string{}
	}
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, address.String())
	}
	return result
}

func envelopeHeaders(envelope *enmime.Envelope) map[string]string {
	headers := map[string]string{}
	for _, key := range envelope.GetHeaderKeys() {
		values := envelope.GetHeaderValues(key)
		if len(values) > 0 {
			headers[strings.ToLower(key)] = strings.Join(values, ", ")
		}
	}
	return headers
}

func htmlToPlainText(input string) string {
	var b strings.Builder
	inTag := false
	lastSpace := false
	for _, r := range input {
		switch r {
		case '<':
			inTag = true
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
		case '>':
			inTag = false
		default:
			if inTag {
				continue
			}
			if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
				if !lastSpace {
					b.WriteRune(' ')
					lastSpace = true
				}
				continue
			}
			b.WriteRune(r)
			lastSpace = false
		}
	}
	return strings.TrimSpace(html.UnescapeString(b.String()))
}

func truncateRunes(input string, limit int) string {
	input = strings.TrimSpace(input)
	if limit <= 0 || utf8.RuneCountInString(input) <= limit {
		return input
	}
	runes := []rune(input)
	return string(runes[:limit]) + "..."
}

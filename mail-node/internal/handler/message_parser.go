package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jhillyerd/enmime"
)

const textPreviewLimit = 300

var cidReferencePattern = regexp.MustCompile(`(?i)cid:([^"'\\s>]+)`)

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

// collectAttachmentParts 按固定顺序展平附件 part（先 envelope.Attachments 后 envelope.Inlines），
// 保留 part.Content 字节，供下载端点按 index 取原始内容。
// 顺序必须与 collectAttachments 完全一致——二者共用本函数，单一顺序来源，杜绝 index 错位。
func collectAttachmentParts(envelope *enmime.Envelope) []*enmime.Part {
	parts := make([]*enmime.Part, 0, len(envelope.Attachments)+len(envelope.Inlines))
	parts = append(parts, envelope.Attachments...)
	parts = append(parts, envelope.Inlines...)
	return parts
}

func collectAttachments(envelope *enmime.Envelope) []parsedAttachment {
	parts := collectAttachmentParts(envelope)
	attachmentCount := len(envelope.Attachments)
	inlineContentIDs := htmlCIDReferences(envelope.HTML)
	attachments := make([]parsedAttachment, 0, len(parts))
	for i, part := range parts {
		inline := i >= attachmentCount || isInlinePart(part, inlineContentIDs)
		attachments = append(attachments, attachmentFromPart(i, part, inline))
	}
	return attachments
}

func isInlinePart(part *enmime.Part, inlineContentIDs map[string]struct{}) bool {
	if strings.EqualFold(strings.TrimSpace(part.Disposition), "inline") {
		return true
	}
	contentID := normalizeCID(part.ContentID)
	if contentID == "" {
		return false
	}
	_, ok := inlineContentIDs[contentID]
	return ok
}

func htmlCIDReferences(htmlBody string) map[string]struct{} {
	matches := cidReferencePattern.FindAllStringSubmatch(htmlBody, -1)
	refs := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		if cid := normalizeCID(match[1]); cid != "" {
			refs[cid] = struct{}{}
		}
	}
	return refs
}

func normalizeCID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(strings.ToLower(value), "cid:")
	value = strings.Trim(value, "<>")
	if decoded, err := url.PathUnescape(value); err == nil {
		value = decoded
	}
	return strings.ToLower(strings.Trim(strings.TrimSpace(value), "<>"))
}

func attachmentFromPart(index int, part *enmime.Part, inline bool) parsedAttachment {
	info := inferPartInfo(part, index, inline)
	disposition := strings.TrimSpace(part.Disposition)
	if disposition == "" && inline {
		disposition = "inline"
	}
	return parsedAttachment{
		Index:       index,
		Filename:    info.Filename,
		ContentType: info.ContentType,
		Size:        int64(len(part.Content)),
		Disposition: disposition,
		ContentID:   strings.Trim(part.ContentID, "<>"),
		Inline:      inline,
	}
}

type inferredPartInfo struct {
	Filename    string
	ContentType string
}

func inferPartInfo(part *enmime.Part, index int, inline bool) inferredPartInfo {
	filename := strings.TrimSpace(part.FileName)
	contentType := strings.TrimSpace(part.ContentType)
	inferredContentType, inferredExt := detectImagePart(part.Content)

	if contentType == "" || strings.EqualFold(contentType, "application/octet-stream") {
		if inferredContentType != "" {
			contentType = inferredContentType
		}
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	ext := inferredExt
	if ext == "" {
		ext = extensionForContentType(contentType)
	}

	if filename == "" {
		prefix := "attachment"
		if inline {
			prefix = "inline"
		}
		filename = fmt.Sprintf("%s-%d%s", prefix, index, ext)
	} else if shouldAppendInferredExtension(filename, ext) {
		filename += ext
	}

	return inferredPartInfo{
		Filename:    filename,
		ContentType: contentType,
	}
}

func shouldAppendInferredExtension(filename, ext string) bool {
	if ext == "" {
		return false
	}
	existing := strings.ToLower(filepath.Ext(filename))
	if existing == "" {
		return true
	}
	switch existing {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return false
	default:
		return strings.Contains(filename, "@") || len(existing) > 8
	}
}

func detectImagePart(content []byte) (string, string) {
	switch {
	case len(content) >= 8 && bytes.Equal(content[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png", ".png"
	case len(content) >= 3 && content[0] == 0xFF && content[1] == 0xD8 && content[2] == 0xFF:
		return "image/jpeg", ".jpg"
	case len(content) >= 6 && (bytes.Equal(content[:6], []byte("GIF87a")) || bytes.Equal(content[:6], []byte("GIF89a"))):
		return "image/gif", ".gif"
	case len(content) >= 12 && bytes.Equal(content[:4], []byte("RIFF")) && bytes.Equal(content[8:12], []byte("WEBP")):
		return "image/webp", ".webp"
	case len(content) >= 2 && content[0] == 'B' && content[1] == 'M':
		return "image/bmp", ".bmp"
	default:
		return "", ""
	}
}

func extensionForContentType(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp", "image/x-ms-bmp":
		return ".bmp"
	default:
		return ""
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

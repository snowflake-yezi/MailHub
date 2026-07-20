package mailparse

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

var cidReferencePattern = regexp.MustCompile(`(?i)cid:([^"'\\s>]+)`)

// ParseSummary and ParseFull preserve the existing query response behavior.
// Feature limits apply only to ParseFile and cannot truncate query bodies.
func ParseSummary(filePath, mailbox, maildirBase string) (*ParsedMessage, error) {
	return parseLegacyMessage(filePath, mailbox, maildirBase, false)
}

func ParseFull(filePath, mailbox, maildirBase string) (*ParsedMessage, error) {
	return parseLegacyMessage(filePath, mailbox, maildirBase, true)
}

// ParseFile produces the query model and bounded filter features in one MIME pass.
func ParseFile(filePath string, options Options) (*Result, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	receivedAt := stat.ModTime()
	limits := normalizedLimits(options.Limits)
	uniqueName := options.MaildirUniqueName
	if uniqueName == "" {
		uniqueName = filepath.Base(filePath)
	}

	features := emptyFeatures(options.ServerID, options.Mailbox, uniqueName, stat.Size())
	if stat.Size() > limits.MaxMessageBytes {
		features.ParseWarnings = []string{"message_size_limit_exceeded"}
		return &Result{
			Message: &ParsedMessage{
				MessageID:   FallbackMessageID(filePath, options.MaildirBase, stat),
				Mailbox:     options.Mailbox,
				ReceivedAt:  &receivedAt,
				Attachments: []ParsedAttachment{},
				ParseStatus: "failed",
				ParseError:  "message size limit exceeded",
			},
			Features: features,
		}, nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	envelope, err := enmime.ReadEnvelope(file)
	if err != nil {
		features.ParseWarnings = []string{"mime_parse_failed"}
		return &Result{
			Message: &ParsedMessage{
				MessageID:   FallbackMessageID(filePath, options.MaildirBase, stat),
				Mailbox:     options.Mailbox,
				ReceivedAt:  &receivedAt,
				Attachments: []ParsedAttachment{},
				ParseStatus: "failed",
				ParseError:  err.Error(),
			},
			Features: features,
		}, nil
	}

	message := messageFromEnvelope(filePath, options.Mailbox, options.MaildirBase, stat, envelope, true)
	features = featuresFromEnvelope(envelope, features, limits)
	return &Result{Message: message, Features: features}, nil
}

func parseLegacyMessage(filePath, mailbox, maildirBase string, includeBody bool) (*ParsedMessage, error) {
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
		return &ParsedMessage{
			MessageID:   FallbackMessageID(filePath, maildirBase, stat),
			Mailbox:     mailbox,
			ReceivedAt:  &receivedAt,
			Attachments: []ParsedAttachment{},
			ParseStatus: "failed",
			ParseError:  err.Error(),
		}, nil
	}
	return messageFromEnvelope(filePath, mailbox, maildirBase, stat, envelope, includeBody), nil
}

func messageFromEnvelope(filePath, mailbox, maildirBase string, stat os.FileInfo, envelope *enmime.Envelope, includeBody bool) *ParsedMessage {
	receivedAt := stat.ModTime()
	messageID := strings.TrimSpace(envelope.GetHeader("Message-ID"))
	if messageID == "" {
		messageID = FallbackMessageID(filePath, maildirBase, stat)
	}

	attachments := Attachments(envelope)
	textBody := strings.TrimSpace(envelope.Text)
	htmlBody := strings.TrimSpace(envelope.HTML)
	if textBody == "" && htmlBody != "" {
		textBody = HTMLToPlainText(htmlBody)
	}

	message := &ParsedMessage{
		MessageID:        messageID,
		Mailbox:          mailbox,
		Subject:          strings.TrimSpace(envelope.GetHeader("Subject")),
		From:             strings.TrimSpace(envelope.GetHeader("From")),
		To:               addressStrings(envelope, "To"),
		Cc:               addressStrings(envelope, "Cc"),
		Date:             parseEnvelopeDate(envelope),
		ReceivedAt:       &receivedAt,
		TextPreview:      TruncateRunes(textBody, TextPreviewLimit),
		HasAttachments:   len(attachments) > 0,
		AttachmentsCount: len(attachments),
		Attachments:      attachments,
		ParseStatus:      "ok",
	}
	if includeBody {
		message.TextBody = textBody
		message.HTMLBody = htmlBody
		message.Headers = EnvelopeHeaders(envelope)
	}
	if len(envelope.Errors) > 0 {
		message.ParseStatus = "partial"
		message.ParseError = envelope.Errors[0].Error()
	}
	return message
}

func FallbackMessageID(filePath, maildirBase string, stat os.FileInfo) string {
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

func AttachmentParts(envelope *enmime.Envelope) []*enmime.Part {
	parts := make([]*enmime.Part, 0, len(envelope.Attachments)+len(envelope.Inlines))
	parts = append(parts, envelope.Attachments...)
	parts = append(parts, envelope.Inlines...)
	return parts
}

func Attachments(envelope *enmime.Envelope) []ParsedAttachment {
	parts := AttachmentParts(envelope)
	attachmentCount := len(envelope.Attachments)
	inlineContentIDs := HTMLCIDReferences(envelope.HTML)
	attachments := make([]ParsedAttachment, 0, len(parts))
	for i, part := range parts {
		inline := i >= attachmentCount || IsInlinePart(part, inlineContentIDs)
		attachments = append(attachments, attachmentFromPart(i, part, inline))
	}
	return attachments
}

func IsInlinePart(part *enmime.Part, inlineContentIDs map[string]struct{}) bool {
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

func HTMLCIDReferences(htmlBody string) map[string]struct{} {
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

func attachmentFromPart(index int, part *enmime.Part, inline bool) ParsedAttachment {
	info := InferPartInfo(part, index, inline)
	disposition := strings.TrimSpace(part.Disposition)
	if disposition == "" && inline {
		disposition = "inline"
	}
	return ParsedAttachment{
		Index:       index,
		Filename:    info.Filename,
		ContentType: info.ContentType,
		Size:        int64(len(part.Content)),
		Disposition: disposition,
		ContentID:   strings.Trim(part.ContentID, "<>"),
		Inline:      inline,
	}
}

func InferPartInfo(part *enmime.Part, index int, inline bool) PartInfo {
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
	return PartInfo{Filename: filename, ContentType: contentType}
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

func EnvelopeHeaders(envelope *enmime.Envelope) map[string]string {
	headers := map[string]string{}
	for _, key := range envelope.GetHeaderKeys() {
		values := envelope.GetHeaderValues(key)
		if len(values) > 0 {
			headers[strings.ToLower(key)] = strings.Join(values, ", ")
		}
	}
	return headers
}

func HTMLToPlainText(input string) string {
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

func TruncateRunes(input string, limit int) string {
	input = strings.TrimSpace(input)
	if limit <= 0 || utf8.RuneCountInString(input) <= limit {
		return input
	}
	runes := []rune(input)
	return string(runes[:limit]) + "..."
}

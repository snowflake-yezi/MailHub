package mailparse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jhillyerd/enmime"
	nethtml "golang.org/x/net/html"
)

var cidReferencePattern = regexp.MustCompile(`(?i)cid:([^"'\\s>]+)`)

func ParseSummary(filePath, mailbox, maildirBase string) (*ParsedMessage, error) {
	options := runtimeOptions()
	options.Mailbox = mailbox
	options.MaildirBase = maildirBase
	result, err := parseMessageFile(filePath, options, false)
	if err != nil {
		return nil, err
	}
	return result.Message, nil
}

func ParseFull(filePath, mailbox, maildirBase string) (*ParsedMessage, error) {
	options := runtimeOptions()
	options.Mailbox = mailbox
	options.MaildirBase = maildirBase
	result, err := parseMessageFile(filePath, options, true)
	if err != nil {
		return nil, err
	}
	return result.Message, nil
}

func ParseFile(filePath string, options Options) (*Result, error) {
	return parseMessageFile(filePath, options, true)
}

var errParserPanic = errors.New("MIME parser panic recovered")
var ErrAttachmentIndex = errors.New("attachment index out of range")
var ErrMIMETooLarge = errors.New("MIME projection exceeds configured limits")

func parseMessageFile(filePath string, options Options, includeBody bool) (*ParseResult, error) {
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	runtime := CurrentRuntimeConfig()
	if options.ProjectorMode == "" {
		options.ProjectorMode = runtime.Mode
	}
	if options.Limits.MaxMessageBytes <= 0 {
		options.Limits.MaxMessageBytes = runtime.MaxMessageBytes
	}
	limits := normalizedLimits(options.Limits)
	uniqueName := options.MaildirUniqueName
	if uniqueName == "" {
		uniqueName = filepath.Base(filePath)
	}
	mode := normalizedProjectorMode(options.ProjectorMode)
	messageID := messageIdentityForFile(filePath, options.MaildirBase, stat)

	features := emptyFeatures(options.ServerID, options.Mailbox, uniqueName, stat.Size())
	if stat.Size() > limits.MaxMessageBytes && mode == ProjectorEnforce {
		features.ParseWarnings = []string{"message_size_limit_exceeded"}
		message := emptyParsedMessage(messageID, options.Mailbox, stat)
		message.ParseStatus = string(ParseTooLarge)
		message.ParseError = "message_size_limit_exceeded"
		return &ParseResult{
			ParserVersion: ParserVersion,
			PolicyVersion: PolicyVersion,
			Status:        ParseTooLarge,
			ErrorCode:     "message_size_limit_exceeded",
			Message: &ParsedMessage{
				MessageID:   message.MessageID,
				Mailbox:     message.Mailbox,
				ReceivedAt:  message.ReceivedAt,
				Attachments: message.Attachments,
				ParseStatus: message.ParseStatus,
				ParseError:  message.ParseError,
			},
			BodyViews: []BodyView{},
			Parts:     []ProjectedPart{},
			Warnings:  []ParseWarning{{Code: "message_size_limit_exceeded", Path: PartPath{}}},
			Features:  features,
		}, nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	envelope, err := readMIMEEnvelope(file)
	if err != nil {
		code := "mime_parse_failed"
		if errors.Is(err, errParserPanic) {
			code = "parser_panic"
		}
		features.ParseWarnings = []string{code}
		message := emptyParsedMessage(messageID, options.Mailbox, stat)
		message.ParseError = code
		return &ParseResult{
			ParserVersion: ParserVersion,
			PolicyVersion: PolicyVersion,
			Status:        ParseFailed,
			ErrorCode:     code,
			Message:       message,
			BodyViews:     []BodyView{},
			Parts:         []ProjectedPart{},
			Warnings:      []ParseWarning{{Code: code, Path: PartPath{}}},
			Features:      features,
		}, nil
	}

	projection := projectEnvelope(envelope, limits)
	legacyText := strings.TrimSpace(envelope.Text)
	legacyHTML := strings.TrimSpace(envelope.HTML)
	if legacyText == "" && legacyHTML != "" {
		legacyText = HTMLToPlainText(legacyHTML)
	}
	textBody := legacyText
	htmlBody := legacyHTML
	if mode == ProjectorEnforce {
		textBody = projection.text
		htmlBody = projection.html
	}
	message := messageFromEnvelope(filePath, options.Mailbox, options.MaildirBase, stat, envelope, includeBody, textBody, htmlBody)
	if mode == ProjectorEnforce {
		message.ParseStatus = string(projection.status)
		message.ParseError = projection.errorCode
	}
	features = featuresFromEnvelope(envelope, features, limits)
	if stat.Size() > limits.MaxMessageBytes && mode == ProjectorShadow {
		projection.status = ParseTooLarge
		projection.errorCode = "message_size_limit_exceeded"
		projection.parts = []ProjectedPart{}
		projection.bodyViews = []BodyView{}
		projection.primaryView = nil
		projection.warnings = append(projection.warnings, ParseWarning{Code: "message_size_limit_exceeded", Path: PartPath{}})
	}
	return &ParseResult{
		ParserVersion: ParserVersion,
		PolicyVersion: PolicyVersion,
		Status:        projection.status,
		ErrorCode:     projection.errorCode,
		Message:       message,
		PrimaryView:   projection.primaryView,
		BodyViews:     projection.bodyViews,
		Parts:         projection.parts,
		Warnings:      projection.warnings,
		Features:      features,
	}, nil
}

func readMIMEEnvelope(reader io.Reader) (envelope *enmime.Envelope, err error) {
	defer func() {
		if recover() != nil {
			envelope = nil
			err = errParserPanic
		}
	}()
	parser := enmime.NewParser(
		enmime.SkipMalformedParts(true),
		enmime.MaxStoredPartErrors(8),
		enmime.DisableTextConversion(true),
	)
	return parser.ReadEnvelope(reader)
}

func ParseAttachment(filePath string, index int) (*ParsedPart, error) {
	if index < 0 {
		return nil, ErrAttachmentIndex
	}
	stat, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	runtime := CurrentRuntimeConfig()
	if runtime.Mode == ProjectorEnforce && stat.Size() > runtime.MaxMessageBytes {
		return nil, ErrMIMETooLarge
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	envelope, err := readMIMEEnvelope(file)
	if err != nil {
		return nil, err
	}
	if runtime.Mode == ProjectorEnforce {
		limits := normalizedLimits(Limits{MaxMessageBytes: runtime.MaxMessageBytes})
		if projection := projectEnvelope(envelope, limits); projection.status == ParseTooLarge {
			return nil, ErrMIMETooLarge
		}
	}
	parts := AttachmentParts(envelope)
	if index >= len(parts) {
		return nil, ErrAttachmentIndex
	}
	part := parts[index]
	inline := index >= len(envelope.Attachments) || IsInlinePart(part, HTMLCIDReferences(envelope.HTML))
	return &ParsedPart{
		Content: part.Content,
		Info:    InferPartInfo(part, index, inline),
		Inline:  inline,
	}, nil
}

func emptyParsedMessage(messageID, mailbox string, stat os.FileInfo) *ParsedMessage {
	receivedAt := stat.ModTime()
	return &ParsedMessage{
		MessageID:   messageID,
		Mailbox:     mailbox,
		ReceivedAt:  &receivedAt,
		Attachments: []ParsedAttachment{},
		ParseStatus: string(ParseFailed),
	}
}

func messageIdentityForFile(filePath, maildirBase string, stat os.FileInfo) string {
	file, err := os.Open(filePath)
	if err == nil {
		defer file.Close()
		if messageID, scanErr := ScanMessageID(file); scanErr == nil && messageID != "" {
			return messageID
		}
	}
	return FallbackMessageID(filePath, maildirBase, stat)
}

func messageFromEnvelope(filePath, mailbox, maildirBase string, stat os.FileInfo, envelope *enmime.Envelope, includeBody bool, textBody, htmlBody string) *ParsedMessage {
	receivedAt := stat.ModTime()
	messageID := strings.TrimSpace(envelope.GetHeader("Message-ID"))
	if messageID == "" {
		messageID = FallbackMessageID(filePath, maildirBase, stat)
	}

	attachments := Attachments(envelope)

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
		ParseStatus:      string(ParseOK),
	}
	if includeBody {
		message.TextBody = textBody
		message.HTMLBody = htmlBody
		message.Headers = EnvelopeHeaders(envelope)
	}
	if len(envelope.Errors) > 0 {
		message.ParseStatus = string(ParsePartial)
		message.ParseError = envelope.Errors[0].Error()
	}
	return message
}

func normalizedProjectorMode(mode ProjectorMode) ProjectorMode {
	switch mode {
	case ProjectorShadow, ProjectorEnforce:
		return mode
	default:
		return ProjectorLegacy
	}
}

func FallbackMessageID(filePath, maildirBase string, stat os.FileInfo) string {
	rel, err := filepath.Rel(maildirBase, filePath)
	if err != nil {
		rel = filepath.Base(filePath)
	}
	seed := fmt.Sprintf("%s|%d|%d", canonicalMaildirPhysicalPath(rel), stat.Size(), stat.ModTime().UnixNano())
	sum := sha256.Sum256([]byte(seed))
	return "fallback-" + hex.EncodeToString(sum[:])
}

func canonicalMaildirPhysicalPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) > 1 {
		parent := len(parts) - 2
		if parts[parent] == "new" || parts[parent] == "cur" {
			parts = append(parts[:parent], parts[parent+1:]...)
		}
	}
	if len(parts) > 0 {
		name := parts[len(parts)-1]
		if separator := strings.LastIndex(name, ":2,"); separator >= 0 {
			parts[len(parts)-1] = name[:separator]
		}
	}
	return strings.Join(parts, "/")
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
	if strings.TrimSpace(input) == "" {
		return ""
	}
	tokenizer := nethtml.NewTokenizer(strings.NewReader(input))
	parts := make([]string, 0)
	suppressedDepth := 0
	for {
		switch tokenizer.Next() {
		case nethtml.ErrorToken:
			return strings.Join(parts, " ")
		case nethtml.StartTagToken, nethtml.SelfClosingTagToken:
			token := tokenizer.Token()
			switch strings.ToLower(token.Data) {
			case "script", "style", "head", "template", "noscript":
				suppressedDepth++
			}
		case nethtml.EndTagToken:
			tag, _ := tokenizer.TagName()
			switch strings.ToLower(string(tag)) {
			case "script", "style", "head", "template", "noscript":
				if suppressedDepth > 0 {
					suppressedDepth--
				}
			}
		case nethtml.TextToken:
			if suppressedDepth == 0 {
				if value := strings.Join(strings.Fields(string(tokenizer.Text())), " "); value != "" {
					parts = append(parts, value)
				}
			}
		}
	}
}

func TruncateRunes(input string, limit int) string {
	input = strings.TrimSpace(input)
	if limit <= 0 || utf8.RuneCountInString(input) <= limit {
		return input
	}
	runes := []rune(input)
	return string(runes[:limit]) + "..."
}

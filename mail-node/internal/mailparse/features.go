package mailparse

import (
	"io"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jhillyerd/enmime"
	"github.com/ticket/email-filter-contract"
	"golang.org/x/net/html"
)

var plainURLPattern = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"']+`)
var angleValuePattern = regexp.MustCompile(`<([^>]*)>`)

var excludedFeatureHeaders = map[string]struct{}{
	"bcc":                       {},
	"cc":                        {},
	"content-transfer-encoding": {},
	"content-type":              {},
	"date":                      {},
	"from":                      {},
	"message-id":                {},
	"mime-version":              {},
	"reply-to":                  {},
	"return-path":               {},
	"subject":                   {},
	"to":                        {},
}

func normalizedLimits(limits Limits) Limits {
	defaults := DefaultLimits()
	if limits.MaxMessageBytes <= 0 {
		limits.MaxMessageBytes = defaults.MaxMessageBytes
	}
	if limits.MaxPartBytes <= 0 {
		limits.MaxPartBytes = limits.MaxMessageBytes
	}
	if limits.MaxDecodedBytes <= 0 {
		const maxDecodedBytes = int64(2 * 1024 * 1024 * 1024)
		if limits.MaxMessageBytes > maxDecodedBytes/2 {
			limits.MaxDecodedBytes = maxDecodedBytes
		} else {
			limits.MaxDecodedBytes = 2 * limits.MaxMessageBytes
		}
	}
	if limits.MaxTextBytes <= 0 {
		limits.MaxTextBytes = defaults.MaxTextBytes
	}
	if limits.MaxHTMLBytes <= 0 {
		limits.MaxHTMLBytes = defaults.MaxHTMLBytes
	}
	if limits.MaxURLs <= 0 {
		limits.MaxURLs = defaults.MaxURLs
	}
	if limits.MaxAttachments <= 0 {
		limits.MaxAttachments = defaults.MaxAttachments
	}
	if limits.MaxHeaderValues <= 0 {
		limits.MaxHeaderValues = defaults.MaxHeaderValues
	}
	if limits.MaxWarnings <= 0 {
		limits.MaxWarnings = defaults.MaxWarnings
	}
	if limits.MaxParts <= 0 {
		limits.MaxParts = defaults.MaxParts
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = defaults.MaxDepth
	}
	if limits.MaxReferences <= 0 {
		limits.MaxReferences = defaults.MaxReferences
	}
	if limits.MaxReferenceBytes <= 0 {
		limits.MaxReferenceBytes = defaults.MaxReferenceBytes
	}
	return limits
}

func emptyFeatures(serverID uint64, mailbox, uniqueName string, sizeBytes int64) filtercontract.MailFeatures {
	return filtercontract.MailFeatures{
		MessageKey:    filtercontract.MessageKey(serverID, mailbox, uniqueName, sizeBytes),
		Mailbox:       mailbox,
		ServerID:      serverID,
		ReplyTo:       []filtercontract.MailAddress{},
		Headers:       map[string][]string{},
		URLs:          []filtercontract.URLFeature{},
		Attachments:   []filtercontract.AttachmentFeature{},
		SizeBytes:     sizeBytes,
		ParseWarnings: []string{},
	}
}

func featuresFromEnvelope(envelope *enmime.Envelope, features filtercontract.MailFeatures, limits Limits, attachments []ParsedAttachment) filtercontract.MailFeatures {
	warnings := newWarningSet(limits.MaxWarnings)

	features.MessageID = strings.TrimSpace(envelope.GetHeader("Message-ID"))
	features.Subject = decodedSubject(envelope, warnings)
	features.HeaderFrom = headerFrom(envelope, warnings)
	features.EnvelopeFrom = envelopeFrom(envelope, warnings)
	features.ReplyTo = replyTo(envelope, warnings)
	features.FromReplyToDomainMatch = fromReplyToDomainMatch(features.HeaderFrom, features.ReplyTo)
	features.Headers = featureHeaders(envelope, limits.MaxHeaderValues, warnings)
	features.ListUnsubscribe = hasListUnsubscribe(envelope.GetHeaderValues("List-Unsubscribe"))
	features.ListID = normalizeListID(envelope.GetHeader("List-ID"))
	features.Precedence = normalizePrecedence(envelope.GetHeader("Precedence"))

	plainText := ""
	if hasPlainTextPart(envelope.Root) {
		plainText = strings.TrimSpace(envelope.Text)
	}
	features.Text = limitUTF8Bytes(plainText, limits.MaxTextBytes, "text_limit_exceeded", warnings)
	htmlBody := limitUTF8Bytes(strings.TrimSpace(envelope.HTML), limits.MaxHTMLBytes, "html_limit_exceeded", warnings)

	urls := newURLCollector()
	urls.addText(features.Text)
	features.HTMLText, features.TrackingPixelCount = analyzeHTML(htmlBody, urls, warnings)
	features.URLs = urls.features(limits.MaxURLs, warnings)
	features.Attachments = attachmentFeatures(attachments, limits.MaxAttachments, warnings)
	checkPartLimits(envelope.Root, limits.MaxPartBytes, warnings)
	addEnvelopeWarnings(envelope, warnings)
	features.ParseWarnings = warnings.values()
	return features
}

func decodedSubject(envelope *enmime.Envelope, warnings *warningSet) string {
	rawValues := envelope.GetHeaderValues("Subject")
	if len(rawValues) > 1 {
		warnings.add("duplicate_subject")
	}
	if len(rawValues) == 0 {
		return ""
	}
	decoded := strings.TrimSpace(rawValues[0])
	if strings.Contains(decoded, "=?") {
		warnings.add("subject_decode_failed")
		return ""
	}
	return decoded
}

func headerFrom(envelope *enmime.Envelope, warnings *warningSet) filtercontract.MailAddress {
	raw := strings.TrimSpace(envelope.GetHeader("From"))
	if raw == "" {
		warnings.add("from_missing")
		return filtercontract.MailAddress{}
	}
	addresses, err := envelope.AddressList("From")
	if err != nil || len(addresses) == 0 {
		warnings.add("from_invalid")
		return filtercontract.MailAddress{}
	}
	return normalizeAddress(addresses[0])
}

func envelopeFrom(envelope *enmime.Envelope, warnings *warningSet) *filtercontract.MailAddress {
	raw := strings.TrimSpace(envelope.GetHeader("Return-Path"))
	if raw == "" {
		warnings.add("envelope_from_missing")
		return nil
	}
	address, err := mail.ParseAddress(raw)
	if err != nil || address.Address == "" {
		warnings.add("envelope_from_invalid")
		return nil
	}
	normalized := normalizeAddress(address)
	return &normalized
}

func replyTo(envelope *enmime.Envelope, warnings *warningSet) []filtercontract.MailAddress {
	raw := strings.TrimSpace(envelope.GetHeader("Reply-To"))
	if raw == "" {
		return []filtercontract.MailAddress{}
	}
	addresses, err := envelope.AddressList("Reply-To")
	if err != nil || len(addresses) == 0 {
		warnings.add("reply_to_invalid")
		return []filtercontract.MailAddress{}
	}
	result := make([]filtercontract.MailAddress, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, normalizeAddress(address))
	}
	return result
}

func normalizeAddress(address *mail.Address) filtercontract.MailAddress {
	value := strings.TrimSpace(address.Address)
	domain := ""
	if index := strings.LastIndex(value, "@"); index > 0 && index < len(value)-1 {
		domain = strings.ToLower(value[index+1:])
		value = value[:index+1] + domain
	}
	return filtercontract.MailAddress{
		Name:    strings.TrimSpace(address.Name),
		Address: value,
		Domain:  domain,
	}
}

func fromReplyToDomainMatch(from filtercontract.MailAddress, replyTo []filtercontract.MailAddress) *bool {
	if from.Domain == "" || len(replyTo) == 0 {
		return nil
	}
	for _, address := range replyTo {
		if address.Domain == "" {
			return nil
		}
		if strings.EqualFold(from.Domain, address.Domain) {
			matched := true
			return &matched
		}
	}
	matched := false
	return &matched
}

func featureHeaders(envelope *enmime.Envelope, maxValues int, warnings *warningSet) map[string][]string {
	keys := envelope.GetHeaderKeys()
	sort.Slice(keys, func(i, j int) bool {
		return strings.ToLower(keys[i]) < strings.ToLower(keys[j])
	})
	headers := map[string][]string{}
	valueCount := 0
	for _, key := range keys {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		values := normalizedHeaderValues(envelope.GetHeaderValues(key))
		if lowerKey == "subject" && len(values) > 1 {
			remaining := maxValues - valueCount
			if remaining <= 0 {
				warnings.add("header_values_limit_exceeded")
				return headers
			}
			if len(values) > remaining {
				values = values[:remaining]
				warnings.add("header_values_limit_exceeded")
			}
			headers[lowerKey] = append([]string(nil), values...)
			valueCount += len(values)
			continue
		}
		if _, excluded := excludedFeatureHeaders[lowerKey]; excluded {
			continue
		}
		for _, value := range values {
			if valueCount >= maxValues {
				warnings.add("header_values_limit_exceeded")
				return headers
			}
			headers[lowerKey] = append(headers[lowerKey], value)
			valueCount++
		}
	}
	return headers
}

func normalizedHeaderValues(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func hasListUnsubscribe(values []string) bool {
	for _, value := range values {
		candidates := angleValuePattern.FindAllStringSubmatch(value, -1)
		if len(candidates) == 0 {
			candidates = [][]string{{value, strings.TrimSpace(value)}}
		}
		for _, candidate := range candidates {
			if len(candidate) < 2 {
				continue
			}
			parsed, err := url.ParseRequestURI(strings.TrimSpace(candidate[1]))
			if err == nil && parsed.Scheme != "" {
				return true
			}
		}
	}
	return false
}

func normalizeListID(value string) string {
	value = strings.TrimSpace(value)
	if match := angleValuePattern.FindStringSubmatch(value); len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	return value
}

func normalizePrecedence(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "bulk", "list", "junk":
		return value
	default:
		return ""
	}
}

func hasPlainTextPart(root *enmime.Part) bool {
	if root == nil {
		return false
	}
	matched := false
	root.DepthMatchAll(func(part *enmime.Part) bool {
		if strings.EqualFold(strings.TrimSpace(part.ContentType), "text/plain") && !strings.EqualFold(part.Disposition, "attachment") {
			matched = true
		}
		return false
	})
	return matched
}

func limitUTF8Bytes(value string, limit int, warning string, warnings *warningSet) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	warnings.add(warning)
	return strings.TrimSpace(value)
}

func analyzeHTML(input string, urls *urlCollector, warnings *warningSet) (string, int) {
	if input == "" {
		return "", 0
	}
	tokenizer := html.NewTokenizer(strings.NewReader(input))
	textParts := make([]string, 0)
	suppressedDepth := 0
	trackingPixels := 0
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			if err := tokenizer.Err(); err != nil && err != io.EOF {
				warnings.add("html_invalid")
			}
			return strings.Join(textParts, " "), trackingPixels
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			tag := strings.ToLower(token.Data)
			if tag == "script" || tag == "style" || tag == "head" {
				suppressedDepth++
			}
			for _, attribute := range token.Attr {
				key := strings.ToLower(attribute.Key)
				if key == "href" || key == "src" || key == "action" {
					urls.add(attribute.Val)
				}
			}
			if tag == "img" && isTrackingImage(token.Attr) {
				trackingPixels++
			}
		case html.EndTagToken:
			tag, _ := tokenizer.TagName()
			if (strings.EqualFold(string(tag), "script") || strings.EqualFold(string(tag), "style") || strings.EqualFold(string(tag), "head")) && suppressedDepth > 0 {
				suppressedDepth--
			}
		case html.TextToken:
			if suppressedDepth == 0 {
				if value := strings.Join(strings.Fields(string(tokenizer.Text())), " "); value != "" {
					textParts = append(textParts, value)
				}
			}
		}
	}
}

func isTrackingImage(attributes []html.Attribute) bool {
	values := map[string]string{}
	for _, attribute := range attributes {
		values[strings.ToLower(attribute.Key)] = strings.ToLower(strings.TrimSpace(attribute.Val))
	}
	if _, hidden := values["hidden"]; hidden {
		return true
	}
	width := strings.TrimSuffix(values["width"], "px")
	height := strings.TrimSuffix(values["height"], "px")
	if (width == "1" && height == "1") || width == "0" || height == "0" {
		return true
	}
	style := strings.ReplaceAll(values["style"], " ", "")
	return strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") || strings.Contains(style, "opacity:0")
}

func attachmentFeatures(attachments []ParsedAttachment, maxAttachments int, warnings *warningSet) []filtercontract.AttachmentFeature {
	if len(attachments) > maxAttachments {
		attachments = attachments[:maxAttachments]
		warnings.add("attachment_limit_exceeded")
	}
	features := make([]filtercontract.AttachmentFeature, 0, len(attachments))
	for _, attachment := range attachments {
		features = append(features, filtercontract.AttachmentFeature{
			Index:       attachment.Index,
			Filename:    attachment.Filename,
			ContentType: attachment.ContentType,
			SizeBytes:   attachment.Size,
			Inline:      attachment.Inline,
		})
	}
	return features
}

func checkPartLimits(root *enmime.Part, maxPartBytes int64, warnings *warningSet) {
	if root == nil {
		return
	}
	root.DepthMatchAll(func(part *enmime.Part) bool {
		if int64(len(part.Content)) > maxPartBytes {
			warnings.add("part_size_limit_exceeded")
		}
		return false
	})
}

func addEnvelopeWarnings(envelope *enmime.Envelope, warnings *warningSet) {
	if envelope.Root != nil && strings.HasPrefix(strings.ToLower(envelope.Root.ContentType), "multipart/") && envelope.Root.FirstChild == nil {
		warnings.add("mime_invalid")
	}
	for _, problem := range envelope.Errors {
		switch problem.Name {
		case enmime.ErrorPlainTextFromHTML:
			continue
		case enmime.ErrorCharsetConversion, enmime.ErrorCharsetDeclaration:
			warnings.add("charset_decode_failed")
		case enmime.ErrorMissingBoundary, enmime.ErrorMalformedBase64, enmime.ErrorMalformedHeader,
			enmime.ErrorContentEncoding, enmime.ErrorMalformedChildPart:
			warnings.add("mime_invalid")
		default:
			if problem.Severe {
				warnings.add("mime_invalid")
			}
		}
	}
}

type urlKey struct {
	scheme string
	host   string
	path   string
}

type urlCollector struct {
	counts map[urlKey]int
}

func newURLCollector() *urlCollector {
	return &urlCollector{counts: map[urlKey]int{}}
}

func (collector *urlCollector) addText(value string) {
	for _, match := range plainURLPattern.FindAllString(value, -1) {
		collector.add(strings.TrimRight(match, ".,;:!?)]}"))
	}
}

func (collector *urlCollector) add(value string) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	collector.counts[urlKey{scheme: scheme, host: host, path: path}]++
}

func (collector *urlCollector) features(maxURLs int, warnings *warningSet) []filtercontract.URLFeature {
	keys := make([]urlKey, 0, len(collector.counts))
	for key := range collector.counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].scheme != keys[j].scheme {
			return keys[i].scheme < keys[j].scheme
		}
		if keys[i].host != keys[j].host {
			return keys[i].host < keys[j].host
		}
		return keys[i].path < keys[j].path
	})
	if len(keys) > maxURLs {
		keys = keys[:maxURLs]
		warnings.add("url_limit_exceeded")
	}
	features := make([]filtercontract.URLFeature, 0, len(keys))
	for _, key := range keys {
		features = append(features, filtercontract.URLFeature{
			Scheme:      key.scheme,
			Host:        key.host,
			Path:        key.path,
			Occurrences: collector.counts[key],
		})
	}
	return features
}

type warningSet struct {
	max          int
	valuesByCode map[string]struct{}
}

func newWarningSet(max int) *warningSet {
	return &warningSet{max: max, valuesByCode: map[string]struct{}{}}
}

func (warnings *warningSet) add(code string) {
	if code != "" {
		warnings.valuesByCode[code] = struct{}{}
	}
}

func (warnings *warningSet) values() []string {
	values := make([]string, 0, len(warnings.valuesByCode))
	for value := range warnings.valuesByCode {
		values = append(values, value)
	}
	sort.Strings(values)
	if len(values) <= warnings.max {
		return values
	}
	if warnings.max == 1 {
		return []string{"warning_limit_exceeded"}
	}
	values = append(values[:warnings.max-1], "warning_limit_exceeded")
	sort.Strings(values)
	return values
}

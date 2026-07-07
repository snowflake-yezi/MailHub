package forward

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	maxEmailSizeDefault = 10 * 1024 * 1024 // 10MB hard cap
	bodyPreviewDefault  = 64 * 1024        // 64KB default body preview for filtering
)

// readForFiltering opens the file, reads headers + body preview text
// for filter decision. The caller uses the returned headers and body preview
// to decide whether to forward. bodyLimit controls how much body text to read.
func readForFiltering(filePath string, maxSize, bodyLimit int64) (headers map[string]string, bodyPreview string, err error) {
	if maxSize <= 0 {
		maxSize = maxEmailSizeDefault
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, "", fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("stat: %w", err)
	}
	if fi.Size() > maxSize {
		return nil, "", fmt.Errorf("email too large: %d bytes (max %d)", fi.Size(), maxSize)
	}

	// LimitReader guards against pathological headers
	lr := io.LimitReader(f, maxSize)
	br := bufio.NewReader(lr)

	headers = make(map[string]string)
	var currentKey string

	// Read header lines until \r\n\r\n or \n\n.
	// Handles RFC 5322 header folding: continuation lines (starting with space/tab)
	// are appended to the current header value.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, "", fmt.Errorf("read header line: %w", err)
		}

		// Strip trailing CRLF
		trimmed := strings.TrimRight(line, "\r\n")

		// Empty line = end of headers
		if trimmed == "" {
			break
		}

		// RFC 5322 folding: continuation line
		if strings.HasPrefix(trimmed, " ") || strings.HasPrefix(trimmed, "\t") {
			if currentKey != "" {
				// Append folded continuation with a space separator so parameters
				// like boundary/filename stay parseable in single-line form.
				headers[currentKey] += " " + strings.TrimSpace(trimmed)
			}
			continue
		}

		// New header field
		if strings.Contains(trimmed, ":") {
			parts := strings.SplitN(trimmed, ":", 2)
			key := strings.TrimSpace(strings.ToLower(parts[0]))
			val := strings.TrimSpace(parts[1])
			headers[key] = val
			currentKey = key
		}
	}

	// Read body text for keyword filtering (size controlled by bodyLimit param).
	// br still wraps the file via LimitReader — any bytes already buffered
	// past the header boundary are included.
	bodyReader := io.LimitReader(br, bodyLimit)
	bodyBytes, _ := io.ReadAll(bodyReader)
	bodyPreview = string(bodyBytes)

	return headers, bodyPreview, nil
}

// streamToSMTP sends the email file to union via SMTP.
//
// Robust body-preservation strategy: instead of parsing and selectively
// copying MIME headers (which can drop boundary/filename/disposition
// parameters), we read the raw file, modify ONLY the Subject line in the
// original headers, prepend our forwarding headers, and pass the original
// body through byte-for-byte unchanged.
func streamToSMTP(cfg ForwardConfig, filePath, newSubject, sourceAddr, targetAddress, smtpUser, smtpPass string) error {
	// 1. Read entire raw file (capped at maxEmailSize for safety).
	//    For Phase 1 volumes this is fine; upgrade to streaming if needed.
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, cfg.MaxEmailSize))
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	// 2. Split at the first blank line — header/body boundary.
	//    Use \r\n\r\n (CRLF) first, fall back to \n\n (bare LF).
	headerEnd := []byte("\r\n\r\n")
	idx := bytes.Index(raw, headerEnd)
	if idx < 0 {
		headerEnd = []byte("\n\n")
		idx = bytes.Index(raw, headerEnd)
	}
	if idx < 0 {
		return fmt.Errorf("invalid email: no header/body boundary")
	}
	bodyStart := idx + len(headerEnd)
	originalHeaders := raw[:idx]
	originalBody := raw[bodyStart:]

	// 3. Replace ONLY the Subject line in the original headers.
	//    Everything else (Content-Type, Content-Disposition, DKIM-Sig,
	//    MIME-Version, etc.) stays byte-for-byte intact.
	encodedSubject := mime.QEncoding.Encode("utf-8", newSubject)
	modifiedHeaders := replaceSubject(originalHeaders, encodedSubject)

	// 4. Normalize CID-referenced inline image part headers before SMTP forwarding.
	//    Postfix/Dovecot store the original bytes, but Roundcube renders from the
	//    forwarded raw MIME. Repairing only the API metadata is not enough.
	originalBody = normalizeForwardedInlineImageParts(originalBody)

	// 5. SMTP connection (STARTTLS on port 587)
	host, _, err := net.SplitHostPort(cfg.SMTPHost)
	if err != nil {
		return fmt.Errorf("invalid smtp host %q: %w", cfg.SMTPHost, err)
	}

	conn, err := net.DialTimeout("tcp", cfg.SMTPHost, cfg.SMTPDialTimeout)
	if err != nil {
		return fmt.Errorf("dial %s: %w", cfg.SMTPHost, err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}

	tlsConfig := &tls.Config{
		ServerName:         host,
		MinVersion:         minTLSVersion(cfg.TLSMinVersion),
		InsecureSkipVerify: cfg.TLSInsecureSkip,
	}
	if err := client.StartTLS(tlsConfig); err != nil {
		return fmt.Errorf("starttls: %w", err)
	}
	defer func() {
		if qErr := client.Quit(); qErr != nil {
			_ = qErr
		}
	}()

	auth := smtp.PlainAuth("", smtpUser, smtpPass, host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}

	if err := client.Mail(smtpUser); err != nil {
		return fmt.Errorf("mail from %s: %w", smtpUser, err)
	}
	if err := client.Rcpt(targetAddress); err != nil {
		return fmt.Errorf("rcpt to %s: %w", targetAddress, err)
	}

	// 5. DATA phase: forwarding headers + modified original headers + body
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}

	now := time.Now().Format(time.RFC1123Z)
	fmt.Fprintf(w, "X-Forwarded-By: mail-node\r\n")
	fmt.Fprintf(w, "X-Original-To: %s\r\n", sourceAddr)
	fmt.Fprintf(w, "Resent-From: %s\r\n", targetAddress)
	fmt.Fprintf(w, "Resent-To: %s\r\n", targetAddress)
	fmt.Fprintf(w, "Resent-Date: %s\r\n", now)

	// Original headers (Subject already replaced, everything else intact).
	// NO blank line here — forwarding headers + original headers form one
	// continuous header block. The blank line comes AFTER all headers.
	w.Write(modifiedHeaders)
	w.Write(headerEnd)

	// Original body — byte-for-byte identical to the received email
	w.Write(originalBody)

	// Ensure final CRLF before SMTP dot
	if !bytes.HasSuffix(originalBody, []byte("\r\n")) {
		fmt.Fprint(w, "\r\n")
	}

	return w.Close()
}

// htmlCIDReferenceRE matches cid: URLs in HTML bodies well enough for MIME header repair.
var htmlCIDReferenceRE = regexp.MustCompile(`(?i)cid:([^"'\s>]+)`)

func normalizeForwardedInlineImageParts(body []byte) []byte {
	segments := splitMIMEBodySegments(body)
	if len(segments) == 0 {
		return body
	}

	cidRefs := collectBodyCIDReferences(segments)
	if len(cidRefs) == 0 {
		return body
	}

	changed := false
	for i := range segments {
		if !segments[i].hasHeaders {
			continue
		}
		rawContentID := headerValue(segments[i].headers, "Content-ID")
		contentID := normalizeForwardCID(rawContentID)
		if contentID == "" {
			continue
		}
		if _, ok := cidRefs[contentID]; !ok {
			continue
		}

		decoded, ok := decodeMIMEPartBody(segments[i].body, headerValue(segments[i].headers, "Content-Transfer-Encoding"))
		if !ok {
			continue
		}
		contentType, ext := detectForwardImage(decoded)
		if contentType == "" {
			continue
		}

		filename := partFilenameFromHeaders(segments[i].headers)
		if filename == "" {
			filename = displayForwardCID(rawContentID)
			if filename == "" {
				filename = contentID
			}
		}
		if shouldAppendForwardExtension(filename, ext) {
			filename += ext
		}

		segments[i].headers = upsertHeader(segments[i].headers, "Content-Type", fmt.Sprintf(`%s; name="%s"`, contentType, quoteHeaderParam(filename)))
		segments[i].headers = upsertHeader(segments[i].headers, "Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, quoteHeaderParam(filename)))
		changed = true
	}

	if !changed {
		return body
	}
	return joinMIMEBodySegments(segments)
}

type mimeBodySegment struct {
	prefix     []byte
	headers    []string
	separator  []byte
	body       []byte
	hasHeaders bool
}

func splitMIMEBodySegments(body []byte) []mimeBodySegment {
	matches := regexp.MustCompile(`(?m)^--[^\r\n]+(?:--)?[\t ]*\r?$`).FindAllIndex(body, -1)
	if len(matches) == 0 {
		return nil
	}

	segments := make([]mimeBodySegment, 0, len(matches))
	for i, match := range matches {
		start := match[1]
		if start < len(body) && body[start] == '\r' {
			start++
		}
		if start < len(body) && body[start] == '\n' {
			start++
		}
		end := len(body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		segment := mimeBodySegment{prefix: body[match[0]:start]}
		content := body[start:end]
		sep, sepLen, headerEnd := findHeaderBodySeparator(content)
		if headerEnd < 0 {
			segment.body = content
			segments = append(segments, segment)
			continue
		}
		segment.headers = strings.Split(string(content[:headerEnd]), string(sep))
		segment.separator = content[headerEnd : headerEnd+sepLen]
		segment.body = content[headerEnd+sepLen:]
		segment.hasHeaders = true
		segments = append(segments, segment)
	}
	return segments
}

func findHeaderBodySeparator(content []byte) ([]byte, int, int) {
	if idx := bytes.Index(content, []byte("\r\n\r\n")); idx >= 0 {
		return []byte("\r\n"), 4, idx
	}
	if idx := bytes.Index(content, []byte("\n\n")); idx >= 0 {
		return []byte("\n"), 2, idx
	}
	return nil, 0, -1
}

func joinMIMEBodySegments(segments []mimeBodySegment) []byte {
	var out bytes.Buffer
	for _, segment := range segments {
		out.Write(segment.prefix)
		if segment.hasHeaders {
			lineSep := []byte("\r\n")
			if len(segment.separator) >= 2 && segment.separator[0] == '\n' {
				lineSep = []byte("\n")
			}
			out.WriteString(strings.Join(segment.headers, string(lineSep)))
			out.Write(segment.separator)
		}
		out.Write(segment.body)
	}
	return out.Bytes()
}

func collectBodyCIDReferences(segments []mimeBodySegment) map[string]struct{} {
	refs := map[string]struct{}{}
	for _, segment := range segments {
		if !strings.HasPrefix(strings.ToLower(headerValue(segment.headers, "Content-Type")), "text/html") {
			continue
		}
		decoded, ok := decodeMIMEPartBody(segment.body, headerValue(segment.headers, "Content-Transfer-Encoding"))
		if !ok {
			continue
		}
		for _, match := range htmlCIDReferenceRE.FindAllStringSubmatch(string(decoded), -1) {
			if len(match) < 2 {
				continue
			}
			if cid := normalizeForwardCID(match[1]); cid != "" {
				refs[cid] = struct{}{}
			}
		}
	}
	return refs
}

func decodeMIMEPartBody(body []byte, encoding string) ([]byte, bool) {
	body = bytes.Trim(body, "\r\n")
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		compact := bytes.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, body)
		decoded, err := base64.StdEncoding.DecodeString(string(compact))
		return decoded, err == nil
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
		return decoded, err == nil
	default:
		return body, true
	}
}

func headerValue(headers []string, key string) string {
	prefix := strings.ToLower(key) + ":"
	for _, header := range headers {
		if strings.HasPrefix(strings.ToLower(header), prefix) {
			return strings.TrimSpace(header[len(prefix):])
		}
	}
	return ""
}

func upsertHeader(headers []string, key, value string) []string {
	prefix := strings.ToLower(key) + ":"
	line := key + ": " + value
	for i, header := range headers {
		if strings.HasPrefix(strings.ToLower(header), prefix) {
			headers[i] = line
			return headers
		}
	}
	return append(headers, line)
}

func partFilenameFromHeaders(headers []string) string {
	for _, key := range []string{"Content-Disposition", "Content-Type"} {
		_, params, err := mime.ParseMediaType(headerValue(headers, key))
		if err != nil {
			continue
		}
		if filename := strings.TrimSpace(params["filename"]); filename != "" {
			return filename
		}
		if name := strings.TrimSpace(params["name"]); name != "" {
			return name
		}
	}
	return ""
}

func normalizeForwardCID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(strings.ToLower(value), "cid:")
	value = strings.Trim(value, "<>")
	if decoded, err := url.PathUnescape(value); err == nil {
		value = decoded
	}
	return strings.ToLower(strings.Trim(strings.TrimSpace(value), "<>"))
}

func displayForwardCID(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "<>")
	if decoded, err := url.PathUnescape(value); err == nil {
		value = decoded
	}
	return strings.Trim(strings.TrimSpace(value), "<>")
}

func quoteHeaderParam(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
}

func shouldAppendForwardExtension(filename, ext string) bool {
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

func detectForwardImage(content []byte) (string, string) {
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

// subjectLineRE matches a Subject header line. Case-insensitive with optional
// RFC 2047 encoding and optional folding whitespace after the colon.
var subjectLineRE = regexp.MustCompile(`(?im)^Subject:\s*[^\r\n]*`)

// replaceSubject replaces the Subject line in raw headers with the new value.
// All other headers (including folded continuation lines) are left unchanged.
func replaceSubject(rawHeaders []byte, newSubject string) []byte {
	return subjectLineRE.ReplaceAll(rawHeaders, []byte("Subject: "+newSubject))
}

// buildSubject constructs the forwarded email's Subject line.
// RFC 2047 encoded subjects (e.g. =?utf-8?B?...?=) are decoded first so the
// prefix concatenation produces a clean, readable subject.
func buildSubject(prefixTemplate, sourceAddr string, action Action, originalSubject string) string {
	prefix := strings.ReplaceAll(prefixTemplate, "${source_addr}", sourceAddr)

	// Decode RFC 2047 encoded-word (e.g. =?utf-8?B?...?= or =?utf-8?Q?...?=)
	dec := new(mime.WordDecoder)
	decoded, err := dec.DecodeHeader(originalSubject)
	if err != nil {
		decoded = originalSubject // fallback to raw
	}

	switch action {
	case ActionFlag:
		return prefix + decoded
	default: // ActionPass
		return prefix + decoded
	}
}

// minTLSVersion maps an integer (12 or 13) to the corresponding TLS version constant.
func minTLSVersion(v int) uint16 {
	switch v {
	case 13:
		return tls.VersionTLS13
	default:
		return tls.VersionTLS12
	}
}

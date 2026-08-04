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
// Robust body-preservation strategy: read the raw file, modify only the
// Subject line in the original headers, prepend forwarding headers, and
// recursively repair only CID-referenced generic inline part headers. MIME
// boundaries, transfer encodings, and encoded payload bytes remain intact.
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
	originalBody = normalizeForwardedInlineImagePartsWithContentType(originalBody, rawHeaderValue(originalHeaders, "Content-Type"))

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
	// Compatibility wrapper for tests/callers that only have the multipart body.
	// The first delimiter gives us a safe fallback boundary; production callers
	// pass the authoritative top-level Content-Type via the typed helper below.
	lineEnd := bytes.IndexByte(body, '\n')
	if lineEnd < 0 {
		return body
	}
	first := strings.TrimSpace(strings.TrimSuffix(string(body[:lineEnd]), "\r"))
	if !strings.HasPrefix(first, "--") {
		return body
	}
	boundary := strings.TrimSuffix(strings.TrimPrefix(first, "--"), "--")
	if boundary == "" {
		return body
	}
	return normalizeForwardedInlineImagePartsWithContentType(body, `multipart/mixed; boundary="`+quoteHeaderParam(boundary)+`"`)
}

func rawHeaderValue(raw []byte, key string) string {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	prefix := strings.ToLower(strings.TrimSpace(key)) + ":"
	var value string
	currentKey := ""
	for _, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if currentKey == prefix {
				value += " " + strings.TrimSpace(line)
			}
			continue
		}
		currentKey = ""
		if i := strings.IndexByte(line, ':'); i >= 0 {
			currentKey = strings.ToLower(strings.TrimSpace(line[:i])) + ":"
			if currentKey == prefix {
				value = strings.TrimSpace(line[i+1:])
			}
		}
	}
	return value
}

func normalizeForwardedInlineImagePartsWithContentType(body []byte, contentType string) []byte {
	boundary := multipartBoundary(contentType)
	if boundary == "" {
		return body
	}
	refs := map[string]struct{}{}
	collectRawCIDReferences(body, boundary, refs)
	if len(refs) == 0 {
		return body
	}
	rewritten, changed := rewriteRawMultipart(body, boundary, refs)
	if !changed {
		return body
	}
	return rewritten
}

func multipartBoundary(contentType string) string {
	_, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return ""
	}
	return params["boundary"]
}

func splitRawHeaderBody(raw []byte) (headers, separator, body []byte, ok bool) {
	if idx := bytes.Index(raw, []byte("\r\n\r\n")); idx >= 0 {
		return raw[:idx], raw[idx : idx+4], raw[idx+4:], true
	}
	if idx := bytes.Index(raw, []byte("\n\n")); idx >= 0 {
		return raw[:idx], raw[idx : idx+2], raw[idx+2:], true
	}
	return nil, nil, raw, false
}

func rawHeaderValueFromBlock(headers []byte, key string) string {
	return rawHeaderValue(headers, key)
}

func collectRawCIDReferences(body []byte, boundary string, refs map[string]struct{}) {
	forEachRawMultipartPart(body, boundary, func(part []byte) {
		headers, _, partBody, ok := splitRawHeaderBody(part)
		if !ok {
			return
		}
		contentType := rawHeaderValueFromBlock(headers, "Content-Type")
		if nested := multipartBoundary(contentType); nested != "" {
			collectRawCIDReferences(partBody, nested, refs)
			return
		}
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/html") {
			return
		}
		decoded, ok := decodeMIMEPartBody(partBody, rawHeaderValueFromBlock(headers, "Content-Transfer-Encoding"))
		if !ok {
			return
		}
		for _, match := range htmlCIDReferenceRE.FindAllStringSubmatch(string(decoded), -1) {
			if len(match) > 1 {
				if cid := normalizeForwardCID(match[1]); cid != "" {
					refs[cid] = struct{}{}
				}
			}
		}
	})
}

func rewriteRawMultipart(body []byte, boundary string, refs map[string]struct{}) ([]byte, bool) {
	var out bytes.Buffer
	changed := false
	last := 0
	var childStart int
	found := false
	forEachRawMultipartDelimiter(body, boundary, func(start, end int, closing bool) bool {
		if !found {
			out.Write(body[:start]) // preserve preamble exactly
			found = true
		} else if childStart <= start {
			part := body[childStart:start]
			headers, _, partBody, ok := splitRawHeaderBody(part)
			if ok {
				contentType := rawHeaderValueFromBlock(headers, "Content-Type")
				if nested := multipartBoundary(contentType); nested != "" {
					if nestedBody, nestedChanged := rewriteRawMultipart(partBody, nested, refs); nestedChanged {
						part = rebuildRawPart(headers, part, nestedBody)
						changed = true
					}
				} else {
					cid := normalizeForwardCID(rawHeaderValueFromBlock(headers, "Content-ID"))
					declared := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
					genericDeclared := declared == "" || declared == "application/octet-stream" || declared == "binary/octet-stream" || declared == "application/x-download"
					if cid != "" && genericDeclared {
						if _, referenced := refs[cid]; referenced {
							if decoded, decodeOK := decodeMIMEPartBody(partBody, rawHeaderValueFromBlock(headers, "Content-Transfer-Encoding")); decodeOK {
								if detected, ext := detectForwardImage(decoded); detected != "" {
									filename := partFilenameFromRawHeaders(headers)
									if filename == "" {
										filename = displayForwardCID(rawHeaderValueFromBlock(headers, "Content-ID"))
									}
									if filename == "" {
										filename = cid
									}
									if shouldAppendForwardExtension(filename, ext) {
										filename += ext
									}
									part = rewriteRawPartHeaders(headers, part, fmt.Sprintf(`%s; name="%s"`, detected, quoteHeaderParam(filename)), fmt.Sprintf(`inline; filename="%s"`, quoteHeaderParam(filename)))
									changed = true
								}
							}
						}
					}
				}
			}
			out.Write(part)
		}
		out.Write(body[start:end])
		last = end
		if closing {
			childStart = end
			return false
		}
		childStart = end
		return true
	})
	if !found {
		return body, false
	}
	if last < len(body) {
		out.Write(body[last:])
	}
	return out.Bytes(), changed
}

func rebuildRawPart(headers, original, newBody []byte) []byte {
	_, sep, _, ok := splitRawHeaderBody(original)
	if !ok {
		return original
	}
	return append(append(append([]byte{}, headers...), sep...), newBody...)
}

func rewriteRawPartHeaders(headers, original []byte, contentType, disposition string) []byte {
	_, sep, body, ok := splitRawHeaderBody(original)
	if !ok {
		return original
	}
	lineSep := "\r\n"
	if bytes.Equal(sep, []byte("\n\n")) {
		lineSep = "\n"
	}
	lines := strings.Split(strings.ReplaceAll(string(headers), "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines)+2)
	skipFolded := false
	for _, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if !skipFolded {
				kept = append(kept, line)
			}
			continue
		}
		skipFolded = false
		name := ""
		if i := strings.IndexByte(line, ':'); i >= 0 {
			name = strings.ToLower(strings.TrimSpace(line[:i]))
		}
		if name == "content-type" || name == "content-disposition" {
			skipFolded = true
			continue
		}
		kept = append(kept, line)
	}
	kept = append(kept, "Content-Type: "+contentType, "Content-Disposition: "+disposition)
	newHeaders := []byte(strings.Join(kept, lineSep))
	return append(append(newHeaders, sep...), body...)
}

func partFilenameFromRawHeaders(headers []byte) string {
	for _, key := range []string{"Content-Disposition", "Content-Type"} {
		_, params, err := mime.ParseMediaType(rawHeaderValueFromBlock(headers, key))
		if err == nil {
			if name := strings.TrimSpace(params["filename"]); name != "" {
				return name
			}
			if name := strings.TrimSpace(params["name"]); name != "" {
				return name
			}
		}
	}
	return ""
}

func forEachRawMultipartPart(body []byte, boundary string, fn func([]byte)) {
	var starts []struct {
		start, end int
		closing    bool
	}
	forEachRawMultipartDelimiter(body, boundary, func(start, end int, closing bool) bool {
		starts = append(starts, struct {
			start, end int
			closing    bool
		}{start, end, closing})
		return !closing
	})
	for i := 0; i+1 < len(starts); i++ {
		if starts[i].closing {
			break
		}
		fn(body[starts[i].end:starts[i+1].start])
	}
}

func forEachRawMultipartDelimiter(body []byte, boundary string, fn func(start, end int, closing bool) bool) {
	prefix := []byte("--" + boundary)
	for pos := 0; pos < len(body); {
		lineEnd := bytes.IndexByte(body[pos:], '\n')
		if lineEnd < 0 {
			lineEnd = len(body) - pos
		} else {
			lineEnd++
		}
		end := pos + lineEnd
		line := strings.TrimSpace(strings.TrimSuffix(string(body[pos:end]), "\n"))
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, string(prefix)) {
			rest := strings.TrimSpace(strings.TrimPrefix(line, string(prefix)))
			if rest == "" || rest == "--" {
				if !fn(pos, end, rest == "--") {
					return
				}
			}
		}
		pos = end
	}
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

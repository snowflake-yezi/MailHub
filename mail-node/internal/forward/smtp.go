package forward

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"net"
	"net/smtp"
	"os"
	"regexp"
	"strings"
	"time"
)

const maxEmailSizeDefault = 10 * 1024 * 1024 // 10MB hard cap

// readForFiltering opens the file, reads headers + up to 64KB of body text
// for filter decision. The caller uses the returned headers and body preview
// to decide whether to forward.
func readForFiltering(filePath string, maxSize int64) (headers map[string]string, bodyPreview string, err error) {
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

	// Read up to 64KB of body text for keyword filtering.
	// br still wraps the file via LimitReader — any bytes already buffered
	// past the header boundary are included.
	bodyLimit := io.LimitReader(br, 64*1024)
	bodyBytes, _ := io.ReadAll(bodyLimit)
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
func streamToSMTP(cfg ForwardConfig, filePath, newSubject, sourceAddr string) error {
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

	// 4. SMTP connection (STARTTLS on port 587)
	host, _, err := net.SplitHostPort(cfg.SMTPHost)
	if err != nil {
		return fmt.Errorf("invalid smtp host %q: %w", cfg.SMTPHost, err)
	}

	conn, err := net.DialTimeout("tcp", cfg.SMTPHost, 15*time.Second)
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
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
	}
	if err := client.StartTLS(tlsConfig); err != nil {
		return fmt.Errorf("starttls: %w", err)
	}
	defer func() {
		if qErr := client.Quit(); qErr != nil {
			_ = qErr
		}
	}()

	auth := smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}

	if err := client.Mail(cfg.SMTPUser); err != nil {
		return fmt.Errorf("mail from %s: %w", cfg.SMTPUser, err)
	}
	if err := client.Rcpt(cfg.TargetAddress); err != nil {
		return fmt.Errorf("rcpt to %s: %w", cfg.TargetAddress, err)
	}

	// 5. DATA phase: forwarding headers + modified original headers + body
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}

	now := time.Now().Format(time.RFC1123Z)
	fmt.Fprintf(w, "X-Forwarded-By: mail-node\r\n")
	fmt.Fprintf(w, "X-Original-To: %s\r\n", sourceAddr)
	fmt.Fprintf(w, "Resent-From: %s\r\n", cfg.TargetAddress)
	fmt.Fprintf(w, "Resent-To: %s\r\n", cfg.TargetAddress)
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
		return "[疑似]" + prefix + decoded
	default: // ActionPass
		return prefix + decoded
	}
}

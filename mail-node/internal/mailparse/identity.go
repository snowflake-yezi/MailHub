package mailparse

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/mail"
	"strings"
)

const MaxMessageHeaderBytes = 1024 * 1024

func ScanMessageID(reader io.Reader) (string, error) {
	limited := &io.LimitedReader{R: reader, N: MaxMessageHeaderBytes + 1}
	buffered := bufio.NewReader(limited)
	var header bytes.Buffer
	for {
		line, err := buffered.ReadBytes('\n')
		if header.Len()+len(line) > MaxMessageHeaderBytes {
			return "", fmt.Errorf("message header exceeds %d bytes", MaxMessageHeaderBytes)
		}
		_, _ = header.Write(line)
		if bytes.Equal(line, []byte("\n")) || bytes.Equal(line, []byte("\r\n")) {
			break
		}
		if err != nil {
			if err == io.EOF {
				return "", fmt.Errorf("message header is not terminated")
			}
			return "", fmt.Errorf("read message header: %w", err)
		}
	}

	message, err := mail.ReadMessage(bytes.NewReader(header.Bytes()))
	if err != nil {
		return "", fmt.Errorf("parse message header: %w", err)
	}
	return strings.TrimSpace(message.Header.Get("Message-ID")), nil
}

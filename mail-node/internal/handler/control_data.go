package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/ticket/email-mail-node/internal/filterquarantine"
	"github.com/ticket/email-mail-node/internal/nodedata"
	nodecontract "github.com/ticket/email-node-contract"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
)

func (h *NodeHandler) OpenControlData(ctx context.Context, request *nodev1.SystemDataRequest) (*nodedata.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request == nil || request.Locator == nil {
		return controlDataError(http.StatusBadRequest, 1001, "data locator is required")
	}
	locator := request.Locator
	switch nodecontract.DataRequestType(request.Type) {
	case nodecontract.DataRequestMessageList:
		return h.openControlMessageList(ctx, locator)
	case nodecontract.DataRequestMessageBody:
		return h.openControlMessageBody(ctx, locator)
	case nodecontract.DataRequestMessageRaw:
		return h.openControlMessageRaw(ctx, locator)
	case nodecontract.DataRequestMessageAttachment:
		return h.openControlMessageAttachment(ctx, locator, false)
	case nodecontract.DataRequestMessageAttachmentPreview:
		return h.openControlMessageAttachment(ctx, locator, true)
	case nodecontract.DataRequestQuarantineMessage:
		return h.openControlQuarantineMessage(ctx, locator)
	case nodecontract.DataRequestQuarantineAttachment:
		return h.openControlQuarantineAttachment(ctx, locator)
	default:
		return controlDataError(http.StatusNotImplemented, 1004, "unsupported data request type")
	}
}

func (h *NodeHandler) openControlMessageList(ctx context.Context, locator *nodev1.DataLocator) (*nodedata.Response, error) {
	email := locator.Mailbox
	if len(strings.SplitN(email, "@", 2)) != 2 {
		return controlDataError(http.StatusBadRequest, 1002, "invalid email")
	}
	options := struct {
		Page int `json:"page"`
		Size int `json:"size"`
	}{Page: 1, Size: 20}
	if len(locator.OptionsJson) > 0 && json.Unmarshal(locator.OptionsJson, &options) != nil {
		return controlDataError(http.StatusBadRequest, 1002, "invalid list options")
	}
	if options.Page < 1 {
		options.Page = 1
	}
	if options.Size < 1 || options.Size > 100 {
		options.Size = 20
	}
	allFiles := sortMailFilesByModTimeDesc(h.scanMailboxFiles(email))
	pageFiles := splitPage(allFiles, options.Page, options.Size)
	messages := make([]*parsedMessage, 0, len(pageFiles))
	for _, filePath := range pageFiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if message, err := parseMaildirMessage(filePath, email, h.mailboxMgr.MaildirBase()); err == nil {
			messages = append(messages, message)
			h.messagePaths().putFile(email, message.MessageID, filePath)
		}
	}
	return controlDataJSON(http.StatusOK, map[string]any{
		"code": 0,
		"data": map[string]any{
			"email_address": email, "page": options.Page, "size": options.Size,
			"total": len(allFiles), "messages": messages,
		},
	})
}

func (h *NodeHandler) openControlMessageBody(ctx context.Context, locator *nodev1.DataLocator) (*nodedata.Response, error) {
	if len(strings.SplitN(locator.Mailbox, "@", 2)) != 2 {
		return controlDataError(http.StatusBadRequest, 1002, "invalid mailbox param")
	}
	message, _, ok := h.findMessage(locator.Mailbox, locator.MessageId)
	if !ok {
		return controlDataError(http.StatusNotFound, 2003, "message not found")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return controlDataJSON(http.StatusOK, map[string]any{"code": 0, "data": message})
}

func (h *NodeHandler) openControlMessageRaw(ctx context.Context, locator *nodev1.DataLocator) (*nodedata.Response, error) {
	if len(strings.SplitN(locator.Mailbox, "@", 2)) != 2 {
		return controlDataError(http.StatusBadRequest, 1002, "invalid mailbox param")
	}
	filePath, ok := h.findMessagePath(locator.Mailbox, locator.MessageId)
	if !ok {
		return controlDataError(http.StatusNotFound, 2003, "message not found")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return controlDataError(http.StatusInternalServerError, 5000, "failed to open message file")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return controlDataError(http.StatusInternalServerError, 5000, "failed to stat message file")
	}
	header := http.Header{
		"Content-Type":           []string{"message/rfc822"},
		"Content-Disposition":    []string{contentDisposition("attachment", "message.eml")},
		"Cache-Control":          []string{"private, no-store"},
		"X-Content-Type-Options": []string{"nosniff"},
	}
	return &nodedata.Response{StatusCode: http.StatusOK, Header: header, ContentLength: info.Size(), Body: file}, nil
}

func (h *NodeHandler) openControlMessageAttachment(ctx context.Context, locator *nodev1.DataLocator, preview bool) (*nodedata.Response, error) {
	if len(strings.SplitN(locator.Mailbox, "@", 2)) != 2 {
		return controlDataError(http.StatusBadRequest, 1002, "invalid mailbox param")
	}
	filePath, ok := h.findMessagePath(locator.Mailbox, locator.MessageId)
	if !ok {
		return controlDataError(http.StatusNotFound, 2003, "message not found")
	}
	part, info, inline, err := attachmentPartFromPath(filePath, int(locator.AttachmentIndex))
	if err != nil {
		return controlDataError(http.StatusNotFound, 2003, "attachment index out of range")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	contentType := info.ContentType
	disposition := "attachment"
	header := make(http.Header)
	if inline {
		disposition = "inline"
	}
	if preview {
		if len(part.Content) > maxAttachmentPreviewBytes {
			return controlDataError(http.StatusRequestEntityTooLarge, 1003, "attachment too large for preview")
		}
		var allowed bool
		contentType, allowed = previewAttachmentContentType(info.ContentType)
		if !allowed {
			return controlDataError(http.StatusUnsupportedMediaType, 1004, "attachment type is not previewable")
		}
		disposition = "inline"
		header.Set("X-Content-Type-Options", "nosniff")
	}
	header.Set("Content-Type", contentType)
	header.Set("Content-Disposition", contentDisposition(disposition, info.Filename))
	return bytesDataResponse(http.StatusOK, header, part.Content), nil
}

func (h *NodeHandler) openControlQuarantineMessage(ctx context.Context, locator *nodev1.DataLocator) (*nodedata.Response, error) {
	if h.quarantine == nil {
		return controlDataError(http.StatusServiceUnavailable, 5000, "quarantine store unavailable")
	}
	metadata, path, err := h.quarantine.MessagePath(locator.QuarantineKey)
	if err != nil {
		return controlQuarantineError(err)
	}
	message, err := parseFullMessage(path, metadata.Mailbox, h.mailboxMgr.MaildirBase())
	if err != nil {
		return controlDataError(http.StatusInternalServerError, 5000, "failed to parse quarantined message")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return controlDataJSON(http.StatusOK, map[string]any{
		"code": 0, "data": map[string]any{"metadata": metadata, "message": message},
	})
}

func (h *NodeHandler) openControlQuarantineAttachment(ctx context.Context, locator *nodev1.DataLocator) (*nodedata.Response, error) {
	if h.quarantine == nil {
		return controlDataError(http.StatusServiceUnavailable, 5000, "quarantine store unavailable")
	}
	_, path, err := h.quarantine.MessagePath(locator.QuarantineKey)
	if err != nil {
		return controlQuarantineError(err)
	}
	part, info, inline, err := attachmentPartFromPath(path, int(locator.AttachmentIndex))
	if err != nil {
		return controlDataError(http.StatusNotFound, 2003, "attachment index out of range")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	header := http.Header{
		"Content-Type":           []string{info.ContentType},
		"Content-Disposition":    []string{contentDisposition(disposition, info.Filename)},
		"X-Content-Type-Options": []string{"nosniff"},
	}
	return bytesDataResponse(http.StatusOK, header, part.Content), nil
}

func controlQuarantineError(err error) (*nodedata.Response, error) {
	switch {
	case errors.Is(err, filterquarantine.ErrInvalidKey), errors.Is(err, filterquarantine.ErrInvalidPath):
		return controlDataError(http.StatusBadRequest, 1002, err.Error())
	case errors.Is(err, filterquarantine.ErrNotFound):
		return controlDataError(http.StatusNotFound, 2003, "quarantine not found")
	default:
		return controlDataError(http.StatusInternalServerError, 5000, err.Error())
	}
}

func controlDataError(status, code int, message string) (*nodedata.Response, error) {
	return controlDataJSON(status, map[string]any{"code": code, "message": message})
}

func controlDataJSON(status int, value any) (*nodedata.Response, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return bytesDataResponse(status, http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}, data), nil
}

func bytesDataResponse(status int, header http.Header, data []byte) *nodedata.Response {
	return &nodedata.Response{
		StatusCode: status, Header: header, ContentLength: int64(len(data)),
		Body: io.NopCloser(bytes.NewReader(data)),
	}
}

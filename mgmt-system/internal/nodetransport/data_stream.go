package nodetransport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/ticket/email-mgmt-system/internal/nodedata"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
)

const defaultDataRequestTimeout = 5 * time.Minute

type DataStreamTransport struct {
	sessions *nodedata.Registry
}

func NewDataStreamTransport(sessions *nodedata.Registry) *DataStreamTransport {
	return &DataStreamTransport{sessions: sessions}
}

func (transport *DataStreamTransport) Execute(context.Context, Target, Command) (*Response, error) {
	return nil, ErrControlOperationUnsupported
}

func (transport *DataStreamTransport) Notify(context.Context, Target, Notification) (*Response, error) {
	return nil, ErrControlOperationUnsupported
}

func (transport *DataStreamTransport) Query(ctx context.Context, target Target, request DataRequest) (*Response, error) {
	response, err := transport.OpenData(ctx, target, request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read node data response: %w", err)
	}
	return &Response{StatusCode: response.StatusCode, Header: response.Header.Clone(), Body: body}, nil
}

func (transport *DataStreamTransport) OpenData(ctx context.Context, target Target, request DataRequest) (*DataResponse, error) {
	if transport.sessions == nil {
		return nil, nodedata.ErrSessionNotFound
	}
	locator, err := dataLocator(request)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().UTC().Add(defaultDataRequestTimeout)
	if request.legacy.Timeout > 0 {
		deadline = time.Now().UTC().Add(request.legacy.Timeout)
	}
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline.UTC()
	}
	requestContext, cancel := context.WithDeadline(ctx, deadline)
	response, err := transport.sessions.Open(requestContext, target.NodeID, nodedata.OpenInput{
		Type: string(request.Type), Locator: locator, DeadlineAt: deadline,
	})
	if err != nil {
		cancel()
		return nil, err
	}
	return &DataResponse{
		StatusCode: response.StatusCode, Header: response.Header,
		Body: &deadlineReadCloser{ReadCloser: response.Body, cancel: cancel},
	}, nil
}

func (transport *DataStreamTransport) Probe(context.Context, Target) (*Response, error) {
	return nil, ErrControlOperationUnsupported
}

func dataLocator(request DataRequest) (*nodev1.DataLocator, error) {
	locator := &nodev1.DataLocator{
		Mailbox: request.Metadata["mailbox"], MessageId: request.Metadata["message_id"],
		QuarantineKey: request.Metadata["quarantine_key"],
	}
	if rawIndex := request.Metadata["attachment_index"]; rawIndex != "" {
		index, err := strconv.ParseUint(rawIndex, 10, 32)
		if err != nil || index > math.MaxUint32 {
			return nil, fmt.Errorf("invalid attachment index %q", rawIndex)
		}
		locator.AttachmentIndex = uint32(index)
	}
	if request.Metadata["page"] != "" || request.Metadata["size"] != "" {
		page, _ := strconv.Atoi(request.Metadata["page"])
		size, _ := strconv.Atoi(request.Metadata["size"])
		if page < 1 {
			page = 1
		}
		if size < 1 || size > 100 {
			size = 20
		}
		locator.OptionsJson, _ = json.Marshal(map[string]int{"page": page, "size": size})
	}
	return locator, nil
}

var _ NodeTransport = (*DataStreamTransport)(nil)

type deadlineReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (body *deadlineReadCloser) Close() error {
	var err error
	body.once.Do(func() {
		err = body.ReadCloser.Close()
		body.cancel()
	})
	return err
}

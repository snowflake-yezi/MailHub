package nodetransport

import (
	"context"
	"io"
	"net/http"
	"time"

	nodecontract "github.com/ticket/email-node-contract"
)

// Target identifies a node independently from its temporary legacy address.
type Target struct {
	NodeID        uint64
	APIHost       string
	TransportMode string
}

type legacyRequest struct {
	Method           string
	Path             string
	Body             []byte
	JSON             bool
	Timeout          time.Duration
	RawHeaderTimeout bool
	MaxResponseBytes int64
}

type Command struct {
	Type           nodecontract.CommandType
	SchemaVersion  uint32
	IdempotencyKey string
	PayloadJSON    []byte
	legacy         legacyRequest
}

type Notification struct {
	Type        nodecontract.NotificationType
	PayloadJSON []byte
	legacy      legacyRequest
}

type DataRequest struct {
	Type     nodecontract.DataRequestType
	Metadata map[string]string
	legacy   legacyRequest
}

type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type DataResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

type NodeTransport interface {
	Execute(context.Context, Target, Command) (*Response, error)
	Notify(context.Context, Target, Notification) (*Response, error)
	Query(context.Context, Target, DataRequest) (*Response, error)
	OpenData(context.Context, Target, DataRequest) (*DataResponse, error)
	Probe(context.Context, Target) (*Response, error)
}

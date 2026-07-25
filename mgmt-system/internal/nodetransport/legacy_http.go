package nodetransport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type LegacyHTTPOptions struct {
	Client    *http.Client
	RawClient *http.Client
}

type LegacyHTTPTransport struct {
	sharedSecret string
	client       *http.Client
	rawClient    *http.Client
}

func NewLegacyHTTPTransport(sharedSecret string) *LegacyHTTPTransport {
	return NewLegacyHTTPTransportWithOptions(sharedSecret, LegacyHTTPOptions{})
}

func NewLegacyHTTPTransportWithOptions(sharedSecret string, options LegacyHTTPOptions) *LegacyHTTPTransport {
	client := options.Client
	if client == nil {
		client = &http.Client{}
	}
	rawClient := options.RawClient
	if rawClient == nil {
		rawClient = &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}}
	}
	return &LegacyHTTPTransport{sharedSecret: sharedSecret, client: client, rawClient: rawClient}
}

func (t *LegacyHTTPTransport) Execute(ctx context.Context, target Target, command Command) (*Response, error) {
	return t.doBuffered(ctx, target, command.legacy)
}

func (t *LegacyHTTPTransport) Notify(ctx context.Context, target Target, notification Notification) (*Response, error) {
	resp, err := t.do(ctx, target, notification.legacy)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return &Response{StatusCode: resp.StatusCode, Header: resp.Header.Clone()}, nil
}

func (t *LegacyHTTPTransport) Query(ctx context.Context, target Target, request DataRequest) (*Response, error) {
	return t.doBuffered(ctx, target, request.legacy)
}

func (t *LegacyHTTPTransport) OpenData(ctx context.Context, target Target, request DataRequest) (*DataResponse, error) {
	resp, err := t.do(ctx, target, request.legacy)
	if err != nil {
		return nil, err
	}
	return &DataResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

func (t *LegacyHTTPTransport) Probe(ctx context.Context, target Target) (*Response, error) {
	return t.doBuffered(ctx, target, legacyRequest{
		Method: http.MethodGet, Path: "/internal/health", MaxResponseBytes: 1 << 20,
	})
}

func (t *LegacyHTTPTransport) doBuffered(ctx context.Context, target Target, request legacyRequest) (*Response, error) {
	resp, err := t.do(ctx, target, request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var reader io.Reader = resp.Body
	if request.MaxResponseBytes > 0 {
		reader = io.LimitReader(reader, request.MaxResponseBytes)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return &Response{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: body}, nil
}

func (t *LegacyHTTPTransport) do(ctx context.Context, target Target, request legacyRequest) (*http.Response, error) {
	if strings.TrimSpace(target.APIHost) == "" {
		return nil, fmt.Errorf("node %d has no legacy API host", target.NodeID)
	}
	endpoint := "http://" + strings.TrimRight(target.APIHost, "/") + request.Path
	req, err := http.NewRequestWithContext(ctx, request.Method, endpoint, bytes.NewReader(request.Body))
	if err != nil {
		return nil, fmt.Errorf("create legacy node request: %w", err)
	}
	if request.JSON {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Internal-Token", t.sharedSecret)

	client := t.client
	if request.RawHeaderTimeout {
		client = t.rawClient
	} else if request.Timeout > 0 {
		clientCopy := *t.client
		clientCopy.Timeout = request.Timeout
		client = &clientCopy
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("legacy node request failed: %w", err)
	}
	return resp, nil
}

var _ NodeTransport = (*LegacyHTTPTransport)(nil)

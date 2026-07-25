package enrollment

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	nodecontract "github.com/ticket/email-node-contract"
)

const maxEnrollmentResponseBytes = 1 << 20

type Request struct {
	ID                string  `json:"id"`
	RequestedNodeUUID string  `json:"requested_node_uuid"`
	RequestedName     string  `json:"requested_name"`
	State             string  `json:"state"`
	ReviewNote        string  `json:"review_note,omitempty"`
	ServerID          *uint64 `json:"server_id,omitempty"`
}

type ClaimInput struct {
	Token              string `json:"token"`
	NodeUUID           string `json:"node_uuid"`
	Name               string `json:"name"`
	Hostname           string `json:"hostname,omitempty"`
	OS                 string `json:"os,omitempty"`
	Arch               string `json:"arch,omitempty"`
	AgentVersion       string `json:"agent_version,omitempty"`
	MachineFingerprint string `json:"machine_fingerprint,omitempty"`
}

type ClaimResult struct {
	Request       Request `json:"request"`
	RequestSecret string  `json:"request_secret"`
}

type CompleteResult struct {
	Credential string `json:"credential"`
	Metadata   struct {
		CredentialPrefix string `json:"credential_prefix"`
		Version          uint64 `json:"version"`
	} `json:"metadata"`
}

type APIError struct {
	Status  int
	Code    int
	Message string
}

func (err *APIError) Error() string {
	return fmt.Sprintf("management enrollment API returned HTTP %d (code %d): %s", err.Status, err.Code, err.Message)
}

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(managementURL, caFile string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(managementURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("management URL must be an HTTPS origin without user information")
	}
	parsed.Path, parsed.RawQuery, parsed.Fragment = "", "", ""

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if strings.TrimSpace(caFile) != "" {
		pemData, err := os.ReadFile(strings.TrimSpace(caFile))
		if err != nil {
			return nil, fmt.Errorf("read management CA: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("management CA file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	return &Client{
		baseURL: strings.TrimRight(parsed.String(), "/"),
		http: &http.Client{
			Timeout:   15 * time.Second,
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return fmt.Errorf("enrollment redirects are not allowed")
			},
		},
	}, nil
}

func (client *Client) Claim(ctx context.Context, input ClaimInput) (*ClaimResult, error) {
	var result ClaimResult
	if err := client.request(ctx, http.MethodPost, nodecontract.NodeEnrollmentClaimRoute, "", input, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Request.ID) == "" || strings.TrimSpace(result.RequestSecret) == "" {
		return nil, fmt.Errorf("management returned an incomplete enrollment claim")
	}
	return &result, nil
}

func (client *Client) Status(ctx context.Context, requestID, requestSecret string) (*Request, error) {
	path := strings.Replace(nodecontract.NodeEnrollmentRequestRoute, ":id", url.PathEscape(strings.TrimSpace(requestID)), 1)
	var result Request
	if err := client.request(ctx, http.MethodGet, path, requestSecret, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (client *Client) Complete(ctx context.Context, requestID, requestSecret string) (*CompleteResult, error) {
	path := strings.Replace(nodecontract.NodeEnrollmentRequestCompleteRoute, ":id", url.PathEscape(strings.TrimSpace(requestID)), 1)
	var result CompleteResult
	if err := client.request(ctx, http.MethodPost, path, requestSecret, struct{}{}, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Credential) == "" {
		return nil, fmt.Errorf("management returned an empty node credential")
	}
	return &result, nil
}

func (client *Client) request(ctx context.Context, method, path, requestSecret string, body, result any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if requestSecret != "" {
		request.Header.Set("Authorization", "Request "+requestSecret)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("call management enrollment API: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxEnrollmentResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read management enrollment response: %w", err)
	}
	if len(payload) > maxEnrollmentResponseBytes {
		return fmt.Errorf("management enrollment response exceeds 1 MiB")
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("decode management enrollment response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || envelope.Code != 0 {
		return &APIError{Status: response.StatusCode, Code: envelope.Code, Message: envelope.Message}
	}
	if result != nil && len(envelope.Data) != 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, result); err != nil {
			return fmt.Errorf("decode management enrollment data: %w", err)
		}
	}
	return nil
}

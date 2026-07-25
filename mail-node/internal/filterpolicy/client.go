package filterpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	filtercontract "github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mail-node/internal/filterdecision"
)

type IdentityFunc func() (nodeID uint64, bootID string)

type Client struct {
	baseURL    string
	secret     string
	engine     *filterdecision.Engine
	identity   IdentityFunc
	nodeUUID   string
	credential string
	http       *http.Client
	syncMu     sync.Mutex
}

func (client *Client) ConfigureNodeCredential(nodeUUID, credential string) {
	client.nodeUUID = strings.TrimSpace(nodeUUID)
	client.credential = strings.TrimSpace(credential)
}

func (client *Client) authorize(request *http.Request) {
	if client.nodeUUID != "" && client.credential != "" {
		request.Header.Set("Authorization", "Node "+client.credential)
		request.Header.Set("X-MailHub-Node-UUID", client.nodeUUID)
		return
	}
	request.Header.Set("X-Internal-Token", client.secret)
}

type nodeState struct {
	NodeID          uint64     `json:"node_id"`
	PolicyKind      string     `json:"policy_kind"`
	AppliedRevision uint64     `json:"applied_revision"`
	Checksum        string     `json:"checksum"`
	BootID          string     `json:"boot_id"`
	LastError       string     `json:"last_error"`
	AppliedAt       *time.Time `json:"applied_at,omitempty"`
}

func NewClient(baseURL, secret string, engine *filterdecision.Engine, identity IdentityFunc) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"), secret: secret, engine: engine, identity: identity,
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

func (client *Client) Start(ctx context.Context, interval func() time.Duration, onError func(error)) {
	if err := client.SyncOnce(ctx); err != nil && onError != nil {
		onError(err)
	}
	for {
		delay := interval()
		if delay <= 0 {
			delay = time.Minute
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if err := client.SyncOnce(ctx); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}

func (client *Client) SyncOnce(ctx context.Context) error {
	client.syncMu.Lock()
	defer client.syncMu.Unlock()
	var results []error
	for _, policyKind := range []string{filtercontract.PolicyManual, filtercontract.PolicyAd} {
		if err := client.syncKind(ctx, policyKind); err != nil {
			results = append(results, err)
		}
	}
	return errors.Join(results...)
}

func (client *Client) syncKind(ctx context.Context, policyKind string) error {
	data, status, err := client.getBundle(ctx, policyKind)
	if err != nil {
		_ = client.report(ctx, policyKind, err.Error())
		return fmt.Errorf("sync %s bundle: %w", policyKind, err)
	}
	if status == http.StatusNotFound {
		if err := client.report(ctx, policyKind, ""); err != nil {
			return fmt.Errorf("report empty %s state: %w", policyKind, err)
		}
		return nil
	}
	current := client.engine.State(policyKind)
	if policyKind == filtercontract.PolicyManual {
		var bundle filtercontract.ManualBundle
		if err := filtercontract.DecodeStrict(data, &bundle); err != nil {
			_ = client.report(ctx, policyKind, err.Error())
			return fmt.Errorf("decode manual bundle: %w", err)
		}
		if current.Revision != bundle.Revision || current.Checksum != bundle.Checksum {
			if err := client.engine.ApplyManual(bundle); err != nil {
				_ = client.report(ctx, policyKind, err.Error())
				return fmt.Errorf("apply manual bundle: %w", err)
			}
		}
	} else {
		var bundle filtercontract.AdBundle
		if err := filtercontract.DecodeStrict(data, &bundle); err != nil {
			_ = client.report(ctx, policyKind, err.Error())
			return fmt.Errorf("decode ad bundle: %w", err)
		}
		if current.Revision != bundle.Revision || current.Checksum != bundle.Checksum {
			if err := client.engine.ApplyAd(bundle); err != nil {
				_ = client.report(ctx, policyKind, err.Error())
				return fmt.Errorf("apply ad bundle: %w", err)
			}
		}
	}
	if err := client.report(ctx, policyKind, ""); err != nil {
		return fmt.Errorf("report %s state: %w", policyKind, err)
	}
	return nil
}

func (client *Client) getBundle(ctx context.Context, policyKind string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/v1/internal/filter-bundles/"+policyKind, nil)
	if err != nil {
		return nil, 0, err
	}
	client.authorize(req)
	resp, err := client.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, resp.StatusCode, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, fmt.Errorf("manager returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 5<<20))
	if err := decoder.Decode(&envelope); err != nil {
		return nil, resp.StatusCode, err
	}
	if envelope.Code != 0 || len(envelope.Data) == 0 {
		return nil, resp.StatusCode, fmt.Errorf("invalid manager envelope code %d", envelope.Code)
	}
	return envelope.Data, resp.StatusCode, nil
}

func (client *Client) report(ctx context.Context, policyKind, lastError string) error {
	if client.identity == nil {
		return nil
	}
	nodeID, bootID := client.identity()
	if nodeID == 0 {
		return nil
	}
	current := client.engine.State(policyKind)
	state := nodeState{
		NodeID: nodeID, PolicyKind: policyKind, AppliedRevision: current.Revision,
		Checksum: current.Checksum, BootID: bootID, LastError: lastError,
	}
	if current.Revision > 0 {
		now := time.Now().UTC()
		state.AppliedAt = &now
	}
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/api/v1/internal/filter-node-states", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client.authorize(req)
	resp, err := client.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("manager returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

package nodetransport

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodedata"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
)

func TestDataStreamTransportMapsLocatorAndResponse(t *testing.T) {
	registry := nodedata.NewRegistry(nodedata.Config{MaxConcurrency: 2, MaxChunkSize: 8})
	session := registry.Register(context.Background(), nodedata.RegisterInput{ServerID: 7})
	transport := NewDataStreamTransport(registry)
	result := make(chan struct {
		response *Response
		err      error
	}, 1)
	go func() {
		response, err := transport.Query(context.Background(), Target{NodeID: 7}, MessageList("a@example.com", "2", "25"))
		result <- struct {
			response *Response
			err      error
		}{response, err}
	}()

	request := (<-session.Outgoing()).GetRequest()
	if request == nil || request.Type != "message.list.v1" || request.Locator.Mailbox != "a@example.com" ||
		!strings.Contains(string(request.Locator.OptionsJson), `"page":2`) || request.DeadlineAt == nil {
		t.Fatalf("data request = %#v", request)
	}
	if err := session.Handle(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Header{Header: &nodev1.NodeDataHeader{
		RequestId: request.RequestId, Status: 200, ContentType: "application/json", ContentLength: 2,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := session.Handle(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Chunk{Chunk: &nodev1.NodeDataChunk{
		RequestId: request.RequestId, Sequence: 1, Data: []byte("{}"),
	}}}); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("{}"))
	if err := session.Handle(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_End{End: &nodev1.NodeDataEnd{
		RequestId: request.RequestId, ChecksumAlgorithm: "sha256", Checksum: digest[:], TotalBytes: 2,
	}}}); err != nil {
		t.Fatal(err)
	}
	completed := <-result
	if completed.err != nil || completed.response.StatusCode != 200 || string(completed.response.Body) != "{}" {
		t.Fatalf("response = %#v, err=%v", completed.response, completed.err)
	}
}

func TestMigrationTransportDataRoutingAndDualFallback(t *testing.T) {
	legacy := &routingTransport{queryResponse: &Response{StatusCode: 200, Body: []byte("legacy")}, dataResponse: &DataResponse{
		StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("legacy-data")),
	}}
	control := &routingTransport{queryResponse: &Response{StatusCode: 202, Body: []byte("command-status")}}
	data := &routingTransport{queryErr: errors.New("data offline"), dataErr: errors.New("data offline")}
	transport := NewMigrationTransport(legacy, control, data)

	response, err := transport.Query(context.Background(), Target{NodeID: 7, TransportMode: model.TransportDual}, MessageBody("m-1", "a@example.com"))
	if err != nil || string(response.Body) != "legacy" || data.queryCalls != 1 || legacy.queryCalls != 1 {
		t.Fatalf("dual query = %#v, err=%v, calls=%d/%d", response, err, data.queryCalls, legacy.queryCalls)
	}
	_, err = transport.Query(context.Background(), Target{NodeID: 7, TransportMode: model.TransportControlStream}, MessageBody("m-1", "a@example.com"))
	if err == nil || legacy.queryCalls != 1 {
		t.Fatalf("control query error=%v legacy calls=%d", err, legacy.queryCalls)
	}
	status, err := transport.Query(context.Background(), Target{NodeID: 7, TransportMode: model.TransportDual}, QuarantineReleaseStatus("q-1"))
	if err != nil || string(status.Body) != "command-status" || control.queryCalls != 1 {
		t.Fatalf("release status = %#v, err=%v", status, err)
	}
	opened, err := transport.OpenData(context.Background(), Target{NodeID: 7, TransportMode: model.TransportDual}, MessageRaw("m-1", "a@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(opened.Body)
	_ = opened.Body.Close()
	if string(body) != "legacy-data" || data.dataCalls != 1 || legacy.dataCalls != 1 {
		t.Fatalf("dual data body=%q calls=%d/%d", body, data.dataCalls, legacy.dataCalls)
	}
}

func TestMigrationTransportDualQueryReportsShadowDifference(t *testing.T) {
	legacy := &routingTransport{queryResponse: &Response{StatusCode: 200, Body: []byte("legacy")}}
	data := &routingTransport{queryResponse: &Response{StatusCode: 200, Body: []byte("data")}}
	transport := NewMigrationTransport(legacy, nil, data)
	comparisons := make(chan ShadowComparison, 1)
	transport.SetShadowObserver(func(comparison ShadowComparison) { comparisons <- comparison })
	if _, err := transport.Query(context.Background(), Target{NodeID: 9, TransportMode: model.TransportDual}, MessageBody("m-1", "a@example.com")); err != nil {
		t.Fatal(err)
	}
	select {
	case comparison := <-comparisons:
		if comparison.NodeID != 9 || !comparison.PrimaryOK || !comparison.LegacyOK || !comparison.StatusMatch || comparison.BodyHashMatch {
			t.Fatalf("comparison = %#v", comparison)
		}
	case <-time.After(time.Second):
		t.Fatal("shadow comparison was not reported")
	}
}

func TestMigrationTransportDualOpenDataReportsShadowDifference(t *testing.T) {
	legacy := &routingTransport{dataResponse: &DataResponse{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("legacy"))}}
	data := &routingTransport{dataResponse: &DataResponse{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data"))}}
	transport := NewMigrationTransport(legacy, nil, data)
	comparisons := make(chan ShadowComparison, 1)
	transport.SetShadowObserver(func(comparison ShadowComparison) { comparisons <- comparison })
	response, err := transport.OpenData(context.Background(), Target{NodeID: 9, TransportMode: model.TransportDual}, MessageRaw("m-1", "a@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	select {
	case comparison := <-comparisons:
		if comparison.RequestType != string(MessageRaw("m-1", "a@example.com").Type) || !comparison.PrimaryOK || !comparison.LegacyOK || !comparison.StatusMatch || comparison.BodyHashMatch {
			t.Fatalf("comparison = %#v", comparison)
		}
	case <-time.After(time.Second):
		t.Fatal("shadow comparison was not reported")
	}
}

func TestDataStreamTransportEnforcesLocalDeadline(t *testing.T) {
	registry := nodedata.NewRegistry(nodedata.Config{MaxConcurrency: 1, MaxChunkSize: 8})
	session := registry.Register(context.Background(), nodedata.RegisterInput{ServerID: 11})
	transport := NewDataStreamTransport(registry)
	request := MessageRaw("m-1", "a@example.com")
	request.legacy.Timeout = 20 * time.Millisecond
	result := make(chan struct {
		response *DataResponse
		err      error
	}, 1)
	go func() {
		response, err := transport.OpenData(context.Background(), Target{NodeID: 11}, request)
		result <- struct {
			response *DataResponse
			err      error
		}{response, err}
	}()
	requestID := (<-session.Outgoing()).GetRequest().RequestId
	if err := session.Handle(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Header{Header: &nodev1.NodeDataHeader{
		RequestId: requestID, Status: 200, ContentLength: -1,
	}}}); err != nil {
		t.Fatal(err)
	}
	opened := <-result
	if opened.err != nil {
		t.Fatal(opened.err)
	}
	if _, err := opened.response.Body.Read(make([]byte, 1)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline read error = %v", err)
	}
	cancel := <-session.Outgoing()
	if cancel.GetCancel() == nil || cancel.GetCancel().RequestId != requestID {
		t.Fatalf("cancel frame = %#v", cancel)
	}
}

type routingTransport struct {
	queryResponse *Response
	queryErr      error
	dataResponse  *DataResponse
	dataErr       error
	queryCalls    int
	dataCalls     int
}

func (transport *routingTransport) Execute(context.Context, Target, Command) (*Response, error) {
	return nil, errors.New("not implemented")
}

func (transport *routingTransport) Notify(context.Context, Target, Notification) (*Response, error) {
	return nil, errors.New("not implemented")
}

func (transport *routingTransport) Query(context.Context, Target, DataRequest) (*Response, error) {
	transport.queryCalls++
	return transport.queryResponse, transport.queryErr
}

func (transport *routingTransport) OpenData(context.Context, Target, DataRequest) (*DataResponse, error) {
	transport.dataCalls++
	return transport.dataResponse, transport.dataErr
}

func (transport *routingTransport) Probe(context.Context, Target) (*Response, error) {
	return nil, errors.New("not implemented")
}

var _ NodeTransport = (*routingTransport)(nil)

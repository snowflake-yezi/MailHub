package nodedata

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
	"time"

	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
)

type openResult struct {
	response *Response
	err      error
}

func TestOpenValidatesAndStreamsResponse(t *testing.T) {
	registry := NewRegistry(Config{MaxConcurrency: 2, MaxChunkSize: 4, MaxResponseBytes: 64})
	session := registry.Register(context.Background(), RegisterInput{ServerID: 7, NodeUUID: "node-7", ControlSessionID: "control-7"})
	result := make(chan openResult, 1)
	go func() {
		response, err := registry.Open(context.Background(), 7, OpenInput{Type: "message.raw.v1", Locator: &nodev1.DataLocator{Mailbox: "a@example.com"}})
		result <- openResult{response: response, err: err}
	}()

	requestFrame := <-session.Outgoing()
	requestID := requestFrame.GetRequest().RequestId
	if requestID == "" {
		t.Fatal("request ID was not assigned")
	}
	if err := session.Handle(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Header{Header: &nodev1.NodeDataHeader{
		RequestId: requestID, Status: 200, ContentType: "message/rfc822", ContentLength: 6,
	}}}); err != nil {
		t.Fatal(err)
	}
	opened := <-result
	if opened.err != nil || opened.response.StatusCode != 200 || opened.response.Header.Get("Content-Type") != "message/rfc822" {
		t.Fatalf("open response = %#v, err=%v", opened.response, opened.err)
	}
	for sequence, data := range [][]byte{[]byte("abc"), []byte("def")} {
		if err := session.Handle(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Chunk{Chunk: &nodev1.NodeDataChunk{
			RequestId: requestID, Sequence: uint64(sequence + 1), Data: data,
		}}}); err != nil {
			t.Fatal(err)
		}
	}
	digest := sha256.Sum256([]byte("abcdef"))
	if err := session.Handle(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_End{End: &nodev1.NodeDataEnd{
		RequestId: requestID, ChecksumAlgorithm: "sha256", Checksum: digest[:], TotalBytes: 6,
	}}}); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(opened.response.Body)
	if err != nil || string(body) != "abcdef" {
		t.Fatalf("body = %q, err=%v", body, err)
	}
	_ = opened.response.Body.Close()
}

func TestInvalidChunkCancelsOnlyTheRequest(t *testing.T) {
	registry := NewRegistry(Config{MaxConcurrency: 1, MaxChunkSize: 4})
	session := registry.Register(context.Background(), RegisterInput{ServerID: 8})
	result := make(chan openResult, 1)
	go func() {
		response, err := registry.Open(context.Background(), 8, OpenInput{Type: "message.raw.v1", Locator: &nodev1.DataLocator{}})
		result <- openResult{response: response, err: err}
	}()
	requestID := (<-session.Outgoing()).GetRequest().RequestId
	if err := session.Handle(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Header{Header: &nodev1.NodeDataHeader{
		RequestId: requestID, Status: 200, ContentLength: -1,
	}}}); err != nil {
		t.Fatal(err)
	}
	opened := <-result
	err := session.Handle(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Chunk{Chunk: &nodev1.NodeDataChunk{
		RequestId: requestID, Sequence: 2, Data: []byte("bad"),
	}}})
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("invalid sequence error = %v", err)
	}
	if _, readErr := io.ReadAll(opened.response.Body); !errors.Is(readErr, ErrProtocol) {
		t.Fatalf("body error = %v", readErr)
	}
	cancel := <-session.Outgoing()
	if cancel.GetCancel() == nil || cancel.GetCancel().RequestId != requestID {
		t.Fatalf("cancel frame = %#v", cancel)
	}
}

func TestContextCancellationSendsCancelFrame(t *testing.T) {
	registry := NewRegistry(Config{MaxConcurrency: 1, MaxChunkSize: 4})
	session := registry.Register(context.Background(), RegisterInput{ServerID: 9})
	ctx, cancelContext := context.WithCancel(context.Background())
	result := make(chan openResult, 1)
	go func() {
		response, err := registry.Open(ctx, 9, OpenInput{Type: "message.raw.v1", Locator: &nodev1.DataLocator{}})
		result <- openResult{response: response, err: err}
	}()
	requestID := (<-session.Outgoing()).GetRequest().RequestId
	if err := session.Handle(&nodev1.NodeDataFrame{Payload: &nodev1.NodeDataFrame_Header{Header: &nodev1.NodeDataHeader{
		RequestId: requestID, Status: 200, ContentLength: -1,
	}}}); err != nil {
		t.Fatal(err)
	}
	opened := <-result
	cancelContext()
	select {
	case frame := <-session.Outgoing():
		if frame.GetCancel() == nil || frame.GetCancel().RequestId != requestID {
			t.Fatalf("cancel frame = %#v", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel frame was not emitted")
	}
	if _, err := opened.response.Body.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancellation = %v", err)
	}
}

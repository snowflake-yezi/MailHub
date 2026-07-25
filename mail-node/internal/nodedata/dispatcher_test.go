package nodedata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDispatcherChunksAndChecksumsResponse(t *testing.T) {
	dispatcher, err := NewDispatcher(func(context.Context, *nodev1.SystemDataRequest) (*Response, error) {
		return &Response{
			StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/octet-stream"}},
			ContentLength: 6, Body: io.NopCloser(bytes.NewReader([]byte("abcdef"))),
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	frames := make(chan *nodev1.NodeDataFrame, 8)
	session, err := dispatcher.NewSession(context.Background(), 1, 3, func(_ context.Context, frame *nodev1.NodeDataFrame) error {
		frames <- frame
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.Accept(&nodev1.SystemDataRequest{
		RequestId: "request-1", Type: "message.raw.v1", Locator: &nodev1.DataLocator{},
		DeadlineAt: timestamppb.New(time.Now().Add(time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}

	header := <-frames
	first := <-frames
	second := <-frames
	end := <-frames
	if header.GetHeader() == nil || header.GetHeader().ContentLength != 6 {
		t.Fatalf("header = %#v", header)
	}
	if first.GetChunk().Sequence != 1 || string(first.GetChunk().Data) != "abc" ||
		second.GetChunk().Sequence != 2 || string(second.GetChunk().Data) != "def" {
		t.Fatalf("chunks = %#v, %#v", first, second)
	}
	digest := sha256.Sum256([]byte("abcdef"))
	if end.GetEnd() == nil || end.GetEnd().TotalBytes != 6 || !bytes.Equal(end.GetEnd().Checksum, digest[:]) {
		t.Fatalf("end = %#v", end)
	}
}

type blockingReadCloser struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (reader *blockingReadCloser) Read([]byte) (int, error) {
	reader.once.Do(func() { close(reader.started) })
	<-reader.closed
	return 0, errors.New("closed")
}

func (reader *blockingReadCloser) Close() error {
	select {
	case <-reader.closed:
	default:
		close(reader.closed)
	}
	return nil
}

func TestCancelClosesBlockedReader(t *testing.T) {
	reader := &blockingReadCloser{started: make(chan struct{}), closed: make(chan struct{})}
	dispatcher, _ := NewDispatcher(func(context.Context, *nodev1.SystemDataRequest) (*Response, error) {
		return &Response{StatusCode: 200, Header: make(http.Header), ContentLength: -1, Body: reader}, nil
	})
	frames := make(chan *nodev1.NodeDataFrame, 4)
	session, _ := dispatcher.NewSession(context.Background(), 1, 4, func(_ context.Context, frame *nodev1.NodeDataFrame) error {
		frames <- frame
		return nil
	})
	if err := session.Accept(&nodev1.SystemDataRequest{RequestId: "request-2", Type: "message.raw.v1", Locator: &nodev1.DataLocator{}}); err != nil {
		t.Fatal(err)
	}
	if (<-frames).GetHeader() == nil {
		t.Fatal("response header was not emitted")
	}
	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("reader did not start")
	}
	if !session.Cancel("request-2") {
		t.Fatal("active request was not canceled")
	}
	select {
	case <-reader.closed:
	case <-time.After(time.Second):
		t.Fatal("cancel did not close the response body")
	}
	session.Close()
}

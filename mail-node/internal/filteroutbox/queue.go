package filteroutbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	filtercontract "github.com/ticket/email-filter-contract"
)

const (
	DefaultMaxEvents = 10000
	DefaultMaxBytes  = int64(64 << 20)
)

var decisionKeyPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Queue struct {
	root      string
	stagedDir string
	readyDir  string
	maxEvents int
	maxBytes  int64
	mu        sync.Mutex
}

type Uploader struct {
	queue   *Queue
	url     string
	secret  string
	client  *http.Client
	backoff time.Duration
}

func New(root string, maxEvents int, maxBytes int64) (*Queue, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("filter outbox root is required")
	}
	if maxEvents <= 0 {
		maxEvents = DefaultMaxEvents
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	queue := &Queue{
		root: root, stagedDir: filepath.Join(root, "staged"), readyDir: filepath.Join(root, "ready"),
		maxEvents: maxEvents, maxBytes: maxBytes,
	}
	for _, directory := range []string{queue.root, queue.stagedDir, queue.readyDir} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return nil, fmt.Errorf("create outbox directory %s: %w", directory, err)
		}
		if err := os.Chmod(directory, 0700); err != nil {
			return nil, fmt.Errorf("secure outbox directory %s: %w", directory, err)
		}
	}
	return queue, nil
}

func (queue *Queue) Stage(event filtercontract.OutboxEvent) error {
	event.SchemaVersion = filtercontract.SchemaVersionV1
	event.Phase = "staged"
	event.Result = nil
	if err := event.Validate(); err != nil {
		return err
	}
	if !decisionKeyPattern.MatchString(event.Decision.DecisionKey) {
		return errors.New("decision key must be lowercase SHA-256")
	}
	data, err := event.CanonicalJSON()
	if err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	path := filepath.Join(queue.stagedDir, event.Decision.DecisionKey+".json")
	if identicalFile(path, data) {
		return nil
	}
	if err := queue.ensureCapacity(int64(len(data))); err != nil {
		return err
	}
	return writeAtomic(path, data)
}

func (queue *Queue) Ready(decisionKey string, result filtercontract.ProcessingResult) error {
	if !decisionKeyPattern.MatchString(decisionKey) {
		return errors.New("decision key must be lowercase SHA-256")
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	stagedPath := filepath.Join(queue.stagedDir, decisionKey+".json")
	readyPath := filepath.Join(queue.readyDir, decisionKey+".json")
	if _, err := os.Stat(stagedPath); errors.Is(err, os.ErrNotExist) {
		if _, readyErr := os.Stat(readyPath); readyErr == nil {
			return nil
		}
		return err
	}
	data, err := os.ReadFile(stagedPath)
	if err != nil {
		return err
	}
	var event filtercontract.OutboxEvent
	if err := filtercontract.DecodeStrict(data, &event); err != nil {
		return err
	}
	event.Phase = "ready"
	event.Result = &result
	if err := event.Validate(); err != nil {
		return err
	}
	readyData, err := event.CanonicalJSON()
	if err != nil {
		return err
	}
	_, _, used, err := queue.Pending()
	if err != nil {
		return err
	}
	projected := used - int64(len(data)) + int64(len(readyData))
	if projected > queue.maxBytes {
		return fmt.Errorf("filter outbox capacity exceeded after ready: bytes=%d", projected)
	}
	if err := writeAtomic(readyPath, readyData); err != nil {
		return err
	}
	if err := os.Remove(stagedPath); err != nil {
		return err
	}
	return syncDirectory(queue.stagedDir)
}

func (queue *Queue) RecoverStaged() (int, error) {
	entries, err := os.ReadDir(queue.stagedDir)
	if err != nil {
		return 0, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	recovered := 0
	var results []error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		key := strings.TrimSuffix(entry.Name(), ".json")
		err := queue.Ready(key, filtercontract.ProcessingResult{
			Status: "failed", AttemptedAction: filtercontract.ActionAllow, ActualAction: filtercontract.ActionAllow,
			ErrorCode: "recovered_incomplete", ErrorSummary: "processing did not complete before restart",
		})
		if err != nil {
			results = append(results, fmt.Errorf("recover %s: %w", key, err))
			continue
		}
		recovered++
	}
	return recovered, errors.Join(results...)
}

func (queue *Queue) Pending() (staged, ready int, bytes int64, err error) {
	for index, directory := range []string{queue.stagedDir, queue.readyDir} {
		entries, readErr := os.ReadDir(directory)
		if readErr != nil {
			return 0, 0, 0, readErr
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				return 0, 0, 0, infoErr
			}
			bytes += info.Size()
			if index == 0 {
				staged++
			} else {
				ready++
			}
		}
	}
	return staged, ready, bytes, nil
}

func NewUploader(queue *Queue, managerURL, secret string) *Uploader {
	return &Uploader{
		queue: queue, url: strings.TrimRight(managerURL, "/") + "/api/v1/internal/filter-decisions",
		secret: secret, client: &http.Client{Timeout: 10 * time.Second}, backoff: time.Second,
	}
}

func (uploader *Uploader) Start(ctx context.Context) {
	backoff := uploader.backoff
	for {
		_, err := uploader.UploadOnce(ctx)
		if err == nil {
			backoff = uploader.backoff
		} else if backoff < time.Minute {
			backoff *= 2
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (uploader *Uploader) UploadOnce(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(uploader.queue.readyDir)
	if err != nil {
		return 0, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	uploaded := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(uploader.queue.readyDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return uploaded, err
		}
		var event filtercontract.OutboxEvent
		if err := filtercontract.DecodeStrict(data, &event); err != nil || event.Phase != "ready" {
			if err == nil {
				err = errors.New("event is not ready")
			}
			return uploaded, fmt.Errorf("decode %s: %w", entry.Name(), err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploader.url, bytes.NewReader(data))
		if err != nil {
			return uploaded, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", uploader.secret)
		resp, err := uploader.client.Do(req)
		if err != nil {
			return uploaded, err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return uploaded, fmt.Errorf("manager returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		if err := os.Remove(path); err != nil {
			return uploaded, err
		}
		if err := syncDirectory(uploader.queue.readyDir); err != nil {
			return uploaded, err
		}
		uploaded++
	}
	return uploaded, nil
}

func (queue *Queue) ensureCapacity(additional int64) error {
	staged, ready, used, err := queue.Pending()
	if err != nil {
		return err
	}
	if staged+ready >= queue.maxEvents || used+additional > queue.maxBytes {
		return fmt.Errorf("filter outbox capacity exceeded: events=%d bytes=%d", staged+ready, used)
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	temporary := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.%d.tmp", filepath.Base(path), time.Now().UnixNano()))
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	cleanup := func() { _ = os.Remove(temporary) }
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		cleanup()
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func identicalFile(path string, data []byte) bool {
	existing, err := os.ReadFile(path)
	return err == nil && bytes.Equal(existing, data)
}

func DecodeEvent(data []byte) (filtercontract.OutboxEvent, error) {
	var event filtercontract.OutboxEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return event, err
	}
	return event, event.Validate()
}

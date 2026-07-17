package handler

import (
	"container/list"
	"io"
	netmail "net/mail"
	"os"
	"strings"
	"sync"

	"github.com/ticket/email-mail-node/internal/mailbox"
)

const defaultMessagePathIndexEntries = 10_000

type messagePathIndexKey struct {
	mailbox   string
	messageID string
}

type messagePathIndexEntry struct {
	key             messagePathIndexKey
	path            string
	size            int64
	modTimeUnixNano int64
}

// messagePathIndex is a bounded, process-local LRU. Maildir files are treated
// as immutable; size and mtime guard against stale paths and replaced files.
type messagePathIndex struct {
	mu         sync.Mutex
	scanMu     sync.Mutex
	maxEntries int
	entries    map[messagePathIndexKey]*list.Element
	lru        *list.List
}

func newMessagePathIndex(maxEntries int) *messagePathIndex {
	if maxEntries <= 0 {
		maxEntries = defaultMessagePathIndexEntries
	}
	return &messagePathIndex{
		maxEntries: maxEntries,
		entries:    make(map[messagePathIndexKey]*list.Element, maxEntries),
		lru:        list.New(),
	}
}

func (h *NodeHandler) messagePaths() *messagePathIndex {
	h.messageIndexOnce.Do(func() {
		if h.messageIndex == nil {
			h.messageIndex = newMessagePathIndex(defaultMessagePathIndexEntries)
		}
	})
	return h.messageIndex
}

func canonicalIndexMailbox(email string) string {
	localPart, domain, err := mailbox.ParseAddress(strings.TrimSpace(email))
	if err != nil {
		return strings.TrimSpace(email)
	}
	return localPart + "@" + domain
}

func newMessagePathIndexKey(email, messageID string) (messagePathIndexKey, bool) {
	messageID = normalizeMessageID(messageID)
	if messageID == "" {
		return messagePathIndexKey{}, false
	}
	if strings.HasPrefix(messageID, "fallback-") {
		messageID = strings.ToLower(messageID)
	}
	return messagePathIndexKey{
		mailbox:   canonicalIndexMailbox(email),
		messageID: messageID,
	}, true
}

func (idx *messagePathIndex) get(email, messageID string) (string, bool) {
	key, ok := newMessagePathIndexKey(email, messageID)
	if !ok {
		return "", false
	}

	idx.mu.Lock()
	element, ok := idx.entries[key]
	if !ok {
		idx.mu.Unlock()
		return "", false
	}
	idx.lru.MoveToFront(element)
	entry := element.Value.(messagePathIndexEntry)
	idx.mu.Unlock()

	info, err := os.Stat(entry.path)
	if err == nil && info.Mode().IsRegular() && info.Size() == entry.size && info.ModTime().UnixNano() == entry.modTimeUnixNano {
		return entry.path, true
	}

	idx.removeIfCurrent(entry)
	return "", false
}

func (idx *messagePathIndex) putFile(email, messageID, path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	idx.put(email, messageID, path, info.Size(), info.ModTime().UnixNano())
	return true
}

func (idx *messagePathIndex) put(email, messageID, path string, size, modTimeUnixNano int64) {
	key, ok := newMessagePathIndexKey(email, messageID)
	if !ok || path == "" {
		return
	}
	entry := messagePathIndexEntry{
		key:             key,
		path:            path,
		size:            size,
		modTimeUnixNano: modTimeUnixNano,
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if element, exists := idx.entries[key]; exists {
		element.Value = entry
		idx.lru.MoveToFront(element)
		return
	}

	element := idx.lru.PushFront(entry)
	idx.entries[key] = element
	for len(idx.entries) > idx.maxEntries {
		oldest := idx.lru.Back()
		if oldest == nil {
			break
		}
		oldEntry := oldest.Value.(messagePathIndexEntry)
		delete(idx.entries, oldEntry.key)
		idx.lru.Remove(oldest)
	}
}

func (idx *messagePathIndex) remove(email, messageID string) {
	key, ok := newMessagePathIndexKey(email, messageID)
	if !ok {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.removeLocked(key)
}

func (idx *messagePathIndex) removeIfCurrent(entry messagePathIndexEntry) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	element, ok := idx.entries[entry.key]
	if !ok {
		return
	}
	current := element.Value.(messagePathIndexEntry)
	if current.path == entry.path && current.size == entry.size && current.modTimeUnixNano == entry.modTimeUnixNano {
		idx.removeLocked(entry.key)
	}
}

func (idx *messagePathIndex) removeLocked(key messagePathIndexKey) {
	element, ok := idx.entries[key]
	if !ok {
		return
	}
	delete(idx.entries, key)
	idx.lru.Remove(element)
}

func readMessageHeaderID(reader io.Reader) (string, error) {
	message, err := netmail.ReadMessage(reader)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(message.Header.Get("Message-ID")), nil
}

func readMessageFileIdentity(filePath, maildirBase string) (messageID string, size int64, modTimeUnixNano int64, err error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return "", 0, 0, err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, 0, err
	}
	defer file.Close()

	messageID, err = readMessageHeaderID(file)
	if err != nil {
		return "", 0, 0, err
	}
	if messageID == "" {
		messageID = fallbackMessageID(filePath, maildirBase, info)
	}
	return messageID, info.Size(), info.ModTime().UnixNano(), nil
}

// findMessagePath uses the LRU first. A cold lookup reads only RFC 5322
// headers; full MIME parsing is reserved for the target message. Malformed
// headers retain compatibility through the existing full-parser fallback.
func (h *NodeHandler) findMessagePath(email, messageID string) (string, bool) {
	index := h.messagePaths()
	if path, ok := index.get(email, messageID); ok {
		return path, true
	}

	index.scanMu.Lock()
	defer index.scanMu.Unlock()
	if path, ok := index.get(email, messageID); ok {
		return path, true
	}

	normalized := normalizeMessageID(messageID)
	for _, filePath := range sortMailFilesByModTimeDesc(h.scanMailboxFiles(email)) {
		candidateID, size, modTimeUnixNano, err := readMessageFileIdentity(filePath, h.mailboxMgr.MaildirBase())
		if err != nil {
			candidate, parseErr := parseFullMessage(filePath, email, h.mailboxMgr.MaildirBase())
			if parseErr != nil {
				continue
			}
			index.putFile(email, candidate.MessageID, filePath)
			if matchMessageID(candidate.MessageID, messageID, normalized) {
				return filePath, true
			}
			continue
		}

		index.put(email, candidateID, filePath, size, modTimeUnixNano)
		if matchMessageID(candidateID, messageID, normalized) {
			return filePath, true
		}
	}
	return "", false
}

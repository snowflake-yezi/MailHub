package nodesession

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
)

const defaultSendQueueSize = 64

var (
	ErrSessionNotFound   = errors.New("node control session not found")
	ErrSessionReplaced   = errors.New("node control session replaced")
	ErrSessionRevoked    = errors.New("node control session revoked")
	ErrCredentialExpired = errors.New("node credential rotation overlap expired")
)

type RegisterInput struct {
	ServerID            uint64
	NodeUUID            string
	BootID              string
	Protocol            uint32
	AgentVersion        string
	Capabilities        []string
	ConnectedAt         time.Time
	CredentialID        uint64
	CredentialVersion   uint64
	CredentialExpiresAt *time.Time
	Cancel              context.CancelCauseFunc
	SendQueueSize       int
}

type Session struct {
	ID                  string
	ServerID            uint64
	NodeUUID            string
	BootID              string
	Protocol            uint32
	AgentVersion        string
	Capabilities        []string
	ConnectedAt         time.Time
	CredentialID        uint64
	CredentialVersion   uint64
	CredentialExpiresAt *time.Time

	ctx      context.Context
	cancel   context.CancelCauseFunc
	outgoing chan *nodev1.SystemControlFrame
}

func (session *Session) Context() context.Context {
	return session.ctx
}

func (session *Session) Outgoing() <-chan *nodev1.SystemControlFrame {
	return session.outgoing
}

func (session *Session) Send(ctx context.Context, frame *nodev1.SystemControlFrame) error {
	if frame == nil {
		return errors.New("node control frame is required")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-session.ctx.Done():
		return context.Cause(session.ctx)
	case session.outgoing <- frame:
		return nil
	}
}

type Registry struct {
	mu       sync.RWMutex
	byServer map[uint64]*Session
}

func NewRegistry() *Registry {
	return &Registry{byServer: make(map[uint64]*Session)}
}

func (registry *Registry) Register(parent context.Context, input RegisterInput) *Session {
	queueSize := input.SendQueueSize
	if queueSize <= 0 {
		queueSize = defaultSendQueueSize
	}
	ctx, cancel := context.WithCancelCause(parent)
	session := &Session{
		ID: uuid.NewString(), ServerID: input.ServerID, NodeUUID: input.NodeUUID,
		BootID: input.BootID, Protocol: input.Protocol, AgentVersion: input.AgentVersion,
		Capabilities: append([]string(nil), input.Capabilities...), ConnectedAt: input.ConnectedAt.UTC(),
		CredentialID: input.CredentialID, CredentialVersion: input.CredentialVersion,
		ctx: ctx, cancel: cancel, outgoing: make(chan *nodev1.SystemControlFrame, queueSize),
	}
	if input.CredentialExpiresAt != nil {
		expiresAt := input.CredentialExpiresAt.UTC()
		session.CredentialExpiresAt = &expiresAt
	}

	registry.mu.Lock()
	previous := registry.byServer[input.ServerID]
	registry.byServer[input.ServerID] = session
	registry.mu.Unlock()

	if previous != nil {
		previous.cancel(ErrSessionReplaced)
	}
	if input.Cancel != nil {
		go func() {
			<-ctx.Done()
			input.Cancel(context.Cause(ctx))
		}()
	}
	return session
}

func (registry *Registry) Get(serverID uint64) (*Session, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	session, ok := registry.byServer[serverID]
	return session, ok
}

func (registry *Registry) IsCurrent(serverID uint64, sessionID string) bool {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	session, ok := registry.byServer[serverID]
	return ok && session.ID == sessionID
}

func (registry *Registry) Remove(serverID uint64, sessionID string) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	session, ok := registry.byServer[serverID]
	if !ok || session.ID != sessionID {
		return false
	}
	delete(registry.byServer, serverID)
	return true
}

func (registry *Registry) DisconnectServer(serverID uint64, cause error) bool {
	if cause == nil {
		cause = ErrSessionRevoked
	}
	registry.mu.Lock()
	session, ok := registry.byServer[serverID]
	if ok {
		delete(registry.byServer, serverID)
	}
	registry.mu.Unlock()
	if ok {
		session.cancel(cause)
	}
	return ok
}

func (registry *Registry) DisconnectCredential(serverID, credentialID uint64, cause error) bool {
	if cause == nil {
		cause = ErrSessionRevoked
	}
	registry.mu.Lock()
	session, ok := registry.byServer[serverID]
	if ok && session.CredentialID == credentialID {
		delete(registry.byServer, serverID)
	} else {
		ok = false
	}
	registry.mu.Unlock()
	if ok {
		session.cancel(cause)
	}
	return ok
}

func (registry *Registry) ExpireCredentialAt(serverID, credentialID uint64, expiresAt time.Time) bool {
	registry.mu.RLock()
	session, ok := registry.byServer[serverID]
	if !ok || session.CredentialID != credentialID {
		registry.mu.RUnlock()
		return false
	}
	registry.mu.RUnlock()

	go func(expected *Session) {
		timer := time.NewTimer(time.Until(expiresAt))
		defer timer.Stop()
		select {
		case <-expected.Context().Done():
			return
		case <-timer.C:
		}

		registry.mu.Lock()
		current, currentOK := registry.byServer[serverID]
		if currentOK && current == expected && current.CredentialID == credentialID {
			delete(registry.byServer, serverID)
		} else {
			currentOK = false
		}
		registry.mu.Unlock()
		if currentOK {
			expected.cancel(ErrCredentialExpired)
		}
	}(session)
	return true
}

func (registry *Registry) Send(ctx context.Context, serverID uint64, frame *nodev1.SystemControlFrame) error {
	session, ok := registry.Get(serverID)
	if !ok {
		return ErrSessionNotFound
	}
	return session.Send(ctx, frame)
}

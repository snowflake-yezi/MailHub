package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	SessionCookieName = "mgmt_session"
	SessionDuration   = 24 * time.Hour
)

// Session holds admin session data.
type Session struct {
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// IsExpired returns true if the session has expired.
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// SessionManager manages in-memory admin sessions.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionManager creates a new session manager and starts the GC goroutine.
func NewSessionManager() *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
	}
	go sm.gcLoop()
	return sm
}

// CreateSession creates a new session for the given username. Returns the token.
func (sm *SessionManager) CreateSession(username string) (string, error) {
	token, err := sm.generateToken()
	if err != nil {
		return "", err
	}

	now := time.Now()
	sm.mu.Lock()
	sm.sessions[token] = &Session{
		Username:  username,
		CreatedAt: now,
		ExpiresAt: now.Add(SessionDuration),
	}
	sm.mu.Unlock()

	return token, nil
}

// ValidateSession returns the session if the token is valid, nil otherwise.
// Expired sessions are cleaned up on access.
func (sm *SessionManager) ValidateSession(token string) *Session {
	if token == "" {
		return nil
	}

	sm.mu.RLock()
	s, ok := sm.sessions[token]
	sm.mu.RUnlock()

	if !ok {
		return nil
	}

	if s.IsExpired() {
		sm.mu.Lock()
		delete(sm.sessions, token)
		sm.mu.Unlock()
		return nil
	}

	return s
}

// DestroySession removes a session by token.
func (sm *SessionManager) DestroySession(token string) {
	sm.mu.Lock()
	delete(sm.sessions, token)
	sm.mu.Unlock()
}

// AdminAuthRequired validates the admin session cookie.
// For page requests it redirects to /admin/login. For API requests it returns 401 JSON.
func AdminAuthRequired(sm *SessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, _ := c.Cookie(SessionCookieName)
		session := sm.ValidateSession(token)

		if session == nil {
			// API requests: return 401 JSON
			if isAPIRequest(c) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"code": 1003, "message": "authentication required",
				})
				return
			}
			// Page requests: redirect to login
			next := c.Request.URL.Path
			if c.Request.URL.RawQuery != "" {
				next += "?" + c.Request.URL.RawQuery
			}
			redirectURL := "/admin/login"
			if next != "" && next != "/" {
				redirectURL += "?next=" + next // URL encoding handled by browser
			}
			c.Redirect(http.StatusFound, redirectURL)
			c.Abort()
			return
		}

		c.Set("admin_user", session.Username)
		c.Next()
	}
}

// isAPIRequest returns true if the request is for an API endpoint.
func isAPIRequest(c *gin.Context) bool {
	path := c.Request.URL.Path
	return len(path) >= 8 && path[:8] == "/api/v1/"
}

// gcLoop periodically cleans up expired sessions.
func (sm *SessionManager) gcLoop() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		sm.mu.Lock()
		for token, s := range sm.sessions {
			if s.IsExpired() {
				delete(sm.sessions, token)
			}
		}
		sm.mu.Unlock()
	}
}

// generateToken creates a cryptographically random 32-byte hex token.
func (sm *SessionManager) generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Package session manages in-flight diagnostic sessions and UniFi correlation.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
	"time"

	"wifi-diagnostics/internal/unifi"
)

// CorrelationStatus describes the outcome of a UniFi client lookup.
type CorrelationStatus string

const (
	CorrelationPending     CorrelationStatus = "pending"
	CorrelationOK          CorrelationStatus = "ok"
	CorrelationNotFound    CorrelationStatus = "not_found"
	CorrelationAmbiguous   CorrelationStatus = "ambiguous"
	CorrelationUnavailable CorrelationStatus = "unavailable"
)

// Session is a single in-flight diagnostic session.
type Session struct {
	mu sync.RWMutex

	ID        string
	CreatedAt time.Time
	ClientIP  string
	UserAgent string

	// Correlation
	CorrelationStatus CorrelationStatus
	CorrelationAt     time.Time
	UnifiClient       *unifi.Client
	UAValidation      string // "match" | "mismatch" | "unknown"
}

// NewID generates a cryptographically random 16-byte hex session ID.
func NewID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// New creates a new Session.
func New(id, clientIP, userAgent string) *Session {
	return &Session{
		ID:                id,
		CreatedAt:         time.Now(),
		ClientIP:          clientIP,
		UserAgent:         userAgent,
		CorrelationStatus: CorrelationPending,
	}
}

// SetCorrelation records the outcome of a UniFi client lookup.
func (s *Session) SetCorrelation(status CorrelationStatus, cl *unifi.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.CorrelationStatus = status
	s.CorrelationAt = time.Now()
	s.UnifiClient = cl

	if cl != nil && s.UserAgent != "" {
		s.UAValidation = validateUA(s.UserAgent, cl)
	}
}

// Snapshot returns a point-in-time copy of the correlation fields, safe to read without holding the lock.
func (s *Session) Snapshot() SessionSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SessionSnapshot{
		CorrelationStatus: s.CorrelationStatus,
		CorrelationAt:     s.CorrelationAt,
		UnifiClient:       s.UnifiClient,
		UAValidation:      s.UAValidation,
	}
}

// SessionSnapshot is a lock-free copy of correlation state.
type SessionSnapshot struct {
	CorrelationStatus CorrelationStatus
	CorrelationAt     time.Time
	UnifiClient       *unifi.Client
	UAValidation      string
}

// Manager tracks in-flight sessions and handles UniFi correlation with retries.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session

	unifi  unifi.UniFiClient
	logger *slog.Logger
}

// NewManager creates a Manager.
func NewManager(uc unifi.UniFiClient, logger *slog.Logger) *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		unifi:    uc,
		logger:   logger,
	}
}

// Create allocates and registers a new session, then begins async UniFi correlation.
func (m *Manager) Create(clientIP, userAgent string) (*Session, error) {
	id, err := NewID()
	if err != nil {
		return nil, err
	}
	sess := New(id, clientIP, userAgent)

	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()

	go m.correlate(sess)
	return sess, nil
}

// Get returns the session with the given ID, or nil if not found.
func (m *Manager) Get(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// Remove removes the session from the in-memory map (called after completion + persistence).
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// correlate attempts UniFi IP lookup with retries for up to 30 seconds to tolerate
// newly-connected clients not yet appearing in the controller's active list.
func (m *Manager) correlate(sess *Session) {
	const (
		maxWait     = 30 * time.Second
		retryPeriod = 5 * time.Second
	)

	ctx, cancel := context.WithTimeout(context.Background(), maxWait)
	defer cancel()

	for {
		cl, err := m.unifi.FindClientByIP(ctx, sess.ClientIP)
		if err == nil {
			m.logger.Info("unifi: correlated session",
				"session_id", sess.ID,
				"client_ip", sess.ClientIP,
				"mac", cl.MAC,
				"hostname", cl.DisplayName())
			sess.SetCorrelation(CorrelationOK, cl)
			return
		}

		switch {
		case unifi.IsNotFound(err):
			// Retry — client may not have appeared in UniFi yet.
			m.logger.Debug("unifi: client not found yet, will retry",
				"session_id", sess.ID, "client_ip", sess.ClientIP)
		case unifi.IsAmbiguous(err):
			// Multiple clients with same IP — ambiguous, do not guess.
			m.logger.Warn("unifi: ambiguous correlation",
				"session_id", sess.ID, "client_ip", sess.ClientIP)
			sess.SetCorrelation(CorrelationAmbiguous, nil)
			return
		case unifi.IsUnavailable(err):
			// Controller unreachable — degrade gracefully.
			m.logger.Warn("unifi: controller unavailable for correlation",
				"session_id", sess.ID, "error", err)
			sess.SetCorrelation(CorrelationUnavailable, nil)
			return
		default:
			m.logger.Error("unifi: unexpected error during correlation",
				"session_id", sess.ID, "error", err)
			sess.SetCorrelation(CorrelationUnavailable, nil)
			return
		}

		select {
		case <-ctx.Done():
			m.logger.Info("unifi: correlation timed out",
				"session_id", sess.ID, "client_ip", sess.ClientIP)
			sess.SetCorrelation(CorrelationNotFound, nil)
			return
		case <-time.After(retryPeriod):
			// retry
		}
	}
}

// validateUA returns "match", "mismatch", or "unknown" by comparing the browser
// User-Agent against the device type reported by UniFi. This is a validation
// signal only, not a primary correlator.
func validateUA(ua string, cl *unifi.Client) string {
	if ua == "" || cl == nil {
		return "unknown"
	}
	uaLower := strings.ToLower(ua)

	// Heuristic: check for gross mismatches only.
	isIOSUA := strings.Contains(uaLower, "iphone") || strings.Contains(uaLower, "ipad")
	isAndroidUA := strings.Contains(uaLower, "android")
	isWindowsUA := strings.Contains(uaLower, "windows")
	isMacUA := strings.Contains(uaLower, "macintosh") || strings.Contains(uaLower, "mac os x")

	hostLower := strings.ToLower(cl.DisplayName())
	isIOSHost := strings.Contains(hostLower, "iphone") || strings.Contains(hostLower, "ipad")
	isAndroidHost := strings.Contains(hostLower, "android")
	isWindowsHost := strings.Contains(hostLower, "windows") || strings.Contains(hostLower, "pc")
	_ = strings.Contains(hostLower, "mac") || strings.Contains(hostLower, "macbook") // isMacHost unused in mismatch check

	// Detect obvious mismatches (e.g. UA says iPhone, UniFi says Windows PC).
	mismatch := (isIOSUA && (isWindowsHost || isAndroidHost)) ||
		(isAndroidUA && (isIOSHost || isWindowsHost)) ||
		(isWindowsUA && (isIOSHost || isAndroidHost)) ||
		(isMacUA && (isIOSHost || isAndroidHost || isWindowsHost))

	if mismatch {
		return "mismatch"
	}
	if isIOSUA || isAndroidUA || isWindowsUA || isMacUA {
		return "match"
	}
	return "unknown"
}

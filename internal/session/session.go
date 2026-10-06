// Package session mirrors old/src/session.ts: sticky session store with
// upward escape. The session id hash is part of the external contract (route
// cards store it; flux report groups by it) — it must produce identical ids
// to the TS engine for the same conversation.
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"sync"
	"time"

	"github.com/abhijeet/fluxrouter/internal/jsstr"
	"github.com/abhijeet/fluxrouter/internal/types"
)

// State mirrors session.ts SessionState.
type State struct {
	Tier     types.TierId
	Model    string
	Upstream string
	PinnedAt string // RFC3339 with milliseconds, ISO "Z"
	Turns    int64
}

// TTL mirrors session.ts TTL_MS (6 hours of inactivity).
const TTL = 6 * time.Hour

// Store mirrors session.ts SessionStore.
type Store struct {
	mu       sync.Mutex
	sessions map[string]*State
	stop     chan struct{}
	stopped  chan struct{}
}

// NewStore starts the 10-minute sweep goroutine (mirrors the setInterval).
func NewStore() *Store {
	s := &Store{
		sessions: make(map[string]*State),
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	go func() {
		defer close(s.stopped)
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.Sweep()
			}
		}
	}()
	return s
}

// Message is the minimal input shape for SessionIDFor.
type Message struct {
	Role    string
	Content string
}

// SessionIDFor mirrors session.ts sessionIdFor:
// sha256(`${sys.length}:${sys}\0${firstUser}`).hex.slice(0, 24)
// where sys is the FIRST system message and firstUser the FIRST user message.
// NOTE: JS `${sys.length}` is the UTF-16 code-unit length — use jsLen, not
// Go len(), or ids diverge on non-ASCII system prompts.
func SessionIDFor(messages []Message) string {
	sys, firstUser := "", ""
	for _, m := range messages {
		if m.Role == "system" && sys == "" {
			sys = m.Content
		}
		if m.Role == "user" && firstUser == "" {
			firstUser = m.Content
		}
		if sys != "" && firstUser != "" {
			break
		}
	}
	seed := strconv.Itoa(jsstr.JsLen(sys)) + ":" + sys + "\x00" + firstUser
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])[:24]
}

// Get mirrors session.ts get: expired entries are deleted and report missing.
func (s *Store) Get(sessionID string) *State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[sessionID]
	if !ok {
		return nil
	}
	pinned, err := time.Parse(time.RFC3339Nano, st.PinnedAt)
	if err == nil && time.Since(pinned) > TTL {
		delete(s.sessions, sessionID)
		return nil
	}
	cp := *st
	return &cp
}

// Pin mirrors session.ts pin: first-turn pin with turns=1.
func (s *Store) Pin(sessionID string, tier types.TierId, model, upstream string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = &State{
		Tier:     tier,
		Model:    model,
		Upstream: upstream,
		PinnedAt: nowISO(),
		Turns:    1,
	}
}

// RePin mirrors session.ts rePin: update pinned tier; falls back to pin.
func (s *Store) RePin(sessionID string, tier types.TierId, model, upstream string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.sessions[sessionID]; ok {
		st.Tier = tier
		st.Model = model
		st.Upstream = upstream
		st.PinnedAt = nowISO()
		st.Turns++
		return
	}
	s.sessions[sessionID] = &State{Tier: tier, Model: model, Upstream: upstream, PinnedAt: nowISO(), Turns: 1}
}

// BumpTurn mirrors session.ts bumpTurn.
func (s *Store) BumpTurn(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.sessions[sessionID]; ok {
		st.Turns++
		st.PinnedAt = nowISO()
	}
}

// Sweep mirrors session.ts sweep: drop entries idle beyond TTL.
func (s *Store) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, st := range s.sessions {
		pinned, err := time.Parse(time.RFC3339Nano, st.PinnedAt)
		if err == nil && now.Sub(pinned) > TTL {
			delete(s.sessions, k)
		}
	}
}

// Size mirrors session.ts get size.
func (s *Store) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// Dispose mirrors session.ts dispose: stop sweep, clear map.
func (s *Store) Dispose() {
	s.mu.Lock()
	if s.stop != nil {
		select {
		case <-s.stop:
		default:
			close(s.stop)
		}
	}
	s.sessions = make(map[string]*State)
	s.mu.Unlock()
	select {
	case <-s.stopped:
	case <-time.After(time.Second):
	}
}

// nowISO produces the same shape as JS new Date().toISOString():
// "2026-09-30T12:00:00.000Z" — exactly 3 fractional digits, UTC.
func nowISO() string {
	return FormatISO(time.Now())
}

// FormatISO formats t as JS Date.prototype.toISOString() does.
func FormatISO(t time.Time) string {
	t = t.UTC()
	return t.Format("2006-01-02T15:04:05.000Z")
}
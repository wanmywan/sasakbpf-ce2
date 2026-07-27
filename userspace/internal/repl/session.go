package repl

import (
	"sync"
	"time"
)

// Session tracks per-target operator-side state. This replaces the inline
// map[string]time.Time that the old REPL used; we now also track RTT and
// command count to render a richer /list view.
type Session struct {
	mu    sync.Mutex
	items map[string]*Target
}

type Target struct {
	ID       string
	LastSeen time.Time
	LastRTT  time.Duration
	CmdCount int
	Online   bool
}

func NewSession() *Session {
	return &Session{items: make(map[string]*Target)}
}

func (s *Session) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.items[id]
	if t == nil {
		t = &Target{ID: id}
		s.items[id] = t
	}
	t.LastSeen = time.Now()
	t.Online = true
}

func (s *Session) SetRTT(id string, rtt time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.items[id]; t != nil {
		t.LastRTT = rtt
		t.LastSeen = time.Now()
		t.Online = true
	}
}

func (s *Session) IncCmd(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.items[id]; t != nil {
		t.CmdCount++
	}
}

// Expire marks targets older than `max` as offline (kept in map for /list).
func (s *Session) Expire(max time.Duration) {
	cutoff := time.Now().Add(-max)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.items {
		if t.LastSeen.Before(cutoff) {
			t.Online = false
		}
	}
}

// OnlineCount returns number of targets seen within `max`.
func (s *Session) OnlineCount(max time.Duration) int {
	cutoff := time.Now().Add(-max)
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, t := range s.items {
		if t.LastSeen.After(cutoff) {
			n++
		}
	}
	return n
}

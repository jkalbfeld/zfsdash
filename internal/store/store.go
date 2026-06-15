package store

import (
	"sync"
	"time"

	"github.com/jkalbfeld/zfsdash/internal/zfs"
)

// HistoryEntry stores a snapshot of pool data at a point in time.
type HistoryEntry struct {
	CollectedAt time.Time
	Pools       []zfs.Pool
}

// Store holds the latest ZFS data and a rolling history window.
type Store struct {
	mu      sync.RWMutex
	latest  *zfs.CollectedData
	history []HistoryEntry // newest last, capped at maxHistory
}

const maxHistory = 1440 // 24h at 1-min intervals

func New() *Store {
	return &Store{}
}

// Set replaces the latest collected data and appends to history.
func (s *Store) Set(d *zfs.CollectedData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = d
	s.history = append(s.history, HistoryEntry{
		CollectedAt: d.CollectedAt,
		Pools:       d.Pools,
	})
	if len(s.history) > maxHistory {
		s.history = s.history[len(s.history)-maxHistory:]
	}
}

// Get returns the latest collected data (nil if not yet collected).
func (s *Store) Get() *zfs.CollectedData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest
}

// History returns a copy of the history slice.
func (s *Store) History() []HistoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]HistoryEntry, len(s.history))
	copy(out, s.history)
	return out
}

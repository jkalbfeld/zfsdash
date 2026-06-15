package store

import (
	"sync"
	"time"

	"github.com/jkalbfeld/zfsdash/internal/zfs"
)

// Snapshot holds a single poll result with its timestamp.
type Snapshot struct {
	CollectedAt time.Time
	Data        *zfs.Data
}

// Store is a thread-safe in-memory ring buffer of recent snapshots.
type Store struct {
	mu       sync.RWMutex
	current  *Snapshot
	history  []*Snapshot // last 1440 samples (~24h at 60s interval)
	maxItems int
}

func New() *Store {
	return &Store{maxItems: 1440}
}

func (s *Store) Set(data *zfs.Data) {
	snap := &Snapshot{
		CollectedAt: time.Now(),
		Data:        data,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = snap
	s.history = append(s.history, snap)
	if len(s.history) > s.maxItems {
		s.history = s.history[len(s.history)-s.maxItems:]
	}
}

func (s *Store) Get() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *Store) History() []*Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Snapshot, len(s.history))
	copy(out, s.history)
	return out
}

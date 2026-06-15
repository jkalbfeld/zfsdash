package store

import (
	"sync"

	"github.com/jkalbfeld/zfsdash/internal/zfs"
)

// Store holds the most-recent collected snapshot in memory.
type Store struct {
	mu   sync.RWMutex
	data *zfs.CollectedData
}

func New() *Store {
	return &Store{}
}

func (s *Store) Set(d *zfs.CollectedData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = d
}

func (s *Store) Get() *zfs.CollectedData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

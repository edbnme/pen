package server

import (
	"fmt"
	"sync"
)

// OperationLock provides domain-exclusive locking for CDP operations
// that cannot run concurrently (e.g., Tracing, HeapProfiler).
type OperationLock struct {
	mu    sync.Mutex
	locks map[string]struct{}
}

// NewOperationLock creates a new OperationLock.
func NewOperationLock() *OperationLock {
	return &OperationLock{
		locks: make(map[string]struct{}),
	}
}

// Acquire attempts to lock a domain exclusively. Returns a release function
// that must be called (typically via defer) when the operation completes.
// Returns an error if the domain is already locked.
func (ol *OperationLock) Acquire(domain string) (release func(), err error) {
	ol.mu.Lock()
	defer ol.mu.Unlock()

	if _, held := ol.locks[domain]; held {
		return nil, fmt.Errorf("%s is already in use by another operation", domain)
	}
	ol.locks[domain] = struct{}{}
	return func() {
		ol.mu.Lock()
		delete(ol.locks, domain)
		ol.mu.Unlock()
	}, nil
}

// IsLocked returns whether a domain is currently locked.
func (ol *OperationLock) IsLocked(domain string) bool {
	ol.mu.Lock()
	defer ol.mu.Unlock()
	_, held := ol.locks[domain]
	return held
}

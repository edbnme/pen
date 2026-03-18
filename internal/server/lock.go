package server

import (
	"fmt"
	"sync"
	"time"
)

// OperationLock provides domain-exclusive locking for CDP operations
// that cannot run concurrently (e.g., Tracing, HeapProfiler).
type OperationLock struct {
	mu    sync.Mutex
	locks map[string]string    // domain → holder description
	since map[string]time.Time // domain → lock time
}

// NewOperationLock creates a new OperationLock.
func NewOperationLock() *OperationLock {
	return &OperationLock{
		locks: make(map[string]string),
		since: make(map[string]time.Time),
	}
}

// Acquire attempts to lock a domain exclusively. Returns a release function
// that must be called (typically via defer) when the operation completes.
// Returns an error if the domain is already locked.
func (ol *OperationLock) Acquire(domain string) (release func(), err error) {
	ol.mu.Lock()
	defer ol.mu.Unlock()

	if holder, held := ol.locks[domain]; held {
		duration := time.Since(ol.since[domain]).Round(time.Second)
		return nil, fmt.Errorf("%s is already in use by %s (held for %s)", domain, holder, duration)
	}
	ol.locks[domain] = domain
	ol.since[domain] = time.Now()
	return func() {
		ol.mu.Lock()
		delete(ol.locks, domain)
		delete(ol.since, domain)
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

// ActiveOperations returns a snapshot of currently held locks with their durations.
func (ol *OperationLock) ActiveOperations() map[string]time.Duration {
	ol.mu.Lock()
	defer ol.mu.Unlock()
	result := make(map[string]time.Duration, len(ol.locks))
	for domain := range ol.locks {
		result[domain] = time.Since(ol.since[domain])
	}
	return result
}

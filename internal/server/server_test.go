package server

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/edbnme/pen/internal/cdp"
)

func TestOperationLock(t *testing.T) {
	ol := NewOperationLock()

	// Acquire should succeed.
	release, err := ol.Acquire("Profiler")
	if err != nil {
		t.Fatalf("first Acquire should succeed: %v", err)
	}

	// Should report as locked.
	if !ol.IsLocked("Profiler") {
		t.Error("IsLocked should return true after Acquire")
	}

	// Second Acquire on same domain should fail.
	_, err = ol.Acquire("Profiler")
	if err == nil {
		t.Fatal("second Acquire on same domain should fail")
	}

	// Different domain should succeed.
	release2, err := ol.Acquire("Tracing")
	if err != nil {
		t.Fatalf("Acquire on different domain should succeed: %v", err)
	}

	// Release first lock.
	release()
	if ol.IsLocked("Profiler") {
		t.Error("IsLocked should return false after release")
	}

	// Re-acquire should work.
	release3, err := ol.Acquire("Profiler")
	if err != nil {
		t.Fatalf("re-Acquire after release should succeed: %v", err)
	}

	release2()
	release3()
}

func TestOperationLockIsLockedUnknownDomain(t *testing.T) {
	ol := NewOperationLock()
	if ol.IsLocked("NonExistent") {
		t.Error("IsLocked for non-existent domain should return false")
	}
}

func TestOperationLockReleaseIdempotent(t *testing.T) {
	ol := NewOperationLock()
	release, err := ol.Acquire("Test")
	if err != nil {
		t.Fatal(err)
	}
	// Double release should not panic.
	release()
	release()
	if ol.IsLocked("Test") {
		t.Error("domain should be unlocked after release")
	}
}

func TestOperationLockConcurrent(t *testing.T) {
	ol := NewOperationLock()
	const goroutines = 50

	var wg sync.WaitGroup
	successes := make(chan struct{}, goroutines)
	failures := make(chan struct{}, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			release, err := ol.Acquire("Contended")
			if err != nil {
				failures <- struct{}{}
				return
			}
			successes <- struct{}{}
			release()
		}()
	}
	wg.Wait()
	close(successes)
	close(failures)

	successCount := 0
	for range successes {
		successCount++
	}
	failCount := 0
	for range failures {
		failCount++
	}
	// At least one should succeed. With release() happening quickly, multiple may succeed.
	if successCount == 0 {
		t.Error("at least one goroutine should acquire the lock")
	}
	t.Logf("successes=%d failures=%d (total=%d)", successCount, failCount, goroutines)
}

func TestOperationLockMultipleDomains(t *testing.T) {
	ol := NewOperationLock()

	domains := []string{"A", "B", "C", "D", "E"}
	releases := make([]func(), len(domains))

	// Acquire all.
	for i, d := range domains {
		r, err := ol.Acquire(d)
		if err != nil {
			t.Fatalf("Acquire(%q) should succeed: %v", d, err)
		}
		releases[i] = r
	}

	// All should be locked.
	for _, d := range domains {
		if !ol.IsLocked(d) {
			t.Errorf("%q should be locked", d)
		}
	}

	// Release all.
	for _, r := range releases {
		r()
	}

	// None should be locked.
	for _, d := range domains {
		if ol.IsLocked(d) {
			t.Errorf("%q should be unlocked", d)
		}
	}
}

func TestNotifyProgressNilReq(t *testing.T) {
	// NotifyProgress with nil request should not panic.
	NotifyProgress(context.TODO(), nil, 0, 1, "msg")
}

func TestNew(t *testing.T) {
	client := cdp.NewClient("http://localhost:9222", nil)
	cfg := &Config{
		Name:      "test-pen",
		Version:   "0.0.1",
		Transport: "stdio",
	}
	p := New(client, cfg)
	if p == nil {
		t.Fatal("New returned nil")
	}
	if p.Server() == nil {
		t.Error("Server() should not be nil")
	}
	if p.CDP() != client {
		t.Error("CDP() should return the provided client")
	}
	if p.Locks() == nil {
		t.Error("Locks() should not be nil")
	}
}

func TestNewWithNilLogger(t *testing.T) {
	client := cdp.NewClient("http://localhost:9222", nil)
	cfg := &Config{
		Name:    "test",
		Version: "1.0.0",
	}
	p := New(client, cfg)
	if p == nil {
		t.Fatal("New returned nil")
	}
	// Logger should be set to default.
	if p.logger == nil {
		t.Error("logger should be set to default when nil")
	}
}

func TestNewWithLogger(t *testing.T) {
	logger := slog.Default()
	client := cdp.NewClient("http://localhost:9222", logger)
	cfg := &Config{
		Name:    "test",
		Version: "1.0.0",
		Logger:  logger,
	}
	p := New(client, cfg)
	if p.logger != logger {
		t.Error("logger should be the one provided in config")
	}
}

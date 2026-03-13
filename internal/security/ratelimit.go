package security

import (
	"fmt"
	"sync"
	"time"
)

// DefaultCooldowns defines rate limits for resource-intensive tools.
var DefaultCooldowns = map[string]time.Duration{
	"pen_heap_snapshot":    10 * time.Second,
	"pen_capture_trace":    5 * time.Second,
	"pen_collect_garbage":  5 * time.Second,
	"pen_lighthouse_audit": 30 * time.Second,
}

// RateLimiter enforces per-tool cooldown periods.
type RateLimiter struct {
	mu        sync.Mutex
	lastCalls map[string]time.Time
	cooldowns map[string]time.Duration
}

// NewRateLimiter creates a rate limiter with the given cooldowns.
func NewRateLimiter(cooldowns map[string]time.Duration) *RateLimiter {
	return &RateLimiter{
		lastCalls: make(map[string]time.Time),
		cooldowns: cooldowns,
	}
}

// Check returns nil if the tool can proceed, or an error with remaining wait time.
func (rl *RateLimiter) Check(toolName string) error {
	cooldown, hasCooldown := rl.cooldowns[toolName]
	if !hasCooldown {
		return nil
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if last, ok := rl.lastCalls[toolName]; ok {
		remaining := cooldown - time.Since(last)
		if remaining > 0 {
			return fmt.Errorf(
				"%s has a %s cooldown. Try again in %s",
				toolName, cooldown, remaining.Round(time.Second),
			)
		}
	}

	rl.lastCalls[toolName] = time.Now()
	return nil
}

// Record records a tool invocation for cooldown tracking without checking.
// Use when the tool check happens separately from recording.
func (rl *RateLimiter) Record(toolName string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.lastCalls[toolName] = time.Now()
}

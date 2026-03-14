package cdp

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	c := NewClient("http://localhost:9222", nil)
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.debugURL != "http://localhost:9222" {
		t.Errorf("debugURL = %q, want %q", c.debugURL, "http://localhost:9222")
	}
	if c.connected {
		t.Error("new client should not be connected")
	}
}

func TestNewClientWithLogger(t *testing.T) {
	logger := slog.Default()
	c := NewClient("http://127.0.0.1:9222", logger)
	if c.logger != logger {
		t.Error("logger should be set to provided logger")
	}
}

func TestNewClientNilLogger(t *testing.T) {
	c := NewClient("http://localhost:9222", nil)
	if c.logger == nil {
		t.Error("nil logger should be replaced with default")
	}
}

func TestClientIsConnectedFalse(t *testing.T) {
	c := NewClient("http://localhost:9222", nil)
	if c.IsConnected() {
		t.Error("new client should not report as connected")
	}
}

func TestClientContextNotConnected(t *testing.T) {
	c := NewClient("http://localhost:9222", nil)
	_, err := c.Context()
	if err == nil {
		t.Fatal("Context() should error when not connected")
	}
	if !strings.Contains(err.Error(), "remote-debugging-port") {
		t.Errorf("error should mention remote-debugging-port, got: %v", err)
	}
}

func TestClientAllocContextNotConnected(t *testing.T) {
	c := NewClient("http://localhost:9222", nil)
	_, err := c.AllocContext()
	if err == nil {
		t.Fatal("AllocContext() should error when not connected")
	}
	if !strings.Contains(err.Error(), "remote-debugging-port") {
		t.Errorf("error should mention remote-debugging-port, got: %v", err)
	}
}

func TestClientCurrentTargetIDNotConnected(t *testing.T) {
	c := NewClient("http://localhost:9222", nil)
	if id := c.CurrentTargetID(); id != "" {
		t.Errorf("CurrentTargetID should be empty when not connected, got %q", id)
	}
}

func TestClientCloseWhenNotConnected(t *testing.T) {
	c := NewClient("http://localhost:9222", nil)
	// Close on a not-connected client should not panic.
	c.Close()
	if c.IsConnected() {
		t.Error("should remain not connected after Close")
	}
}

func TestContainsInsensitive(t *testing.T) {
	tests := []struct {
		s, substr string
		want      bool
	}{
		{"Hello World", "hello", true},
		{"Hello World", "WORLD", true},
		{"Hello World", "xyz", false},
		{"", "", true},
		{"abc", "", true},
		{"", "abc", false},
		{"FooBar", "foobar", true},
		{"FooBar", "OoBa", true},
		{"https://example.com/page", "example.com", true},
		{"https://example.com/page", "example.org", false},
	}
	for _, tt := range tests {
		got := containsInsensitive(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("containsInsensitive(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
		}
	}
}

func TestContextWithTimeoutNotConnected(t *testing.T) {
	c := NewClient("http://localhost:9222", nil)
	_, cancel, err := c.ContextWithTimeout(5 * time.Second)
	if err == nil {
		cancel()
		t.Fatal("ContextWithTimeout should error when not connected")
	}
	if !strings.Contains(err.Error(), "remote-debugging-port") {
		t.Errorf("error should mention remote-debugging-port, got: %v", err)
	}
}

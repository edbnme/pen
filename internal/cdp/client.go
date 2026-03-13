// Package cdp manages CDP connections to Chrome/Chromium browsers.
package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// Client manages the lifecycle of a CDP connection to a browser.
type Client struct {
	mu        sync.RWMutex
	debugURL  string
	allocCtx  context.Context
	allocStop context.CancelFunc
	ctx       context.Context
	ctxStop   context.CancelFunc
	connected bool
	logger    *slog.Logger
}

// NewClient creates a CDP client for the given debug URL.
// The URL should be an HTTP endpoint (e.g., http://localhost:9222).
func NewClient(debugURL string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		debugURL: debugURL,
		logger:   logger,
	}
}

// Connect establishes a CDP connection to the browser.
// It discovers the WebSocket endpoint and creates a chromedp context.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	wsURL, err := DiscoverEndpoint(ctx, c.debugURL)
	if err != nil {
		return fmt.Errorf("discover CDP endpoint: %w", err)
	}

	c.logger.Info("connecting to browser", "ws", wsURL)

	allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, wsURL)
	c.allocCtx = allocCtx
	c.allocStop = allocCancel

	tabCtx, tabCancel := chromedp.NewContext(allocCtx)
	c.ctx = tabCtx
	c.ctxStop = tabCancel

	// Verify the connection by running a no-op action.
	if err := chromedp.Run(tabCtx); err != nil {
		allocCancel()
		return fmt.Errorf("CDP handshake failed: %w", err)
	}

	c.connected = true
	c.logger.Info("CDP connection established")
	return nil
}

// Context returns the active chromedp context.
// Returns an error if not connected.
func (c *Client) Context() (context.Context, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.connected {
		return nil, errors.New("CDP not connected — call Connect() first")
	}
	return c.ctx, nil
}

// AllocContext returns the allocator context for creating new tab contexts.
func (c *Client) AllocContext() (context.Context, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.connected {
		return nil, errors.New("CDP not connected")
	}
	return c.allocCtx, nil
}

// IsConnected reports whether the client has an active connection.
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Close tears down the CDP connection cleanly.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctxStop != nil {
		c.ctxStop()
	}
	if c.allocStop != nil {
		c.allocStop()
	}
	c.connected = false
	c.logger.Info("CDP connection closed")
}

// Reconnect attempts to re-establish the CDP connection with exponential backoff.
// It tries up to maxAttempts times before returning an error.
func (c *Client) Reconnect(ctx context.Context, maxAttempts int) error {
	c.Close()

	backoff := 500 * time.Millisecond
	const maxBackoff = 10 * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		c.logger.Info("reconnecting", "attempt", attempt, "max", maxAttempts)

		err := c.Connect(ctx)
		if err == nil {
			return nil
		}

		c.logger.Warn("reconnect failed", "attempt", attempt, "err", err)

		if attempt == maxAttempts {
			return fmt.Errorf("reconnection failed after %d attempts: %w", maxAttempts, err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return errors.New("unreachable")
}

// DiscoverEndpoint probes common CDP ports and returns the browser WebSocket URL.
// It tries the provided URL first, then falls back to common ports.
func DiscoverEndpoint(ctx context.Context, baseURL string) (string, error) {
	// Try the provided URL directly first.
	if ws, err := fetchBrowserWSURL(ctx, baseURL); err == nil {
		return ws, nil
	}

	// Parse to get the host for fallback port scanning.
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid CDP URL %q: %w", baseURL, err)
	}
	host := parsed.Hostname()

	// Try common debugging ports.
	ports := []string{"9222", "9229", "9333"}
	for _, port := range ports {
		candidate := fmt.Sprintf("http://%s:%s", host, port)
		if ws, err := fetchBrowserWSURL(ctx, candidate); err == nil {
			return ws, nil
		}
	}

	return "", fmt.Errorf("no CDP endpoint found at %s (tried ports %v)", baseURL, ports)
}

// fetchBrowserWSURL calls /json/version on a Chrome debug endpoint
// and returns the webSocketDebuggerUrl.
func fetchBrowserWSURL(ctx context.Context, baseURL string) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	versionURL := baseURL + "/json/version"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, versionURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("/json/version returned HTTP %d", resp.StatusCode)
	}

	var info struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("decode /json/version: %w", err)
	}

	if info.WebSocketDebuggerURL == "" {
		return "", errors.New("webSocketDebuggerUrl is empty")
	}

	return info.WebSocketDebuggerURL, nil
}

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
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// Client manages the lifecycle of a CDP connection to a browser.
type Client struct {
	mu          sync.RWMutex
	debugURL    string
	allocCtx    context.Context
	allocStop   context.CancelFunc
	ctx         context.Context
	ctxStop     context.CancelFunc
	connected   bool
	logger      *slog.Logger
	parentCtx   context.Context // stored for reconnection
	reconnectFn func() error    // optional: called when connection is lost and a tool needs it
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

// SetLogger replaces the logger used by the client.
func (c *Client) SetLogger(logger *slog.Logger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logger = logger
}

// SetReconnectFunc sets a callback that is invoked when a tool needs a CDP
// connection but the browser has disconnected. The callback should re-launch
// the browser (if applicable) and then call Reconnect on this client.
func (c *Client) SetReconnectFunc(fn func() error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reconnectFn = fn
}

// Connect establishes a CDP connection to the browser.
// It discovers the WebSocket endpoint and creates a chromedp context.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()

	if c.connected {
		c.mu.Unlock()
		return nil
	}

	c.parentCtx = ctx

	wsURL, err := DiscoverEndpoint(ctx, c.debugURL)
	if err != nil {
		c.mu.Unlock()
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
		c.mu.Unlock()
		return fmt.Errorf("CDP handshake failed: %w", err)
	}

	c.connected = true
	c.logger.Info("CDP connection established")

	// Monitor for unexpected disconnection (browser crash, WebSocket close).
	go func() {
		<-tabCtx.Done()
		c.mu.Lock()
		wasConnected := c.connected
		// Only mark disconnected if this is still the active context.
		if c.ctx == tabCtx {
			c.connected = false
		}
		c.mu.Unlock()
		if wasConnected && c.ctx == tabCtx {
			c.logger.Warn("CDP connection lost - will attempt to reconnect on next tool call")
		}
	}()

	// Release the lock before autoSelectContentTarget, which calls
	// Context()/ListTargets() that need to acquire the read lock.
	c.mu.Unlock()

	// Auto-select a content tab if connected to about:blank.
	c.autoSelectContentTarget(ctx)

	return nil
}

// autoSelectContentTarget checks if the current target is about:blank
// and, if so, switches to the first page target with a real URL.
// This avoids the common UX issue where PEN connects to an empty tab.
func (c *Client) autoSelectContentTarget(ctx context.Context) {
	// Get the current target's URL.
	var currentURL string
	if err := chromedp.Run(c.ctx, chromedp.Location(&currentURL)); err != nil {
		return // Can't check — proceed with whatever target we have.
	}

	if currentURL != "" && currentURL != "about:blank" && !strings.HasPrefix(currentURL, "chrome://") {
		return // Already on a content page.
	}

	// List all targets and find a better one.
	targets, err := c.ListTargets(ctx)
	if err != nil {
		return
	}

	for _, t := range targets {
		if t.Type != "page" {
			continue
		}
		if t.URL == "" || t.URL == "about:blank" || strings.HasPrefix(t.URL, "chrome://") {
			continue
		}
		// Found a content page — switch to it.
		c.logger.Info("auto-switching from about:blank to content page",
			"url", t.URL, "title", t.Title, "targetId", t.ID)
		if _, _, err := c.SelectTarget(ctx, t.ID); err != nil {
			c.logger.Warn("auto-switch failed, staying on current target", "err", err)
		}
		return
	}

	c.logger.Warn("connected to about:blank — no content tabs found. Navigate to a URL or open a page in the browser.")
}

// Context returns the active chromedp context.
// If the connection was lost and a reconnect function is set, it attempts
// to reconnect automatically before returning an error.
func (c *Client) Context() (context.Context, error) {
	c.mu.RLock()
	if c.connected {
		ctx := c.ctx
		c.mu.RUnlock()
		return ctx, nil
	}
	fn := c.reconnectFn
	c.mu.RUnlock()

	if fn != nil {
		c.logger.Info("CDP connection lost, attempting automatic reconnection...")
		if err := fn(); err != nil {
			c.logger.Warn("automatic reconnection failed", "err", err)
			return nil, errors.New("CDP connection lost and reconnection failed - the browser may have been closed")
		}
		// Reconnect succeeded, return the new context.
		c.mu.RLock()
		defer c.mu.RUnlock()
		if c.connected {
			return c.ctx, nil
		}
	}

	return nil, errors.New("CDP not connected - start Chrome with --remote-debugging-port=9222")
}

// ContextWithTimeout returns the active chromedp context with a timeout.
// The caller must call the returned cancel function when done.
func (c *Client) ContextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc, error) {
	ctx, err := c.Context()
	if err != nil {
		return nil, func() {}, err
	}
	tCtx, cancel := context.WithTimeout(ctx, timeout)
	return tCtx, cancel, nil
}

// AllocContext returns the allocator context for creating new tab contexts.
func (c *Client) AllocContext() (context.Context, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.connected {
		return nil, errors.New("CDP not connected - start Chrome with --remote-debugging-port=9222")
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

	backoff := time.Second
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
	// Loop always returns before reaching here.
	return errors.New("reconnect: no attempts configured")
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

	return "", fmt.Errorf("no CDP endpoint found at %s (tried ports %v). "+
		"Ensure Chrome is running with: chrome --remote-debugging-port=9222 "+
		"and that all other Chrome instances are fully closed first", baseURL, ports)
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

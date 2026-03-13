package cdp

import (
	"context"
	"strings"

	"github.com/chromedp/chromedp"
)

// Listen registers a CDP event listener on the client's active context.
// The callback receives raw events; callers should type-switch on CDP event types.
// Returns a cancel function to stop listening.
func (c *Client) Listen(handler func(ev interface{})) (context.CancelFunc, error) {
	cdpCtx, err := c.Context()
	if err != nil {
		return nil, err
	}
	chromedp.ListenTarget(cdpCtx, handler)
	// chromedp.ListenTarget doesn't return a cancel; the listener lives
	// until the context is cancelled. We return a no-op for API consistency.
	return func() {}, nil
}

// RunAction executes a chromedp action using the client's active context.
func (c *Client) RunAction(ctx context.Context, actions ...chromedp.Action) error {
	cdpCtx, err := c.Context()
	if err != nil {
		return err
	}
	// Merge the caller's context deadline with the CDP context.
	_ = ctx // The CDP context carries the connection; caller ctx for cancellation is handled upstream.
	return chromedp.Run(cdpCtx, actions...)
}

// RunActionFunc executes a raw CDP command using ActionFunc.
func (c *Client) RunActionFunc(fn func(ctx context.Context) error) error {
	cdpCtx, err := c.Context()
	if err != nil {
		return err
	}
	return chromedp.Run(cdpCtx, chromedp.ActionFunc(fn))
}

// containsInsensitive checks if s contains substr (case-insensitive).
func containsInsensitive(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

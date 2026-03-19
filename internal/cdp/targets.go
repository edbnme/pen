package cdp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// TargetInfo holds information about a browser target (tab, worker, etc.).
type TargetInfo struct {
	ID    string
	Type  string
	Title string
	URL   string
}

// ListTargets returns all browser targets visible from the current connection.
func (c *Client) ListTargets(ctx context.Context) ([]TargetInfo, error) {
	cdpCtx, err := c.Context()
	if err != nil {
		return nil, err
	}

	targets, err := chromedp.Targets(cdpCtx)
	if err != nil {
		return nil, fmt.Errorf("list targets: %w", err)
	}

	result := make([]TargetInfo, 0, len(targets))
	for _, t := range targets {
		result = append(result, TargetInfo{
			ID:    string(t.TargetID),
			Type:  string(t.Type),
			Title: t.Title,
			URL:   t.URL,
		})
	}
	return result, nil
}

// SelectTarget switches the active CDP context to a specific target by ID.
// Returns a new context targeting that tab and a cancel function.
func (c *Client) SelectTarget(ctx context.Context, targetID string) (context.Context, context.CancelFunc, error) {
	allocCtx, err := c.AllocContext()
	if err != nil {
		return nil, nil, err
	}

	tid := target.ID(targetID)
	tabCtx, tabCancel := chromedp.NewContext(allocCtx, chromedp.WithTargetID(tid))

	// Verify the target is reachable.
	if err := chromedp.Run(tabCtx); err != nil {
		tabCancel()
		return nil, nil, fmt.Errorf("target %s unreachable: %w", targetID, err)
	}

	// Update the client's active context.
	c.mu.Lock()
	if c.ctxStop != nil {
		c.ctxStop()
	}
	c.ctx = tabCtx
	c.ctxStop = tabCancel
	c.connected = true
	c.mu.Unlock()

	// Monitor for unexpected disconnection of the new tab context.
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

	c.logger.Info("switched target", "id", targetID)
	return tabCtx, tabCancel, nil
}

// FindTargetByURL finds the first target whose URL contains the given substring.
func (c *Client) FindTargetByURL(ctx context.Context, urlPattern string) (*TargetInfo, error) {
	targets, err := c.ListTargets(ctx)
	if err != nil {
		return nil, err
	}

	for _, t := range targets {
		if containsInsensitive(t.URL, urlPattern) {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("no target matching URL pattern %q", urlPattern)
}

// CurrentTargetID returns the target ID of the active context, if available.
func (c *Client) CurrentTargetID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ctx == nil {
		return ""
	}
	if tgt := chromedp.FromContext(c.ctx); tgt != nil {
		return string(tgt.Target.TargetID)
	}
	return ""
}

// LogTargetSummary logs a summary of all visible targets.
func (c *Client) LogTargetSummary(ctx context.Context) {
	targets, err := c.ListTargets(ctx)
	if err != nil {
		c.logger.Warn("failed to list targets", "err", err)
		return
	}
	for _, t := range targets {
		c.logger.Info("target",
			slog.String("id", t.ID),
			slog.String("type", t.Type),
			slog.String("title", t.Title),
			slog.String("url", t.URL),
		)
	}
}

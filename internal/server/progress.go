package server

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NotifyProgress sends a progress notification if the client supports it.
// Safely handles nil progress tokens and nil sessions.
// Falls back to debug logging when the client doesn't provide a progress token.
func NotifyProgress(ctx context.Context, req *mcp.CallToolRequest, progress, total float64, msg string) {
	if req == nil {
		return
	}

	token := req.Params.GetProgressToken()
	if token == nil {
		pct := 0
		if total > 0 {
			pct = int(progress / total * 100)
		}
		slog.Debug("progress", "pct", pct, "msg", msg)
		return
	}

	session := req.Session
	if session == nil {
		pct := 0
		if total > 0 {
			pct = int(progress / total * 100)
		}
		slog.Debug("progress", "pct", pct, "msg", msg)
		return
	}

	_ = session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
		ProgressToken: token,
		Progress:      progress,
		Total:         total,
		Message:       msg,
	})
}

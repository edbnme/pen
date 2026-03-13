package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NotifyProgress sends a progress notification if the client supports it.
// Safely handles nil progress tokens and nil sessions.
func NotifyProgress(ctx context.Context, req *mcp.CallToolRequest, progress, total float64, msg string) {
	if req == nil {
		return
	}

	token := req.Params.GetProgressToken()
	if token == nil {
		return
	}

	session := req.Session
	if session == nil {
		return
	}

	_ = session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
		ProgressToken: token,
		Progress:      progress,
		Total:         total,
		Message:       msg,
	})
}

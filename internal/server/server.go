// Package server sets up and runs the MCP server for PEN.
package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/cdp"
)

// Config holds configuration for the PEN MCP server.
type Config struct {
	// Name and version for the MCP Implementation header.
	Name    string
	Version string

	// Transport mode: "stdio", "sse", or "http".
	Transport string

	// HTTPAddr is the bind address for HTTP/SSE transport (e.g., "localhost:6100").
	HTTPAddr string

	// AllowEval enables the pen_evaluate tool (security-sensitive).
	AllowEval bool

	// Logger for structured output.
	Logger *slog.Logger
}

// PEN is the top-level MCP server that coordinates CDP and tools.
type PEN struct {
	server *mcp.Server
	cdp    *cdp.Client
	locks  *OperationLock
	config *Config
	logger *slog.Logger
}

// New creates a new PEN MCP server with the given CDP client and config.
func New(cdpClient *cdp.Client, cfg *Config) *PEN {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	srv := mcp.NewServer(
		&mcp.Implementation{
			Name:    cfg.Name,
			Version: cfg.Version,
		},
		&mcp.ServerOptions{
			Logger:       cfg.Logger,
			Instructions: "PEN is an autonomous performance engineer for web applications. Use pen_ tools to profile, analyze, and debug frontend performance.",
		},
	)

	p := &PEN{
		server: srv,
		cdp:    cdpClient,
		locks:  NewOperationLock(),
		config: cfg,
		logger: cfg.Logger,
	}

	return p
}

// Server returns the underlying MCP server for tool registration.
func (p *PEN) Server() *mcp.Server {
	return p.server
}

// CDP returns the CDP client.
func (p *PEN) CDP() *cdp.Client {
	return p.cdp
}

// Locks returns the operation lock manager.
func (p *PEN) Locks() *OperationLock {
	return p.locks
}

// Run starts the MCP server with the configured transport.
// This blocks until the context is cancelled or the client disconnects.
func (p *PEN) Run(ctx context.Context) error {
	switch p.config.Transport {
	case "stdio", "":
		p.logger.Info("starting MCP server", "transport", "stdio")
		return p.server.Run(ctx, &mcp.StdioTransport{})
	case "sse":
		return p.runHTTP(ctx, "sse")
	case "http":
		return p.runHTTP(ctx, "streamable-http")
	default:
		return fmt.Errorf("unsupported transport: %q", p.config.Transport)
	}
}

// runHTTP starts the server with an HTTP-based transport (SSE or Streamable HTTP).
func (p *PEN) runHTTP(ctx context.Context, mode string) error {
	addr := p.config.HTTPAddr
	if addr == "" {
		addr = "localhost:6100"
	}
	p.logger.Info("starting MCP server", "transport", mode, "addr", addr)
	return fmt.Errorf("HTTP transport (%s) not yet implemented — use stdio", mode)
}

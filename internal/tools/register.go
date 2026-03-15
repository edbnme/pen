// Package tools implements all PEN MCP tool handlers.
// Each file covers one tool category; this file provides shared registration
// and the PEN context all handlers need.
package tools

import (
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/cdp"
	"github.com/edbnme/pen/internal/security"
	"github.com/edbnme/pen/internal/server"
)

// toolError returns a tool-level error that the MCP SDK will automatically
// pack into CallToolResult.Content with IsError set to true.
func toolError(msg string) (*mcp.CallToolResult, any, error) {
	return nil, nil, errors.New(msg)
}

// boolPtr returns a pointer to a bool value (used for optional ToolAnnotation hints).
func boolPtr(v bool) *bool { return &v }

// Deps bundles the dependencies every tool handler needs.
// Passed by the server during registration to avoid global state.
type Deps struct {
	CDP     *cdp.Client
	Locks   *server.OperationLock
	Limiter *security.RateLimiter
	Config  *ToolsConfig
}

// ToolsConfig holds tool-level configuration.
type ToolsConfig struct {
	AllowEval   bool   // Whether pen_evaluate is enabled.
	ProjectRoot string // For path traversal checks on source tools.
	Version     string // Server version for pen_status.
	CDPPort     int    // CDP debug port (for Lighthouse integration).
}

// RegisterAll registers every PEN tool category on the MCP server.
func RegisterAll(s *mcp.Server, deps *Deps) {
	registerAuditTools(s, deps)
	registerUtilityTools(s, deps)
	registerMemoryTools(s, deps)
	registerCPUTools(s, deps)
	registerNetworkTools(s, deps)
	registerCoverageTools(s, deps)
	registerSourceTools(s, deps)
	registerConsoleTools(s, deps)
	registerLighthouseTools(s, deps)
	registerStatusTool(s, deps)
}

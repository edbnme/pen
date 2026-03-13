# Part 5: MCP Server Design

## 5.1 Server Initialization

PEN uses the official **MCP Go SDK v1.2.0** (`github.com/modelcontextprotocol/go-sdk/mcp`). The SDK provides typed, generic tool registration via `mcp.AddTool[In, Out]`, automatic JSON Schema inference from Go struct types, and built-in transport implementations.

> **Verified API (v1.2.0)**: `mcp.NewServer(*Implementation, *ServerOptions)` creates a server. Tools are added via the package-level generic function `mcp.AddTool[In, Out]()`. The server is run via `srv.Run(ctx, &mcp.StdioTransport{})`. All types are in the single `mcp` package — there is no separate `server` subpackage.

```go
package server

import (
    "log/slog"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

func NewPenServer(cdpClient *cdp.Client) *mcp.Server {
    s := mcp.NewServer(
        &mcp.Implementation{
            Name:    "pen",
            Version: "0.1.0",
        },
        &mcp.ServerOptions{
            Logger:   slog.Default(),
            HasTools: true, // Advertise tools capability even before registration
        },
    )

    // Register all tool categories
    registerMemoryTools(s, cdpClient)
    registerCPUTools(s, cdpClient)
    registerNetworkTools(s, cdpClient)
    registerCoverageTools(s, cdpClient)
    registerAuditTools(s, cdpClient)
    registerSourceMapTools(s, cdpClient)
    registerUtilityTools(s, cdpClient)

    slog.Info("PEN MCP server initialized", "version", "0.1.0")

    return s
}
```

## 5.2 Tool Registration with Typed Generics

The MCP Go SDK v1.2.0 uses `mcp.AddTool[In, Out]()` — a **package-level generic function** that auto-infers the JSON Schema from Go struct types. Input schemas are derived from struct field types and `jsonschema` tags. This eliminates manual schema construction.

> **Verified API (v1.2.0)**: `mcp.AddTool[In, Out any](s *Server, t *Tool, h ToolHandlerFor[In, Out])`. The `In` type provides the input schema (auto-populated from struct fields). If `Out` is `any`, the output schema is omitted. The handler receives pre-parsed, validated input — no manual unmarshaling needed.

```go
package server

import (
    "context"
    "fmt"
    "os"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Input types: struct fields auto-generate JSON Schema ---

// HeapSnapshotInput defines the tool's input schema via struct tags.
// The `json` tag sets the property name; the `jsonschema` tag sets the description.
type HeapSnapshotInput struct {
    ForceGC    bool `json:"forceGC"    jsonschema:"Force garbage collection before snapshot"`
    IncludeDOM bool `json:"includeDOM" jsonschema:"Include DOM node details in analysis"`
    MaxDepth   int  `json:"maxDepth"   jsonschema:"Maximum depth for retained size analysis (1-10)"`
}

func registerMemoryTools(s *mcp.Server, cdp *cdp.Client) {
    // Register with typed generics — SDK auto-infers InputSchema from HeapSnapshotInput struct.
    // The Tool's InputSchema is populated automatically if nil.
    mcp.AddTool(s, &mcp.Tool{
        Name:        "pen_heap_snapshot",
        Description: "Take a V8 heap snapshot and analyze memory usage. Returns top retained objects, size statistics, and potential leak indicators. The snapshot is streamed to disk — safe to call on large heaps.",
    }, makeHeapSnapshotHandler(cdp))
}

// makeHeapSnapshotHandler returns a typed handler for the heap snapshot tool.
// Handler signature: func(ctx, *CallToolRequest, In) (*CallToolResult, Out, error)
//   - ctx: standard Go context (carries deadlines, cancellation)
//   - req: the raw MCP request (access session, progress token, params)
//   - input: pre-parsed, validated HeapSnapshotInput (SDK handles unmarshaling)
// Returns:
//   - *CallToolResult: structured MCP response (text content for LLM)
//   - Out (any): output value (nil when using CallToolResult directly)
//   - error: transport-level error only (app errors go in CallToolResult)
func makeHeapSnapshotHandler(cdp *cdp.Client) func(context.Context, *mcp.CallToolRequest, HeapSnapshotInput) (*mcp.CallToolResult, any, error) {
    return func(ctx context.Context, req *mcp.CallToolRequest, input HeapSnapshotInput) (*mcp.CallToolResult, any, error) {
        // Report progress to the MCP client
        notifyProgress(ctx, req, 0, "Starting heap snapshot...")

        // Capture (streamed to disk — see Part 4)
        result, err := profiling.CaptureHeapSnapshot(ctx, func(done, total int) {
            pct := float64(done) / float64(total) * 100
            notifyProgress(ctx, req, pct, fmt.Sprintf("Capturing snapshot... %d%%", int(pct)))
        })
        if err != nil {
            return mcp.NewToolResultError("heap snapshot failed: " + err.Error()), nil, nil
        }
        defer os.Remove(result.TempFile)

        // Analyze
        analysis, err := analysis.AnalyzeHeapSnapshot(result.TempFile, input.MaxDepth)
        if err != nil {
            return mcp.NewToolResultError("analysis failed: " + err.Error()), nil, nil
        }

        notifyProgress(ctx, req, 100, "Complete")

        // Return structured summary (never raw snapshot data)
        return &mcp.CallToolResult{
            Content: []mcp.Content{
                &mcp.TextContent{Text: analysis.FormatForLLM()},
            },
        }, nil, nil
    }
}
```

### Progress Notification API (Verified)

The MCP Go SDK exposes progress notifications through the `ServerSession` object, accessible via the `Session` field on `CallToolRequest` (which is `ServerRequest[*CallToolParams]`):

```go
// ServerRequest (verified v1.2.0):
//   type ServerRequest[P] struct {
//       Extra   *RequestExtra
//       Params  P
//       Session *ServerSession  // Direct struct field — NOT a method
//   }
//
// CallToolRequest = ServerRequest[*CallToolParams]
// So: req.Session is *ServerSession, req.Params is *CallToolParams

// Sending progress (verified signature):
req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
    ProgressToken: req.Params.GetProgressToken(), // Token provided by client in _meta
    Progress:      float64,                        // Current progress value
    Total:         float64,                        // Total expected (0 = unknown)
    Message:       string,                         // Optional human-readable message
})
```

**Important**: Progress notifications are only sent if: (1) `req.Session` is non-nil, and (2) the client provides a progress token in the request's `_meta` field (retrievable via `req.Params.GetProgressToken()`). PEN wraps this in a helper to avoid repetition:

```go
// notifyProgress safely sends a progress notification, silently skipping
// if the session or progress token is not available.
func notifyProgress(ctx context.Context, req *mcp.CallToolRequest, pct float64, msg string) {
    if req.Session == nil {
        return
    }
    token := req.Params.GetProgressToken()
    if token == nil {
        return
    }
    _ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
        ProgressToken: token,
        Progress:      pct,
        Total:         100,
        Message:       msg,
    })
}
```

## 5.3 Transport Options

PEN supports all three MCP transport mechanisms:

### stdio (Primary — for IDE integration)

```go
func runStdio(ctx context.Context, srv *mcp.Server) error {
    return srv.Run(ctx, &mcp.StdioTransport{})
}
```

This is the default for IDE integration. The MCP client (Cursor, VS Code Copilot, Claude Desktop) launches PEN as a subprocess and communicates via stdin/stdout.

### Streamable HTTP (for remote/team use)

```go
func runHTTP(ctx context.Context, srv *mcp.Server, addr string) error {
    handler := mcp.NewStreamableHTTPHandler(
        func(r *http.Request) *mcp.Server { return srv },
        nil, // *StreamableHTTPOptions — nil for defaults
    )

    httpSrv := &http.Server{
        Addr:    addr,
        Handler: handler,
    }
    return httpSrv.ListenAndServe()
}
```

### SSE (Legacy — for older MCP clients)

```go
func runSSE(ctx context.Context, srv *mcp.Server, addr string) error {
    handler := mcp.NewSSEHandler(
        func(r *http.Request) *mcp.Server { return srv },
        nil, // *SSEOptions — nil for defaults
    )

    httpSrv := &http.Server{
        Addr:    addr,
        Handler: handler,
    }
    return httpSrv.ListenAndServe()
}
```

> **Verified API (v1.2.0)**: Both `NewSSEHandler` and `NewStreamableHTTPHandler` take a `getServer func(*http.Request) *Server` function (returning the server per request) and an options pointer. They implement `http.Handler`.

## 5.4 Handler Pattern

All PEN tool handlers follow a consistent typed generic pattern:

```go
// --- Input type (auto-generates JSON Schema via struct tags) ---
type ToolInput struct {
    SomeParam string `json:"someParam" jsonschema:"Description of the parameter"`
    Depth     int    `json:"depth"     jsonschema:"Analysis depth (1-10)"`
}

// --- Handler factory (closure captures CDP client) ---
func makeToolHandler(cdp *cdp.Client) func(context.Context, *mcp.CallToolRequest, ToolInput) (*mcp.CallToolResult, any, error) {
    return func(ctx context.Context, req *mcp.CallToolRequest, input ToolInput) (*mcp.CallToolResult, any, error) {
        // 1. Input is already parsed and validated by the SDK
        //    (no manual UnmarshalJSON needed)

        // 2. Ensure required CDP domains are enabled
        if err := cdp.Domains().EnsureEnabled("HeapProfiler"); err != nil {
            return mcp.NewToolResultError("CDP domain error: " + err.Error()), nil, nil
        }

        // 3. Report initial progress
        notifyProgress(ctx, req, 0, "Starting...")

        // 4. Execute CDP operations
        rawResult, err := doTheThing(ctx, cdp, input)
        if err != nil {
            return mcp.NewToolResultError(err.Error()), nil, nil
        }

        // 5. Analyze and format for LLM consumption
        summary := analyze(rawResult)

        // 6. Report completion
        notifyProgress(ctx, req, 100, "Complete")

        // 7. Return structured text (never raw binary)
        return &mcp.CallToolResult{
            Content: []mcp.Content{
                &mcp.TextContent{Text: summary},
            },
        }, nil, nil
    }
}

// --- Registration ---
// mcp.AddTool(server, &mcp.Tool{Name: "pen_tool_name", Description: "..."}, makeToolHandler(cdp))
```

### Error Handling Convention

MCP tools return **application errors** via `mcp.NewToolResultError()` in the `*CallToolResult` (sets `isError: true`). The Go `error` return value (third return) is reserved for **transport-level failures**. The second return value (`Out`, typed as `any` in PEN) is `nil` when using `CallToolResult` directly:

```go
// Application error (bad input, CDP failure, etc.) — client sees error in tool result
return mcp.NewToolResultError("heap snapshot failed: browser disconnected"), nil, nil

// Transport error (should never happen in normal operation) — connection fails
return nil, nil, fmt.Errorf("fatal: %w", err)
```

## 5.5 Concurrency Model

MCP clients may call multiple tools simultaneously. PEN handles this:

```go
type OperationLock struct {
    mu       sync.Mutex
    active   map[string]bool  // domain → is-in-use
}

// AcquireExclusive acquires exclusive access to a CDP domain.
// Some CDP operations (like Tracing) can only have one active session.
func (ol *OperationLock) AcquireExclusive(domain string) (release func(), err error) {
    ol.mu.Lock()
    defer ol.mu.Unlock()

    if ol.active[domain] {
        return nil, fmt.Errorf("%s is already in use by another operation", domain)
    }

    ol.active[domain] = true
    return func() {
        ol.mu.Lock()
        delete(ol.active, domain)
        ol.mu.Unlock()
    }, nil
}
```

**Exclusive domains**: Tracing (one trace at a time), HeapProfiler.takeHeapSnapshot (one snapshot at a time)
**Concurrent domains**: Network, Performance, Runtime, CSS, Debugger (multiple tools can read simultaneously)

## 5.6 Server Capabilities Declaration

Per the MCP specification, the server declares its capabilities during initialization. The SDK auto-advertises capabilities based on registered features (tools, resources, prompts). Setting `HasTools: true` in `ServerOptions` ensures tools are advertised even if registered after initialization:

```json
{
  "capabilities": {
    "tools": {
      "listChanged": true
    },
    "logging": {}
  },
  "serverInfo": {
    "name": "pen",
    "version": "0.1.0"
  }
}
```

- **`tools.listChanged: true`**: PEN can notify clients when available tools change (e.g., some tools become unavailable during disconnection)
- **`logging`**: PEN sends structured log messages via `ServerSession.Log()`
- **Resources**: Not exposed in v0.1.0. Planned for v0.2.0 (live metrics feed). Set `HasResources: true` in `ServerOptions` when resource implementations are ready.

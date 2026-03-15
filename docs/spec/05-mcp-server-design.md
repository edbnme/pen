# Part 5: MCP Server Design

## Server Initialization

PEN creates the MCP server with an implementation header and server options:

```go
srv := mcp.NewServer(
    &mcp.Implementation{Name: "pen", Version: version},
    &mcp.ServerOptions{
        Logger:       logger,
        Instructions: "PEN is an autonomous performance engineer for web applications. Use pen_ tools to profile, analyze, and debug frontend performance.",
        KeepAlive:    30 * time.Second,
        InitializedHandler: func(ctx context.Context, _ *mcp.InitializedRequest) {
            logger.Info("MCP client connected and initialized")
        },
    },
)
```

The `Instructions` field tells the LLM what PEN does and how to use it. `KeepAlive` sends periodic pings over the MCP connection to detect stale sessions.

## Tool Registration

All 30 tools are registered at startup via `tools.RegisterAll`:

```go
tools.RegisterAll(pen.Server(), &tools.Deps{
    CDP:     cdpClient,
    Locks:   pen.Locks(),
    Limiter: security.NewRateLimiter(security.DefaultCooldowns),
    Config:  &tools.ToolsConfig{
        AllowEval:   *allowEval,
        ProjectRoot: *projectRoot,
        Version:     version,
    },
})
```

The `Deps` struct bundles everything tool handlers need — no global state. Each category registers its tools in a separate function (`registerMemoryTools`, `registerCPUTools`, etc.).

### Typed Generic Handlers

The MCP Go SDK uses Go generics for type-safe tool handlers:

```go
mcp.AddTool[InputType, any](server, toolDefinition, handlerFunc)
```

The SDK auto-generates `inputSchema` from the Go struct's `jsonschema` tags. Handler functions receive the unmarshaled input directly — no manual JSON parsing.

## Transports

PEN supports three MCP transports:

| Transport | Flag                          | Use Case                            |
| --------- | ----------------------------- | ----------------------------------- |
| stdio     | `--transport stdio` (default) | IDE spawns PEN as child process     |
| SSE       | `--transport sse`             | Browser-based or remote clients     |
| HTTP      | `--transport http`            | Streamable HTTP (stateful sessions) |

Both `sse` and `http` use `mcp.NewStreamableHTTPHandler` internally, mounted at `/mcp`. Default bind: `localhost:6100`.

## Error Handling

Tool errors return through the SDK's error mechanism:

```go
func toolError(msg string) (*mcp.CallToolResult, any, error) {
    return nil, nil, errors.New(msg)
}
```

The SDK sets `isError: true` on the response automatically. Error messages are written for LLM consumption — they explain what went wrong and suggest next steps.

## Concurrency

### OperationLock

Domain-exclusive locking prevents conflicting CDP operations:

```go
release, err := deps.Locks.Acquire("HeapProfiler")
if err != nil {
    return toolError("HeapProfiler is already in use by another operation")
}
defer release()
```

### Rate Limiting

Heavy tools have cooldowns enforced before execution:

```go
if err := deps.Limiter.Check("pen_heap_snapshot"); err != nil {
    return toolError(err.Error()) // "pen_heap_snapshot has a 10s cooldown. Try again in 6s"
}
```

### Context Cancellation

Every handler respects `ctx.Done()`. If the MCP client disconnects mid-operation, CDP calls are cancelled, temp files are cleaned via `defer`, and domain locks are released.

## Capabilities

PEN declares standard MCP server capabilities during the `initialize` handshake:

- **Tools**: Full `tools/list` and `tools/call` support
- **Progress**: Sends `notifications/progress` for long-running operations
- **No resources or prompts**: PEN is tools-only — no MCP resources or prompt templates

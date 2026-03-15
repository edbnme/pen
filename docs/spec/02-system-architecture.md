# Part 2: System Architecture

## Component Overview

```
┌─────────────────────────────────────────────────────────┐
│ IDE / LLM Client                                        │
│  (Cursor, VS Code, Claude Desktop, etc.)                │
└──────────────────────┬──────────────────────────────────┘
                       │ MCP (stdio or HTTP)
┌──────────────────────▼──────────────────────────────────┐
│ PEN Server (cmd/pen)                                    │
│                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────┐ │
│  │ MCP Server   │  │ Tool Handlers│  │ Security      │ │
│  │ (server/)    │  │ (tools/)     │  │ (security/)   │ │
│  └──────┬───────┘  └──────┬───────┘  └───────────────┘ │
│         │                 │                             │
│  ┌──────▼─────────────────▼───────┐  ┌───────────────┐ │
│  │ CDP Client (cdp/)              │  │ Format        │ │
│  └──────────────┬─────────────────┘  └───────────────┘ │
└─────────────────┼───────────────────────────────────────┘
                  │ CDP (WebSocket)
┌─────────────────▼───────────────────────────────────────┐
│ Chrome / Chromium (--remote-debugging-port=9222)        │
└─────────────────────────────────────────────────────────┘
```

## Package Map

```
cmd/pen/main.go         Entry point. Flag parsing, signal handling, wiring.
internal/
  cdp/
    client.go           CDP connection lifecycle (Connect, Reconnect, Close)
    listener.go         Event listener registration (ListenTarget wrapper)
    targets.go          Tab listing and switching (ListTargets, SelectTarget)
  server/
    server.go           MCP server creation and transport handling (stdio/sse/http)
    lock.go             Domain-exclusive locking (OperationLock)
    progress.go         MCP progress notification helper
  tools/
    register.go         Tool registration entry point (RegisterAll + Deps)
    audit.go            pen_performance_metrics, pen_web_vitals, pen_accessibility_check
    memory.go           pen_heap_snapshot, pen_heap_diff, pen_heap_track, pen_heap_sampling
    cpu.go              pen_cpu_profile, pen_capture_trace, pen_trace_insights
    network.go          pen_network_enable, pen_network_waterfall, pen_network_request, pen_network_blocking
    coverage.go         pen_js_coverage, pen_css_coverage
    source.go           pen_list_sources, pen_source_content, pen_search_source
    console.go          pen_console_enable, pen_console_messages
    lighthouse.go       pen_lighthouse
    utility.go          pen_list_pages, pen_select_page, pen_collect_garbage, pen_screenshot, pen_emulate, pen_navigate, pen_evaluate
    status.go           pen_status
  format/
    output.go           Markdown table builder and formatting helpers
  security/
    validate.go         Expression filtering, path traversal checks, CDP URL validation, temp file creation
    ratelimit.go        Per-tool cooldown enforcement
```

## Data Flow

A typical tool call flows through these layers:

```
1. LLM calls pen_heap_snapshot via MCP
2. server/ receives the CallToolRequest, dispatches to tool handler
3. tools/memory.go checks rate limit, acquires OperationLock("HeapProfiler")
4. cdp/client.go provides chromedp context
5. Handler enables HeapProfiler domain, registers event listeners
6. CDP streams heap chunks → handler writes to temp file (security/validate.go creates it)
7. Handler builds structured analysis, formats via format/output.go
8. Returns CallToolResult with Markdown text → flows back to LLM
```

## Dependencies

| Dependency                               | Version | Purpose                          |
| ---------------------------------------- | ------- | -------------------------------- |
| `github.com/modelcontextprotocol/go-sdk` | v1.3.1  | MCP server, transports, types    |
| `github.com/chromedp/chromedp`           | v0.13.6 | CDP connection and actions       |
| `github.com/chromedp/cdproto`            | pinned  | Auto-generated CDP type bindings |

No other runtime dependencies. Standard library only beyond these three.

## Build

```bash
go build -ldflags "-s -w -X main.version=v1.0.0" -o pen ./cmd/pen
```

Cross-compiled via GoReleaser for linux/darwin/windows on amd64 and arm64 (excludes windows/arm64).

## Concurrency Model

- **OperationLock**: Domain-exclusive mutex. Tools that use conflicting CDP domains (e.g., two simultaneous trace captures) are blocked at the lock level with a clear error.
- **RateLimiter**: Per-tool cooldowns prevent resource exhaustion. Heavy tools (`pen_heap_snapshot`: 10s, `pen_capture_trace`: 5s, `pen_collect_garbage`: 5s) enforce minimum intervals.
- **Context propagation**: All handlers receive `context.Context` from MCP. Client disconnect → context cancellation → CDP operations abort cleanly, temp files cleaned via `defer`.

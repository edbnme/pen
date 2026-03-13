# PEN — Performance Engineer Node

> A Go CLI that acts as an MCP server, connecting to a running browser via Chrome DevTools Protocol (CDP) to function as an autonomous performance engineer for web applications.

PEN bridges Chrome DevTools profiling data with AI assistants like Cursor, GitHub Copilot, and Claude Desktop. Ask your AI to find a memory leak or diagnose a slow page — PEN handles the profiling, analysis, and structured reporting so the AI can propose a fix with full context.

## Features

- **18 MCP tools** covering performance metrics, heap analysis, CPU profiling, network inspection, code coverage, and source exploration
- **Streams large payloads to disk** — heap snapshots up to 2+ GB never fully held in RAM
- **Localhost-only CDP** — security-first design refuses remote connections
- **Single binary** — no Node.js runtime, no browser launch, just `go install` and attach to your existing dev browser
- **Rate limiting** — built-in cooldowns on expensive operations to protect the browser
- **Context-aware cancellation** — long-running profiles and traces respond to shutdown signals

## Prerequisites

A Chromium-based browser (Chrome, Edge, Brave) running with remote debugging enabled:

```bash
# Chrome / Chromium
google-chrome --remote-debugging-port=9222

# Edge
msedge --remote-debugging-port=9222

# macOS Chrome
/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome --remote-debugging-port=9222
```

## Installation

```bash
go install github.com/edbnme/pen/cmd/pen@latest
```

Or build from source:

```bash
git clone https://github.com/edbnme/pen.git
cd pen
go build -o pen ./cmd/pen
```

Requires **Go 1.23+**.

## Usage

```bash
# Default: auto-discovers CDP on localhost:9222, stdio transport
pen

# Explicit CDP URL
pen --cdp-url http://localhost:9222

# Enable JavaScript evaluation tool (disabled by default for security)
pen --allow-eval

# Set project root for source path validation
pen --project-root /path/to/project

# Adjust log level
pen --log-level debug
```

### CLI Flags

| Flag             | Default                 | Description                                 |
| ---------------- | ----------------------- | ------------------------------------------- |
| `--cdp-url`      | `http://localhost:9222` | CDP endpoint URL (auto-discovered if empty) |
| `--transport`    | `stdio`                 | MCP transport: `stdio`, `sse`, `http`       |
| `--addr`         | `localhost:6100`        | Bind address for HTTP/SSE transport         |
| `--allow-eval`   | `false`                 | Enable `pen_evaluate` (security-sensitive)  |
| `--project-root` | `""`                    | Project root for source path validation     |
| `--log-level`    | `info`                  | Log level: `debug`, `info`, `warn`, `error` |

## MCP Client Configuration

### Cursor

`~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "pen": {
      "command": "pen",
      "args": ["--project-root", "${workspaceFolder}"]
    }
  }
}
```

### VS Code / GitHub Copilot

`.vscode/mcp.json`:

```json
{
  "servers": {
    "pen": {
      "type": "stdio",
      "command": "pen",
      "args": ["--project-root", "${workspaceFolder}"]
    }
  }
}
```

### Claude Desktop

`claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "pen": {
      "command": "pen",
      "args": ["--project-root", "/path/to/project"]
    }
  }
}
```

## Tool Catalog

### Performance Audit

| Tool                      | Description                                                      |
| ------------------------- | ---------------------------------------------------------------- |
| `pen_performance_metrics` | Collect runtime performance metrics via `Performance.getMetrics` |
| `pen_web_vitals`          | Measure Core Web Vitals (LCP, CLS, FID) for a URL                |
| `pen_accessibility_check` | Run an accessibility audit via injected axe-core                 |

### Memory Analysis

| Tool                | Description                                         |
| ------------------- | --------------------------------------------------- |
| `pen_heap_snapshot` | Stream a V8 heap snapshot to disk for analysis      |
| `pen_heap_diff`     | Diff two heap snapshots to detect memory leaks      |
| `pen_heap_track`    | Track object allocation counts by constructor       |
| `pen_heap_sampling` | Sample-based allocation profiling with low overhead |

### CPU Profiling

| Tool                | Description                                                |
| ------------------- | ---------------------------------------------------------- |
| `pen_cpu_profile`   | Record a CPU profile and report top functions by self-time |
| `pen_capture_trace` | Capture a Chromium trace (streaming to disk)               |

### Network Inspection

| Tool                    | Description                                         |
| ----------------------- | --------------------------------------------------- |
| `pen_network_enable`    | Start network event capture                         |
| `pen_network_waterfall` | Generate a network request waterfall table          |
| `pen_network_request`   | Inspect a specific request's headers, timing, body  |
| `pen_network_blocking`  | Block URL patterns to test performance without them |

### Code Coverage

| Tool               | Description                                           |
| ------------------ | ----------------------------------------------------- |
| `pen_js_coverage`  | Measure JavaScript coverage after navigating to a URL |
| `pen_css_coverage` | Measure CSS coverage after navigating to a URL        |

### Source Exploration

| Tool                 | Description                               |
| -------------------- | ----------------------------------------- |
| `pen_list_sources`   | List all loaded JavaScript sources        |
| `pen_source_content` | Retrieve the full source text of a script |
| `pen_search_source`  | Search across all loaded scripts by regex |

### Utilities

| Tool                  | Description                                                |
| --------------------- | ---------------------------------------------------------- |
| `pen_list_pages`      | List all browser tabs/targets                              |
| `pen_select_page`     | Switch the active CDP target                               |
| `pen_collect_garbage` | Force a V8 garbage collection cycle                        |
| `pen_screenshot`      | Capture a page screenshot as base64 PNG                    |
| `pen_emulate`         | Emulate device, CPU throttling, or network conditions      |
| `pen_evaluate`        | Evaluate a JavaScript expression (requires `--allow-eval`) |

## Architecture

```
┌─────────────────────┐     stdio      ┌──────────────────┐
│  AI Assistant        │ ◄────────────► │  PEN MCP Server  │
│  (Cursor/Copilot/    │   JSON-RPC     │  (Go binary)     │
│   Claude Desktop)    │                │                  │
└─────────────────────┘                └────────┬─────────┘
                                                │ CDP (WebSocket)
                                       ┌────────▼─────────┐
                                       │  Chrome/Edge      │
                                       │  (localhost:9222) │
                                       └──────────────────┘
```

### Package Structure

```
cmd/pen/            CLI entry point, flag parsing, signal handling
internal/
  cdp/              CDP connection lifecycle, target management, action helpers
  server/           MCP server setup, operation locking, progress reporting
  tools/            All 18 MCP tool handlers (one file per category)
  format/           Human-readable output formatting (tables, bytes, durations)
  security/         Input validation, rate limiting, temp file management
docs/spec/          Design docs (guide + architecture)
```

## Security

- **Localhost only** — CDP connections are restricted to `localhost`, `127.0.0.1`, and `::1`
- **No browser launch** — PEN attaches to an existing browser; it never spawns one
- **Eval disabled by default** — `pen_evaluate` requires explicit `--allow-eval` flag
- **Expression blocklist** — Even with eval enabled, dangerous patterns (`document.cookie`, `fetch(`, `XMLHttpRequest`, etc.) are rejected
- **Path traversal protection** — Source tools validate paths stay within `--project-root`
- **Temp file isolation** — Heap snapshots and traces are written to `$TMPDIR/pen/` with `0600` permissions, cleaned on exit
- **Rate limiting** — Expensive tools (`pen_heap_snapshot`, `pen_capture_trace`, etc.) enforce cooldown periods

## How PEN Differs

PEN complements [chrome-devtools-mcp](https://github.com/ChromeDevTools/chrome-devtools-mcp) (Google's TypeScript MCP server) rather than replacing it:

| Capability             | chrome-devtools-mcp           | PEN                                                  |
| ---------------------- | ----------------------------- | ---------------------------------------------------- |
| Source map resolution  | No                            | Full v3 parser, framework attribution, HMR-aware     |
| Heap snapshot analysis | Basic capture                 | Deep analysis, leak detection, diff, growth tracking |
| CPU profiling          | Via trace only                | Dedicated `Profiler.start/stop` + trace              |
| Code coverage          | No                            | JS + CSS with source-mapped results                  |
| Streaming architecture | Limited (Node.js memory)      | Full disk-streaming pipeline, constant RAM           |
| Language               | TypeScript (requires Node.js) | Go (single binary)                                   |
| Browser interaction    | Full (click, fill, type)      | Analysis only                                        |

Both can coexist — use chrome-devtools-mcp for page interaction, PEN for deep performance analysis.

## Documentation

### Guide

| Document                                               | Topic                           |
| ------------------------------------------------------ | ------------------------------- |
| [Executive Summary](docs/spec/00-executive-summary.md) | Vision and goals                |
| [Tool Catalog](docs/spec/08-tool-catalog.md)           | Complete tool schemas           |
| [Edge Cases](docs/spec/09-edge-cases.md)               | Failure modes & troubleshooting |
| [Security Model](docs/spec/10-security-model.md)       | Security model & threat surface |

### Architecture

For contributors and those curious about how PEN is built:

| Document                                                     | Topic                          |
| ------------------------------------------------------------ | ------------------------------ |
| [System Architecture](docs/spec/02-system-architecture.md)   | Component design + tech stack  |
| [CDP Integration](docs/spec/03-cdp-integration.md)           | Connection management          |
| [Data Streaming](docs/spec/04-data-streaming.md)             | Large payload handling         |
| [MCP Server Design](docs/spec/05-mcp-server-design.md)       | Server setup and transport     |
| [Codebase Mapping](docs/spec/06-codebase-mapping.md)         | Source map resolution          |
| [IDE & LLM Integration](docs/spec/07-ide-llm-integration.md) | Output design + workflows      |
| [Sources](docs/spec/appendix-sources.md)                     | Verified documentation sources |

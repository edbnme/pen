# PEN

MCP server that connects AI assistants to Chrome DevTools. Ask your AI to profile a page, find a memory leak, or measure coverage — PEN runs the browser profiling and returns structured results.

Single Go binary. No Node.js. No browser launch. Attach to your dev browser and go.

## Install

```bash
brew install edbnme/tap/pen              # macOS / Linux
```

```powershell
scoop bucket add pen https://github.com/edbnme/scoop-pen
scoop install pen                        # Windows
```

Or grab a binary from [Releases](https://github.com/edbnme/pen/releases/latest), or `go install github.com/edbnme/pen/cmd/pen@latest` (needs Go 1.23+).

Full setup: [docs/INSTALL.md](docs/INSTALL.md)

## Quick Start

```bash
# 1. Launch browser with remote debugging
google-chrome --remote-debugging-port=9222

# 2. Add to your IDE (see below) or run directly
pen
```

### IDE Config

**VS Code** — `.vscode/mcp.json`:

```json
{
  "servers": {
    "pen": {
      "command": "pen",
      "args": ["--project-root", "${workspaceFolder}"]
    }
  }
}
```

**Cursor** — `~/.cursor/mcp.json`:

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

**Claude Desktop** — [config path](docs/INSTALL.md#claude-desktop):

```json
{
  "mcpServers": {
    "pen": {
      "command": "pen",
      "args": ["--project-root", "/absolute/path/to/project"]
    }
  }
}
```

## Flags

| Flag             | Default                 | Purpose                                    |
| ---------------- | ----------------------- | ------------------------------------------ |
| `--cdp-url`      | `http://localhost:9222` | CDP endpoint                               |
| `--transport`    | `stdio`                 | `stdio`, `http`, or `sse`                  |
| `--addr`         | `localhost:6100`        | Bind address for HTTP/SSE                  |
| `--allow-eval`   | `false`                 | Enable `pen_evaluate` (runs JS in browser) |
| `--project-root` | `.`                     | Sandbox for source file paths              |
| `--log-level`    | `info`                  | `debug` / `info` / `warn` / `error`        |
| `--version`      | —                       | Print version and exit                     |

## Tools

25 tools across 7 categories:

| Category        | Tools                                                                                                                           |
| --------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| **Performance** | `pen_performance_metrics` · `pen_web_vitals` · `pen_accessibility_check`                                                        |
| **Memory**      | `pen_heap_snapshot` · `pen_heap_diff` · `pen_heap_track` · `pen_heap_sampling`                                                  |
| **CPU**         | `pen_cpu_profile` · `pen_capture_trace`                                                                                         |
| **Network**     | `pen_network_enable` · `pen_network_waterfall` · `pen_network_request` · `pen_network_blocking`                                 |
| **Coverage**    | `pen_js_coverage` · `pen_css_coverage`                                                                                          |
| **Source**      | `pen_list_sources` · `pen_source_content` · `pen_search_source`                                                                 |
| **Utility**     | `pen_status` · `pen_list_pages` · `pen_select_page` · `pen_collect_garbage` · `pen_screenshot` · `pen_emulate` · `pen_evaluate` |

Full schemas: [docs/spec/08-tool-catalog.md](docs/spec/08-tool-catalog.md)

## Architecture

```
AI Assistant ◄── stdio/JSON-RPC ──► PEN (Go) ── CDP/WebSocket ──► Chrome (localhost:9222)
```

```
cmd/pen/          Entry point, flags, signals
internal/
  cdp/            CDP connection, target management
  server/         MCP server, locking, progress
  tools/          Tool handlers (one file per category)
  format/         Output formatting
  security/       Validation, rate limiting, temp files
```

## Security

- **Localhost only** — rejects remote CDP URLs
- **No browser launch** — attaches to existing browser
- **Eval gated** — `pen_evaluate` needs `--allow-eval`
- **Expression blocklist** — blocks `fetch`, `document.cookie`, `eval`, etc. even with eval on
- **Path sandboxing** — source tools can't escape `--project-root`
- **Temp isolation** — snapshots/traces go to `$TMPDIR/pen/` with `0600` perms
- **Rate limiting** — cooldowns on heap snapshots, traces, and other heavy ops

## Docs

| Doc                                                        | What's in it                                   |
| ---------------------------------------------------------- | ---------------------------------------------- |
| [Getting Started](docs/INSTALL.md)                         | Install, browser setup, IDE config             |
| [Running PEN](docs/RUNNING.md)                             | Usage, Docker, server deploys, troubleshooting |
| [Tool Catalog](docs/spec/08-tool-catalog.md)               | Every tool's params and output                 |
| [Security Model](docs/spec/10-security-model.md)           | Threat model, defenses                         |
| [System Architecture](docs/spec/02-system-architecture.md) | Design and tech stack                          |

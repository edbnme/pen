# PEN

MCP server that connects AI assistants to Chrome DevTools. Ask your AI to profile a page, find a memory leak, or measure coverage — PEN runs the browser profiling and returns structured results.

Single Go binary. No Node.js. No browser launch. Attach to your dev browser and go.

## Install

**Quick install (recommended):**

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/edbnme/pen/main/install.sh | sh

# Windows (PowerShell)
irm https://raw.githubusercontent.com/edbnme/pen/main/install.ps1 | iex
```

Then run the interactive setup wizard:

```bash
pen init
```

**Package managers:**

```bash
# macOS / Linux
brew install edbnme/tap/pen

# Windows
scoop bucket add pen https://github.com/edbnme/scoop-pen
scoop install pen
```

**Other options:**

```bash
# Download binary from GitHub Releases (all platforms)
# → https://github.com/edbnme/pen/releases/latest

# Or install with Go 1.23+
go install github.com/edbnme/pen/cmd/pen@latest
```

Verify it works:

```bash
pen --version
```

More install methods (from source, manual binary setup): [docs/INSTALL.md](docs/INSTALL.md)

## Quick Start

The fastest way to get going is the interactive setup wizard:

```bash
pen init
```

This auto-detects your environment, lets you pick your IDE and browser, generates the MCP config, and optionally launches your browser with debugging — all in one command.

### Manual setup

If you prefer to configure things by hand:

> **Important:** Quit the browser completely first — all windows and background processes. The debug port only works if Chrome starts fresh with the flag.

**macOS:**

```bash
open -a "Google Chrome" --args --remote-debugging-port=9222
```

**Windows (PowerShell):**

```powershell
& "C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=9222
```

<details>
<summary>Using Edge instead?</summary>

```powershell
& "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --remote-debugging-port=9222
```

</details>

**Linux:**

```bash
google-chrome --remote-debugging-port=9222
```

Verify the debug port is open — visit http://localhost:9222/json in a new tab. You should see a JSON array. If it doesn't load, the browser wasn't fully closed before relaunch.

### 2. Add PEN to your IDE

**VS Code** — create `.vscode/mcp.json` in your project:

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

**Cursor** — `.cursor/mcp.json` in your project (or `~/.cursor/mcp.json` for global):

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

### 3. Use it

Open a page in the debug browser, then ask your AI:

> _"Check the performance metrics of this page"_

PEN connects to the browser, runs the profiling, and returns structured results.

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

30 tools across 9 categories:

| Category        | Tools                                                                                                                                            |
| --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Performance** | `pen_performance_metrics` · `pen_web_vitals` · `pen_accessibility_check`                                                                         |
| **Memory**      | `pen_heap_snapshot` · `pen_heap_diff` · `pen_heap_track` · `pen_heap_sampling`                                                                   |
| **CPU**         | `pen_cpu_profile` · `pen_capture_trace` · `pen_trace_insights`                                                                                   |
| **Network**     | `pen_network_enable` · `pen_network_waterfall` · `pen_network_request` · `pen_network_blocking`                                                  |
| **Coverage**    | `pen_js_coverage` · `pen_css_coverage`                                                                                                           |
| **Source**      | `pen_list_sources` · `pen_source_content` · `pen_search_source`                                                                                  |
| **Console**     | `pen_console_enable` · `pen_console_messages`                                                                                                    |
| **Lighthouse**  | `pen_lighthouse`                                                                                                                                 |
| **Utility**     | `pen_status` · `pen_list_pages` · `pen_select_page` · `pen_navigate` · `pen_collect_garbage` · `pen_screenshot` · `pen_emulate` · `pen_evaluate` |

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

New in this release: `console.go` (real-time console capture via Runtime CDP domain), `lighthouse.go` (Lighthouse CLI integration), `pen_navigate` in `utility.go` (browser navigation control), and `pen_trace_insights` in `cpu.go` (offline trace analysis engine).

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

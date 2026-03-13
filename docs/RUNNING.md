# Running PEN

This document covers how to build and run PEN both **locally** (for IDE integration) and **on a remote server** (for CI or shared dev environments).

---

## Quick start (local)

```bash
# 1. Clone and build
git clone https://github.com/edbnme/pen.git
cd pen
go build -o pen ./cmd/pen        # produces ./pen  (pen.exe on Windows)

# 2. Launch Chrome with remote-debugging enabled  ← required every time
google-chrome --remote-debugging-port=9222

# 3. Run PEN (stdio transport — the default)
./pen
# or explicitly:
./pen --cdp-url http://localhost:9222 --log-level debug
```

That's it. PEN prints `PEN ready` and waits for an MCP client to connect via stdin/stdout.

---

## Prerequisites

| Requirement                             | Notes                              |
| --------------------------------------- | ---------------------------------- |
| **Go 1.23+**                            | `go version` to verify             |
| **Chromium-based browser**              | Chrome, Edge, or Brave             |
| Remote debugging enabled on the browser | See "Launch browser" section below |

---

## Launch the browser with remote debugging

Choose the command for your OS / browser:

```bash
# Linux — Chrome
google-chrome --remote-debugging-port=9222 --no-first-run --no-default-browser-check

# Linux — Chromium
chromium-browser --remote-debugging-port=9222

# macOS — Chrome
/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome --remote-debugging-port=9222

# macOS — Edge
/Applications/Microsoft\ Edge.app/Contents/MacOS/Microsoft\ Edge --remote-debugging-port=9222

# Windows — Chrome (PowerShell)
& "C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=9222

# Windows — Edge
& "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --remote-debugging-port=9222
```

> **Verify it's working:** open `http://localhost:9222/json` in a browser tab — you should see a JSON array of open pages.

---

## Build from source

```bash
git clone https://github.com/edbnme/pen.git
cd pen
go build -o pen ./cmd/pen    # ./pen on Linux/macOS, pen.exe on Windows

# Or install to $GOPATH/bin so it's on your PATH:
go install ./cmd/pen
```

Requires **Go 1.23+**. Run `go version` if unsure.

---

## Running locally

PEN uses **stdio transport** by default, which means an MCP client (Cursor, VS Code/Copilot, Claude Desktop) spawns `pen` as a child process and communicates over stdin/stdout. You never need to run it manually in normal use — just configure your IDE client (see below) and it launches automatically.

### Run manually for testing / debugging

```bash
# Basic run — discovers Chrome on localhost:9222
./pen

# Enable the JavaScript eval tool (disabled by default for security)
./pen --allow-eval

# Set project root for source-path sandboxing
./pen --project-root /path/to/my/project

# Verbose output to stderr
./pen --log-level debug

# All flags
./pen \
  --cdp-url http://localhost:9222 \
  --transport stdio \
  --allow-eval \
  --project-root /path/to/project \
  --log-level debug
```

### CLI flag reference

| Flag             | Default                 | Description                                   |
| ---------------- | ----------------------- | --------------------------------------------- |
| `--cdp-url`      | `http://localhost:9222` | CDP endpoint (auto-resolved if omitted)       |
| `--transport`    | `stdio`                 | MCP transport: `stdio` only (see note below)  |
| `--addr`         | `localhost:6100`        | Bind address for future HTTP/SSE transport    |
| `--allow-eval`   | `false`                 | Enable `pen_evaluate` (executes arbitrary JS) |
| `--project-root` | `""`                    | Sandbox source-tool paths to this directory   |
| `--log-level`    | `info`                  | `debug` / `info` / `warn` / `error`           |

> **Transport note:** `sse` and `http` transports are stubbed and return "not implemented". Only `stdio` is production-ready.

---

## Connecting your IDE / AI assistant

### VS Code + GitHub Copilot

Create or edit `.vscode/mcp.json` in your project:

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

If `pen` is not on your `PATH`, use the absolute path to the binary, e.g. `"command": "/home/user/go/bin/pen"`.

### Cursor

Edit `~/.cursor/mcp.json`:

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

### Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "pen": {
      "command": "pen",
      "args": ["--project-root", "/absolute/path/to/your/project"]
    }
  }
}
```

---

## Running on a server

> **Context:** PEN connects to a locally-running browser via CDP. CDP connections are intentionally restricted to `localhost` only — this is a security feature, not a limitation.

There are two supported patterns for server usage:

---

### Pattern 1 — Headless Chrome on the same machine

This is the most common CI / server pattern.

```bash
# 1. Install Chrome headless on the server (Debian/Ubuntu example)
apt-get install -y google-chrome-stable

# 2. Launch Chrome headless with remote debugging
google-chrome \
  --headless \
  --no-sandbox \
  --disable-gpu \
  --remote-debugging-port=9222 \
  --remote-debugging-address=127.0.0.1 &

# 3. Build PEN
cd /opt/pen && go build -o pen ./cmd/pen

# 4. Run PEN (stdio — same machine as Chrome)
./pen --cdp-url http://127.0.0.1:9222 --log-level info
```

For Docker:

```dockerfile
FROM golang:1.23-bookworm AS builder
WORKDIR /app
COPY . .
RUN go build -o pen ./cmd/pen

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y \
    google-chrome-stable \
    --no-install-recommends && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/pen /usr/local/bin/pen
# Entrypoint: start Chrome then pen in the same container
COPY entrypoint.sh /entrypoint.sh
CMD ["/entrypoint.sh"]
```

```bash
#!/bin/sh
# entrypoint.sh
google-chrome --headless --no-sandbox --disable-gpu \
  --remote-debugging-port=9222 --remote-debugging-address=127.0.0.1 &
sleep 1          # give Chrome time to start
exec pen --cdp-url http://127.0.0.1:9222 "$@"
```

---

### Pattern 2 — SSH port-forward from a remote dev machine

If you want to use a browser on your laptop but run PEN somewhere else (or vice versa), forward CDP over SSH:

```bash
# On your dev machine: forward the remote server's port 9222 to localhost:9222
ssh -L 9222:localhost:9222 user@server

# On the server, PEN just connects to localhost:9222 as usual
./pen --cdp-url http://localhost:9222
```

This keeps the CDP connection over an encrypted tunnel without opening port 9222 to the network.

---

### Security notes for server deployments

- **Never** expose port 9222 to the public internet — PEN intentionally refuses non-localhost CDP URLs.
- Run Chrome with `--no-sandbox` only in containers (not on bare-metal hosts).
- Use `--allow-eval` only when the LLM / automation environment is trusted, as it allows arbitrary JS execution in the browser.
- The `--project-root` flag sandboxes all source-tool file-path lookups; always set it in production.

---

## Troubleshooting

| Symptom                                  | Likely cause                      | Fix                                                                                                |
| ---------------------------------------- | --------------------------------- | -------------------------------------------------------------------------------------------------- |
| `CDP connect failed: connection refused` | Chrome not running or wrong port  | Launch Chrome with `--remote-debugging-port=9222` and verify `http://localhost:9222/json` responds |
| `invalid CDP URL`                        | Non-localhost URL passed          | PEN only allows `localhost` / `127.0.0.1` for security                                             |
| `no targets found`                       | Chrome launched but no pages open | Open at least one tab in Chrome                                                                    |
| `HTTP transport not yet implemented`     | `--transport sse` or `http` used  | Use `--transport stdio` (the default)                                                              |
| `pen: command not found`                 | Binary not on PATH                | Run `go install ./cmd/pen` or use absolute path in IDE config                                      |
| Tools return `rate limit` errors         | Expensive operation run too fast  | Wait the cooldown period (see tool docs) or restart Chrome                                         |

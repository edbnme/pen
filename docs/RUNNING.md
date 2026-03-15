# Running PEN

## Local

```bash
pen                                          # defaults: stdio, localhost:9222
pen --cdp-url http://localhost:9222 --log-level debug
pen --allow-eval --project-root /my/project
```

PEN prints `PEN ready` to stderr and waits for an MCP client on stdin/stdout. In normal use your IDE launches it automatically — you don't run it by hand.

### Flags

| Flag             | Default                 | Purpose                                        |
| ---------------- | ----------------------- | ---------------------------------------------- |
| `--cdp-url`      | `http://localhost:9222` | CDP endpoint                                   |
| `--transport`    | `stdio`                 | `stdio`, `http`, or `sse`                      |
| `--addr`         | `localhost:6100`        | Bind address for HTTP/SSE                      |
| `--allow-eval`   | `false`                 | Enable `pen_evaluate` (executes JS in browser) |
| `--project-root` | `.`                     | Sandbox for source tool file paths             |
| `--log-level`    | `info`                  | `debug` / `info` / `warn` / `error`            |
| `--version`      | —                       | Print version and exit                         |

Both `-flag` and `--flag` work.

### HTTP Transport

For network-accessible use instead of stdio:

```bash
pen --transport http --addr localhost:6100
```

Serves MCP at `http://localhost:6100/mcp`. The `sse` transport works the same way.

---

## Browser Setup

Quit the browser **completely** first — close all windows and background processes. The debug port only works if Chrome starts fresh with the flag.

**macOS:**

```bash
open -a "Google Chrome" --args --remote-debugging-port=9222
```

**Windows (PowerShell):**

```powershell
& "C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=9222       # Chrome
& "C:\Program Files\Microsoft\Edge\Application\msedge.exe" --remote-debugging-port=9222      # Edge
```

**Linux:**

```bash
google-chrome --remote-debugging-port=9222
```

Verify: `http://localhost:9222/json` should return a JSON array of open tabs.

---

## IDE Config

Your editor spawns PEN as a child process. Configure once, then forget about it.

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

**Claude Desktop** — `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

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

If `pen` isn't on PATH, use the full binary path in the `command` field.

---

## Building from Source

```bash
git clone https://github.com/edbnme/pen.git && cd pen
go build -o pen ./cmd/pen        # Linux / macOS
go build -o pen.exe ./cmd/pen    # Windows
```

Needs Go 1.23+. Dependencies download automatically.

---

## Server / CI

PEN connects to a local browser via CDP. CDP is locked to localhost — that's a security choice, not a bug.

### Headless Chrome on the same box

```bash
# Install Chrome (Debian/Ubuntu)
apt-get install -y google-chrome-stable

# Launch headless
google-chrome --headless --no-sandbox --disable-gpu \
  --remote-debugging-port=9222 --remote-debugging-address=127.0.0.1 &

# Run PEN
pen --cdp-url http://127.0.0.1:9222
```

### Docker

```dockerfile
FROM golang:1.23-bookworm AS builder
WORKDIR /app
COPY . .
RUN go build -o pen ./cmd/pen

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y google-chrome-stable --no-install-recommends \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/pen /usr/local/bin/pen
CMD ["sh", "-c", "google-chrome --headless --no-sandbox --disable-gpu --remote-debugging-port=9222 --remote-debugging-address=127.0.0.1 & sleep 2 && exec pen --cdp-url http://127.0.0.1:9222"]
```

Use a real process manager (supervisord, etc.) in production instead of backgrounding Chrome with `&`.

### SSH tunnel

Forward a remote CDP port to localhost:

```bash
ssh -L 9222:localhost:9222 user@server
pen --cdp-url http://localhost:9222
```

### Security notes

- Never expose port 9222 to the network
- `--no-sandbox` only in containers, not bare metal
- `--allow-eval` only in trusted environments
- Always set `--project-root` in production

---

## Optional: Lighthouse CLI

The `pen_lighthouse` tool runs full Lighthouse audits against your page. It requires the Lighthouse CLI installed separately:

```bash
npm install -g lighthouse
```

If Lighthouse isn't installed, all other PEN tools work normally — `pen_lighthouse` will just return a helpful error telling you how to install it.

---

## Troubleshooting

| Problem                                  | Fix                                                                    |
| ---------------------------------------- | ---------------------------------------------------------------------- |
| `CDP connect failed: connection refused` | Browser not running or wrong port. Check `http://localhost:9222/json`. |
| `invalid CDP URL`                        | PEN only allows localhost / 127.0.0.1                                  |
| `no targets found`                       | Open at least one tab                                                  |
| `pen: command not found`                 | Binary not on PATH — use full path or install via Homebrew/Scoop       |
| Rate limit errors                        | Wait the cooldown or restart PEN                                       |
| IDE doesn't see tools                    | Restart IDE after editing MCP config                                   |

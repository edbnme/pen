# Getting Started

## Prerequisites

| Requirement                | Check                                        |
| -------------------------- | -------------------------------------------- |
| **Go 1.23+**               | `go version`                                 |
| **Chrome, Edge, or Brave** | Any Chromium-based browser                   |
| **MCP-compatible IDE**     | VS Code + Copilot, Cursor, or Claude Desktop |

Need Go? Grab it from [go.dev/dl](https://go.dev/dl/).

## Install

```bash
go install github.com/edbnme/pen/cmd/pen@latest
```

The binary lands in `$(go env GOPATH)/bin/`. Make sure that directory is on your `PATH`.

### From source

```bash
git clone https://github.com/edbnme/pen.git
cd pen
go build -o pen ./cmd/pen
```

## Start your browser with remote debugging

Quit the browser completely, then relaunch with `--remote-debugging-port=9222`:

**Windows (PowerShell)**

```bash
& "C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=9222
# or Edge:
& "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --remote-debugging-port=9222
```

**macOS**

```bash
/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome --remote-debugging-port=9222
```

**Linux**

```bash
google-chrome --remote-debugging-port=9222
```

Verify by visiting `http://localhost:9222/json` — you should see a JSON array of open tabs.

## Configure your IDE

PEN is spawned automatically by your IDE's MCP client. Add the config and you're set — no manual launch needed.

**VS Code + Copilot** — `.vscode/mcp.json` in your project root:

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

**Claude Desktop** — config at `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

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

If `pen` isn't on your PATH, use the full path to the binary instead.

## Verify

Open your web app in the debugging-enabled browser, then ask your AI assistant:

> "Check the performance metrics of this page"

PEN connects to the browser, captures the data, and returns structured results.

## Common issues

| Problem                    | Fix                                                                                                                                |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `pen: command not found`   | Add `$(go env GOPATH)/bin` to your PATH, or use the absolute binary path in your IDE config                                        |
| `CDP connect failed`       | Browser isn't running with `--remote-debugging-port=9222`, or another instance is already running — close all windows and relaunch |
| `no targets found`         | Open at least one tab in Chrome                                                                                                    |
| PEN doesn't respond in IDE | Restart the IDE after editing the MCP config                                                                                       |

# Getting Started

## What You Need

- A Chromium browser (Chrome, Edge, Brave)
- An MCP-capable editor (VS Code + Copilot, Cursor, or Claude Desktop)

That's it. No Go, no Node.js — just download the binary.

**Optional:** For `pen_lighthouse` (full Lighthouse audits), install the Lighthouse CLI:

```bash
npm install -g lighthouse
```

All other PEN tools work without it.

## Quick Install (Recommended)

One command to download PEN, then run the interactive setup wizard:

**macOS / Linux:**

```bash
curl -fsSL https://raw.githubusercontent.com/edbnme/pen/main/install.sh | sh
pen init
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/edbnme/pen/main/install.ps1 | iex
pen init
```

`pen init` walks you through everything — IDE selection, browser setup, config generation, and connection verification.

## Install PEN

### Homebrew (macOS / Linux)

```bash
brew install edbnme/tap/pen
```

### Scoop (Windows)

```powershell
scoop bucket add pen https://github.com/edbnme/scoop-pen
scoop install pen
```

### GitHub Releases

Grab the latest binary from the [Releases page](https://github.com/edbnme/pen/releases/latest).

**macOS / Linux:**

```bash
curl -Lo pen.tar.gz "https://github.com/edbnme/pen/releases/latest/download/pen_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/').tar.gz"
tar xzf pen.tar.gz pen
sudo mv pen /usr/local/bin/
```

**Windows:** Download the `.zip` from releases, extract `pen.exe`, put it somewhere on your `PATH`.

### go install

```bash
go install github.com/edbnme/pen/cmd/pen@latest
```

Needs Go 1.23+. Binary goes to `$(go env GOPATH)/bin` — make sure that's on your `PATH`.

### From source

```bash
git clone https://github.com/edbnme/pen.git
cd pen
go build -o pen ./cmd/pen        # Linux / macOS
go build -o pen.exe ./cmd/pen    # Windows
```

### Verify

```bash
pen --version
# pen 0.x.x (or "pen dev" when built without version flags)
```

## Start Your Browser

Quit the browser **completely** first — close all windows, check the system tray (Windows) or Activity Monitor (macOS) for background processes. The debug port only works if Chrome starts fresh with the flag.

**macOS:**

```bash
open -a "Google Chrome" --args --remote-debugging-port=9222
```

**Windows (PowerShell):**

```powershell
# Chrome
& "C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=9222

# Edge
& "C:\Program Files\Microsoft\Edge\Application\msedge.exe" --remote-debugging-port=9222
```

**Linux:**

```bash
google-chrome --remote-debugging-port=9222
```

Verify: open `http://localhost:9222/json` in a new tab — you should see a JSON array of open tabs.

> **Still not loading?** The browser wasn't fully closed before relaunch. On Windows, open Task Manager (`Ctrl+Shift+Esc`) and end all Chrome/Edge processes. On macOS, run `killall "Google Chrome"` then relaunch.

## Configure Your IDE

PEN runs as a child process of your editor — you configure it once and the editor handles launching.

### VS Code + GitHub Copilot

`.vscode/mcp.json` in your project:

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

### Cursor

`.cursor/mcp.json` in your project (or `~/.cursor/mcp.json` for global):

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

Config file location:

- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`

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

Claude Desktop doesn't support `${workspaceFolder}` — use the full path.

> If `pen` isn't on your PATH, use the full binary path in the `command` field.

## Test It

1. Browser running with `--remote-debugging-port=9222`
2. Open a page in that browser
3. Ask your AI: _"Check the performance metrics of this page"_

PEN connects to the browser, runs the profiling, and returns results. Logs go to stderr — check the MCP output panel in your IDE if something looks wrong.

## Troubleshooting

| Problem                  | Fix                                                                                      |
| ------------------------ | ---------------------------------------------------------------------------------------- |
| `pen: command not found` | Add the binary location to PATH, or use the full path in IDE config                      |
| `CDP connect failed`     | Browser not running with `--remote-debugging-port=9222`. Close all windows and relaunch. |
| `no targets found`       | Open at least one tab                                                                    |
| `invalid CDP URL`        | PEN only allows localhost connections                                                    |
| PEN doesn't respond      | Restart the IDE after editing MCP config                                                 |

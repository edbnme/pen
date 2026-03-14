# Getting Started

## Prerequisites

| Requirement                | Check                                        |
| -------------------------- | -------------------------------------------- |
| **Chrome, Edge, or Brave** | Any Chromium-based browser                   |
| **MCP-compatible IDE**     | VS Code + Copilot, Cursor, or Claude Desktop |

That's it. No build tools needed — just download and run.

## Install

### macOS (Homebrew)

```bash
brew install edbnme/tap/pen
```

### Windows (Scoop)

```powershell
scoop bucket add pen https://github.com/edbnme/scoop-pen
scoop install pen
```

### Download from GitHub Releases

Grab the latest binary for your platform from the
[Releases page](https://github.com/edbnme/pen/releases/latest).

**macOS / Linux:**

```bash
curl -Lo pen.tar.gz "https://github.com/edbnme/pen/releases/latest/download/pen_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/').tar.gz"
tar xzf pen.tar.gz pen
sudo mv pen /usr/local/bin/
```

**Windows (PowerShell):**

1. Download the `.zip` for Windows from the [Releases page](https://github.com/edbnme/pen/releases/latest)
2. Extract `pen.exe`
3. Move it to a directory on your `PATH`, or add its location to `PATH`

### Via `go install` (requires Go 1.23+)

```bash
go install github.com/edbnme/pen/cmd/pen@latest
```

The binary lands in `$(go env GOPATH)/bin/`. Make sure that directory is on your `PATH`:

```bash
# Linux / macOS — add to ~/.bashrc or ~/.zshrc
export PATH="$PATH:$(go env GOPATH)/bin"
```

```powershell
# Windows (PowerShell) — current session only
$env:PATH += ";$(go env GOPATH)\bin"
```

To make it permanent on Windows, add `$(go env GOPATH)\bin` to your system PATH via **Settings → System → About → Advanced system settings → Environment Variables**.

### From source (for contributors)

```bash
git clone https://github.com/edbnme/pen.git
cd pen
go build -o pen ./cmd/pen      # Linux / macOS
go build -o pen.exe ./cmd/pen  # Windows
```

### Verify the install

```bash
pen --version
```

You should see `pen 0.x.x` (or `pen dev` when built from source without version flags).

## Start your browser with remote debugging

Quit the browser completely first (all windows), then relaunch with `--remote-debugging-port=9222`:

**Windows (PowerShell)**

```powershell
# Chrome
& "C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=9222

# Edge (most common path)
& "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --remote-debugging-port=9222

# Edge (alternative 64-bit path — try this if the above doesn't work)
& "C:\Program Files\Microsoft\Edge\Application\msedge.exe" --remote-debugging-port=9222
```

**macOS**

```bash
# Chrome
/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome --remote-debugging-port=9222

# Edge
/Applications/Microsoft\ Edge.app/Contents/MacOS/Microsoft\ Edge --remote-debugging-port=9222
```

**Linux**

```bash
# Chrome
google-chrome --remote-debugging-port=9222

# Chromium
chromium-browser --remote-debugging-port=9222
```

Verify by visiting `http://localhost:9222/json` in a browser tab — you should see a JSON array of open pages.

> **Tip:** If the page doesn't load, make sure you closed **all** browser windows before relaunching. The debugging port can only be set at startup. If another Chrome/Edge process is running in the background, check your system tray or Task Manager.

## Configure your IDE

PEN is spawned automatically by your IDE's MCP client. Add the config below and the IDE handles the rest — no manual launch needed.

### VS Code + GitHub Copilot

Create or edit `.vscode/mcp.json` in your project root:

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

If `pen` isn't on your `PATH`, use the full path to the binary:

```json
{
  "servers": {
    "pen": {
      "type": "stdio",
      "command": "C:\\Users\\you\\go\\bin\\pen.exe",
      "args": ["--project-root", "${workspaceFolder}"]
    }
  }
}
```

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

Edit the config file at:

- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`

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

> **Note:** Claude Desktop requires absolute paths — `${workspaceFolder}` is not supported. Use the full path to your project directory.

## Verify

1. Make sure Chrome/Edge is running with `--remote-debugging-port=9222`
2. Open a web page in that browser
3. Ask your AI assistant:

   > "Check the performance metrics of this page"

PEN connects to the browser via CDP, captures profiling data, and returns structured results. Logs are written to **stderr** — in your IDE, check the MCP server output panel if you need to see them.

## Common issues

| Problem                         | Fix                                                                                                                             |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `pen: command not found`        | Add Go's bin directory to your PATH (see install section above), or use the absolute binary path in your IDE config             |
| `CDP connect failed`            | Browser isn't running with `--remote-debugging-port=9222`, or another instance is already running — close **all** windows first |
| `no targets found`              | Open at least one tab in Chrome/Edge                                                                                            |
| `invalid CDP URL`               | PEN only allows `localhost` / `127.0.0.1` CDP connections for security                                                          |
| PEN doesn't respond in IDE      | Restart the IDE after editing the MCP config. Check the MCP server output panel for error messages.                             |
| Build fails with version errors | Make sure you have Go 1.23 or later — run `go version` to check                                                                 |

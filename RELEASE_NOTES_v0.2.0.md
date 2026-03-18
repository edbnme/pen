# PEN v0.2.0 Release Notes

**Release date:** 2025-07-17  
**Tag:** `v0.2.0`  
**Compared to:** `v0.1.0`

---

## What's New

### `pen update` — self-update command

Run `pen update` to fetch and replace the binary with the latest release from GitHub. No need to re-run the install script.

### `pen init` — interactive setup wizard

`pen init` launches a terminal wizard (powered by charmbracelet/huh) that guides you through configuring your Chrome/Chromium path and project root, then writes a ready-to-use MCP config block for your IDE.

### `pen_navigate` tool

Navigate the active browser tab programmatically:

```
pen_navigate({ "url": "https://example.com" })
```

### `--stateless` flag

For deployments where clients must not send `Mcp-Session-Id` headers (e.g. load-balanced environments), start the server with `--stateless`:

```
pen --transport http --stateless
```

### Security Gate 9: HTTP Transport

Validates session-header constraints for HTTP transport mode, including enforcement of stateless mode when `--stateless` is set.

### One-line install scripts

- **macOS / Linux:** `curl -fsSL https://raw.githubusercontent.com/edbnme/pen/main/install.sh | bash`
- **Windows:** `irm https://raw.githubusercontent.com/edbnme/pen/main/install.ps1 | iex`

### Documentation site

Full docs now live at the pen-docs SvelteKit site (15 pages covering architecture, CDP integration, tool development, security model, troubleshooting, and more).

---

## Bug Fixes

- Fixed Edge browser path detection on Windows
- Fixed Cursor IDE config directory location
- Fixed data race in concurrent console-log entry appending (proper mutex locking in tests)
- Typo in subcommand name now shows the closest suggestion instead of a bare error

---

## Download

| Platform | Architecture          | File                    |
| -------- | --------------------- | ----------------------- |
| Linux    | amd64                 | `pen-linux-amd64`       |
| Linux    | arm64                 | `pen-linux-arm64`       |
| macOS    | amd64 (Intel)         | `pen-darwin-amd64`      |
| macOS    | arm64 (Apple Silicon) | `pen-darwin-arm64`      |
| Windows  | amd64                 | `pen-windows-amd64.exe` |

All binaries ~11–13 MB (stripped, no debug symbols).

---

## Upgrading from v0.1.0

If you have v0.1.0 installed, the quickest way to upgrade is:

```sh
# macOS / Linux
pen update

# or re-run the install script
curl -fsSL https://raw.githubusercontent.com/edbnme/pen/main/install.sh | bash
```

```powershell
# Windows
pen update

# or re-run the install script
irm https://raw.githubusercontent.com/edbnme/pen/main/install.ps1 | iex
```

No config changes are required — existing MCP config blocks remain valid.

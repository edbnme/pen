# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [0.2.0] - 2025-07-17

### Added

- **`pen update` subcommand** — self-update from the latest GitHub release without reinstalling
- **`pen init` wizard** — interactive setup wizard powered by charmbracelet/huh to configure Chrome path and project root
- **`--stateless` flag** — disables `Mcp-Session-Id` tracking for stateless HTTP transport deployments
- **`pen_navigate` tool** — navigate the active browser tab to any URL or trigger browser actions
- **Gate 9: HTTP Transport security** — validates HTTP transport session headers and enforces stateless-mode constraints
- **Installation scripts** — `install.sh` (macOS/Linux) and `install.ps1` (Windows) for one-line installs
- **Pen-docs site** — full SvelteKit documentation site covering architecture, CDP integration, tool development, security, and more
- **Typo correction** — unknown subcommands now suggest the closest valid command via Levenshtein distance

### Changed

- Environment detection now identifies VS Code, Cursor, Windsurf, and other IDE environments from process tree inspection
- Edge browser path detection fixed for Windows; Cursor config location updated
- Concurrent console-log entry appending now uses proper locking (fixes data race in tests)
- `pen-docs` added as a tracked submodule / separate build target

### Removed

- Wrangler (Cloudflare Workers) configuration — docs site moved to Vercel static adapter

---

## [0.1.0] - 2025-06-01

### Added

- Initial release of **PEN** (Performance Engineering Node) — a Model Context Protocol server for browser performance analysis
- 30 MCP tools across 9 categories: memory, CPU, network, coverage, audit, source, console, lighthouse, utility, status
- CDP (Chrome DevTools Protocol) client with auto-reconnect and multiplexed event listeners
- Streamable HTTP and stdio MCP transports
- Rate limiting, path-traversal protection, eval gating, and URL scheme validation (security Gates 1–8)
- `--cdp-url`, `--transport`, `--addr`, `--allow-eval`, `--auto-launch`, `--project-root`, `--log-level` flags
- Structured JSON output with lipgloss-styled terminal formatting

[0.2.0]: https://github.com/edbnme/pen/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/edbnme/pen/releases/tag/v0.1.0

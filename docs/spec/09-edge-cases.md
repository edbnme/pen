# Part 9: Edge Cases & Error Handling

Critical edge cases PEN handles. Each describes detection and response.

## CDP Connection

### Browser not running

Chrome wasn't started with `--remote-debugging-port`.

**Detection**: Connection to `http://localhost:9222` fails.
**Response**: Exit with error. Message includes the exact Chrome flag needed.

### Connection drops mid-operation

Browser crashes or tab closes during a heap snapshot.

**Detection**: chromedp context cancelled / WebSocket closed.
**Response**: Clean up partial temp files via `defer`. Release domain locks via `defer`. Return `isError` with "Browser disconnected during operation". On next tool call, `Reconnect` attempts to restore the connection.

### Page navigates during profiling

SPA route change or full page reload during an active profile.

**Detection**: CDP events (`Page.frameNavigated`, target destroyed).
**Response**: Same-page navigation (SPA) — continue, note it in output. Full reload — abort current operation, return partial results. Script inventory becomes stale after navigation — tools note this.

## Payload Size

### Large heap snapshots (>500 MB)

Enterprise SPA with massive data.

**Detection**: Bytes written to temp file exceed threshold.
**Response**: Continue streaming to disk (constant memory). Warn in output: "Large heap detected. Analysis limited to top retainers." Skip full graph traversal, use sampling.

### Trace buffer overflow

Long trace duration exhausts Chrome's buffer.

**Detection**: `Tracing.bufferUsage` event with `percentFull > 0.9`.
**Response**: Progress warning at 90%. If buffer fills: "Trace truncated at Xs due to buffer limit." Suggest shorter duration or fewer categories.

## MCP Protocol

### Unknown tool name (LLM hallucination)

LLM calls a non-existent tool like `pen_fix_memory_leak`.

**Detection**: Tool not found in registry.
**Response**: MCP SDK returns standard "unknown tool" error. The LLM sees `tools/list` and can self-correct.

### Concurrent conflicting tool calls

LLM calls `pen_capture_trace` and `pen_cpu_profile` simultaneously.

**Detection**: `OperationLock` sees the domain is already held.
**Response**: Immediate `isError`: "Tracing is already in use by another operation."

### Client disconnects mid-tool

IDE closes while a heap snapshot is in progress.

**Detection**: Context cancellation (`ctx.Done()`).
**Response**: Temp files cleaned via `defer`. Domain locks released via `defer`. No dangling goroutines. CDP session cleaned by chromedp's context tree.

## Source Maps

### Missing source maps

Production build without source maps, or `.map` URL returns 404.

**Detection**: No `sourceMapURL` in `scriptParsed` events.
**Response**: Degrade gracefully. `pen_list_sources` shows scripts without source map URLs. Analysis still works — all positions are in loaded script coordinates.

## Rate Limiting

### Rapid-fire tool calls

LLM calls `pen_heap_snapshot` in a tight loop.

**Detection**: `RateLimiter.Check` sees the cooldown hasn't elapsed.
**Response**: Immediate `isError`: "pen_heap_snapshot has a 10s cooldown. Try again in Xs."

## Navigation

### Blocked URL scheme

LLM tries to navigate to `javascript:alert(1)`, `data:text/html,...`, `file:///etc/passwd`, or any other non-HTTP(S) URL.

**Detection**: `validateNavigationURL` checks the scheme against a blocklist (`javascript`, `data`, `file`, `chrome`, `about`, `ftp`, `ws`, `wss`, `blob`, `vbscript`).
**Response**: Immediate `isError`: URL scheme is not allowed. Only HTTP and HTTPS URLs are accepted.

### History navigation with no history

LLM calls `pen_navigate` with `action: forward` when the browser has no forward history.

**Detection**: `currentHistoryOffset` finds the current entry is already the last in the navigation history.
**Response**: Immediate `isError` explaining there are no forward entries.

## Console

### Console buffer overflow

Console-heavy page generates thousands of messages per second.

**Detection**: Console store exceeds 1000 entries.
**Response**: Oldest 100 entries are evicted silently. The LLM sees the most recent messages. A note in the output lets the caller know messages were dropped.

### Console enable called twice

LLM calls `pen_console_enable` when the listener is already running.

**Detection**: `consoleListenerOnce` ensures the CDP listener is only registered once.
**Response**: The call succeeds idempotently — the store can optionally be cleared with the `clearFirst` parameter, but duplicate listeners are never created.

## Lighthouse

### Lighthouse CLI not installed

`pen_lighthouse` is called but the `lighthouse` binary isn't on PATH.

**Detection**: `exec.LookPath("lighthouse")` fails.
**Response**: Immediate `isError` with a clear message: "lighthouse CLI not found. Install it with: npm install -g lighthouse"

### Lighthouse timeout on complex page

Page takes too long to audit, exceeding Lighthouse's internal timeout.

**Detection**: `exec.CommandContext` cancels the Lighthouse process on context expiry.
**Response**: Return `isError` with the partial stderr output, noting the audit timed out.

## Trace Analysis

### Trace file too large

The LLM passes a trace file larger than 100 MB to `pen_trace_insights`.

**Detection**: `parseTraceFile` checks file size before reading.
**Response**: Immediate `isError`: "Trace file is X MB — exceeds the 100 MB limit." Prevents unbounded memory usage during JSON parsing.

### Empty or malformed trace

LLM passes a trace file that's empty, corrupted, or not valid JSON.

**Detection**: `parseTraceFile` attempts JSON unmarshal, checking for both wrapper format (`{"traceEvents":[...]}`) and plain array format.
**Response**: Immediate `isError` with the parse failure reason. No panic, no partial state.

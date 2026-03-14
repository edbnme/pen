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

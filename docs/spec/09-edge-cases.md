# Part 9: Edge Cases & Error Handling

This document covers the most critical edge cases PEN handles. Each scenario describes detection and response behavior.

## 9.1 CDP Connection

### 1. Browser Not Running / No Remote Debugging

**Scenario:** User starts PEN without `--remote-debugging-port` on Chrome.
**Detection:** Connection to `ws://localhost:9222` fails.
**Response:** Clear error with setup instructions — includes the exact Chrome flag and alternate port suggestion.

### 2. CDP Connection Drops Mid-Operation

**Scenario:** Browser crashes or tab closes during a heap snapshot.
**Detection:** `chromedp` context canceled / WebSocket closed.
**Response:**

- Clean up partial temp files
- Return `isError` with "Browser disconnected during operation"
- Trigger reconnection loop with exponential backoff
- Subsequent tool calls get "Reconnecting..." until connection restored

### 3. Stale CDP Session After Navigation

**Scenario:** Page navigates (SPA route change or full reload) during profiling.
**Detection:** `Target.targetDestroyed` or `Page.frameNavigated` events.
**Response:**

- Same-page (SPA): continue profiling, note navigation in output
- Full reload: abort current operation, note partial results
- Re-discover source maps after navigation

## 9.2 Payload Size

### 4. Extremely Large Heap Snapshots (>500 MB)

**Scenario:** Enterprise SPA with massive data caches.
**Detection:** Total bytes written to temp file exceeds threshold.
**Response:**

- Continue streaming to disk (no memory issue — constant RAM usage)
- Warn: "Large heap detected (523 MB). Analysis limited to top 100 retainers."
- Skip full graph traversal, use sampling-based analysis

### 5. Trace Buffer Overflow

**Scenario:** Long trace duration exhausts Chrome's trace buffer.
**Detection:** `Tracing.bufferUsage` event with `percentFull > 0.9`.
**Response:**

- Progress warning: "Trace buffer 90% full, consider stopping early"
- If buffer fills, note: "Trace truncated at 8.2s due to buffer limit"
- Suggest shorter duration or fewer categories

## 9.3 Source Maps

### 6. Missing Source Maps

**Scenario:** Production build without source maps, or `.map` URL returns 404.
**Detection:** No `sourceMappingURL` in `scriptParsed` events.
**Response:**

- Degrade gracefully — show generated file positions
- Flag: "No source map for bundle.js — showing generated positions"
- Analysis (sizes, timing) still works without source attribution

### 7. Inline Source Maps (Data URLs)

**Scenario:** Dev server embeds source map as `data:application/json;base64,...`.
**Detection:** `sourceMappingURL` starts with `data:`.
**Response:** Decode base64 payload inline, parse as regular source map. Supports both base64 and URL-encoded formats.

## 9.4 MCP Protocol

### 8. Unknown Tool Name (LLM Hallucination)

**Scenario:** LLM calls a non-existent tool like `pen_fix_memory_leak`.
**Detection:** Tool not found in registry.
**Response:** Return `isError` with fuzzy-matched suggestion: "Did you mean `pen_heap_snapshot`?" and list available tools.

### 9. Concurrent Conflicting Tool Calls

**Scenario:** LLM calls `pen_capture_trace` and `pen_cpu_profile` simultaneously.
**Detection:** `OperationLock` sees the `Tracing` domain is already held.
**Response:** Second call immediately returns `isError`: "Cannot start trace: Tracing domain is already in use. Wait or use `pen_stop_trace`."

### 10. Client Disconnects Mid-Tool

**Scenario:** IDE closes while a heap snapshot is in progress.
**Detection:** Context cancellation (`ctx.Done()`).
**Response:**

- Temp files cleaned immediately via `defer`
- Domain locks released via `defer`
- No dangling goroutines — all check `ctx.Done()`
- CDP session cleaned by chromedp's context tree

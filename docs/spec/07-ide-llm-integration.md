# Part 7: IDE & LLM Integration

## 7.1 Design Principles for LLM-Consumed Tool Output

PEN's tools output is consumed by LLMs (GPT-4, Claude, Gemini, etc.) inside IDE agents. The output format must be optimized for LLM comprehension, not human readability:

### Principle 1: Structured Text Over JSON

LLMs parse structured text better than deeply nested JSON. Each tool response is formatted as a structured report:

```
## Heap Snapshot Analysis

**Summary**: 156 MB total heap | 12,847 nodes | 34,291 edges
**Captured at**: 2025-01-15T10:23:45Z
**GC forced**: Yes

### Top Retained Objects (by retained size)
| Rank | Constructor | Count | Shallow Size | Retained Size | Source |
|------|-------------|-------|-------------|---------------|--------|
| 1 | InternalNode | 4,521 | 2.1 MB | 48.3 MB | src/components/TreeView.tsx:45 |
| 2 | EventListener | 892 | 428 KB | 12.1 MB | src/hooks/useEventBus.ts:23 |
| 3 | Uint8Array | 156 | 8.2 MB | 8.2 MB | (V8 internal) |

### Potential Memory Leaks
- **Detached DOM nodes**: 23 nodes detached but still retained via `EventListener` closures
  - Root cause chain: `TreeView.tsx:45 → useEventBus.ts:23 → addEventListener()`
  - Recommendation: Add cleanup in useEffect return function

### Growth Pattern (if tracking enabled)
- Heap grew 12 MB over 30 seconds (3 snapshots)
- Primary growth: `InternalNode` objects (+1,200 per snapshot)
```

### Principle 2: Actionable Recommendations

Every analysis output includes concrete actions the LLM can suggest to the developer:

```
### Recommendations
1. **HIGH**: Detached DOM nodes indicate a memory leak in `TreeView.tsx`.
   Fix: Add cleanup to the useEffect that attaches event listeners.
   File: src/hooks/useEventBus.ts:23

2. **MEDIUM**: Large Uint8Array allocations (8.2 MB) from image processing.
   Consider: Lazy loading or streaming image data.
   File: src/utils/imageProcessor.ts:112
```

### Principle 3: Source-Mapped Positions Always

All file references in tool output use **original source positions**, never generated/bundled positions. When a source map isn't available, PEN clearly indicates the fallback:

```
| Source | Line | Function |
|--------|------|----------|
| src/App.tsx | 42 | useExpensiveComputation |
| bundle.js:14523 (no source map) | — | anonymous |
```

### Principle 4: Progressive Detail

Tools can return summary or detailed output. LLMs typically need summaries first, then drill down:

```go
type OutputLevel string

const (
    OutputSummary  OutputLevel = "summary"   // 10-20 lines, key findings only
    OutputDetailed OutputLevel = "detailed"  // Full tables, all entries
    OutputRaw      OutputLevel = "raw"       // Maximum detail for debugging
)
```

## 7.2 Composable Workflow Design

PEN tools are designed for multi-step workflows where the LLM chains tools together:

### Example: Memory Leak Investigation

```
LLM workflow:
1. pen_performance_metrics → See if JSHeapUsedSize is growing
2. pen_heap_snapshot → Take baseline snapshot
3. (user performs action)
4. pen_heap_snapshot → Take second snapshot
5. pen_heap_diff → Compare two snapshots, identify growth
6. pen_resolve_source → Map retained objects to source files
```

### Example: Page Load Optimization

```
LLM workflow:
1. pen_capture_trace (duration=5, includeNavigation=true) → Capture page load
2. pen_analyze_trace → Get breakdown of loading phases
3. pen_network_waterfall → Identify blocking resources
4. pen_css_coverage → Find unused CSS
5. pen_js_coverage → Find unused JavaScript
```

### Tool Chaining Conventions

- Tools that produce identifiers (snapshot IDs, trace file paths) make them easy to pass to subsequent tools
- Error messages include suggested next steps: "Try `pen_list_pages` to see available tabs"
- Tools are idempotent — calling `pen_heap_snapshot` twice gives two independent snapshots

## 7.3 Tool Discovery

LLMs discover PEN's capabilities via MCP's `tools/list` method. Tool descriptions follow these guidelines:

- Start with the **action**: "Take a...", "Capture a...", "Analyze the..."
- Include **what it returns**: "Returns top retained objects..."
- Note **safety characteristics**: "Safe to call on large heaps..."
- Mention **prerequisites**: "Requires an active trace capture" (if applicable)

> For MCP client configuration examples (Cursor, VS Code, Claude Desktop), see [Running PEN](../RUNNING.md) or the [README](../../README.md).

## 7.4 Progress and Streaming for Long Operations

Long-running operations (heap snapshots, traces) use MCP progress notifications to keep the LLM (and user) informed:

```
[progress] pen_capture_trace: Starting trace capture (10s)...
[progress] pen_capture_trace: Recording... 30%
[progress] pen_capture_trace: Recording... 60%
[progress] pen_capture_trace: Recording... 90%
[progress] pen_capture_trace: Analyzing trace data...
[result] pen_capture_trace: Trace analysis complete (28 MB, 142,000 events)
```

The MCP Go SDK handles this via the `NotifyProgress` API (see Part 5, section 5.2).

## 7.5 Token Budget Awareness

LLMs have context window limits. PEN is mindful of output size:

| Tool                           | Typical Output Size | Max Output Size |
| ------------------------------ | ------------------- | --------------- |
| `pen_performance_metrics`      | 500 tokens          | 1,000 tokens    |
| `pen_heap_snapshot` (summary)  | 2,000 tokens        | 5,000 tokens    |
| `pen_network_waterfall`        | 1,500 tokens        | 4,000 tokens    |
| `pen_capture_trace` (analysis) | 3,000 tokens        | 8,000 tokens    |
| `pen_js_coverage`              | 1,000 tokens        | 3,000 tokens    |

When detailed output would exceed token limits, PEN truncates with a note:

```
### Network Requests (showing 25 of 142, sorted by duration)
...
[25 more requests omitted. Call pen_network_requests with offset=25 for next page]
```

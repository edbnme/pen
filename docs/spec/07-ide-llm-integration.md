# Part 7: IDE & LLM Output Design

## Output Principles

Every PEN tool returns text in `CallToolResult.Content`. The text is designed for LLM consumption first, human readability second.

### Structure

1. **Section header** — tool name and summary stats
2. **Data tables** — Markdown tables with the most important columns
3. **Observations** — anything anomalous, flagged inline
4. **Recommendations** — prioritized as HIGH / MEDIUM / LOW / INFO

### Constraints

- **Token budget**: Tools truncate output to stay within reasonable LLM context limits. Large result sets (e.g., 500 network requests) are capped at the top N entries.
- **No binary data**: Everything is text. Screenshots are base64-encoded PNGs embedded in the response.
- **No external references**: Output is self-contained. An LLM should never need to "go look at a file" to understand the result.
- **Structured, not free-form**: Tables and labeled sections, not prose paragraphs. LLMs parse structured text more reliably.

### Token Budget Awareness

| Tool                    | Typical Output | Upper Bound    |
| ----------------------- | -------------- | -------------- |
| pen_performance_metrics | ~30 lines      | 50 lines       |
| pen_heap_snapshot       | ~40 lines      | 100 lines      |
| pen_network_waterfall   | ~50 lines      | 120 lines      |
| pen_cpu_profile         | ~40 lines      | 100 lines      |
| pen_source_content      | ~200 lines     | maxLines param |
| pen_capture_trace       | ~50 lines      | 120 lines      |
| pen_trace_insights      | ~60 lines      | 150 lines      |
| pen_console_messages    | ~30 lines      | 100 lines      |
| pen_lighthouse          | ~50 lines      | 120 lines      |

Tools with unbounded output (coverage, waterfall, search, console messages) accept `topN`, `limit`, `lastN`, or `maxResults` parameters.

## Formatting

PEN uses `internal/format/output.go` for consistent table rendering. The `format.Table` function builds aligned Markdown tables:

```go
out := format.Table(
    []string{"Metric", "Value", "Status"},
    [][]string{
        {"JSHeapUsedSize", "82.4 MB", "⚠ High"},
        {"Nodes", "4,521", ""},
    },
)
```

Tables auto-size column widths. Additional format helpers handle byte sizes, durations, and percentages.

## Workflow Composition

PEN tools are designed to chain. The LLM drives the composition — PEN doesn't enforce workflows.

### Memory Leak Investigation

```
pen_collect_garbage → pen_heap_snapshot (A) → [user action] → pen_heap_snapshot (B) → pen_heap_diff (A, B)
```

### Page Load Optimization

```
pen_navigate (goto) → pen_capture_trace → pen_trace_insights → pen_network_waterfall → pen_web_vitals
```

### Console Debugging

```
pen_console_enable → [user triggers the problem] → pen_console_messages (level=error)
```

Start with `pen_console_enable` to wire up the listener, then wait for the user to reproduce the issue. Pull the errors with `pen_console_messages` — filtering by level keeps the noise down.

### Full Page Audit

```
pen_navigate (goto) → pen_lighthouse → pen_capture_trace → pen_trace_insights
```

Open the page, run Lighthouse for a high-level score, then drill into a trace for the specifics. `pen_trace_insights` surfaces long tasks, layout shifts, LCP, and resource bottlenecks — the exact things Lighthouse flags but doesn't explain.

### Trace-Driven Analysis

```
pen_capture_trace → pen_trace_insights
```

Capture a raw trace file with `pen_capture_trace`, then hand it to `pen_trace_insights` for a structured breakdown. No need to leave the MCP conversation to analyze the trace manually.

### Bundle Audit

```
pen_js_coverage (start) → [navigate] → pen_js_coverage (stop) → pen_css_coverage (start) → [navigate] → pen_css_coverage (stop)
```

### Multi-Tab Profiling

```
pen_list_pages → pen_select_page (target) → pen_cpu_profile → pen_performance_metrics
```

### Tool ID Flow

Some tools produce IDs consumed by downstream tools:

| Producer              | ID Type     | Consumer                              |
| --------------------- | ----------- | ------------------------------------- |
| pen_heap_snapshot     | snapshot ID | pen_heap_diff                         |
| pen_list_pages        | target ID   | pen_select_page                       |
| pen_network_waterfall | request ID  | pen_network_request                   |
| pen_list_sources      | script ID   | pen_source_content, pen_search_source |
| pen_capture_trace     | trace path  | pen_trace_insights                    |

IDs are opaque strings (or file paths in the case of traces). They remain valid until PEN restarts or the referenced resource is destroyed (tab closed, page navigated, etc.).

## Error Output

When a tool fails, the error text follows the same structured approach:

- What happened
- Why it likely happened
- What to do next

Example: _"HeapProfiler is already in use by another operation. Wait for the current heap snapshot to finish, or call another tool in the meantime."_

# Part 8: Tool Catalog

PEN exposes 25 tools across 7 categories. Each tool follows MCP conventions: structured `inputSchema` auto-generated from Go structs, text-based output, and `isError: true` for failures.

## Memory (4 tools)

### `pen_heap_snapshot`

Take a V8 heap snapshot and analyze memory usage. Streamed to disk — safe on large heaps.

| Param        | Type | Default | Description                         |
| ------------ | ---- | ------- | ----------------------------------- |
| `forceGC`    | bool | true    | Force GC before snapshot            |
| `includeDOM` | bool | false   | Include detached DOM node analysis  |
| `maxDepth`   | int  | 3       | Retained size analysis depth (1–10) |

CDP: `HeapProfiler.takeHeapSnapshot`, `addHeapSnapshotChunk` events. Exclusive lock.

### `pen_heap_diff`

Compare two heap snapshots to identify memory growth.

| Param       | Type   | Required | Description           |
| ----------- | ------ | -------- | --------------------- |
| `snapshotA` | string | yes      | ID of first snapshot  |
| `snapshotB` | string | yes      | ID of second snapshot |

### `pen_heap_track`

Start/stop heap object allocation tracking for leak detection over time.

| Param              | Type   | Default | Description             |
| ------------------ | ------ | ------- | ----------------------- |
| `action`           | string | —       | `"start"` or `"stop"`   |
| `trackAllocations` | bool   | true    | Track allocation stacks |

CDP: `HeapProfiler.startTrackingHeapObjects` / `stopTrackingHeapObjects`.

### `pen_heap_sampling`

Start/stop sampling-based heap profiling (lower overhead than full snapshots).

| Param              | Type   | Default | Description           |
| ------------------ | ------ | ------- | --------------------- |
| `action`           | string | —       | `"start"` or `"stop"` |
| `samplingInterval` | int    | 32768   | Bytes between samples |

CDP: `HeapProfiler.startSampling` / `stopSampling` / `getSamplingProfile`.

---

## CPU (2 tools)

### `pen_cpu_profile`

Record a CPU profile for a given duration and analyze hot functions.

| Param        | Type | Default | Description                       |
| ------------ | ---- | ------- | --------------------------------- |
| `duration`   | int  | 5       | Seconds to profile (1–30)         |
| `sampleRate` | int  | 100     | Sampling interval in microseconds |
| `topN`       | int  | 20      | Number of top hotspot functions   |

CDP: `Profiler.start` / `stop`. Exclusive lock.

### `pen_capture_trace`

Capture a Chrome trace (DevTools Timeline).

| Param        | Type     | Default | Description             |
| ------------ | -------- | ------- | ----------------------- |
| `duration`   | int      | 5       | Seconds to trace (1–30) |
| `categories` | []string | —       | Chrome trace categories |

CDP: `Tracing.start` / `end`, `IO.read` for stream. Exclusive lock. Default categories: `devtools.timeline`, `v8.execute`, `blink.user_timing`, `loading`.

---

## Network (4 tools)

### `pen_network_enable`

Start capturing network requests.

| Param          | Type | Default | Description                    |
| -------------- | ---- | ------- | ------------------------------ |
| `disableCache` | bool | true    | Disable browser cache          |
| `clearFirst`   | bool | true    | Clear previously captured data |

CDP: `Network.enable`.

### `pen_network_waterfall`

Show captured network requests as a waterfall table.

| Param    | Type   | Default  | Description                                |
| -------- | ------ | -------- | ------------------------------------------ |
| `sortBy` | string | `"time"` | Sort: `time`, `size`, `status`, `duration` |
| `filter` | string | —        | Filter by MIME type prefix                 |
| `limit`  | int    | 50       | Max entries                                |

### `pen_network_request`

Get details of a specific captured network request.

| Param        | Type   | Description                     |
| ------------ | ------ | ------------------------------- |
| `urlPattern` | string | URL substring to match          |
| `requestID`  | string | Exact request ID from waterfall |

### `pen_network_blocking`

Identify render-blocking resources. No parameters. Returns synchronous scripts and blocking stylesheets.

---

## Coverage (2 tools)

### `pen_js_coverage`

Collect JavaScript code coverage: per-function byte ranges, used vs unused percentages.

| Param       | Type   | Default | Description                          |
| ----------- | ------ | ------- | ------------------------------------ |
| `callCount` | bool   | true    | Include per-function call counts     |
| `detailed`  | bool   | false   | Block-level coverage granularity     |
| `navigate`  | string | —       | URL to navigate to before collecting |
| `topN`      | int    | 20      | Top N scripts by unused bytes        |

CDP: `Profiler.startPreciseCoverage` / `stopPreciseCoverage`.

### `pen_css_coverage`

Collect CSS rule usage: which rules were applied vs unused.

| Param      | Type   | Default | Description                          |
| ---------- | ------ | ------- | ------------------------------------ |
| `navigate` | string | —       | URL to navigate to for full-page CSS |
| `topN`     | int    | 20      | Top N stylesheets by unused rules    |

CDP: `CSS.startRuleUsageTracking` / `stopRuleUsageTracking`.

---

## Audit (3 tools)

### `pen_performance_metrics`

Get real-time performance metrics (instant, no profiling required). No parameters.

CDP: `Performance.getMetrics`. Returns JSHeapUsedSize, Nodes, LayoutCount, RecalcStyleCount, ScriptDuration, etc.

### `pen_web_vitals`

Capture Core Web Vitals (LCP, CLS, INP estimate).

| Param        | Type | Default | Description               |
| ------------ | ---- | ------- | ------------------------- |
| `waitForLCP` | bool | true    | Wait for LCP to stabilize |

CDP: `Runtime.evaluate` with PerformanceObserver entries.

### `pen_accessibility_check`

Quick accessibility scan: missing alt text, unlabeled inputs, contrast issues, ARIA violations.

| Param      | Type   | Description                           |
| ---------- | ------ | ------------------------------------- |
| `selector` | string | CSS selector to scope (default: page) |

CDP: `DOM`, `Runtime`.

---

## Source (3 tools)

### `pen_list_sources`

List all parsed JavaScript sources in the page.

| Param     | Type   | Default | Description                       |
| --------- | ------ | ------- | --------------------------------- |
| `refresh` | bool   | false   | Re-enable debugger for fresh list |
| `filter`  | string | —       | Filter by URL substring           |

CDP: `Debugger.enable`, `scriptParsed` events.

### `pen_source_content`

Get the source code of a specific script.

| Param        | Type   | Default | Description                     |
| ------------ | ------ | ------- | ------------------------------- |
| `scriptID`   | string | —       | Script ID from pen_list_sources |
| `urlPattern` | string | —       | URL substring (first match)     |
| `maxLines`   | int    | 200     | Truncate after N lines          |

CDP: `Debugger.getScriptSource`.

### `pen_search_source`

Search across all loaded scripts for a string or pattern.

| Param           | Type   | Default | Description                |
| --------------- | ------ | ------- | -------------------------- |
| `query`         | string | —       | Search query (required)    |
| `isRegex`       | bool   | false   | Treat query as regex       |
| `caseSensitive` | bool   | false   | Case-sensitive search      |
| `maxResults`    | int    | 50      | Max results across scripts |

CDP: `Debugger.searchInContent`.

---

## Utility (7 tools)

### `pen_status`

Show PEN server status. No parameters. Returns CDP connection state, version, active target, configured features.

### `pen_list_pages`

List all browser tabs/pages with URLs, titles, and target IDs. No parameters.

### `pen_select_page`

Switch PEN's target to a different browser tab.

| Param        | Type   | Description                   |
| ------------ | ------ | ----------------------------- |
| `targetId`   | string | Target ID from pen_list_pages |
| `urlPattern` | string | URL substring to match        |

### `pen_collect_garbage`

Force V8 garbage collection. No parameters. Cooldown: 5s.

### `pen_screenshot`

Capture a screenshot of the current page or a specific element.

| Param      | Type   | Default | Description                   |
| ---------- | ------ | ------- | ----------------------------- |
| `selector` | string | —       | CSS selector for element shot |
| `fullPage` | bool   | false   | Full page capture             |
| `format`   | string | `"png"` | `png`, `jpeg`, or `webp`      |
| `quality`  | int    | —       | 0–100 for jpeg/webp           |

### `pen_emulate`

Set device emulation: CPU throttling, network throttling, viewport presets.

| Param               | Type    | Description                            |
| ------------------- | ------- | -------------------------------------- |
| `device`            | string  | Preset: `iPhone 14`, `Pixel 7`, `iPad` |
| `cpuThrottling`     | float64 | CPU slowdown factor (e.g., 4 = 4x)     |
| `networkThrottling` | string  | `3G`, `4G`, or `WiFi`                  |

### `pen_evaluate`

Evaluate a JavaScript expression in the page context. **Only available when `--allow-eval` flag is set.**

| Param           | Type   | Default | Description              |
| --------------- | ------ | ------- | ------------------------ |
| `expression`    | string | —       | JS expression (required) |
| `returnByValue` | bool   | true    | Return result by value   |

Gated by `--allow-eval` flag (tool not registered without it) and an expression blocklist (see [Part 10](10-security-model.md)).

---

## Rate Limits

| Tool                  | Cooldown | Reason                   |
| --------------------- | -------- | ------------------------ |
| `pen_heap_snapshot`   | 10s      | Heavy GC + disk I/O      |
| `pen_capture_trace`   | 5s       | Exclusive Tracing domain |
| `pen_collect_garbage` | 5s       | V8 GC is expensive       |

All other tools: no cooldown.

## Tool Chaining

Tools produce IDs consumed by downstream tools:

| Producer                | Consumer              | ID Type     |
| ----------------------- | --------------------- | ----------- |
| `pen_heap_snapshot`     | `pen_heap_diff`       | Snapshot ID |
| `pen_list_pages`        | `pen_select_page`     | Target ID   |
| `pen_network_waterfall` | `pen_network_request` | Request ID  |
| `pen_list_sources`      | `pen_source_content`  | Script ID   |

IDs remain valid until PEN restarts or the underlying resource is destroyed.

# Part 8: Tool Catalog

## 8.1 Overview

PEN exposes 27 tools across 7 categories. Each tool follows MCP conventions: structured `inputSchema`, text-based output optimized for LLM consumption, and `isError: true` for failures.

## 8.2 Memory Tools

### `pen_heap_snapshot`

Take a V8 heap snapshot and analyze memory usage.

| Property         | Value                                                                      |
| ---------------- | -------------------------------------------------------------------------- |
| CDP Domains      | HeapProfiler                                                               |
| Streaming        | Yes — chunks written to temp file via `addHeapSnapshotChunk` events        |
| Typical Duration | 2–30 seconds depending on heap size                                        |
| Output           | Top retained objects, size stats, leak indicators, source-mapped positions |

**Parameters**:

- `forceGC` (boolean, default: true) — Force GC before snapshot
- `includeDOM` (boolean, default: false) — Include detached DOM node analysis
- `maxDepth` (integer, 1–10, default: 3) — Retained size analysis depth

### `pen_heap_diff`

Compare two heap snapshots to identify memory growth.

| Property     | Value                                                                  |
| ------------ | ---------------------------------------------------------------------- |
| CDP Domains  | HeapProfiler                                                           |
| Prerequisite | Two prior `pen_heap_snapshot` calls                                    |
| Output       | New objects, grown objects, deleted objects, net growth by constructor |

**Parameters**:

- `snapshotA` (string, required) — ID of first snapshot (from `pen_heap_snapshot` output)
- `snapshotB` (string, required) — ID of second snapshot

### `pen_heap_track`

Start/stop heap object allocation tracking for leak detection over time.

| Property    | Value                                                      |
| ----------- | ---------------------------------------------------------- |
| CDP Domains | HeapProfiler                                               |
| Uses        | `startTrackingHeapObjects` / `stopTrackingHeapObjects`     |
| Output      | Allocation timeline, objects that persist across GC cycles |

**Parameters**:

- `action` (string: "start" | "stop", required)
- `trackAllocations` (boolean, default: true) — Track allocation stacks

### `pen_heap_sampling`

Start sampling-based heap profiling (lower overhead than full snapshots).

| Property    | Value                                                   |
| ----------- | ------------------------------------------------------- |
| CDP Domains | HeapProfiler                                            |
| Uses        | `startSampling` / `stopSampling` / `getSamplingProfile` |
| Output      | Allocation sites ranked by size, source-mapped          |

**Parameters**:

- `action` (string: "start" | "stop", required)
- `samplingInterval` (integer, default: 32768) — Bytes between samples

## 8.3 CPU Tools

### `pen_cpu_profile`

Capture a CPU profile for a specified duration.

| Property    | Value                                                             |
| ----------- | ----------------------------------------------------------------- |
| CDP Domains | Profiler                                                          |
| Duration    | User-specified (default: 5s)                                      |
| Output      | Hot functions, call tree, time breakdown, source-mapped positions |

**Parameters**:

- `duration` (integer, default: 5) — Seconds to profile
- `samplingInterval` (integer, default: 100) — Microseconds between samples

### `pen_capture_trace`

Capture a full Chrome performance trace.

| Property    | Value                                                                         |
| ----------- | ----------------------------------------------------------------------------- |
| CDP Domains | Tracing, IO                                                                   |
| Streaming   | Yes — `ReturnAsStream` with gzip compression                                  |
| Duration    | User-specified (default: 5s)                                                  |
| Output      | Trace analysis: main thread activity, long tasks, layout shifts, paint events |

**Parameters**:

- `duration` (integer, default: 5) — Seconds to trace
- `categories` (string[], optional) — Custom trace categories (see table below)
- `includeScreenshots` (boolean, default: false) — Capture frame screenshots

**Trace Categories Reference:**

| Category                                      | Description                                      | Default | Overhead |
| --------------------------------------------- | ------------------------------------------------ | ------- | -------- |
| `devtools.timeline`                           | Main thread activity, layout, paint, compositing | Yes     | Low      |
| `v8.execute`                                  | JS execution events (function calls)             | Yes     | Low      |
| `blink.user_timing`                           | performance.mark() / performance.measure()       | Yes     | None     |
| `disabled-by-default-devtools.timeline`       | Detailed timeline (style invalidations)          | No      | Medium   |
| `disabled-by-default-devtools.timeline.frame` | Individual frame compositing                     | No      | Medium   |
| `disabled-by-default-v8.cpu_profiler`         | CPU profiler samples within trace                | No      | Medium   |
| `disabled-by-default-devtools.screenshot`     | Frame screenshots                                | No      | High     |
| `loading`                                     | Resource loading events                          | Yes     | Low      |
| `cc`                                          | Compositor thread activity                       | No      | Low      |

When `categories` is omitted, PEN uses the defaults marked "Yes" above. Adding `disabled-by-default-*` categories significantly increases trace file size.

### `pen_analyze_trace`

Analyze a previously captured trace file for performance insights.

| Property     | Value                                                           |
| ------------ | --------------------------------------------------------------- |
| Prerequisite | Prior `pen_capture_trace` call                                  |
| Output       | Long tasks, forced layouts, style recalculations, LCP breakdown |

**Parameters**:

- `traceId` (string, required) — ID from `pen_capture_trace` output
- `focusMetric` (string, optional) — "lcp" | "fid" | "cls" | "long-tasks"

## 8.4 Network Tools

### `pen_network_enable`

Start capturing network requests.

| Property    | Value                           |
| ----------- | ------------------------------- |
| CDP Domains | Network                         |
| Output      | Confirmation with request count |

**Parameters**:

- `disableCache` (boolean, default: false)

### `pen_network_waterfall`

Get the network request waterfall for the current page.

| Property    | Value                                                                  |
| ----------- | ---------------------------------------------------------------------- |
| CDP Domains | Network                                                                |
| Output      | Request list with timing breakdown (DNS, connect, TLS, TTFB, download) |

**Parameters**:

- `sortBy` (string, default: "startTime") — "startTime" | "duration" | "size"
- `filter` (string, optional) — URL pattern filter
- `limit` (integer, default: 50)

### `pen_network_request`

Get detailed info about a specific network request.

| Property    | Value                                                         |
| ----------- | ------------------------------------------------------------- |
| CDP Domains | Network                                                       |
| Output      | Full headers, timing, response body (if text), redirect chain |

**Parameters**:

- `requestId` (string, required)
- `includeBody` (boolean, default: false)

### `pen_network_blocking`

Identify render-blocking resources.

| Property    | Value                                                          |
| ----------- | -------------------------------------------------------------- |
| CDP Domains | Network, Page                                                  |
| Output      | Scripts/stylesheets blocking first paint, with recommendations |

**Parameters**: None

## 8.5 Coverage Tools

### `pen_js_coverage`

Measure JavaScript code coverage.

| Property    | Value                                                  |
| ----------- | ------------------------------------------------------ |
| CDP Domains | Profiler                                               |
| Uses        | `startPreciseCoverage` / `stopPreciseCoverage`         |
| Output      | Per-file coverage %, unused byte ranges, source-mapped |

**Parameters**:

- `action` (string: "start" | "stop", required)
- `detailed` (boolean, default: false) — Per-function granularity

### `pen_css_coverage`

Measure CSS code coverage.

| Property    | Value                                                      |
| ----------- | ---------------------------------------------------------- |
| CDP Domains | CSS                                                        |
| Uses        | `startRuleUsageTracking` / `stopRuleUsageTracking`         |
| Output      | Per-file coverage %, unused rules, source-mapped selectors |

**Parameters**:

- `action` (string: "start" | "stop", required)

### `pen_bundle_analysis`

Analyze JavaScript bundle sizes and composition.

| Property    | Value                                                                        |
| ----------- | ---------------------------------------------------------------------------- |
| CDP Domains | Debugger, Network                                                            |
| Output      | Per-chunk sizes, source map attribution (which source files contribute most) |

**Parameters**:

- `url` (string, optional) — Specific bundle URL (default: all JS bundles)

## 8.6 Audit Tools

### `pen_performance_metrics`

Get real-time performance metrics from the browser.

| Property    | Value                                                                       |
| ----------- | --------------------------------------------------------------------------- |
| CDP Domains | Performance                                                                 |
| Uses        | `getMetrics`                                                                |
| Duration    | Instant (<100ms)                                                            |
| Output      | JSHeapUsedSize, JSHeapTotalSize, Nodes, LayoutCount, RecalcStyleCount, etc. |

**Parameters**: None

### `pen_web_vitals`

Capture Core Web Vitals (LCP, FID/INP, CLS).

| Property    | Value                                                            |
| ----------- | ---------------------------------------------------------------- |
| CDP Domains | PerformanceTimeline, Runtime                                     |
| Output      | LCP value + element, CLS score + shifting elements, INP estimate |

**Parameters**:

- `waitForLCP` (boolean, default: true) — Wait for LCP to stabilize

### `pen_lighthouse_audit`

Run a Lighthouse audit (subset — performance only).

| Property       | Value                                             |
| -------------- | ------------------------------------------------- |
| Implementation | Node.js subprocess (no Go Lighthouse port exists) |
| Output         | Performance score, opportunities, diagnostics     |
| Note           | Requires `lighthouse` npm package installed       |

**Parameters**:

- `url` (string, required) — URL to audit
- `categories` (string[], default: ["performance"])
- `device` (string, default: "mobile") — "mobile" | "desktop"

### `pen_accessibility_check`

Quick accessibility scan using CDP DOM inspection.

| Property    | Value                                                                |
| ----------- | -------------------------------------------------------------------- |
| CDP Domains | DOM, Runtime                                                         |
| Output      | Missing alt text, unlabeled inputs, contrast issues, ARIA violations |

**Parameters**:

- `selector` (string, optional) — CSS selector to scope the check

## 8.7 Source Mapping Tools

### `pen_resolve_source`

Resolve a generated file position to its original source.

| Property | Value                                      |
| -------- | ------------------------------------------ |
| Uses     | Source map parser (Part 6)                 |
| Output   | Original file, line, column, function name |

**Parameters**:

- `url` (string, required) — Generated file URL
- `line` (integer, required) — 0-based line number
- `column` (integer, required) — 0-based column number

### `pen_list_sources`

List all source files contributing to the loaded page.

| Property    | Value                       |
| ----------- | --------------------------- |
| CDP Domains | Debugger                    |
| Output      | Source file tree with sizes |

**Parameters**:

- `filter` (string, optional) — Glob pattern (e.g., "src/components/\*\*")

### `pen_source_content`

Get the original source content of a file (from source map `sourcesContent`).

| Property | Value               |
| -------- | ------------------- |
| Uses     | Source map parser   |
| Output   | Source file content |

**Parameters**:

- `source` (string, required) — Source path (as shown in `pen_list_sources`)

## 8.8 Utility Tools

### `pen_evaluate`

Evaluate a JavaScript expression in the page context.

| Property    | Value                                         |
| ----------- | --------------------------------------------- |
| CDP Domains | Runtime                                       |
| Security    | Gated — only enabled with `--allow-eval` flag |
| Output      | Expression result (serialized)                |

**Parameters**:

- `expression` (string, required) — JS expression to evaluate
- `returnByValue` (boolean, default: true)

### `pen_screenshot`

Capture a screenshot of the current page or element.

| Property    | Value                                     |
| ----------- | ----------------------------------------- |
| CDP Domains | Page                                      |
| Output      | Base64 PNG image embedded in MCP response |

**Parameters**:

- `selector` (string, optional) — CSS selector for element screenshot
- `fullPage` (boolean, default: false)
- `format` (string, default: "png") — "png" | "jpeg" | "webp"
- `quality` (integer, optional) — 0–100 for jpeg/webp

### `pen_emulate`

Set device emulation parameters.

| Property    | Value                            |
| ----------- | -------------------------------- |
| CDP Domains | Runtime, Network                 |
| Output      | Confirmation of applied settings |

**Parameters**:

- `device` (string, optional) — Preset: "iPhone 14", "Pixel 7", "iPad"
- `cpuThrottling` (number, optional) — CPU slowdown factor (e.g., 4 = 4x slower)
- `networkThrottling` (string, optional) — "3G" | "4G" | "WiFi"

### `pen_list_pages`

List all browser tabs/pages.

| Property | Value                                  |
| -------- | -------------------------------------- |
| Uses     | `chromedp.Targets()`                   |
| Output   | Tab list with URLs, titles, target IDs |

**Parameters**: None

### `pen_select_page`

Switch PEN's target to a different tab.

| Property | Value                                |
| -------- | ------------------------------------ |
| Uses     | `chromedp.NewContext` with target ID |
| Output   | Confirmation with new target info    |

**Parameters**:

- `targetId` (string, optional) — Target from `pen_list_pages`
- `urlPattern` (string, optional) — URL substring to match

### `pen_collect_garbage`

Force V8 garbage collection.

| Property    | Value            |
| ----------- | ---------------- |
| CDP Domains | HeapProfiler     |
| Uses        | `collectGarbage` |
| Output      | Confirmation     |

**Parameters**: None

## 8.9 Tool Chaining & Cross-References

Tools are designed for multi-step workflows. This section documents how tools chain together and how IDs flow between them.

### Tool Output Identifiers

Several tools produce identifiers that are consumed by downstream tools:

| Producer Tool           | ID Format                                          | Validity                                            | Consumer Tools                                 |
| ----------------------- | -------------------------------------------------- | --------------------------------------------------- | ---------------------------------------------- |
| `pen_heap_snapshot`     | `snapshot_{unix_ms}` (e.g., `snapshot_1736935425`) | Until PEN restarts or temp cleanup                  | `pen_heap_diff`                                |
| `pen_capture_trace`     | `trace_{unix_ms}` (e.g., `trace_1736935500`)       | Until PEN restarts or temp cleanup                  | `pen_analyze_trace`                            |
| `pen_network_enable`    | — (stateful, no ID)                                | Until page navigation or `pen_network_enable` again | `pen_network_waterfall`, `pen_network_request` |
| `pen_list_pages`        | Target ID (e.g., `8A3F...B2C1`)                    | Until tab closes                                    | `pen_select_page`                              |
| `pen_network_waterfall` | Request ID per row                                 | Until page navigation                               | `pen_network_request`                          |

**ID lifetime:** Snapshot and trace IDs map to temp files on disk. They persist until PEN's temp directory is cleaned (on process exit or manual cleanup). If a tool receives a stale ID, it returns a clear error: _"Snapshot snapshot_1736935425 not found. It may have been cleaned up. Take a new snapshot."_

### Related Tool Groups

Tools that naturally chain together:

| Workflow                   | Tool Sequence                                                                                       | Purpose                         |
| -------------------------- | --------------------------------------------------------------------------------------------------- | ------------------------------- |
| Memory leak investigation  | `pen_collect_garbage` → `pen_heap_snapshot` → (user action) → `pen_heap_snapshot` → `pen_heap_diff` | Compare heap state before/after |
| Memory allocation tracking | `pen_heap_track` (start) → (user action) → `pen_heap_track` (stop)                                  | Find persistent allocations     |
| Page load optimization     | `pen_capture_trace` → `pen_analyze_trace` → `pen_network_waterfall` → `pen_network_blocking`        | Full load performance picture   |
| Bundle optimization        | `pen_js_coverage` (start) → (navigate) → `pen_js_coverage` (stop) → `pen_bundle_analysis`           | Find dead code                  |
| CSS optimization           | `pen_css_coverage` (start) → (navigate) → `pen_css_coverage` (stop)                                 | Find unused styles              |
| Web Vitals deep dive       | `pen_web_vitals` → `pen_capture_trace` → `pen_analyze_trace`                                        | Drill into bad vitals           |
| Source debugging           | `pen_resolve_source` → `pen_source_content`                                                         | Locate and read source          |
| Multi-tab profiling        | `pen_list_pages` → `pen_select_page` → (any profiling tool)                                         | Profile specific tab            |
| Device testing             | `pen_emulate` → `pen_lighthouse_audit`                                                              | Test under simulated conditions |

### Cooldowns per Tool

Some tools have rate limits to prevent resource exhaustion:

| Tool                   | Cooldown | Reason                             |
| ---------------------- | -------- | ---------------------------------- |
| `pen_heap_snapshot`    | 10s      | Heavy GC + disk I/O                |
| `pen_capture_trace`    | 5s       | Exclusive Tracing domain           |
| `pen_lighthouse_audit` | 30s      | Spawns external Node.js subprocess |
| `pen_collect_garbage`  | 5s       | V8 GC is expensive                 |

All other tools: no cooldown (can be called as frequently as needed).

## 8.10 Sample Tool Outputs

These examples show the actual text returned to the LLM in `CallToolResult.Content`, demonstrating the structured format described in [Part 7](07-ide-llm-integration.md).

### `pen_heap_snapshot` — Sample Output

```
## Heap Snapshot Analysis

**Summary**: 156 MB total heap | 12,847 nodes | 34,291 edges
**Captured at**: 2025-01-15T10:23:45Z | **GC forced**: Yes

### Top Retained Objects (by retained size)
| # | Constructor      | Count | Shallow   | Retained  | Source                              |
|---|------------------|-------|-----------|-----------|-------------------------------------|
| 1 | InternalNode     | 4,521 | 2.1 MB    | 48.3 MB   | src/components/TreeView.tsx:45      |
| 2 | EventListener    | 892   | 428 KB    | 12.1 MB   | src/hooks/useEventBus.ts:23         |
| 3 | Uint8Array       | 156   | 8.2 MB    | 8.2 MB    | (V8 internal)                       |
| 4 | FiberNode        | 3,102 | 1.8 MB    | 6.4 MB    | (React internal: reconciler)        |
| 5 | string           | 8,401 | 4.1 MB    | 4.1 MB    | (various)                           |

### Potential Memory Leaks
- **Detached DOM nodes**: 23 nodes detached but retained via EventListener closures
  Chain: TreeView.tsx:45 → useEventBus.ts:23 → addEventListener()
  Fix: Add cleanup in useEffect return function
- **Growing array**: `cachedResults` in src/hooks/useSearch.ts:67 — 2,400 entries, never trimmed
  Fix: Add LRU eviction or max size limit

### Recommendations
1. [HIGH] Fix event listener leak in useEventBus.ts:23 (12.1 MB retained)
2. [MEDIUM] Add cache eviction to useSearch.ts:67 (growing unbounded)
3. [LOW] Consider lazy loading for TreeView children (48.3 MB retained by tree)
```

### `pen_cpu_profile` — Sample Output

```
## CPU Profile (5.0 seconds)

**Total samples**: 4,891 | **Sample interval**: 100μs
**Main thread busy**: 72% (3,521 samples with JS activity)

### Hot Functions (by self time)
| # | Function                   | Self Time | Total Time | Source                           |
|---|----------------------------|-----------|------------|----------------------------------|
| 1 | JSON.parse                 | 340ms     | 340ms      | (V8 native)                      |
| 2 | renderTreeNode             | 280ms     | 890ms      | src/components/TreeView.tsx:112   |
| 3 | useExpensiveComputation    | 210ms     | 210ms      | src/hooks/useCompute.ts:34        |
| 4 | processStyleRules (React)  | 180ms     | 420ms      | (React internal: commitRoot)      |
| 5 | Array.prototype.filter     | 150ms     | 150ms      | src/utils/filterData.ts:8         |

### Framework Breakdown
- React reconciler: 890ms (25.3% of busy time)
  - commitRoot: 420ms
  - beginWork: 310ms
  - completeWork: 160ms
- Application code: 2,120ms (60.2% of busy time)
- V8 internals (GC, parsing): 511ms (14.5% of busy time)

### Recommendations
1. [HIGH] renderTreeNode takes 890ms total — consider React.memo() or virtualization
2. [MEDIUM] JSON.parse 340ms — consider streaming parser or caching parsed results
3. [LOW] Array.filter in filterData.ts could be replaced with indexed lookup
```

### `pen_network_waterfall` — Sample Output

```
## Network Waterfall (47 requests)

**Page**: http://localhost:3000/dashboard
**Total transfer**: 2.4 MB | **DOMContentLoaded**: 1.2s | **Load**: 3.8s

### Requests (sorted by start time, showing top 15)
| # | URL (truncated)                          | Method | Status | Size    | Duration | Blocking? |
|---|------------------------------------------|--------|--------|---------|----------|-----------|
| 1 | /dashboard                               | GET    | 200    | 14 KB   | 45ms     | —         |
| 2 | /_next/static/chunks/main-a1b2c3.js      | GET    | 200    | 245 KB  | 120ms    | render    |
| 3 | /_next/static/css/globals-d4e5f6.css      | GET    | 200    | 38 KB   | 85ms     | render    |
| 4 | /api/user/profile                         | GET    | 200    | 2 KB    | 890ms    | —         |
| 5 | /api/dashboard/metrics                    | GET    | 200    | 156 KB  | 1,200ms  | —         |
| 6 | /_next/static/chunks/components-g7h8.js   | GET    | 200    | 180 KB  | 95ms     | —         |

### Timing Breakdown (slowest requests)
| # | URL                        | DNS  | Connect | TLS  | TTFB   | Download |
|---|----------------------------|------|---------|------|--------|----------|
| 5 | /api/dashboard/metrics     | 0ms  | 0ms     | 0ms  | 1,150ms | 50ms    |
| 4 | /api/user/profile          | 0ms  | 0ms     | 0ms  | 885ms   | 5ms     |

### Render-Blocking Resources
- main-a1b2c3.js (245 KB) — blocks first paint for 120ms
- globals-d4e5f6.css (38 KB) — blocks first paint for 85ms

### Recommendations
1. [HIGH] /api/dashboard/metrics takes 1.2s TTFB — optimize API endpoint
2. [MEDIUM] Defer main JS bundle (245 KB render-blocking) — use async/defer
3. [LOW] Inline critical CSS to avoid 85ms render-block from stylesheet
```

### `pen_js_coverage` — Sample Output

```
## JavaScript Coverage Report

**Total JS loaded**: 1.8 MB across 12 scripts
**Total JS used**: 680 KB (37.8%)
**Unused JS**: 1.12 MB (62.2%)

### Per-File Coverage (source-mapped)
| # | Source File                           | Total   | Used   | Coverage | Unused Bytes |
|---|---------------------------------------|---------|--------|----------|--------------|
| 1 | node_modules/react-dom/...            | 420 KB  | 180 KB | 42.9%    | 240 KB       |
| 2 | node_modules/lodash/...               | 210 KB  | 12 KB  | 5.7%     | 198 KB       |
| 3 | src/components/TreeView.tsx            | 45 KB   | 38 KB  | 84.4%    | 7 KB         |
| 4 | src/pages/Dashboard.tsx                | 28 KB   | 24 KB  | 85.7%    | 4 KB         |
| 5 | src/components/Charts.tsx              | 62 KB   | 8 KB   | 12.9%    | 54 KB        |

### Unused Code Hotspots
- lodash: 198 KB unused — only using `debounce` and `groupBy`
  Fix: Replace with individual imports: `import debounce from 'lodash/debounce'`
- Charts.tsx: 54 KB unused — component conditionally rendered but all chart types bundled
  Fix: Dynamic import: `const Charts = React.lazy(() => import('./Charts'))`

### Recommendations
1. [HIGH] Replace full lodash import with tree-shakeable imports (saves 198 KB)
2. [HIGH] Lazy-load Charts component (saves 54 KB on initial load)
3. [INFO] react-dom overhead (240 KB unused) is normal — framework internals
```

### `pen_heap_diff` — Sample Output

```
## Heap Diff: snapshot_1736935425 → snapshot_1736935487

**Time between snapshots**: 62 seconds
**Net heap growth**: +18.4 MB (156 MB → 174.4 MB)

### New Objects (top by retained size)
| # | Constructor       | Count  | Retained  | Source                              |
|---|-------------------|--------|-----------|-------------------------------------|
| 1 | InternalNode      | +1,204 | +14.2 MB  | src/components/TreeView.tsx:45      |
| 2 | EventListener     | +312   | +3.8 MB   | src/hooks/useEventBus.ts:23         |
| 3 | string            | +2,100 | +0.4 MB   | (various)                           |

### Grown Objects (existing constructors that increased)
| # | Constructor       | Count Δ | Size Δ   | Source                              |
|---|-------------------|---------|-----------|-------------------------------------|
| 1 | Array             | +89     | +1.2 MB   | src/hooks/useSearch.ts:67           |
| 2 | Object            | +412    | +0.6 MB   | src/state/store.ts:34               |

### Deleted Objects
| Constructor | Count | Freed    |
|-------------|-------|----------|
| Promise     | -245  | 0.8 MB   |
| Timeout     | -12   | 0.01 MB  |

### Recommendations
1. [HIGH] InternalNode growth (+14.2 MB) indicates leak in TreeView — nodes created but never detached
2. [HIGH] EventListener growth (+312) — confirm cleanup on unmount
3. [MEDIUM] Array growth in useSearch.ts — cache not bounded
```

### `pen_heap_track` — Sample Output

```
## Heap Allocation Tracking (45 seconds)

**Action**: stop (was tracking since 2025-01-15T10:22:00Z)
**Allocations recorded**: 28,491 objects | **GC survived**: 4,210 objects (14.8%)

### Persistent Allocations (survived ≥2 GC cycles)
| # | Constructor      | Survived | Total Alloc | Retained  | Source                              |
|---|------------------|----------|-------------|-----------|-------------------------------------|
| 1 | InternalNode     | 1,204    | 3,800       | 14.2 MB   | src/components/TreeView.tsx:45      |
| 2 | EventListener    | 312      | 890         | 3.8 MB    | src/hooks/useEventBus.ts:23         |
| 3 | ResizeObserver   | 45       | 45          | 0.2 MB    | src/hooks/useResize.ts:12           |

### Allocation Timeline
| Time Window  | Allocations | GC Events | Survived |
|-------------|-------------|-----------|----------|
| 0–15s       | 9,400       | 2         | 1,380    |
| 15–30s      | 10,200      | 3         | 1,450    |
| 30–45s      | 8,891       | 2         | 1,380    |

### Recommendations
1. [HIGH] InternalNode: 32% survival rate across all windows — consistent leak
2. [MEDIUM] EventListener: created on every interaction, not cleaned up on unmount
```

### `pen_heap_sampling` — Sample Output

```
## Heap Sampling Profile

**Action**: stop | **Duration**: 30 seconds | **Sampling interval**: 32 KB

### Top Allocation Sites (by allocated bytes)
| # | Function                   | Allocated | % Total | Source                              |
|---|----------------------------|-----------|---------|-------------------------------------|
| 1 | JSON.parse                 | 12.4 MB   | 34.2%   | (V8 native)                         |
| 2 | createFiberNode            | 4.8 MB    | 13.2%   | (React internal)                    |
| 3 | transformData              | 3.2 MB    | 8.8%    | src/utils/transform.ts:28           |
| 4 | buildTree                  | 2.1 MB    | 5.8%    | src/components/TreeView.tsx:89      |
| 5 | concat                     | 1.8 MB    | 5.0%    | src/utils/stringBuilder.ts:14       |

### Recommendations
1. [HIGH] JSON.parse allocating 12.4 MB — consider streaming JSON parser
2. [MEDIUM] transformData: 3.2 MB — check if intermediate arrays can be reused
3. [LOW] String concatenation in stringBuilder.ts — use Array.join() pattern
```

### `pen_capture_trace` — Sample Output

```
## Trace Capture Complete

**Duration**: 5.0 seconds | **Trace size**: 28.4 MB (gzip: 4.2 MB)
**Events**: 142,391 | **Trace ID**: trace_1736935500

### Main Thread Activity Breakdown
| Category              | Time     | % of Trace |
|-----------------------|----------|------------|
| Scripting             | 2,180ms  | 43.6%      |
| Rendering             | 890ms    | 17.8%      |
| Painting              | 340ms    | 6.8%       |
| Loading               | 420ms    | 8.4%       |
| Idle                  | 1,170ms  | 23.4%      |

### Long Tasks (>50ms)
| # | Duration | Category          | Detail                                          |
|---|----------|-------------------|-------------------------------------------------|
| 1 | 320ms    | Scripting         | Event handler: onClick → renderTreeNode         |
| 2 | 180ms    | Rendering         | Forced layout: offsetHeight read after DOM write |
| 3 | 95ms     | Scripting         | Timer callback: setInterval (data polling)       |
| 4 | 72ms     | Rendering         | Style recalculation (1,204 elements affected)    |

### Layout Shifts (CLS contributors)
| Time   | Score  | Elements                        |
|--------|--------|---------------------------------|
| 1.2s   | 0.08   | .sidebar, .content-wrapper      |
| 2.4s   | 0.03   | .ad-banner (late-loaded image)  |

### Recommendations
1. [HIGH] 320ms long task in onClick handler — break into smaller chunks or use requestIdleCallback
2. [HIGH] Forced layout at 180ms — batch DOM reads before writes
3. [MEDIUM] setInterval polling at 95ms — consider requestAnimationFrame or reduce frequency
```

### `pen_analyze_trace` — Sample Output

```
## Trace Analysis: trace_1736935500

**Focus**: Long Tasks | **Events analyzed**: 142,391

### LCP Breakdown
| Phase          | Duration | Cumulative |
|----------------|----------|------------|
| TTFB           | 45ms     | 45ms       |
| Resource load  | 180ms    | 225ms      |
| Render delay   | 340ms    | 565ms      |
| LCP element    | img.hero-image (565ms)                |

### Forced Layout Events (Layout Thrashing)
| # | Trigger                           | Duration | Source                           |
|---|-----------------------------------|----------|----------------------------------|
| 1 | offsetHeight after appendChild    | 85ms     | src/components/VirtualList.tsx:67 |
| 2 | getBoundingClientRect in loop     | 62ms     | src/hooks/usePositioning.ts:23   |
| 3 | scrollTop after style mutation    | 34ms     | src/components/ScrollSync.tsx:45  |

### Style Recalculations
- 8 recalculations affecting >500 elements each
- Largest: 1,204 elements at t=2.4s (triggered by className toggle on body)

### Recommendations
1. [HIGH] LCP render delay (340ms) — preload hero image, reduce render-blocking JS
2. [HIGH] Layout thrashing in VirtualList.tsx — use ResizeObserver instead of offsetHeight reads
3. [MEDIUM] Body className toggle causes massive recalc — scope style changes to subtree
```

### `pen_network_enable` — Sample Output

```
## Network Capture Started

**Status**: Active | **Cache**: Enabled | **Requests captured so far**: 0
**Note**: Network events are now being recorded. Use pen_network_waterfall to view results.
```

### `pen_network_request` — Sample Output

```
## Network Request Detail

**URL**: https://api.example.com/dashboard/metrics
**Method**: GET | **Status**: 200 OK | **Protocol**: h2

### Timing
| Phase     | Duration |
|-----------|----------|
| DNS       | 12ms     |
| Connect   | 45ms     |
| TLS       | 32ms     |
| TTFB      | 1,150ms  |
| Download  | 50ms     |
| **Total** | **1,289ms** |

### Headers
**Request**:
  Accept: application/json
  Authorization: Bearer [REDACTED]
  X-Request-ID: abc-123

**Response**:
  Content-Type: application/json; charset=utf-8
  Content-Length: 159744
  Cache-Control: no-cache
  X-Response-Time: 1148ms

### Redirect Chain: None

### Recommendations
1. [HIGH] 1,150ms TTFB — server-side bottleneck. Check database queries or cache layer.
2. [LOW] No caching headers — consider Cache-Control with ETag for repeated requests.
```

### `pen_network_blocking` — Sample Output

```
## Render-Blocking Resources

**First Paint delayed by**: 205ms (blocking resources)

### Blocking Scripts
| # | URL                                      | Size   | Block Time | Recommendation             |
|---|------------------------------------------|--------|------------|----------------------------|
| 1 | /_next/static/chunks/main-a1b2c3.js      | 245 KB | 120ms      | Add async/defer attribute  |
| 2 | /scripts/analytics.js                     | 18 KB  | 45ms       | Move to end of body or defer |

### Blocking Stylesheets
| # | URL                                      | Size   | Block Time | Recommendation             |
|---|------------------------------------------|--------|------------|----------------------------|
| 1 | /_next/static/css/globals-d4e5f6.css      | 38 KB  | 85ms       | Inline critical CSS        |

### Estimated Savings
- Deferring scripts: ~165ms faster first paint
- Inlining critical CSS: ~40ms faster first paint (full CSS still loads async)
```

### `pen_css_coverage` — Sample Output

```
## CSS Coverage Report

**Total CSS loaded**: 142 KB across 4 stylesheets
**Total CSS used**: 48 KB (33.8%)
**Unused CSS**: 94 KB (66.2%)

### Per-File Coverage (source-mapped)
| # | Source File                       | Total  | Used   | Coverage | Unused  |
|---|-----------------------------------|--------|--------|----------|---------|
| 1 | src/styles/globals.css            | 38 KB  | 12 KB  | 31.6%    | 26 KB   |
| 2 | node_modules/tailwind/base.css    | 62 KB  | 18 KB  | 29.0%    | 44 KB   |
| 3 | src/components/Charts.module.css  | 24 KB  | 6 KB   | 25.0%    | 18 KB   |
| 4 | src/styles/theme.css              | 18 KB  | 12 KB  | 66.7%    | 6 KB    |

### Top Unused Rules
| Selector                    | File                        | Size  |
|-----------------------------|-----------------------------|-------|
| .dark .sidebar-collapsed    | globals.css:234             | 2.1 KB |
| .chart-tooltip-*            | Charts.module.css:45-89     | 1.8 KB |
| .prose h1, .prose h2, ...   | tailwind/base.css:1200      | 1.4 KB |

### Recommendations
1. [HIGH] PurgeCSS/Tailwind purge: 44 KB unused base styles (configure content paths)
2. [MEDIUM] Charts.module.css: 75% unused — conditional import only when Charts rendered
3. [LOW] Dark mode sidebar styles unused on this page — expected if light mode active
```

### `pen_bundle_analysis` — Sample Output

```
## Bundle Analysis

**Total JS bundles**: 8 files | **Total size**: 1.82 MB (gzip: 485 KB)

### Bundle Breakdown
| # | Chunk                              | Raw     | Gzip   | Modules |
|---|------------------------------------|---------|--------|---------|
| 1 | main-a1b2c3.js                     | 245 KB  | 72 KB  | 34      |
| 2 | vendor-d4e5f6.js                   | 820 KB  | 210 KB | 89      |
| 3 | components-g7h8.js                 | 180 KB  | 48 KB  | 22      |
| 4 | pages/dashboard-i9j0.js            | 62 KB   | 18 KB  | 8       |

### Source Attribution (vendor-d4e5f6.js — largest)
| Package               | Size    | % of Chunk |
|-----------------------|---------|------------|
| react-dom             | 420 KB  | 51.2%      |
| lodash                | 210 KB  | 25.6%      |
| date-fns              | 95 KB   | 11.6%      |
| axios                 | 45 KB   | 5.5%       |
| other (12 packages)   | 50 KB   | 6.1%       |

### Recommendations
1. [HIGH] lodash (210 KB) — switch to lodash-es or individual imports
2. [MEDIUM] date-fns (95 KB) — check if all locales are imported (tree-shake locales)
3. [INFO] react-dom (420 KB) is expected for React apps
```

### `pen_performance_metrics` — Sample Output

```
## Performance Metrics (instant)

**Captured at**: 2025-01-15T10:25:12Z

| Metric                 | Value          | Status |
|------------------------|----------------|--------|
| JSHeapUsedSize         | 82.4 MB        | ⚠ High |
| JSHeapTotalSize        | 128 MB         |        |
| Documents              | 3              |        |
| Nodes                  | 4,521          |        |
| LayoutCount            | 847            |        |
| RecalcStyleCount       | 1,203          | ⚠ High |
| LayoutDuration         | 0.342s         |        |
| RecalcStyleDuration    | 0.518s         | ⚠ High |
| ScriptDuration         | 2.180s         |        |
| TaskDuration           | 4.120s         |        |
| JSHeapUsedSize (delta) | +12.3 MB/min   | ⚠ Growing |

### Observations
- Heap growing at 12.3 MB/min — potential memory leak
- 1,203 style recalculations — check for layout thrashing
- 4,521 DOM nodes — moderate; monitor for growth
```

### `pen_web_vitals` — Sample Output

```
## Core Web Vitals

**Page**: http://localhost:3000/dashboard
**Measured at**: 2025-01-15T10:25:30Z

| Metric | Value    | Rating | Threshold          |
|--------|----------|--------|--------------------|
| LCP    | 2.4s     | Needs Improvement | Good: <2.5s |
| INP    | 180ms    | Needs Improvement | Good: <200ms |
| CLS    | 0.11     | Poor              | Good: <0.1   |

### LCP Details
- **Element**: `<img class="hero-image" src="/images/dashboard-hero.webp">`
- **Resource load**: 180ms
- **Render delay**: 340ms after resource loaded
- **Suggestion**: Preload LCP image via `<link rel="preload">`

### CLS Details
| Shift Time | Score | Elements Shifted              |
|-----------|-------|-------------------------------|
| 1.2s      | 0.08  | .sidebar (width change)        |
| 2.4s      | 0.03  | .ad-banner (image load)        |
- **Total CLS**: 0.11
- **Suggestion**: Set explicit dimensions on images and dynamic containers

### INP Details
- **Worst interaction**: click on "Load More" button → 180ms
- **Breakdown**: Input delay: 20ms | Processing: 140ms | Presentation: 20ms
- **Suggestion**: Break processing into smaller chunks with scheduler.yield()
```

### `pen_lighthouse_audit` — Sample Output

```
## Lighthouse Audit: Performance

**URL**: http://localhost:3000/dashboard
**Device**: Mobile (simulated) | **Performance Score**: 62/100

### Metrics
| Metric                     | Value | Score |
|----------------------------|-------|-------|
| First Contentful Paint     | 1.8s  | 🟠 68  |
| Largest Contentful Paint   | 3.2s  | 🔴 42  |
| Total Blocking Time        | 450ms | 🔴 38  |
| Cumulative Layout Shift    | 0.11  | 🟠 65  |
| Speed Index                | 2.4s  | 🟠 58  |

### Opportunities (estimated savings)
| Opportunity                       | Savings  |
|-----------------------------------|----------|
| Eliminate render-blocking resources | 800ms   |
| Reduce unused JavaScript           | 1.12 MB  |
| Properly size images               | 340 KB   |
| Serve images in modern formats     | 180 KB   |

### Diagnostics
- Main-thread work: 4.2s
- JavaScript execution time: 2.8s
- DOM size: 1,204 elements
- Largest network request: 245 KB (main-a1b2c3.js)

**Note**: Lighthouse run via external subprocess. For deeper analysis, use
pen_capture_trace and pen_analyze_trace for source-mapped insights.
```

### `pen_accessibility_check` — Sample Output

```
## Accessibility Scan

**Scope**: Full page | **Issues found**: 12

### Critical Issues
| # | Rule                    | Count | Elements                          |
|---|-------------------------|-------|-----------------------------------|
| 1 | img-missing-alt         | 3     | img.avatar, img.icon, img.logo    |
| 2 | input-missing-label     | 2     | input#search, input#filter        |
| 3 | color-contrast          | 4     | .text-gray-400 on .bg-white       |

### Warnings
| # | Rule                    | Count | Elements                          |
|---|-------------------------|-------|-----------------------------------|
| 1 | heading-order           | 1     | h4 appears before h2              |
| 2 | link-name               | 2     | <a> with only icon, no text/aria  |

### Details
- **img-missing-alt**: `<img class="avatar" src="/user/photo.jpg">` at src/components/Header.tsx:34
  Fix: Add `alt="User profile photo"`
- **input-missing-label**: `<input id="search" placeholder="Search...">` at src/components/SearchBar.tsx:12
  Fix: Add `<label for="search">` or `aria-label="Search"`
- **color-contrast**: Ratio 3.2:1 (needs 4.5:1) on `.text-gray-400` text
  Fix: Use `.text-gray-600` for sufficient contrast

### Summary
- Critical: 9 issues (must fix for WCAG 2.1 AA)
- Warnings: 3 issues (should fix)
```

### `pen_resolve_source` — Sample Output

```
## Source Resolution

**Generated**: http://localhost:3000/_next/static/chunks/main-a1b2c3.js
**Position**: line 14523, column 42

### Resolved
| Property        | Value                              |
|-----------------|------------------------------------|
| Original file   | src/components/TreeView.tsx         |
| Original line   | 45                                  |
| Original column | 12                                  |
| Function name   | renderTreeNode                      |

### Context (±3 lines)
```

43 | const renderTreeNode = (node: TreeNode) => {
44 | if (!node.children) return null;
45 | return <div className="tree-node"> ← HERE
46 | {node.children.map(renderTreeNode)}
47 | </div>;
48 | };

```

```

### `pen_list_sources` — Sample Output

```
## Source Files (47 files from 6 scripts)

**Filter**: src/**

### Source Tree
src/
├── components/
│   ├── Charts.tsx              (62 KB)
│   ├── Header.tsx              (8 KB)
│   ├── ScrollSync.tsx          (4 KB)
│   ├── SearchBar.tsx           (6 KB)
│   ├── Sidebar.tsx             (12 KB)
│   ├── TreeView.tsx            (45 KB)
│   └── VirtualList.tsx         (18 KB)
├── hooks/
│   ├── useCompute.ts           (3 KB)
│   ├── useEventBus.ts          (5 KB)
│   ├── usePositioning.ts       (2 KB)
│   ├── useResize.ts            (1 KB)
│   └── useSearch.ts            (4 KB)
├── pages/
│   ├── Dashboard.tsx           (28 KB)
│   └── Settings.tsx            (14 KB)
├── state/
│   └── store.ts                (6 KB)
├── styles/
│   ├── globals.css             (38 KB)
│   └── theme.css               (18 KB)
└── utils/
    ├── filterData.ts           (2 KB)
    ├── stringBuilder.ts        (1 KB)
    └── transform.ts            (3 KB)
```

### `pen_source_content` — Sample Output

````
## Source Content: src/hooks/useEventBus.ts

**Size**: 5 KB | **From source map**: main-a1b2c3.js.map

```typescript
import { useEffect, useRef, useCallback } from 'react';

type EventHandler = (data: unknown) => void;

interface EventBus {
  on(event: string, handler: EventHandler): void;
  off(event: string, handler: EventHandler): void;
  emit(event: string, data?: unknown): void;
}

const globalBus: EventBus = {
  handlers: new Map(),
  on(event, handler) {
    if (!this.handlers.has(event)) {
      this.handlers.set(event, new Set());
    }
    this.handlers.get(event)!.add(handler);
  },
  off(event, handler) {
    this.handlers.get(event)?.delete(handler);
  },
  emit(event, data) {
    this.handlers.get(event)?.forEach(h => h(data));
  }
};

export function useEventBus(event: string, handler: EventHandler) {
  const savedHandler = useRef(handler);
  savedHandler.current = handler;

  useEffect(() => {
    const h = (data: unknown) => savedHandler.current(data);
    globalBus.on(event, h);
    // BUG: This cleanup never fires if component unmounts unexpectedly
    return () => globalBus.off(event, h);
  }, [event]);
}
````

```

### `pen_evaluate` — Sample Output

```

## Expression Result

**Expression**: `document.querySelectorAll('[data-testid]').length`
**Type**: number
**Value**: 47

**Note**: eval is gated — enabled with --allow-eval flag.

```

### `pen_screenshot` — Sample Output

```

## Screenshot Captured

**Page**: http://localhost:3000/dashboard
**Viewport**: 1280×720 | **Format**: PNG | **Size**: 248 KB
**Full page**: No

[Image data: base64-encoded PNG, 248 KB]

**Note**: Image embedded as MCP content with mimeType "image/png".

```

### `pen_emulate` — Sample Output

```

## Device Emulation Applied

**Device**: iPhone 14
**Settings applied**:
| Setting | Value |
|---------------------|---------------------|
| Viewport | 390×844 |
| Device scale factor | 3 |
| User agent | iPhone (Safari 17) |
| CPU throttling | 4x slowdown |
| Network | 4G (1.6 Mbps down) |
| Touch emulation | Enabled |

**Note**: Reload page to see emulation effects on initial load metrics.

```

### `pen_list_pages` — Sample Output

```

## Browser Tabs (4 targets)

| #   | Target ID   | Type   | Title             | URL                                | Active    |
| --- | ----------- | ------ | ----------------- | ---------------------------------- | --------- |
| 1   | 8A3F...B2C1 | page   | Dashboard — MyApp | http://localhost:3000/dashboard    | ← current |
| 2   | 7D2E...A4F3 | page   | Settings — MyApp  | http://localhost:3000/settings     |           |
| 3   | 1B9C...E5D8 | page   | DevTools          | devtools://devtools/inspector.html |           |
| 4   | 3F1A...C7B2 | worker | Service Worker    | http://localhost:3000/sw.js        |           |

**Tip**: Use pen_select_page with targetId or urlPattern to switch targets.

```

### `pen_select_page` — Sample Output

```

## Page Selected

**Previous target**: Dashboard — MyApp (8A3F...B2C1)
**New target**: Settings — MyApp (7D2E...A4F3)
**URL**: http://localhost:3000/settings

CDP session re-established. All tools now operate on the new target.

```

### `pen_collect_garbage` — Sample Output

```

## Garbage Collection Forced

**Before GC**: JSHeapUsedSize = 94.2 MB
**After GC**: JSHeapUsedSize = 78.6 MB
**Freed**: 15.6 MB

**Note**: Run pen_heap_snapshot after GC for accurate retained object analysis.

```

```

# Part 3: CDP Integration Layer

## 3.1 CDP Domains Used

PEN uses these Chrome DevTools Protocol domains. All are available via the `cdproto` Go package — auto-generated from the official protocol PDL, ensuring 100% API coverage.

| CDP Domain                      | Purpose                                             | Key Methods                                                                                                                                        | Key Events                                                                                  |
| ------------------------------- | --------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| **HeapProfiler** (Experimental) | Heap snapshots, allocation tracking, leak detection | `takeHeapSnapshot`, `startSampling`, `stopSampling`, `startTrackingHeapObjects`, `stopTrackingHeapObjects`, `collectGarbage`, `getSamplingProfile` | `addHeapSnapshotChunk`, `reportHeapSnapshotProgress`, `heapStatsUpdate`, `lastSeenObjectId` |
| **Profiler**                    | CPU profiling, code coverage                        | `start`, `stop`, `startPreciseCoverage`, `stopPreciseCoverage`, `getBestEffortCoverage`                                                            | `consoleProfileFinished`, `preciseCoverageDeltaUpdate`                                      |
| **Tracing**                     | Full Chrome performance traces                      | `start` (with `ReturnAsStream` or `ReportEvents`), `end`, `getCategories`                                                                          | `dataCollected`, `tracingComplete`, `bufferUsage`                                           |
| **Performance**                 | Runtime metrics snapshot                            | `enable`, `disable`, `getMetrics`                                                                                                                  | `metrics`                                                                                   |
| **PerformanceTimeline**         | Web Vitals (LCP, FID, CLS)                          | `enable`                                                                                                                                           | `timelineEventAdded`                                                                        |
| **Network**                     | Request/response capture, waterfall timing          | `enable`, `disable`, `getResponseBody`, `setCacheDisabled`                                                                                         | `requestWillBeSent`, `responseReceived`, `loadingFinished`, `loadingFailed`, `dataReceived` |
| **Runtime**                     | JS evaluation, object inspection                    | `evaluate`, `getProperties`, `callFunctionOn`, `releaseObjectGroup`                                                                                | `consoleAPICalled`, `exceptionThrown`                                                       |
| **CSS**                         | CSS coverage, computed styles                       | `startRuleUsageTracking`, `stopRuleUsageTracking`, `getComputedStyleForNode`                                                                       | —                                                                                           |
| **Debugger**                    | Script source, source map URLs                      | `enable`, `disable`, `getScriptSource`                                                                                                             | `scriptParsed` (crucial for source map discovery)                                           |
| **Page**                        | Navigation, lifecycle events                        | `enable`, `reload`, `navigate`                                                                                                                     | `loadEventFired`, `lifecycleEvent`, `frameNavigated`                                        |
| **DOM**                         | DOM tree inspection                                 | `getDocument`, `describeNode`, `querySelector`                                                                                                     | —                                                                                           |
| **IO**                          | Stream handle reading (for large traces)            | `read`, `close`                                                                                                                                    | —                                                                                           |

### Verified Against CDP Specification

All methods and events above are verified against the [Chrome DevTools Protocol documentation](https://chromedevtools.github.io/devtools-protocol/tot/) (tip-of-tree).

Key details confirmed:

- **HeapProfiler.addHeapSnapshotChunk**: Delivers snapshot data as string chunks. Each `chunk` is a JSON fragment. Chunks arrive in order and must be concatenated.
- **HeapProfiler.reportHeapSnapshotProgress**: Parameters are `done` (integer), `total` (integer), `finished` (optional boolean).
- **Tracing.start `transferMode`**: Two modes — `ReportEvents` (data arrives via `dataCollected` events) and `ReturnAsStream` (data referenced by `IO.StreamHandle` in `tracingComplete` event). For large traces, `ReturnAsStream` with `streamCompression: "gzip"` is strongly preferred.
- **Tracing.TraceConfig.includedCategories**: Controls which trace categories are recorded. Common performance categories: `devtools.timeline`, `v8.execute`, `blink.user_timing`, `loading`, `devtools.timeline.frame`.
- **IO.read**: Takes a `handle` (StreamHandle) and optional `offset`/`size`. Returns `data` (string, possibly base64), `base64Encoded` (boolean), and `eof` (boolean).

## 3.2 Connection Strategy

PEN connects to an **existing browser** — it never launches one. The developer's dev server (Vite, Next.js, etc.) is already running Chrome.

```go
package cdp

import (
    "context"
    "fmt"
    "log/slog"
    "time"

    "github.com/chromedp/chromedp"
)

type Client struct {
    ctx       context.Context
    cancel    context.CancelFunc
    domains   *DomainManager
    cdpURL    string
}

// Connect attaches to an existing browser at the given CDP WebSocket URL.
// It does NOT launch a browser.
func Connect(ctx context.Context, cdpURL string) (*Client, error) {
    // chromedp.NewRemoteAllocator connects to existing browser
    allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, cdpURL)

    // Create a browser context (operates on the active tab)
    taskCtx, taskCancel := chromedp.NewContext(allocCtx,
        chromedp.WithLogf(slog.Info),
    )

    // Verify connection with a simple action
    if err := chromedp.Run(taskCtx); err != nil {
        allocCancel()
        taskCancel()
        return nil, fmt.Errorf("CDP connection to %s failed: %w", cdpURL, err)
    }

    client := &Client{
        ctx:    taskCtx,
        cancel: func() { taskCancel(); allocCancel() },
        cdpURL: cdpURL,
    }
    client.domains = NewDomainManager(taskCtx)

    slog.Info("connected to browser", "url", cdpURL)
    return client, nil
}

func (c *Client) Close() {
    c.cancel()
}
```

### Auto-Discovery

If no CDP URL is provided, PEN tries to auto-discover a browser:

```go
// DiscoverCDPEndpoint attempts to find a running browser with remote debugging enabled.
// It checks http://localhost:9222/json/version which Chrome exposes when
// --remote-debugging-port=9222 is set.
func DiscoverCDPEndpoint() (string, error) {
    ports := []int{9222, 9229, 9333} // common debug ports
    for _, port := range ports {
        url := fmt.Sprintf("http://localhost:%d/json/version", port)
        resp, err := http.Get(url) // #nosec G107 — localhost only
        if err != nil {
            continue
        }
        defer resp.Body.Close()

        var info struct {
            WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
        }
        if json.NewDecoder(resp.Body).Decode(&info) == nil && info.WebSocketDebuggerURL != "" {
            return info.WebSocketDebuggerURL, nil
        }
    }
    return "", fmt.Errorf("no browser with remote debugging found on ports %v", ports)
}
```

## 3.3 Reconnection Strategy

CDP WebSocket connections can drop (browser restart, dev server HMR full reload, etc.). PEN handles this gracefully:

```go
func (c *Client) reconnectLoop() {
    backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second,
                                8 * time.Second, 15 * time.Second, 30 * time.Second}
    attempt := 0

    for {
        select {
        case <-c.ctx.Done():
            return
        case <-c.disconnectCh:
            slog.Warn("CDP disconnected, attempting reconnect")
        }

        for attempt < len(backoff) {
            delay := backoff[attempt]
            if attempt < len(backoff)-1 {
                attempt++
            }

            time.Sleep(delay)

            newClient, err := Connect(c.ctx, c.cdpURL)
            if err != nil {
                slog.Warn("reconnect failed", "attempt", attempt, "err", err)
                continue
            }

            // Swap the underlying context
            c.mu.Lock()
            c.taskCtx = newClient.ctx
            c.domains = newClient.domains
            c.mu.Unlock()

            slog.Info("reconnected to browser")
            attempt = 0
            break
        }
    }
}
```

**During disconnection**, all tool calls return a clear error:

```json
{
  "content": [
    {
      "type": "text",
      "text": "CDP connection lost. Attempting to reconnect..."
    }
  ],
  "isError": true
}
```

## 3.4 Domain Lifecycle Manager

CDP domains must be explicitly enabled before use and should be disabled when no longer needed to avoid resource leaks in the browser.

```go
type DomainManager struct {
    mu      sync.Mutex
    enabled map[string]bool
    ctx     context.Context
}

func (dm *DomainManager) EnsureEnabled(domain string) error {
    dm.mu.Lock()
    defer dm.mu.Unlock()

    if dm.enabled[domain] {
        return nil
    }

    var err error
    switch domain {
    case "HeapProfiler":
        err = chromedp.Run(dm.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
            return heapprofiler.Enable().Do(ctx)
        }))
    case "Profiler":
        err = chromedp.Run(dm.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
            return profiler.Enable().Do(ctx)
        }))
    case "Network":
        err = chromedp.Run(dm.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
            return network.Enable().Do(ctx)
        }))
    case "Performance":
        err = chromedp.Run(dm.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
            return performance.Enable().Do(ctx)
        }))
    case "Debugger":
        err = chromedp.Run(dm.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
            return debugger.Enable().Do(ctx)
        }))
    case "Page":
        err = chromedp.Run(dm.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
            return page.Enable().Do(ctx)
        }))
    case "CSS":
        err = chromedp.Run(dm.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
            return css.Enable().Do(ctx)
        }))
    case "Runtime":
        err = chromedp.Run(dm.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
            return runtime.Enable().Do(ctx)
        }))
    case "Tracing":
        // Tracing has no Enable() — it's controlled via Tracing.start/end
        dm.enabled[domain] = true
        return nil
    case "IO":
        // IO has no Enable() — it's always available
        dm.enabled[domain] = true
        return nil
    default:
        return fmt.Errorf("unknown domain: %s", domain)
    }

    if err == nil {
        dm.enabled[domain] = true
        slog.Debug("CDP domain enabled", "domain", domain)
    }
    return err
}
```

## 3.5 Multi-Tab Targeting

By default, PEN operates on the first non-extension page. Users can specify a target:

```go
// SelectTarget finds the right browser tab to profile.
// Priority: 1. Explicit targetId  2. URL pattern match  3. First non-extension page
//
// Note: chromedp.NewContext returns (context.Context, context.CancelFunc), not an error.
// The caller is responsible for calling the returned cancel function on cleanup.
func SelectTarget(ctx context.Context, targetID string, urlPattern string) (context.Context, context.CancelFunc, error) {
    targets, err := chromedp.Targets(ctx)
    if err != nil {
        return nil, nil, err
    }

    for _, t := range targets {
        if targetID != "" && string(t.TargetID) == targetID {
            newCtx, cancel := chromedp.NewContext(ctx, chromedp.WithTargetID(t.TargetID))
            return newCtx, cancel, nil
        }
        if urlPattern != "" && strings.Contains(t.URL, urlPattern) {
            newCtx, cancel := chromedp.NewContext(ctx, chromedp.WithTargetID(t.TargetID))
            return newCtx, cancel, nil
        }
    }

    // Fallback: first page type target (not extension, not devtools)
    for _, t := range targets {
        if t.Type == "page" && !strings.HasPrefix(t.URL, "chrome-extension://") &&
           !strings.HasPrefix(t.URL, "devtools://") {
            newCtx, cancel := chromedp.NewContext(ctx, chromedp.WithTargetID(t.TargetID))
            return newCtx, cancel, nil
        }
    }

    return nil, nil, fmt.Errorf("no suitable browser tab found")
}
```

## 3.6 Coexistence with Chrome DevTools

PEN can run alongside an open Chrome DevTools window. CDP supports multiple connected clients. However:

- **Tracing conflicts**: Only one trace can be active at a time. If DevTools is recording a trace, PEN's `pen_capture_trace` will fail. PEN detects this and returns a clear error.
- **HeapProfiler**: Multiple clients can trigger snapshots independently. No conflict.
- **Network**: Events are broadcast to all connected clients. No conflict.
- **Performance**: `getMetrics` is read-only. No conflict.

# Part 3: CDP Integration

## Connection Lifecycle

PEN attaches to an already-running Chrome/Chromium instance. It never launches a browser.

### Endpoint Discovery

On startup, PEN hits the HTTP endpoint (typically `http://localhost:9222/json/version`) to discover the browser's WebSocket URL:

```go
func DiscoverEndpoint(ctx context.Context, httpURL string) (string, error)
```

This returns a `ws://` URL like `ws://localhost:9222/devtools/browser/...` that chromedp uses to establish the CDP session.

### Connection

```go
client := cdp.NewClient("http://localhost:9222", logger)
err := client.Reconnect(ctx, 3) // 3 retries with backoff
```

Internally, `Connect` calls `DiscoverEndpoint`, creates a `chromedp.NewRemoteAllocator`, then a `chromedp.NewContext`, and verifies with a no-op `chromedp.Run`. If it fails, `Reconnect` retries with exponential backoff.

### Cleanup

`client.Close()` cancels the tab context and the allocator context. Called via `defer` in `main.go`.

## Event Listening

PEN registers CDP event listeners through a thin wrapper over `chromedp.ListenTarget`:

```go
func (c *Client) Listen(handler func(ev interface{})) (context.CancelFunc, error)
```

Tool handlers type-switch on event types. For example, heap snapshot streaming listens for `heapProfiler.AddHeapSnapshotChunk` events and writes each chunk to a temp file.

## Tab Management

### Listing Targets

```go
targets, err := client.ListTargets(ctx)
```

Returns `[]TargetInfo` with ID, Type, Title, and URL for each browser target. Used by `pen_list_pages`.

### Switching Targets

```go
tabCtx, cancel, err := client.SelectTarget(ctx, targetID)
```

Creates a new `chromedp.NewContext` with `chromedp.WithTargetID`, verifies it's reachable, and updates the client's active context. Used by `pen_select_page`.

## CDP Domains Used

| Domain       | Tools                                                                                    | Exclusive? |
| ------------ | ---------------------------------------------------------------------------------------- | ---------- |
| HeapProfiler | pen_heap_snapshot, pen_heap_diff, pen_heap_track, pen_heap_sampling, pen_collect_garbage | Yes        |
| Profiler     | pen_cpu_profile, pen_js_coverage                                                         | Yes        |
| Tracing      | pen_capture_trace                                                                        | Yes        |
| Performance  | pen_performance_metrics                                                                  | No         |
| Network      | pen_network_enable, pen_network_waterfall, pen_network_request, pen_network_blocking     | No         |
| Page         | pen_screenshot, pen_network_blocking, pen_navigate                                       | No         |
| Runtime      | pen_evaluate, pen_web_vitals, pen_emulate, pen_console_enable, pen_console_messages      | No         |
| Debugger     | pen_list_sources, pen_source_content, pen_search_source                                  | No         |
| CSS          | pen_css_coverage                                                                         | No         |
| DOM          | pen_accessibility_check                                                                  | No         |

"Exclusive" means PEN uses `OperationLock` to prevent two tools from using the domain simultaneously. If a second tool tries to acquire a locked domain, it returns an immediate error explaining the conflict.

## DevTools Coexistence

Chrome supports multiple CDP clients on the same WebSocket. PEN can run alongside open DevTools, but:

- Only one client can control the Tracing domain at a time
- HeapProfiler operations may conflict with DevTools Memory panel usage
- This is a Chrome limitation, not a PEN limitation

PEN's domain locking prevents internal conflicts. External conflicts (DevTools vs PEN) surface as CDP errors, which PEN reports back to the LLM.

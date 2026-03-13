# Part 4: Data Streaming Strategy

## 4.1 Payload Size Reality

CDP payloads vary by orders of magnitude. This is the single biggest engineering challenge:

| Data Type                        | Typical Size | Maximum Observed | Streaming Required? |
| -------------------------------- | ------------ | ---------------- | ------------------- |
| `Performance.getMetrics`         | 2–5 KB       | 10 KB            | No                  |
| CPU Profile (30s)                | 200 KB–2 MB  | 15 MB            | Recommended         |
| Heap Snapshot (simple app)       | 10–50 MB     | —                | **Yes**             |
| Heap Snapshot (large SPA)        | 50–300 MB    | 800 MB+          | **Critical**        |
| Network waterfall (100 requests) | 50–200 KB    | 2 MB             | No                  |
| Performance Trace (10s)          | 5–30 MB      | 200 MB+          | **Yes**             |
| CSS Coverage report              | 100 KB–2 MB  | 10 MB            | Recommended         |

**Design principle**: PEN must handle 300 MB heap snapshots and 200 MB traces without OOM. Never buffer entire payloads in memory.

## 4.2 Heap Snapshot Streaming

CDP's `HeapProfiler.takeHeapSnapshot` delivers data as a stream of `addHeapSnapshotChunk` events. Each chunk contains a string fragment of the JSON snapshot. PEN processes chunks incrementally:

```go
package profiling

import (
    "context"
    "fmt"
    "io"
    "log/slog"
    "os"
    "sync"
    "sync/atomic"

    "github.com/chromedp/cdproto/heapprofiler"
    "github.com/chromedp/chromedp"
)

type HeapSnapshotResult struct {
    TempFile    string // path to streaming-written snapshot
    TotalSize   int64
    NodeCount   int
    EdgeCount   int
    Warnings    []string
}

// CaptureHeapSnapshot takes a heap snapshot via CDP and streams chunks to
// a temp file without buffering the entire snapshot in memory.
func CaptureHeapSnapshot(ctx context.Context, reportProgress func(done, total int)) (*HeapSnapshotResult, error) {
    result := &HeapSnapshotResult{}

    // Create temp file for streaming write
    tmpFile, err := os.CreateTemp("", "pen-heap-*.json")
    if err != nil {
        return nil, fmt.Errorf("create temp file: %w", err)
    }
    result.TempFile = tmpFile.Name()

    // Mutex protects concurrent writes from CDP event callbacks.
    // chromedp may deliver addHeapSnapshotChunk events on different goroutines.
    var mu sync.Mutex
    var totalSize atomic.Int64

    // Listen for chunk events BEFORE triggering the snapshot
    chromedp.ListenTarget(ctx, func(ev interface{}) {
        switch e := ev.(type) {
        case *heapprofiler.EventAddHeapSnapshotChunk:
            mu.Lock()
            n, writeErr := io.WriteString(tmpFile, e.Chunk)
            mu.Unlock()
            if writeErr != nil {
                slog.Error("failed to write heap chunk", "err", writeErr)
                return
            }
            totalSize.Add(int64(n))

        case *heapprofiler.EventReportHeapSnapshotProgress:
            if reportProgress != nil {
                reportProgress(int(e.Done), int(e.Total))
            }
        }
    })

    // Force GC before snapshot for cleaner results
    if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
        return heapprofiler.CollectGarbage().Do(ctx)
    })); err != nil {
        slog.Warn("pre-snapshot GC failed", "err", err)
    }

    // Trigger the snapshot — chunks arrive via events above
    if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
        return heapprofiler.TakeHeapSnapshot().
            WithReportProgress(true).
            WithTreatGlobalObjectsAsRoots(true).
            Do(ctx)
    })); err != nil {
        tmpFile.Close()
        os.Remove(result.TempFile)
        return nil, fmt.Errorf("take heap snapshot: %w", err)
    }

    tmpFile.Close()
    result.TotalSize = totalSize.Load()

    return result, nil
}
```

### Memory budget: O(chunk_size), not O(snapshot_size)

Each chunk from `addHeapSnapshotChunk` is typically 32 KB–512 KB. By writing directly to disk, PEN's memory usage stays constant regardless of snapshot size.

## 4.3 Trace Streaming with ReturnAsStream

CDP's Tracing domain supports two data transfer modes:

1. **`ReportEvents`** (default): Trace data arrives as `dataCollected` events containing arrays of trace event objects. For large traces, this means hundreds of event batches in memory.
2. **`ReturnAsStream`**: After tracing completes, `tracingComplete` event contains a `stream` handle. Data is read on-demand via `IO.read`. Supports `gzip` compression.

PEN uses `ReturnAsStream` for all traces over trivial duration:

```go
package profiling

import (
    "compress/gzip"
    "context"
    "encoding/base64"
    "fmt"
    "log/slog"
    "os"
    "sync"
    "time"

    cdpio "github.com/chromedp/cdproto/io"
    "github.com/chromedp/cdproto/tracing"
    "github.com/chromedp/chromedp"
)

type TraceResult struct {
    TempFile  string
    TotalSize int64
    Compressed bool
}

func CaptureTrace(ctx context.Context, durationSec int, categories []string) (*TraceResult, error) {
    result := &TraceResult{}

    // Default categories for performance analysis
    if len(categories) == 0 {
        categories = []string{
            "devtools.timeline",
            "v8.execute",
            "blink.user_timing",
            "loading",
            "devtools.timeline.frame",
            "disabled-by-default-devtools.timeline",
            "disabled-by-default-devtools.timeline.frame",
        }
    }

    // Channel to receive the stream handle when tracing completes
    streamCh := make(chan cdpio.StreamHandle, 1)

    chromedp.ListenTarget(ctx, func(ev interface{}) {
        switch e := ev.(type) {
        case *tracing.EventTracingComplete:
            if e.Stream != "" {
                streamCh <- e.Stream
            }
        case *tracing.EventBufferUsage:
            if e.PercentFull != nil && *e.PercentFull > 0.8 {
                slog.Warn("trace buffer >80% full, consider shorter duration")
            }
        }
    })

    // Start tracing with ReturnAsStream and gzip compression
    if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
        return tracing.Start().
            WithTraceConfig(&tracing.TraceConfig{
                IncludedCategories: categories,
                MemoryDumpConfig:   nil,
            }).
            WithTransferMode(tracing.TransferModeReturnAsStream).
            WithStreamCompression(tracing.StreamCompressionGzip).
            WithBufferUsageReportingInterval(1000). // ms
            Do(ctx)
    })); err != nil {
        return nil, fmt.Errorf("start trace: %w", err)
    }

    // Wait for requested duration
    select {
    case <-time.After(time.Duration(durationSec) * time.Second):
    case <-ctx.Done():
        return nil, ctx.Err()
    }

    // End tracing
    if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
        return tracing.End().Do(ctx)
    })); err != nil {
        return nil, fmt.Errorf("end trace: %w", err)
    }

    // Wait for stream handle (tracingComplete event)
    var streamHandle cdpio.StreamHandle
    select {
    case streamHandle = <-streamCh:
    case <-time.After(30 * time.Second):
        return nil, fmt.Errorf("timeout waiting for trace stream")
    }

    // Stream read → disk
    tmpFile, err := os.CreateTemp("", "pen-trace-*.json.gz")
    if err != nil {
        return nil, fmt.Errorf("create trace temp file: %w", err)
    }
    defer tmpFile.Close()
    result.TempFile = tmpFile.Name()
    result.Compressed = true

    var totalRead int64
    for {
        data, eof, readErr := readIOStream(ctx, streamHandle)
        if readErr != nil {
            os.Remove(result.TempFile)
            return nil, fmt.Errorf("read trace stream: %w", readErr)
        }

        n, _ := tmpFile.Write(data)
        totalRead += int64(n)

        if eof {
            break
        }
    }

    // Close the stream handle
    _ = closeIOStream(ctx, streamHandle)

    result.TotalSize = totalRead
    return result, nil
}

// readIOStream reads a chunk from a CDP IO stream handle.
// Uses the cdproto/io package (aliased as cdpio to avoid collision with std io).
func readIOStream(ctx context.Context, handle cdpio.StreamHandle) ([]byte, bool, error) {
    var data string
    var eof bool
    var base64Encoded bool

    err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
        var readErr error
        data, base64Encoded, eof, readErr = cdpio.Read(handle).Do(ctx)
        return readErr
    }))
    if err != nil {
        return nil, false, err
    }

    if base64Encoded {
        decoded, decErr := base64.StdEncoding.DecodeString(data)
        if decErr != nil {
            return nil, false, fmt.Errorf("base64 decode: %w", decErr)
        }
        return decoded, eof, nil
    }

    return []byte(data), eof, nil
}

func closeIOStream(ctx context.Context, handle cdpio.StreamHandle) error {
    return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
        return cdpio.Close(handle).Do(ctx)
    }))
}
```

### gzip Decompression Pipeline

When a tool needs to analyze the trace (not just store it), PEN decompresses on-the-fly:

```go
// DecompressTrace produces a reader over the decompressed trace data.
func DecompressTrace(traceFile string) (io.ReadCloser, error) {
    f, err := os.Open(traceFile)
    if err != nil {
        return nil, err
    }

    gz, err := gzip.NewReader(f)
    if err != nil {
        f.Close()
        return nil, fmt.Errorf("gzip reader: %w", err)
    }

    return &compoundCloser{gz, f}, nil
}

type compoundCloser struct {
    io.ReadCloser
    underlying io.Closer
}

func (c *compoundCloser) Close() error {
    err1 := c.ReadCloser.Close()
    err2 := c.underlying.Close()
    if err1 != nil {
        return err1
    }
    return err2
}
```

## 4.4 Incremental JSON Processing

Heap snapshots are structured as a single JSON object with large arrays (`nodes`, `edges`, `strings`). Parsing the entire snapshot into memory defeats the purpose of streaming. PEN uses an incremental JSON stream parser:

```go
package analysis

import (
    "encoding/json"
    "fmt"
    "io"
    "os"
)

type SnapshotMeta struct {
    NodeCount  int
    EdgeCount  int
    TotalSize  int64
}

// ScanSnapshotMeta reads key metadata from a heap snapshot without loading
// the entire file into memory. Uses json.Decoder for streaming.
func ScanSnapshotMeta(snapshotPath string) (*SnapshotMeta, error) {
    f, err := os.Open(snapshotPath)
    if err != nil {
        return nil, err
    }
    defer f.Close()

    info, _ := f.Stat()
    meta := &SnapshotMeta{TotalSize: info.Size()}

    dec := json.NewDecoder(f)

    // Navigate to "snapshot" → "meta" without loading everything
    depth := 0
    for {
        t, err := dec.Token()
        if err == io.EOF {
            break
        }
        if err != nil {
            return nil, fmt.Errorf("json parse: %w", err)
        }

        switch v := t.(type) {
        case json.Delim:
            switch v {
            case '{', '[':
                depth++
            case '}', ']':
                depth--
            }
        case string:
            if depth == 2 && v == "node_count" {
                dec.Decode(&meta.NodeCount)
            }
            if depth == 2 && v == "edge_count" {
                dec.Decode(&meta.EdgeCount)
            }
        }
    }

    return meta, nil
}
```

For deeper analysis (e.g., finding retained size of specific objects), PEN will need a more sophisticated streaming graph walker — planned for a future release.

## 4.5 Memory Budget & Cleanup

### Memory Limits

| Component                   | Budget             | Enforcement             |
| --------------------------- | ------------------ | ----------------------- |
| Heap snapshot chunk buffer  | 1 MB max per chunk | Write-through to disk   |
| Trace IO.read buffer        | 1 MB per read call | CDP default chunk size  |
| JSON scanner working memory | 10 MB max          | Incremental parsing     |
| Concurrent operation total  | 256 MB ceiling     | Go runtime `GOMEMLIMIT` |
| Temp file storage           | 2 GB per session   | Periodic cleanup        |

### Temp File Lifecycle

```go
package storage

import (
    "os"
    "path/filepath"
    "strings"
    "time"
    "log/slog"
)

var tempDir string

func init() {
    tempDir = filepath.Join(os.TempDir(), "pen")
    os.MkdirAll(tempDir, 0700)
}

// CleanStaleTempFiles removes temp files older than maxAge.
// Called on startup and periodically during long sessions.
func CleanStaleTempFiles(maxAge time.Duration) {
    entries, err := os.ReadDir(tempDir)
    if err != nil {
        return
    }

    cutoff := time.Now().Add(-maxAge)
    for _, entry := range entries {
        if !strings.HasPrefix(entry.Name(), "pen-") {
            continue
        }
        info, err := entry.Info()
        if err != nil {
            continue
        }
        if info.ModTime().Before(cutoff) {
            path := filepath.Join(tempDir, entry.Name())
            if err := os.Remove(path); err == nil {
                slog.Debug("cleaned stale temp file", "file", entry.Name())
            }
        }
    }
}
```

### Cleanup Triggers

1. **On startup**: Clean files older than 1 hour
2. **After each tool completes**: Clean the specific temp files used (if analysis is done)
3. **On graceful shutdown**: Clean all session temp files
4. **Periodic**: Every 30 minutes during long sessions

## 4.6 Data Flow Summary

```
Browser (CDP WebSocket)
    │
    ├─── Small payloads (<1 MB) ──────────→ In-memory analysis → MCP response
    │    (Performance.getMetrics,
    │     Network.requestWillBeSent)
    │
    ├─── Medium payloads (1-10 MB) ───────→ Temp file + streaming parse → MCP response
    │    (CPU profiles,
    │     CSS coverage)
    │
    └─── Large payloads (10 MB–800 MB) ──→ Temp file + incremental pass → Summary → MCP response
         (Heap snapshots via chunks,
          Traces via IO.read stream)
```

The MCP response always contains a **structured summary**, never raw multi-megabyte data. The LLM receives actionable analysis, not raw bytes.

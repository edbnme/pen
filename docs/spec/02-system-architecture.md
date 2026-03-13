# Part 2: System Architecture

## 2.0 Technology Choices

**Language: Go** — selected for CDP library maturity (`chromedp` — 11k+ stars, 100% CDP domain coverage), the official MCP Go SDK v1.2.0 (three transports, generic `AddTool`, progress notifications), single-binary distribution, and fast compile-test loops.

| Component              | Library                                      | Version         | Why                                                                 |
| ---------------------- | -------------------------------------------- | --------------- | ------------------------------------------------------------------- |
| **MCP Server**         | `github.com/modelcontextprotocol/go-sdk/mcp` | v1.2.0          | Three transports, generic tool registration, progress notifications |
| **CDP Client**         | `github.com/chromedp/chromedp`               | v0.13+          | Full CDP coverage, event-driven, remote allocator support           |
| **CDP Protocol Types** | `github.com/chromedp/cdproto`                | auto-generated  | Type-safe CDP methods, events, types for every domain               |
| **Structured Logging** | `log/slog`                                   | Go 1.21+ stdlib | Zero-dep, structured, levels                                        |
| **JSON Streaming**     | `encoding/json`                              | stdlib          | `json.Decoder` for incremental heap snapshot parsing                |

**Dependency principles**: minimize external deps, no CGo, no Node.js (except Lighthouse subprocess), vendored via `go mod vendor`.

> **Rust escape hatch**: If heap graph analysis becomes CPU-bound, specific modules can be extracted to Rust as a shared library or subprocess.

## 2.1 High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         IDE / AI Assistant                              │
│               (Cursor, GitHub Copilot, Claude Desktop)                  │
│                                                                         │
│  The LLM invokes MCP tools to investigate performance issues:          │
│    pen_find_memory_leaks → pen_take_heap_snapshot → pen_capture_trace   │
│    pen_analyze_network_waterfall → pen_get_bottleneck_source            │
└───────────────────────────────┬─────────────────────────────────────────┘
                                │ JSON-RPC 2.0 over stdio (or HTTP)
                                ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                          PEN MCP Server                                 │
│                                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────────────────────┐    │
│  │  Tool Router  │  │ Session Mgr  │  │  Progress Reporter         │    │
│  │  (mcp.AddTool │  │ (CDP state)  │  │  (notifications/progress)  │    │
│  │   handlers)   │  │              │  │                            │    │
│  └──────┬────────┘  └──────┬───────┘  └────────────────────────────┘    │
│         │                  │                                            │
│  ┌──────▼──────────────────▼────────────────────────────────────────┐   │
│  │                     Orchestrator Layer                            │   │
│  │                                                                  │   │
│  │  Coordinates multi-step profiling workflows:                     │   │
│  │  1. Enable CDP domains → 2. Capture data (disk-streamed)        │   │
│  │  3. Parse & analyze → 4. Source map resolve                     │   │
│  │  5. Format results → 6. Return to MCP client                   │   │
│  └──────┬────────────────────────────────┬──────────────────────────┘   │
│         │                                │                              │
│  ┌──────▼───────────┐   ┌───────────────▼──────────────────────┐       │
│  │  CDP Client       │   │  Source Map Engine                   │       │
│  │  (chromedp)       │   │                                     │       │
│  │                   │   │  - Parse .map files from disk        │       │
│  │  - WebSocket conn │   │  - VLQ decode mappings segment       │       │
│  │  - Event listener │   │  - Binary search gen→orig coords    │       │
│  │  - Domain manager │   │  - fsnotify for HMR invalidation    │       │
│  └──────┬────────────┘   └───────────────┬──────────────────────┘       │
│         │                                │                              │
│  ┌──────▼────────────┐   ┌───────────────▼──────────────────────┐       │
│  │  Temp File Mgr    │   │  Framework Analyzer                  │       │
│  │                   │   │                                     │       │
│  │  - Track temp     │   │  - React component attribution      │       │
│  │    files created  │   │  - Hook identification              │       │
│  │  - Cleanup on     │   │  - Scheduler internal detection     │       │
│  │    exit/signal    │   │  - (Svelte/Vue planned)             │       │
│  └───────────────────┘   └──────────────────────────────────────┘       │
│                                                                         │
└──────────┬──────────────────────────────────┬───────────────────────────┘
           │ WebSocket                        │ Filesystem reads
           │ ws://localhost:9222              │
           ▼                                  ▼
┌─────────────────────┐          ┌──────────────────────────────────┐
│  Chrome / Chromium   │          │  Local Project Filesystem        │
│  (dev server tab)    │          │                                  │
│                      │          │  src/                            │
│  localhost:3000      │          │  ├── components/                 │
│  (Vite/Next/etc.)    │          │  │   ├── DataGrid.tsx            │
│  --remote-debugging  │          │  │   └── Header.tsx              │
│  -port=9222          │          │  dist/ (or .next/ or build/)     │
│                      │          │  ├── assets/index-a1b2c3.js      │
│                      │          │  └── assets/index-a1b2c3.js.map  │
└──────────────────────┘          └──────────────────────────────────┘
```

## 2.2 Component Responsibilities

| Component              | Responsibility                                                                                                                                         | Key Interfaces                              |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------- |
| **Tool Router**        | Registers MCP tools, validates input schemas (auto from Go struct tags), dispatches to handler functions                                               | `mcp.AddTool(server, tool, handler)`        |
| **Session Manager**    | Tracks CDP WebSocket connection state. Reconnects on disconnect with exponential backoff. Manages which CDP domains are enabled.                       | `ConnectToBrowser()`, `EnsureDomain()`      |
| **Progress Reporter**  | Sends MCP `notifications/progress` during long operations (heap snapshot capture, Lighthouse). Uses progress tokens from MCP requests.                 | `req.Session.NotifyProgress()`              |
| **Orchestrator**       | Coordinates multi-step workflows. E.g.: "find memory leak" = GC → snapshot₁ → wait → GC → snapshot₂ → diff → source resolve                            | Composes CDP + SourceMap + Analysis         |
| **CDP Client**         | Thin wrapper around chromedp providing domain-specific helpers. Handles event subscription and data streaming.                                         | `chromedp.Run()`, `chromedp.ListenTarget()` |
| **Source Map Engine**  | Parses `.map` files, maintains in-memory index of generated→original mappings, watches for rebuilds via fsnotify.                                      | `Resolve(url, line, col)`                   |
| **Framework Analyzer** | React-specific attribution (v0.1.0). Maps CDP function names to component names, identifies hooks and scheduler internals. Svelte/Vue/Angular planned. | `AttributeToComponent()`                    |
| **Temp File Manager**  | Tracks all temp files created during profiling. Cleans up on SIGINT/SIGTERM and graceful shutdown.                                                     | `Track(path)`, `Cleanup()`                  |

## 2.3 Data Flow: Memory Leak Detection

```
Step 1: LLM calls pen_find_memory_leaks
         │
Step 2:  ├→ Orchestrator: EnsureDomain("HeapProfiler")
         │
Step 3:  ├→ CDP: HeapProfiler.collectGarbage()
         │
Step 4:  ├→ CDP: HeapProfiler.takeHeapSnapshot(reportProgress=true)
         │   └→ Events: addHeapSnapshotChunk × N → streamed to temp file on disk
         │   └→ Events: reportHeapSnapshotProgress → sent as MCP progress
         │
Step 5:  ├→ Wait (user-defined action window)
         │
Step 6:  ├→ CDP: HeapProfiler.collectGarbage()
         │
Step 7:  ├→ CDP: HeapProfiler.takeHeapSnapshot() → second temp file
         │
Step 8:  ├→ Streaming diff analysis (json.Decoder on both files)
         │   └→ Compare node counts and sizes by constructor name
         │   └→ Identify growing object types
         │
Step 9:  ├→ For each growing type, extract allocation stack traces
         │   └→ Source Map Engine: resolve bundled → original locations
         │
Step 10: └→ Return structured result to MCP client
              └→ Top leaked types with source locations + code snippets
```

## 2.4 Process Model

```
IDE Process                      PEN Process                    Browser Process
─────────────                    ────────────                   ───────────────
                                                                Chrome/Chromium
Cursor/Copilot/etc.              pen serve --transport stdio    localhost:3000
                                                                :9222 debug port
    │                                │                              │
    │──── spawn (stdio) ────────────→│                              │
    │                                │──── ws connect ─────────────→│
    │                                │←── ws connected ────────────│
    │                                │                              │
    │── tools/call ─────────────────→│                              │
    │                                │── CDP commands ─────────────→│
    │                                │←── CDP events ──────────────│
    │←── notifications/progress ─────│                              │
    │                                │ (stream to disk, parse)      │
    │←── tools/result ───────────────│                              │
    │                                │                              │
    │── tools/call (another) ───────→│                              │
    │   ...                          │   ...                        │
    │                                │                              │
    │── [IDE exits] ────────────────→│── ws close ─────────────────→│
    │                                │── cleanup temp files          │
    │                                │── exit                        │
```

## 2.5 Directory Structure

```
pen/
├── cmd/
│   └── pen/
│       └── main.go              # CLI entry point (cobra)
├── internal/
│   ├── server/
│   │   ├── server.go            # MCP server setup and tool registration
│   │   └── transport.go         # Transport selection (stdio vs HTTP)
│   ├── cdp/
│   │   ├── client.go            # CDP connection manager
│   │   ├── domains.go           # Domain enable/disable lifecycle
│   │   ├── heap.go              # Heap snapshot capture + streaming
│   │   ├── profiler.go          # CPU profiling
│   │   ├── trace.go             # Performance trace capture
│   │   ├── network.go           # Network waterfall capture
│   │   ├── coverage.go          # JS/CSS coverage
│   │   └── metrics.go           # Runtime metrics
│   ├── analysis/
│   │   ├── heap_parser.go       # Streaming V8 heap snapshot parser
│   │   ├── heap_diff.go         # Differential snapshot analysis
│   │   ├── trace_parser.go      # Chrome trace format parser
│   │   ├── network_analysis.go  # Network waterfall analysis
│   │   └── webvitals.go         # Core Web Vitals extraction
│   ├── sourcemap/
│   │   ├── parser.go            # Source map v3 parser + VLQ decoder
│   │   ├── index.go             # Source map index + resolution
│   │   └── watcher.go           # fsnotify watcher for HMR
│   ├── framework/
│   │   └── react.go             # React component attribution (v0.1.0)
│   ├── tools/
│   │   ├── memory.go            # Memory tool handlers
│   │   ├── cpu.go               # CPU tool handlers
│   │   ├── trace.go             # Trace tool handlers
│   │   ├── network.go           # Network tool handlers
│   │   ├── coverage.go          # Coverage tool handlers
│   │   ├── lighthouse.go        # Lighthouse tool handler
│   │   └── sourcemap.go         # Source map tool handlers
│   └── tempfiles/
│       └── manager.go           # Temp file tracking + cleanup
├── docs/
│   └── spec/                    # This specification
├── go.mod
├── go.sum
└── README.md
```

## 2.6 Concurrency Model

PEN uses Go's goroutine model:

| Goroutine                   | Purpose                                                 | Lifetime                      |
| --------------------------- | ------------------------------------------------------- | ----------------------------- |
| **Main**                    | MCP server event loop (JSON-RPC dispatch)               | Process lifetime              |
| **CDP event listener**      | `chromedp.ListenTarget()` callback goroutine            | CDP connection lifetime       |
| **Tool handler** (per call) | Each `tools/call` runs in its own goroutine             | Single tool call              |
| **fsnotify watcher**        | Watches for source map changes on disk                  | Process lifetime              |
| **Reconnection**            | Exponential backoff reconnect loop when CDP disconnects | Until reconnected or shutdown |

**Serialization constraint**: Some CDP operations are mutually exclusive (e.g., you cannot run two `Tracing.start` calls simultaneously). The orchestrator layer uses a `sync.Mutex` per CDP domain to serialize conflicting operations, returning an error if a tool is called while a conflicting operation is in progress.

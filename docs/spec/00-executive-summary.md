# Part 0: Executive Summary

PEN is an MCP server that gives LLMs direct access to Chrome DevTools Protocol. It connects to a running Chrome instance, exposes 30 profiling and analysis tools via MCP, and returns structured text optimized for LLM consumption.

LLMs can interpret performance data and suggest fixes, but they can't reach into browser internals. PEN bridges that gap.

## Architecture

```
[IDE / LLM] ←— MCP (stdio | HTTP) —→ [PEN] ←— CDP (WebSocket) —→ [Chrome]
```

The LLM calls tools like `pen_heap_snapshot` or `pen_cpu_profile`. PEN translates them to CDP commands, streams results to disk, and returns structured analysis.

## Tool Categories

| Category   | Count | Scope                                        |
| ---------- | ----- | -------------------------------------------- |
| Memory     | 4     | Heap snapshots, diffs, allocation tracking   |
| CPU        | 3     | CPU profiling, Chrome traces, trace insights |
| Network    | 4     | Request capture, waterfall, blocking detect  |
| Coverage   | 2     | JS/CSS code coverage                         |
| Audit      | 3     | Performance metrics, Web Vitals, a11y        |
| Source     | 3     | Script listing, content retrieval, search    |
| Console    | 2     | Real-time console message capture            |
| Lighthouse | 1     | Full Lighthouse audits (requires CLI)        |
| Utility    | 8     | Navigation, screenshots, eval, emulate, tabs |

## Key Decisions

| Decision       | Choice                       | Rationale                                                       |
| -------------- | ---------------------------- | --------------------------------------------------------------- |
| Language       | Go                           | Single binary, chromedp (v0.13.6), MCP Go SDK (v1.3.1)          |
| Transport      | stdio primary, HTTP optional | stdio for IDE spawning; HTTP for shared/remote                  |
| CDP connection | Attach to existing browser   | Never launch a browser — dev already has one running            |
| Large payloads | Stream to disk               | Heap snapshots can exceed 1 GB; constant memory usage           |
| Security       | Layered gates                | Eval gating, expression blocklist, path validation, rate limits |

## Relationship to chrome-devtools-mcp

Google maintains [`chrome-devtools-mcp`](https://github.com/ChromeDevTools/chrome-devtools-mcp) — a general DevTools MCP server for navigation, DOM, screenshots, network, traces, memory, and Lighthouse.

PEN is performance-focused: differential heap analysis (multi-snapshot leak detection), streaming architecture for large payloads, Go single binary (no Node.js), and every tool designed to answer "why is this slow?" rather than "what's on the page?". The two can complement each other.

## Spec Index

| Part | Title             | Covers                                      |
| ---- | ----------------- | ------------------------------------------- |
| 0    | Executive Summary | This document                               |
| 2    | Architecture      | Components, data flow, packages             |
| 3    | CDP Integration   | Connection lifecycle, domain management     |
| 4    | Data Streaming    | Heap/trace streaming, temp files            |
| 5    | MCP Server Design | Handlers, transports, capabilities          |
| 6    | Source Tools      | Script listing, content retrieval, search   |
| 7    | IDE & LLM Output  | Output format, workflow composition         |
| 8    | Tool Catalog      | All 30 tools with params and sample outputs |
| 9    | Edge Cases        | Error handling for critical scenarios       |
| 10   | Security Model    | Threat model, gates, attack/defense matrix  |
| A    | Appendix          | Verified sources and references             |

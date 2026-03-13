# Part 0: Executive Summary

## What is PEN?

**PEN** (Performance Engineer Node) is a CLI binary that acts as an autonomous performance engineering assistant for UI codebases. It bridges three worlds:

1. **Chrome DevTools Protocol (CDP)** — Live profiling data from a running browser
2. **Local Source Code** — Your React/Svelte/Vue project files and source maps
3. **AI Assistants** — Cursor, GitHub Copilot, Claude Desktop, or any MCP-compatible client

## The Core Value Proposition

Today, performance debugging requires a human to:

1. Open Chrome DevTools
2. Record a trace or heap snapshot
3. Interpret complex flame charts and retainer graphs
4. Mentally map minified bundle locations back to source code
5. Formulate a fix

PEN automates steps 1–4, delivering structured, source-mapped performance intelligence directly to an LLM. The LLM can then propose a code fix with full context.

## How It Works

```
Developer asks Copilot: "This page has a memory leak in the data grid. Find and fix it."

Copilot (via MCP) → pen_find_memory_leaks
    PEN → CDP: collectGarbage → takeHeapSnapshot (streamed to disk)
    PEN → user interaction pause
    PEN → CDP: collectGarbage → takeHeapSnapshot (streamed to disk)
    PEN → diff snapshots (incremental parse, never fully in RAM)
    PEN → source map resolve: bundle.js:1:45302 → src/components/DataGrid.tsx:142
    PEN → return structured result

Copilot receives:
{
  "leaks": [{
    "type": "Array",
    "name": "eventListeners",
    "retainedSize": "12.4 MB",
    "growthRate": "3,847 objects/snapshot",
    "source": {
      "file": "src/components/DataGrid.tsx",
      "line": 142,
      "snippet": "listeners.push(new ResizeObserver(...))",
      "context": "useEffect missing cleanup function"
    }
  }]
}

Copilot → proposes: Add cleanup return to useEffect on line 138
```

## Key Design Decisions

| Decision          | Choice                               | Why                                                                       |
| ----------------- | ------------------------------------ | ------------------------------------------------------------------------- |
| Language          | **Go**                               | Best CDP library (chromedp), official MCP SDK, single binary distribution |
| MCP transport     | **stdio** (primary), HTTP (optional) | stdio is how IDEs spawn tool processes; HTTP for shared/remote use        |
| CDP connection    | **Attach to existing browser**       | Never launch a browser — the dev server already has one running           |
| Large payloads    | **Stream to disk**                   | Heap snapshots can be 2+ GB; never hold in RAM                            |
| Source mapping    | **Custom VLQ parser**                | Avoids external deps; source map v3 spec is simple enough                 |
| Framework support | **React** (v0.1.0)                   | Most common UI framework; Svelte/Vue/Angular planned next                 |

## Relationship to chrome-devtools-mcp

Google's Chrome team maintains [`chrome-devtools-mcp`](https://github.com/ChromeDevTools/chrome-devtools-mcp) — an MCP server that exposes general DevTools functionality (navigation, DOM, screenshots, network, performance traces, memory snapshots, Lighthouse).

**PEN differentiates by:**

1. **Source map resolution** — chrome-devtools-mcp reports bundled locations; PEN resolves to original source
2. **Differential analysis** — PEN does multi-snapshot leak detection, not just single snapshots
3. **Framework-aware attribution** — PEN identifies React components and Svelte reactive blocks
4. **Go single binary** — No Node.js runtime required
5. **Streaming architecture** — Purpose-built for multi-GB heap snapshots
6. **Performance-focused intelligence** — Every tool is designed to answer "why is this slow?" not just "what's on the page?"

PEN can also **complement** chrome-devtools-mcp — a developer might use chrome-devtools-mcp for general browser automation and PEN specifically for performance analysis.

## Non-Goals

- PEN is **not** a general browser automation tool (use chrome-devtools-mcp or Playwright for that)
- PEN does **not** launch or manage browser instances
- PEN does **not** modify running application state (read-only profiling)
- PEN does **not** replace Chrome DevTools for interactive debugging

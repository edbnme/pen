# Appendix: Sources

Primary sources referenced in this specification.

## CDP Protocol

| Domain       | URL                                                                  |
| ------------ | -------------------------------------------------------------------- |
| HeapProfiler | https://chromedevtools.github.io/devtools-protocol/tot/HeapProfiler/ |
| Profiler     | https://chromedevtools.github.io/devtools-protocol/tot/Profiler/     |
| Tracing      | https://chromedevtools.github.io/devtools-protocol/tot/Tracing/      |
| IO           | https://chromedevtools.github.io/devtools-protocol/tot/IO/           |
| Performance  | https://chromedevtools.github.io/devtools-protocol/tot/Performance/  |
| Network      | https://chromedevtools.github.io/devtools-protocol/tot/Network/      |
| Debugger     | https://chromedevtools.github.io/devtools-protocol/tot/Debugger/     |
| Page         | https://chromedevtools.github.io/devtools-protocol/tot/Page/         |
| Runtime      | https://chromedevtools.github.io/devtools-protocol/tot/Runtime/      |
| DOM          | https://chromedevtools.github.io/devtools-protocol/tot/DOM/          |
| CSS          | https://chromedevtools.github.io/devtools-protocol/tot/CSS/          |

## MCP Protocol

| Source                         | URL                                              |
| ------------------------------ | ------------------------------------------------ |
| MCP Specification (2025-03-26) | https://spec.modelcontextprotocol.io/2025-03-26/ |
| MCP Go SDK (v1.3.1)            | https://github.com/modelcontextprotocol/go-sdk   |

## Go Libraries

| Library  | Version | URL                                            | Used for                    |
| -------- | ------- | ---------------------------------------------- | --------------------------- |
| chromedp | v0.13.6 | https://github.com/chromedp/chromedp           | CDP connection and actions  |
| cdproto  | pinned  | https://github.com/chromedp/cdproto            | Auto-generated CDP bindings |
| MCP SDK  | v1.3.1  | https://github.com/modelcontextprotocol/go-sdk | MCP server, transports      |

## Prior Art

| Project             | URL                                                   | Relation                                                                        |
| ------------------- | ----------------------------------------------------- | ------------------------------------------------------------------------------- |
| chrome-devtools-mcp | https://github.com/ChromeDevTools/chrome-devtools-mcp | Google's general DevTools MCP server. PEN is performance-focused and Go-native. |

## External Tools

| Tool       | Install                     | Used by        | Purpose                                                           |
| ---------- | --------------------------- | -------------- | ----------------------------------------------------------------- |
| Lighthouse | `npm install -g lighthouse` | pen_lighthouse | Full page audit (performance, accessibility, SEO, best practices) |

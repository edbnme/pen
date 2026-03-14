# Part 6: Source Tools

## Overview

PEN provides three tools for inspecting JavaScript loaded in the browser. These use the CDP Debugger domain to enumerate scripts, retrieve their content, and search across them.

**Important**: PEN does not parse source maps. It reports the `sourceMapURL` field from CDP's `scriptParsed` events as metadata, but does not fetch, decode, or resolve source maps. All source positions are in terms of the loaded (possibly bundled/minified) scripts.

## Tools

### `pen_list_sources`

Lists all JavaScript sources parsed by V8 in the current page.

- Enables the `Debugger` domain (if `refresh` is true, disables and re-enables to get fresh events)
- Collects `Debugger.scriptParsed` events
- Returns script ID, URL, source map URL (if present), and byte length

Useful for understanding what scripts are loaded and their sizes.

### `pen_source_content`

Retrieves the source text of a specific script.

- Accepts a script ID (from `pen_list_sources`) or a URL substring pattern
- Calls `Debugger.getScriptSource` to fetch the content
- Truncates output after `maxLines` lines (default 200) to stay within LLM token budgets

The content is the script as V8 sees it — if it's bundled/minified, that's what you get.

### `pen_search_source`

Searches across all loaded scripts for a string or regex pattern.

- Calls `Debugger.searchInContent` on each script
- Supports regex and case-sensitive modes
- Caps results at `maxResults` (default 50) across all scripts
- Returns matching lines with script URL and line numbers

## CDP Interaction

All three tools use the Debugger domain:

| CDP Method                 | Used by            |
| -------------------------- | ------------------ |
| `Debugger.enable`          | pen_list_sources   |
| `Debugger.disable`         | pen_list_sources   |
| `Debugger.getScriptSource` | pen_source_content |
| `Debugger.searchInContent` | pen_search_source  |

The `scriptParsed` event (fired after `Debugger.enable`) provides the script inventory: ID, URL, start/end positions, hash, source map URL, execution context.

## Source Map URL

When a script has a `//# sourceMappingURL=...` comment, CDP includes it in the `scriptParsed` event's `sourceMapURL` field. PEN reports this URL in `pen_list_sources` output but does not:

- Fetch the source map file
- Parse the source map JSON
- Decode VLQ mappings
- Resolve original file positions

This is by design. Source map parsing is complex and the primary use case (LLMs analyzing performance data) works well with bundled script positions. If source map resolution is needed, it can be added as a future enhancement.

## Path Validation

`pen_source_content` validates any file paths against `security.ValidateSourcePath` to prevent path traversal. This applies when source content references local file paths rather than URLs.

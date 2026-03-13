# Part 6: Codebase Mapping — Source Maps & Framework Attribution

## 6.1 The Problem

CDP reports performance data in terms of **generated code** — minified JS files, bundled CSS, and transformed source. Developers think in terms of **source files** — React components, Vue SFCs, TypeScript modules.

PEN must bridge this gap: when CDP says "function at bundle.js:14523:42 is a hot spot", PEN must report "function `useExpensiveHook` at `src/hooks/useExpensiveHook.ts:37:5`".

## 6.2 Source Map Discovery

Source maps are discovered through two CDP mechanisms:

### 1. Debugger.scriptParsed Events

When the `Debugger` domain is enabled, Chrome emits `scriptParsed` events for every JavaScript file loaded. These events include `sourceMapURL` if the script has an associated source map:

```go
package sourcemap

import (
    "context"
    "log/slog"
    "sync"

    "github.com/chromedp/cdproto/debugger"
    "github.com/chromedp/chromedp"
)

type ScriptInfo struct {
    ScriptID     string
    URL          string
    SourceMapURL string
}

type SourceMapRegistry struct {
    mu      sync.RWMutex
    scripts map[string]*ScriptInfo // scriptID → info
    byURL   map[string]*ScriptInfo // URL → info
}

func NewSourceMapRegistry() *SourceMapRegistry {
    return &SourceMapRegistry{
        scripts: make(map[string]*ScriptInfo),
        byURL:   make(map[string]*ScriptInfo),
    }
}

// Listen starts watching for scriptParsed events from the Debugger domain.
func (r *SourceMapRegistry) Listen(ctx context.Context) {
    chromedp.ListenTarget(ctx, func(ev interface{}) {
        if e, ok := ev.(*debugger.EventScriptParsed); ok {
            info := &ScriptInfo{
                ScriptID:     string(e.ScriptID),
                URL:          e.URL,
                SourceMapURL: e.SourceMapURL,
            }

            r.mu.Lock()
            r.scripts[info.ScriptID] = info
            if info.URL != "" {
                r.byURL[info.URL] = info
            }
            r.mu.Unlock()

            if info.SourceMapURL != "" {
                slog.Debug("discovered source map",
                    "script", info.URL,
                    "sourceMap", info.SourceMapURL,
                )
            }
        }
    })
}
```

### 2. Inline `//# sourceMappingURL=` Comments

For scripts loaded without the `Debugger` domain active, PEN can fall back to fetching script source and parsing the `sourceMappingURL` comment:

```go
// ExtractSourceMapURL parses the //# sourceMappingURL= comment from script source.
func ExtractSourceMapURL(source string) string {
    // Search from the end — the comment is always at the bottom
    lines := strings.Split(source, "\n")
    for i := len(lines) - 1; i >= max(0, len(lines)-5); i-- {
        line := strings.TrimSpace(lines[i])
        if strings.HasPrefix(line, "//# sourceMappingURL=") {
            return strings.TrimPrefix(line, "//# sourceMappingURL=")
        }
        if strings.HasPrefix(line, "/*# sourceMappingURL=") {
            url := strings.TrimPrefix(line, "/*# sourceMappingURL=")
            return strings.TrimSuffix(url, " */")
        }
    }
    return ""
}
```

## 6.3 Source Map v3 Parser

PEN implements its own Source Map v3 parser for two reasons:

1. **No good Go library exists** for the full source map spec with VLQ decoding
2. **We need fine-grained control** over memory usage (source maps can be large)

### Source Map v3 Format

A source map is a JSON file:

```json
{
  "version": 3,
  "file": "bundle.js",
  "sourceRoot": "",
  "sources": ["../src/App.tsx", "../src/hooks/useData.ts"],
  "sourcesContent": ["import React...", "export function..."],
  "names": ["useState", "useEffect", "fetchData"],
  "mappings": "AAAA,SAAS,GAAG,GAAG;AACf..."
}
```

The `mappings` field encodes position mappings using **Base64 VLQ** encoding:

```go
package sourcemap

import (
    "encoding/json"
    "fmt"
    "os"
    "sort"
)

type SourceMap struct {
    Version        int      `json:"version"`
    File           string   `json:"file"`
    SourceRoot     string   `json:"sourceRoot"`
    Sources        []string `json:"sources"`
    SourcesContent []string `json:"sourcesContent"`
    Names          []string `json:"names"`
    Mappings       string   `json:"mappings"`

    // Decoded segments, populated by Parse()
    segments [][]Segment
}

type Segment struct {
    GeneratedCol   int
    SourceIndex    int
    SourceLine     int
    SourceCol      int
    NameIndex      int
    HasSource      bool
    HasName        bool
}

// OriginalPosition represents a resolved position in the original source.
type OriginalPosition struct {
    Source string
    Line   int
    Column int
    Name   string
}

func Parse(data []byte) (*SourceMap, error) {
    var sm SourceMap
    if err := json.Unmarshal(data, &sm); err != nil {
        return nil, fmt.Errorf("parse source map JSON: %w", err)
    }
    if sm.Version != 3 {
        return nil, fmt.Errorf("unsupported source map version: %d", sm.Version)
    }

    sm.segments = decodeVLQMappings(sm.Mappings)
    return &sm, nil
}

// Lookup resolves a generated position (line:col) to the original source position.
// Uses binary search on the decoded segments for O(log n) lookup.
func (sm *SourceMap) Lookup(genLine, genCol int) *OriginalPosition {
    if genLine < 0 || genLine >= len(sm.segments) {
        return nil
    }

    segs := sm.segments[genLine]
    if len(segs) == 0 {
        return nil
    }

    // Binary search for the segment with the largest generatedCol <= genCol
    idx := sort.Search(len(segs), func(i int) bool {
        return segs[i].GeneratedCol > genCol
    }) - 1

    if idx < 0 || idx >= len(segs) {
        return nil
    }

    seg := segs[idx]
    if !seg.HasSource {
        return nil
    }

    pos := &OriginalPosition{
        Line:   seg.SourceLine,
        Column: seg.SourceCol,
    }

    if seg.SourceIndex >= 0 && seg.SourceIndex < len(sm.Sources) {
        pos.Source = sm.Sources[seg.SourceIndex]
    }
    if seg.HasName && seg.NameIndex >= 0 && seg.NameIndex < len(sm.Names) {
        pos.Name = sm.Names[seg.NameIndex]
    }

    return pos
}
```

### VLQ Decoder

```go
// Base64 VLQ decoding as specified in the Source Map v3 spec.
// Each value is encoded as a variable-length sequence of base64 characters.
// Bit 0 of each sextet is a continuation bit. Bit 0 of the first sextet is the sign bit.

const b64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var b64Lookup [128]int

func init() {
    for i := range b64Lookup {
        b64Lookup[i] = -1
    }
    for i, c := range b64Chars {
        b64Lookup[c] = i
    }
}

func decodeVLQMappings(mappings string) [][]Segment {
    var lines [][]Segment
    var currentLine []Segment

    // State: accumulated column/source/line/name offsets (VLQ values are relative)
    var genCol, srcIdx, srcLine, srcCol, nameIdx int

    i := 0
    for i <= len(mappings) {
        if i == len(mappings) || mappings[i] == ';' {
            lines = append(lines, currentLine)
            currentLine = nil
            genCol = 0 // Reset column within new line
            i++
            continue
        }

        if mappings[i] == ',' {
            i++
            continue
        }

        // Decode 1, 4, or 5 VLQ values for this segment
        seg := Segment{}

        // Field 1: Generated column (always present)
        val, newI := decodeVLQ(mappings, i)
        genCol += val
        seg.GeneratedCol = genCol
        i = newI

        // Check if there are more fields
        if i < len(mappings) && mappings[i] != ',' && mappings[i] != ';' {
            seg.HasSource = true

            // Field 2: Source file index
            val, newI = decodeVLQ(mappings, i)
            srcIdx += val
            seg.SourceIndex = srcIdx
            i = newI

            // Field 3: Original line
            val, newI = decodeVLQ(mappings, i)
            srcLine += val
            seg.SourceLine = srcLine
            i = newI

            // Field 4: Original column
            val, newI = decodeVLQ(mappings, i)
            srcCol += val
            seg.SourceCol = srcCol
            i = newI

            // Field 5: Name index (optional)
            if i < len(mappings) && mappings[i] != ',' && mappings[i] != ';' {
                seg.HasName = true
                val, newI = decodeVLQ(mappings, i)
                nameIdx += val
                seg.NameIndex = nameIdx
                i = newI
            }
        }

        currentLine = append(currentLine, seg)
    }

    return lines
}

func decodeVLQ(s string, start int) (int, int) {
    value := 0
    shift := 0
    i := start

    for i < len(s) {
        c := s[i]
        if int(c) >= len(b64Lookup) {
            break
        }
        digit := b64Lookup[c]
        if digit == -1 {
            break
        }
        i++

        continuation := digit & 0x20
        digit &= 0x1F
        value |= digit << shift
        shift += 5

        if continuation == 0 {
            break
        }
    }

    // Sign is in the least significant bit
    if value&1 != 0 {
        value = -(value >> 1)
    } else {
        value = value >> 1
    }

    return value, i
}
```

## 6.4 Framework-Specific Attribution

CDPs report low-level function names (often mangled by bundlers). PEN maps these to framework-level concepts:

### React Attribution

```go
package framework

// ReactAttributor identifies React-specific patterns in call stacks.
type ReactAttributor struct{}

func (r *ReactAttributor) Identify(functionName string, source string) *Attribution {
    // React component render detection
    if strings.HasPrefix(functionName, "render") ||
       strings.Contains(source, ".tsx") || strings.Contains(source, ".jsx") {
        return &Attribution{
            Framework: "react",
            Type:      "component-render",
        }
    }

    // React hook detection
    hookPatterns := []struct{ prefix, hookType string }{
        {"useState", "state-hook"},
        {"useEffect", "effect-hook"},
        {"useMemo", "memo-hook"},
        {"useCallback", "callback-hook"},
        {"useRef", "ref-hook"},
        {"useContext", "context-hook"},
        {"useReducer", "reducer-hook"},
    }
    for _, p := range hookPatterns {
        if strings.HasPrefix(functionName, p.prefix) {
            return &Attribution{
                Framework: "react",
                Type:      p.hookType,
                HookName:  functionName,
            }
        }
    }

    // React internal patterns (fiber scheduler, reconciler, etc.)
    internalPatterns := []string{
        "workLoopSync", "performUnitOfWork", "beginWork",
        "completeWork", "commitRoot", "flushPassiveEffects",
    }
    for _, pat := range internalPatterns {
        if strings.Contains(functionName, pat) {
            return &Attribution{
                Framework: "react",
                Type:      "internal",
                Internal:  functionName,
            }
        }
    }

    return nil
}
```

### Full Resolution Pipeline

Combining source maps and framework attribution, the complete pipeline:

```go
// ResolveCallStack takes a CDP call stack (generated positions) and resolves
// each frame to its original source position with framework attribution.
func (resolver *Resolver) ResolveCallStack(frames []cdp.CallFrame) []ResolvedFrame {
    resolved := make([]ResolvedFrame, 0, len(frames))

    for _, frame := range frames {
        r := ResolvedFrame{
            Raw: frame,
        }

        // 1. Find the source map for this script
        sm := resolver.registry.GetSourceMap(frame.URL)
        if sm != nil {
            // 2. Resolve generated position → original position
            orig := sm.Lookup(frame.LineNumber, frame.ColumnNumber)
            if orig != nil {
                r.OriginalSource = orig.Source
                r.OriginalLine = orig.Line
                r.OriginalColumn = orig.Column
                r.OriginalName = orig.Name
            }
        }

        // 3. Framework attribution
        funcName := frame.FunctionName
        if r.OriginalName != "" {
            funcName = r.OriginalName
        }
        r.Attribution = resolver.attributor.Identify(funcName, r.OriginalSource)

        resolved = append(resolved, r)
    }

    return resolved
}
```

## 6.5 Hot Reload Awareness

Modern dev servers (Vite, Next.js, Webpack) perform Hot Module Replacement (HMR). When source files change, bundle outputs change, and source maps are regenerated. PEN must invalidate its source map cache.

### File System Watching with fsnotify

```go
package sourcemap

import (
    "log/slog"
    "path/filepath"
    "strings"
    "sync"

    "github.com/fsnotify/fsnotify"
)

type HotReloadWatcher struct {
    watcher  *fsnotify.Watcher
    cache    *SourceMapCache
    mu       sync.Mutex
    watching map[string]bool
}

func NewHotReloadWatcher(cache *SourceMapCache) (*HotReloadWatcher, error) {
    w, err := fsnotify.NewWatcher()
    if err != nil {
        return nil, err
    }

    hrw := &HotReloadWatcher{
        watcher:  w,
        cache:    cache,
        watching: make(map[string]bool),
    }

    go hrw.processEvents()
    return hrw, nil
}

func (h *HotReloadWatcher) WatchDir(dir string) error {
    h.mu.Lock()
    defer h.mu.Unlock()

    if h.watching[dir] {
        return nil
    }

    if err := h.watcher.Add(dir); err != nil {
        return err
    }
    h.watching[dir] = true
    return nil
}

func (h *HotReloadWatcher) processEvents() {
    for {
        select {
        case event, ok := <-h.watcher.Events:
            if !ok {
                return
            }

            // Only care about .map files being written or renamed
            if !isSourceMapFile(event.Name) {
                continue
            }

            if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
                slog.Info("source map changed, invalidating cache", "file", event.Name)
                h.cache.InvalidateByMapFile(event.Name)
            }

        case err, ok := <-h.watcher.Errors:
            if !ok {
                return
            }
            slog.Warn("fsnotify error", "err", err)
        }
    }
}

func isSourceMapFile(name string) bool {
    ext := filepath.Ext(name)
    return ext == ".map" || strings.HasSuffix(name, ".js.map") || strings.HasSuffix(name, ".css.map")
}
```

### Debugger.scriptParsed Re-Discovery

When Chrome loads a new version of a script after HMR, it emits a new `scriptParsed` event. PEN's `SourceMapRegistry.Listen()` (section 6.2) automatically picks up the new source map URL, replacing the stale entry.

## 6.6 Source Map Cache

Source maps are cached with LRU eviction to avoid repeated parsing:

```go
type SourceMapCache struct {
    mu      sync.RWMutex
    entries map[string]*cacheEntry
    order   []string
    maxSize int
}

type cacheEntry struct {
    sm       *SourceMap
    mapFile  string // file path of the .map file (for invalidation)
    loadedAt time.Time
}

func (c *SourceMapCache) Get(url string) *SourceMap {
    c.mu.RLock()
    defer c.mu.RUnlock()
    if entry, ok := c.entries[url]; ok {
        return entry.sm
    }
    return nil
}

func (c *SourceMapCache) InvalidateByMapFile(mapFile string) {
    c.mu.Lock()
    defer c.mu.Unlock()

    for url, entry := range c.entries {
        if entry.mapFile == mapFile {
            delete(c.entries, url)
            slog.Debug("invalidated source map cache", "url", url, "mapFile", mapFile)
        }
    }
}
```

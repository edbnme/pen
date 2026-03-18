package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/chromedp/cdproto/debugger"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
	"github.com/edbnme/pen/internal/server"
)

// scriptEntry stores parsed script information.
type scriptEntry struct {
	ScriptID     string
	URL          string
	SourceMapURL string
	IsModule     bool
	StartLine    int64
	EndLine      int64
	Length       int64
	Hash         string
}

const maxScripts = 500 // Limit cached scripts to prevent unbounded memory growth.

// scriptStore caches parsed scripts for quick lookup.
var scriptStore = struct {
	mu      sync.RWMutex
	scripts map[string]*scriptEntry // scriptID → entry
	order   []string                // Insertion order for eviction.
	active  bool
}{scripts: make(map[string]*scriptEntry)}

// scriptListenerOnce ensures the CDP event listener is registered at most once.
var scriptListenerOnce sync.Once

func registerSourceTools(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_list_sources",
		Description: "List all parsed JavaScript sources in the page. Enables the Debugger domain and captures ScriptParsed events.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "List Sources",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makeListSourcesHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_source_content",
		Description: "Get the source code of a specific script by script ID or URL pattern.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Source Content",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makeSourceContentHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_search_source",
		Description: "Search across all loaded scripts for a string or pattern.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Search Source",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makeSearchSourceHandler(deps))
}

// --- pen_list_sources ---

type listSourcesInput struct {
	Refresh bool   `json:"refresh,omitempty" jsonschema:"Re-enable debugger to capture fresh script list (default false)"`
	Filter  string `json:"filter"  jsonschema:"Filter scripts by URL substring"`
}

func makeListSourcesHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, listSourcesInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input listSourcesInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		// If not active or refresh requested, enable debugger.
		scriptStore.mu.RLock()
		needsInit := !scriptStore.active || input.Refresh
		scriptStore.mu.RUnlock()

		if needsInit {
			server.NotifyProgress(ctx, req, 0, 100, "Enabling debugger and collecting scripts...")

			scriptStore.mu.Lock()
			if input.Refresh {
				scriptStore.scripts = make(map[string]*scriptEntry)
				scriptStore.order = nil
			}
			scriptStore.active = true
			scriptStore.mu.Unlock()

			// Register the listener only once to prevent accumulation.
			scriptListenerOnce.Do(func() {
				chromedp.ListenTarget(cdpCtx, func(ev interface{}) {
					if e, ok := ev.(*debugger.EventScriptParsed); ok {
						scriptStore.mu.Lock()
						defer scriptStore.mu.Unlock()
						id := string(e.ScriptID)
						if _, exists := scriptStore.scripts[id]; !exists {
							scriptStore.order = append(scriptStore.order, id)
						}
						scriptStore.scripts[id] = &scriptEntry{
							ScriptID:     id,
							URL:          e.URL,
							SourceMapURL: e.SourceMapURL,
							IsModule:     e.IsModule,
							StartLine:    e.StartLine,
							EndLine:      e.EndLine,
							Length:       e.Length,
							Hash:         e.Hash,
						}
						// Evict oldest entries when over limit.
						for len(scriptStore.order) > maxScripts {
							oldest := scriptStore.order[0]
							scriptStore.order = scriptStore.order[1:]
							delete(scriptStore.scripts, oldest)
						}
					}
				})
			})

			// Enable debugger — this triggers ScriptParsed for all known scripts.
			err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				_, err := debugger.Enable().Do(ctx)
				return err
			}))
			if err != nil {
				return toolError("failed to enable debugger: " + err.Error())
			}
		}

		server.NotifyProgress(ctx, req, 80, 100, "Formatting script list...")

		// Collect and filter.
		scriptStore.mu.RLock()
		scripts := make([]*scriptEntry, 0, len(scriptStore.scripts))
		for _, s := range scriptStore.scripts {
			if s.URL == "" {
				continue // Skip eval/internal scripts without URLs.
			}
			if input.Filter != "" && !strings.Contains(s.URL, input.Filter) {
				continue
			}
			scripts = append(scripts, s)
		}
		scriptStore.mu.RUnlock()

		// Sort by URL.
		sort.Slice(scripts, func(i, j int) bool {
			return scripts[i].URL < scripts[j].URL
		})

		headers := []string{"#", "ID", "URL", "Size", "Module", "SourceMap"}
		rows := make([][]string, 0, len(scripts))
		for i, s := range scripts {
			url := s.URL
			if len(url) > 60 {
				url = "…" + url[len(url)-59:]
			}
			hasMap := "no"
			if s.SourceMapURL != "" {
				hasMap = "yes"
			}
			module := "no"
			if s.IsModule {
				module = "yes"
			}
			rows = append(rows, []string{
				fmt.Sprintf("%d", i+1),
				s.ScriptID,
				url,
				format.Bytes(s.Length),
				module,
				hasMap,
			})
		}

		output := format.ToolResult("Parsed Scripts",
			format.Summary([][2]string{
				{"Total Scripts", fmt.Sprintf("%d", len(scripts))},
			}),
			"",
			format.Table(headers, rows),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- pen_source_content ---

type sourceContentInput struct {
	ScriptID   string `json:"scriptID"   jsonschema:"Script ID from pen_list_sources"`
	URLPattern string `json:"urlPattern" jsonschema:"URL substring to match (first match used)"`
	MaxLines   int    `json:"maxLines"   jsonschema:"Truncate output after N lines (default 200)"`
}

func makeSourceContentHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, sourceContentInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input sourceContentInput) (*mcp.CallToolResult, any, error) {
		if input.ScriptID == "" && input.URLPattern == "" {
			return toolError("provide scriptID or urlPattern")
		}

		// Find the script.
		var targetID cdpruntime.ScriptID
		scriptStore.mu.RLock()
		if input.ScriptID != "" {
			if _, ok := scriptStore.scripts[input.ScriptID]; ok {
				targetID = cdpruntime.ScriptID(input.ScriptID)
			}
		} else {
			for _, s := range scriptStore.scripts {
				if strings.Contains(s.URL, input.URLPattern) {
					targetID = cdpruntime.ScriptID(s.ScriptID)
					break
				}
			}
		}
		scriptStore.mu.RUnlock()

		if targetID == "" {
			return toolError("no matching script found. Run pen_list_sources first.")
		}

		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		var source string
		err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			var getErr error
			source, _, getErr = debugger.GetScriptSource(targetID).Do(ctx)
			return getErr
		}))
		if err != nil {
			return toolError("failed to get script source: " + err.Error())
		}

		maxLines := input.MaxLines
		if maxLines <= 0 {
			maxLines = 200
		}

		lines := strings.Split(source, "\n")
		truncated := false
		if len(lines) > maxLines {
			lines = lines[:maxLines]
			truncated = true
		}

		var b strings.Builder
		b.WriteString(fmt.Sprintf("Script: %s\nLines: %d", string(targetID), len(strings.Split(source, "\n"))))
		if truncated {
			b.WriteString(fmt.Sprintf(" (showing first %d)", maxLines))
		}
		b.WriteString("\n\n```javascript\n")
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteString("\n```")
		if truncated {
			b.WriteString(fmt.Sprintf("\n\n… truncated at %d lines", maxLines))
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
		}, nil, nil
	}
}

// --- pen_search_source ---

type searchSourceInput struct {
	Query         string `json:"query"         jsonschema:"Search query string"`
	IsRegex       bool   `json:"isRegex,omitempty"       jsonschema:"Treat query as a regex pattern"`
	CaseSensitive bool   `json:"caseSensitive,omitempty" jsonschema:"Case-sensitive search (default false)"`
	MaxResults    int    `json:"maxResults"     jsonschema:"Maximum results across all scripts (default 50)"`
}

func makeSearchSourceHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, searchSourceInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input searchSourceInput) (*mcp.CallToolResult, any, error) {
		if input.Query == "" {
			return toolError("query is required")
		}
		if input.MaxResults <= 0 {
			input.MaxResults = 50
		}

		scriptStore.mu.RLock()
		scriptIDs := make([]string, 0, len(scriptStore.scripts))
		urlMap := make(map[string]string) // scriptID → URL
		for id, s := range scriptStore.scripts {
			if s.URL != "" {
				scriptIDs = append(scriptIDs, id)
				urlMap[id] = s.URL
			}
		}
		scriptStore.mu.RUnlock()

		if len(scriptIDs) == 0 {
			return toolError("no scripts loaded. Run pen_list_sources first.")
		}

		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 0, 100, "Searching across scripts...")

		type searchResult struct {
			ScriptURL string
			Line      int64
			Content   string
		}
		var results []searchResult

		for _, sid := range scriptIDs {
			if len(results) >= input.MaxResults {
				break
			}

			var matches []*debugger.SearchMatch
			err := chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				var searchErr error
				matches, searchErr = debugger.SearchInContent(cdpruntime.ScriptID(sid), input.Query).
					WithCaseSensitive(input.CaseSensitive).
					WithIsRegex(input.IsRegex).
					Do(ctx)
				return searchErr
			}))
			if err != nil {
				continue // Skip scripts that error (e.g., stale references).
			}

			url := urlMap[sid]
			for _, m := range matches {
				if len(results) >= input.MaxResults {
					break
				}
				content := m.LineContent
				if len(content) > 120 {
					content = content[:117] + "…"
				}
				results = append(results, searchResult{
					ScriptURL: url,
					Line:      int64(m.LineNumber),
					Content:   content,
				})
			}
		}

		if len(results) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("No matches found for %q across %d scripts.", input.Query, len(scriptIDs))}},
			}, nil, nil
		}

		headers := []string{"#", "Script", "Line", "Content"}
		rows := make([][]string, 0, len(results))
		for i, r := range results {
			url := r.ScriptURL
			if len(url) > 50 {
				url = "…" + url[len(url)-49:]
			}
			rows = append(rows, []string{
				fmt.Sprintf("%d", i+1),
				url,
				fmt.Sprintf("%d", r.Line),
				r.Content,
			})
		}

		output := format.ToolResult("Source Search Results",
			format.Summary([][2]string{
				{"Query", input.Query},
				{"Matches", fmt.Sprintf("%d (across %d scripts)", len(results), len(scriptIDs))},
			}),
			"",
			format.Table(headers, rows),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

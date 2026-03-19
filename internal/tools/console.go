package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
)

// consoleEntry stores a captured console message or exception.
type consoleEntry struct {
	ID         int
	Level      string  // "error", "warning", "log", "info", "debug"
	Text       string  // Formatted message text
	URL        string  // Source URL
	Line       int64   // Source line number
	Column     int64   // Source column number
	Timestamp  float64 // Monotonic timestamp
	IsError    bool    // true if from Runtime.exceptionThrown
	StackTrace string  // Stack trace for errors (if available)
}

const maxConsoleEntries = 1000

// consoleStore holds captured console messages for the current session.
var consoleStore = struct {
	mu      sync.RWMutex
	entries []*consoleEntry
	active  bool
	nextID  int
}{entries: make([]*consoleEntry, 0)}

// consoleListenerOnce ensures the CDP event listener is registered at most once.
var consoleListenerOnce sync.Once

// ResetConsoleListener allows re-registration of the CDP console event
// listener after a reconnect (the old listener dies with the old context).
func ResetConsoleListener() {
	consoleListenerOnce = sync.Once{}
	consoleStore.mu.Lock()
	consoleStore.entries = make([]*consoleEntry, 0)
	consoleStore.active = false
	consoleStore.nextID = 0
	consoleStore.mu.Unlock()
}

func registerConsoleTools(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_console_enable",
		Description: "Start capturing console messages and exceptions. Must be called before pen_console_messages. Messages emitted before enabling are not captured.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Enable Console Capture",
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(false),
		},
	}, makeConsoleEnableHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_console_messages",
		Description: "List captured console messages with level, text, source URL, and timestamp. Filter by level (error, warning, log, info, debug) or text substring. Requires pen_console_enable first.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Console Messages",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makeConsoleMessagesHandler(deps))
}

// --- pen_console_enable ---

type consoleEnableInput struct {
	ClearFirst bool `json:"clearFirst,omitempty" jsonschema:"Clear existing messages before starting (default false)"`
}

func makeConsoleEnableHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, consoleEnableInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input consoleEnableInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		consoleStore.mu.Lock()
		if input.ClearFirst || !consoleStore.active {
			consoleStore.entries = make([]*consoleEntry, 0)
			consoleStore.nextID = 0
		}
		consoleStore.active = true
		consoleStore.mu.Unlock()

		// Enable Runtime domain (needed for console events).
		enableCtx, enableCancel := context.WithTimeout(cdpCtx, cdpEnableTimeout)
		defer enableCancel()
		err = chromedp.Run(enableCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			return cdpruntime.Enable().Do(ctx)
		}))
		if err != nil {
			return toolError("failed to enable runtime: " + err.Error())
		}

		// Register listener once.
		consoleListenerOnce.Do(func() {
			chromedp.ListenTarget(cdpCtx, func(ev interface{}) {
				consoleStore.mu.Lock()
				defer consoleStore.mu.Unlock()

				if !consoleStore.active {
					return
				}

				switch e := ev.(type) {
				case *cdpruntime.EventConsoleAPICalled:
					// Format args into readable text.
					var parts []string
					for _, arg := range e.Args {
						if arg.Value != nil {
							parts = append(parts, string(arg.Value))
						} else if arg.Description != "" {
							parts = append(parts, arg.Description)
						} else {
							parts = append(parts, arg.Type.String())
						}
					}

					text := strings.Join(parts, " ")
					if len(text) > 2000 {
						text = text[:2000] + "…(truncated)"
					}

					var stackStr string
					if e.StackTrace != nil && len(e.StackTrace.CallFrames) > 0 {
						stackStr = formatStackTrace(e.StackTrace)
					}

					// Source location from first stack frame.
					var url string
					var line, col int64
					if e.StackTrace != nil && len(e.StackTrace.CallFrames) > 0 {
						f := e.StackTrace.CallFrames[0]
						url = f.URL
						line = f.LineNumber
						col = f.ColumnNumber
					}

					var timestamp float64
					if e.Timestamp != nil {
						timestamp = float64(e.Timestamp.Time().UnixNano()) / 1e9
					}

					entry := &consoleEntry{
						ID:         consoleStore.nextID,
						Level:      mapConsoleType(e.Type),
						Text:       text,
						URL:        url,
						Line:       line,
						Column:     col,
						Timestamp:  timestamp,
						StackTrace: stackStr,
					}
					consoleStore.nextID++
					appendConsoleEntry(entry)

				case *cdpruntime.EventExceptionThrown:
					ed := e.ExceptionDetails
					text := ed.Text
					if ed.Exception != nil && ed.Exception.Description != "" {
						text = ed.Exception.Description
					}
					if len(text) > 2000 {
						text = text[:2000] + "…(truncated)"
					}

					var stackStr string
					if ed.StackTrace != nil && len(ed.StackTrace.CallFrames) > 0 {
						stackStr = formatStackTrace(ed.StackTrace)
					}

					var timestamp float64
					if e.Timestamp != nil {
						timestamp = float64(e.Timestamp.Time().UnixNano()) / 1e9
					}

					entry := &consoleEntry{
						ID:         consoleStore.nextID,
						Level:      "error",
						Text:       text,
						URL:        ed.URL,
						Line:       int64(ed.LineNumber),
						Column:     int64(ed.ColumnNumber),
						Timestamp:  timestamp,
						IsError:    true,
						StackTrace: stackStr,
					}
					consoleStore.nextID++
					appendConsoleEntry(entry)
				}
			})
		})

		consoleStore.mu.RLock()
		count := len(consoleStore.entries)
		consoleStore.mu.RUnlock()

		output := format.ToolResult("Console Capture Enabled",
			format.Summary([][2]string{
				{"Status", "Active"},
				{"Messages buffered", fmt.Sprintf("%d", count)},
			}),
		)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- pen_console_messages ---

type consoleMessagesInput struct {
	Level      string `json:"level,omitempty"      jsonschema:"Filter by level: error, warning, log, info, debug (default: all)"`
	TextFilter string `json:"textFilter,omitempty" jsonschema:"Filter by case-insensitive substring match on message text"`
	Last       int    `json:"last,omitempty"       jsonschema:"Return only the N most recent messages (default: all, max 200)"`
	Clear      bool   `json:"clear,omitempty"      jsonschema:"Clear messages after reading (default false)"`
}

func makeConsoleMessagesHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, consoleMessagesInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input consoleMessagesInput) (*mcp.CallToolResult, any, error) {
		consoleStore.mu.RLock()
		if !consoleStore.active {
			consoleStore.mu.RUnlock()
			return toolError("Console capture not active. Call pen_console_enable first.")
		}

		// Copy entries under read lock.
		entries := make([]*consoleEntry, len(consoleStore.entries))
		copy(entries, consoleStore.entries)
		consoleStore.mu.RUnlock()

		// Filter by level.
		if input.Level != "" {
			level := strings.ToLower(input.Level)
			filtered := entries[:0]
			for _, e := range entries {
				if e.Level == level {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}

		// Filter by text substring (case-insensitive).
		if input.TextFilter != "" {
			needle := strings.ToLower(input.TextFilter)
			filtered := make([]*consoleEntry, 0)
			for _, e := range entries {
				if strings.Contains(strings.ToLower(e.Text), needle) {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}

		// Apply "last N".
		if input.Last > 0 {
			if input.Last > 200 {
				input.Last = 200
			}
			if len(entries) > input.Last {
				entries = entries[len(entries)-input.Last:]
			}
		}

		if len(entries) == 0 {
			msg := "No console messages captured."
			if input.Level != "" {
				msg = fmt.Sprintf("No %q messages captured.", input.Level)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: msg}},
			}, nil, nil
		}

		// Build table.
		headers := []string{"#", "Level", "Text", "Source"}
		rows := make([][]string, 0, len(entries))
		for _, e := range entries {
			text := e.Text
			if len(text) > 120 {
				text = text[:117] + "..."
			}
			src := ""
			if e.URL != "" {
				src = e.URL
				if e.Line > 0 {
					src = fmt.Sprintf("%s:%d", e.URL, e.Line)
				}
				if len(src) > 50 {
					src = "…" + src[len(src)-49:]
				}
			}

			levelStr := e.Level
			if e.IsError {
				levelStr = "ERROR"
			}

			rows = append(rows, []string{
				fmt.Sprintf("%d", e.ID),
				levelStr,
				text,
				src,
			})
		}

		consoleStore.mu.RLock()
		totalCount := len(consoleStore.entries)
		consoleStore.mu.RUnlock()

		output := format.ToolResult("Console Messages",
			format.Summary([][2]string{
				{"Showing", fmt.Sprintf("%d of %d", len(entries), totalCount)},
			}),
			"",
			format.Table(headers, rows),
		)

		// Clear if requested.
		if input.Clear {
			consoleStore.mu.Lock()
			consoleStore.entries = make([]*consoleEntry, 0)
			consoleStore.nextID = 0
			consoleStore.mu.Unlock()
			output += "\n" + format.Warning("Console messages cleared.")
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- Helper functions ---

// appendConsoleEntry adds an entry, evicting oldest if over capacity.
// MUST be called while holding consoleStore.mu write lock.
func appendConsoleEntry(entry *consoleEntry) {
	if len(consoleStore.entries) >= maxConsoleEntries {
		// Drop oldest 100 entries to avoid frequent eviction.
		consoleStore.entries = consoleStore.entries[100:]
	}
	consoleStore.entries = append(consoleStore.entries, entry)
}

// mapConsoleType converts cdpruntime.APIType to a simple level string.
func mapConsoleType(t cdpruntime.APIType) string {
	switch t {
	case cdpruntime.APITypeLog:
		return "log"
	case cdpruntime.APITypeWarning:
		return "warning"
	case cdpruntime.APITypeError:
		return "error"
	case cdpruntime.APITypeInfo:
		return "info"
	case cdpruntime.APITypeDebug:
		return "debug"
	default:
		return "log"
	}
}

// formatStackTrace formats a runtime.StackTrace into readable text.
func formatStackTrace(st *cdpruntime.StackTrace) string {
	var b strings.Builder
	for _, f := range st.CallFrames {
		name := f.FunctionName
		if name == "" {
			name = "(anonymous)"
		}
		fmt.Fprintf(&b, "  at %s (%s:%d:%d)\n", name, f.URL, f.LineNumber, f.ColumnNumber)
	}
	return b.String()
}

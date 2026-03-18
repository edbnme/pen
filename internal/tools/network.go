package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
)

// networkEntry stores a captured network request/response pair.
type networkEntry struct {
	RequestID  string
	URL        string
	Method     string
	Status     int64
	MimeType   string
	Size       float64 // encoded data length
	StartTime  float64 // monotonic timestamp
	EndTime    float64
	Timing     *network.ResourceTiming
	Priority   string
	Initiator  string
	Protocol   string
	FromCache  bool
	Failed     bool
	FailReason string
}

// networkStore holds captured network entries for the current session.
var networkStore = struct {
	mu      sync.RWMutex
	entries map[string]*networkEntry // requestID → entry
	active  bool
}{entries: make(map[string]*networkEntry)}

// networkListenerOnce ensures the CDP event listener is registered at most once.
var networkListenerOnce sync.Once

// ResetNetworkListener allows re-registration of the CDP network event
// listener after a reconnect (the old listener dies with the old context).
func ResetNetworkListener() {
	networkListenerOnce = sync.Once{}
	networkStore.mu.Lock()
	networkStore.entries = make(map[string]*networkEntry)
	networkStore.active = false
	networkStore.mu.Unlock()
}

func registerNetworkTools(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_network_enable",
		Description: "Start capturing network requests. Must be called before pen_network_waterfall. Optionally disable cache.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Enable Network Capture",
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(false),
		},
	}, makeNetworkEnableHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_network_waterfall",
		Description: "Show captured network requests as a waterfall table with timing, size, and status. Filter by MIME type, status code (4xx, 5xx, error), or URL substring. Requires pen_network_enable first.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Network Waterfall",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makeNetworkWaterfallHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_network_request",
		Description: "Get details of a specific captured network request by URL pattern or request ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Network Request Detail",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makeNetworkRequestHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_network_blocking",
		Description: "Identify render-blocking resources: synchronous scripts, blocking stylesheets, and large assets.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Blocking Resources",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makeNetworkBlockingHandler(deps))
}

// --- pen_network_enable ---

type networkEnableInput struct {
	DisableCache *bool `json:"disableCache,omitempty" jsonschema:"Disable browser cache during capture (default true)"`
	ClearFirst   *bool `json:"clearFirst,omitempty"   jsonschema:"Clear previously captured entries (default true)"`
}

func makeNetworkEnableHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, networkEnableInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input networkEnableInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		// Default both to true when omitted.
		disableCache := input.DisableCache == nil || *input.DisableCache
		clearFirst := input.ClearFirst == nil || *input.ClearFirst

		networkStore.mu.Lock()
		if clearFirst || !networkStore.active {
			networkStore.entries = make(map[string]*networkEntry)
		}
		networkStore.active = true
		networkStore.mu.Unlock()

		// Enable network domain.
		err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			if err := network.Enable().Do(ctx); err != nil {
				return fmt.Errorf("network.Enable: %w", err)
			}
			if disableCache {
				return network.SetCacheDisabled(true).Do(ctx)
			}
			return nil
		}))
		if err != nil {
			return toolError("failed to enable network: " + err.Error())
		}

		// Register network event listener only once to prevent accumulation.
		networkListenerOnce.Do(func() {
			chromedp.ListenTarget(cdpCtx, func(ev interface{}) {
				networkStore.mu.Lock()
				defer networkStore.mu.Unlock()

				switch e := ev.(type) {
				case *network.EventRequestWillBeSent:
					rid := string(e.RequestID)
					entry := &networkEntry{
						RequestID: rid,
						URL:       e.Request.URL,
						Method:    e.Request.Method,
						StartTime: float64(e.Timestamp.Time().UnixNano()) / 1e9,
						Priority:  e.Request.InitialPriority.String(),
					}
					if e.Initiator != nil {
						entry.Initiator = e.Initiator.Type.String()
					}
					networkStore.entries[rid] = entry

				case *network.EventResponseReceived:
					rid := string(e.RequestID)
					if entry, ok := networkStore.entries[rid]; ok {
						entry.Status = e.Response.Status
						entry.MimeType = e.Response.MimeType
						entry.Protocol = e.Response.Protocol
						entry.FromCache = e.Response.FromDiskCache || e.Response.FromPrefetchCache
						if e.Response.Timing != nil {
							entry.Timing = e.Response.Timing
						}
					}

				case *network.EventLoadingFinished:
					rid := string(e.RequestID)
					if entry, ok := networkStore.entries[rid]; ok {
						entry.EndTime = float64(e.Timestamp.Time().UnixNano()) / 1e9
						entry.Size = e.EncodedDataLength
					}

				case *network.EventLoadingFailed:
					rid := string(e.RequestID)
					if entry, ok := networkStore.entries[rid]; ok {
						entry.EndTime = float64(e.Timestamp.Time().UnixNano()) / 1e9
						entry.Failed = true
						entry.FailReason = e.ErrorText
					}
				}
			})
		})

		networkStore.mu.RLock()
		count := len(networkStore.entries)
		networkStore.mu.RUnlock()

		msg := "Network capture enabled."
		if disableCache {
			msg += " Cache disabled."
		}
		msg += fmt.Sprintf(" %d entries in buffer.", count)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		}, nil, nil
	}
}

// --- pen_network_waterfall ---

type networkWaterfallInput struct {
	SortBy       string `json:"sortBy"                jsonschema:"Sort by: time (default), size, status, duration"`
	Filter       string `json:"filter"                jsonschema:"Filter by MIME type prefix, e.g. 'image/', 'text/javascript'"`
	StatusFilter string `json:"statusFilter,omitempty" jsonschema:"Filter by status: '4xx' (400-499), '5xx' (500-599), 'error' (all failures), or exact code like '404'"`
	URLFilter    string `json:"urlFilter,omitempty"    jsonschema:"Filter by URL substring (case-insensitive)"`
	Limit        int    `json:"limit"                 jsonschema:"Max entries to show (default 50)"`
}

func makeNetworkWaterfallHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, networkWaterfallInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input networkWaterfallInput) (*mcp.CallToolResult, any, error) {
		networkStore.mu.RLock()
		if !networkStore.active {
			networkStore.mu.RUnlock()
			return toolError("Network capture not active. Call pen_network_enable first.")
		}

		entries := make([]*networkEntry, 0, len(networkStore.entries))
		for _, e := range networkStore.entries {
			entries = append(entries, e)
		}
		networkStore.mu.RUnlock()

		if len(entries) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "No network requests captured yet. Navigate or reload the page."}},
			}, nil, nil
		}

		// Filter by MIME type.
		if input.Filter != "" {
			filtered := entries[:0]
			for _, e := range entries {
				if strings.HasPrefix(e.MimeType, input.Filter) {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}

		// Filter by status code range.
		if input.StatusFilter != "" {
			sf := strings.ToLower(input.StatusFilter)
			filtered := make([]*networkEntry, 0)
			for _, e := range entries {
				switch sf {
				case "4xx":
					if e.Status >= 400 && e.Status < 500 {
						filtered = append(filtered, e)
					}
				case "5xx":
					if e.Status >= 500 && e.Status < 600 {
						filtered = append(filtered, e)
					}
				case "error":
					if e.Failed || e.Status >= 400 {
						filtered = append(filtered, e)
					}
				default:
					// Exact status code match.
					if fmt.Sprintf("%d", e.Status) == sf {
						filtered = append(filtered, e)
					}
				}
			}
			entries = filtered
		}

		// Filter by URL substring.
		if input.URLFilter != "" {
			needle := strings.ToLower(input.URLFilter)
			filtered := make([]*networkEntry, 0)
			for _, e := range entries {
				if strings.Contains(strings.ToLower(e.URL), needle) {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}

		// Sort.
		switch input.SortBy {
		case "size":
			sort.Slice(entries, func(i, j int) bool { return entries[i].Size > entries[j].Size })
		case "status":
			sort.Slice(entries, func(i, j int) bool { return entries[i].Status < entries[j].Status })
		case "duration":
			sort.Slice(entries, func(i, j int) bool { return entryDuration(entries[i]) > entryDuration(entries[j]) })
		default: // "time"
			sort.Slice(entries, func(i, j int) bool { return entries[i].StartTime < entries[j].StartTime })
		}

		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		if len(entries) > limit {
			entries = entries[:limit]
		}

		// Build table.
		headers := []string{"#", "Status", "Method", "URL", "Type", "Size", "Duration"}
		rows := make([][]string, 0, len(entries))
		var totalSize float64
		for i, e := range entries {
			url := e.URL
			if len(url) > 70 {
				url = url[:67] + "…"
			}
			statusStr := fmt.Sprintf("%d", e.Status)
			if e.Failed {
				statusStr = "FAIL"
			}
			if e.FromCache {
				statusStr += " (cache)"
			}
			dur := entryDuration(e)
			totalSize += e.Size
			rows = append(rows, []string{
				fmt.Sprintf("%d", i+1),
				statusStr,
				e.Method,
				url,
				simpleMime(e.MimeType),
				format.Bytes(int64(e.Size)),
				fmt.Sprintf("%.0fms", dur),
			})
		}

		networkStore.mu.RLock()
		totalEntries := len(networkStore.entries)
		networkStore.mu.RUnlock()

		output := format.ToolResult("Network Waterfall",
			format.Summary([][2]string{
				{"Total Requests", fmt.Sprintf("%d", totalEntries)},
				{"Showing", fmt.Sprintf("%d", len(entries))},
				{"Total Transfer", format.Bytes(int64(totalSize))},
			}),
			"",
			format.Table(headers, rows),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- pen_network_request ---

type networkRequestInput struct {
	URLPattern string `json:"urlPattern" jsonschema:"URL substring to match"`
	RequestID  string `json:"requestID"  jsonschema:"Exact request ID from waterfall"`
}

func makeNetworkRequestHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, networkRequestInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input networkRequestInput) (*mcp.CallToolResult, any, error) {
		if input.URLPattern == "" && input.RequestID == "" {
			return toolError("provide urlPattern or requestID")
		}

		networkStore.mu.RLock()
		var entry *networkEntry
		if input.RequestID != "" {
			entry = networkStore.entries[input.RequestID]
		} else {
			for _, e := range networkStore.entries {
				if strings.Contains(e.URL, input.URLPattern) {
					entry = e
					break
				}
			}
		}
		networkStore.mu.RUnlock()

		if entry == nil {
			return toolError("no matching request found")
		}

		// Build detail view.
		kvPairs := [][2]string{
			{"Request ID", entry.RequestID},
			{"URL", entry.URL},
			{"Method", entry.Method},
			{"Status", fmt.Sprintf("%d", entry.Status)},
			{"MIME Type", entry.MimeType},
			{"Protocol", entry.Protocol},
			{"Priority", entry.Priority},
			{"Transfer Size", format.Bytes(int64(entry.Size))},
			{"Cached", fmt.Sprintf("%v", entry.FromCache)},
			{"Duration", fmt.Sprintf("%.1fms", entryDuration(entry))},
			{"Initiator", entry.Initiator},
		}

		if entry.Failed {
			kvPairs = append(kvPairs, [2]string{"Error", entry.FailReason})
		}

		var timingSection string
		if entry.Timing != nil {
			t := entry.Timing
			timingSection = format.Section("Resource Timing",
				format.KeyValue("DNS", fmt.Sprintf("%.1fms", t.DNSEnd-t.DNSStart)),
				format.KeyValue("Connect", fmt.Sprintf("%.1fms", t.ConnectEnd-t.ConnectStart)),
				format.KeyValue("SSL", fmt.Sprintf("%.1fms", t.SslEnd-t.SslStart)),
				format.KeyValue("Send", fmt.Sprintf("%.1fms", t.SendEnd-t.SendStart)),
				format.KeyValue("Wait (TTFB)", fmt.Sprintf("%.1fms", t.ReceiveHeadersEnd-t.SendEnd)),
			)
		}

		output := format.ToolResult("Network Request Detail",
			format.Summary(kvPairs),
			"",
			timingSection,
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- pen_network_blocking ---

type networkBlockingInput struct{}

func makeNetworkBlockingHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, networkBlockingInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, _ networkBlockingInput) (*mcp.CallToolResult, any, error) {
		networkStore.mu.RLock()
		if !networkStore.active {
			networkStore.mu.RUnlock()
			return toolError("Network capture not active. Call pen_network_enable first.")
		}

		entries := make([]*networkEntry, 0, len(networkStore.entries))
		for _, e := range networkStore.entries {
			entries = append(entries, e)
		}
		networkStore.mu.RUnlock()

		// Identify blocking resources.
		var blocking []string
		var largeAssets []string
		const largeThreshold = 100 * 1024 // 100KB

		for _, e := range entries {
			url := e.URL
			if len(url) > 80 {
				url = url[:77] + "…"
			}

			// Synchronous JS without async/defer markers.
			if isBlockingScript(e) {
				blocking = append(blocking,
					fmt.Sprintf("**Blocking JS**: %s (%s, %.0fms)", url, format.Bytes(int64(e.Size)), entryDuration(e)))
			}

			// Render-blocking CSS.
			if isBlockingCSS(e) {
				blocking = append(blocking,
					fmt.Sprintf("**Blocking CSS**: %s (%s, %.0fms)", url, format.Bytes(int64(e.Size)), entryDuration(e)))
			}

			// Large assets.
			if e.Size > largeThreshold && !e.FromCache {
				largeAssets = append(largeAssets,
					fmt.Sprintf("%s (%s) — %s", url, simpleMime(e.MimeType), format.Bytes(int64(e.Size))))
			}
		}

		var sections []string
		if len(blocking) > 0 {
			sections = append(sections,
				format.Section("Render-Blocking Resources", format.BulletList(blocking)))
		} else {
			sections = append(sections, "No obvious render-blocking resources detected.")
		}

		if len(largeAssets) > 0 {
			sort.Strings(largeAssets)
			if len(largeAssets) > 15 {
				largeAssets = largeAssets[:15]
			}
			sections = append(sections,
				format.Section("Large Uncached Assets (>100KB)", format.BulletList(largeAssets)))
		}

		output := format.ToolResult("Render-Blocking Analysis",
			format.Summary([][2]string{
				{"Total Requests", fmt.Sprintf("%d", len(entries))},
				{"Blocking Resources", fmt.Sprintf("%d", len(blocking))},
				{"Large Assets", fmt.Sprintf("%d", len(largeAssets))},
			}),
			"",
			strings.Join(sections, "\n\n"),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- helpers ---

func entryDuration(e *networkEntry) float64 {
	if e.EndTime <= 0 || e.StartTime <= 0 {
		return 0
	}
	return (e.EndTime - e.StartTime) * 1000 // seconds → ms
}

func simpleMime(mime string) string {
	parts := strings.Split(mime, "/")
	if len(parts) == 2 {
		return parts[1]
	}
	return mime
}

func isBlockingScript(e *networkEntry) bool {
	if e.MimeType == "" {
		return false
	}
	isJS := strings.Contains(e.MimeType, "javascript") || strings.Contains(e.MimeType, "ecmascript")
	if !isJS {
		return false
	}
	// Scripts loaded via parser are typically high priority.
	return e.Priority == "High" || e.Priority == "VeryHigh"
}

func isBlockingCSS(e *networkEntry) bool {
	return strings.Contains(e.MimeType, "css") &&
		(e.Priority == "VeryHigh" || e.Priority == "High") &&
		!e.FromCache
}

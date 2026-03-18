package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	cdpio "github.com/chromedp/cdproto/io"
	"github.com/chromedp/cdproto/profiler"
	"github.com/chromedp/cdproto/tracing"
	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
	"github.com/edbnme/pen/internal/security"
	"github.com/edbnme/pen/internal/server"
)

func registerCPUTools(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_cpu_profile",
		Description: "Record a V8 CPU profile for a given duration and analyze hot functions, call trees, and bottlenecks. Locks the Profiler domain during capture. For a quick overview instead, use pen_performance_metrics.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "CPU Profile",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, makeCPUProfileHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_capture_trace",
		Description: "Capture a Chrome trace (DevTools Timeline) for given categories and duration. Returns a downloadable trace file path. Use pen_trace_insights to analyze the captured trace, or load in chrome://tracing for visualization.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Capture Trace",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, makeCaptureTraceHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_trace_insights",
		Description: "Analyze a captured trace file for performance insights: long tasks, layout shifts (CLS), LCP candidates, resource bottlenecks, frame timing.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Trace Insights",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makeTraceInsightsHandler(deps))
}

// --- pen_cpu_profile ---

type cpuProfileInput struct {
	Duration   int `json:"duration"   jsonschema:"Profile duration in seconds (1-30, default 5)"`
	SampleRate int `json:"sampleRate" jsonschema:"Sampling interval in microseconds (default 100)"`
	TopN       int `json:"topN"       jsonschema:"Number of top hotspot functions to show (default 20)"`
}

func makeCPUProfileHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, cpuProfileInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input cpuProfileInput) (*mcp.CallToolResult, any, error) {
		// Defaults and bounds.
		if input.Duration <= 0 {
			input.Duration = 5
		}
		if input.Duration > 30 {
			input.Duration = 30
		}
		if input.SampleRate <= 0 {
			input.SampleRate = 100
		}
		if input.SampleRate < 10 {
			input.SampleRate = 10
		}
		if input.TopN <= 0 {
			input.TopN = 20
		}

		release, err := deps.Locks.Acquire("Profiler")
		if err != nil {
			return toolError("Cannot profile: " + err.Error() +
				". Try pen_performance_metrics for a quick overview instead.")
		}
		defer release()

		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 0, 100, "Starting CPU profiler...")

		var prof *profiler.Profile
		err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			if err := profiler.Enable().Do(ctx); err != nil {
				return fmt.Errorf("profiler.Enable: %w", err)
			}
			if err := profiler.SetSamplingInterval(int64(input.SampleRate)).Do(ctx); err != nil {
				return fmt.Errorf("profiler.SetSamplingInterval: %w", err)
			}
			if err := profiler.Start().Do(ctx); err != nil {
				return fmt.Errorf("profiler.Start: %w", err)
			}
			return nil
		}))
		if err != nil {
			return toolError("failed to start profiler: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 10, 100, fmt.Sprintf("Profiling for %ds...", input.Duration))
		select {
		case <-time.After(time.Duration(input.Duration) * time.Second):
		case <-ctx.Done():
			// Clean up profiler using a fresh context so commands reach Chrome.
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			_ = chromedp.Run(cleanupCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				profiler.Stop().Do(ctx)
				return profiler.Disable().Do(ctx)
			}))
			return toolError("profiling cancelled")
		}

		err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			var stopErr error
			prof, stopErr = profiler.Stop().Do(ctx)
			if stopErr != nil {
				return fmt.Errorf("profiler.Stop: %w", stopErr)
			}
			return profiler.Disable().Do(ctx)
		}))
		if err != nil {
			return toolError("failed to stop profiler: " + err.Error())
		}
		if prof == nil {
			return toolError("profiler returned nil profile")
		}

		server.NotifyProgress(ctx, req, 80, 100, "Analyzing profile...")

		output := formatCPUProfile(prof, input.TopN, input.Duration)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// profileHotspot represents an aggregated function from the profile.
type profileHotspot struct {
	FuncName string
	URL      string
	Line     int64
	SelfTime int64 // hit count (proportional to self time)
}

func formatCPUProfile(prof *profiler.Profile, topN int, durationSec int) string {
	// Build node map.
	nodeMap := make(map[int64]*profiler.ProfileNode, len(prof.Nodes))
	for _, n := range prof.Nodes {
		nodeMap[n.ID] = n
	}

	// Aggregate samples: count hits per node.
	hitCount := make(map[int64]int64)
	for _, sampleID := range prof.Samples {
		hitCount[sampleID]++
	}

	// Build hotspot list.
	hotspots := make([]profileHotspot, 0, len(hitCount))
	var totalHits int64
	for nodeID, hits := range hitCount {
		totalHits += hits
		node, ok := nodeMap[nodeID]
		if !ok || node.CallFrame == nil {
			continue
		}
		cf := node.CallFrame
		funcName := cf.FunctionName
		if funcName == "" {
			funcName = "(anonymous)"
		}
		url := cf.URL
		if url == "" {
			url = "(internal)"
		}
		hotspots = append(hotspots, profileHotspot{
			FuncName: funcName,
			URL:      url,
			Line:     cf.LineNumber,
			SelfTime: hits,
		})
	}

	sort.Slice(hotspots, func(i, j int) bool {
		return hotspots[i].SelfTime > hotspots[j].SelfTime
	})
	if len(hotspots) > topN {
		hotspots = hotspots[:topN]
	}

	// Format.
	profileDuration := time.Duration(
		(prof.EndTime - prof.StartTime) * float64(time.Microsecond),
	)

	headers := []string{"#", "Function", "Source", "Self %", "Hits"}
	rows := make([][]string, 0, len(hotspots))
	for i, h := range hotspots {
		pct := float64(0)
		if totalHits > 0 {
			pct = float64(h.SelfTime) / float64(totalHits) * 100
		}
		src := h.URL
		if h.Line > 0 {
			src = fmt.Sprintf("%s:%d", h.URL, h.Line)
		}
		// Truncate long URLs.
		if len(src) > 60 {
			src = "…" + src[len(src)-59:]
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", i+1),
			h.FuncName,
			src,
			format.Percent(pct),
			fmt.Sprintf("%d", h.SelfTime),
		})
	}

	return format.ToolResult("CPU Profile",
		format.Summary([][2]string{
			{"Duration", fmt.Sprintf("%ds (actual: %s)", durationSec, format.Duration(profileDuration))},
			{"Total Samples", fmt.Sprintf("%d", len(prof.Samples))},
			{"Total Nodes", fmt.Sprintf("%d", len(prof.Nodes))},
		}),
		"",
		format.Section("Top Hotspots (by self time)", format.Table(headers, rows)),
	)
}

// --- pen_capture_trace ---

type captureTraceInput struct {
	Duration   int      `json:"duration"   jsonschema:"Trace duration in seconds (1-30, default 5)"`
	Categories []string `json:"categories" jsonschema:"Chrome trace categories (default: standard perf set)"`
}

var defaultTraceCategories = []string{
	"devtools.timeline",
	"v8.execute",
	"blink.user_timing",
	"loading",
	"latencyInfo",
	"disabled-by-default-devtools.timeline",
}

func makeCaptureTraceHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, captureTraceInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input captureTraceInput) (*mcp.CallToolResult, any, error) {
		if err := deps.Limiter.Check("pen_capture_trace"); err != nil {
			return toolError(err.Error())
		}

		if input.Duration <= 0 {
			input.Duration = 5
		}
		if input.Duration > 30 {
			input.Duration = 30
		}
		categories := input.Categories
		if len(categories) == 0 {
			categories = defaultTraceCategories
		}

		release, err := deps.Locks.Acquire("Tracing")
		if err != nil {
			return toolError("Cannot trace: " + err.Error() +
				". Try pen_cpu_profile or pen_performance_metrics instead.")
		}
		defer release()

		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 0, 100, "Starting trace capture...")

		// Start tracing with stream mode.
		err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			return tracing.Start().
				WithTraceConfig(&tracing.TraceConfig{
					RecordMode:         tracing.RecordModeRecordAsMuchAsPossible,
					IncludedCategories: categories,
				}).
				WithTransferMode(tracing.TransferModeReturnAsStream).
				WithStreamFormat(tracing.StreamFormatJSON).
				Do(ctx)
		}))
		if err != nil {
			return toolError("failed to start tracing: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 10, 100, fmt.Sprintf("Tracing for %ds...", input.Duration))
		select {
		case <-time.After(time.Duration(input.Duration) * time.Second):
		case <-ctx.Done():
			_ = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				return tracing.End().Do(ctx)
			}))
			return toolError("tracing cancelled")
		}

		// End tracing and wait for completion event.
		type traceResult struct {
			stream cdpio.StreamHandle
			err    error
		}
		done := make(chan traceResult, 1)

		// Use a cancelable child context so the listener is removed when the handler returns.
		listenerCtx, listenerCancel := context.WithCancel(cdpCtx)
		defer listenerCancel()

		chromedp.ListenTarget(listenerCtx, func(ev interface{}) {
			switch e := ev.(type) {
			case *tracing.EventTracingComplete:
				if e.Stream != "" {
					done <- traceResult{stream: e.Stream}
				} else {
					done <- traceResult{err: fmt.Errorf("trace completed without stream")}
				}
			}
		})

		err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			return tracing.End().Do(ctx)
		}))
		if err != nil {
			return toolError("failed to end tracing: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 60, 100, "Reading trace data...")

		// Wait for trace completion with timeout.
		var streamHandle cdpio.StreamHandle
		select {
		case result := <-done:
			if result.err != nil {
				return toolError("trace error: " + result.err.Error())
			}
			streamHandle = result.stream
		case <-time.After(30 * time.Second):
			return toolError("trace completion timed out")
		}

		// Read stream to temp file.
		tmpFile, err := security.CreateSecureTempFile("pen-trace-*.json")
		if err != nil {
			return toolError("cannot create temp file: " + err.Error())
		}
		tmpPath := tmpFile.Name()

		success := false
		defer func() {
			tmpFile.Close()
			if !success {
				os.Remove(tmpPath)
			}
		}()

		const maxTraceBytes = 500 * 1024 * 1024 // 500 MB
		var totalBytes int64
		err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			for {
				data, eof, readErr := cdpio.Read(streamHandle).Do(ctx)
				if readErr != nil {
					return fmt.Errorf("io.Read: %w", readErr)
				}
				if len(data) > 0 {
					n, writeErr := tmpFile.WriteString(data)
					totalBytes += int64(n)
					if writeErr != nil {
						return fmt.Errorf("write trace: %w", writeErr)
					}
					if totalBytes > maxTraceBytes {
						_ = cdpio.Close(streamHandle).Do(ctx)
						return fmt.Errorf("trace file exceeded %s limit — use fewer categories or a shorter duration", format.Bytes(maxTraceBytes))
					}
				}
				if eof {
					break
				}
			}
			return cdpio.Close(streamHandle).Do(ctx)
		}))
		if err != nil {
			return toolError("failed to read trace stream: " + err.Error())
		}

		success = true
		server.NotifyProgress(ctx, req, 100, 100, "Trace captured")

		// Quick summary from trace file.
		summary := summarizeTraceFile(tmpPath)

		output := format.ToolResult("Trace Capture",
			format.Summary([][2]string{
				{"Duration", fmt.Sprintf("%ds", input.Duration)},
				{"Categories", strings.Join(categories, ", ")},
				{"Trace Size", format.Bytes(totalBytes)},
				{"File", tmpPath},
			}),
			"",
			summary,
			"",
			format.Warning("Use 'chrome://tracing' or Perfetto UI to load the trace file for full visualization."),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// summarizeTraceFile reads a trace JSON and extracts quick stats.
func summarizeTraceFile(path string) string {
	events, err := parseTraceFile(path)
	if err != nil {
		return "(could not read trace file for summary)"
	}

	// Quick counts.
	catCounts := make(map[string]int)
	var longTaskCount int
	for _, e := range events {
		catCounts[e.Cat]++
		if e.Ph == "X" && e.Dur > 50000 {
			longTaskCount++
		}
	}

	items := []string{
		fmt.Sprintf("Total events: %d", len(events)),
		fmt.Sprintf("Categories: %d", len(catCounts)),
		fmt.Sprintf("Long tasks (>50ms): %d", longTaskCount),
	}

	return format.Section("Trace Summary",
		format.BulletList(items),
		"",
		format.Warning("Use pen_trace_insights for detailed analysis, or load in chrome://tracing for visualization."),
	)
}

// --- Reusable trace types and parsing ---

// traceEvent represents a single event from a Chrome trace file.
type traceEvent struct {
	Cat  string                 `json:"cat"`
	Name string                 `json:"name"`
	Ph   string                 `json:"ph"`
	Ts   float64                `json:"ts"`  // microseconds
	Dur  float64                `json:"dur"` // microseconds (for "X" events)
	Pid  int                    `json:"pid"`
	Tid  int                    `json:"tid"`
	Args map[string]interface{} `json:"args"`
}

func parseTraceFile(path string) ([]traceEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	const maxSize = 100 * 1024 * 1024 // 100MB
	if len(data) > maxSize {
		return nil, fmt.Errorf("trace file too large (%s, max 100MB)", format.Bytes(int64(len(data))))
	}

	// Try {traceEvents: [...]} wrapper first.
	var wrapper struct {
		TraceEvents []traceEvent `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.TraceEvents != nil {
		return wrapper.TraceEvents, nil
	}

	// Try plain array.
	var events []traceEvent
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("unrecognized trace format: %w", err)
	}
	return events, nil
}

// --- pen_trace_insights ---

type traceInsightsInput struct {
	File string `json:"file" jsonschema:"Path to a trace JSON file (from pen_capture_trace)"`
	TopN int    `json:"topN,omitempty" jsonschema:"Number of top items per category (default 10)"`
}

func makeTraceInsightsHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, traceInsightsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input traceInsightsInput) (*mcp.CallToolResult, any, error) {
		if input.File == "" {
			return toolError("file path is required")
		}

		// Validate file path (prevent path traversal).
		if deps.Config.ProjectRoot != "" {
			if _, err := security.ValidateSourcePath(deps.Config.ProjectRoot, input.File); err != nil {
				if err2 := security.ValidateTempPath(input.File); err2 != nil {
					return toolError("invalid file path: " + err.Error())
				}
			}
		} else {
			if err := security.ValidateTempPath(input.File); err != nil {
				return toolError("invalid file path: " + err.Error())
			}
		}

		if input.TopN <= 0 {
			input.TopN = 10
		}

		// Parse trace file.
		events, err := parseTraceFile(input.File)
		if err != nil {
			return toolError("failed to parse trace: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 30, 100, "Analyzing trace events...")

		insights := analyzeTrace(events, input.TopN)

		server.NotifyProgress(ctx, req, 100, 100, "Analysis complete")

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: insights}},
		}, nil, nil
	}
}

// --- Trace analysis engine ---

func analyzeTrace(events []traceEvent, topN int) string {
	var sections []string

	longTasks := extractLongTasks(events, topN)
	if longTasks != "" {
		sections = append(sections, longTasks)
	}

	cls := extractLayoutShifts(events)
	if cls != "" {
		sections = append(sections, cls)
	}

	lcp := extractLCP(events)
	if lcp != "" {
		sections = append(sections, lcp)
	}

	resources := extractResourceBottlenecks(events, topN)
	if resources != "" {
		sections = append(sections, resources)
	}

	fps := extractFrameTiming(events)
	if fps != "" {
		sections = append(sections, fps)
	}

	if len(sections) == 0 {
		return format.ToolResult("Trace Insights", "No actionable insights found in trace.")
	}

	return format.ToolResult("Trace Insights",
		format.Summary([][2]string{
			{"Total Events", fmt.Sprintf("%d", len(events))},
		}),
		"",
		strings.Join(sections, "\n\n"),
	)
}

func extractLongTasks(events []traceEvent, topN int) string {
	type task struct {
		name string
		dur  float64 // ms
		ts   float64 // ms from first event
	}

	// Find trace start time.
	var minTs float64 = math.MaxFloat64
	for _, e := range events {
		if e.Ts > 0 && e.Ts < minTs {
			minTs = e.Ts
		}
	}

	var tasks []task
	for _, e := range events {
		if e.Ph != "X" {
			continue
		}
		durMs := e.Dur / 1000.0
		if durMs < 50 {
			continue
		}
		if e.Cat != "devtools.timeline" && !strings.Contains(e.Cat, "devtools.timeline") {
			continue
		}
		tasks = append(tasks, task{
			name: e.Name,
			dur:  durMs,
			ts:   (e.Ts - minTs) / 1000.0,
		})
	}

	if len(tasks) == 0 {
		return ""
	}

	totalCount := len(tasks)
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].dur > tasks[j].dur
	})
	if len(tasks) > topN {
		tasks = tasks[:topN]
	}

	headers := []string{"#", "Task", "Duration", "At"}
	rows := make([][]string, len(tasks))
	for i, t := range tasks {
		rows[i] = []string{
			fmt.Sprintf("%d", i+1),
			t.name,
			fmt.Sprintf("%.1fms", t.dur),
			fmt.Sprintf("%.0fms", t.ts),
		}
	}

	return format.Section("Long Tasks (>50ms)",
		format.KeyValue("Count", fmt.Sprintf("%d total, showing top %d", totalCount, len(tasks))),
		"",
		format.Table(headers, rows),
	)
}

func extractLayoutShifts(events []traceEvent) string {
	var totalCLS float64
	var shiftCount int

	for _, e := range events {
		if e.Name != "LayoutShift" {
			continue
		}
		data, ok := e.Args["data"].(map[string]interface{})
		if !ok {
			continue
		}
		// Only count shifts without recent input (per CLS spec).
		if hadInput, ok := data["had_recent_input"].(bool); ok && hadInput {
			continue
		}
		score, ok := data["score"].(float64)
		if !ok {
			continue
		}
		totalCLS += score
		shiftCount++
	}

	if shiftCount == 0 {
		return ""
	}

	rating := "Good"
	if totalCLS > 0.25 {
		rating = "Poor"
	} else if totalCLS > 0.1 {
		rating = "Needs Improvement"
	}

	return format.Section("Cumulative Layout Shift (CLS)",
		format.Summary([][2]string{
			{"CLS Score", fmt.Sprintf("%.4f", totalCLS)},
			{"Rating", rating},
			{"Layout Shifts", fmt.Sprintf("%d (without recent input)", shiftCount)},
		}),
	)
}

func extractLCP(events []traceEvent) string {
	// Find the last LCP candidate (the final one is the actual LCP).
	var lastCandidate *traceEvent
	var navStartTs float64

	for i := range events {
		e := &events[i]
		if e.Name == "navigationStart" && navStartTs == 0 {
			navStartTs = e.Ts
		}
		if e.Name == "largestContentfulPaint::Candidate" {
			lastCandidate = e
		}
	}

	if lastCandidate == nil {
		return ""
	}

	var lcpTime float64
	if navStartTs > 0 {
		lcpTime = (lastCandidate.Ts - navStartTs) / 1000.0 // ms
	}

	pairs := [][2]string{
		{"LCP Time", fmt.Sprintf("%.0fms", lcpTime)},
	}

	// Extract data from args if available.
	if data, ok := lastCandidate.Args["data"].(map[string]interface{}); ok {
		if size, ok := data["size"].(float64); ok {
			pairs = append(pairs, [2]string{"Size", fmt.Sprintf("%.0f px²", size)})
		}
		if typ, ok := data["type"].(string); ok {
			pairs = append(pairs, [2]string{"Type", typ})
		}
		if u, ok := data["url"].(string); ok && u != "" {
			if len(u) > 80 {
				u = u[:77] + "..."
			}
			pairs = append(pairs, [2]string{"URL", u})
		}
	}

	rating := "Good"
	if lcpTime > 4000 {
		rating = "Poor"
	} else if lcpTime > 2500 {
		rating = "Needs Improvement"
	}
	pairs = append(pairs, [2]string{"Rating", rating})

	return format.Section("Largest Contentful Paint (LCP)", format.Summary(pairs))
}

func extractResourceBottlenecks(events []traceEvent, topN int) string {
	type resourceInfo struct {
		url      string
		startTs  float64
		endTs    float64
		duration float64 // ms
	}

	// Correlate send/finish by requestId.
	starts := make(map[string]*resourceInfo)
	for _, e := range events {
		data, ok := e.Args["data"].(map[string]interface{})
		if !ok {
			continue
		}
		rid, _ := data["requestId"].(string)
		if rid == "" {
			continue
		}

		switch e.Name {
		case "ResourceSendRequest":
			u, _ := data["url"].(string)
			starts[rid] = &resourceInfo{url: u, startTs: e.Ts}
		case "ResourceFinish":
			if info, ok := starts[rid]; ok {
				info.endTs = e.Ts
				info.duration = (e.Ts - info.startTs) / 1000.0
			}
		}
	}

	// Collect completed resources.
	var resources []resourceInfo
	for _, info := range starts {
		if info.endTs > 0 && info.url != "" {
			resources = append(resources, *info)
		}
	}

	if len(resources) == 0 {
		return ""
	}

	sort.Slice(resources, func(i, j int) bool {
		return resources[i].duration > resources[j].duration
	})
	if len(resources) > topN {
		resources = resources[:topN]
	}

	headers := []string{"#", "URL", "Duration"}
	rows := make([][]string, len(resources))
	for i, r := range resources {
		u := r.url
		if len(u) > 60 {
			u = "…" + u[len(u)-59:]
		}
		rows[i] = []string{
			fmt.Sprintf("%d", i+1),
			u,
			fmt.Sprintf("%.0fms", r.duration),
		}
	}

	return format.Section("Slowest Resources",
		format.Table(headers, rows),
	)
}

func extractFrameTiming(events []traceEvent) string {
	// Collect DrawFrame timestamps.
	var frameTimes []float64
	for _, e := range events {
		if e.Name == "DrawFrame" {
			frameTimes = append(frameTimes, e.Ts)
		}
	}

	if len(frameTimes) < 2 {
		return ""
	}

	sort.Float64s(frameTimes)

	var totalGap float64
	var drops int
	for i := 1; i < len(frameTimes); i++ {
		gap := (frameTimes[i] - frameTimes[i-1]) / 1000.0 // ms
		totalGap += gap
		if gap > 33.3 { // below 30fps
			drops++
		}
	}

	durationMs := (frameTimes[len(frameTimes)-1] - frameTimes[0]) / 1000.0
	avgFPS := float64(len(frameTimes)-1) / (durationMs / 1000.0)

	return format.Section("Frame Timing",
		format.Summary([][2]string{
			{"Frames", fmt.Sprintf("%d", len(frameTimes))},
			{"Avg FPS", fmt.Sprintf("%.1f", avgFPS)},
			{"Frame Drops (>33ms gap)", fmt.Sprintf("%d", drops)},
			{"Duration", fmt.Sprintf("%.0fms", durationMs)},
		}),
	)
}

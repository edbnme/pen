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
	"github.com/chromedp/cdproto/tracing"
	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
	"github.com/edbnme/pen/internal/security"
	"github.com/edbnme/pen/internal/server"
)

func registerTraceTools(s *mcp.Server, deps *Deps) {
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

// --- pen_capture_trace ---

type captureTraceInput struct {
	Duration   int      `json:"duration,omitempty"   jsonschema:"Trace duration in seconds (1-30, default 5)"`
	Categories []string `json:"categories,omitempty" jsonschema:"Chrome trace categories (default: standard perf set)"`
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

		// Overall timeout: trace duration + 60s for start/stop/read overhead.
		traceTimeout := time.Duration(input.Duration+60) * time.Second
		cdpCtx, traceCancel := context.WithTimeout(cdpCtx, traceTimeout)
		defer traceCancel()

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

	overview := extractMainThreadBreakdown(events)
	if overview != "" {
		sections = append(sections, overview)
	}

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

	scriptWork := extractScriptActivity(events, topN)
	if scriptWork != "" {
		sections = append(sections, scriptWork)
	}

	gcActivity := extractGCActivity(events)
	if gcActivity != "" {
		sections = append(sections, gcActivity)
	}

	paintActivity := extractPaintActivity(events)
	if paintActivity != "" {
		sections = append(sections, paintActivity)
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

// --- Trace extractors ---

func extractLongTasks(events []traceEvent, topN int) string {
	type task struct {
		name string
		dur  float64 // ms
		ts   float64 // ms from first event
	}

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
		lcpTime = (lastCandidate.Ts - navStartTs) / 1000.0
	}

	pairs := [][2]string{
		{"LCP Time", fmt.Sprintf("%.0fms", lcpTime)},
	}

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

	var drops int
	for i := 1; i < len(frameTimes); i++ {
		gap := (frameTimes[i] - frameTimes[i-1]) / 1000.0
		if gap > 33.3 {
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

// extractMainThreadBreakdown categorizes all complete ("X") events by type
// and reports the total time spent in each category.
func extractMainThreadBreakdown(events []traceEvent) string {
	categoryTime := make(map[string]float64)

	for _, e := range events {
		if e.Ph != "X" || e.Dur <= 0 {
			continue
		}
		durMs := e.Dur / 1000.0
		cat := classifyEvent(e.Name, e.Cat)
		if cat != "" {
			categoryTime[cat] += durMs
		}
	}

	if len(categoryTime) == 0 {
		return ""
	}

	type catEntry struct {
		name string
		ms   float64
	}
	var entries []catEntry
	var totalMs float64
	for name, ms := range categoryTime {
		entries = append(entries, catEntry{name, ms})
		totalMs += ms
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ms > entries[j].ms
	})

	headers := []string{"Category", "Time", "% of Total"}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		pct := float64(0)
		if totalMs > 0 {
			pct = e.ms / totalMs * 100
		}
		rows = append(rows, []string{
			e.name,
			fmt.Sprintf("%.1fms", e.ms),
			format.Percent(pct),
		})
	}

	return format.Section("Main-Thread Work Breakdown",
		format.Summary([][2]string{
			{"Total Work", fmt.Sprintf("%.1fms", totalMs)},
			{"Categories", fmt.Sprintf("%d", len(entries))},
		}),
		"",
		format.Table(headers, rows),
	)
}

// classifyEvent maps trace event names to high-level categories.
func classifyEvent(name, cat string) string {
	switch name {
	case "EvaluateScript", "v8.evaluateModule", "v8.compile", "v8.compileModule",
		"V8.Execute", "FunctionCall", "v8.produceCache", "v8.run":
		return "Script Evaluation"
	case "Layout", "LayoutShift":
		return "Layout"
	case "UpdateLayoutTree", "RecalculateStyles", "ParseAuthorStyleSheet":
		return "Style"
	case "Paint", "PaintImage", "RasterTask", "CompositeLayers", "Commit",
		"UpdateLayerTree", "UpdateLayer":
		return "Rendering"
	case "ParseHTML":
		return "HTML Parsing"
	case "ResourceSendRequest", "ResourceReceiveResponse", "ResourceReceivedData",
		"ResourceFinish":
		return "Network"
	case "V8.GCScavenger", "V8.GCIncrementalMarking", "MajorGC", "MinorGC",
		"V8.GCCompactor", "BlinkGC.AtomicPhase", "BlinkGC.CompleteSweep":
		return "Garbage Collection"
	case "TimerFire", "TimerInstall", "TimerRemove":
		return "Timers"
	case "EventDispatch", "HitTest":
		return "Event Handling"
	case "ScheduleStyleRecalculation", "InvalidateLayout":
		return "Layout Invalidation"
	}

	if strings.Contains(cat, "v8") {
		return "Script Evaluation"
	}
	if strings.Contains(cat, "blink.user_timing") {
		return "User Timing"
	}
	return ""
}

// extractScriptActivity finds the slowest script evaluation/compilation events.
func extractScriptActivity(events []traceEvent, topN int) string {
	type scriptEvent struct {
		name string
		url  string
		dur  float64 // ms
	}

	var items []scriptEvent
	for _, e := range events {
		if e.Ph != "X" || e.Dur <= 0 {
			continue
		}
		switch e.Name {
		case "EvaluateScript", "v8.compile", "v8.compileModule", "v8.evaluateModule":
		default:
			continue
		}
		durMs := e.Dur / 1000.0
		if durMs < 1 {
			continue
		}
		u := ""
		if data, ok := e.Args["data"].(map[string]interface{}); ok {
			if uVal, ok := data["url"].(string); ok {
				u = uVal
			}
		}
		items = append(items, scriptEvent{name: e.Name, url: u, dur: durMs})
	}

	if len(items) == 0 {
		return ""
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].dur > items[j].dur
	})
	if len(items) > topN {
		items = items[:topN]
	}

	headers := []string{"#", "Event", "Script", "Duration"}
	rows := make([][]string, len(items))
	for i, s := range items {
		u := s.url
		if len(u) > 55 {
			u = "…" + u[len(u)-54:]
		}
		if u == "" {
			u = "(inline)"
		}
		rows[i] = []string{
			fmt.Sprintf("%d", i+1),
			s.name,
			u,
			fmt.Sprintf("%.1fms", s.dur),
		}
	}

	return format.Section("Script Activity (Evaluation & Compilation)",
		format.Table(headers, rows),
	)
}

// extractGCActivity summarizes garbage collection events.
func extractGCActivity(events []traceEvent) string {
	var totalGCTime float64
	var gcCount int
	var maxGC float64

	for _, e := range events {
		if e.Ph != "X" || e.Dur <= 0 {
			continue
		}
		switch e.Name {
		case "V8.GCScavenger", "V8.GCIncrementalMarking", "MajorGC", "MinorGC",
			"V8.GCCompactor", "BlinkGC.AtomicPhase", "BlinkGC.CompleteSweep":
		default:
			continue
		}
		durMs := e.Dur / 1000.0
		totalGCTime += durMs
		gcCount++
		if durMs > maxGC {
			maxGC = durMs
		}
	}

	if gcCount == 0 {
		return ""
	}

	pairs := [][2]string{
		{"GC Events", fmt.Sprintf("%d", gcCount)},
		{"Total GC Time", fmt.Sprintf("%.1fms", totalGCTime)},
		{"Longest GC Pause", fmt.Sprintf("%.1fms", maxGC)},
	}

	if maxGC > 50 {
		pairs = append(pairs, [2]string{"⚠ Warning", "GC pauses >50ms can cause visible jank"})
	}

	return format.Section("Garbage Collection", format.Summary(pairs))
}

// extractPaintActivity summarizes paint and rendering events.
func extractPaintActivity(events []traceEvent) string {
	var totalPaintTime float64
	var paintCount int
	var maxPaint float64

	for _, e := range events {
		if e.Ph != "X" || e.Dur <= 0 {
			continue
		}
		switch e.Name {
		case "Paint", "PaintImage", "RasterTask", "CompositeLayers",
			"UpdateLayerTree":
		default:
			continue
		}
		durMs := e.Dur / 1000.0
		totalPaintTime += durMs
		paintCount++
		if durMs > maxPaint {
			maxPaint = durMs
		}
	}

	if paintCount == 0 {
		return ""
	}

	return format.Section("Paint & Rendering",
		format.Summary([][2]string{
			{"Paint Events", fmt.Sprintf("%d", paintCount)},
			{"Total Paint Time", fmt.Sprintf("%.1fms", totalPaintTime)},
			{"Longest Paint", fmt.Sprintf("%.1fms", maxPaint)},
		}),
	)
}

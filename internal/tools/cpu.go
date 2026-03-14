package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
		Description: "Record a V8 CPU profile for a given duration and analyze hot functions, call trees, and bottlenecks.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "CPU Profile",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, makeCPUProfileHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_capture_trace",
		Description: "Capture a Chrome trace (DevTools Timeline) for given categories and duration. Returns a downloadable trace file path.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Capture Trace",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, makeCaptureTraceHandler(deps))
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
			return toolError("Cannot profile: " + err.Error())
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
			// Clean up profiler before returning.
			_ = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
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
			return toolError("Cannot trace: " + err.Error())
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

		chromedp.ListenTarget(cdpCtx, func(ev interface{}) {
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
	data, err := os.ReadFile(path)
	if err != nil {
		return "(could not read trace file for summary)"
	}

	// Limit parsing to avoid OOM on huge traces.
	const maxParse = 50 * 1024 * 1024 // 50MB
	if len(data) > maxParse {
		return fmt.Sprintf("Trace file too large for inline summary (%s). Load in chrome://tracing.", format.Bytes(int64(len(data))))
	}

	// Parse as JSON array of trace events.
	var wrapper struct {
		TraceEvents []struct {
			Cat string  `json:"cat"`
			Dur float64 `json:"dur"` // microseconds
			Ph  string  `json:"ph"`
			Pid int     `json:"pid"`
			Tid int     `json:"tid"`
		} `json:"traceEvents"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		// Try as plain array.
		if err := json.Unmarshal(data, &wrapper.TraceEvents); err != nil {
			return "(trace format not recognized for summary)"
		}
	}

	catCounts := make(map[string]int)
	var totalDur float64
	for _, ev := range wrapper.TraceEvents {
		catCounts[ev.Cat]++
		if ev.Ph == "X" || ev.Ph == "B" {
			totalDur += ev.Dur
		}
	}

	headers := []string{"Category", "Events"}
	rows := make([][]string, 0, len(catCounts))
	for cat, count := range catCounts {
		if cat == "" {
			cat = "(none)"
		}
		rows = append(rows, []string{cat, fmt.Sprintf("%d", count)})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i][1] > rows[j][1]
	})
	if len(rows) > 15 {
		rows = rows[:15]
	}

	return format.Section("Trace Summary",
		format.KeyValue("Total Events", fmt.Sprintf("%d", len(wrapper.TraceEvents))),
		format.KeyValue("Categories", fmt.Sprintf("%d", len(catCounts))),
		"",
		format.Table(headers, rows),
	)
}

package tools

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/chromedp/cdproto/profiler"
	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
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
}

// --- pen_cpu_profile ---

type cpuProfileInput struct {
	Duration   int `json:"duration,omitempty"   jsonschema:"Profile duration in seconds (1-30, default 5)"`
	SampleRate int `json:"sampleRate,omitempty" jsonschema:"Sampling interval in microseconds (default 100)"`
	TopN       int `json:"topN,omitempty"       jsonschema:"Number of top hotspot functions to show (default 20)"`
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

		// Timeout for the CDP control operations (not the profiling wait).
		profilerTimeout := time.Duration(input.Duration+30) * time.Second
		cdpCtx, cdpCancel := context.WithTimeout(cdpCtx, profilerTimeout)
		defer cdpCancel()

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

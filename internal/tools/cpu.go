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

		server.NotifyProgress(ctx, req, 0, 100, "Starting CPU profiler...")

		server.NotifyProgress(ctx, req, 10, 100, fmt.Sprintf("Profiling for %ds...", input.Duration))

		prof, err := captureCPUProfile(ctx, deps, input.Duration, input.SampleRate)
		if err != nil {
			return toolError(err.Error())
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

func captureCPUProfile(ctx context.Context, deps *Deps, durationSec, sampleRate int) (*profiler.Profile, error) {
	release, err := deps.Locks.Acquire("Profiler")
	if err != nil {
		return nil, fmt.Errorf("Cannot profile: %w. Try pen_performance_metrics for a quick overview instead.", err)
	}
	defer release()

	cdpCtx, err := deps.CDP.Context()
	if err != nil {
		return nil, fmt.Errorf("CDP not connected: %w", err)
	}

	profilerTimeout := time.Duration(durationSec+30) * time.Second
	cdpCtx, cdpCancel := context.WithTimeout(cdpCtx, profilerTimeout)
	defer cdpCancel()

	err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := profiler.Enable().Do(ctx); err != nil {
			return fmt.Errorf("profiler.Enable: %w", err)
		}
		if err := profiler.SetSamplingInterval(int64(sampleRate)).Do(ctx); err != nil {
			return fmt.Errorf("profiler.SetSamplingInterval: %w", err)
		}
		if err := profiler.Start().Do(ctx); err != nil {
			return fmt.Errorf("profiler.Start: %w", err)
		}
		return nil
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to start profiler: %w", err)
	}

	select {
	case <-time.After(time.Duration(durationSec) * time.Second):
	case <-ctx.Done():
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = chromedp.Run(cleanupCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			profiler.Stop().Do(ctx)
			return profiler.Disable().Do(ctx)
		}))
		return nil, fmt.Errorf("profiling cancelled")
	}

	var prof *profiler.Profile
	err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var stopErr error
		prof, stopErr = profiler.Stop().Do(ctx)
		if stopErr != nil {
			return fmt.Errorf("profiler.Stop: %w", stopErr)
		}
		return profiler.Disable().Do(ctx)
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to stop profiler: %w", err)
	}
	if prof == nil {
		return nil, fmt.Errorf("profiler returned nil profile")
	}

	return prof, nil
}

type cpuProfileAssessment struct {
	Verdict   Verdict
	NextSteps []string
}

func assessCPUProfile(totalHits int64, hotspots []profileHotspot) cpuProfileAssessment {
	steps := []string{
		"Use the slow-page workflow when you need a broader diagnosis across vitals, CPU, and blocking resources.",
	}

	if totalHits == 0 || len(hotspots) == 0 {
		steps = append([]string{
			"Capture the profile while reproducing the slow load or interaction, then re-run so the hottest functions reflect real work instead of an empty sample.",
		}, steps...)

		return cpuProfileAssessment{
			Verdict:   VerdictWarn,
			NextSteps: steps,
		}
	}

	dominantPct := float64(hotspots[0].SelfTime) / float64(totalHits) * 100
	if dominantPct >= 50 {
		steps = append([]string{
			fmt.Sprintf("Inspect the %q hotspot first; it accounts for %s of sampled CPU time.", hotspots[0].FuncName, format.Percent(dominantPct)),
			"If that work occurs during startup or input handling, follow up with pen_capture_trace to inspect long tasks and timing breakdowns.",
		}, steps...)

		return cpuProfileAssessment{
			Verdict:   VerdictWarn,
			NextSteps: steps,
		}
	}

	steps = append([]string{
		"No single hotspot dominates this sample. If the page still feels slow, compare this profile with pen_web_vitals and pen_network_waterfall.",
	}, steps...)

	return cpuProfileAssessment{
		Verdict:   VerdictPass,
		NextSteps: steps,
	}
}

func summarizeCPUHotspots(prof *profiler.Profile, topN int) []profileHotspot {
	nodeMap := make(map[int64]*profiler.ProfileNode, len(prof.Nodes))
	for _, node := range prof.Nodes {
		nodeMap[node.ID] = node
	}

	hitCount := make(map[int64]int64)
	for _, sampleID := range prof.Samples {
		hitCount[sampleID]++
	}

	hotspots := make([]profileHotspot, 0, len(hitCount))
	for nodeID, hits := range hitCount {
		node, ok := nodeMap[nodeID]
		if !ok || node.CallFrame == nil {
			continue
		}
		callFrame := node.CallFrame
		funcName := callFrame.FunctionName
		if funcName == "" {
			funcName = "(anonymous)"
		}
		url := callFrame.URL
		if url == "" {
			url = "(internal)"
		}
		hotspots = append(hotspots, profileHotspot{
			FuncName: funcName,
			URL:      url,
			Line:     callFrame.LineNumber,
			SelfTime: hits,
		})
	}

	sort.Slice(hotspots, func(i, j int) bool {
		return hotspots[i].SelfTime > hotspots[j].SelfTime
	})
	if topN > 0 && len(hotspots) > topN {
		hotspots = hotspots[:topN]
	}

	return hotspots
}

func formatCPUProfile(prof *profiler.Profile, topN int, durationSec int) string {
	hotspots := summarizeCPUHotspots(prof, topN)
	totalHits := int64(len(prof.Samples))

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

	assessment := assessCPUProfile(totalHits, hotspots)

	return format.ToolResult("CPU Profile",
		format.Summary([][2]string{
			{"Duration", fmt.Sprintf("%ds (actual: %s)", durationSec, format.Duration(profileDuration))},
			{"Total Samples", fmt.Sprintf("%d", len(prof.Samples))},
			{"Total Nodes", fmt.Sprintf("%d", len(prof.Nodes))},
		}),
		"",
		fmt.Sprintf("**Verdict**: %s", assessment.Verdict),
		"",
		format.Section("Top Hotspots (by self time)", format.Table(headers, rows)),
		"",
		format.Section("Recommended Next Steps", format.BulletList(assessment.NextSteps)),
	)
}

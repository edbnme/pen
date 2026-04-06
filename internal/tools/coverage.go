package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/cdproto/css"
	"github.com/chromedp/cdproto/profiler"
	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
	"github.com/edbnme/pen/internal/server"
)

func registerCoverageTools(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_js_coverage",
		Description: "Collect precise JavaScript code coverage: per-function byte ranges, used vs unused percentages per script.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "JS Coverage",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, makeJSCoverageHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_css_coverage",
		Description: "Collect CSS rule usage: which rules were applied and which are unused dead code.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "CSS Coverage",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, makeCSSCoverageHandler(deps))
}

// --- pen_js_coverage ---

type jsCoverageInput struct {
	CallCount bool   `json:"callCount,omitempty" jsonschema:"Include per-function call counts (default true)"`
	Detailed  bool   `json:"detailed,omitempty"  jsonschema:"Block-level coverage granularity (default false)"`
	Navigate  string `json:"navigate,omitempty"  jsonschema:"Optional URL to navigate to before collecting (triggers full page load coverage)"`
	TopN      int    `json:"topN,omitempty"      jsonschema:"Top N scripts by unused bytes to display (default 20)"`
}

type jsCoverageOverview struct {
	Stats           []scriptCoverageStats
	ScriptsAnalyzed int
	ScriptsWithCode int
	TotalBytes      int64
	UsedBytes       int64
	UnusedBytes     int64
	UsedPct         float64
}

func makeJSCoverageHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, jsCoverageInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input jsCoverageInput) (*mcp.CallToolResult, any, error) {
		if input.TopN <= 0 {
			input.TopN = 20
		}

		server.NotifyProgress(ctx, req, 0, 100, "Starting JS coverage...")

		server.NotifyProgress(ctx, req, 50, 100, "Taking coverage snapshot...")

		coverage, err := collectJSCoverage(ctx, deps, input)
		if err != nil {
			return toolError(err.Error())
		}

		server.NotifyProgress(ctx, req, 80, 100, "Analyzing coverage data...")

		output := formatJSCoverage(coverage, input.TopN)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

func collectJSCoverage(ctx context.Context, deps *Deps, input jsCoverageInput) ([]*profiler.ScriptCoverage, error) {
	release, err := deps.Locks.Acquire("Profiler")
	if err != nil {
		return nil, fmt.Errorf("Cannot collect coverage: %w. Wait for the current profiling operation to finish, or try pen_css_coverage instead.", err)
	}
	defer release()

	cdpCtx, err := deps.CDP.Context()
	if err != nil {
		return nil, fmt.Errorf("CDP not connected: %w", err)
	}

	enableCtx, enableCancel := context.WithTimeout(cdpCtx, cdpEnableTimeout)
	defer enableCancel()
	err = chromedp.Run(enableCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := profiler.Enable().Do(ctx); err != nil {
			return fmt.Errorf("profiler.Enable: %w", err)
		}
		_, err := profiler.StartPreciseCoverage().
			WithCallCount(true).
			WithDetailed(input.Detailed).
			Do(ctx)
		return err
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to start coverage: %w", err)
	}

	if input.Navigate != "" {
		if err := chromedp.Run(cdpCtx, chromedp.Navigate(input.Navigate)); err != nil {
			return nil, fmt.Errorf("navigation failed: %w", err)
		}
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return nil, fmt.Errorf("cancelled during navigation")
		}
	}

	var coverage []*profiler.ScriptCoverage
	err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var takeErr error
		coverage, _, takeErr = profiler.TakePreciseCoverage().Do(ctx)
		if takeErr != nil {
			return takeErr
		}
		if err := profiler.StopPreciseCoverage().Do(ctx); err != nil {
			return err
		}
		return profiler.Disable().Do(ctx)
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to take coverage: %w", err)
	}

	return coverage, nil
}

// scriptCoverageStats holds per-script coverage summary.
type scriptCoverageStats struct {
	URL         string
	TotalBytes  int64
	UsedBytes   int64
	UnusedBytes int64
	FuncCount   int
	UsedPct     float64
}

func formatJSCoverage(coverage []*profiler.ScriptCoverage, topN int) string {
	overview := summarizeJSCoverage(coverage)
	stats := overview.Stats
	totalScriptsWithCode := overview.ScriptsWithCode
	if topN <= 0 {
		topN = len(stats)
	}
	if len(stats) > topN {
		stats = stats[:topN]
	}

	headers := []string{"#", "Script", "Total", "Used", "Unused", "Used %", "Functions"}
	rows := make([][]string, 0, len(stats))
	for i, s := range stats {
		url := s.URL
		if len(url) > 55 {
			url = "…" + url[len(url)-54:]
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", i+1),
			url,
			format.Bytes(s.TotalBytes),
			format.Bytes(s.UsedBytes),
			format.Bytes(s.UnusedBytes),
			format.Percent(s.UsedPct),
			fmt.Sprintf("%d", s.FuncCount),
		})
	}

	return format.ToolResult("JavaScript Coverage",
		format.Summary([][2]string{
			{"Scripts Analyzed", fmt.Sprintf("%d", overview.ScriptsAnalyzed)},
			{"Total JS Size", format.Bytes(overview.TotalBytes)},
			{"Used", fmt.Sprintf("%s (%s)", format.Bytes(overview.UsedBytes), format.Percent(overview.UsedPct))},
			{"Unused", format.Bytes(overview.UnusedBytes)},
		}),
		"",
		format.Section(fmt.Sprintf("Top %d Scripts by Unused Bytes (of %d with code)", len(stats), totalScriptsWithCode), format.Table(headers, rows)),
	)
}

func summarizeJSCoverage(coverage []*profiler.ScriptCoverage) jsCoverageOverview {
	overview := jsCoverageOverview{
		Stats:           make([]scriptCoverageStats, 0, len(coverage)),
		ScriptsAnalyzed: len(coverage),
	}

	for _, sc := range coverage {
		if sc.URL == "" {
			continue
		}

		var totalBytes, usedBytes int64
		for _, fn := range sc.Functions {
			for _, r := range fn.Ranges {
				rangeSize := r.EndOffset - r.StartOffset
				if r == fn.Ranges[0] {
					totalBytes += rangeSize
				}
				if r.Count > 0 {
					usedBytes += rangeSize
				}
			}
		}
		if totalBytes == 0 {
			continue
		}
		if usedBytes > totalBytes {
			usedBytes = totalBytes
		}

		overview.TotalBytes += totalBytes
		overview.UsedBytes += usedBytes
		overview.Stats = append(overview.Stats, scriptCoverageStats{
			URL:         sc.URL,
			TotalBytes:  totalBytes,
			UsedBytes:   usedBytes,
			UnusedBytes: totalBytes - usedBytes,
			FuncCount:   len(sc.Functions),
			UsedPct:     float64(usedBytes) / float64(totalBytes) * 100,
		})
	}

	sort.Slice(overview.Stats, func(i, j int) bool {
		return overview.Stats[i].UnusedBytes > overview.Stats[j].UnusedBytes
	})

	overview.ScriptsWithCode = len(overview.Stats)
	overview.UnusedBytes = overview.TotalBytes - overview.UsedBytes
	if overview.TotalBytes > 0 {
		overview.UsedPct = float64(overview.UsedBytes) / float64(overview.TotalBytes) * 100
	}

	return overview
}

// --- pen_css_coverage ---

type cssCoverageInput struct {
	Navigate string `json:"navigate,omitempty" jsonschema:"Optional URL to navigate to for full-page CSS coverage"`
	TopN     int    `json:"topN,omitempty"     jsonschema:"Top N stylesheets by unused rules to display (default 20)"`
}

func makeCSSCoverageHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, cssCoverageInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input cssCoverageInput) (*mcp.CallToolResult, any, error) {
		release, err := deps.Locks.Acquire("CSS")
		if err != nil {
			return toolError("Cannot collect CSS coverage: " + err.Error())
		}
		defer release()

		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		if input.TopN <= 0 {
			input.TopN = 20
		}

		server.NotifyProgress(ctx, req, 0, 100, "Starting CSS coverage tracking...")

		// Enable CSS and start rule usage tracking.
		enableCtx, enableCancel := context.WithTimeout(cdpCtx, cdpEnableTimeout)
		defer enableCancel()
		err = chromedp.Run(enableCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			if err := css.Enable().Do(ctx); err != nil {
				return fmt.Errorf("css.Enable: %w", err)
			}
			return css.StartRuleUsageTracking().Do(ctx)
		}))
		if err != nil {
			return toolError("failed to start CSS tracking: " + err.Error())
		}

		// Navigate if requested.
		if input.Navigate != "" {
			server.NotifyProgress(ctx, req, 20, 100, "Navigating...")
			if err := chromedp.Run(cdpCtx, chromedp.Navigate(input.Navigate)); err != nil {
				return toolError("navigation failed: " + err.Error())
			}
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return toolError("cancelled during navigation")
			}
		}

		server.NotifyProgress(ctx, req, 50, 100, "Stopping CSS tracking and analyzing...")

		// Stop tracking and get results.
		var ruleUsage []*css.RuleUsage
		err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			var stopErr error
			ruleUsage, stopErr = css.StopRuleUsageTracking().Do(ctx)
			return stopErr
		}))
		if err != nil {
			return toolError("failed to stop CSS tracking: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 80, 100, "Formatting results...")

		output := formatCSSCoverage(ruleUsage, input.TopN)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// cssBySheet aggregates rule usage per stylesheet.
type cssBySheet struct {
	SheetID    string
	TotalRules int
	UsedRules  int
	TotalBytes float64
	UsedBytes  float64
}

func formatCSSCoverage(ruleUsage []*css.RuleUsage, topN int) string {
	sheets := make(map[string]*cssBySheet)

	for _, ru := range ruleUsage {
		id := string(ru.StyleSheetID)
		sheet, ok := sheets[id]
		if !ok {
			sheet = &cssBySheet{SheetID: id}
			sheets[id] = sheet
		}
		ruleSize := ru.EndOffset - ru.StartOffset
		sheet.TotalRules++
		sheet.TotalBytes += ruleSize
		if ru.Used {
			sheet.UsedRules++
			sheet.UsedBytes += ruleSize
		}
	}

	sheetList := make([]*cssBySheet, 0, len(sheets))
	for _, s := range sheets {
		sheetList = append(sheetList, s)
	}

	// Sort by unused bytes.
	sort.Slice(sheetList, func(i, j int) bool {
		unusedI := sheetList[i].TotalBytes - sheetList[i].UsedBytes
		unusedJ := sheetList[j].TotalBytes - sheetList[j].UsedBytes
		return unusedI > unusedJ
	})

	if len(sheetList) > topN {
		sheetList = sheetList[:topN]
	}

	var totalRules, usedRules int
	var totalBytes, usedBytes float64
	for _, s := range sheets {
		totalRules += s.TotalRules
		usedRules += s.UsedRules
		totalBytes += s.TotalBytes
		usedBytes += s.UsedBytes
	}

	headers := []string{"#", "Stylesheet ID", "Rules", "Used", "Unused", "Used %"}
	rows := make([][]string, 0, len(sheetList))
	for i, s := range sheetList {
		usedPct := float64(0)
		if s.TotalRules > 0 {
			usedPct = float64(s.UsedRules) / float64(s.TotalRules) * 100
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", i+1),
			s.SheetID,
			fmt.Sprintf("%d", s.TotalRules),
			fmt.Sprintf("%d", s.UsedRules),
			fmt.Sprintf("%d", s.TotalRules-s.UsedRules),
			format.Percent(usedPct),
		})
	}

	usedPctAll := float64(0)
	if totalRules > 0 {
		usedPctAll = float64(usedRules) / float64(totalRules) * 100
	}

	parts := []string{
		format.Summary([][2]string{
			{"Total Rules", fmt.Sprintf("%d", totalRules)},
			{"Used Rules", fmt.Sprintf("%d (%s)", usedRules, format.Percent(usedPctAll))},
			{"Unused Rules", fmt.Sprintf("%d", totalRules-usedRules)},
			{"Total CSS Bytes", format.Bytes(int64(totalBytes))},
			{"Unused CSS Bytes", format.Bytes(int64(totalBytes - usedBytes))},
		}),
		"",
		format.Section("Stylesheets by Unused Rules", format.Table(headers, rows)),
	}

	if usedPctAll < 50 {
		parts = append(parts, "",
			format.Warning(fmt.Sprintf("Only %s of CSS rules are used. Consider removing unused styles or splitting critical CSS.",
				format.Percent(usedPctAll))))
	}

	return format.ToolResult("CSS Coverage", strings.Join(parts, "\n"))
}

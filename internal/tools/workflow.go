package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/profiler"
	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
	"github.com/edbnme/pen/internal/server"
)

type workflowInput struct {
	Name     string `json:"name" jsonschema:"Workflow name: slow-page-triage, js-bloat-check, accessibility-pass"`
	URL      string `json:"url,omitempty" jsonschema:"Optional URL to navigate to before running the workflow"`
	WaitMs   int    `json:"waitMs,omitempty" jsonschema:"Wait time for vitals and page settling"`
	Duration int    `json:"duration,omitempty" jsonschema:"Optional profiling duration in seconds"`
	Selector string `json:"selector,omitempty" jsonschema:"Optional selector for accessibility-pass"`
}

type workflowPageInfo struct {
	URL   string
	Title string
}

type workflowReport struct {
	SummaryPairs  [][2]string
	Verdict       Verdict
	Findings      []string
	NextSteps     []string
	Notes         []string
	ExtraSections []string
}

func registerWorkflowTools(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_workflow",
		Description: "Run a guided PEN workflow for slow page triage, JavaScript bloat checks, or accessibility passes using typed tool helpers instead of raw output parsing.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Workflow Runner",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, makeWorkflowHandler(deps))
}

func validateWorkflowName(name string) error {
	switch name {
	case "slow-page-triage", "js-bloat-check", "accessibility-pass":
		return nil
	default:
		return fmt.Errorf("unknown workflow %q", name)
	}
}

func makeWorkflowHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, workflowInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input workflowInput) (*mcp.CallToolResult, any, error) {
		input.Name = normalizeWorkflowName(input.Name)
		if err := validateWorkflowName(input.Name); err != nil {
			return toolError(err.Error())
		}

		input.WaitMs = normalizeWorkflowWaitMs(input.WaitMs)
		input.Duration = normalizeWorkflowDuration(input.Duration)

		var (
			title  string
			report workflowReport
			err    error
		)

		switch input.Name {
		case "slow-page-triage":
			title = "Workflow: Slow Page Triage"
			report, err = runSlowPageTriageWorkflow(ctx, req, deps, input)
		case "js-bloat-check":
			title = "Workflow: JS Bloat Check"
			report, err = runJSBloatCheckWorkflow(ctx, req, deps, input)
		case "accessibility-pass":
			title = "Workflow: Accessibility Pass"
			report, err = runAccessibilityPassWorkflow(ctx, req, deps, input)
		}
		if err != nil {
			return toolError(err.Error())
		}

		server.NotifyProgress(ctx, req, 100, 100, "Workflow complete")

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: formatWorkflowReport(title, report)}},
		}, nil, nil
	}
}

func normalizeWorkflowName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func normalizeWorkflowWaitMs(waitMs int) int {
	if waitMs <= 0 {
		return 3000
	}
	if waitMs > 30000 {
		return 30000
	}
	return waitMs
}

func normalizeWorkflowDuration(duration int) int {
	if duration <= 0 {
		return 5
	}
	if duration > 30 {
		return 30
	}
	return duration
}

func currentWorkflowPageInfo(deps *Deps) (workflowPageInfo, error) {
	cdpCtx, err := deps.CDP.Context()
	if err != nil {
		return workflowPageInfo{}, fmt.Errorf("CDP not connected: %w", err)
	}

	var info workflowPageInfo
	if err := chromedp.Run(cdpCtx,
		chromedp.Location(&info.URL),
		chromedp.Title(&info.Title),
	); err != nil {
		return workflowPageInfo{}, err
	}

	return info, nil
}

func navigateWorkflowURL(ctx context.Context, deps *Deps, rawURL string, waitMs int) (workflowPageInfo, error) {
	if err := validateNavigationURL(rawURL); err != nil {
		return workflowPageInfo{}, err
	}

	cdpCtx, err := deps.CDP.Context()
	if err != nil {
		return workflowPageInfo{}, fmt.Errorf("CDP not connected: %w", err)
	}

	if err := chromedp.Run(cdpCtx, chromedp.Navigate(rawURL)); err != nil {
		return workflowPageInfo{}, err
	}

	select {
	case <-time.After(time.Duration(waitMs) * time.Millisecond):
	case <-ctx.Done():
		return workflowPageInfo{}, fmt.Errorf("workflow navigation wait cancelled")
	}

	return currentWorkflowPageInfo(deps)
}

func runSlowPageTriageWorkflow(ctx context.Context, req *mcp.CallToolRequest, deps *Deps, input workflowInput) (workflowReport, error) {
	notes := make([]string, 0)
	findings := make([]string, 0)

	server.NotifyProgress(ctx, req, 5, 100, "Preparing slow-page workflow inputs...")

	var pageInfo workflowPageInfo
	var coverage []*profiler.ScriptCoverage
	coverageAvailable := false

	if input.URL != "" {
		if _, err := enableNetworkCapture(ctx, deps, true, true); err != nil {
			notes = append(notes, "Network capture could not be started automatically: "+err.Error())
		}

		server.NotifyProgress(ctx, req, 15, 100, "Capturing JavaScript coverage during navigation...")
		collectedCoverage, err := collectJSCoverage(ctx, deps, jsCoverageInput{Navigate: input.URL, TopN: 5})
		if err != nil {
			notes = append(notes, "JavaScript coverage was unavailable during navigation: "+err.Error())
			server.NotifyProgress(ctx, req, 25, 100, "Falling back to direct navigation...")
			pageInfo, err = navigateWorkflowURL(ctx, deps, input.URL, input.WaitMs)
			if err != nil {
				return workflowReport{}, fmt.Errorf("workflow navigation failed: %w", err)
			}
		} else {
			coverage = collectedCoverage
			coverageAvailable = true
			var pageErr error
			pageInfo, pageErr = currentWorkflowPageInfo(deps)
			if pageErr != nil {
				notes = append(notes, "Current page metadata was unavailable after navigation: "+pageErr.Error())
			}
		}
	} else {
		var pageErr error
		pageInfo, pageErr = currentWorkflowPageInfo(deps)
		if pageErr != nil {
			notes = append(notes, "Current page metadata was unavailable: "+pageErr.Error())
		}

		server.NotifyProgress(ctx, req, 15, 100, "Collecting in-page JavaScript coverage...")
		collectedCoverage, err := collectJSCoverage(ctx, deps, jsCoverageInput{TopN: 5})
		if err != nil {
			notes = append(notes, "JavaScript coverage was unavailable: "+err.Error())
		} else {
			coverage = collectedCoverage
			coverageAvailable = true
			notes = append(notes, "No URL was provided, so JavaScript coverage only reflects work that happened after the workflow started.")
		}
	}

	server.NotifyProgress(ctx, req, 40, 100, "Measuring Core Web Vitals...")
	vitals, err := measureWebVitals(ctx, deps, input.WaitMs)
	if err != nil {
		return workflowReport{}, fmt.Errorf("failed to measure web vitals: %w", err)
	}

	server.NotifyProgress(ctx, req, 60, 100, "Capturing CPU profile...")
	prof, cpuErr := captureCPUProfile(ctx, deps, input.Duration, 100)
	hotspots := make([]profileHotspot, 0)
	cpuAssessment := cpuProfileAssessment{Verdict: VerdictPass}
	if cpuErr != nil {
		notes = append(notes, "CPU profiling was unavailable during this workflow run: "+cpuErr.Error())
	} else {
		hotspots = summarizeCPUHotspots(prof, 3)
		cpuAssessment = assessCPUProfile(int64(len(prof.Samples)), hotspots)
	}

	server.NotifyProgress(ctx, req, 80, 100, "Summarizing blocking resources and workflow verdict...")
	entries := snapshotNetworkEntries()
	blockingCount, largeAssetCount := summarizeBlockingResources(entries)
	if len(entries) == 0 {
		notes = append(notes, "No captured network requests were available for this run. Provide a URL or start capture before rerunning if you need blocking-resource evidence.")
	}

	lcpVerdict := VerdictWarn
	lcpSummary := "not measured"
	if vitals.LCP != nil {
		rating, _ := rateVital("LCP", *vitals.LCP)
		lcpVerdict = verdictFromVitalRating(rating)
		lcpSummary = fmt.Sprintf("%.0fms (%s)", *vitals.LCP, rating)
		finding := fmt.Sprintf("LCP measured %s", lcpSummary)
		if vitals.LCPElement != "" && vitals.LCPElement != "unknown" {
			finding += fmt.Sprintf(" on %s", vitals.LCPElement)
		}
		findings = append(findings, finding+".")
	} else {
		findings = append(findings, "LCP was not recorded in this run.")
	}

	if len(entries) > 0 {
		findings = append(findings, fmt.Sprintf("Network capture recorded %d requests, %d render-blocking resources, and %d large uncached assets.", len(entries), blockingCount, largeAssetCount))
	}

	jsUnusedSummary := "not captured"
	jsSummary := jsBloatSummary{Verdict: VerdictPass}
	jsUnusedPct := 0.0
	var coverageOverview jsCoverageOverview
	if coverageAvailable {
		coverageOverview = summarizeJSCoverage(coverage)
		if coverageOverview.TotalBytes > 0 {
			jsUnusedPct = float64(coverageOverview.UnusedBytes) / float64(coverageOverview.TotalBytes) * 100
		}
		jsSummary = summarizeJSBloatAssessment(jsUnusedPct, cpuAssessment.Verdict != VerdictPass)
		jsUnusedSummary = format.Percent(jsUnusedPct)
		findings = append(findings, fmt.Sprintf("JavaScript coverage found %s unused code (%s of %s analyzed).", jsUnusedSummary, format.Bytes(coverageOverview.UnusedBytes), format.Bytes(coverageOverview.TotalBytes)))
		if coverageOverview.TotalBytes == 0 {
			notes = append(notes, "JavaScript coverage completed but captured no script bytes in this run.")
		}
	}

	if len(hotspots) > 0 && prof != nil && len(prof.Samples) > 0 {
		dominantPct := float64(hotspots[0].SelfTime) / float64(len(prof.Samples)) * 100
		findings = append(findings, fmt.Sprintf("Top CPU hotspot: %s at %s of sampled time.", hotspots[0].FuncName, format.Percent(dominantPct)))
	}

	assessment := slowPageAssessment{
		LCPRating:          lcpVerdict,
		BlockingCSSCount:   blockingCount,
		CPUHotspotDetected: cpuAssessment.Verdict != VerdictPass,
	}
	if coverageAvailable {
		assessment.JSUnusedPercent = jsUnusedPct
	}

	nextSteps := slowPageNextSteps(assessment)
	if !coverageAvailable {
		nextSteps = appendUniqueStep(nextSteps, "Collect JavaScript coverage on a fresh navigation if you need a confident bundle-waste reading.")
	}
	if len(entries) == 0 {
		nextSteps = appendUniqueStep(nextSteps, "Capture a fresh navigation with network logging enabled if you need render-blocking resource evidence.")
	}

	verdicts := []Verdict{lcpVerdict}
	if blockingCount > 0 {
		verdicts = append(verdicts, VerdictWarn)
	}
	if coverageAvailable {
		verdicts = append(verdicts, jsSummary.Verdict)
	}
	if prof != nil {
		verdicts = append(verdicts, cpuAssessment.Verdict)
	}

	summaryPairs := [][2]string{{"Workflow", input.Name}}
	if pageInfo.URL != "" {
		summaryPairs = append(summaryPairs, [2]string{"Page", pageInfo.URL})
	}
	if lcpSummary != "" {
		summaryPairs = append(summaryPairs, [2]string{"LCP", lcpSummary})
	}
	summaryPairs = append(summaryPairs,
		[2]string{"Blocking Resources", fmt.Sprintf("%d", blockingCount)},
		[2]string{"Unused JS", jsUnusedSummary},
	)

	return workflowReport{
		SummaryPairs: summaryPairs,
		Verdict:      worstVerdict(verdicts...),
		Findings:     findings,
		NextSteps:    nextSteps,
		Notes:        notes,
		ExtraSections: []string{
			formatTopScriptsSection(coverageOverview.Stats, 5),
		},
	}, nil
}

func runJSBloatCheckWorkflow(ctx context.Context, req *mcp.CallToolRequest, deps *Deps, input workflowInput) (workflowReport, error) {
	notes := make([]string, 0)

	server.NotifyProgress(ctx, req, 10, 100, "Collecting JavaScript coverage...")
	coverageInput := jsCoverageInput{TopN: 5}
	if input.URL != "" {
		coverageInput.Navigate = input.URL
	} else {
		notes = append(notes, "No URL was provided, so this coverage snapshot only reflects work that occurred after the workflow started.")
	}

	coverage, err := collectJSCoverage(ctx, deps, coverageInput)
	if err != nil {
		return workflowReport{}, fmt.Errorf("failed to collect JavaScript coverage: %w", err)
	}

	pageInfo, pageErr := currentWorkflowPageInfo(deps)
	if pageErr != nil {
		notes = append(notes, "Current page metadata was unavailable after coverage collection: "+pageErr.Error())
	}

	overview := summarizeJSCoverage(coverage)
	unusedPct := 0.0
	if overview.TotalBytes > 0 {
		unusedPct = float64(overview.UnusedBytes) / float64(overview.TotalBytes) * 100
	}

	server.NotifyProgress(ctx, req, 55, 100, "Capturing optional CPU profile for JS context...")
	prof, cpuErr := captureCPUProfile(ctx, deps, input.Duration, 100)
	hotspots := make([]profileHotspot, 0)
	cpuAssessment := cpuProfileAssessment{Verdict: VerdictPass}
	if cpuErr != nil {
		notes = append(notes, "CPU profiling was unavailable, so bundle execution cost may be underexplained: "+cpuErr.Error())
	} else {
		hotspots = summarizeCPUHotspots(prof, 3)
		cpuAssessment = assessCPUProfile(int64(len(prof.Samples)), hotspots)
	}

	server.NotifyProgress(ctx, req, 80, 100, "Building JS bloat assessment...")
	jsSummary := summarizeJSBloatAssessment(unusedPct, cpuAssessment.Verdict != VerdictPass)
	nextSteps := append([]string{}, jsSummary.NextSteps...)
	nextSteps = appendUniqueStep(nextSteps, "Use slow-page-triage if JavaScript waste appears alongside poor Core Web Vitals or blocking resources.")

	findings := []string{
		fmt.Sprintf("Analyzed %d scripts with code across %s of JavaScript.", overview.ScriptsWithCode, format.Bytes(overview.TotalBytes)),
		fmt.Sprintf("Unused JavaScript accounts for %s (%s).", format.Percent(unusedPct), format.Bytes(overview.UnusedBytes)),
	}
	if len(overview.Stats) > 0 {
		findings = append(findings, fmt.Sprintf("Most wasteful script in this run: %s with %s unused.", overview.Stats[0].URL, format.Bytes(overview.Stats[0].UnusedBytes)))
	}
	if len(hotspots) > 0 && prof != nil && len(prof.Samples) > 0 {
		findings = append(findings, fmt.Sprintf("Top CPU hotspot during the sample: %s.", hotspots[0].FuncName))
	}
	if overview.TotalBytes == 0 {
		notes = append(notes, "Coverage collection finished without any script bytes, so this run may not represent the page's actual startup cost.")
	}

	summaryPairs := [][2]string{
		{"Workflow", input.Name},
		{"Scripts With Code", fmt.Sprintf("%d", overview.ScriptsWithCode)},
		{"Total JS", format.Bytes(overview.TotalBytes)},
		{"Unused JS", fmt.Sprintf("%s (%s)", format.Bytes(overview.UnusedBytes), format.Percent(unusedPct))},
	}
	if pageInfo.URL != "" {
		summaryPairs = append(summaryPairs, [2]string{"Page", pageInfo.URL})
	}

	return workflowReport{
		SummaryPairs: summaryPairs,
		Verdict:      jsSummary.Verdict,
		Findings:     findings,
		NextSteps:    nextSteps,
		Notes:        notes,
		ExtraSections: []string{
			formatTopScriptsSection(overview.Stats, 5),
		},
	}, nil
}

func runAccessibilityPassWorkflow(ctx context.Context, req *mcp.CallToolRequest, deps *Deps, input workflowInput) (workflowReport, error) {
	notes := make([]string, 0)

	server.NotifyProgress(ctx, req, 15, 100, "Preparing accessibility workflow target...")
	pageInfo := workflowPageInfo{}
	if input.URL != "" {
		var err error
		pageInfo, err = navigateWorkflowURL(ctx, deps, input.URL, input.WaitMs)
		if err != nil {
			return workflowReport{}, fmt.Errorf("workflow navigation failed: %w", err)
		}
	} else {
		var pageErr error
		pageInfo, pageErr = currentWorkflowPageInfo(deps)
		if pageErr != nil {
			notes = append(notes, "Current page metadata was unavailable before scanning: "+pageErr.Error())
		}
	}

	server.NotifyProgress(ctx, req, 55, 100, "Running accessibility scan...")
	scan, _, err := runA11yScan(ctx, deps, input.Selector)
	if err != nil {
		return workflowReport{}, fmt.Errorf("failed to run accessibility scan: %w", err)
	}

	verdict, nextSteps := assessA11yScan(scan)
	findings := make([]string, 0, len(scan.Issues)+1)
	switch {
	case scan.Error != "":
		findings = append(findings, scan.Error)
	case scan.Count == 0:
		findings = append(findings, "No automated issues were found in this quick scan.")
	default:
		limit := len(scan.Issues)
		if limit > 5 {
			limit = 5
		}
		for _, issue := range scan.Issues[:limit] {
			findings = append(findings, fmt.Sprintf("%s: %s", issue.Rule, issue.Detail))
		}
	}
	if scan.Count > len(scan.Issues) {
		notes = append(notes, fmt.Sprintf("Only the first %d issues are included in the quick scan output.", len(scan.Issues)))
	}

	summaryPairs := [][2]string{{"Workflow", input.Name}, {"Issues", fmt.Sprintf("%d", scan.Count)}}
	if pageInfo.URL != "" {
		summaryPairs = append(summaryPairs, [2]string{"Page", pageInfo.URL})
	}
	if input.Selector != "" {
		summaryPairs = append(summaryPairs, [2]string{"Scope", input.Selector})
	}

	return workflowReport{
		SummaryPairs: summaryPairs,
		Verdict:      verdict,
		Findings:     findings,
		NextSteps:    nextSteps,
		Notes:        notes,
	}, nil
}

func formatWorkflowReport(title string, report workflowReport) string {
	parts := make([]string, 0, 8)
	if len(report.SummaryPairs) > 0 {
		parts = append(parts, format.Summary(report.SummaryPairs), "")
	}
	parts = append(parts, fmt.Sprintf("**Verdict**: %s", report.Verdict))
	if len(report.Findings) > 0 {
		parts = append(parts, "", format.Section("Key Findings", format.BulletList(report.Findings)))
	}
	for _, section := range report.ExtraSections {
		if section == "" {
			continue
		}
		parts = append(parts, "", section)
	}
	if len(report.Notes) > 0 {
		parts = append(parts, "", format.Section("Workflow Notes", format.BulletList(report.Notes)))
	}
	if len(report.NextSteps) == 0 {
		report.NextSteps = []string{"No follow-up steps were generated for this workflow run."}
	}
	parts = append(parts, "", format.Section("Recommended Next Steps", format.BulletList(report.NextSteps)))

	return format.ToolResult(title, parts...)
}

func formatTopScriptsSection(stats []scriptCoverageStats, limit int) string {
	if len(stats) == 0 {
		return ""
	}
	if limit <= 0 {
		limit = len(stats)
	}
	if len(stats) > limit {
		stats = stats[:limit]
	}

	headers := []string{"#", "Script", "Total", "Unused", "Used %"}
	rows := make([][]string, 0, len(stats))
	for i, stat := range stats {
		url := stat.URL
		if len(url) > 55 {
			url = "…" + url[len(url)-54:]
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", i+1),
			url,
			format.Bytes(stat.TotalBytes),
			format.Bytes(stat.UnusedBytes),
			format.Percent(stat.UsedPct),
		})
	}

	return format.Section("Top Scripts by Unused Bytes", format.Table(headers, rows))
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/performance"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
	"github.com/edbnme/pen/internal/server"
)

// --- pen_performance_metrics ---

type perfMetricsInput struct{}

func registerAuditTools(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_performance_metrics",
		Description: "Get real-time performance metrics from the browser (heap size, DOM nodes, layout count, etc.). Instant — no profiling required. Use as a quick first step before deeper analysis with pen_cpu_profile or pen_lighthouse.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Performance Metrics",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makePerfMetricsHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_web_vitals",
		Description: "Capture Core Web Vitals (LCP, CLS, INP estimate). Evaluates performance observer entries in the page context. Best used after page load completes. For deeper analysis, follow up with pen_cpu_profile or pen_capture_trace.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Core Web Vitals",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, makeWebVitalsHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_accessibility_check",
		Description: "Quick accessibility scan: missing alt text, unlabeled inputs, contrast issues, ARIA violations.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Accessibility Check",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, makeA11yCheckHandler(deps))
}

// --- pen_performance_metrics handler ---

func makePerfMetricsHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, perfMetricsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, _ perfMetricsInput) (*mcp.CallToolResult, any, error) {
		metrics, err := collectPerformanceMetrics(ctx, deps)
		if err != nil {
			return toolError("failed to get metrics: " + err.Error())
		}

		// Format as table.
		headers := []string{"Metric", "Value"}
		rows := make([][]string, 0, len(metrics))
		for _, m := range metrics {
			rows = append(rows, []string{m.Name, formatMetricValue(m.Name, m.Value)})
		}

		output := format.ToolResult("Performance Metrics",
			format.Summary([][2]string{
				{"Captured at", time.Now().UTC().Format(time.RFC3339)},
				{"Metrics", fmt.Sprintf("%d", len(metrics))},
			}),
			"",
			format.Table(headers, rows),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

func collectPerformanceMetrics(ctx context.Context, deps *Deps) ([]*performance.Metric, error) {
	cdpCtx, err := deps.CDP.Context()
	if err != nil {
		return nil, fmt.Errorf("CDP not connected: %w", err)
	}

	var metrics []*performance.Metric
	if err := chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := performance.Enable().Do(ctx); err != nil {
			return err
		}
		var err error
		metrics, err = performance.GetMetrics().Do(ctx)
		return err
	})); err != nil {
		return nil, err
	}

	return metrics, nil
}

// formatMetricValue formats metric values based on the metric name.
func formatMetricValue(name string, val float64) string {
	switch {
	case strings.Contains(name, "Size") || strings.Contains(name, "Bytes"):
		return format.Bytes(int64(val))
	case strings.Contains(name, "Duration") || strings.Contains(name, "Time"):
		return format.Duration(time.Duration(val * float64(time.Second)))
	default:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%.2f", val)
	}
}

// --- pen_web_vitals handler ---

type webVitalsInput struct {
	WaitMs int `json:"waitMs,omitempty" jsonschema:"Milliseconds to wait for metrics to stabilize (default 3000). Increase for slow pages."`
}

type measuredWebVitals struct {
	LCP        *float64 `json:"lcp"`
	LCPElement string   `json:"lcpElement"`
	CLS        float64  `json:"cls"`
	INP        *float64 `json:"inp"`
	FCP        *float64 `json:"fcp"`
	TTFB       *float64 `json:"ttfb"`
}

type webVitalsResult = measuredWebVitals

func makeWebVitalsHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, webVitalsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input webVitalsInput) (*mcp.CallToolResult, any, error) {
		server.NotifyProgress(ctx, req, 0, 100, "Injecting PerformanceObservers...")

		vitals, err := measureWebVitals(ctx, deps, input.WaitMs)
		if err != nil {
			return toolError("failed to measure Web Vitals: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 80, 100, "Formatting results...")

		server.NotifyProgress(ctx, req, 100, 100, "Complete")

		output := formatWebVitalsReport(vitals, time.Now().UTC())

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

func measureWebVitals(ctx context.Context, deps *Deps, waitMs int) (webVitalsResult, error) {
	cdpCtx, err := deps.CDP.Context()
	if err != nil {
		return measuredWebVitals{}, fmt.Errorf("CDP not connected: %w", err)
	}

	if waitMs <= 0 {
		waitMs = 3000
	}
	if waitMs > 30000 {
		waitMs = 30000
	}

	vitalsScript := fmt.Sprintf(`(async () => {
		const result = { lcp: null, lcpElement: '', cls: 0, inp: null, fcp: null, ttfb: null };
		const wait = %d;

		try {
			await new Promise((resolve) => {
				new PerformanceObserver((list) => {
					const entries = list.getEntries();
					if (entries.length > 0) {
						const last = entries[entries.length - 1];
						result.lcp = last.startTime;
						result.lcpElement = last.element
							? last.element.tagName + (last.element.id ? '#' + last.element.id : '') + (last.element.className ? '.' + last.element.className.split(' ')[0] : '')
							: 'unknown';
					}
					resolve();
				}).observe({ type: 'largest-contentful-paint', buffered: true });
				setTimeout(resolve, Math.min(wait, 2000));
			});
		} catch(e) {}

		try {
			await new Promise((resolve) => {
				let clsValue = 0, sessionValue = 0, sessionEntries = [], prevTs = 0;
				new PerformanceObserver((list) => {
					for (const entry of list.getEntries()) {
						if (!entry.hadRecentInput) {
							if (entry.startTime - prevTs < 1000 && sessionEntries.length &&
								entry.startTime - sessionEntries[0].startTime < 5000) {
								sessionValue += entry.value;
								sessionEntries.push(entry);
							} else {
								sessionValue = entry.value;
								sessionEntries = [entry];
							}
							if (sessionValue > clsValue) clsValue = sessionValue;
							prevTs = entry.startTime;
						}
					}
					result.cls = clsValue;
					resolve();
				}).observe({ type: 'layout-shift', buffered: true });
				setTimeout(resolve, Math.min(wait, 1000));
			});
		} catch(e) {}

		try {
			await new Promise((resolve) => {
				new PerformanceObserver((list) => {
					const durations = list.getEntries().map(e => e.duration).filter(d => d > 0);
					if (durations.length > 0) {
						durations.sort((a, b) => b - a);
						result.inp = durations[0];
					}
					resolve();
				}).observe({ type: 'event', buffered: true });
				setTimeout(resolve, Math.min(wait, 1000));
			});
		} catch(e) {}

		try {
			const paintEntries = performance.getEntriesByType('paint');
			const fcp = paintEntries.find(e => e.name === 'first-contentful-paint');
			if (fcp) result.fcp = fcp.startTime;
		} catch(e) {}

		try {
			const nav = performance.getEntriesByType('navigation');
			if (nav.length > 0) result.ttfb = nav[0].responseStart;
		} catch(e) {}

		return JSON.stringify(result);
	})()`, waitMs)

	var vitalsJSON string
	if err := chromedp.Run(cdpCtx, chromedp.Evaluate(vitalsScript, &vitalsJSON, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	})); err != nil {
		return measuredWebVitals{}, err
	}

	var vitals measuredWebVitals
	if err := json.Unmarshal([]byte(vitalsJSON), &vitals); err != nil {
		return measuredWebVitals{}, err
	}

	return vitals, nil
}

func formatWebVitalsReport(vitals measuredWebVitals, measuredAt time.Time) string {
	headers := []string{"Metric", "Value", "Rating", "Threshold"}
	rows := make([][]string, 0, 5)
	verdict := VerdictPass
	var notes []string
	var nextSteps []string

	if vitals.LCP != nil {
		value := *vitals.LCP
		rating, thresholds := rateVital("LCP", value)
		rows = append(rows, []string{
			"Largest Contentful Paint (LCP)",
			fmt.Sprintf("%.0fms", value),
			rating,
			thresholds,
		})
		verdict = worstVerdict(verdict, verdictFromVitalRating(rating))
		if verdictFromVitalRating(rating) != VerdictPass {
			nextSteps = appendUniqueStep(nextSteps, "Run pen_network_blocking to inspect render-blocking CSS and JavaScript affecting the critical path to the LCP element.")
		}
	} else {
		rows = append(rows, []string{
			"Largest Contentful Paint (LCP)",
			"—",
			"Not measured",
			"Good < 2500ms",
		})
		verdict = worstVerdict(verdict, VerdictWarn)
		notes = append(notes, "LCP was not recorded. This usually means the page has not completed a full navigation. Try: pen_navigate to the page first, then re-run pen_web_vitals.")
		nextSteps = appendUniqueStep(nextSteps, "Re-run after a full navigation so LCP can be measured, then compare the reported LCP element against blocking resources.")
	}

	if vitals.FCP != nil {
		value := *vitals.FCP
		rating, thresholds := rateVital("FCP", value)
		rows = append(rows, []string{
			"First Contentful Paint (FCP)",
			fmt.Sprintf("%.0fms", value),
			rating,
			thresholds,
		})
		if verdictFromVitalRating(rating) != VerdictPass {
			nextSteps = appendUniqueStep(nextSteps, "Review first-paint blockers and non-critical CSS or JavaScript on the initial render path if FCP remains elevated.")
		}
	}

	clsRating, clsThresholds := rateVital("CLS", vitals.CLS)
	rows = append(rows, []string{
		"Cumulative Layout Shift (CLS)",
		fmt.Sprintf("%.4f", vitals.CLS),
		clsRating,
		clsThresholds,
	})
	verdict = worstVerdict(verdict, verdictFromVitalRating(clsRating))
	if verdictFromVitalRating(clsRating) != VerdictPass {
		nextSteps = appendUniqueStep(nextSteps, "Inspect shifting images, ads, and late-inserted UI near the viewport before re-testing layout stability.")
	}

	if vitals.INP != nil {
		value := *vitals.INP
		rating, thresholds := rateVital("INP", value)
		rows = append(rows, []string{
			"Interaction to Next Paint (INP)",
			fmt.Sprintf("%.0fms", value),
			rating,
			thresholds,
		})
		verdict = worstVerdict(verdict, verdictFromVitalRating(rating))
		if verdictFromVitalRating(rating) != VerdictPass {
			nextSteps = appendUniqueStep(nextSteps, "Capture a CPU profile or trace during the slow interaction to identify long tasks hurting responsiveness.")
		}
	} else {
		rows = append(rows, []string{
			"Interaction to Next Paint (INP)",
			"—",
			"No interactions recorded",
			"Good < 200ms",
		})
		verdict = worstVerdict(verdict, VerdictWarn)
		notes = append(notes, "INP requires user interactions (clicks, taps, key presses) to be measured. Interact with the page first, then re-run.")
		nextSteps = appendUniqueStep(nextSteps, "Interact with the page and rerun pen_web_vitals so INP can be measured before you treat responsiveness as healthy.")
	}

	if vitals.TTFB != nil {
		value := *vitals.TTFB
		rating, thresholds := rateVital("TTFB", value)
		rows = append(rows, []string{
			"Time to First Byte (TTFB)",
			fmt.Sprintf("%.0fms", value),
			rating,
			thresholds,
		})
		if verdictFromVitalRating(rating) != VerdictPass {
			nextSteps = appendUniqueStep(nextSteps, "Investigate server response time and upstream latency before focusing only on frontend rendering work.")
		}
	}

	if len(nextSteps) == 0 {
		nextSteps = append(nextSteps, "Core Web Vitals look healthy in this sample. Use the slow-page workflow if the page still feels slow during real interactions.")
	}

	summaryPairs := [][2]string{
		{"Measured at", measuredAt.Format(time.RFC3339)},
	}
	if vitals.LCPElement != "" && vitals.LCPElement != "unknown" {
		summaryPairs = append(summaryPairs, [2]string{"LCP Element", vitals.LCPElement})
	}

	parts := []string{
		format.Summary(summaryPairs),
		"",
		fmt.Sprintf("**Verdict**: %s", verdict),
		"",
		format.Table(headers, rows),
	}
	if len(notes) > 0 {
		parts = append(parts, "", format.Section("Notes", format.BulletList(notes)))
	}
	parts = append(parts, "", format.Section("Recommended Next Steps", format.BulletList(nextSteps)))

	return format.ToolResult("Core Web Vitals", parts...)
}

// rateVital returns a rating and threshold string for a Web Vital metric.
// Thresholds from https://web.dev/vitals/
func rateVital(metric string, value float64) (rating, thresholds string) {
	switch metric {
	case "LCP":
		thresholds = "Good < 2500ms | Poor > 4000ms"
		switch {
		case value < 2500:
			return "Good", thresholds
		case value < 4000:
			return "Needs Improvement", thresholds
		default:
			return "Poor", thresholds
		}
	case "FCP":
		thresholds = "Good < 1800ms | Poor > 3000ms"
		switch {
		case value < 1800:
			return "Good", thresholds
		case value < 3000:
			return "Needs Improvement", thresholds
		default:
			return "Poor", thresholds
		}
	case "CLS":
		thresholds = "Good < 0.1 | Poor > 0.25"
		switch {
		case value < 0.1:
			return "Good", thresholds
		case value < 0.25:
			return "Needs Improvement", thresholds
		default:
			return "Poor", thresholds
		}
	case "INP":
		thresholds = "Good < 200ms | Poor > 500ms"
		switch {
		case value < 200:
			return "Good", thresholds
		case value < 500:
			return "Needs Improvement", thresholds
		default:
			return "Poor", thresholds
		}
	case "TTFB":
		thresholds = "Good < 800ms | Poor > 1800ms"
		switch {
		case value < 800:
			return "Good", thresholds
		case value < 1800:
			return "Needs Improvement", thresholds
		default:
			return "Poor", thresholds
		}
	default:
		return "—", ""
	}
}

// --- pen_accessibility_check handler ---

type a11yCheckInput struct {
	Selector string `json:"selector,omitempty" jsonschema:"CSS selector to scope the check (optional, default: full page)"`
}

type a11yIssue struct {
	Rule    string `json:"rule"`
	Element string `json:"element"`
	Detail  string `json:"detail"`
}

type a11yScanResult struct {
	Error  string      `json:"error"`
	Count  int         `json:"count"`
	Issues []a11yIssue `json:"issues"`
}

func runA11yScan(ctx context.Context, deps *Deps, selector string) (a11yScanResult, string, error) {
	cdpCtx, err := deps.CDP.Context()
	if err != nil {
		return a11yScanResult{}, "", fmt.Errorf("CDP not connected: %w", err)
	}

	scope := "document"
	if selector != "" {
		scope = fmt.Sprintf("document.querySelector(%q)", selector)
	}

	script := fmt.Sprintf(`(() => {
		const root = %s;
		if (!root) return JSON.stringify({error: "Selector not found"});

		const issues = [];

		root.querySelectorAll('img').forEach(img => {
			if (!img.hasAttribute('alt')) {
				issues.push({rule: 'img-alt', element: img.outerHTML.substring(0, 100), detail: 'Missing alt attribute'});
			}
		});

		root.querySelectorAll('input, select, textarea').forEach(el => {
			const id = el.id;
			const hasLabel = id && root.querySelector('label[for="' + id + '"]');
			const hasAriaLabel = el.hasAttribute('aria-label') || el.hasAttribute('aria-labelledby');
			const wrappedInLabel = el.closest('label');
			if (!hasLabel && !hasAriaLabel && !wrappedInLabel) {
				issues.push({rule: 'input-label', element: el.outerHTML.substring(0, 100), detail: 'Input without associated label'});
			}
		});

		root.querySelectorAll('button').forEach(btn => {
			if (!btn.textContent.trim() && !btn.hasAttribute('aria-label') && !btn.querySelector('img[alt]')) {
				issues.push({rule: 'button-name', element: btn.outerHTML.substring(0, 100), detail: 'Button without accessible name'});
			}
		});

		const headings = root.querySelectorAll('h1, h2, h3, h4, h5, h6');
		let prevLevel = 0;
		headings.forEach(h => {
			const level = parseInt(h.tagName[1]);
			if (level > prevLevel + 1 && prevLevel > 0) {
				issues.push({rule: 'heading-order', element: h.tagName + ': ' + h.textContent.substring(0, 50), detail: 'Skipped heading level (h' + prevLevel + ' to h' + level + ')'});
			}
			prevLevel = level;
		});

		if (!document.documentElement.hasAttribute('lang')) {
			issues.push({rule: 'html-lang', element: '<html>', detail: 'Missing lang attribute on html element'});
		}

		return JSON.stringify({count: issues.length, issues: issues.slice(0, 50)});
	})()`, scope)

	var resultJSON string
	if err := chromedp.Run(cdpCtx, chromedp.Evaluate(script, &resultJSON)); err != nil {
		return a11yScanResult{}, "", err
	}

	var scanResult a11yScanResult
	if err := json.Unmarshal([]byte(resultJSON), &scanResult); err != nil {
		return a11yScanResult{}, resultJSON, err
	}

	return scanResult, resultJSON, nil
}

func assessA11yScan(scan a11yScanResult) (Verdict, []string) {
	verdict := VerdictPass
	nextSteps := []string{
		"Follow up with Lighthouse and manual keyboard or screen-reader testing because this quick scan only covers a subset of accessibility failures.",
	}

	switch {
	case scan.Error != "":
		verdict = VerdictWarn
		nextSteps = append([]string{
			"Verify the selector exists in the current document or rerun without a selector so the scan can inspect the intended scope.",
		}, nextSteps...)
	case scan.Count > 0:
		verdict = VerdictWarn
		nextSteps = append([]string{
			"Fix the reported automated issues, then rerun pen_accessibility_check to confirm the obvious regressions are gone.",
		}, nextSteps...)
	default:
		nextSteps = append([]string{
			"No automated issues were detected in this quick pass. Keep going with Lighthouse and manual testing before treating the page as accessible.",
		}, nextSteps...)
	}

	return verdict, nextSteps
}

func makeA11yCheckHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, a11yCheckInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input a11yCheckInput) (*mcp.CallToolResult, any, error) {
		server.NotifyProgress(ctx, req, 0, 100, "Scanning accessibility...")

		scanResult, resultJSON, err := runA11yScan(ctx, deps, input.Selector)
		if err != nil {
			return toolError("accessibility scan failed: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 100, 100, "Complete")

		output := formatA11yScanReport(scanResult, resultJSON)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

func formatA11yScanReport(scan a11yScanResult, rawJSON string) string {
	verdict, nextSteps := assessA11yScan(scan)

	return format.ToolResult("Accessibility Scan",
		fmt.Sprintf("**Verdict**: %s", verdict),
		"",
		"```json\n"+rawJSON+"\n```",
		"",
		format.Section("Recommended Next Steps", format.BulletList(nextSteps)),
		"",
		"**Note**: This is a quick automated scan. It does not replace manual accessibility testing or a full WCAG 2.2 audit. Use Lighthouse for more comprehensive checks.",
	)
}

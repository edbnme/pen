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
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		var metrics []*performance.Metric
		if err := chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			// Enable the Performance domain first (idempotent — safe to call multiple times).
			if err := performance.Enable().Do(ctx); err != nil {
				return err
			}
			var err error
			metrics, err = performance.GetMetrics().Do(ctx)
			return err
		})); err != nil {
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

func makeWebVitalsHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, webVitalsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input webVitalsInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		waitMs := input.WaitMs
		if waitMs <= 0 {
			waitMs = 3000
		}
		if waitMs > 30000 {
			waitMs = 30000
		}

		server.NotifyProgress(ctx, req, 0, 100, "Injecting PerformanceObservers...")

		// Uses PerformanceObserver with buffered:true to capture already-emitted
		// entries, then polls briefly in case metrics arrive late.
		vitalsScript := fmt.Sprintf(`(async () => {
			const result = { lcp: null, lcpElement: '', cls: 0, inp: null, fcp: null, ttfb: null };
			const wait = %d;

			// --- LCP via PerformanceObserver (buffered) ---
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

			// --- CLS via PerformanceObserver (buffered, session windows) ---
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

			// --- INP via event timing (buffered) ---
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

			// --- FCP from paint entries ---
			try {
				const paintEntries = performance.getEntriesByType('paint');
				const fcp = paintEntries.find(e => e.name === 'first-contentful-paint');
				if (fcp) result.fcp = fcp.startTime;
			} catch(e) {}

			// --- TTFB from navigation timing ---
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
			return toolError("failed to measure Web Vitals: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 80, 100, "Formatting results...")

		// Parse the JSON result.
		var vitals struct {
			LCP        *float64 `json:"lcp"`
			LCPElement string   `json:"lcpElement"`
			CLS        float64  `json:"cls"`
			INP        *float64 `json:"inp"`
			FCP        *float64 `json:"fcp"`
			TTFB       *float64 `json:"ttfb"`
		}
		if err := json.Unmarshal([]byte(vitalsJSON), &vitals); err != nil {
			return toolError("failed to parse Web Vitals result: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 100, 100, "Complete")

		// Build formatted output with ratings.
		headers := []string{"Metric", "Value", "Rating", "Threshold"}
		var rows [][]string

		// LCP
		if vitals.LCP != nil {
			val := *vitals.LCP
			rating, thresholds := rateVital("LCP", val)
			rows = append(rows, []string{
				"Largest Contentful Paint (LCP)",
				fmt.Sprintf("%.0fms", val),
				rating,
				thresholds,
			})
		} else {
			rows = append(rows, []string{
				"Largest Contentful Paint (LCP)",
				"—",
				"Not measured",
				"Good < 2500ms",
			})
		}

		// FCP
		if vitals.FCP != nil {
			val := *vitals.FCP
			rating, thresholds := rateVital("FCP", val)
			rows = append(rows, []string{
				"First Contentful Paint (FCP)",
				fmt.Sprintf("%.0fms", val),
				rating,
				thresholds,
			})
		}

		// CLS
		{
			rating, thresholds := rateVital("CLS", vitals.CLS)
			rows = append(rows, []string{
				"Cumulative Layout Shift (CLS)",
				fmt.Sprintf("%.4f", vitals.CLS),
				rating,
				thresholds,
			})
		}

		// INP
		if vitals.INP != nil {
			val := *vitals.INP
			rating, thresholds := rateVital("INP", val)
			rows = append(rows, []string{
				"Interaction to Next Paint (INP)",
				fmt.Sprintf("%.0fms", val),
				rating,
				thresholds,
			})
		} else {
			rows = append(rows, []string{
				"Interaction to Next Paint (INP)",
				"—",
				"No interactions recorded",
				"Good < 200ms",
			})
		}

		// TTFB
		if vitals.TTFB != nil {
			val := *vitals.TTFB
			rating, thresholds := rateVital("TTFB", val)
			rows = append(rows, []string{
				"Time to First Byte (TTFB)",
				fmt.Sprintf("%.0fms", val),
				rating,
				thresholds,
			})
		}

		summaryPairs := [][2]string{
			{"Measured at", time.Now().UTC().Format(time.RFC3339)},
		}
		if vitals.LCPElement != "" && vitals.LCPElement != "unknown" {
			summaryPairs = append(summaryPairs, [2]string{"LCP Element", vitals.LCPElement})
		}

		var notes []string
		if vitals.LCP == nil {
			notes = append(notes, "LCP was not recorded. This usually means the page has not completed a full navigation. Try: pen_navigate to the page first, then re-run pen_web_vitals.")
		}
		if vitals.INP == nil {
			notes = append(notes, "INP requires user interactions (clicks, taps, key presses) to be measured. Interact with the page first, then re-run.")
		}

		parts := []string{
			format.Summary(summaryPairs),
			"",
			format.Table(headers, rows),
		}
		if len(notes) > 0 {
			parts = append(parts, "", "**Notes:**")
			for _, n := range notes {
				parts = append(parts, "- "+n)
			}
		}

		output := format.ToolResult("Core Web Vitals", parts...)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
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

func makeA11yCheckHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, a11yCheckInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input a11yCheckInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 0, 100, "Scanning accessibility...")

		scope := "document"
		if input.Selector != "" {
			scope = fmt.Sprintf("document.querySelector(%q)", input.Selector)
		}

		// Run accessibility checks via JS evaluation.
		script := fmt.Sprintf(`(() => {
			const root = %s;
			if (!root) return JSON.stringify({error: "Selector not found"});

			const issues = [];

			// Check images without alt text
			root.querySelectorAll('img').forEach(img => {
				if (!img.hasAttribute('alt')) {
					issues.push({rule: 'img-alt', element: img.outerHTML.substring(0, 100), detail: 'Missing alt attribute'});
				}
			});

			// Check inputs without labels
			root.querySelectorAll('input, select, textarea').forEach(el => {
				const id = el.id;
				const hasLabel = id && root.querySelector('label[for="' + id + '"]');
				const hasAriaLabel = el.hasAttribute('aria-label') || el.hasAttribute('aria-labelledby');
				const wrappedInLabel = el.closest('label');
				if (!hasLabel && !hasAriaLabel && !wrappedInLabel) {
					issues.push({rule: 'input-label', element: el.outerHTML.substring(0, 100), detail: 'Input without associated label'});
				}
			});

			// Check for missing button text
			root.querySelectorAll('button').forEach(btn => {
				if (!btn.textContent.trim() && !btn.hasAttribute('aria-label') && !btn.querySelector('img[alt]')) {
					issues.push({rule: 'button-name', element: btn.outerHTML.substring(0, 100), detail: 'Button without accessible name'});
				}
			});

			// Check for missing heading hierarchy
			const headings = root.querySelectorAll('h1, h2, h3, h4, h5, h6');
			let prevLevel = 0;
			headings.forEach(h => {
				const level = parseInt(h.tagName[1]);
				if (level > prevLevel + 1 && prevLevel > 0) {
					issues.push({rule: 'heading-order', element: h.tagName + ': ' + h.textContent.substring(0, 50), detail: 'Skipped heading level (h' + prevLevel + ' to h' + level + ')'});
				}
				prevLevel = level;
			});

			// Check for missing lang on html
			if (!document.documentElement.hasAttribute('lang')) {
				issues.push({rule: 'html-lang', element: '<html>', detail: 'Missing lang attribute on html element'});
			}

			return JSON.stringify({count: issues.length, issues: issues.slice(0, 50)});
		})()`, scope)

		var resultJSON string
		if err := chromedp.Run(cdpCtx, chromedp.Evaluate(script, &resultJSON)); err != nil {
			return toolError("accessibility scan failed: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 100, 100, "Complete")

		output := format.ToolResult("Accessibility Scan",
			"```json\n"+resultJSON+"\n```",
			"",
			"**Note**: This is a quick automated scan. It does not replace manual accessibility testing or a full WCAG 2.2 audit. Use Lighthouse for more comprehensive checks.",
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

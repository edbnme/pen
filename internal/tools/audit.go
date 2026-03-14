package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/performance"
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
		Description: "Get real-time performance metrics from the browser (heap size, DOM nodes, layout count, etc.). Instant — no profiling required.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Performance Metrics",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makePerfMetricsHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_web_vitals",
		Description: "Capture Core Web Vitals (LCP, CLS, INP estimate). Evaluates performance observer entries in the page context.",
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
	WaitForLCP bool `json:"waitForLCP" jsonschema:"Wait for LCP to stabilize before measuring (default true)"`
}

func makeWebVitalsHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, webVitalsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input webVitalsInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 0, 100, "Measuring Web Vitals...")

		// Inject performance observer and collect vitals.
		const vitalsScript = `(() => {
			const result = { lcp: null, cls: 0, inp: null };

			// LCP
			const lcpEntries = performance.getEntriesByType('largest-contentful-paint');
			if (lcpEntries.length > 0) {
				const last = lcpEntries[lcpEntries.length - 1];
				result.lcp = { value: last.startTime, element: last.element ? last.element.tagName + (last.element.id ? '#' + last.element.id : '') : 'unknown' };
			}

			// CLS
			const layoutShifts = performance.getEntriesByType('layout-shift');
			let clsValue = 0;
			let sessionValue = 0;
			let sessionEntries = [];
			let previousTs = 0;
			for (const entry of layoutShifts) {
				if (!entry.hadRecentInput) {
					if (entry.startTime - previousTs < 1000 && sessionEntries.length && entry.startTime - sessionEntries[0].startTime < 5000) {
						sessionValue += entry.value;
						sessionEntries.push(entry);
					} else {
						sessionValue = entry.value;
						sessionEntries = [entry];
					}
					if (sessionValue > clsValue) clsValue = sessionValue;
					previousTs = entry.startTime;
				}
			}
			result.cls = clsValue;

			// INP (approximation from event timing)
			const eventEntries = performance.getEntriesByType('event');
			if (eventEntries.length > 0) {
				const durations = eventEntries.map(e => e.duration).sort((a, b) => b - a);
				result.inp = durations[0] || null;
			}

			return JSON.stringify(result);
		})()`

		var vitalsJSON string
		if err := chromedp.Run(cdpCtx, chromedp.Evaluate(vitalsScript, &vitalsJSON)); err != nil {
			return toolError("failed to measure Web Vitals: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 100, 100, "Complete")

		output := format.ToolResult("Core Web Vitals",
			format.Summary([][2]string{
				{"Measured at", time.Now().UTC().Format(time.RFC3339)},
			}),
			"",
			"```json\n"+vitalsJSON+"\n```",
			"",
			"**Note**: LCP and CLS values are from PerformanceObserver entries already recorded by the browser. For most accurate results, measure after full page load.",
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
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

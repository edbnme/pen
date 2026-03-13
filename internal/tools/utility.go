package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/heapprofiler"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
	"github.com/edbnme/pen/internal/security"
	"github.com/edbnme/pen/internal/server"
)

func registerUtilityTools(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_list_pages",
		Description: "List all browser tabs/pages with URLs, titles, and target IDs.",
	}, makeListPagesHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_select_page",
		Description: "Switch PEN's target to a different browser tab by target ID or URL pattern.",
	}, makeSelectPageHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_collect_garbage",
		Description: "Force V8 garbage collection. Useful before heap snapshots for cleaner baselines.",
	}, makeCollectGarbageHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_screenshot",
		Description: "Capture a screenshot of the current page or a specific element.",
	}, makeScreenshotHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_emulate",
		Description: "Set device emulation: CPU throttling, network throttling, viewport presets.",
	}, makeEmulateHandler(deps))

	if deps.Config.AllowEval {
		mcp.AddTool(s, &mcp.Tool{
			Name:        "pen_evaluate",
			Description: "Evaluate a JavaScript expression in the page context. SECURITY: Only available when --allow-eval flag is set.",
		}, makeEvaluateHandler(deps))
	}
}

// --- pen_list_pages ---

type listPagesInput struct{}

func makeListPagesHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, listPagesInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, _ listPagesInput) (*mcp.CallToolResult, any, error) {
		targets, err := deps.CDP.ListTargets(ctx)
		if err != nil {
			return toolError("failed to list pages: " + err.Error())
		}

		headers := []string{"#", "Type", "Title", "URL", "Target ID"}
		rows := make([][]string, 0, len(targets))
		for i, t := range targets {
			title := t.Title
			if len(title) > 40 {
				title = title[:37] + "..."
			}
			url := t.URL
			if len(url) > 60 {
				url = url[:57] + "..."
			}
			rows = append(rows, []string{
				fmt.Sprintf("%d", i+1), t.Type, title, url, t.ID,
			})
		}

		current := deps.CDP.CurrentTargetID()
		output := format.ToolResult("Browser Targets",
			format.Summary([][2]string{
				{"Total targets", fmt.Sprintf("%d", len(targets))},
				{"Active target", current},
			}),
			"",
			format.Table(headers, rows),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- pen_select_page ---

type selectPageInput struct {
	TargetID   string `json:"targetId,omitempty"   jsonschema:"Target ID from pen_list_pages"`
	URLPattern string `json:"urlPattern,omitempty" jsonschema:"URL substring to match"`
}

func makeSelectPageHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, selectPageInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input selectPageInput) (*mcp.CallToolResult, any, error) {
		targetID := input.TargetID

		if targetID == "" && input.URLPattern == "" {
			return toolError("provide either targetId or urlPattern")
		}

		// Resolve by URL pattern if targetID not given.
		if targetID == "" {
			t, err := deps.CDP.FindTargetByURL(ctx, input.URLPattern)
			if err != nil {
				return toolError(err.Error())
			}
			targetID = t.ID
		}

		_, _, err := deps.CDP.SelectTarget(ctx, targetID)
		if err != nil {
			return toolError("failed to switch target: " + err.Error())
		}

		output := format.ToolResult("Target Switched",
			fmt.Sprintf("Now targeting: **%s**", targetID),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- pen_collect_garbage ---

type collectGCInput struct{}

func makeCollectGarbageHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, collectGCInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, _ collectGCInput) (*mcp.CallToolResult, any, error) {
		if err := deps.Limiter.Check("pen_collect_garbage"); err != nil {
			return toolError(err.Error())
		}

		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		start := time.Now()
		if err := chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			return heapprofiler.CollectGarbage().Do(ctx)
		})); err != nil {
			return toolError("GC failed: " + err.Error())
		}

		output := format.ToolResult("Garbage Collection",
			fmt.Sprintf("V8 garbage collection completed in %s.\n\nHeap is now compacted. Take a `pen_heap_snapshot` for a clean baseline.", format.Duration(time.Since(start))),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- pen_screenshot ---

type screenshotInput struct {
	Selector string `json:"selector,omitempty" jsonschema:"CSS selector for element screenshot"`
	FullPage bool   `json:"fullPage"           jsonschema:"Capture full page (default false)"`
	Format   string `json:"format,omitempty"   jsonschema:"Image format: png, jpeg, webp (default png)"`
	Quality  int    `json:"quality,omitempty"   jsonschema:"Image quality 0-100 for jpeg/webp"`
}

func makeScreenshotHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, screenshotInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input screenshotInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 0, 100, "Capturing screenshot...")

		imgFormat := page.CaptureScreenshotFormatPng
		switch strings.ToLower(input.Format) {
		case "jpeg", "jpg":
			imgFormat = page.CaptureScreenshotFormatJpeg
		case "webp":
			imgFormat = page.CaptureScreenshotFormatWebp
		}

		var buf []byte
		var action chromedp.Action
		if input.Selector != "" {
			action = chromedp.Screenshot(input.Selector, &buf, chromedp.NodeVisible)
		} else if input.FullPage {
			action = chromedp.FullScreenshot(&buf, 90)
		} else {
			action = chromedp.ActionFunc(func(ctx context.Context) error {
				var err error
				quality := int64(input.Quality)
				if quality <= 0 {
					quality = 90
				}
				if quality > 100 {
					quality = 100
				}
				buf, err = page.CaptureScreenshot().
					WithFormat(imgFormat).
					WithQuality(quality).
					Do(ctx)
				return err
			})
		}

		if err := chromedp.Run(cdpCtx, action); err != nil {
			return toolError("screenshot failed: " + err.Error())
		}

		server.NotifyProgress(ctx, req, 100, 100, "Complete")

		mimeType := "image/png"
		switch imgFormat {
		case page.CaptureScreenshotFormatJpeg:
			mimeType = "image/jpeg"
		case page.CaptureScreenshotFormatWebp:
			mimeType = "image/webp"
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.ImageContent{
					MIMEType: mimeType,
					Data:     buf,
				},
			},
		}, nil, nil
	}
}

// --- pen_emulate ---

type emulateInput struct {
	Device          string  `json:"device,omitempty"           jsonschema:"Device preset: iPhone 14, Pixel 7, iPad"`
	CPUThrottling   float64 `json:"cpuThrottling,omitempty"    jsonschema:"CPU slowdown factor (e.g. 4 = 4x slower)"`
	NetworkThrottle string  `json:"networkThrottling,omitempty" jsonschema:"Network preset: 3G, 4G, WiFi"`
}

func makeEmulateHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, emulateInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input emulateInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		var applied []string

		// CPU throttling via Emulation.setCPUThrottlingRate
		if input.CPUThrottling > 1 {
			if err := chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				return emulation.SetCPUThrottlingRate(input.CPUThrottling).Do(ctx)
			})); err != nil {
				return toolError("CPU throttling failed: " + err.Error())
			}
			applied = append(applied, fmt.Sprintf("CPU throttling: %.0fx slowdown", input.CPUThrottling))
		}

		// Network throttling via Network.emulateNetworkConditions
		if input.NetworkThrottle != "" {
			latency, down, up, err := networkPreset(input.NetworkThrottle)
			if err != nil {
				return toolError(err.Error())
			}
			if err := chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				return network.EmulateNetworkConditions(false, float64(latency), down, up).Do(ctx)
			})); err != nil {
				return toolError("network throttling failed: " + err.Error())
			}
			applied = append(applied, fmt.Sprintf("Network: %s (latency=%dms, down=%.1f Mbps, up=%.1f Mbps)",
				input.NetworkThrottle, latency, down*8/1_000_000, up*8/1_000_000))
		}

		if len(applied) == 0 {
			return toolError("no emulation parameters provided — specify device, cpuThrottling, or networkThrottling")
		}

		output := format.ToolResult("Emulation Applied",
			format.BulletList(applied),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// networkPreset returns (latencyMs, downloadBytesPerSec, uploadBytesPerSec).
// Values match Chrome DevTools standard presets.
func networkPreset(name string) (int, float64, float64, error) {
	switch strings.ToLower(name) {
	case "3g":
		return 563, 187_500, 93_750, nil // Fast 3G: 1.5 Mbps down, 750 Kbps up
	case "4g":
		return 170, 500_000, 375_000, nil // Regular 4G: 4 Mbps down, 3 Mbps up
	case "wifi":
		return 2, 3_750_000, 1_875_000, nil // WiFi: 30 Mbps down, 15 Mbps up
	default:
		return 0, 0, 0, fmt.Errorf("unknown network preset %q (valid: 3g, 4g, wifi)", name)
	}
}

// --- pen_evaluate ---

type evaluateInput struct {
	Expression    string `json:"expression"              jsonschema:"JavaScript expression to evaluate"`
	ReturnByValue bool   `json:"returnByValue,omitempty" jsonschema:"Return result by value (default true)"`
}

func makeEvaluateHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, evaluateInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input evaluateInput) (*mcp.CallToolResult, any, error) {
		if input.Expression == "" {
			return toolError("expression is required")
		}

		// Security gate: check expression against blocklist.
		if err := security.ValidateExpression(input.Expression); err != nil {
			return toolError(err.Error())
		}

		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		var result interface{}
		if err := chromedp.Run(cdpCtx, chromedp.Evaluate(input.Expression, &result)); err != nil {
			return toolError("eval failed: " + err.Error())
		}

		output := format.ToolResult("Evaluate Result",
			fmt.Sprintf("```\n%v\n```", result),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

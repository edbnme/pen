package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/heapprofiler"
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
		Annotations: &mcp.ToolAnnotations{
			Title:          "List Browser Pages",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makeListPagesHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_select_page",
		Description: "Switch PEN's target to a different browser tab by target ID or URL pattern.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Select Page",
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(false),
		},
	}, makeSelectPageHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_collect_garbage",
		Description: "Force V8 garbage collection. Useful before heap snapshots for cleaner baselines.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Collect Garbage",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, makeCollectGarbageHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_screenshot",
		Description: "Capture a screenshot of the current page or a specific element.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Screenshot",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, makeScreenshotHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_navigate",
		Description: "Navigate to a URL, go back, go forward, or reload. Only http/https URLs allowed.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Navigate Page",
			DestructiveHint: boolPtr(true),
			IdempotentHint:  false,
			OpenWorldHint:   boolPtr(true),
		},
	}, makeNavigateHandler(deps))

	if deps.Config.AllowEval {
		mcp.AddTool(s, &mcp.Tool{
			Name:        "pen_evaluate",
			Description: "Evaluate a JavaScript expression in the page context. SECURITY: Only available when --allow-eval flag is set.",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Evaluate JavaScript",
				DestructiveHint: boolPtr(true),
				OpenWorldHint:   boolPtr(true),
			},
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

		// Reset event listeners — they are bound to the old CDP context
		// and will not fire for the new target.
		ResetNetworkListener()
		ResetConsoleListener()
		ResetScriptListener()

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
	FullPage bool   `json:"fullPage,omitempty"  jsonschema:"Capture full page (default false)"`
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

		// Guard against excessively large screenshots (e.g., full-page on infinite scroll).
		const maxScreenshotBytes = 5 * 1024 * 1024 // 5 MB
		if len(buf) > maxScreenshotBytes {
			return toolError(fmt.Sprintf(
				"Screenshot too large (%s). Try a viewport screenshot instead of full page, "+
					"or use a CSS selector to capture a specific element.",
				format.Bytes(int64(len(buf))),
			))
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

		resultText := fmt.Sprintf("%v", result)
		const maxEvalOutput = 50000 // 50 KB
		if len(resultText) > maxEvalOutput {
			resultText = resultText[:maxEvalOutput] +
				fmt.Sprintf("\n\n... truncated (%s total). Use more specific expressions to reduce output.",
					format.Bytes(int64(len(resultText))))
		}

		output := format.ToolResult("Evaluate Result",
			fmt.Sprintf("```\n%s\n```", resultText),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- pen_navigate ---

type navigateInput struct {
	Action string `json:"action" jsonschema:"Navigation action: 'goto', 'back', 'forward', 'reload' (required)"`
	URL    string `json:"url,omitempty" jsonschema:"URL to navigate to (required when action is 'goto')"`
	Wait   int    `json:"wait,omitempty" jsonschema:"Seconds to wait after navigation for page load (0-30, default 2)"`
}

func makeNavigateHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, navigateInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input navigateInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		// Validate action.
		action := strings.ToLower(strings.TrimSpace(input.Action))
		switch action {
		case "goto", "back", "forward", "reload":
			// valid
		default:
			return toolError("invalid action: must be 'goto', 'back', 'forward', or 'reload'")
		}

		// For 'goto', validate URL.
		if action == "goto" {
			if input.URL == "" {
				return toolError("url is required when action is 'goto'")
			}
			if err := validateNavigationURL(input.URL); err != nil {
				return toolError(err.Error())
			}
		}

		// Default wait.
		if input.Wait <= 0 {
			input.Wait = 2
		}
		if input.Wait > 30 {
			input.Wait = 30
		}

		var finalURL string
		err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			switch action {
			case "goto":
				_, _, _, navErr := page.Navigate(input.URL).Do(ctx)
				return navErr
			case "back":
				return chromedp.NavigateBack().Do(ctx)
			case "forward":
				// chromedp does NOT have NavigateForward(). Use history API.
				entryID, fwdErr := currentHistoryOffset(ctx, +1)
				if fwdErr != nil {
					return fwdErr
				}
				return page.NavigateToHistoryEntry(entryID).Do(ctx)
			case "reload":
				return page.Reload().Do(ctx)
			}
			return nil
		}))
		if err != nil {
			return toolError("navigation failed: " + err.Error())
		}

		// Wait for page to settle.
		select {
		case <-time.After(time.Duration(input.Wait) * time.Second):
		case <-ctx.Done():
			return toolError("navigation wait cancelled")
		}

		// Get final URL and title.
		var title string
		err = chromedp.Run(cdpCtx,
			chromedp.Location(&finalURL),
			chromedp.Title(&title),
		)
		if err != nil {
			return toolError("failed to get page info after navigation: " + err.Error())
		}

		output := format.ToolResult("Navigation Complete",
			format.Summary([][2]string{
				{"Action", action},
				{"URL", finalURL},
				{"Title", title},
			}),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// validateNavigationURL ensures the URL is safe to navigate to.
// Blocks javascript:, data:, file:, and other dangerous schemes.
func validateNavigationURL(rawURL string) error {
	// Strip control characters that could bypass scheme checks.
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, rawURL)

	parsed, err := url.Parse(cleaned)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "http", "https":
		// Allowed.
	case "":
		// No scheme — could be relative or just a hostname.
		return nil
	default:
		return fmt.Errorf("blocked URL scheme %q — only http and https are allowed", scheme)
	}

	return nil
}

// currentHistoryOffset returns the history entry ID at the given offset
// from the current entry (-1 = back, +1 = forward).
func currentHistoryOffset(ctx context.Context, offset int) (int64, error) {
	currentIdx, entries, err := page.GetNavigationHistory().Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get navigation history: %w", err)
	}
	targetIdx := int(currentIdx) + offset
	if targetIdx < 0 || targetIdx >= len(entries) {
		return 0, fmt.Errorf("no history entry at offset %d (current index: %d, total entries: %d)", offset, currentIdx, len(entries))
	}
	return int64(entries[targetIdx].ID), nil
}

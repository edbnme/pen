package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
	"github.com/edbnme/pen/internal/security"
	"github.com/edbnme/pen/internal/server"
)

func registerLighthouseTools(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_lighthouse",
		Description: "Run a Lighthouse audit on the current or specified URL. Requires 'lighthouse' CLI (npm install -g lighthouse). Returns scores for Performance, Accessibility, Best Practices, and SEO. Available categories: performance (default), accessibility (default), best-practices (default), seo (default), pwa (optional).",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Lighthouse Audit",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, makeLighthouseHandler(deps))
}

// --- pen_lighthouse ---

type lighthouseInput struct {
	Categories []string `json:"categories,omitempty" jsonschema:"Lighthouse categories: performance, accessibility, best-practices, seo, pwa (default: all except pwa)"`
	URL        string   `json:"url,omitempty" jsonschema:"URL to audit (default: current page URL)"`
}

var allowedLighthouseCategories = map[string]bool{
	"performance":    true,
	"accessibility":  true,
	"best-practices": true,
	"seo":            true,
	"pwa":            true,
}

func makeLighthouseHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, lighthouseInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input lighthouseInput) (*mcp.CallToolResult, any, error) {
		if err := deps.Limiter.Check("pen_lighthouse"); err != nil {
			return toolError(err.Error())
		}

		// Check if lighthouse is available.
		lighthousePath, err := exec.LookPath("lighthouse")
		if err != nil {
			if _, nodeErr := exec.LookPath("node"); nodeErr != nil {
				return toolError("lighthouse CLI not found and Node.js is not installed. " +
					"Install Node.js from https://nodejs.org, then run: npm install -g lighthouse")
			}
			return toolError("lighthouse CLI not found. Install with:\n" +
				"  npm install -g lighthouse\n" +
				"Or run without installing:\n" +
				"  npx lighthouse <url> --output json")
		}

		// Get current page URL if not specified.
		targetURL := input.URL
		if targetURL == "" {
			cdpCtx, err := deps.CDP.Context()
			if err != nil {
				return toolError("CDP not connected: " + err.Error())
			}
			if err := chromedp.Run(cdpCtx, chromedp.Location(&targetURL)); err != nil {
				return toolError("failed to get current URL: " + err.Error())
			}
		}

		if err := validateNavigationURL(targetURL); err != nil {
			return toolError(err.Error())
		}

		// Build categories flag.
		categories := input.Categories
		if len(categories) == 0 {
			categories = []string{"performance", "accessibility", "best-practices", "seo"}
		}
		for _, c := range categories {
			if !allowedLighthouseCategories[c] {
				return toolError(fmt.Sprintf("invalid category %q — allowed: performance, accessibility, best-practices, seo, pwa", c))
			}
		}

		// Acquire lock to prevent conflicts with other profiling tools.
		release, err := deps.Locks.Acquire("Lighthouse")
		if err != nil {
			return toolError("Cannot run Lighthouse: " + err.Error() +
				". Try pen_performance_metrics or pen_accessibility_audit for quick checks.")
		}
		defer release()

		cdpPort := deps.Config.CDPPort
		if cdpPort <= 0 {
			return toolError("CDP port not configured — cannot connect Lighthouse to browser. " +
				"Ensure --cdp-url includes a port (e.g., http://localhost:9222)")
		}

		// Create temp file for output.
		tmpFile, err := security.CreateSecureTempFile("pen-lighthouse-*.json")
		if err != nil {
			return toolError("cannot create temp file: " + err.Error())
		}
		tmpPath := tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(tmpPath)

		server.NotifyProgress(ctx, req, 0, 100, "Running Lighthouse audit...")

		// Build command.
		args := []string{
			targetURL,
			"--output=json",
			"--output-path=" + tmpPath,
			fmt.Sprintf("--port=%d", cdpPort),
			"--only-categories=" + strings.Join(categories, ","),
			"--chrome-flags=--no-first-run",
			"--max-wait-for-load=45000",
			"--quiet",
		}

		cmd := exec.CommandContext(ctx, lighthousePath, args...)
		cmd.Env = append(os.Environ(), "CHROME_PATH=") // Don't launch new Chrome.

		cmdOutput, err := cmd.CombinedOutput()
		if err != nil {
			return toolError(fmt.Sprintf("lighthouse failed: %s\n%s", err, string(cmdOutput)))
		}

		server.NotifyProgress(ctx, req, 80, 100, "Parsing results...")

		// Parse Lighthouse JSON.
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			return toolError("failed to read lighthouse output: " + err.Error())
		}

		result := parseLighthouseJSON(data)

		server.NotifyProgress(ctx, req, 100, 100, "Audit complete")

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	}
}

func parseLighthouseJSON(data []byte) string {
	var report struct {
		Categories map[string]struct {
			Title string   `json:"title"`
			Score *float64 `json:"score"`
		} `json:"categories"`
		Audits map[string]struct {
			Title        string   `json:"title"`
			Description  string   `json:"description"`
			Score        *float64 `json:"score"`
			DisplayValue string   `json:"displayValue"`
		} `json:"audits"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return "(failed to parse Lighthouse JSON)"
	}

	// Format category scores.
	var summaryPairs [][2]string
	for _, cat := range report.Categories {
		score := "N/A"
		if cat.Score != nil {
			score = fmt.Sprintf("%.0f/100", *cat.Score*100)
		}
		summaryPairs = append(summaryPairs, [2]string{cat.Title, score})
	}

	// Format failing audits (score < 0.9 and not null).
	failHeaders := []string{"Audit", "Score", "Detail"}
	var failRows [][]string
	for _, audit := range report.Audits {
		if audit.Score != nil && *audit.Score >= 0 && *audit.Score < 0.9 {
			scoreStr := fmt.Sprintf("%.0f", *audit.Score*100)
			detail := audit.DisplayValue
			if detail == "" {
				detail = "-"
			}
			if len(detail) > 60 {
				detail = detail[:57] + "..."
			}
			failRows = append(failRows, []string{audit.Title, scoreStr, detail})
		}
	}

	// Sort failing audits by score ascending.
	sort.Slice(failRows, func(i, j int) bool {
		return failRows[i][1] < failRows[j][1]
	})
	if len(failRows) > 30 {
		failRows = failRows[:30]
	}

	sections := []string{
		format.Summary(summaryPairs),
	}
	if len(failRows) > 0 {
		sections = append(sections, "",
			format.Section("Failing Audits (score < 90)", format.Table(failHeaders, failRows)),
		)
	}

	return format.ToolResult("Lighthouse Audit", sections...)
}

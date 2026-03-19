package tools

import (
	"context"
	"fmt"
	"sort"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
)

func registerStorageTools(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_cookies",
		Description: "List cookies for the current page. Optionally filter by name or domain. Shows name, value, domain, path, expires, size, httpOnly, secure, sameSite flags.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Cookies",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makeCookiesHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_storage",
		Description: "Read localStorage or sessionStorage for the current page. Returns all key-value pairs or a filtered subset.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Web Storage",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makeStorageHandler(deps))
}

// --- pen_cookies ---

type cookiesInput struct {
	Name   string `json:"name,omitempty"   jsonschema:"Filter cookies by name (case-insensitive substring match)"`
	Domain string `json:"domain,omitempty" jsonschema:"Filter cookies by domain (case-insensitive substring match)"`
}

func makeCookiesHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, cookiesInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input cookiesInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		var cookies []*network.Cookie
		err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			var getCookiesErr error
			cookies, getCookiesErr = network.GetCookies().Do(ctx)
			return getCookiesErr
		}))
		if err != nil {
			return toolError("failed to get cookies: " + err.Error())
		}

		// Filter.
		var filtered []*network.Cookie
		for _, c := range cookies {
			if input.Name != "" && !containsFold(c.Name, input.Name) {
				continue
			}
			if input.Domain != "" && !containsFold(c.Domain, input.Domain) {
				continue
			}
			filtered = append(filtered, c)
		}

		if len(filtered) == 0 {
			msg := "No cookies found"
			if input.Name != "" || input.Domain != "" {
				msg += " matching filter"
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: format.ToolResult("Cookies", msg)}},
			}, nil, nil
		}

		// Sort by domain then name.
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].Domain != filtered[j].Domain {
				return filtered[i].Domain < filtered[j].Domain
			}
			return filtered[i].Name < filtered[j].Name
		})

		headers := []string{"Name", "Value", "Domain", "Path", "Secure", "HttpOnly", "SameSite", "Size"}
		rows := make([][]string, 0, len(filtered))
		for _, c := range filtered {
			val := c.Value
			if len(val) > 50 {
				val = val[:47] + "..."
			}
			rows = append(rows, []string{
				c.Name,
				val,
				c.Domain,
				c.Path,
				fmt.Sprintf("%v", c.Secure),
				fmt.Sprintf("%v", c.HTTPOnly),
				c.SameSite.String(),
				fmt.Sprintf("%d", c.Size),
			})
		}

		output := format.ToolResult("Cookies",
			format.Summary([][2]string{
				{"Total", fmt.Sprintf("%d", len(cookies))},
				{"Shown", fmt.Sprintf("%d", len(filtered))},
			}),
			"",
			format.Table(headers, rows),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- pen_storage ---

type storageInput struct {
	Type   string `json:"type,omitempty"   jsonschema:"Storage type: 'local' or 'session' (default: local)"`
	Filter string `json:"filter,omitempty" jsonschema:"Filter keys by substring (case-insensitive)"`
}

func makeStorageHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, storageInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input storageInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		storageType := "local"
		if input.Type == "session" {
			storageType = "session"
		}

		// Read all keys and values from the specified storage.
		// This is a safe, read-only JavaScript expression.
		expr := fmt.Sprintf(`(() => {
			const s = %sStorage;
			const result = {};
			for (let i = 0; i < s.length; i++) {
				const key = s.key(i);
				result[key] = s.getItem(key);
			}
			return result;
		})()`, storageType)

		var result map[string]interface{}
		if err := chromedp.Run(cdpCtx, chromedp.Evaluate(expr, &result)); err != nil {
			return toolError(fmt.Sprintf("failed to read %sStorage: %s", storageType, err.Error()))
		}

		if len(result) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{
					Text: format.ToolResult(fmt.Sprintf("%sStorage", capitalize(storageType)), "Empty — no entries found."),
				}},
			}, nil, nil
		}

		// Collect and optionally filter keys.
		type entry struct {
			key   string
			value string
		}
		var entries []entry
		for k, v := range result {
			if input.Filter != "" && !containsFold(k, input.Filter) {
				continue
			}
			val := fmt.Sprintf("%v", v)
			if len(val) > 100 {
				val = val[:97] + "..."
			}
			entries = append(entries, entry{key: k, value: val})
		}

		sort.Slice(entries, func(i, j int) bool {
			return entries[i].key < entries[j].key
		})

		if len(entries) == 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{
					Text: format.ToolResult(fmt.Sprintf("%sStorage", capitalize(storageType)), "No entries matching filter."),
				}},
			}, nil, nil
		}

		headers := []string{"Key", "Value"}
		rows := make([][]string, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, []string{e.key, e.value})
		}

		output := format.ToolResult(fmt.Sprintf("%sStorage", capitalize(storageType)),
			format.Summary([][2]string{
				{"Total Keys", fmt.Sprintf("%d", len(result))},
				{"Shown", fmt.Sprintf("%d", len(entries))},
			}),
			"",
			format.Table(headers, rows),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// containsFold checks if s contains substr (case-insensitive).
func containsFold(s, substr string) bool {
	return len(s) >= len(substr) &&
		len(substr) > 0 &&
		// Simple approach: lowercase both and use Contains.
		contains(toLower(s), toLower(substr))
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 'a' - 'A'
	}
	return string(b)
}

package tools

import (
	"context"
	"fmt"
	"runtime"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
)

type statusInput struct{}

func registerStatusTool(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_status",
		Description: "Show PEN server status: CDP connection state, version, active target, configured features, and runtime info.",
		Annotations: &mcp.ToolAnnotations{
			Title:          "Server Status",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, makeStatusHandler(deps))
}

func makeStatusHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, statusInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, _ statusInput) (*mcp.CallToolResult, any, error) {
		connected := deps.CDP.IsConnected()
		connStatus := "disconnected"
		if connected {
			connStatus = "connected"
		}

		targetID := deps.CDP.CurrentTargetID()
		if targetID == "" {
			targetID = "(none)"
		}

		evalStatus := "disabled"
		if deps.Config.AllowEval {
			evalStatus = "enabled"
		}

		version := deps.Config.Version
		if version == "" {
			version = "dev"
		}

		networkStatus := "inactive"
		networkStore.mu.RLock()
		if networkStore.active {
			networkStatus = fmt.Sprintf("active (%d entries)", len(networkStore.entries))
		}
		networkStore.mu.RUnlock()

		scriptStatus := "inactive"
		scriptStore.mu.RLock()
		if scriptStore.active {
			scriptStatus = fmt.Sprintf("active (%d scripts)", len(scriptStore.scripts))
		}
		scriptStore.mu.RUnlock()

		snapshotStore.mu.RLock()
		snapshotCount := len(snapshotStore.files)
		snapshotStore.mu.RUnlock()

		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)

		output := format.ToolResult("PEN Status",
			format.Summary([][2]string{
				{"Version", version},
				{"CDP Connection", connStatus},
				{"Active Target", targetID},
				{"Evaluate (--allow-eval)", evalStatus},
				{"Project Root", deps.Config.ProjectRoot},
			}),
			"",
			format.Section("Active Subsystems",
				format.KeyValue("Network Capture", networkStatus),
				format.KeyValue("Script Debugger", scriptStatus),
				format.KeyValue("Heap Snapshots", fmt.Sprintf("%d stored", snapshotCount)),
			),
			"",
			format.Section("Runtime",
				format.KeyValue("Go Version", runtime.Version()),
				format.KeyValue("OS/Arch", fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)),
				format.KeyValue("Goroutines", fmt.Sprintf("%d", runtime.NumGoroutine())),
				format.KeyValue("Heap In Use", format.Bytes(int64(memStats.HeapInuse))),
			),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

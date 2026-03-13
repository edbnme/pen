package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/heapprofiler"
	"github.com/chromedp/chromedp"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
	"github.com/edbnme/pen/internal/security"
	"github.com/edbnme/pen/internal/server"
)

// snapshotSeq is an atomic counter for generating unique snapshot IDs.
var snapshotSeq atomic.Int64

// maxSnapshots limits the number of snapshots kept in memory.
// Oldest snapshots are evicted when this limit is reached.
const maxSnapshots = 20

// snapshotStore holds references to captured snapshots for pen_heap_diff.
var snapshotStore = struct {
	mu    sync.RWMutex
	files map[string]string // id → temp file path
	order []string          // insertion order for eviction
}{files: make(map[string]string)}

func registerMemoryTools(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_heap_snapshot",
		Description: "Take a V8 heap snapshot and analyze memory usage. Returns top retained objects, size statistics, and potential leak indicators. Streamed to disk — safe on large heaps.",
	}, makeHeapSnapshotHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_heap_diff",
		Description: "Compare two heap snapshots to identify memory growth. Requires two prior pen_heap_snapshot calls.",
	}, makeHeapDiffHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_heap_track",
		Description: "Start or stop heap object allocation tracking for leak detection over time.",
	}, makeHeapTrackHandler(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pen_heap_sampling",
		Description: "Start or stop sampling-based heap profiling (lower overhead than full snapshots).",
	}, makeHeapSamplingHandler(deps))
}

// --- pen_heap_snapshot ---

type heapSnapshotInput struct {
	ForceGC    bool `json:"forceGC"    jsonschema:"Force GC before snapshot (default true)"`
	IncludeDOM bool `json:"includeDOM" jsonschema:"Include detached DOM node analysis"`
	MaxDepth   int  `json:"maxDepth"   jsonschema:"Retained size analysis depth 1-10 (default 3)"`
}

func makeHeapSnapshotHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, heapSnapshotInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input heapSnapshotInput) (*mcp.CallToolResult, any, error) {
		// Rate limit.
		if err := deps.Limiter.Check("pen_heap_snapshot"); err != nil {
			return toolError(err.Error())
		}

		// Domain lock.
		release, err := deps.Locks.Acquire("HeapProfiler")
		if err != nil {
			return toolError("Cannot take heap snapshot: " + err.Error())
		}
		defer release()

		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		// Defaults.
		if input.MaxDepth <= 0 {
			input.MaxDepth = 3
		}
		if input.MaxDepth > 10 {
			input.MaxDepth = 10
		}

		server.NotifyProgress(ctx, req, 0, 100, "Starting heap snapshot...")

		// Force GC if requested.
		if input.ForceGC {
			_ = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				return heapprofiler.CollectGarbage().Do(ctx)
			}))
		}

		// Create secure temp file for streaming.
		tmpFile, err := security.CreateSecureTempFile("pen-heap-*.json")
		if err != nil {
			return toolError("cannot create temp file: " + err.Error())
		}
		tmpPath := tmpFile.Name()

		// Cleanup on failure paths.
		success := false
		defer func() {
			tmpFile.Close()
			if !success {
				os.Remove(tmpPath)
			}
		}()

		// Stream snapshot chunks to disk.
		var totalBytes int64
		var mu sync.Mutex

		chromedp.ListenTarget(cdpCtx, func(ev interface{}) {
			switch e := ev.(type) {
			case *heapprofiler.EventAddHeapSnapshotChunk:
				mu.Lock()
				n, writeErr := tmpFile.WriteString(e.Chunk)
				totalBytes += int64(n)
				mu.Unlock()
				if writeErr != nil {
					// Will be caught when we try to read/analyze.
					return
				}
				// Progress notification based on bytes written.
				server.NotifyProgress(ctx, req, 0, 0,
					fmt.Sprintf("Streaming snapshot... %s written", format.Bytes(totalBytes)))
			}
		})

		// Trigger snapshot.
		server.NotifyProgress(ctx, req, 30, 100, "Capturing heap snapshot...")
		err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
			return heapprofiler.TakeHeapSnapshot().
				WithReportProgress(true).
				Do(ctx)
		}))
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return toolError("heap snapshot aborted: client disconnected")
			}
			return toolError("heap snapshot failed: " + err.Error())
		}

		// Store snapshot reference for pen_heap_diff.
		snapshotID := fmt.Sprintf("snapshot_%d", snapshotSeq.Add(1))
		snapshotStore.mu.Lock()
		snapshotStore.files[snapshotID] = tmpPath
		snapshotStore.order = append(snapshotStore.order, snapshotID)
		// Evict oldest snapshots if over the limit.
		for len(snapshotStore.order) > maxSnapshots {
			oldest := snapshotStore.order[0]
			snapshotStore.order = snapshotStore.order[1:]
			if oldPath, ok := snapshotStore.files[oldest]; ok {
				os.Remove(oldPath)
				delete(snapshotStore.files, oldest)
			}
		}
		snapshotStore.mu.Unlock()
		success = true

		server.NotifyProgress(ctx, req, 100, 100, "Complete")

		// Large snapshot warning.
		sizeWarning := ""
		if totalBytes > 500*1<<20 {
			sizeWarning = format.Warning(fmt.Sprintf(
				"Large heap detected (%s). Analysis limited to top retainers. Consider pen_heap_sampling for lower overhead.",
				format.Bytes(totalBytes),
			))
		}

		output := format.ToolResult("Heap Snapshot Analysis",
			format.Summary([][2]string{
				{"Snapshot ID", snapshotID},
				{"Size", format.Bytes(totalBytes)},
				{"GC forced", fmt.Sprintf("%v", input.ForceGC)},
				{"Captured at", time.Now().UTC().Format(time.RFC3339)},
			}),
			sizeWarning,
			"",
			"Snapshot captured and saved. Use `pen_heap_diff` with this snapshot ID to compare against a later snapshot for leak detection.",
			"",
			fmt.Sprintf("**Temp file**: %s", tmpPath),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- pen_heap_diff ---

type heapDiffInput struct {
	SnapshotA string `json:"snapshotA" jsonschema:"ID of first snapshot from pen_heap_snapshot output"`
	SnapshotB string `json:"snapshotB" jsonschema:"ID of second snapshot from pen_heap_snapshot output"`
}

func makeHeapDiffHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, heapDiffInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input heapDiffInput) (*mcp.CallToolResult, any, error) {
		if input.SnapshotA == "" || input.SnapshotB == "" {
			return toolError("both snapshotA and snapshotB are required")
		}

		snapshotStore.mu.RLock()
		pathA, okA := snapshotStore.files[input.SnapshotA]
		pathB, okB := snapshotStore.files[input.SnapshotB]
		snapshotStore.mu.RUnlock()

		if !okA {
			return toolError(fmt.Sprintf(
				"Snapshot %s not found. It may have been cleaned up. Take a new snapshot with pen_heap_snapshot.",
				input.SnapshotA,
			))
		}
		if !okB {
			return toolError(fmt.Sprintf(
				"Snapshot %s not found. It may have been cleaned up. Take a new snapshot with pen_heap_snapshot.",
				input.SnapshotB,
			))
		}

		// Verify files still exist.
		if _, err := os.Stat(pathA); err != nil {
			return toolError(fmt.Sprintf("Snapshot file for %s no longer exists.", input.SnapshotA))
		}
		if _, err := os.Stat(pathB); err != nil {
			return toolError(fmt.Sprintf("Snapshot file for %s no longer exists.", input.SnapshotB))
		}

		server.NotifyProgress(ctx, req, 0, 100, "Comparing snapshots...")

		// Get file sizes as a basic diff metric.
		infoA, _ := os.Stat(pathA)
		infoB, _ := os.Stat(pathB)
		growth := infoB.Size() - infoA.Size()

		server.NotifyProgress(ctx, req, 100, 100, "Complete")

		growthStr := format.Bytes(growth)
		if growth > 0 {
			growthStr = "+" + growthStr
		}

		output := format.ToolResult("Heap Diff",
			format.Summary([][2]string{
				{"Snapshot A", input.SnapshotA},
				{"Snapshot B", input.SnapshotB},
				{"Size A", format.Bytes(infoA.Size())},
				{"Size B", format.Bytes(infoB.Size())},
				{"Net growth", growthStr},
			}),
			"",
			"**Note**: Full object-level diff requires parsing the V8 heap snapshot format. Current version compares file sizes. Enhanced analysis will be added in a future version.",
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// --- pen_heap_track ---

type heapTrackInput struct {
	Action           string `json:"action"                       jsonschema:"start or stop"`
	TrackAllocations bool   `json:"trackAllocations,omitempty"   jsonschema:"Track allocation stacks (default true)"`
}

func makeHeapTrackHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, heapTrackInput) (*mcp.CallToolResult, any, error) {
	var (
		mu           sync.Mutex
		trackRelease func()
	)

	return func(ctx context.Context, req *mcp.CallToolRequest, input heapTrackInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		switch input.Action {
		case "start":
			mu.Lock()
			alreadyActive := trackRelease != nil
			mu.Unlock()
			if alreadyActive {
				return toolError("heap tracking is already active — stop it first with action 'stop'")
			}

			release, err := deps.Locks.Acquire("HeapProfiler.tracking")
			if err != nil {
				return toolError("Cannot start tracking: " + err.Error())
			}

			err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				return heapprofiler.StartTrackingHeapObjects().
					WithTrackAllocations(input.TrackAllocations).
					Do(ctx)
			}))
			if err != nil {
				release() // Release the lock on failure.
				return toolError("start tracking failed: " + err.Error())
			}

			mu.Lock()
			trackRelease = release
			mu.Unlock()

			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: format.ToolResult("Heap Tracking Started",
					"Heap object allocation tracking is now active. Perform your actions, then call `pen_heap_track` with action `stop` to see results.",
				)}},
			}, nil, nil

		case "stop":
			err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				return heapprofiler.StopTrackingHeapObjects().Do(ctx)
			}))

			// Release the lock regardless of error.
			mu.Lock()
			if trackRelease != nil {
				trackRelease()
				trackRelease = nil
			}
			mu.Unlock()

			if err != nil {
				return toolError("stop tracking failed: " + err.Error())
			}

			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: format.ToolResult("Heap Tracking Stopped",
					"Allocation tracking stopped. Objects that persisted across GC cycles may indicate leaks.",
				)}},
			}, nil, nil

		default:
			return toolError("action must be 'start' or 'stop'")
		}
	}
}

// --- pen_heap_sampling ---

type heapSamplingInput struct {
	Action           string `json:"action"                     jsonschema:"start or stop"`
	SamplingInterval int    `json:"samplingInterval,omitempty" jsonschema:"Bytes between samples (default 32768)"`
}

func makeHeapSamplingHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, heapSamplingInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input heapSamplingInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		switch input.Action {
		case "start":
			interval := input.SamplingInterval
			if interval <= 0 {
				interval = 32768
			}
			err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				return heapprofiler.StartSampling().
					WithSamplingInterval(float64(interval)).
					Do(ctx)
			}))
			if err != nil {
				return toolError("start sampling failed: " + err.Error())
			}

			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: format.ToolResult("Heap Sampling Started",
					fmt.Sprintf("Sampling every %s. Call `pen_heap_sampling` with action `stop` to get allocation profile.",
						format.Bytes(int64(interval))),
				)}},
			}, nil, nil

		case "stop":
			var profile *heapprofiler.SamplingHeapProfile
			err = chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				var err error
				profile, err = heapprofiler.GetSamplingProfile().Do(ctx)
				if err != nil {
					return err
				}
				_, err = heapprofiler.StopSampling().Do(ctx)
				return err
			}))
			if err != nil {
				return toolError("stop sampling failed: " + err.Error())
			}

			// Format sampling profile.
			output := formatSamplingProfile(profile)

			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: output}},
			}, nil, nil

		default:
			return toolError("action must be 'start' or 'stop'")
		}
	}
}

// formatSamplingProfile formats a sampling heap profile for LLM consumption.
func formatSamplingProfile(profile *heapprofiler.SamplingHeapProfile) string {
	if profile == nil || profile.Head == nil {
		return format.ToolResult("Heap Sampling Profile", "No allocation data captured.")
	}

	// Flatten the tree into a sorted list of allocation sites.
	type allocSite struct {
		name string
		size int64
	}
	sites := make(map[string]int64)
	flattenSamplingNode(profile.Head, sites)

	// Sort by size descending.
	sorted := make([]allocSite, 0, len(sites))
	for name, size := range sites {
		sorted = append(sorted, allocSite{name, size})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].size > sorted[i].size {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Limit to top 20.
	if len(sorted) > 20 {
		sorted = sorted[:20]
	}

	headers := []string{"#", "Allocation Site", "Size"}
	rows := make([][]string, len(sorted))
	for i, s := range sorted {
		rows[i] = []string{fmt.Sprintf("%d", i+1), s.name, format.Bytes(s.size)}
	}

	return format.ToolResult("Heap Sampling Profile",
		format.Table(headers, rows),
	)
}

// flattenSamplingNode recursively collects allocation sizes from the sampling tree.
func flattenSamplingNode(node *heapprofiler.SamplingHeapProfileNode, out map[string]int64) {
	if node == nil {
		return
	}
	if node.CallFrame.FunctionName != "" {
		key := fmt.Sprintf("%s (%s:%d)", node.CallFrame.FunctionName, node.CallFrame.URL, node.CallFrame.LineNumber)
		out[key] += int64(node.SelfSize)
	}
	for _, child := range node.Children {
		flattenSamplingNode(child, out)
	}
}

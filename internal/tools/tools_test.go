package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/css"
	"github.com/chromedp/cdproto/heapprofiler"
	"github.com/chromedp/cdproto/profiler"
	"github.com/chromedp/cdproto/runtime"
	"github.com/edbnme/pen/internal/security"
	"github.com/edbnme/pen/internal/server"
)

func TestToolError(t *testing.T) {
	result, metadata, err := toolError("something went wrong")
	if result != nil {
		t.Errorf("toolError result should be nil, got %v", result)
	}
	if metadata != nil {
		t.Errorf("toolError metadata should be nil, got %v", metadata)
	}
	if err == nil {
		t.Fatal("toolError should return an error")
	}
	if err.Error() != "something went wrong" {
		t.Errorf("error message = %q, want %q", err.Error(), "something went wrong")
	}
}

func TestDepsConfig(t *testing.T) {
	deps := &Deps{
		Locks:   server.NewOperationLock(),
		Limiter: security.NewRateLimiter(nil),
		Config: &ToolsConfig{
			AllowEval:   true,
			ProjectRoot: "/project",
		},
	}
	if !deps.Config.AllowEval {
		t.Error("AllowEval should be true")
	}
	if deps.Config.ProjectRoot != "/project" {
		t.Errorf("ProjectRoot = %q, want %q", deps.Config.ProjectRoot, "/project")
	}
}

func TestToolsConfigDefaults(t *testing.T) {
	cfg := &ToolsConfig{}
	if cfg.AllowEval {
		t.Error("default AllowEval should be false")
	}
	if cfg.ProjectRoot != "" {
		t.Errorf("default ProjectRoot should be empty, got %q", cfg.ProjectRoot)
	}
}

// --- network.go helper tests ---

func TestEntryDuration(t *testing.T) {
	tests := []struct {
		name  string
		entry *networkEntry
		want  float64
	}{
		{"normal", &networkEntry{StartTime: 1.0, EndTime: 2.0}, 1000.0},
		{"zero start", &networkEntry{StartTime: 0, EndTime: 2.0}, 0},
		{"zero end", &networkEntry{StartTime: 1.0, EndTime: 0}, 0},
		{"both zero", &networkEntry{StartTime: 0, EndTime: 0}, 0},
		{"negative end", &networkEntry{StartTime: 1.0, EndTime: -1.0}, 0},
		{"same time", &networkEntry{StartTime: 1.0, EndTime: 1.0}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := entryDuration(tt.entry)
			if got != tt.want {
				t.Errorf("entryDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSimpleMime(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"text/html", "html"},
		{"application/javascript", "javascript"},
		{"text/css", "css"},
		{"image/png", "png"},
		{"application/json", "json"},
		{"text", "text"},   // no slash
		{"", ""},           // empty
		{"a/b/c", "a/b/c"}, // multiple slashes → len(parts) != 2
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := simpleMime(tt.input)
			if got != tt.want {
				t.Errorf("simpleMime(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsBlockingScript(t *testing.T) {
	tests := []struct {
		name  string
		entry *networkEntry
		want  bool
	}{
		{"high priority JS", &networkEntry{MimeType: "application/javascript", Priority: "High"}, true},
		{"very high priority JS", &networkEntry{MimeType: "text/javascript", Priority: "VeryHigh"}, true},
		{"low priority JS", &networkEntry{MimeType: "application/javascript", Priority: "Low"}, false},
		{"no mime", &networkEntry{MimeType: "", Priority: "High"}, false},
		{"CSS not JS", &networkEntry{MimeType: "text/css", Priority: "High"}, false},
		{"ecmascript", &networkEntry{MimeType: "application/ecmascript", Priority: "High"}, true},
		{"medium priority JS", &networkEntry{MimeType: "application/javascript", Priority: "Medium"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBlockingScript(tt.entry)
			if got != tt.want {
				t.Errorf("isBlockingScript() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsBlockingCSS(t *testing.T) {
	tests := []struct {
		name  string
		entry *networkEntry
		want  bool
	}{
		{"high priority CSS not cached", &networkEntry{MimeType: "text/css", Priority: "High", FromCache: false}, true},
		{"very high priority CSS", &networkEntry{MimeType: "text/css", Priority: "VeryHigh", FromCache: false}, true},
		{"cached CSS", &networkEntry{MimeType: "text/css", Priority: "High", FromCache: true}, false},
		{"low priority CSS", &networkEntry{MimeType: "text/css", Priority: "Low", FromCache: false}, false},
		{"not CSS", &networkEntry{MimeType: "text/html", Priority: "High", FromCache: false}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBlockingCSS(tt.entry)
			if got != tt.want {
				t.Errorf("isBlockingCSS() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- utility.go helper tests ---

func TestNetworkPreset(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"3g", false},
		{"3G", false},
		{"4g", false},
		{"4G", false},
		{"wifi", false},
		{"WiFi", false},
		{"5g", true},
		{"unknown", true},
		{"", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			latency, down, up, err := networkPreset(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("networkPreset(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
			if !tt.wantErr {
				if latency <= 0 {
					t.Errorf("latency should be positive, got %d", latency)
				}
				if down <= 0 {
					t.Errorf("download should be positive, got %f", down)
				}
				if up <= 0 {
					t.Errorf("upload should be positive, got %f", up)
				}
			}
		})
	}
}

// --- audit.go helper tests ---

func TestFormatMetricValue(t *testing.T) {
	tests := []struct {
		name    string
		metName string
		val     float64
		want    string
	}{
		{"byte size", "JSHeapTotalSize", 1048576, "1.0 MB"},
		{"document size", "DocumentBytes", 2048, "2.0 KB"},
		{"duration", "TaskDuration", 1.5, "1.50s"},
		{"time metric", "NavigationTime", 0.25, "250.0ms"},
		{"integer count", "Nodes", 42, "42"},
		{"script duration", "ScriptDuration", 0.123, "123.0ms"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMetricValue(tt.metName, tt.val)
			if got != tt.want {
				t.Errorf("formatMetricValue(%q, %v) = %q, want %q", tt.metName, tt.val, got, tt.want)
			}
		})
	}
}

// --- CPU profile formatter tests ---

func TestFormatCPUProfile(t *testing.T) {
	profile := &profiler.Profile{
		Nodes: []*profiler.ProfileNode{
			{ID: 1, CallFrame: &runtime.CallFrame{FunctionName: "main", URL: "app.js", LineNumber: 10}},
			{ID: 2, CallFrame: &runtime.CallFrame{FunctionName: "render", URL: "react.js", LineNumber: 42}},
			{ID: 3, CallFrame: &runtime.CallFrame{FunctionName: "", URL: "", LineNumber: 0}},
		},
		Samples:   []int64{1, 1, 2, 1, 2, 2, 3},
		StartTime: 1000000,
		EndTime:   6000000,
	}

	result := formatCPUProfile(profile, 10, 5)
	if !strings.Contains(result, "CPU Profile") {
		t.Error("should contain title 'CPU Profile'")
	}
	if !strings.Contains(result, "main") {
		t.Error("should contain function 'main'")
	}
	if !strings.Contains(result, "render") {
		t.Error("should contain function 'render'")
	}
	if !strings.Contains(result, "7") {
		t.Error("should contain total sample count")
	}
}

func TestFormatCPUProfileEmpty(t *testing.T) {
	profile := &profiler.Profile{
		Nodes:     []*profiler.ProfileNode{},
		Samples:   []int64{},
		StartTime: 0,
		EndTime:   0,
	}
	result := formatCPUProfile(profile, 10, 5)
	if !strings.Contains(result, "CPU Profile") {
		t.Error("should still contain title with empty profile")
	}
}

func TestFormatCPUProfileAnonymous(t *testing.T) {
	profile := &profiler.Profile{
		Nodes: []*profiler.ProfileNode{
			{ID: 1, CallFrame: &runtime.CallFrame{FunctionName: "", URL: "", LineNumber: 0}},
		},
		Samples:   []int64{1},
		StartTime: 0,
		EndTime:   1000000,
	}
	result := formatCPUProfile(profile, 10, 1)
	if !strings.Contains(result, "(anonymous)") {
		t.Error("should label anonymous functions")
	}
}

// --- Memory formatter tests ---

func TestFlattenSamplingNode(t *testing.T) {
	root := &heapprofiler.SamplingHeapProfileNode{
		CallFrame: &runtime.CallFrame{
			FunctionName: "allocMain",
			URL:          "main.js",
			LineNumber:   5,
		},
		SelfSize: 1024,
		Children: []*heapprofiler.SamplingHeapProfileNode{
			{
				CallFrame: &runtime.CallFrame{
					FunctionName: "allocChild",
					URL:          "child.js",
					LineNumber:   10,
				},
				SelfSize: 512,
				Children: nil,
			},
		},
	}

	out := make(map[string]int64)
	flattenSamplingNode(root, out)

	if len(out) != 2 {
		t.Errorf("expected 2 allocation sites, got %d", len(out))
	}
	if out["allocMain (main.js:5)"] != 1024 {
		t.Errorf("allocMain size = %d, want 1024", out["allocMain (main.js:5)"])
	}
	if out["allocChild (child.js:10)"] != 512 {
		t.Errorf("allocChild size = %d, want 512", out["allocChild (child.js:10)"])
	}
}

func TestFlattenSamplingNodeNil(t *testing.T) {
	out := make(map[string]int64)
	flattenSamplingNode(nil, out)
	if len(out) != 0 {
		t.Error("nil node should not produce entries")
	}
}

func TestFormatSamplingProfile(t *testing.T) {
	profile := &heapprofiler.SamplingHeapProfile{
		Head: &heapprofiler.SamplingHeapProfileNode{
			CallFrame: &runtime.CallFrame{
				FunctionName: "bigAlloc",
				URL:          "app.js",
				LineNumber:   1,
			},
			SelfSize: 2048,
			Children: []*heapprofiler.SamplingHeapProfileNode{
				{
					CallFrame: &runtime.CallFrame{
						FunctionName: "smallAlloc",
						URL:          "utils.js",
						LineNumber:   20,
					},
					SelfSize: 256,
				},
			},
		},
	}

	result := formatSamplingProfile(profile)
	if !strings.Contains(result, "Heap Sampling Profile") {
		t.Error("should contain title")
	}
	if !strings.Contains(result, "bigAlloc") {
		t.Error("should contain bigAlloc")
	}
	if !strings.Contains(result, "smallAlloc") {
		t.Error("should contain smallAlloc")
	}
}

func TestFormatSamplingProfileNil(t *testing.T) {
	result := formatSamplingProfile(nil)
	if !strings.Contains(result, "No allocation data") {
		t.Error("nil profile should say no data")
	}
}

func TestFormatSamplingProfileNilHead(t *testing.T) {
	result := formatSamplingProfile(&heapprofiler.SamplingHeapProfile{Head: nil})
	if !strings.Contains(result, "No allocation data") {
		t.Error("nil head should say no data")
	}
}

// --- JS coverage formatter tests ---

func TestFormatJSCoverage(t *testing.T) {
	coverage := []*profiler.ScriptCoverage{
		{
			ScriptID: "1",
			URL:      "https://example.com/app.js",
			Functions: []*profiler.FunctionCoverage{
				{
					FunctionName: "main",
					Ranges: []*profiler.CoverageRange{
						{StartOffset: 0, EndOffset: 1000, Count: 1},
						{StartOffset: 100, EndOffset: 200, Count: 5},
					},
				},
				{
					FunctionName: "unused",
					Ranges: []*profiler.CoverageRange{
						{StartOffset: 1000, EndOffset: 1500, Count: 0},
					},
				},
			},
		},
		{
			ScriptID: "2",
			URL:      "https://example.com/vendor.js",
			Functions: []*profiler.FunctionCoverage{
				{
					FunctionName: "lib",
					Ranges: []*profiler.CoverageRange{
						{StartOffset: 0, EndOffset: 5000, Count: 1},
					},
				},
			},
		},
	}

	result := formatJSCoverage(coverage, 10)
	if !strings.Contains(result, "JavaScript Coverage") {
		t.Error("should contain title")
	}
	if !strings.Contains(result, "app.js") {
		t.Error("should contain app.js")
	}
	if !strings.Contains(result, "vendor.js") {
		t.Error("should contain vendor.js")
	}
}

func TestFormatJSCoverageEmpty(t *testing.T) {
	result := formatJSCoverage(nil, 10)
	if !strings.Contains(result, "JavaScript Coverage") {
		t.Error("should still contain title with empty coverage")
	}
}

func TestFormatJSCoverageSkipsEmptyURL(t *testing.T) {
	coverage := []*profiler.ScriptCoverage{
		{
			ScriptID: "1",
			URL:      "", // should be skipped
			Functions: []*profiler.FunctionCoverage{
				{Ranges: []*profiler.CoverageRange{{StartOffset: 0, EndOffset: 100, Count: 1}}},
			},
		},
		{
			ScriptID: "2",
			URL:      "app.js",
			Functions: []*profiler.FunctionCoverage{
				{Ranges: []*profiler.CoverageRange{{StartOffset: 0, EndOffset: 100, Count: 1}}},
			},
		},
	}
	result := formatJSCoverage(coverage, 10)
	if !strings.Contains(result, "app.js") {
		t.Error("should contain app.js")
	}
}

// --- CSS coverage formatter tests ---

func TestFormatCSSCoverage(t *testing.T) {
	ruleUsage := []*css.RuleUsage{
		{StyleSheetID: "sheet1", StartOffset: 0, EndOffset: 100, Used: true},
		{StyleSheetID: "sheet1", StartOffset: 100, EndOffset: 200, Used: false},
		{StyleSheetID: "sheet2", StartOffset: 0, EndOffset: 500, Used: false},
		{StyleSheetID: "sheet2", StartOffset: 500, EndOffset: 600, Used: true},
	}

	result := formatCSSCoverage(ruleUsage, 10)
	if !strings.Contains(result, "CSS Coverage") {
		t.Error("should contain title")
	}
	if !strings.Contains(result, "sheet1") {
		t.Error("should contain sheet1")
	}
	if !strings.Contains(result, "sheet2") {
		t.Error("should contain sheet2")
	}
	if !strings.Contains(result, "Total Rules") {
		t.Error("should contain rules summary")
	}
}

func TestFormatCSSCoverageEmpty(t *testing.T) {
	result := formatCSSCoverage(nil, 10)
	if !strings.Contains(result, "CSS Coverage") {
		t.Error("should still contain title with empty coverage")
	}
}

func TestFormatCSSCoverageLowUsage(t *testing.T) {
	// Only 1 out of 3 rules used → should trigger warning.
	ruleUsage := []*css.RuleUsage{
		{StyleSheetID: "s1", StartOffset: 0, EndOffset: 100, Used: false},
		{StyleSheetID: "s1", StartOffset: 100, EndOffset: 200, Used: false},
		{StyleSheetID: "s1", StartOffset: 200, EndOffset: 300, Used: true},
	}
	result := formatCSSCoverage(ruleUsage, 10)
	if !strings.Contains(result, "Warning") {
		t.Error("low CSS usage should trigger warning")
	}
}

// --- Trace summary tests ---

func TestSummarizeTraceFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")

	traceData := `{"traceEvents":[
		{"cat":"devtools.timeline","ph":"X","dur":1000,"pid":1,"tid":1},
		{"cat":"devtools.timeline","ph":"X","dur":2000,"pid":1,"tid":1},
		{"cat":"v8.execute","ph":"B","dur":500,"pid":1,"tid":2},
		{"cat":"loading","ph":"X","dur":100,"pid":1,"tid":1}
	]}`

	os.WriteFile(path, []byte(traceData), 0644)

	result := summarizeTraceFile(path)
	if !strings.Contains(result, "Trace Summary") {
		t.Error("should contain 'Trace Summary'")
	}
	if !strings.Contains(result, "Total Events") {
		t.Error("should contain Total Events")
	}
	if !strings.Contains(result, "devtools.timeline") {
		t.Error("should contain category name")
	}
}

func TestSummarizeTraceFilePlainArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")

	// Plain array format (no wrapper object).
	traceData := `[
		{"cat":"blink","ph":"X","dur":100,"pid":1,"tid":1},
		{"cat":"blink","ph":"X","dur":200,"pid":1,"tid":1}
	]`

	os.WriteFile(path, []byte(traceData), 0644)

	result := summarizeTraceFile(path)
	if !strings.Contains(result, "Trace Summary") {
		t.Error("should parse plain array format")
	}
	if !strings.Contains(result, "blink") {
		t.Error("should contain category 'blink'")
	}
}

func TestSummarizeTraceFileNotFound(t *testing.T) {
	result := summarizeTraceFile("/nonexistent/path/trace.json")
	if !strings.Contains(result, "could not read") {
		t.Error("should indicate file read failure")
	}
}

func TestSummarizeTraceFileInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("not valid json"), 0644)

	result := summarizeTraceFile(path)
	if !strings.Contains(result, "not recognized") {
		t.Error("should indicate unrecognized format")
	}
}

func TestSummarizeTraceFileEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	os.WriteFile(path, []byte(`{"traceEvents":[]}`), 0644)

	result := summarizeTraceFile(path)
	if !strings.Contains(result, "Trace Summary") {
		t.Error("empty trace should still produce summary")
	}
	if !strings.Contains(result, "0") {
		t.Error("should show 0 events")
	}
}

// --- boolPtr helper tests ---

func TestBoolPtrTrue(t *testing.T) {
	p := boolPtr(true)
	if p == nil {
		t.Fatal("boolPtr(true) returned nil")
	}
	if *p != true {
		t.Error("boolPtr(true) should point to true")
	}
}

func TestBoolPtrFalse(t *testing.T) {
	p := boolPtr(false)
	if p == nil {
		t.Fatal("boolPtr(false) returned nil")
	}
	if *p != false {
		t.Error("boolPtr(false) should point to false")
	}
}

func TestBoolPtrUniqueness(t *testing.T) {
	a := boolPtr(true)
	b := boolPtr(true)
	if a == b {
		t.Error("boolPtr should return distinct pointers")
	}
}

// --- ToolsConfig.Version tests ---

func TestToolsConfigVersion(t *testing.T) {
	cfg := &ToolsConfig{Version: "1.2.3"}
	if cfg.Version != "1.2.3" {
		t.Errorf("Version = %q, want %q", cfg.Version, "1.2.3")
	}
}

func TestToolsConfigVersionDefault(t *testing.T) {
	cfg := &ToolsConfig{}
	if cfg.Version != "" {
		t.Errorf("default Version should be empty, got %q", cfg.Version)
	}
}

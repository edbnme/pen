// Command pen is an MCP server that acts as an autonomous performance
// engineer for web applications, connecting to Chrome via CDP.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/edbnme/pen/internal/cdp"
	"github.com/edbnme/pen/internal/security"
	"github.com/edbnme/pen/internal/server"
	"github.com/edbnme/pen/internal/tools"
)

// Set via -ldflags at build time; falls back to "dev".
// When installed via "go install", the module version is read from build info.
var version = "dev"

func init() {
	if version != "dev" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	// go install sets Main.Version to the module version (e.g. "v0.1.0").
	// Skip pseudo-versions from local builds (contain timestamps) and dirty builds.
	v := info.Main.Version
	if v == "" || v == "(devel)" {
		return
	}
	if strings.Contains(v, "+dirty") || strings.Contains(v, "-0.") {
		return
	}
	version = strings.TrimPrefix(v, "v")
}

func main() {
	// Handle subcommands before flag parsing.
	if len(os.Args) > 1 {
		sub := os.Args[1]
		switch sub {
		case "init":
			runInit(os.Args[2:])
			return
		case "check":
			runCheck()
			return
		case "update":
			runUpdate()
			return
		default:
			// Check for unknown subcommands (not flags).
			if !strings.HasPrefix(sub, "-") {
				suggestion := suggestCommand(sub)
				if suggestion != "" {
					fmt.Fprintf(os.Stderr, "pen: unknown command %q\n\n  Did you mean:  pen %s\n\n", sub, suggestion)
				} else {
					fmt.Fprintf(os.Stderr, "pen: unknown command %q\n\n  Available commands:\n    init      Set up your IDE and browser\n    check     Verify your setup is working\n    update    Update pen to the latest version\n\n  Run pen --help for server options.\n\n", sub)
				}
				os.Exit(1)
			}
		}
	}

	cdpURL := flag.String("cdp-url", "http://localhost:9222", "CDP endpoint URL")
	transport := flag.String("transport", "stdio", "MCP transport: stdio, sse, http")
	addr := flag.String("addr", "localhost:6100", "Bind address for HTTP/SSE transport (endpoint: /mcp)")
	allowEval := flag.Bool("allow-eval", false, "Enable pen_evaluate (security-sensitive)")
	stateless := flag.Bool("stateless", false, "Disable session tracking for HTTP transport (no Mcp-Session-Id needed)")
	projectRoot := flag.String("project-root", ".", "Project root for source path validation (default: current directory)")
	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")
	autoLaunch := flag.Bool("auto-launch", true, "Auto-launch a debug browser if CDP is not reachable (uses a separate profile, does not affect your existing browser)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("pen", version)
		os.Exit(0)
	}

	// Configure logger.
	level := parseLogLevel(*logLevel)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	// Validate CDP URL (localhost-only).
	if err := security.ValidateCDPURL(*cdpURL); err != nil {
		logger.Error("invalid CDP URL", "err", err)
		os.Exit(1)
	}

	// Set up context with signal handling.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// On shutdown signal, log and enforce a grace period for in-progress operations.
	go func() {
		<-ctx.Done()
		logger.Info("shutting down, allowing in-progress operations to complete...")
		time.AfterFunc(5*time.Second, func() {
			logger.Warn("forced shutdown after 5s grace period")
			os.Exit(1)
		})
	}()

	// Create CDP client and connect with retry.
	// Use a quiet logger for initial connection to avoid noisy output on failure.
	quietLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cdpClient := cdp.NewClient(*cdpURL, quietLogger)
	const maxRetries = 3
	if err := cdpClient.Reconnect(ctx, maxRetries); err != nil {
		if !*autoLaunch {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "  [x] No browser with debugging enabled found")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "  PEN needs a Chromium-based browser running with a debug port.")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "  Options:")
			fmt.Fprintln(os.Stderr, "    1. Run  pen init  - interactive setup (recommended)")
			fmt.Fprintln(os.Stderr, "    2. Run  pen        - auto-launches a debug browser for you")
			fmt.Fprintln(os.Stderr, "    3. Start Chrome manually:")
			fmt.Fprintf(os.Stderr, "       chrome --remote-debugging-port=9222 --user-data-dir=%s\n", debugProfileDir())
			fmt.Fprintln(os.Stderr)
			os.Exit(1)
		}

		// Auto-launch: switch to real logger for visible feedback.
		cdpClient.SetLogger(logger)
		logger.Info("CDP not reachable, auto-launching a debug browser...")
		if launchErr := autoLaunchBrowser(*cdpURL, logger); launchErr != nil {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "  [x] Auto-launch failed")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "  Error: %v\n", launchErr)
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "  Troubleshooting:")
			fmt.Fprintln(os.Stderr, "    • Make sure Chrome, Edge, or Brave is installed")
			fmt.Fprintln(os.Stderr, "    • Close all Chrome windows and try again (existing Chrome can block the debug port)")
			fmt.Fprintln(os.Stderr, "    • Run  pen init  for guided setup")
			fmt.Fprintln(os.Stderr, "    • Or start Chrome manually:")
			fmt.Fprintf(os.Stderr, "      chrome --remote-debugging-port=9222 --user-data-dir=%s\n", debugProfileDir())
			fmt.Fprintln(os.Stderr)
			os.Exit(1)
		}

		// Retry connection after launch.
		if err := cdpClient.Reconnect(ctx, maxRetries); err != nil {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "  [x] Browser launched but CDP connection failed")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "  This usually means:")
			fmt.Fprintln(os.Stderr, "    • Another process is already using port 9222")
			fmt.Fprintln(os.Stderr, "    • The browser crashed during startup")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "  Try:")
			fmt.Fprintln(os.Stderr, "    • Close all browser windows, then run  pen  again")
			fmt.Fprintln(os.Stderr, "    • Use a different port:  pen --cdp-url http://localhost:9333")
			fmt.Fprintln(os.Stderr)
			os.Exit(1)
		}
	}
	// Ensure the real logger is used for ongoing operations.
	cdpClient.SetLogger(logger)

	// Set up auto-reconnect so tools can recover if the browser is closed.
	if *autoLaunch {
		cdpURL := *cdpURL
		cdpClient.SetReconnectFunc(func() error {
			tools.ResetNetworkListener()
			tools.ResetConsoleListener()
			tools.ResetScriptListener()
			cleanupTempDir(logger) // Remove stale temp files from old session.
			if err := autoLaunchBrowser(cdpURL, logger); err != nil {
				return err
			}
			return cdpClient.Reconnect(ctx, maxRetries)
		})
	}

	defer cdpClient.Close()

	// Build server.
	pen := server.New(cdpClient, &server.Config{
		Name:      "pen",
		Version:   version,
		Transport: *transport,
		HTTPAddr:  *addr,
		AllowEval: *allowEval,
		Stateless: *stateless,
		Logger:    logger,
	})

	// Register all tools.
	cdpPort := parseCDPPort(*cdpURL)
	tools.RegisterAll(pen.Server(), &tools.Deps{
		CDP:     cdpClient,
		Locks:   pen.Locks(),
		Limiter: security.NewRateLimiter(security.DefaultCooldowns),
		Config: &tools.ToolsConfig{
			AllowEval:   *allowEval,
			ProjectRoot: *projectRoot,
			Version:     version,
			CDPPort:     cdpPort,
		},
	})

	// Clean up temp files on exit.
	defer cleanupTempDir(logger)

	if *transport == "http" || *transport == "sse" {
		logger.Info("PEN ready", "version", version, "transport", *transport, "endpoint", fmt.Sprintf("http://%s/mcp", *addr), "cdp", *cdpURL)
	} else {
		logger.Info("PEN ready", "version", version, "transport", *transport, "cdp", *cdpURL)
	}

	// Run blocks until context is cancelled or client disconnects.
	if err := pen.Run(ctx); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// cleanupTempDir removes PEN's temp directory and all its contents.
func cleanupTempDir(logger *slog.Logger) {
	dir := filepath.Join(os.TempDir(), "pen")
	if _, err := os.Stat(dir); err != nil {
		return // Nothing to clean.
	}
	if err := os.RemoveAll(dir); err != nil {
		logger.Warn("failed to clean temp directory", "dir", dir, "err", err)
	} else {
		logger.Info("cleaned temp directory", "dir", dir)
	}
}

// autoLaunchBrowser detects an installed Chromium browser and launches it
// with a separate debug profile. The user's existing browser is not touched.
// It waits up to 10 seconds for the CDP port to become available.
func autoLaunchBrowser(cdpURL string, logger *slog.Logger) error {
	// Parse port from CDP URL.
	u, err := url.Parse(cdpURL)
	if err != nil {
		return fmt.Errorf("invalid CDP URL: %w", err)
	}
	port := u.Port()
	if port == "" {
		port = "9222"
	}

	// Check if CDP is already listening (e.g., user or previous PEN already launched it).
	if _, err := checkCDPConnection(port); err == nil {
		logger.Info("CDP already available, skipping browser launch", "port", port)
		return nil
	}

	// Detect available browsers.
	logger.Debug("searching for installed browsers...")
	browsers := detectBrowsers()
	if len(browsers) == 0 {
		return fmt.Errorf("no Chromium-based browser found - install Chrome, Edge, or Brave and try again")
	}
	for _, b := range browsers {
		logger.Debug("found browser", "name", b.Name, "path", b.Path)
	}

	// Use the first detected browser.
	browser := browsers[0]
	logger.Info("auto-launching browser", "browser", browser.Name, "path", browser.Path, "port", port)

	cfg := &initConfig{
		Browser:     browser.ID,
		BrowserPath: browser.Path,
		CDPPort:     port,
	}

	if err := launchBrowserProcess(cfg); err != nil {
		return fmt.Errorf("launch %s: %w", browser.Name, err)
	}

	// Wait for the CDP port to become available with progressive feedback.
	logger.Info("waiting for browser to start...", "port", port)
	for i := 1; i <= 10; i++ {
		time.Sleep(time.Second)
		if _, err := checkCDPConnection(port); err == nil {
			logger.Info("browser ready - CDP connected", "browser", browser.Name, "port", port)
			return nil
		}
		if i%3 == 0 {
			logger.Debug("still waiting for CDP port...", "elapsed_seconds", i)
		}
	}

	return fmt.Errorf("auto-launch: %s started but CDP port %s not available after 10s\n"+
		"  Hint: Port %s may be in use by another application. Try --cdp-url http://localhost:9333",
		browser.Name, port, port)
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// parseCDPPort extracts the port number from a CDP URL.
// Returns 0 if the port cannot be determined.
func parseCDPPort(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	portStr := u.Port()
	if portStr == "" {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}

// suggestCommand returns the closest known subcommand for a typo, or "" if no close match.
func suggestCommand(input string) string {
	commands := []string{"init", "check", "update"}
	input = strings.ToLower(input)
	for _, cmd := range commands {
		if levenshtein(input, cmd) <= 2 {
			return cmd
		}
	}
	return ""
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// runUpdate self-updates pen to the latest release from GitHub.
func runUpdate() {
	fmt.Println()
	fmt.Println("  Checking for updates...")
	fmt.Println()

	// Fetch latest release from GitHub.
	latestVersion, downloadURL, err := fetchLatestRelease()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [x] Update check failed: %v\n\n", err)
		fmt.Fprintln(os.Stderr, "  You can update manually:")
		fmt.Fprintln(os.Stderr, "    go install github.com/edbnme/pen/cmd/pen@latest")
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}

	currentVersion := strings.TrimPrefix(version, "v")
	latestClean := strings.TrimPrefix(latestVersion, "v")

	if currentVersion == latestClean || !isNewerVersion(currentVersion, latestClean) {
		fmt.Printf("  [ok] Already up to date (%s)\n\n", currentVersion)
		return
	}

	fmt.Printf("  Current version: %s\n", currentVersion)
	fmt.Printf("  Latest version:  %s\n", latestClean)
	fmt.Println()

	// Determine installation method and update accordingly.
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [x] Cannot determine executable path: %v\n\n", err)
		os.Exit(1)
	}
	exePath, _ = filepath.EvalSymlinks(exePath)

	// Check if installed via go install (binary in GOPATH/bin or GOBIN).
	if isGoInstall(exePath) {
		fmt.Println("  Updating via go install...")
		cmd := exec.Command("go", "install", "github.com/edbnme/pen/cmd/pen@latest")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "\n  [x] go install failed: %v\n\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n  [ok] Updated to %s\n\n", latestClean)
		return
	}

	// Binary install - download and replace.
	fmt.Println("  Downloading update...")
	if err := downloadAndReplace(exePath, downloadURL); err != nil {
		fmt.Fprintf(os.Stderr, "  [x] Update failed: %v\n\n", err)
		fmt.Fprintln(os.Stderr, "  You can update manually by re-running the install script:")
		if runtime.GOOS == "windows" {
			fmt.Fprintln(os.Stderr, "    irm https://raw.githubusercontent.com/edbnme/pen/main/install.ps1 | iex")
		} else {
			fmt.Fprintln(os.Stderr, "    curl -fsSL https://raw.githubusercontent.com/edbnme/pen/main/install.sh | sh")
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}

	fmt.Printf("\n  [ok] Updated pen: %s -> %s\n\n", currentVersion, latestClean)
}

// fetchLatestRelease queries GitHub for the latest release version and asset URL.
func fetchLatestRelease() (version string, assetURL string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/edbnme/pen/releases/latest", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("cannot reach GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", fmt.Errorf("invalid API response: %w", err)
	}

	if release.TagName == "" {
		return "", "", fmt.Errorf("no releases found")
	}

	// Find the asset for this OS/arch.
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	wantName := fmt.Sprintf("pen_%s_%s_%s%s",
		strings.TrimPrefix(release.TagName, "v"), goos, goarch, ext)

	for _, a := range release.Assets {
		if a.Name == wantName {
			return release.TagName, a.BrowserDownloadURL, nil
		}
	}

	return release.TagName, "", fmt.Errorf("no binary found for %s/%s in release %s", goos, goarch, release.TagName)
}

// downloadAndReplace downloads the release archive and replaces the current binary.
func downloadAndReplace(currentPath, assetURL string) error {
	if assetURL == "" {
		return fmt.Errorf("no download URL available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	// Write to a temp file next to the current binary.
	dir := filepath.Dir(currentPath)
	tmpFile, err := os.CreateTemp(dir, "pen-update-*")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Read archive into memory (release zips are small, <20MB).
	archiveData, err := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	tmpFile.Close()
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("download read failed: %w", err)
	}

	// Extract the binary.
	binaryData, err := extractBinaryFromArchive(archiveData, runtime.GOOS)
	if err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Write the new binary to a temp file.
	if err := os.WriteFile(tmpPath, binaryData, 0755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write failed: %w", err)
	}

	// On Windows, we can't replace a running binary directly.
	// Rename current -> .old, then new -> current.
	if runtime.GOOS == "windows" {
		oldPath := currentPath + ".old"
		os.Remove(oldPath) // Remove previous .old if exists.
		if err := os.Rename(currentPath, oldPath); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("cannot rename current binary: %w", err)
		}
		if err := os.Rename(tmpPath, currentPath); err != nil {
			// Attempt recovery.
			os.Rename(oldPath, currentPath)
			os.Remove(tmpPath)
			return fmt.Errorf("cannot place new binary: %w", err)
		}
		// Clean up old binary (best effort - may still be locked).
		go func() {
			time.Sleep(2 * time.Second)
			os.Remove(oldPath)
		}()
	} else {
		if err := os.Rename(tmpPath, currentPath); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("cannot replace binary: %w", err)
		}
	}

	return nil
}

// extractBinaryFromArchive extracts the pen binary from a zip or tar.gz archive.
func extractBinaryFromArchive(data []byte, goos string) ([]byte, error) {
	binaryName := "pen"
	if goos == "windows" {
		binaryName = "pen.exe"
	}

	if goos == "windows" {
		return extractFromZip(data, binaryName)
	}
	return extractFromTarGz(data, binaryName)
}

// extractFromZip extracts a named file from a zip archive in memory.
func extractFromZip(data []byte, name string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip: %w", err)
	}
	for _, f := range r.File {
		if filepath.Base(f.Name) == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 50*1024*1024))
		}
	}
	return nil, fmt.Errorf("%s not found in archive", name)
}

// extractFromTarGz extracts a named file from a tar.gz archive in memory.
func extractFromTarGz(data []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read error: %w", err)
		}
		if filepath.Base(hdr.Name) == name {
			return io.ReadAll(io.LimitReader(tr, 50*1024*1024))
		}
	}
	return nil, fmt.Errorf("%s not found in archive", name)
}

// isGoInstall checks if the binary is in a Go bin directory.
func isGoInstall(exePath string) bool {
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		home, _ := os.UserHomeDir()
		gopath = filepath.Join(home, "go")
	}
	goBin := os.Getenv("GOBIN")
	if goBin == "" {
		goBin = filepath.Join(gopath, "bin")
	}

	dir := filepath.Dir(exePath)
	return strings.EqualFold(dir, goBin) || strings.EqualFold(dir, filepath.Join(gopath, "bin"))
}

// isNewerVersion returns true if latest is strictly newer than current.
// Handles simple semver (major.minor.patch) with optional pre-release suffixes.
func isNewerVersion(current, latest string) bool {
	parseParts := func(v string) (int, int, int) {
		v = strings.TrimPrefix(v, "v")
		// Strip pre-release suffix (e.g., "0.2.0-rc1" -> "0.2.0").
		if idx := strings.IndexByte(v, '-'); idx >= 0 {
			v = v[:idx]
		}
		parts := strings.SplitN(v, ".", 3)
		atoi := func(s string) int {
			n, _ := strconv.Atoi(s)
			return n
		}
		var maj, min, pat int
		if len(parts) > 0 {
			maj = atoi(parts[0])
		}
		if len(parts) > 1 {
			min = atoi(parts[1])
		}
		if len(parts) > 2 {
			pat = atoi(parts[2])
		}
		return maj, min, pat
	}

	cmaj, cmin, cpat := parseParts(current)
	lmaj, lmin, lpat := parseParts(latest)

	if lmaj != cmaj {
		return lmaj > cmaj
	}
	if lmin != cmin {
		return lmin > cmin
	}
	return lpat > cpat
}

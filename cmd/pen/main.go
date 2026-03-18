// Command pen is an MCP server that acts as an autonomous performance
// engineer for web applications, connecting to Chrome via CDP.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
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
	if len(os.Args) > 1 && os.Args[1] == "init" {
		runInit()
		return
	}

	cdpURL := flag.String("cdp-url", "http://localhost:9222", "CDP endpoint URL")
	transport := flag.String("transport", "stdio", "MCP transport: stdio, sse, http")
	addr := flag.String("addr", "localhost:6100", "Bind address for HTTP/SSE transport")
	allowEval := flag.Bool("allow-eval", false, "Enable pen_evaluate (security-sensitive)")
	stateless := flag.Bool("stateless", false, "Disable session tracking for HTTP transport (each request handled independently)")
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
	cdpClient := cdp.NewClient(*cdpURL, logger)
	const maxRetries = 3
	if err := cdpClient.Reconnect(ctx, maxRetries); err != nil {
		if !*autoLaunch {
			logger.Error("CDP connect failed — no browser with debugging enabled found",
				"err", err,
				"hint", "Start Chrome with: chrome --remote-debugging-port=9222 --user-data-dir="+debugProfileDir(),
			)
			os.Exit(1)
		}

		// Auto-launch: detect a browser and launch it with a separate debug profile.
		logger.Info("CDP not reachable, auto-launching a debug browser...")
		if launchErr := autoLaunchBrowser(*cdpURL, logger); launchErr != nil {
			logger.Error("auto-launch failed",
				"err", launchErr,
				"hint", "Start Chrome/Chromium manually with: chrome --remote-debugging-port=9222 --user-data-dir="+debugProfileDir(),
			)
			os.Exit(1)
		}

		// Retry connection after launch.
		if err := cdpClient.Reconnect(ctx, maxRetries); err != nil {
			logger.Error("CDP connect failed after auto-launch",
				"err", err,
				"hint", "Browser launched but CDP port not available. Check if another process is using the port.",
			)
			os.Exit(1)
		}
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

	logger.Info("PEN ready", "version", version, "transport", *transport, "cdp", *cdpURL)

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
	browsers := detectBrowsers()
	if len(browsers) == 0 {
		return fmt.Errorf("no Chromium-based browser found — install Chrome, Edge, or Brave and try again")
	}

	// Use the first detected browser.
	browser := browsers[0]
	logger.Info("auto-launching browser", "browser", browser.Name, "port", port)

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
			logger.Info("browser ready — CDP connected", "browser", browser.Name, "port", port)
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

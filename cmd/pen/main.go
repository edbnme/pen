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
	"strconv"
	"strings"
	"syscall"

	"github.com/edbnme/pen/internal/cdp"
	"github.com/edbnme/pen/internal/security"
	"github.com/edbnme/pen/internal/server"
	"github.com/edbnme/pen/internal/tools"
)

// Set via -ldflags at build time; falls back to "dev".
var version = "dev"

func main() {
	cdpURL := flag.String("cdp-url", "http://localhost:9222", "CDP endpoint URL")
	transport := flag.String("transport", "stdio", "MCP transport: stdio, sse, http")
	addr := flag.String("addr", "localhost:6100", "Bind address for HTTP/SSE transport")
	allowEval := flag.Bool("allow-eval", false, "Enable pen_evaluate (security-sensitive)")
	projectRoot := flag.String("project-root", ".", "Project root for source path validation (default: current directory)")
	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")
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

	// Create CDP client and connect with retry.
	cdpClient := cdp.NewClient(*cdpURL, logger)
	const maxRetries = 3
	if err := cdpClient.Reconnect(ctx, maxRetries); err != nil {
		logger.Error("CDP connect failed after retries",
			"err", err,
			"hint", "Start Chrome/Chromium with: chrome --remote-debugging-port=9222",
		)
		os.Exit(1)
	}
	defer cdpClient.Close()

	// Build server.
	pen := server.New(cdpClient, &server.Config{
		Name:      "pen",
		Version:   version,
		Transport: *transport,
		HTTPAddr:  *addr,
		AllowEval: *allowEval,
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

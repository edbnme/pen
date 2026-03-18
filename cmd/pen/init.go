package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/charmbracelet/lipgloss"
)

// ── Styles ──────────────────────────────────────────────────────────────────

var (
	cyan   = lipgloss.Color("#00D7FF")
	green  = lipgloss.Color("#00FF87")
	yellow = lipgloss.Color("#FFD700")
	red    = lipgloss.Color("#FF5F5F")
	gray   = lipgloss.Color("#6C6C6C")
	white  = lipgloss.Color("#FAFAFA")
	purple = lipgloss.Color("#B48EF7")

	brandStyle   = lipgloss.NewStyle().Bold(true).Foreground(cyan)
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(white)
	dimStyle     = lipgloss.NewStyle().Foreground(gray)
	accentStyle  = lipgloss.NewStyle().Foreground(purple)
	successStyle = lipgloss.NewStyle().Bold(true).Foreground(green)

	checkMark = lipgloss.NewStyle().Bold(true).Foreground(green).Render("✓")
	warnMark  = lipgloss.NewStyle().Bold(true).Foreground(yellow).Render("!")
	crossMark = lipgloss.NewStyle().Bold(true).Foreground(red).Render("✗")
)

// ── Banner ──────────────────────────────────────────────────────────────────

const asciiBanner = `
  ██████╗ ███████╗███╗   ██╗
  ██╔══██╗██╔════╝████╗  ██║
  ██████╔╝█████╗  ██╔██╗ ██║
  ██╔═══╝ ██╔══╝  ██║╚██╗██║
  ██║     ███████╗██║ ╚████║
  ╚═╝     ╚══════╝╚═╝  ╚═══╝`

func printBanner() {
	fmt.Println(brandStyle.Render(asciiBanner))
	fmt.Println()
	fmt.Println(titleStyle.Render("  AI-Powered Browser Performance Engineering"))
	fmt.Println(dimStyle.Render("  v" + version))
	fmt.Println()
}

func printSeparator() {
	fmt.Println(dimStyle.Render("  " + strings.Repeat("─", 52)))
	fmt.Println()
}

// ── Types ───────────────────────────────────────────────────────────────────

type browserInfo struct {
	Name string
	ID   string // "chrome", "edge", "brave"
	Path string
}

type ideInfo struct {
	Name string
	ID   string // "vscode", "cursor", "claude"
}

type detectedEnv struct {
	OS            string
	Arch          string
	Browsers      []browserInfo
	IDEs          []ideInfo
	HasNode       bool
	NodeVersion   string
	HasLighthouse bool
}

type initConfig struct {
	IDE         string
	Browser     string
	BrowserPath string
	CDPPort     string
	AllowEval   bool
}

// ── Detection ───────────────────────────────────────────────────────────────

func detectEnvironment() detectedEnv {
	env := detectedEnv{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Browsers: detectBrowsers(),
		IDEs:     detectIDEs(),
	}

	// Detect Node.js.
	if nodePath, err := exec.LookPath("node"); err == nil {
		env.HasNode = true
		if out, err := exec.Command(nodePath, "--version").Output(); err == nil {
			env.NodeVersion = strings.TrimSpace(string(out))
		}
	}

	// Detect lighthouse CLI.
	if _, err := exec.LookPath("lighthouse"); err == nil {
		env.HasLighthouse = true
	}

	return env
}

func detectBrowsers() []browserInfo {
	switch runtime.GOOS {
	case "darwin":
		return detectBrowsersDarwin()
	case "windows":
		return detectBrowsersWindows()
	default:
		return detectBrowsersLinux()
	}
}

func detectBrowsersDarwin() []browserInfo {
	var found []browserInfo
	apps := []struct {
		name, id, path string
	}{
		{"Google Chrome", "chrome", "/Applications/Google Chrome.app"},
		{"Microsoft Edge", "edge", "/Applications/Microsoft Edge.app"},
		{"Brave Browser", "brave", "/Applications/Brave Browser.app"},
	}
	for _, a := range apps {
		if _, err := os.Stat(a.path); err == nil {
			found = append(found, browserInfo{Name: a.name, ID: a.id, Path: a.path})
		}
	}
	return found
}

func detectBrowsersWindows() []browserInfo {
	var found []browserInfo
	pf := os.Getenv("ProgramFiles")
	pfx86 := os.Getenv("ProgramFiles(x86)")
	localAppData := os.Getenv("LOCALAPPDATA")

	type search struct {
		name, id string
		paths    []string
	}
	searches := []search{
		{"Google Chrome", "chrome", []string{
			filepath.Join(pf, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(pfx86, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(localAppData, "Google", "Chrome", "Application", "chrome.exe"),
		}},
		{"Microsoft Edge", "edge", []string{
			filepath.Join(pf, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(pfx86, "Microsoft", "Edge", "Application", "msedge.exe"),
		}},
		{"Brave Browser", "brave", []string{
			filepath.Join(pf, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			filepath.Join(pfx86, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			filepath.Join(localAppData, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
		}},
	}

	for _, s := range searches {
		for _, p := range s.paths {
			if _, err := os.Stat(p); err == nil {
				found = append(found, browserInfo{Name: s.name, ID: s.id, Path: p})
				break
			}
		}
	}
	return found
}

func detectBrowsersLinux() []browserInfo {
	var found []browserInfo
	cmds := []struct {
		name, id, cmd string
	}{
		{"Google Chrome", "chrome", "google-chrome"},
		{"Chromium", "chrome", "chromium-browser"},
		{"Chromium", "chrome", "chromium"},
		{"Microsoft Edge", "edge", "microsoft-edge"},
		{"Brave Browser", "brave", "brave-browser"},
	}
	seen := map[string]bool{}
	for _, c := range cmds {
		if seen[c.id] {
			continue
		}
		if p, err := exec.LookPath(c.cmd); err == nil {
			found = append(found, browserInfo{Name: c.name, ID: c.id, Path: p})
			seen[c.id] = true
		}
	}
	return found
}

func detectIDEs() []ideInfo {
	var found []ideInfo

	// VS Code
	for _, cmd := range []string{"code", "code.cmd"} {
		if _, err := exec.LookPath(cmd); err == nil {
			found = append(found, ideInfo{Name: "VS Code", ID: "vscode"})
			break
		}
	}

	// Cursor
	for _, cmd := range []string{"cursor", "cursor.cmd"} {
		if _, err := exec.LookPath(cmd); err == nil {
			found = append(found, ideInfo{Name: "Cursor", ID: "cursor"})
			break
		}
	}

	// Claude Desktop — check if config directory exists
	home, _ := os.UserHomeDir()
	claudePaths := []string{
		filepath.Join(home, "Library", "Application Support", "Claude"),
		filepath.Join(os.Getenv("APPDATA"), "Claude"),
		filepath.Join(home, ".config", "Claude"),
	}
	for _, p := range claudePaths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			found = append(found, ideInfo{Name: "Claude Desktop", ID: "claude"})
			break
		}
	}

	return found
}

// ── Display Helpers ─────────────────────────────────────────────────────────

func printDetectionResults(env *detectedEnv) {
	osNames := map[string]string{
		"darwin": "macOS", "linux": "Linux", "windows": "Windows",
	}
	osName := osNames[env.OS]
	if osName == "" {
		osName = env.OS
	}

	fmt.Printf("  %s Platform: %s/%s\n", checkMark, osName, env.Arch)

	if len(env.Browsers) > 0 {
		names := make([]string, len(env.Browsers))
		for i, b := range env.Browsers {
			names[i] = b.Name
		}
		fmt.Printf("  %s Browsers: %s\n", checkMark, strings.Join(names, ", "))
	} else {
		fmt.Printf("  %s No Chromium browsers detected\n", warnMark)
	}

	if len(env.IDEs) > 0 {
		names := make([]string, len(env.IDEs))
		for i, ide := range env.IDEs {
			names[i] = ide.Name
		}
		fmt.Printf("  %s IDEs: %s\n", checkMark, strings.Join(names, ", "))
	} else {
		fmt.Printf("  %s No MCP-compatible IDEs detected\n", warnMark)
	}

	if env.HasNode {
		nodeInfo := "Node.js " + env.NodeVersion
		if env.HasLighthouse {
			nodeInfo += " + Lighthouse"
		}
		fmt.Printf("  %s %s\n", checkMark, nodeInfo)
	} else {
		fmt.Printf("  %s Node.js not found (needed for Lighthouse audits)\n", warnMark)
	}

	fmt.Println()
}

// ── Config Generation ───────────────────────────────────────────────────────

func buildPenArgs(cfg *initConfig, useWorkspaceVar bool) []string {
	args := []string{"--auto-launch"}
	if useWorkspaceVar {
		args = append(args, "--project-root", "${workspaceFolder}")
	} else {
		abs, err := filepath.Abs(".")
		if err != nil {
			abs = "."
		}
		args = append(args, "--project-root", abs)
	}
	if cfg.AllowEval {
		args = append(args, "--allow-eval")
	}
	if cfg.CDPPort != "" && cfg.CDPPort != "9222" {
		args = append(args, "--cdp-url", "http://localhost:"+cfg.CDPPort)
	}
	return args
}

func generateMCPConfig(cfg *initConfig) (string, error) {
	var path, rootKey string
	var useWorkspaceVar bool

	switch cfg.IDE {
	case "vscode":
		if err := os.MkdirAll(".vscode", 0755); err != nil {
			return "", fmt.Errorf("create .vscode: %w", err)
		}
		path = filepath.Join(".vscode", "mcp.json")
		rootKey = "servers"
		useWorkspaceVar = true
	case "cursor":
		if err := os.MkdirAll(".cursor", 0755); err != nil {
			return "", fmt.Errorf("create .cursor: %w", err)
		}
		path = filepath.Join(".cursor", "mcp.json")
		rootKey = "mcpServers"
		useWorkspaceVar = true
	case "claude":
		path = claudeConfigPath()
		if path == "" {
			return "", fmt.Errorf("could not determine Claude Desktop config path")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", fmt.Errorf("create config directory: %w", err)
		}
		rootKey = "mcpServers"
		useWorkspaceVar = false
	default:
		return "", nil
	}

	// Read existing config to preserve other entries.
	existing := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	servers, ok := existing[rootKey].(map[string]any)
	if !ok {
		servers = map[string]any{}
	}

	servers["pen"] = map[string]any{
		"command": "pen",
		"args":    buildPenArgs(cfg, useWorkspaceVar),
	}
	existing[rootKey] = servers

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, append(out, '\n'), 0644)
}

func claudeConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return ""
		}
		return filepath.Join(appData, "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
}

// ── Browser Launch ──────────────────────────────────────────────────────────

func browserDisplayName(id string) string {
	switch id {
	case "chrome":
		return "Google Chrome"
	case "edge":
		return "Microsoft Edge"
	case "brave":
		return "Brave"
	default:
		return id
	}
}

// browserProcessNames returns the process names to check for a given browser ID.
func browserProcessNames(id string) []string {
	switch id {
	case "chrome":
		return []string{"chrome", "chrome.exe", "Google Chrome"}
	case "edge":
		return []string{"msedge", "msedge.exe", "Microsoft Edge"}
	case "brave":
		return []string{"brave", "brave.exe", "Brave Browser"}
	default:
		return nil
	}
}

// isBrowserRunning checks if any process matching the browser ID is currently running.
func isBrowserRunning(browserID string) bool {
	names := browserProcessNames(browserID)
	if len(names) == 0 {
		return false
	}

	switch runtime.GOOS {
	case "windows":
		for _, name := range names {
			out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq "+name, "/NH").Output()
			if err == nil && strings.Contains(string(out), name) {
				return true
			}
		}
	case "darwin":
		// macOS: check by "Google Chrome", "Microsoft Edge" etc via pgrep.
		for _, name := range names {
			if err := exec.Command("pgrep", "-x", name).Run(); err == nil {
				return true
			}
		}
	default:
		// Linux: pgrep by process name.
		for _, name := range names {
			if err := exec.Command("pgrep", "-x", name).Run(); err == nil {
				return true
			}
		}
	}
	return false
}

// debugProfileDir returns a temp directory path for the browser debug profile.
func debugProfileDir() string {
	return filepath.Join(os.TempDir(), "pen-debug-profile")
}

func getBrowserManualCmd(cfg *initConfig) string {
	port := cfg.CDPPort
	if port == "" {
		port = "9222"
	}
	profileDir := debugProfileDir()
	extraFlags := " --no-first-run --no-default-browser-check"

	switch runtime.GOOS {
	case "darwin":
		switch cfg.Browser {
		case "chrome":
			return fmt.Sprintf(`open -a "Google Chrome" --args --remote-debugging-port=%s --user-data-dir=%s%s`, port, profileDir, extraFlags)
		case "edge":
			return fmt.Sprintf(`open -a "Microsoft Edge" --args --remote-debugging-port=%s --user-data-dir=%s%s`, port, profileDir, extraFlags)
		case "brave":
			return fmt.Sprintf(`open -a "Brave Browser" --args --remote-debugging-port=%s --user-data-dir=%s%s`, port, profileDir, extraFlags)
		}
	case "windows":
		if cfg.BrowserPath != "" {
			return fmt.Sprintf(`& "%s" --remote-debugging-port=%s --user-data-dir="%s"%s`, cfg.BrowserPath, port, profileDir, extraFlags)
		}
		switch cfg.Browser {
		case "chrome":
			return fmt.Sprintf(`& "C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=%s --user-data-dir="%s"%s`, port, profileDir, extraFlags)
		case "edge":
			return fmt.Sprintf(`& "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --remote-debugging-port=%s --user-data-dir="%s"%s`, port, profileDir, extraFlags)
		case "brave":
			return fmt.Sprintf(`& "C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe" --remote-debugging-port=%s --user-data-dir="%s"%s`, port, profileDir, extraFlags)
		}
	default:
		switch cfg.Browser {
		case "chrome":
			return fmt.Sprintf("google-chrome --remote-debugging-port=%s --user-data-dir=%s%s", port, profileDir, extraFlags)
		case "edge":
			return fmt.Sprintf("microsoft-edge --remote-debugging-port=%s --user-data-dir=%s%s", port, profileDir, extraFlags)
		case "brave":
			return fmt.Sprintf("brave-browser --remote-debugging-port=%s --user-data-dir=%s%s", port, profileDir, extraFlags)
		}
	}
	return fmt.Sprintf("chrome --remote-debugging-port=%s --user-data-dir=%s%s", port, profileDir, extraFlags)
}

func launchBrowserProcess(cfg *initConfig) error {
	port := cfg.CDPPort
	if port == "" {
		port = "9222"
	}

	profileDir := debugProfileDir()
	var bin string
	var args []string

	// Common flags for all platforms.
	commonFlags := []string{
		"--remote-debugging-port=" + port,
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}

	switch runtime.GOOS {
	case "darwin":
		bin = "open"
		var appName string
		switch cfg.Browser {
		case "chrome":
			appName = "Google Chrome"
		case "edge":
			appName = "Microsoft Edge"
		case "brave":
			appName = "Brave Browser"
		default:
			return fmt.Errorf("unsupported browser: %s", cfg.Browser)
		}
		args = append([]string{"-a", appName, "--args"}, commonFlags...)
	case "windows":
		bin = cfg.BrowserPath
		if bin == "" {
			switch cfg.Browser {
			case "chrome":
				bin = filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe")
			case "edge":
				bin = filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe")
			case "brave":
				bin = filepath.Join(os.Getenv("ProgramFiles"), "BraveSoftware", "Brave-Browser", "Application", "brave.exe")
			default:
				return fmt.Errorf("unsupported browser: %s", cfg.Browser)
			}
		}
		args = commonFlags
	default:
		switch cfg.Browser {
		case "chrome":
			bin = "google-chrome"
		case "edge":
			bin = "microsoft-edge"
		case "brave":
			bin = "brave-browser"
		default:
			return fmt.Errorf("unsupported browser: %s", cfg.Browser)
		}
		args = commonFlags
	}

	if bin == "" {
		return fmt.Errorf("could not determine browser path")
	}

	proc := exec.Command(bin, args...)
	proc.Stdout = nil
	proc.Stderr = nil
	return proc.Start()
}

// ── CDP Verification ────────────────────────────────────────────────────────

func checkCDPConnection(port string) (int, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:" + port + "/json")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var targets []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return 0, err
	}
	return len(targets), nil
}

// ── Main Init Flow ──────────────────────────────────────────────────────────

func runInit() {
	printBanner()
	printSeparator()

	// ── Phase 1: Environment Detection ──────────────────────────────────
	fmt.Println(titleStyle.Render("  Scanning environment"))
	fmt.Println()

	var env detectedEnv
	_ = spinner.New().
		Title("  Detecting browsers and IDEs...").
		Action(func() {
			env = detectEnvironment()
			time.Sleep(600 * time.Millisecond)
		}).
		Run()

	printDetectionResults(&env)
	printSeparator()

	// ── Phase 2: Interactive Configuration ──────────────────────────────
	fmt.Println(titleStyle.Render("  Configuration"))
	fmt.Println()

	cfg := &initConfig{
		CDPPort: "9222",
	}

	// Build IDE options — detected first, then others, then skip.
	ideOptions := buildIDEOptions(env.IDEs)
	browserOptions := buildBrowserOptions(env.Browsers)

	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which IDE do you use?").
				Description("PEN will create the MCP config for your editor.").
				Options(ideOptions...).
				Value(&cfg.IDE),

			huh.NewSelect[string]().
				Title("Which browser for debugging?").
				Description("PEN connects via Chrome DevTools Protocol.").
				Options(browserOptions...).
				Value(&cfg.Browser),
		),

		huh.NewGroup(
			huh.NewInput().
				Title("CDP debugging port").
				Description("The port Chrome listens on for DevTools connections.").
				Placeholder("9222").
				Value(&cfg.CDPPort).
				Validate(validatePort),

			huh.NewConfirm().
				Title("Enable pen_evaluate?").
				Description("Allows executing JavaScript in the browser. Security-sensitive — only enable in trusted environments.").
				Affirmative("Yes").
				Negative("No (recommended)").
				Value(&cfg.AllowEval),
		),
	).WithTheme(huh.ThemeCatppuccin()).Run()

	if err != nil {
		handleFormError(err)
		return
	}

	// Default port if user left it empty.
	if cfg.CDPPort == "" {
		cfg.CDPPort = "9222"
	}

	// Resolve browser path from detection.
	for _, b := range env.Browsers {
		if b.ID == cfg.Browser {
			cfg.BrowserPath = b.Path
			break
		}
	}

	printSeparator()

	// ── Phase 3: Generate Config ────────────────────────────────────────
	if cfg.IDE != "skip" {
		fmt.Println(titleStyle.Render("  Setting up your project"))
		fmt.Println()

		// Check if config already exists.
		existingPath := configPathForIDE(cfg.IDE)
		if existingPath != "" {
			if _, statErr := os.Stat(existingPath); statErr == nil {
				overwrite := true
				overwriteErr := huh.NewForm(
					huh.NewGroup(
						huh.NewConfirm().
							Title(fmt.Sprintf("%s already exists. Overwrite PEN entry?", existingPath)).
							Description("Other entries in the file will be preserved.").
							Affirmative("Yes, update it").
							Negative("Skip").
							Value(&overwrite),
					),
				).WithTheme(huh.ThemeCatppuccin()).Run()

				if overwriteErr != nil {
					handleFormError(overwriteErr)
					return
				}

				if !overwrite {
					fmt.Printf("  %s Skipped config generation\n", dimStyle.Render("—"))
					fmt.Println()
					printSeparator()
					goto browserPhase
				}
			}
		}

		var configPath string
		var genErr error
		_ = spinner.New().
			Title("  Writing MCP configuration...").
			Action(func() {
				configPath, genErr = generateMCPConfig(cfg)
				time.Sleep(400 * time.Millisecond)
			}).
			Run()

		if genErr != nil {
			fmt.Printf("  %s Failed to write config: %v\n", crossMark, genErr)
		} else {
			fmt.Printf("  %s Created %s\n", checkMark, accentStyle.Render(configPath))
		}
		fmt.Println()
		printSeparator()
	}

browserPhase:

	// ── Phase 4: Dependencies ───────────────────────────────────────────
	if !env.HasLighthouse {
		fmt.Println(titleStyle.Render("  Dependencies"))
		fmt.Println()

		if !env.HasNode {
			fmt.Printf("  %s Node.js is required for Lighthouse audits\n", warnMark)
			fmt.Println(dimStyle.Render("  Install from: https://nodejs.org"))
			fmt.Println(dimStyle.Render("  PEN works without it, but pen_lighthouse will be unavailable."))
			fmt.Println()
		} else {
			installLH := false
			lhErr := huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title("Install Lighthouse? (npm install -g lighthouse)").
						Description("Optional - only needed for pen_lighthouse audits. Takes ~30s on first install.").
						Affirmative("Yes, install it").
						Negative("Skip").
						Value(&installLH),
				),
			).WithTheme(huh.ThemeCatppuccin()).Run()

			if lhErr != nil && lhErr != huh.ErrUserAborted {
				handleFormError(lhErr)
				return
			}

			if installLH {
				var lhInstallErr error
				_ = spinner.New().
					Title("  Installing lighthouse...").
					Action(func() {
						cmd := exec.Command("npm", "install", "-g", "lighthouse")
						cmd.Stdout = nil
						cmd.Stderr = nil
						lhInstallErr = cmd.Run()
					}).
					Run()

				if lhInstallErr != nil {
					fmt.Printf("  %s Could not install lighthouse: %v\n", warnMark, lhInstallErr)
					fmt.Println(dimStyle.Render("  You can install manually later: npm install -g lighthouse"))
				} else {
					fmt.Printf("  %s Lighthouse installed\n", checkMark)
				}
			} else {
				fmt.Printf("  %s Skipped Lighthouse install\n", dimStyle.Render("-"))
			}
			fmt.Println()
		}
		printSeparator()
	}

	// ── Phase 5: Browser Launch ─────────────────────────────────────────
	fmt.Println(titleStyle.Render("  Browser setup"))
	fmt.Println()

	// Check if the selected browser is already running — the debug port
	// will be silently ignored if Chrome is already open.
	if isBrowserRunning(cfg.Browser) {
		fmt.Printf("  %s %s is already running\n", warnMark, browserDisplayName(cfg.Browser))
		fmt.Println(dimStyle.Render("  The debug port (--remote-debugging-port) is ignored when the"))
		fmt.Println(dimStyle.Render("  browser is already open. Close all instances first, then re-launch."))
		fmt.Println()
	}

	launchBrowser := false
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Launch browser with remote debugging?").
				Description(fmt.Sprintf("Opens %s with --remote-debugging-port=%s (uses a separate debug profile)", browserDisplayName(cfg.Browser), cfg.CDPPort)).
				Affirmative("Yes, launch it").
				Negative("No, I'll do it myself").
				Value(&launchBrowser),
		),
	).WithTheme(huh.ThemeCatppuccin()).Run()

	if err != nil && err != huh.ErrUserAborted {
		handleFormError(err)
		return
	}

	if launchBrowser {
		if launchErr := launchBrowserProcess(cfg); launchErr != nil {
			fmt.Printf("  %s Could not launch browser: %v\n", warnMark, launchErr)
			fmt.Println(dimStyle.Render("  Launch manually:"))
			fmt.Println(accentStyle.Render("  " + getBrowserManualCmd(cfg)))
		} else {
			fmt.Printf("  %s Browser launching on port %s\n", checkMark, cfg.CDPPort)
			fmt.Println(dimStyle.Render("  Waiting for browser to start..."))
			// Wait with retries instead of a fixed sleep — Chrome can be slow on some machines.
			for i := 0; i < 5; i++ {
				time.Sleep(time.Duration(i+1) * time.Second)
				if _, err := checkCDPConnection(cfg.CDPPort); err == nil {
					break
				}
			}
		}
	} else {
		fmt.Println(dimStyle.Render("  Launch your browser manually:"))
		fmt.Println()
		fmt.Println(accentStyle.Render("  " + getBrowserManualCmd(cfg)))
	}
	fmt.Println()
	printSeparator()

	// ── Phase 5: Verify CDP ─────────────────────────────────────────────
	fmt.Println(titleStyle.Render("  Checking connection"))
	fmt.Println()

	var tabCount int
	var cdpErr error
	_ = spinner.New().
		Title("  Connecting to Chrome DevTools Protocol...").
		Action(func() {
			tabCount, cdpErr = checkCDPConnection(cfg.CDPPort)
		}).
		Run()

	if cdpErr != nil {
		fmt.Printf("  %s Could not connect to CDP on port %s\n", warnMark, cfg.CDPPort)
		fmt.Println(dimStyle.Render("  That's OK — PEN connects when launched from your IDE."))
		fmt.Println(dimStyle.Render("  Make sure the browser is running with debugging enabled."))
	} else {
		noun := "tabs"
		if tabCount == 1 {
			noun = "tab"
		}
		fmt.Printf("  %s Connected! Found %d open %s\n", checkMark, tabCount, noun)
	}
	fmt.Println()
	printSeparator()

	// ── Phase 6: Success ────────────────────────────────────────────────
	printSuccess(cfg, cdpErr == nil)
}

// ── Form Helpers ────────────────────────────────────────────────────────────

func buildIDEOptions(detected []ideInfo) []huh.Option[string] {
	var opts []huh.Option[string]
	detectedIDs := map[string]bool{}

	// Detected IDEs first.
	for _, ide := range detected {
		opts = append(opts, huh.NewOption[string](ide.Name+" (detected)", ide.ID))
		detectedIDs[ide.ID] = true
	}

	// Non-detected in stable order.
	allIDEs := []struct{ id, name string }{
		{"vscode", "VS Code"},
		{"cursor", "Cursor"},
		{"claude", "Claude Desktop"},
	}
	for _, ide := range allIDEs {
		if !detectedIDs[ide.id] {
			opts = append(opts, huh.NewOption[string](ide.name, ide.id))
		}
	}

	opts = append(opts, huh.NewOption[string]("Skip (configure later)", "skip"))
	return opts
}

func buildBrowserOptions(detected []browserInfo) []huh.Option[string] {
	var opts []huh.Option[string]
	detectedIDs := map[string]bool{}

	// Detected browsers first.
	for _, b := range detected {
		opts = append(opts, huh.NewOption[string](b.Name+" (detected)", b.ID))
		detectedIDs[b.ID] = true
	}

	// Non-detected in stable order.
	allBrowsers := []struct{ id, name string }{
		{"chrome", "Google Chrome"},
		{"edge", "Microsoft Edge"},
		{"brave", "Brave"},
	}
	for _, b := range allBrowsers {
		if !detectedIDs[b.id] {
			opts = append(opts, huh.NewOption[string](b.name, b.id))
		}
	}
	return opts
}

func validatePort(s string) error {
	if s == "" {
		return nil
	}
	port, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("must be a number")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("must be between 1 and 65535")
	}
	return nil
}

func configPathForIDE(ide string) string {
	switch ide {
	case "vscode":
		return filepath.Join(".vscode", "mcp.json")
	case "cursor":
		return filepath.Join(".cursor", "mcp.json")
	case "claude":
		return claudeConfigPath()
	default:
		return ""
	}
}

func handleFormError(err error) {
	if err == huh.ErrUserAborted {
		fmt.Println()
		fmt.Println(dimStyle.Render("  Setup cancelled. Run " + accentStyle.Render("pen init") + " anytime to try again."))
		fmt.Println()
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "  %s %v\n", crossMark, err)
	os.Exit(1)
}

// ── Success ─────────────────────────────────────────────────────────────────

func printSuccess(cfg *initConfig, cdpConnected bool) {
	fmt.Println(successStyle.Render("  ✓ PEN is ready!"))
	fmt.Println()
	fmt.Println(titleStyle.Render("  Next steps"))
	fmt.Println()

	step := 1

	if cfg.IDE != "skip" {
		names := map[string]string{
			"vscode": "VS Code", "cursor": "Cursor", "claude": "Claude Desktop",
		}
		fmt.Printf("  %s Open your project in %s\n",
			dimStyle.Render(fmt.Sprintf("%d.", step)), names[cfg.IDE])
		step++
	}

	if !cdpConnected {
		fmt.Printf("  %s Make sure your browser is running with debugging enabled\n",
			dimStyle.Render(fmt.Sprintf("%d.", step)))
		step++
	}

	fmt.Printf("  %s Ask your AI: %s\n",
		dimStyle.Render(fmt.Sprintf("%d.", step)),
		accentStyle.Render(`"Check the performance of this page"`))
	fmt.Println()

	fmt.Println(dimStyle.Render("  30 tools ready — profiling, memory, network, coverage & more"))
	fmt.Println(dimStyle.Render("  Docs  → https://github.com/edbnme/pen"))
	fmt.Println(dimStyle.Render("  Tools → https://github.com/edbnme/pen/blob/main/docs/spec/08-tool-catalog.md"))
	fmt.Println()
}

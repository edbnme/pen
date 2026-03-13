// Package security provides validation and safety checks for PEN tool inputs.
package security

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// --- Eval expression filtering ---

// blockedPatterns lists regexes for dangerous JS patterns that pen_evaluate blocks.
// This is defense-in-depth — the primary gate is --allow-eval.
var blockedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bfetch\s*\(`),
	regexp.MustCompile(`(?i)\bXMLHttpRequest\b`),
	regexp.MustCompile(`(?i)\bnavigator\.sendBeacon\b`),
	regexp.MustCompile(`(?i)\blocalStorage\b`),
	regexp.MustCompile(`(?i)\bsessionStorage\b`),
	regexp.MustCompile(`(?i)\bdocument\.cookie\b`),
	regexp.MustCompile(`(?i)\bwindow\.open\s*\(`),
	regexp.MustCompile(`(?i)\beval\s*\(`),
	regexp.MustCompile(`(?i)\bFunction\s*\(`),
	regexp.MustCompile(`(?i)\bimport\s*\(`),
}

// unicodeEscapePattern detects JS Unicode escape sequences (\uXXXX) that could
// bypass keyword-based blocklist checks.
var unicodeEscapePattern = regexp.MustCompile(`\\u[0-9a-fA-F]{4}`)

// ValidateExpression checks a JS expression against the blocklist.
// Also rejects Unicode escape sequences that could disguise blocked identifiers.
func ValidateExpression(expr string) error {
	// Reject Unicode escapes that could bypass keyword checks.
	if unicodeEscapePattern.MatchString(expr) {
		return fmt.Errorf(
			"blocked expression: contains Unicode escape sequence. " +
				"PEN does not allow Unicode escapes in evaluated expressions for security reasons",
		)
	}

	for _, pat := range blockedPatterns {
		if pat.MatchString(expr) {
			return fmt.Errorf(
				"blocked expression: matches forbidden pattern %q. "+
					"PEN does not allow this pattern for security reasons",
				pat.String(),
			)
		}
	}
	return nil
}

// --- Path traversal prevention ---

// ValidateSourcePath ensures a source path is within the project root.
// Prevents path traversal attacks via source map manipulation.
func ValidateSourcePath(projectRoot, requestedPath string) (string, error) {
	cleaned := filepath.Clean(requestedPath)

	if !filepath.IsAbs(cleaned) {
		cleaned = filepath.Join(projectRoot, cleaned)
	}

	// Resolve symlinks if the path exists.
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		// File may not exist — use cleaned path.
		resolved = cleaned
	}

	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("cannot resolve project root: %w", err)
	}

	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path: %w", err)
	}

	// Use filepath.Rel to safely check containment (handles case-insensitive
	// filesystems and mixed separators better than strings.HasPrefix).
	rel, err := filepath.Rel(absRoot, absResolved)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf(
			"path %q resolves outside project root %q — access denied",
			requestedPath, projectRoot,
		)
	}

	return absResolved, nil
}

// ValidateTempPath ensures a file is within PEN's temp directory.
func ValidateTempPath(path string) error {
	tempDir := filepath.Join(os.TempDir(), "pen")
	absTempDir, err := filepath.Abs(tempDir)
	if err != nil {
		return fmt.Errorf("cannot resolve temp directory: %w", err)
	}
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}
	rel, err := filepath.Rel(absTempDir, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path %q is not in PEN temp directory", path)
	}
	return nil
}

// --- CDP URL validation ---

// ValidateCDPURL ensures the URL is a localhost address.
// PEN only connects to local browsers for security.
func ValidateCDPURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid CDP URL: %w", err)
	}

	host := u.Hostname()
	allowed := map[string]bool{
		"localhost": true,
		"127.0.0.1": true,
		"::1":       true,
	}

	if !allowed[host] {
		return fmt.Errorf(
			"CDP connection to remote host %q is not allowed — "+
				"PEN only connects to localhost for security. "+
				"Use SSH tunneling to forward a remote debug port to localhost",
			host,
		)
	}
	return nil
}

// --- Temp file creation ---

// TempDir returns PEN's temp directory, creating it if needed (mode 0700).
func TempDir() (string, error) {
	dir := filepath.Join(os.TempDir(), "pen")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create PEN temp dir: %w", err)
	}
	return dir, nil
}

// CreateSecureTempFile creates a temp file with restricted permissions (0600).
func CreateSecureTempFile(prefix string) (*os.File, error) {
	dir, err := TempDir()
	if err != nil {
		return nil, err
	}

	f, err := os.CreateTemp(dir, prefix)
	if err != nil {
		return nil, err
	}

	if err := f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}

	return f, nil
}

// CleanTempFiles removes all PEN temp files. Returns bytes freed.
func CleanTempFiles() (int64, error) {
	dir := filepath.Join(os.TempDir(), "pen")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var freed int64
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err == nil {
			freed += info.Size()
		}
		os.Remove(path)
	}
	return freed, nil
}

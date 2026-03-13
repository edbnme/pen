// Package format provides output formatting helpers for tool results.
// All PEN tools return structured text optimized for LLM consumption.
package format

import (
	"fmt"
	"strings"
	"time"
)

// Table builds a Markdown table from headers and rows.
func Table(headers []string, rows [][]string) string {
	if len(headers) == 0 {
		return ""
	}

	// Compute column widths.
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i := 0; i < len(row) && i < len(headers); i++ {
			if len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}

	var b strings.Builder

	// Header row.
	b.WriteByte('|')
	for i, h := range headers {
		fmt.Fprintf(&b, " %-*s |", widths[i], h)
	}
	b.WriteByte('\n')

	// Separator row.
	b.WriteByte('|')
	for _, w := range widths {
		b.WriteString(strings.Repeat("-", w+2))
		b.WriteByte('|')
	}
	b.WriteByte('\n')

	// Data rows.
	for _, row := range rows {
		b.WriteByte('|')
		for i := 0; i < len(headers); i++ {
			val := ""
			if i < len(row) {
				val = row[i]
			}
			fmt.Fprintf(&b, " %-*s |", widths[i], val)
		}
		b.WriteByte('\n')
	}

	return b.String()
}

// Section creates a Markdown section header with joined content parts.
func Section(title string, parts ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n\n", title)
	for _, p := range parts {
		b.WriteString(p)
		b.WriteByte('\n')
	}
	return b.String()
}

// Bytes formats a byte count in human-readable form.
func Bytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// Duration formats a duration in a concise human-readable form.
func Duration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.1fm", d.Minutes())
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
}

// Percent formats a pre-computed percentage value as a string.
func Percent(pct float64) string {
	return fmt.Sprintf("%.1f%%", pct)
}

// BulletList creates a Markdown bullet list from items.
func BulletList(items []string) string {
	var b strings.Builder
	for _, item := range items {
		fmt.Fprintf(&b, "- %s\n", item)
	}
	return b.String()
}

// Warning formats a warning note.
func Warning(msg string) string {
	return fmt.Sprintf("⚠ **Warning**: %s\n", msg)
}

// KeyValue formats a key-value line for summaries.
func KeyValue(key, value string) string {
	return fmt.Sprintf("**%s**: %s", key, value)
}

// Summary builds a summary block with key-value pairs.
func Summary(pairs [][2]string) string {
	var b strings.Builder
	for _, p := range pairs {
		fmt.Fprintf(&b, "**%s**: %s | ", p[0], p[1])
	}
	s := b.String()
	if len(s) > 3 {
		s = s[:len(s)-3] // trim trailing " | "
	}
	return s
}

// ToolResult wraps formatted content into a standard tool response structure.
func ToolResult(title string, sections ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", title)
	for _, s := range sections {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	return b.String()
}

package format

import (
	"strings"
	"testing"
	"time"
)

func TestBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{10240, "10.0 KB"},
		{1048576, "1.0 MB"},
		{5242880, "5.0 MB"},
		{1073741824, "1.0 GB"},
		{2147483648, "2.0 GB"},
	}
	for _, tt := range tests {
		got := Bytes(tt.input)
		if got != tt.want {
			t.Errorf("Bytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDuration(t *testing.T) {
	tests := []struct {
		input time.Duration
		want  string
	}{
		{0, "0us"},
		{1 * time.Microsecond, "1us"},
		{500 * time.Microsecond, "500us"},
		{999 * time.Microsecond, "999us"},
		{1 * time.Millisecond, "1.0ms"},
		{15 * time.Millisecond, "15.0ms"},
		{999 * time.Millisecond, "999.0ms"},
		{1 * time.Second, "1.00s"},
		{2500 * time.Millisecond, "2.50s"},
		{59 * time.Second, "59.00s"},
		{60 * time.Second, "1.0m"},
		{90 * time.Second, "1.5m"},
		{2 * time.Hour, "2.0h"},
		{24 * time.Hour, "24.0h"},
	}
	for _, tt := range tests {
		got := Duration(tt.input)
		if got != tt.want {
			t.Errorf("Duration(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPercent(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0, "0.0%"},
		{42.5, "42.5%"},
		{100, "100.0%"},
		{99.99, "100.0%"},
		{0.1, "0.1%"},
	}
	for _, tt := range tests {
		got := Percent(tt.input)
		if got != tt.want {
			t.Errorf("Percent(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTable(t *testing.T) {
	headers := []string{"Name", "Value"}
	rows := [][]string{
		{"CPU", "4"},
		{"RAM", "16GB"},
	}
	result := Table(headers, rows)
	if !strings.Contains(result, "Name") || !strings.Contains(result, "CPU") {
		t.Errorf("Table output missing expected content: %s", result)
	}
	// Should have header, separator, and data rows.
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 4 {
		t.Errorf("Table should have 4 lines (header, separator, 2 data), got %d", len(lines))
	}
}

func TestTableEmpty(t *testing.T) {
	if result := Table(nil, nil); result != "" {
		t.Errorf("Table(nil, nil) should return empty string, got %q", result)
	}
}

func TestTableSingleColumn(t *testing.T) {
	result := Table([]string{"Item"}, [][]string{{"A"}, {"B"}, {"C"}})
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 5 {
		t.Errorf("expected 5 lines (header + sep + 3 data), got %d", len(lines))
	}
}

func TestTableMismatchedColumns(t *testing.T) {
	// More headers than row columns — should not panic.
	result := Table([]string{"A", "B", "C"}, [][]string{{"1"}})
	if !strings.Contains(result, "A") {
		t.Errorf("table should still render: %s", result)
	}
}

func TestTableNoRows(t *testing.T) {
	result := Table([]string{"H1", "H2"}, nil)
	lines := strings.Split(strings.TrimSpace(result), "\n")
	// Header + separator only.
	if len(lines) != 2 {
		t.Errorf("expected 2 lines (header + sep), got %d", len(lines))
	}
}

func TestBulletList(t *testing.T) {
	items := []string{"one", "two"}
	result := BulletList(items)
	if !strings.Contains(result, "- one") || !strings.Contains(result, "- two") {
		t.Errorf("BulletList missing items: %s", result)
	}
}

func TestBulletListEmpty(t *testing.T) {
	result := BulletList(nil)
	if result != "" {
		t.Errorf("BulletList(nil) = %q, want empty", result)
	}
}

func TestBulletListSingle(t *testing.T) {
	result := BulletList([]string{"only"})
	if result != "- only\n" {
		t.Errorf("BulletList([only]) = %q, want %q", result, "- only\n")
	}
}

func TestSection(t *testing.T) {
	result := Section("Title", "part1", "part2")
	if !strings.HasPrefix(result, "### Title\n") {
		t.Errorf("Section should start with ### header: %s", result)
	}
	if !strings.Contains(result, "part1") || !strings.Contains(result, "part2") {
		t.Errorf("Section missing parts: %s", result)
	}
}

func TestSectionNoParts(t *testing.T) {
	result := Section("Empty")
	if !strings.HasPrefix(result, "### Empty\n") {
		t.Errorf("Section with no parts should still have header: %s", result)
	}
}

func TestWarning(t *testing.T) {
	result := Warning("something broke")
	if !strings.Contains(result, "Warning") {
		t.Errorf("Warning should contain 'Warning': %s", result)
	}
	if !strings.Contains(result, "something broke") {
		t.Errorf("Warning should contain message: %s", result)
	}
}

func TestKeyValue(t *testing.T) {
	result := KeyValue("CPU", "Intel i9")
	if result != "**CPU**: Intel i9" {
		t.Errorf("KeyValue = %q, want %q", result, "**CPU**: Intel i9")
	}
}

func TestSummary(t *testing.T) {
	result := Summary([][2]string{
		{"Count", "5"},
		{"Size", "10MB"},
	})
	if !strings.Contains(result, "**Count**: 5") {
		t.Errorf("Summary missing Count pair: %s", result)
	}
	if !strings.Contains(result, "**Size**: 10MB") {
		t.Errorf("Summary missing Size pair: %s", result)
	}
	// Should not end with " | ".
	if strings.HasSuffix(result, " | ") {
		t.Errorf("Summary should not end with ' | ': %s", result)
	}
}

func TestSummaryEmpty(t *testing.T) {
	result := Summary(nil)
	if strings.Contains(result, "|") {
		t.Errorf("Summary(nil) should not contain pipe: %q", result)
	}
}

func TestSummarySingle(t *testing.T) {
	result := Summary([][2]string{{"K", "V"}})
	if result != "**K**: V" {
		t.Errorf("Summary single pair = %q, want %q", result, "**K**: V")
	}
}

func TestToolResult(t *testing.T) {
	result := ToolResult("Test Title", "body content")
	if !strings.HasPrefix(result, "## Test Title") {
		t.Errorf("ToolResult missing title prefix: %s", result)
	}
	if !strings.Contains(result, "body content") {
		t.Errorf("ToolResult missing body: %s", result)
	}
}

func TestToolResultMultipleSections(t *testing.T) {
	result := ToolResult("Report", "section1", "section2", "section3")
	if !strings.HasPrefix(result, "## Report") {
		t.Error("missing title")
	}
	for _, s := range []string{"section1", "section2", "section3"} {
		if !strings.Contains(result, s) {
			t.Errorf("missing section %q", s)
		}
	}
}

func TestToolResultNoSections(t *testing.T) {
	result := ToolResult("Empty Report")
	if !strings.HasPrefix(result, "## Empty Report") {
		t.Error("missing title even with no sections")
	}
}

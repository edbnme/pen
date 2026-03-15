package main

import "testing"

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

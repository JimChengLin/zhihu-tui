package display

import (
	"strings"
	"testing"
)

func TestStripHTML(t *testing.T) {
	tests := map[string]string{
		"<p>hello</p>":                    "hello",
		"<div><b>bold</b> text</div>":     "bold text",
		"a &amp; b":                       "a & b",
		"<a href='#'>click &gt; here</a>": "click > here",
		"  <p> padded </p>  ":             "padded",
		"line1<br/>line2":                 "line1line2",
	}
	for in, want := range tests {
		if got := StripHTML(in); got != want {
			t.Fatalf("StripHTML(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestFormatCount(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{42, "42"},
		{0, "0"},
		{12345, "1.2万"},
		{10000, "1.0万"},
		{99999, "10.0万"},
		{100_000_000, "1.0亿"},
		{"50000", "5.0万"},
		{"abc", "abc"},
	}
	for _, tt := range tests {
		if got := FormatCount(tt.in); got != tt.want {
			t.Fatalf("FormatCount(%v)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("hello", 10); got != "hello" {
		t.Fatalf("short text changed: %q", got)
	}
	if got := Truncate("hello world", 6); got != "hello…" {
		t.Fatalf("unexpected truncate: %q", got)
	}
	if got := Truncate("line1\nline2", 50); got != "line1 line2" {
		t.Fatalf("newlines not replaced: %q", got)
	}
}

func TestStatsLine(t *testing.T) {
	got := StatsLine(map[string]any{"Answers": 42, "Followers": 100})
	if got == "" || !strings.Contains(got, "Answers") || !strings.Contains(got, "Followers") || !strings.Contains(got, "▸") {
		t.Fatalf("unexpected stats line: %q", got)
	}
}

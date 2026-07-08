package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCookieString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{name: "basic", in: "z_c0=abc; _xsrf=xyz", want: map[string]string{"z_c0": "abc", "_xsrf": "xyz"}},
		{name: "empty", in: "", want: map[string]string{}},
		{name: "spaces", in: "  a = 1 ;  b = 2  ", want: map[string]string{"a": "1", "b": "2"}},
		{name: "equals in value", in: "z_c0=abc=def=ghi", want: map[string]string{"z_c0": "abc=def=ghi"}},
		{name: "ignores malformed", in: "z_c0=abc; invalid; d_c0=xyz", want: map[string]string{"z_c0": "abc", "d_c0": "xyz"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseCookieString(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("len=%d, want %d: %#v", len(got), len(tt.want), got)
			}
			for key, want := range tt.want {
				if got[key] != want {
					t.Fatalf("%s=%q, want %q", key, got[key], want)
				}
			}
		})
	}
}

func TestCookieString(t *testing.T) {
	got := CookieString(map[string]string{"z_c0": "abc", "_xsrf": "xyz"})
	if !strings.Contains(got, "z_c0=abc") || !strings.Contains(got, "_xsrf=xyz") || !strings.Contains(got, "; ") {
		t.Fatalf("unexpected cookie string: %q", got)
	}
	if CookieString(map[string]string{}) != "" {
		t.Fatal("empty cookie map should format as empty string")
	}
}

func TestHasRequiredCookies(t *testing.T) {
	if !HasRequiredCookies(map[string]string{"z_c0": "abc", "_xsrf": "x", "d_c0": "d"}) {
		t.Fatal("expected required cookies to be present")
	}
	if HasRequiredCookies(map[string]string{"_xsrf": "x", "d_c0": "d"}) {
		t.Fatal("missing z_c0 should fail")
	}
}

func TestSaveLoadAndClearCookies(t *testing.T) {
	t.Setenv("ZHIHU_CLI_HOME", t.TempDir())
	if err := SaveCookies("z_c0=test_token; _xsrf=xsrf_val; d_c0=dc0_val"); err != nil {
		t.Fatalf("save cookies: %v", err)
	}
	cookieStr, ok, err := GetSavedCookieString()
	if err != nil {
		t.Fatalf("load cookies: %v", err)
	}
	if !ok || !strings.Contains(cookieStr, "z_c0=test_token") {
		t.Fatalf("unexpected loaded cookie: ok=%v %q", ok, cookieStr)
	}
	removed, err := ClearCookies()
	if err != nil {
		t.Fatalf("clear cookies: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed %d files, want 1", len(removed))
	}
	_, ok, err = GetSavedCookieString()
	if err != nil {
		t.Fatalf("load after clear: %v", err)
	}
	if ok {
		t.Fatal("cookie should not exist after clear")
	}
}

func TestLoadCookiesFailsForInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZHIHU_CLI_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "cookies.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := GetSavedCookieString(); err == nil {
		t.Fatal("invalid JSON should return an error")
	}
}

func TestLoadCookiesFailsWhenRequiredCookiesMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZHIHU_CLI_HOME", dir)
	data, err := json.Marshal(cookieFile{Cookies: map[string]string{"_xsrf": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cookies.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := GetSavedCookieString(); err == nil {
		t.Fatal("missing required cookies should return an error")
	}
}

func TestRenderQRHalfBlocks(t *testing.T) {
	if RenderQRHalfBlocks(nil) != "" {
		t.Fatal("empty matrix should render empty")
	}
	got := RenderQRHalfBlocks([][]bool{{true, false}, {false, true}})
	if got == "" {
		t.Fatal("non-empty matrix should render content")
	}
	for _, ch := range got {
		if ch != '\n' && ch != ' ' && ch != '▀' && ch != '▄' && ch != '█' {
			t.Fatalf("unexpected QR char %q", ch)
		}
	}
}

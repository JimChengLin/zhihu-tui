package display

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

var tagPattern = regexp.MustCompile(`<[^>]+>`)
var invisibleSpanPattern = regexp.MustCompile(`(?is)<span\b[^>]*\bclass\s*=\s*(?:"(?:[^"]*\s)?invisible(?:\s[^"]*)?"|'(?:[^']*\s)?invisible(?:\s[^']*)?')[^>]*>.*?</span\s*>`)

func StripHTML(text string) string {
	if text == "" {
		return ""
	}
	clean := invisibleSpanPattern.ReplaceAllString(text, "")
	clean = tagPattern.ReplaceAllString(clean, "")
	return strings.TrimSpace(html.UnescapeString(clean))
}

func FormatCount(value any) string {
	n, ok := int64Value(value)
	if !ok {
		return fmt.Sprint(value)
	}
	switch {
	case n >= 100_000_000:
		return fmt.Sprintf("%.1f亿", float64(n)/100_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%.1f万", float64(n)/10_000)
	default:
		return strconv.FormatInt(n, 10)
	}
}

func Truncate(text string, maxLen int) string {
	if text == "" {
		return ""
	}
	if maxLen <= 0 {
		return ""
	}
	text = strings.ReplaceAll(text, "\n", " ")
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	if maxLen == 1 {
		return "…"
	}
	return string(runes[:maxLen-1]) + "…"
}

func StatsLine(pairs map[string]any) string {
	if len(pairs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(pairs))
	for label, value := range pairs {
		parts = append(parts, "▸ "+FormatCount(value)+" "+label)
	}
	return strings.Join(parts, "  ")
}

func Success(msg string) string {
	return "OK: " + msg
}

func Error(msg string) string {
	return "ERROR: " + msg
}

func Warning(msg string) string {
	return "WARN: " + msg
}

func Info(msg string) string {
	return "INFO: " + msg
}

func ToPrettyJSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func int64Value(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(v), true
	case float64:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

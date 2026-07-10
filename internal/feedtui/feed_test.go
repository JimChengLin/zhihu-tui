package feedtui

import (
	"bufio"
	"strings"
	"testing"
)

func TestParseFeedItemFormatsFollowingActivity(t *testing.T) {
	raw := map[string]any{
		"id":           "activity-1",
		"action_text":  `<a href="/people/alice">Alice</a>赞同了回答`,
		"created_time": 1_700_000_000,
		"target": map[string]any{
			"id":            "456",
			"type":          "answer",
			"content":       `<p>第一段。</p><p>第二段。<img src="x.jpg"></p>`,
			"voteup_count":  12000,
			"comment_count": 7,
			"author": map[string]any{
				"name":     "Bob",
				"headline": "第一行\n第二行",
			},
			"question": map[string]any{
				"id":    "123",
				"title": "测试问题",
			},
		},
	}

	item, ok := parseFeedItem(raw)
	if !ok {
		t.Fatal("parseFeedItem returned false")
	}
	if item.action != "Alice 赞同了回答" {
		t.Fatalf("action=%q", item.action)
	}
	if item.title != "测试问题" {
		t.Fatalf("title=%q", item.title)
	}
	if item.body != "第一段。\n\n第二段。" {
		t.Fatalf("body=%q", item.body)
	}
	if item.headline != "第一行 第二行" {
		t.Fatalf("headline=%q", item.headline)
	}
	if item.stats != "赞同 1.2万  ·  评论 7" {
		t.Fatalf("stats=%q", item.stats)
	}
	if item.url != "https://www.zhihu.com/question/123/answer/456" {
		t.Fatalf("url=%q", item.url)
	}
	if !item.hasImage {
		t.Fatal("hasImage=false")
	}
}

func TestWrapTextKeepsClosingPunctuationOnPreviousLine(t *testing.T) {
	lines := wrapText("中文中文。下一句", 8)
	if len(lines) < 2 {
		t.Fatalf("lines=%q", lines)
	}
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "。") {
			t.Fatalf("closing punctuation started a line: %q", lines)
		}
	}
}

func TestWrapTextDoesNotSplitShortASCIITokens(t *testing.T) {
	lines := wrapText("这是一个 Zig community 和 100% 测试", 18)
	for _, line := range lines {
		if line == "Z" || strings.HasSuffix(line, " 10") || strings.HasPrefix(line, "0%") {
			t.Fatalf("ASCII token was split: %q", lines)
		}
	}
}

func TestReadKeyRecognizesNavigationSequences(t *testing.T) {
	tests := []struct {
		input string
		want  keyEvent
	}{
		{"j", "j"},
		{"\x1b[A", keyUp},
		{"\x1b[B", keyDown},
		{"\x1b[5~", keyPageUp},
		{"\x1b[6~", keyPageDown},
		{"\x03", keyCtrlC},
	}
	for _, test := range tests {
		got, err := readKey(bufio.NewReader(strings.NewReader(test.input)))
		if err != nil {
			t.Fatalf("readKey(%q): %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("readKey(%q)=%q, want %q", test.input, got, test.want)
		}
	}
}

func TestApplyFetchDeduplicatesOverlappingPages(t *testing.T) {
	model := &app{generation: 1, loading: true}
	response := map[string]any{
		"data": []any{
			feedTestRaw("1", "问题一"),
			feedTestRaw("1", "问题一"),
			feedTestRaw("2", "问题二"),
		},
		"paging": map[string]any{
			"is_end": false,
			"next":   "https://www.zhihu.com/api/v3/moments?after_id=2",
		},
	}
	model.applyFetch(fetchResult{response: response, reset: true, generation: 1})
	if len(model.items) != 2 {
		t.Fatalf("items=%d, want 2", len(model.items))
	}
	if model.nextURL == "" || model.end {
		t.Fatalf("nextURL=%q end=%v", model.nextURL, model.end)
	}
}

func feedTestRaw(id, title string) map[string]any {
	return map[string]any{
		"id": "activity-" + id,
		"target": map[string]any{
			"id":      id,
			"type":    "answer",
			"content": "正文",
			"question": map[string]any{
				"id":    "question-" + id,
				"title": title,
			},
		},
	}
}

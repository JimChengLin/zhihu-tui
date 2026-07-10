package feedtui

import (
	"bufio"
	"regexp"
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
	if item.body != "第一段。\n\n第二段。\n▣ 图片 1" {
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
	if item.imageCount != 1 {
		t.Fatalf("imageCount=%d", item.imageCount)
	}
}

func TestParseFeedItemFormatsStructuredPinContent(t *testing.T) {
	raw := map[string]any{
		"id":          "activity-pin",
		"action_text": "Alice 发布了想法",
		"target": map[string]any{
			"id":   "789",
			"type": "pin",
			"content": []any{
				map[string]any{"type": "text", "content": "想法标题\n\n想法正文"},
				map[string]any{"type": "image", "url": "https://example.com/image.jpg"},
			},
			"author": map[string]any{"name": "Alice"},
		},
	}

	item, ok := parseFeedItem(raw)
	if !ok {
		t.Fatal("parseFeedItem returned false")
	}
	if item.title != "想法标题" {
		t.Fatalf("title=%q", item.title)
	}
	if item.body != "想法正文\n\n▣ 图片 1" {
		t.Fatalf("body=%q", item.body)
	}
	if item.imageCount != 1 {
		t.Fatalf("imageCount=%d", item.imageCount)
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

func TestRenderAppUsesResponsiveWideLayout(t *testing.T) {
	items := make([]feedItem, 20)
	for index := range items {
		items[index] = feedItem{
			key:    toString(index),
			kind:   "answer",
			action: "某人赞同了回答",
			title:  "第 " + toString(index+1) + " 条关注动态",
			author: "答主",
			body:   "这是一段用于验证宽屏响应式布局的正文。",
		}
	}
	model := &app{items: items, index: 10, width: 160, height: 32}
	lines, metrics := renderApp(model)
	if !lines[0].raw {
		t.Fatal("wide layout did not merge independently styled columns")
	}
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(rendered, "关注动态") || !strings.Contains(rendered, "第 11 条关注动态") {
		t.Fatalf("wide layout is missing sidebar or current item: %q", rendered)
	}
	if metrics.bodyHeight <= 0 {
		t.Fatalf("bodyHeight=%d", metrics.bodyHeight)
	}
	ansiPattern := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for row, line := range lines {
		plain := ansiPattern.ReplaceAllString(line.text, "")
		if width := stringCellWidth(plain); width >= model.width {
			t.Fatalf("row %d width=%d, terminal width=%d", row, width, model.width)
		}
	}

	model.width = 100
	lines, _ = renderApp(model)
	if lines[0].raw {
		t.Fatal("compact layout unexpectedly rendered columns")
	}

	model.width, model.height = 220, 70
	lines, _ = renderApp(model)
	statusRow := -1
	for row, line := range lines {
		if strings.Contains(line.text, "第 11 / 20 条") {
			statusRow = row
			break
		}
	}
	if statusRow < 0 || statusRow >= model.height/2 {
		t.Fatalf("short-content status row=%d, want it near the content in a tall terminal", statusRow)
	}

	model.width, model.height = 160, 32
	model.zenMode = true
	model.items[10].body = strings.Repeat("中", 100)
	lines, metrics = renderApp(model)
	if lines[0].raw {
		t.Fatal("zen mode unexpectedly rendered the sidebar")
	}
	if metrics.bodyLines != 2 {
		t.Fatalf("zen bodyLines=%d, want 2 with an adaptive reading width", metrics.bodyLines)
	}
	if width := adaptiveReadingWidth(160); width != maxReadingWidth {
		t.Fatalf("adaptiveReadingWidth(160)=%d, want %d", width, maxReadingWidth)
	}
	if width := adaptiveReadingWidth(100); width != 94 {
		t.Fatalf("adaptiveReadingWidth(100)=%d, want 94", width)
	}

	model.zenMode = false
	sidebar := renderSidebar(model, 40)
	if sidebar[5].text != "" {
		t.Fatalf("sidebar items do not have a spacer row: %#v", sidebar[5])
	}
}

func TestAddParagraphSpacingPreservesAuthorLayout(t *testing.T) {
	lines := []string{"第一段第一行", "第一段第二行", "", "第二段"}
	want := []string{"第一段第一行", "第一段第二行", "", "", "第二段"}
	got := addParagraphSpacing(lines)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("addParagraphSpacing()=%q, want %q", got, want)
	}
}

func styledLineTexts(lines []styledLine) []string {
	texts := make([]string, len(lines))
	for index := range lines {
		texts[index] = lines[index].text
	}
	return texts
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

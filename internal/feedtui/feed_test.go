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

func TestRefreshMarksNewAndPreviouslyViewedRangeAfterSuccess(t *testing.T) {
	model := &app{
		generation: 1,
		items: []feedItem{
			{key: "answer:1", title: "问题一", action: "甲赞同了回答"},
			{key: "answer:2", title: "问题二", action: "乙赞同了回答"},
			{key: "answer:3", title: "问题三", action: "丙赞同了回答"},
		},
		height: 24,
	}
	model.index = 0
	model.markCurrentViewed()
	model.index = 1
	model.markCurrentViewed()
	model.captureRefreshBoundary()
	if model.lastReadTopKey != "" || model.lastReadBottomKey != "" {
		t.Fatalf("last-read range=(%q, %q) before refresh finishes", model.lastReadTopKey, model.lastReadBottomKey)
	}
	if model.pendingReadTopKey != "answer:1" || model.pendingReadBottomKey != "answer:2" {
		t.Fatalf("pending range=(%q, %q), want previous first and last viewed items", model.pendingReadTopKey, model.pendingReadBottomKey)
	}
	if len(model.newItemKeys) != 0 {
		t.Fatalf("newItemKeys=%v before refresh finishes", model.newItemKeys)
	}

	response := map[string]any{
		"data": []any{
			feedTestRaw("new", "新问题"),
			feedTestRaw("1", "问题一"),
			feedTestRaw("2", "问题二"),
			feedTestRaw("3", "问题三"),
		},
		"paging": map[string]any{"is_end": true},
	}
	model.generation++
	model.applyFetch(fetchResult{response: response, reset: true, generation: model.generation})

	if model.lastReadTopKey != "answer:1" || model.lastReadBottomKey != "answer:2" {
		t.Fatalf("last-read range=(%q, %q), want previous first and last viewed items", model.lastReadTopKey, model.lastReadBottomKey)
	}
	if model.pendingReadTopKey != "" || model.pendingReadBottomKey != "" {
		t.Fatalf("pending range=(%q, %q) after refresh finishes", model.pendingReadTopKey, model.pendingReadBottomKey)
	}
	if _, ok := model.newItemKeys["answer:new"]; !ok || len(model.newItemKeys) != 1 {
		t.Fatalf("newItemKeys=%v, want only the item before the previous first item", model.newItemKeys)
	}

	sidebar := renderSidebar(model, 40)
	if sidebar[3].text != "› 新问题" {
		t.Fatalf("first sidebar title=%q, want no numeric prefix", sidebar[3].text)
	}
	if !strings.Contains(sidebar[1].text, "NEW 1") || !strings.HasPrefix(sidebar[4].text, "  NEW · ") {
		t.Fatalf("sidebar is missing the NEW marker: %#v", sidebar)
	}
	if !strings.HasPrefix(sidebar[7].text, "  上次读到↓ · ") {
		t.Fatalf("last-read top summary=%q", sidebar[7].text)
	}
	if !strings.HasPrefix(sidebar[10].text, "  上次读到↑ · ") {
		t.Fatalf("last-read bottom summary=%q", sidebar[10].text)
	}
	if strings.Contains(sidebar[13].text, "上次读到") {
		t.Fatalf("unread prefetched item was marked as viewed: %q", sidebar[13].text)
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
	if strings.Contains(rendered, "知乎关注 · 回答 · 已加载 20 条") {
		t.Fatalf("wide main pane repeats the feed header: %q", rendered)
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
	if rendered := strings.Join(styledLineTexts(lines), "\n"); !strings.Contains(rendered, "第 11 / 20 条") {
		t.Fatalf("compact layout is missing the item position: %q", rendered)
	}
	if rendered := strings.Join(styledLineTexts(lines), "\n"); !strings.Contains(rendered, "知乎关注 · 回答 · 已加载 20 条") {
		t.Fatalf("compact layout is missing the feed header: %q", rendered)
	}

	model.width, model.height = 220, 70
	lines, _ = renderApp(model)
	rendered = strings.Join(styledLineTexts(lines), "\n")
	if strings.Contains(rendered, "第 11 / 20 条") {
		t.Fatalf("wide main pane repeats the sidebar item position: %q", rendered)
	}
	hintsRow := -1
	for row, line := range lines {
		if strings.Contains(line.text, "j/k 滚动") {
			hintsRow = row
			break
		}
	}
	if hintsRow != model.height-2 {
		t.Fatalf("short-content hints row=%d, want it pinned at row %d", hintsRow, model.height-2)
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
	for _, line := range sidebar {
		if strings.Contains(line.text, "后面还有") {
			t.Fatalf("sidebar contains a finite-list tail hint: %q", line.text)
		}
	}
}

func TestSidebarSelectionStaysPutWhenNextPageArrives(t *testing.T) {
	items := make([]feedItem, 10)
	for index := range items {
		items[index] = feedItem{key: toString(index), title: "动态 " + toString(index+1)}
	}
	model := &app{items: items, index: 7, height: 32}
	before := selectedSidebarRow(renderSidebar(model, 40))
	if before < 0 {
		t.Fatal("selected item is missing before pagination")
	}

	for index := 10; index < 20; index++ {
		model.items = append(model.items, feedItem{key: toString(index), title: "动态 " + toString(index+1)})
	}
	after := selectedSidebarRow(renderSidebar(model, 40))
	if before != after {
		t.Fatalf("selected sidebar row moved from %d to %d after pagination", before, after)
	}
}

func selectedSidebarRow(lines []styledLine) int {
	for row, line := range lines {
		if strings.HasPrefix(line.text, "› ") {
			return row
		}
	}
	return -1
}

func TestAddParagraphSpacingPreservesAuthorLayout(t *testing.T) {
	lines := []string{"第一段第一行", "第一段第二行", "", "第二段"}
	want := []string{"第一段第一行", "第一段第二行", "", "", "第二段"}
	got := addParagraphSpacing(lines)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("addParagraphSpacing()=%q, want %q", got, want)
	}
}

func TestReadingHeaderDoesNotRepeatImageCount(t *testing.T) {
	model := &app{
		items: []feedItem{{
			kind:       "answer",
			action:     "某人赞同了回答",
			title:      "测试问题",
			author:     "答主",
			stats:      "赞同 12  ·  评论 3",
			body:       "正文",
			imageCount: 2,
		}},
		width:  100,
		height: 24,
	}
	lines, _ := renderSingleApp(model)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if strings.Contains(rendered, "图片 2") {
		t.Fatalf("reading header repeats the image count: %q", rendered)
	}
}

func TestLongBodyScrollbarTracksReadingPosition(t *testing.T) {
	model := &app{
		items: []feedItem{{
			kind:   "answer",
			action: "某人赞同了回答",
			title:  "长文",
			author: "答主",
			body:   strings.Repeat("这是一段用于测试长文滚动位置的正文。", 80),
		}},
		width:  100,
		height: 24,
	}
	lines, metrics := renderSingleApp(model)
	bar := scrollbarLines(lines)
	if len(bar) != metrics.bodyHeight {
		t.Fatalf("scrollbar height=%d, want body height %d", len(bar), metrics.bodyHeight)
	}
	if bar[0].suffix != "┃" {
		t.Fatalf("top scrollbar does not start with the thumb: %q", bar[0].suffix)
	}
	if bar[0].suffixStyle != ansiDim {
		t.Fatalf("scrollbar style=%q, want dim", bar[0].suffixStyle)
	}
	var output strings.Builder
	if err := writeFrame(&output, []styledLine{bar[0]}, model.width, 1); err != nil {
		t.Fatalf("writeFrame(): %v", err)
	}
	if !strings.Contains(output.String(), ansiDim+"┃"+ansiReset) {
		t.Fatalf("rendered scrollbar is not dim: %q", output.String())
	}

	model.scroll = metrics.maxScroll
	lines, _ = renderSingleApp(model)
	bar = scrollbarLines(lines)
	if bar[len(bar)-1].suffix != "┃" {
		t.Fatalf("bottom scrollbar does not end with the thumb: %q", bar[len(bar)-1].suffix)
	}
}

func scrollbarLines(lines []styledLine) []styledLine {
	var result []styledLine
	for _, line := range lines {
		if line.suffix == "┊" || line.suffix == "┃" {
			result = append(result, line)
		}
	}
	return result
}

func styledLineTexts(lines []styledLine) []string {
	texts := make([]string, len(lines))
	for index := range lines {
		texts[index] = lines[index].text + lines[index].suffix
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

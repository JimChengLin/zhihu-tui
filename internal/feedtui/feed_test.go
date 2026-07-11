package feedtui

import (
	"bufio"
	"context"
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
	if item.key != "answer:456:Alice 赞同了回答" {
		t.Fatalf("key=%q, want stable target and action identity", item.key)
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

func TestFeedItemKeyIgnoresVolatileActivityID(t *testing.T) {
	activity := func(activityID, actor string) map[string]any {
		return map[string]any{
			"id":          activityID,
			"action_text": actor + "赞同了回答",
			"target": map[string]any{
				"id":      "same-answer",
				"type":    "answer",
				"content": "正文",
				"question": map[string]any{
					"id":    "question",
					"title": "同一个问题",
				},
			},
		}
	}
	first, _ := parseFeedItem(activity("volatile-a", "Alice"))
	refreshed, _ := parseFeedItem(activity("volatile-b", "Alice"))
	otherActor, _ := parseFeedItem(activity("volatile-c", "Bob"))
	if first.key != refreshed.key {
		t.Fatalf("same feed changed key across refresh: %q != %q", first.key, refreshed.key)
	}
	if first.key == otherActor.key {
		t.Fatalf("different actors for the same target share key %q", first.key)
	}
}

func TestParseFeedItemsExpandsServerFoldedGroup(t *testing.T) {
	visible := feedTestRaw("visible", "同一个问题")
	visible["target"].(map[string]any)["id"] = "same-answer"
	folded := feedTestRaw("folded", "同一个问题")
	folded["target"].(map[string]any)["id"] = "same-answer"
	folded["action_text"] = "另一位用户赞同了回答"

	items := parseFeedItems([]any{
		visible,
		map[string]any{
			"id":         "group-1",
			"group_text": "还有 1 个动态被收起",
			"style_type": 0,
			"list":       []any{folded},
		},
	})
	if len(items) != 2 {
		t.Fatalf("items=%#v, want visible activity and collapsed group", items)
	}
	if len(items[1].foldedItems) != 1 || items[1].groupOpen {
		t.Fatalf("group=%#v, want one child collapsed by default", items[1])
	}
	child := items[1].foldedItems[0]
	if items[0].key == child.key {
		t.Fatalf("different activities for the same target share key %q", items[0].key)
	}
	if !child.serverFolded || child.action != "另一位用户 赞同了回答" {
		t.Fatalf("folded child=%#v, want complete activity with source marker", child)
	}

	model := &app{items: items, height: 20}
	sidebar := renderSidebar(model, 48)
	if !strings.Contains(sidebar[6].text, "▸ 还有 1 个动态被收起") || !strings.Contains(sidebar[7].text, "e/Enter 展开") {
		t.Fatalf("collapsed group has no disclosure control: %#v %#v", sidebar[6], sidebar[7])
	}
	model.index = 1
	model.width = 100
	groupLines, _ := renderSingleApp(model)
	groupPreview := strings.Join(styledLineTexts(groupLines), "\n")
	if !strings.Contains(groupPreview, "同一个问题") || !strings.Contains(groupPreview, "另一位用户 赞同了 匿名用户 的回答") || !strings.Contains(groupPreview, "  正文") {
		t.Fatalf("collapsed group has no useful content preview: %q", groupPreview)
	}
	for _, line := range groupLines {
		if strings.Contains(line.text, "还有 1 个动态被收起") && line.style != ansiDim {
			t.Fatalf("folded group title style=%q, want neutral dim text", line.style)
		}
		if strings.Contains(line.text, "同一个问题") && line.style != ansiBlue {
			t.Fatalf("folded child title style=%q, want content-title blue", line.style)
		}
	}
	if strings.Contains(groupPreview, "知乎收起\n") || strings.Contains(groupPreview, "展开到左栏") {
		t.Fatalf("collapsed group still renders redundant labels or instructions: %q", groupPreview)
	}
	if !model.toggleFoldedGroup() || len(model.items) != 3 || !model.items[1].groupOpen {
		t.Fatalf("group did not expand: %#v", model.items)
	}
	sidebar = renderSidebar(model, 48)
	if !strings.Contains(sidebar[6].text, "▾ 还有 1 个动态被收起") || sidebar[9].text != "      同一个问题" || sidebar[10].text != "      另一位用户 赞同了回答" {
		t.Fatalf("expanded group or child is not identified: %#v", sidebar)
	}
	model.index = 2
	model.width = 100
	lines, _ := renderSingleApp(model)
	if !strings.Contains(strings.Join(styledLineTexts(lines), "\n"), "知乎收起 · 另一位用户 赞同了回答") {
		t.Fatal("folded activity is not identified in the reading pane")
	}
	if !model.toggleFoldedGroup() || len(model.items) != 2 || model.index != 1 || model.items[1].groupOpen {
		t.Fatalf("group did not collapse from its child: index=%d items=%#v", model.index, model.items)
	}
}

func TestFoldedPreviewMarksOnlyTruncatedExcerpts(t *testing.T) {
	lines := layoutFoldedGroupPreview([]feedItem{
		{kind: "answer", title: "短回答", author: "甲", action: "某人赞同了回答", body: "令人感叹"},
		{kind: "answer", title: "多段回答", author: "乙", action: "某人赞同了回答", body: "第一段\n\n第二段"},
	}, 40)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(rendered, "  令人感叹") || strings.Contains(rendered, "令人感叹…") {
		t.Fatalf("complete short answer has an unnecessary marker: %q", rendered)
	}
	if !strings.Contains(rendered, "第一段…") {
		t.Fatalf("multi-paragraph answer was not marked truncated: %q", rendered)
	}
	if strings.Contains(rendered, "（全文）") {
		t.Fatalf("folded preview still renders the redundant full-text marker: %q", rendered)
	}
	for _, line := range lines {
		if strings.Contains(line.text, "令人感叹") && line.style != "" {
			t.Fatalf("folded answer excerpt style=%q, want normal body text", line.style)
		}
	}
}

func TestFoldedPreviewDescribesAnswerActorAndAuthor(t *testing.T) {
	lines := layoutFoldedGroupPreview([]feedItem{
		{kind: "answer", title: "问题", author: "好好睡觉", action: "codedump 赞同了回答", body: "回答摘要"},
	}, 60)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(rendered, "codedump 赞同了 好好睡觉 的回答") {
		t.Fatalf("answer activity relationship is unclear: %q", rendered)
	}
	if strings.Contains(rendered, "答主 好好睡觉") {
		t.Fatalf("answer activity still uses the old role-first copy: %q", rendered)
	}
}

func TestFoldedPreviewLabelsContentAuthorRoles(t *testing.T) {
	lines := layoutFoldedGroupPreview([]feedItem{
		{kind: "question", title: "航天问题", author: "不方的圆", action: "codedump 关注了问题", body: "问题详情"},
		{kind: "article", title: "文章", author: "作者甲", action: "某人赞同了文章", body: "文章摘要"},
	}, 60)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(rendered, "codedump 关注了 不方的圆 的问题") {
		t.Fatalf("question author role is ambiguous: %q", rendered)
	}
	if !strings.Contains(rendered, "作者 作者甲 · 某人赞同了文章") {
		t.Fatalf("article author role is ambiguous: %q", rendered)
	}
}

func TestFoldedGroupKeyIgnoresVolatileRequestSegment(t *testing.T) {
	first := stableFoldedGroupKey("0_1783736483078040247_1783736159954_4")
	refreshed := stableFoldedGroupKey("0_1783736484122108502_1783736159954_4")
	otherGroup := stableFoldedGroupKey("0_1783736484141440881_1783734050973_4")
	if first != refreshed {
		t.Fatalf("same folded group changed key across refresh: %q != %q", first, refreshed)
	}
	if first == otherGroup {
		t.Fatalf("different folded groups share key %q", first)
	}
}

func TestFoldedGroupRefreshDoesNotDuplicateOrBlockReadBottom(t *testing.T) {
	groupRaw := func(volatile string) map[string]any {
		return map[string]any{
			"id":         "0_" + volatile + "_1783736159954_4",
			"group_text": "还有 {LIST_COUNT} 个用户的动态被收起",
			"list":       []any{feedTestRaw("folded-child", "组内动态")},
		}
	}
	response := func(volatile string) map[string]any {
		return map[string]any{
			"data": []any{
				feedTestRaw("top", "顶部动态"),
				groupRaw(volatile),
				feedTestRaw("after-group", "组后动态"),
			},
			"paging": map[string]any{"is_end": true},
		}
	}

	model := &app{generation: 1, items: parseFeedItems(asSlice(response("request-a")["data"])), height: 20}
	model.firstViewedKey = model.items[0].key
	model.furthestViewedKey = model.items[1].key
	model.captureRefreshBoundary()
	model.generation++
	model.applyFetch(fetchResult{response: response("request-b"), reset: true, generation: model.generation})
	if len(model.items) != 3 {
		t.Fatalf("refresh accumulated duplicate folded group: %#v", model.items)
	}
	model.index = 2
	model.markCurrentViewed()
	if model.furthestViewedKey != "answer:after-group" {
		t.Fatalf("furthestViewedKey=%q, want item after folded group", model.furthestViewedKey)
	}
	model.captureRefreshBoundary()
	model.generation++
	model.applyFetch(fetchResult{response: response("request-c"), reset: true, generation: model.generation})
	if model.lastReadBottomKey != "answer:after-group" {
		t.Fatalf("lastReadBottomKey=%q, want read boundary after folded group", model.lastReadBottomKey)
	}
	if len(model.items) != 3 {
		t.Fatalf("second refresh accumulated duplicate folded group: %#v", model.items)
	}
}

func TestCollapsedGroupInheritsAndExpandedGroupDistributesReadState(t *testing.T) {
	group := feedItem{
		key:         "group",
		title:       "还有 2 条动态被收起",
		action:      "知乎关注流收起的动态",
		foldedItems: []feedItem{{key: "child-top", title: "子项一"}, {key: "child-bottom", title: "子项二"}},
	}
	for index := range group.foldedItems {
		group.foldedItems[index].foldedParent = group.key
	}
	model := &app{
		items:             []feedItem{{key: "normal", title: "普通动态"}, group},
		index:             0,
		height:            20,
		lastReadTopKey:    "child-top",
		lastReadBottomKey: "child-bottom",
		newItemKeys:       map[string]struct{}{"child-top": {}},
	}

	sidebar := renderSidebar(model, 48)
	if !strings.HasPrefix(sidebar[7].text, "  上次读到↓↑ · ") || sidebar[6].style != ansiCyan {
		t.Fatalf("collapsed group did not inherit child boundaries over NEW: %#v %#v", sidebar[6], sidebar[7])
	}

	model.index = 1
	if !model.toggleFoldedGroup() {
		t.Fatal("group did not expand")
	}
	sidebar = renderSidebar(model, 48)
	if strings.Contains(sidebar[7].text, "上次读到") {
		t.Fatalf("expanded group kept inherited boundary: %#v", sidebar[7])
	}
	if !strings.HasPrefix(sidebar[10].text, "      上次读到↓ · ") || !strings.HasPrefix(sidebar[13].text, "      上次读到↑ · ") {
		t.Fatalf("expanded children did not recover exact boundaries: %#v", sidebar)
	}
}

func TestCollapsedGroupInheritsNewStateUntilExpanded(t *testing.T) {
	group := feedItem{
		key:         "group",
		title:       "还有 1 条动态被收起",
		foldedItems: []feedItem{{key: "new-child", title: "新子项", foldedParent: "group"}},
	}
	model := &app{
		items:       []feedItem{{key: "normal", title: "普通动态"}, group},
		height:      20,
		newItemKeys: map[string]struct{}{"new-child": {}},
	}
	sidebar := renderSidebar(model, 48)
	if sidebar[6].style != ansiGreen {
		t.Fatalf("collapsed group style=%q, want inherited green", sidebar[6].style)
	}
	model.index = 1
	model.toggleFoldedGroup()
	model.index = 0
	sidebar = renderSidebar(model, 48)
	if sidebar[6].style != "" || sidebar[9].style != ansiGreen {
		t.Fatalf("expanded NEW state was not moved to child: group=%q child=%q", sidebar[6].style, sidebar[9].style)
	}
}

func TestRefreshTracksNewChildrenInsideExistingFoldedGroup(t *testing.T) {
	groupRaw := func(children ...map[string]any) map[string]any {
		list := make([]any, len(children))
		for index := range children {
			list[index] = children[index]
		}
		return map[string]any{
			"id":         "stable-group",
			"group_text": "还有 {LIST_COUNT} 条动态被收起",
			"list":       list,
		}
	}
	oldChild := feedTestRaw("old-child", "旧子项")
	model := &app{
		generation:           2,
		items:                parseFeedItems([]any{groupRaw(oldChild)}),
		pendingRefreshTopKey: "stable-group",
		height:               14,
	}
	model.applyFetch(fetchResult{
		reset:      true,
		generation: 2,
		response: map[string]any{
			"data":   []any{groupRaw(oldChild, feedTestRaw("new-child", "新子项"))},
			"paging": map[string]any{"is_end": true},
		},
	})
	if _, isNew := model.newItemKeys["answer:new-child"]; !isNew {
		t.Fatalf("new folded child was not tracked: %v", model.newItemKeys)
	}
	if _, isNew := model.newItemKeys["stable-group"]; isNew {
		t.Fatalf("existing group was incorrectly marked directly new: %v", model.newItemKeys)
	}
	sidebar := renderSidebar(model, 48)
	if sidebar[3].style != ansiBold+ansiGreen {
		t.Fatalf("collapsed group did not inherit refreshed child state: %#v", sidebar[3])
	}
}

func TestViewedRangeUsesFoldedChildrenLogicalOrder(t *testing.T) {
	group := feedItem{
		key: "group",
		foldedItems: []feedItem{
			{key: "child-one", foldedParent: "group"},
			{key: "child-two", foldedParent: "group"},
		},
	}
	model := &app{
		items:             []feedItem{group, {key: "tail"}},
		firstViewedKey:    "child-one",
		furthestViewedKey: "child-two",
	}
	model.index = 1
	model.markCurrentViewed()
	if model.furthestViewedKey != "tail" {
		t.Fatalf("furthestViewedKey=%q, hidden children blocked downward progress", model.furthestViewedKey)
	}
	model.index = 0
	model.markCurrentViewed()
	if model.firstViewedKey != "group" {
		t.Fatalf("firstViewedKey=%q, logical group order was ignored", model.firstViewedKey)
	}
}

func TestCodeBlockKeepsSemanticBoundaryAndFormatting(t *testing.T) {
	body, _ := contentText(`<p>前文。</p><pre><code class="language-text">2021-03-15 &gt;&gt;&gt; zig
  indented

done</code></pre><p>后文。</p>`)
	lines := layoutBodyLines(body, 40)

	want := []styledLine{
		{text: "前文。"},
		{},
		{},
		{text: "┌─ 代码", style: ansiCode},
		{text: "│ 2021-03-15 >>> zig", style: ansiCode},
		{text: "│   indented", style: ansiCode},
		{text: "│ ", style: ansiCode},
		{text: "│ done", style: ansiCode},
		{text: "└─", style: ansiCode},
		{},
		{},
		{text: "后文。"},
	}
	if len(lines) != len(want) {
		t.Fatalf("layoutBodyLines()=%#v, want %#v", lines, want)
	}
	for index := range want {
		if lines[index].text != want[index].text || lines[index].style != want[index].style {
			t.Fatalf("layoutBodyLines()[%d]=%#v, want %#v", index, lines[index], want[index])
		}
	}
}

func TestCodeBlockWrapsWithoutLosingWhitespaceOrGraphemes(t *testing.T) {
	body, _ := contentText(`<pre><code>  🇳🇱abcdef</code></pre>`)
	lines := layoutBodyLines(body, 8)
	want := []string{"┌─ 代码", "│   🇳🇱ab", "│ cdef", "└─"}
	if got := styledLineTexts(lines); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("layoutBodyLines()=%q, want %q", got, want)
	}
}

func TestStandaloneClassCodeIsRenderedAsBlock(t *testing.T) {
	body, _ := contentText(`<p>运行结果：</p><code class="language-text">first
second</code><p>普通 <code>inline()</code> 代码。</p>`)
	lines := layoutBodyLines(body, 40)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(rendered, "┌─ 代码\n│ first\n│ second\n└─") {
		t.Fatalf("class code was not rendered as a block: %q", rendered)
	}
	if !strings.Contains(rendered, "普通 inline() 代码。") {
		t.Fatalf("inline code was not kept in prose: %q", rendered)
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

func TestWrapTextMovesContentInsteadOfOverflowingForClosingPunctuation(t *testing.T) {
	text := "比肩cuBLAS的性能。"
	lines := wrapText(text, 16)
	if strings.Join(lines, "") != text {
		t.Fatalf("wrapped text changed content: %q", lines)
	}
	for _, line := range lines {
		if width := stringCellWidth(line); width > 16 {
			t.Fatalf("wrapped line width=%d exceeds 16 cells: %q", width, line)
		}
		if strings.Contains(line, "…") {
			t.Fatalf("wrapped line contains a synthetic ellipsis: %q", line)
		}
	}
	if len(lines) < 2 || lines[1] != "能。" {
		t.Fatalf("closing punctuation was not kept with preceding content: %q", lines)
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

func TestTextLayoutKeepsGraphemeClustersTogether(t *testing.T) {
	tests := []struct {
		name    string
		cluster string
		width   int
	}{
		{name: "Luxembourg flag", cluster: "🇱🇺", width: 2},
		{name: "Netherlands flag", cluster: "🇳🇱", width: 2},
		{name: "skin tone", cluster: "👍🏽", width: 2},
		{name: "ZWJ family", cluster: "👨‍👩‍👧‍👦", width: 2},
		{name: "variation selector", cluster: "❤️", width: 2},
		{name: "combining accent", cluster: "é", width: 1},
		{name: "keycap", cluster: "1️⃣", width: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			units := textUnits("x" + test.cluster + "y")
			if len(units) != 3 || units[1] != test.cluster {
				t.Fatalf("textUnits()=%q, cluster was split", units)
			}
			if width := stringCellWidth(test.cluster); width != test.width {
				t.Fatalf("stringCellWidth()=%d, want %d", width, test.width)
			}
			text := "1234" + test.cluster
			lines := wrapText(text, 4)
			if strings.Join(lines, "") != text || len(lines) != 2 || lines[1] != test.cluster {
				t.Fatalf("wrapText()=%q, cluster was split", lines)
			}
			if truncated := truncateCells(text+"x", 5); truncated != "1234…" {
				t.Fatalf("truncateCells()=%q, cluster was split", truncated)
			}
		})
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

func TestReadingKeysRequireExplicitBoundaryConfirmation(t *testing.T) {
	ctx := context.Background()
	model := &app{
		items:   []feedItem{{key: "1"}, {key: "2"}},
		metrics: layoutMetrics{bodyHeight: 8, bodyLines: 16, maxScroll: 8},
	}

	model.scroll = model.metrics.maxScroll
	model.handleKey(ctx, "j")
	if model.index != 0 || model.scroll != model.metrics.maxScroll {
		t.Fatalf("j changed item or crossed the body boundary: index=%d scroll=%d", model.index, model.scroll)
	}
	model.index, model.scroll = 1, 0
	model.handleKey(ctx, "k")
	if model.index != 1 || model.scroll != 0 {
		t.Fatalf("k changed item or crossed the body boundary: index=%d scroll=%d", model.index, model.scroll)
	}

	model.index, model.scroll = 0, 0
	model.handleKey(ctx, " ")
	if model.scroll != 7 || model.index != 0 {
		t.Fatalf("first space did not move down seven eighths of a page: index=%d scroll=%d", model.index, model.scroll)
	}
	model.handleKey(ctx, " ")
	if model.scroll != 8 || model.index != 0 || model.boundarySwitchKey != "" {
		t.Fatalf("space landing at bottom armed item switch: index=%d scroll=%d key=%q", model.index, model.scroll, model.boundarySwitchKey)
	}
	if model.message != "" {
		t.Fatalf("bottom landing unexpectedly showed confirmation: %q", model.message)
	}
	if !model.pageAnchorVisible || model.pageAnchorLine != 14 {
		t.Fatalf("space continuation anchor=(%d, %v), want previous last line 14", model.pageAnchorLine, model.pageAnchorVisible)
	}
	model.handleKey(ctx, " ")
	if model.scroll != 8 || model.index != 0 || model.boundarySwitchKey != " " {
		t.Fatalf("space at bottom did not arm confirmation: index=%d scroll=%d key=%q", model.index, model.scroll, model.boundarySwitchKey)
	}
	if !strings.Contains(model.message, "再按一次 space") || model.pageAnchorLine != 15 {
		t.Fatalf("bottom confirmation anchor=%d message=%q", model.pageAnchorLine, model.message)
	}
	model.handleKey(ctx, " ")
	if model.index != 1 || model.scroll != 0 {
		t.Fatalf("confirmed space did not switch to the next item: index=%d scroll=%d", model.index, model.scroll)
	}

	model.scroll = 8
	model.handleKey(ctx, "b")
	if model.scroll != 1 || model.index != 1 {
		t.Fatalf("first b did not move up seven eighths of a page: index=%d scroll=%d", model.index, model.scroll)
	}
	model.handleKey(ctx, "b")
	if model.scroll != 0 || model.index != 1 || model.boundarySwitchKey != "" {
		t.Fatalf("b landing at top armed item switch: index=%d scroll=%d key=%q", model.index, model.scroll, model.boundarySwitchKey)
	}
	if model.message != "" {
		t.Fatalf("top landing unexpectedly showed confirmation: %q", model.message)
	}
	if !model.pageAnchorVisible || model.pageAnchorLine != 1 {
		t.Fatalf("b continuation anchor=(%d, %v), want previous first line 1", model.pageAnchorLine, model.pageAnchorVisible)
	}
	model.handleKey(ctx, "b")
	if model.scroll != 0 || model.index != 1 || model.boundarySwitchKey != "b" {
		t.Fatalf("b at top did not arm confirmation: index=%d scroll=%d key=%q", model.index, model.scroll, model.boundarySwitchKey)
	}
	if !strings.Contains(model.message, "再按一次 b") || model.pageAnchorLine != 0 {
		t.Fatalf("top confirmation anchor=%d message=%q", model.pageAnchorLine, model.message)
	}
	model.handleKey(ctx, "b")
	if model.index != 0 || model.scroll == 0 {
		t.Fatalf("confirmed b did not switch to the previous item bottom: index=%d scroll=%d", model.index, model.scroll)
	}
}

func TestSpaceOnOneScreenBodyRequiresConfirmation(t *testing.T) {
	ctx := context.Background()
	model := &app{
		items: []feedItem{
			{key: "1", kind: "answer", title: "短回答", body: "第一段\n\n最后一行"},
			{key: "2", kind: "answer", title: "下一条", body: "正文"},
		},
		width:  100,
		height: 20,
	}
	_, model.metrics = renderSingleApp(model)
	if model.metrics.maxScroll != 0 {
		t.Fatalf("test body unexpectedly needs scrolling: %#v", model.metrics)
	}

	model.handleKey(ctx, " ")
	if model.index != 0 || model.boundarySwitchKey != " " {
		t.Fatalf("first space switched a one-screen body: index=%d key=%q", model.index, model.boundarySwitchKey)
	}
	if model.pageAnchorLine != model.metrics.bodyLines-1 || !model.pageAnchorVisible || !strings.Contains(model.message, "再按一次 space") {
		t.Fatalf("one-screen bottom state anchor=(%d, %v) message=%q", model.pageAnchorLine, model.pageAnchorVisible, model.message)
	}
	lines, _ := renderSingleApp(model)
	anchors := pageAnchorLines(lines)
	if len(anchors) != 1 || anchors[0].style != ansiBlue || !strings.Contains(anchors[0].text, "最后一行") {
		t.Fatalf("one-screen bottom focus was not rendered on the final line: %#v", anchors)
	}
	model.clearMessage()
	if model.boundarySwitchKey != " " || !model.pageAnchorVisible {
		t.Fatalf("message expiry cleared the visible boundary state: key=%q anchor=%v", model.boundarySwitchKey, model.pageAnchorVisible)
	}

	model.handleKey(ctx, " ")
	if model.index != 1 {
		t.Fatalf("confirmed space did not switch one-screen body: index=%d", model.index)
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

func TestRefreshKeepsLoadedItemsMissingFromLatestSnapshot(t *testing.T) {
	model := &app{
		generation: 1,
		items: []feedItem{
			{key: "answer:1", title: "问题一"},
			{key: "answer:2", title: "问题二"},
			{key: "answer:3", title: "问题三"},
		},
		firstViewedKey:    "answer:1",
		furthestViewedKey: "answer:2",
	}
	model.captureRefreshBoundary()
	model.generation++
	model.applyFetch(fetchResult{
		reset:      true,
		generation: model.generation,
		response: map[string]any{
			"data": []any{
				feedTestRaw("new", "新问题"),
				feedTestRaw("1", "问题一"),
			},
			"paging": map[string]any{
				"is_end": false,
				"next":   "https://www.zhihu.com/api/v3/moments?after_id=next",
			},
		},
	})

	wantKeys := []string{"answer:new", "answer:1", "answer:2", "answer:3"}
	gotKeys := make([]string, len(model.items))
	for index, item := range model.items {
		gotKeys[index] = item.key
	}
	if strings.Join(gotKeys, "\n") != strings.Join(wantKeys, "\n") {
		t.Fatalf("refreshed keys=%q, want latest snapshot followed by retained history %q", gotKeys, wantKeys)
	}
	if _, isNew := model.newItemKeys["answer:new"]; !isNew || len(model.newItemKeys) != 1 {
		t.Fatalf("newItemKeys=%v, want only the unseen snapshot item", model.newItemKeys)
	}
	if model.nextURL == "" || model.end {
		t.Fatalf("paging was not kept from the latest snapshot: next=%q end=%v", model.nextURL, model.end)
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
		t.Fatalf("pending range=(%q, %q), want session first and furthest viewed items", model.pendingReadTopKey, model.pendingReadBottomKey)
	}
	if model.pendingRefreshTopKey != "answer:1" {
		t.Fatalf("pendingRefreshTopKey=%q, want the current feed head", model.pendingRefreshTopKey)
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
	if model.pendingReadTopKey != "" || model.pendingReadBottomKey != "" || model.pendingRefreshTopKey != "" {
		t.Fatalf("pending state=(%q, %q, %q) after refresh finishes", model.pendingReadTopKey, model.pendingReadBottomKey, model.pendingRefreshTopKey)
	}
	if _, ok := model.newItemKeys["answer:new"]; !ok || len(model.newItemKeys) != 1 {
		t.Fatalf("newItemKeys=%v, want only the item before the previous first item", model.newItemKeys)
	}

	sidebar := renderSidebar(model, 40)
	if sidebar[3].text != "› 新问题" {
		t.Fatalf("first sidebar title=%q, want no numeric prefix", sidebar[3].text)
	}
	if strings.Contains(sidebar[1].text, "NEW") || strings.Contains(sidebar[4].text, "NEW") {
		t.Fatalf("sidebar still contains redundant NEW text: %#v", sidebar)
	}
	if sidebar[3].style != ansiBold+ansiGreen {
		t.Fatalf("new selected title style=%q, want green", sidebar[3].style)
	}
	if sidebar[4].style != ansiDim {
		t.Fatalf("new item summary style=%q, want the normal dim style", sidebar[4].style)
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

	model.index = 0
	model.markCurrentViewed()
	model.index = 3
	model.markCurrentViewed()
	model.captureRefreshBoundary()
	if model.pendingReadTopKey != "answer:new" || model.pendingReadBottomKey != "answer:3" {
		t.Fatalf("cumulative pending range=(%q, %q), want process-lifetime endpoints", model.pendingReadTopKey, model.pendingReadBottomKey)
	}
	if model.pendingRefreshTopKey != "answer:new" {
		t.Fatalf("pendingRefreshTopKey=%q, want the latest feed head", model.pendingRefreshTopKey)
	}

	response = map[string]any{
		"data": []any{
			feedTestRaw("newer", "更新的问题"),
			feedTestRaw("new", "新问题"),
			feedTestRaw("1", "问题一"),
			feedTestRaw("2", "问题二"),
			feedTestRaw("3", "问题三"),
		},
		"paging": map[string]any{"is_end": true},
	}
	model.generation++
	model.applyFetch(fetchResult{response: response, reset: true, generation: model.generation})
	model.markCurrentViewed()
	if model.lastReadTopKey != "answer:new" || model.lastReadBottomKey != "answer:3" {
		t.Fatalf("cumulative last-read range=(%q, %q), want process-lifetime endpoints", model.lastReadTopKey, model.lastReadBottomKey)
	}
	if model.firstViewedKey != "answer:newer" || model.furthestViewedKey != "answer:3" {
		t.Fatalf("session viewed range=(%q, %q) regressed after refresh", model.firstViewedKey, model.furthestViewedKey)
	}
	if _, ok := model.newItemKeys["answer:newer"]; !ok || len(model.newItemKeys) != 1 {
		t.Fatalf("newItemKeys=%v, want only content from the latest refresh", model.newItemKeys)
	}

	model.captureRefreshBoundary()
	response = map[string]any{
		"data": []any{
			feedTestRaw("newest", "最新问题"),
			feedTestRaw("newer", "更新的问题"),
			feedTestRaw("new", "新问题"),
			feedTestRaw("1", "问题一"),
			feedTestRaw("2", "问题二"),
			feedTestRaw("3", "问题三"),
		},
		"paging": map[string]any{"is_end": true},
	}
	model.generation++
	model.applyFetch(fetchResult{response: response, reset: true, generation: model.generation})
	if model.lastReadTopKey != "answer:newer" {
		t.Fatalf("lastReadTopKey=%q, want the newest item displayed before refresh", model.lastReadTopKey)
	}
	if _, stillNew := model.newItemKeys["answer:newer"]; stillNew {
		t.Fatalf("previously displayed boundary is still marked new: %v", model.newItemKeys)
	}
	if _, isNew := model.newItemKeys["answer:newest"]; !isNew {
		t.Fatalf("latest refresh item is not marked new: %v", model.newItemKeys)
	}
	sidebar = renderSidebar(model, 40)
	if !strings.HasPrefix(sidebar[7].text, "  上次读到↓ · ") || sidebar[7].style != ansiCyan {
		t.Fatalf("displayed new item was not promoted to read boundary: %#v", sidebar[7])
	}
}

func TestSidebarReadBoundaryOverridesNewStyle(t *testing.T) {
	model := &app{
		items:             []feedItem{{key: "answer:1", title: "重叠状态", action: "某人赞同了回答"}},
		newItemKeys:       map[string]struct{}{"answer:1": {}},
		lastReadTopKey:    "answer:1",
		lastReadBottomKey: "answer:1",
		height:            14,
	}
	sidebar := renderSidebar(model, 40)
	if sidebar[3].style != ansiBold+ansiCyan {
		t.Fatalf("overlapping boundary title style=%q, want selected cyan", sidebar[3].style)
	}
	if !strings.HasPrefix(sidebar[4].text, "  上次读到↓↑ · ") || sidebar[4].style != ansiCyan {
		t.Fatalf("overlapping boundary summary=%#v, want read-range marker", sidebar[4])
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

	model.scroll = 0
	model.metrics = metrics
	model.pageDownWithConfirmation(context.Background(), maxInt(1, metrics.bodyHeight*7/8))
	lines, _ = renderSingleApp(model)
	anchors := pageAnchorLines(lines)
	if len(anchors) != 1 || !strings.Contains(anchors[0].text, "▸ ") {
		t.Fatalf("page continuation anchors=%#v, want one visible marker", anchors)
	}
	if anchors[0].style != ansiBlue {
		t.Fatalf("page continuation line style=%q, want soft blue", anchors[0].style)
	}
}

func TestBlankContinuationAnchorUsesDashedLine(t *testing.T) {
	model := &app{
		items: []feedItem{{
			kind:   "answer",
			action: "某人赞同了回答",
			title:  "多段正文",
			author: "答主",
			body:   "第一段\n\n第二段\n\n第三段\n\n第四段",
		}},
		width:             100,
		height:            14,
		pageAnchorLine:    1,
		pageAnchorVisible: true,
	}
	lines, _ := renderSingleApp(model)
	anchors := pageAnchorLines(lines)
	if len(anchors) != 1 || !strings.Contains(anchors[0].text, "┄┄┄") {
		t.Fatalf("blank continuation anchor does not contain a dashed line: %#v", anchors)
	}
	if anchors[0].style != ansiBlue {
		t.Fatalf("blank continuation anchor style=%q, want soft blue", anchors[0].style)
	}
}

func TestHelpUsesAlignedCommandColumns(t *testing.T) {
	lines := renderHelp(100, 24)
	if strings.TrimSpace(lines[0].text) != "快捷键" {
		t.Fatalf("help title=%q", lines[0].text)
	}
	descriptions := []string{
		"向下滚动；正文底部停止",
		"向上滚动；正文顶部停止",
		"向下翻页；到底后再按一次切换下一条",
		"向上翻页；到顶后再按一次切换上一条",
		"向下 / 向上半页，不切换动态",
		"下一条 / 上一条",
		"第一条 / 最后一条已加载动态",
		"加载评论 / 返回正文",
		"展开 / 收起知乎聚合动态",
		"专注阅读 / 恢复双栏",
		"用默认浏览器打开当前动态",
		"刷新；新标题变绿 / 标记进程阅读范围",
		"退出并恢复终端",
	}
	descriptionColumn := -1
	for index, description := range descriptions {
		prefix, _, found := strings.Cut(lines[index+2].text, description)
		if !found {
			t.Fatalf("help line %d has no description %q: %q", index, description, lines[index+2].text)
		}
		column := stringCellWidth(prefix)
		if descriptionColumn < 0 {
			descriptionColumn = column
		} else if column != descriptionColumn {
			t.Fatalf("help description column=%d, want %d: %q", column, descriptionColumn, lines[index+2].text)
		}
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

func pageAnchorLines(lines []styledLine) []styledLine {
	var result []styledLine
	for _, line := range lines {
		if strings.Contains(line.text, "▸ ") {
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
		"id": "answer:" + id,
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

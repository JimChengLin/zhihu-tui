package feedtui

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

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

func TestCommentHeaderOwnsCommentCount(t *testing.T) {
	model := &app{
		items: []feedItem{{
			key:          "answer:42",
			kind:         "answer",
			title:        "测试问题",
			author:       "答主",
			stats:        "赞同 12  ·  评论 3  ·  收藏 2",
			commentCount: 3,
		}},
		comments: map[string]*commentState{
			"answer:42": {items: []feedComment{{id: "1", author: "用户", content: "评论"}}, loaded: true},
		},
		commentMode: true,
		width:       100,
		height:      24,
	}
	lines, _ := renderSingleApp(model)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if strings.Contains(rendered, "评论 3") {
		t.Fatalf("feed stats repeat comment count in comment mode: %q", rendered)
	}
	for _, want := range []string{"赞同 12  ·  收藏 2", "评论区 · 共 3 条 · 已加载 1 条"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("comment header does not contain %q: %q", want, rendered)
		}
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
		"正文滚动；评论区逐条移动蓝色焦点",
		"向下翻页；正文到底后再按一次切换下一条",
		"向上翻页；正文到顶后再按一次切换上一条",
		"向下 / 向上半页，保留蓝色续读焦点",
		"向下 / 向上滚动一行",
		"下一条 / 上一条",
		"第一条 / 最后一条已加载动态",
		"赞同回答或蓝色焦点评论 / 取消赞同",
		"写评论 / 回复蓝色焦点所在评论",
		"加载评论 / 返回正文",
		"展开 / 收起蓝色焦点评论的回复或知乎聚合动态",
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

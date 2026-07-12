package feedtui

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestCommentComposerUsesBluePageAnchorAsReplyTarget(t *testing.T) {
	source := &commentTestSource{posts: make(chan string, 2)}
	model := &app{
		source: source,
		items:  []feedItem{{key: "answer:42", id: "42", kind: "answer", title: "问题"}},
		comments: map[string]*commentState{"answer:42": {items: []feedComment{
			{id: "100", author: "Alice", content: "根评论", children: []feedComment{{id: "101", author: "Bob", content: "子评论"}}},
		}},
		},
		commentMode:       true,
		commentPosts:      make(chan commentPostResult, 2),
		pageAnchorVisible: true,
		pageAnchorLine:    2,
		metrics: layoutMetrics{
			bodyHeight: 10,
			commentIDs: []string{"100", "100", "101"},
		},
	}

	model.handleKey(context.Background(), "w")
	if len(model.composeTargets) != 1 || model.composeTargets[0].commentID != "101" || model.composeTargets[0].label != "回复 Bob" || model.composeTargets[0].indent != 3 {
		t.Fatalf("reply target=%#v", model.composeTargets)
	}
	model.handleCommentComposerKey(context.Background(), "你")
	model.handleCommentComposerKey(context.Background(), "好")
	model.handleCommentComposerKey(context.Background(), "\r")
	waitForCommentPost(t, source.posts, "reply:answer:42:101:你好")

	model.commentSubmitting = false
	model.composing = false
	model.commentMode = false
	model.startCommentComposer()
	if len(model.composeTargets) != 1 || model.composeTargets[0].commentID != "" || model.composeTargets[0].label != "评论当前回答" {
		t.Fatalf("root target=%#v", model.composeTargets)
	}
	model.handleCommentComposerKey(context.Background(), "根")
	model.handleCommentComposerKey(context.Background(), "\r")
	waitForCommentPost(t, source.posts, "root:answer:42:根")
}

func TestPublishedReplyIsInsertedWithoutResettingCommentTree(t *testing.T) {
	state := &commentState{
		items: []feedComment{{
			id:            "100",
			author:        "Alice",
			content:       "根评论",
			childComments: 1,
			children:      []feedComment{{id: "101", author: "Bob", content: "子评论"}},
		}},
		expandedChildren: map[string]bool{"100": true},
		loaded:           true,
		end:              true,
	}
	model := &app{
		items:        []feedItem{{key: "answer:42", id: "42", kind: "answer", commentCount: 2}},
		comments:     map[string]*commentState{"answer:42": state},
		commentMode:  true,
		scroll:       7,
		composeInput: "回复内容",
		composing:    true,
	}
	model.applyCommentPost(context.Background(), commentPostResult{
		itemKey:  "answer:42",
		targetID: "101",
		content:  "回复内容",
		response: map[string]any{"id": "102", "content": "回复内容", "author": map[string]any{"name": "我"}},
		reply:    true,
	})

	root := state.items[0]
	if !state.loaded || state.loading || !state.expandedChildren["100"] || model.scroll != 7 {
		t.Fatalf("comment state was reset: state=%#v scroll=%d", state, model.scroll)
	}
	if root.childComments != 2 || len(root.children[0].children) != 1 || root.children[0].children[0].id != "102" {
		t.Fatalf("published reply was not inserted into tree: %#v", root)
	}
	if model.items[0].commentCount != 3 || model.composing {
		t.Fatalf("item=%#v composing=%v", model.items[0], model.composing)
	}
}

func TestPublishedRootCommentIsPrependedAndFocused(t *testing.T) {
	state := &commentState{
		items: []feedComment{
			{id: "100", author: "Alice", content: "原评论一"},
			{id: "101", author: "Bob", content: "原评论二"},
		},
		expandedChildren: map[string]bool{"100": true},
		loaded:           true,
		nextCursor:       "20",
	}
	model := &app{
		width:       100,
		height:      24,
		items:       []feedItem{{key: "answer:42", id: "42", kind: "answer", title: "问题", commentCount: 2}},
		comments:    map[string]*commentState{"answer:42": state},
		commentMode: true,
		scroll:      8,
		composing:   true,
	}

	model.applyCommentPost(context.Background(), commentPostResult{
		itemKey:  "answer:42",
		content:  "刚发布的评论",
		response: map[string]any{"id": "102", "content": "刚发布的评论", "author": map[string]any{"name": "我"}},
	})

	if len(state.items) != 3 || state.items[0].id != "102" || state.items[1].id != "100" {
		t.Fatalf("root comment order=%#v", state.items)
	}
	if !state.loaded || state.nextCursor != "20" || !state.expandedChildren["100"] {
		t.Fatalf("comment state was reset: %#v", state)
	}
	if model.scroll != 0 || !model.pageAnchorVisible || model.pageAnchorLine != 0 {
		t.Fatalf("published comment focus scroll=%d anchor=(%d,%v)", model.scroll, model.pageAnchorLine, model.pageAnchorVisible)
	}
	_, metrics := renderSingleApp(model)
	if len(metrics.commentIDs) == 0 || metrics.commentIDs[0] != "102" {
		t.Fatalf("first rendered comment IDs=%#v", metrics.commentIDs)
	}
	if model.items[0].commentCount != 3 || model.composing || model.message != "评论已发布" {
		t.Fatalf("item=%#v composing=%v message=%q", model.items[0], model.composing, model.message)
	}
}

func TestCommentComposerRequiresBlueFocusInCommentMode(t *testing.T) {
	model := &app{
		items:       []feedItem{{key: "answer:42", id: "42", kind: "answer"}},
		commentMode: true,
	}
	model.startCommentComposer()
	if model.composing || !strings.Contains(model.message, "space/b") {
		t.Fatalf("composing=%v message=%q", model.composing, model.message)
	}
}

func TestJKMovesBlueFocusBetweenComments(t *testing.T) {
	model := &app{
		items:       []feedItem{{key: "answer:42", id: "42", kind: "answer"}},
		commentMode: true,
		metrics: layoutMetrics{
			bodyHeight: 4,
			commentIDs: []string{"100", "100", "", "101", "101", "102"},
		},
	}
	model.handleKey(context.Background(), "j")
	if !model.pageAnchorVisible || model.pageAnchorLine != 0 {
		t.Fatalf("first focus visible=%v line=%d", model.pageAnchorVisible, model.pageAnchorLine)
	}
	model.handleKey(context.Background(), "j")
	if model.pageAnchorLine != 3 {
		t.Fatalf("next focus line=%d", model.pageAnchorLine)
	}
	model.handleKey(context.Background(), "k")
	if model.pageAnchorLine != 0 {
		t.Fatalf("previous focus line=%d", model.pageAnchorLine)
	}
}

func TestCommentComposerRendersInlineWithCursor(t *testing.T) {
	lines := []styledLine{{text: "评论", commentID: "100"}}
	model := &app{
		composing:      true,
		composeInput:   "test",
		composeCursor:  4,
		composeTargets: []commentComposeTarget{{commentID: "100", label: "回复 Alice"}},
	}
	rendered := insertCommentComposer(lines, model, 40)
	var text strings.Builder
	for _, line := range rendered {
		text.WriteString(line.text)
		text.WriteString(line.middle)
		text.WriteString(line.tail)
		text.WriteString(line.suffix)
		text.WriteByte('\n')
	}
	for _, want := range []string{"评论", "╭─ 回复 Alice", "│  test", "╰─"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("inline composer does not contain %q:\n%s", want, text.String())
		}
	}
	inputLine, _ := renderStyledLine(rendered[2], 40)
	if !strings.Contains(inputLine, styleText("│  ", ansiDim)+"test") {
		t.Fatalf("input colors are not separated: %q", inputLine)
	}
	input := inlineCommentComposerLines(model, model.composeTargets[0], 40)[1]
	if !input.hasCursor || input.cursorCell != stringCellWidth("│  test") {
		t.Fatalf("native cursor visible=%v cell=%d", input.hasCursor, input.cursorCell)
	}
}

func TestCommentComposerKeepsStableBottomBar(t *testing.T) {
	model := &app{
		width:          100,
		height:         24,
		items:          []feedItem{{key: "answer:42", id: "42", kind: "answer", title: "问题", body: "正文"}},
		composing:      true,
		composeTargets: []commentComposeTarget{{label: "评论当前回答"}},
	}
	lines, _ := renderSingleApp(model)
	if len(lines) != model.height {
		t.Fatalf("lines=%d, want %d", len(lines), model.height)
	}
	if !strings.Contains(lines[model.height-4].text, "──") {
		t.Fatalf("bottom separator=%q", lines[model.height-4].text)
	}
	hints := lines[model.height-2].text
	for _, want := range []string{"C-b/C-f 移动", "Backspace/Delete", "Enter 发送", "Esc 取消"} {
		if !strings.Contains(hints, want) {
			t.Fatalf("composer footer missing %q: %q", want, hints)
		}
	}
}

func TestCommentComposerEditsAtGraphemeCursor(t *testing.T) {
	model := &app{composing: true, composeInput: "你界", composeCursor: 2}
	model.handleCommentComposerKey(context.Background(), keyLeft)
	model.handleCommentComposerKey(context.Background(), "好")
	model.handleCommentComposerKey(context.Background(), " ")
	if model.composeInput != "你好 界" || model.composeCursor != 3 {
		t.Fatalf("insert input=%q cursor=%d", model.composeInput, model.composeCursor)
	}
	model.handleCommentComposerKey(context.Background(), keyBackspace)
	model.handleCommentComposerKey(context.Background(), keyDelete)
	if model.composeInput != "你好" || model.composeCursor != 2 {
		t.Fatalf("delete input=%q cursor=%d", model.composeInput, model.composeCursor)
	}
	model.handleCommentComposerKey(context.Background(), keyHome)
	for _, r := range "👨‍👩‍👧‍👦" {
		model.handleCommentComposerKey(context.Background(), keyEvent(string(r)))
	}
	model.handleCommentComposerKey(context.Background(), keyEnd)
	if model.composeInput != "👨‍👩‍👧‍👦你好" || model.composeCursor != 3 {
		t.Fatalf("grapheme input=%q cursor=%d", model.composeInput, model.composeCursor)
	}
}

func TestCommentComposerSupportsReadlineControlKeys(t *testing.T) {
	model := &app{composing: true, composeInput: "abc", composeCursor: 3}
	model.handleCommentComposerKey(context.Background(), keyCtrlA)
	model.handleCommentComposerKey(context.Background(), keyCtrlF)
	model.handleCommentComposerKey(context.Background(), keyCtrlD)
	if model.composeInput != "ac" || model.composeCursor != 1 {
		t.Fatalf("Ctrl-A/F/D input=%q cursor=%d", model.composeInput, model.composeCursor)
	}
	model.handleCommentComposerKey(context.Background(), keyCtrlE)
	model.handleCommentComposerKey(context.Background(), keyCtrlB)
	if model.composeCursor != 1 {
		t.Fatalf("Ctrl-E/B cursor=%d", model.composeCursor)
	}
}

func TestComposerWrappingPreservesSpacesAndCursor(t *testing.T) {
	lines, cursorLine, cursorCell := wrapComposerInput("ab  cd", 4, 4)
	if len(lines) != 2 || lines[0] != "ab  " || lines[1] != "cd" || cursorLine != 1 || cursorCell != 0 {
		t.Fatalf("lines=%q cursor=(%d,%d)", lines, cursorLine, cursorCell)
	}
}

func TestCommentComposerKeepsFocusedCommentAtSameScreenRow(t *testing.T) {
	comments := make([]feedComment, 0, 8)
	for index := 0; index < 8; index++ {
		comments = append(comments, feedComment{
			id:      strconv.Itoa(index + 1),
			author:  "用户" + strconv.Itoa(index+1),
			content: "评论正文",
		})
	}
	model := &app{
		width:       100,
		height:      24,
		source:      &commentTestSource{},
		items:       []feedItem{{key: "answer:42", id: "42", kind: "answer", title: "问题"}},
		comments:    map[string]*commentState{"answer:42": {items: comments, loaded: true, end: true}},
		commentMode: true,
	}
	_, model.metrics = renderSingleApp(model)
	focusLine := -1
	for line, commentID := range model.metrics.commentIDs {
		if commentID == "4" {
			focusLine = line
			break
		}
	}
	if focusLine < 0 {
		t.Fatal("focused comment was not laid out")
	}
	model.pageAnchorVisible = true
	model.pageAnchorLine = focusLine
	model.scroll = maxInt(0, focusLine-4)
	before, metrics := renderSingleApp(model)
	model.metrics = metrics
	beforeRow := blueFocusRow(before)
	beforeScroll := model.scroll

	model.startCommentComposer()
	after, _ := renderSingleApp(model)
	afterRow := blueFocusRow(after)
	if model.scroll != beforeScroll {
		t.Fatalf("scroll changed from %d to %d", beforeScroll, model.scroll)
	}
	if afterRow != beforeRow {
		t.Fatalf("blue focus moved from screen row %d to %d", beforeRow, afterRow)
	}
	cursorVisible := false
	for _, line := range after {
		cursorVisible = cursorVisible || line.hasCursor
	}
	if !styledLinesContain(after, "╭─ 回复 用户4") || !cursorVisible {
		t.Fatalf("inline composer was not rendered below focused comment: %#v", after)
	}
}

func blueFocusRow(lines []styledLine) int {
	for row, line := range lines {
		if line.style == ansiBlue && strings.Contains(styledLineText(line), "▸ ") {
			return row
		}
	}
	return -1
}

func styledLinesContain(lines []styledLine, want string) bool {
	for _, line := range lines {
		if strings.Contains(styledLineText(line), want) {
			return true
		}
	}
	return false
}

func TestCommentComposerSkipsTrailingParagraphGap(t *testing.T) {
	lines := []styledLine{
		{text: "目标评论", commentID: "100"},
		{commentID: "100"},
		{commentID: "100"},
		{text: "下一条评论", commentID: "101"},
	}
	model := &app{
		composing:         true,
		composeTargets:    []commentComposeTarget{{commentID: "100", label: "回复 Alice"}},
		composeInsertLine: 2,
	}
	rendered := insertCommentComposer(lines, model, 40)
	if len(rendered) < 2 || rendered[1].text != "╭─ " {
		t.Fatalf("composer was not placed directly below target: %#v", rendered)
	}
}

func TestEscapeCancelsInlineCommentComposer(t *testing.T) {
	model := &app{composing: true, composeInput: "未发送内容"}
	model.handleKey(context.Background(), keyEscape)
	if model.composing || model.composeInput != "" {
		t.Fatalf("composing=%v input=%q", model.composing, model.composeInput)
	}
}

func TestControlJDoesNotSubmitComment(t *testing.T) {
	model := &app{composing: true, composeInput: "未发送", composeCursor: 3}
	model.handleCommentComposerKey(context.Background(), keyCtrlJ)
	if !model.composing || model.commentSubmitting || model.composeInput != "未发送" {
		t.Fatalf("Ctrl-J changed composer: composing=%v submitting=%v input=%q", model.composing, model.commentSubmitting, model.composeInput)
	}
}

func TestDropLastTextUnitKeepsEmojiClusterIntact(t *testing.T) {
	if got := dropLastTextUnit("你好👨‍👩‍👧‍👦"); got != "你好" {
		t.Fatalf("dropLastTextUnit()=%q", got)
	}
}

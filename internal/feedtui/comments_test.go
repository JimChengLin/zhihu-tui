package feedtui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

type commentTestSource struct {
	calls chan string
	posts chan string
}

type commentPagingTestSource struct {
	commentTestSource
	offsets chan int
}

type blockingRelationSource struct {
	commentTestSource
	started chan struct{}
	release chan struct{}
}

type blockingChildSource struct {
	commentTestSource
	started chan struct{}
	release chan struct{}
}

type blockingCommentPageSource struct {
	commentTestSource
	started chan struct{}
}

func (source *blockingCommentPageSource) GetComments(ctx context.Context, _, _ string, _, _ int, _ string) (map[string]any, error) {
	source.started <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (source *blockingRelationSource) GetUserProfile(_ context.Context, urlToken string) (map[string]any, error) {
	source.started <- struct{}{}
	<-source.release
	return map[string]any{"url_token": urlToken, "is_followed": true}, nil
}

func (source *blockingChildSource) GetChildComments(context.Context, string, int, int) (map[string]any, error) {
	source.started <- struct{}{}
	<-source.release
	return map[string]any{"data": []any{map[string]any{
		"id":      "101",
		"author":  map[string]any{"name": "Bob", "url_token": "bob"},
		"content": "子评论",
	}}}, nil
}

func (source *commentPagingTestSource) GetComments(_ context.Context, _, _ string, offset, _ int, _ string) (map[string]any, error) {
	source.offsets <- offset
	response := map[string]any{
		"data": []any{map[string]any{
			"id":      offset + 1,
			"author":  map[string]any{"name": "用户"},
			"content": "第 " + strconv.Itoa(offset) + " 页",
		}},
		"paging": map[string]any{"is_end": offset > 0},
	}
	if offset == 0 {
		response["paging"].(map[string]any)["next"] = "https://www.zhihu.com/api/v4/comments?offset=10"
	}
	return response, nil
}

func (source *commentTestSource) GetFollowingFeed(context.Context, string, int) (map[string]any, error) {
	return nil, nil
}

func (source *commentTestSource) GetPin(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func (source *commentTestSource) GetAnswer(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func (source *commentTestSource) GetComments(_ context.Context, resourceType, resourceID string, _, _ int, _ string) (map[string]any, error) {
	source.calls <- resourceType + ":" + resourceID
	return map[string]any{
		"data": []any{map[string]any{
			"author": map[string]any{
				"member": map[string]any{"name": "Alice", "url_token": "alice", "is_following": true, "is_followed": true},
			},
			"content":             `<p>评论正文</p><img src="comment.jpg">`,
			"vote_count":          8,
			"child_comment_count": 2,
			"id":                  "100",
		}},
	}, nil
}

func (source *commentTestSource) GetChildComments(context.Context, string, int, int) (map[string]any, error) {
	return map[string]any{"data": []any{map[string]any{
		"id":      "101",
		"author":  map[string]any{"name": "Bob", "url_token": "bob", "is_following": true, "is_followed": true},
		"content": "子评论",
	}}}, nil
}

func (source *commentTestSource) GetUserProfile(_ context.Context, urlToken string) (map[string]any, error) {
	return map[string]any{
		"url_token":    urlToken,
		"is_following": true,
		"is_followed":  true,
	}, nil
}

func (source *commentTestSource) VoteUp(context.Context, string) (bool, error) {
	return true, nil
}

func (source *commentTestSource) VoteNeutral(context.Context, string) (bool, error) {
	return true, nil
}

func (source *commentTestSource) CreateComment(_ context.Context, resourceType, resourceID, content string) (map[string]any, error) {
	if source.posts != nil {
		source.posts <- "root:" + resourceType + ":" + resourceID + ":" + content
	}
	return map[string]any{"id": "new-root"}, nil
}

func (source *commentTestSource) ReplyCommentToResource(_ context.Context, resourceType, resourceID, commentID, content string) (map[string]any, error) {
	if source.posts != nil {
		source.posts <- "reply:" + resourceType + ":" + resourceID + ":" + commentID + ":" + content
	}
	return map[string]any{"id": "new-reply"}, nil
}

func TestToggleCommentsLoadsAndReturnsToBody(t *testing.T) {
	source := &commentTestSource{calls: make(chan string, 1)}
	model := &app{
		source: source,
		items: []feedItem{{
			key:          "answer:42",
			id:           "42",
			kind:         "answer",
			commentCount: 12,
		}},
		scroll:         5,
		comments:       map[string]*commentState{},
		commentFetches: make(chan commentFetchResult, 1),
	}
	model.toggleComments(context.Background())
	if !model.commentMode || model.scroll != 0 || model.bodyScroll != 5 {
		t.Fatalf("commentMode=%v scroll=%d bodyScroll=%d", model.commentMode, model.scroll, model.bodyScroll)
	}
	select {
	case call := <-source.calls:
		if call != "answer:42" {
			t.Fatalf("comment request=%q", call)
		}
	case <-time.After(time.Second):
		t.Fatal("comment request was not made")
	}
	select {
	case result := <-model.commentFetches:
		model.applyCommentFetch(result)
	case <-time.After(time.Second):
		t.Fatal("comment result was not delivered")
	}
	model.startCommentChildFetch(context.Background(), "answer:42")
	select {
	case result := <-model.commentChildFetches:
		model.applyCommentChildFetch(result)
	case <-time.After(time.Second):
		t.Fatal("child comment result was not delivered")
	}
	state := model.currentCommentState()
	if state == nil || len(state.items) != 1 {
		t.Fatalf("comment state=%#v", state)
	}
	view, label := formatCommentView(model.items[0], state, 0)
	for _, want := range []string{"Alice（互相关注）", "评论正文", "▣ 图片 1", "赞同 8"} {
		if !strings.Contains(view, want) {
			t.Fatalf("comment view does not contain %q: %s", want, view)
		}
	}
	if strings.Contains(view, "Bob") || strings.Contains(view, "子评论") {
		t.Fatalf("child comments should be collapsed by default: %s", view)
	}
	if collapsed := joinedBodyLines(view, 80); !strings.Contains(collapsed, "   ▸ 2 条回复") {
		t.Fatalf("collapsed root is missing disclosure tree node:\n%s", collapsed)
	}
	state.expandedChildren = map[string]bool{"100": true}
	view, label = formatCommentView(model.items[0], state, 0)
	var laidOut strings.Builder
	for _, line := range layoutBodyLines(view, 80) {
		laidOut.WriteString(line.text)
		laidOut.WriteString(line.middle)
		laidOut.WriteByte('\n')
	}
	for _, want := range []string{"   ▾ 2 条回复", "   ├─ Bob（互相关注）", "   │  子评论", "   └─ 还有 1 条回复未加载"} {
		if !strings.Contains(laidOut.String(), want) {
			t.Fatalf("laid out comment tree does not contain %q:\n%s", want, laidOut.String())
		}
	}
	if !strings.Contains(label, "共 12 条") || !strings.Contains(label, "已加载 2 条") {
		t.Fatalf("comment label=%q", label)
	}

	model.scroll = 3
	model.toggleComments(context.Background())
	if model.commentMode || model.scroll != 5 {
		t.Fatalf("commentMode=%v scroll=%d", model.commentMode, model.scroll)
	}
}

func joinedBodyLines(body string, width int) string {
	var result strings.Builder
	for _, line := range layoutBodyLines(body, width) {
		result.WriteString(styledLineText(line))
		result.WriteByte('\n')
	}
	return result.String()
}

func TestEmptyCommentsOnlyAppearInBottomStatus(t *testing.T) {
	model := &app{
		items:       []feedItem{{key: "answer:42", id: "42", kind: "answer", title: "问题"}},
		comments:    map[string]*commentState{"answer:42": {loaded: true, end: true}},
		commentMode: true,
		width:       100,
		height:      20,
	}
	lines, _ := renderSingleApp(model)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if strings.Contains(rendered, "按 c 返回正文") {
		t.Fatalf("empty comment prompt still occupies the reading area: %q", rendered)
	}
	if !strings.Contains(styledLineText(lines[model.height-3]), "评论区  ·  暂无评论") {
		t.Fatalf("bottom status does not show empty comments: %#v", lines[model.height-3])
	}
	if strings.Count(rendered, "暂无评论") != 1 {
		t.Fatalf("empty status should appear exactly once: %q", rendered)
	}
}

func TestKnownEmptyCommentsStayInBodyView(t *testing.T) {
	tests := []struct {
		name     string
		item     feedItem
		comments map[string]*commentState
	}{
		{
			name: "API count is zero",
			item: feedItem{key: "answer:42", id: "42", kind: "answer", hasCommentCount: true},
		},
		{
			name:     "empty result is cached",
			item:     feedItem{key: "answer:42", id: "42", kind: "answer"},
			comments: map[string]*commentState{"answer:42": {loaded: true, end: true}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := &app{
				items:    []feedItem{test.item},
				comments: test.comments,
				scroll:   5,
			}
			model.toggleComments(context.Background())
			if model.commentMode || model.scroll != 5 || model.message != "暂无评论" {
				t.Fatalf("commentMode=%v scroll=%d message=%q", model.commentMode, model.scroll, model.message)
			}
		})
	}
}

func TestToggleFocusedCommentChildren(t *testing.T) {
	state := &commentState{
		items: []feedComment{{
			id:            "100",
			author:        "Alice",
			content:       "根评论",
			childComments: 1,
			children:      []feedComment{{id: "101", author: "Bob", content: "子评论"}},
		}},
		loaded: true,
	}
	model := &app{
		items:             []feedItem{{key: "answer:42", id: "42", kind: "answer"}},
		comments:          map[string]*commentState{"answer:42": state},
		commentMode:       true,
		pageAnchorVisible: true,
		pageAnchorLine:    0,
		metrics:           layoutMetrics{commentIDs: []string{"100", "100"}},
	}
	model.toggleFocusedCommentChildren(context.Background())
	if !state.expandedChildren["100"] {
		t.Fatal("focused root comment was not expanded")
	}

	model.metrics.commentIDs = []string{"100", "100", "101", "101"}
	model.pageAnchorLine = 2
	model.toggleFocusedCommentChildren(context.Background())
	if state.expandedChildren["100"] {
		t.Fatal("focused child did not collapse its root comment")
	}
	if model.pageAnchorLine != 0 {
		t.Fatalf("collapsed focus line=%d, want root line 0", model.pageAnchorLine)
	}
}

func TestCommentRelationshipLabels(t *testing.T) {
	tests := []struct {
		name      string
		following bool
		followed  bool
		want      string
	}{
		{name: "following", following: true, want: "我关注"},
		{name: "followed", followed: true, want: "关注我"},
		{name: "mutual", following: true, followed: true, want: "互相关注"},
		{name: "unrelated", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := commentRelationshipLabel(feedComment{isFollowing: test.following, isFollowed: test.followed})
			if got != test.want {
				t.Fatalf("relationship label=%q, want %q", got, test.want)
			}
		})
	}
}

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
	for _, want := range []string{"评论", "╭─ 回复 Alice", "│  test|", "╰─ Enter 发送 · Esc 取消"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("inline composer does not contain %q:\n%s", want, text.String())
		}
	}
	inputLine, _ := renderStyledLine(rendered[2], 40)
	if !strings.Contains(inputLine, styleText("│  ", ansiDim)+"test|") {
		t.Fatalf("input colors are not separated: %q", inputLine)
	}
	model.spinner = 4
	dimCursor := inlineCommentComposerLines(model, model.composeTargets[0], 40)[1]
	if dimCursor.tail != "|" || dimCursor.tailStyle != ansiDim {
		t.Fatalf("dim cursor=%q style=%q", dimCursor.tail, dimCursor.tailStyle)
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
	if !styledLinesContain(after, "╭─ 回复 用户4") || !styledLinesContain(after, "│  |") {
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

func TestDropLastTextUnitKeepsEmojiClusterIntact(t *testing.T) {
	if got := dropLastTextUnit("你好👨‍👩‍👧‍👦"); got != "你好" {
		t.Fatalf("dropLastTextUnit()=%q", got)
	}
}

func TestCommentSpaceLoadsNextPageBeforeSwitchingFeed(t *testing.T) {
	source := &commentPagingTestSource{offsets: make(chan int, 2)}
	model := &app{
		source:         source,
		items:          []feedItem{{key: "answer:42", id: "42", kind: "answer"}, {key: "answer:43", id: "43", kind: "answer"}},
		comments:       map[string]*commentState{},
		commentFetches: make(chan commentFetchResult, 2),
		commentMode:    true,
		metrics:        layoutMetrics{bodyHeight: 10, bodyLines: 10},
	}
	model.startComments(context.Background(), model.items[0])
	waitForCommentOffset(t, source.offsets, 0)
	model.applyCommentFetch(waitForCommentFetch(t, model.commentFetches))
	state := model.currentCommentState()
	if state.end || state.nextOffset != 10 {
		t.Fatalf("initial paging state=%#v", state)
	}

	model.pageDownWithConfirmation(context.Background(), 8)
	waitForCommentOffset(t, source.offsets, 10)
	if model.boundarySwitchKey != "" || model.index != 0 || !state.loading {
		t.Fatalf("boundary=%q index=%d loading=%v", model.boundarySwitchKey, model.index, state.loading)
	}
	model.applyCommentFetch(waitForCommentFetch(t, model.commentFetches))
	if len(state.items) != 2 || !state.end {
		t.Fatalf("comments=%#v end=%v", state.items, state.end)
	}
}

func TestCommentPageTimeoutLeavesLoadingState(t *testing.T) {
	originalTimeout := commentPageTimeout
	commentPageTimeout = 20 * time.Millisecond
	t.Cleanup(func() { commentPageTimeout = originalTimeout })
	source := &blockingCommentPageSource{started: make(chan struct{}, 1)}
	model := &app{
		source:         source,
		items:          []feedItem{{key: "answer:42", id: "42", kind: "answer"}},
		comments:       map[string]*commentState{},
		commentFetches: make(chan commentFetchResult, 1),
	}
	state := &commentState{loaded: true}
	model.comments["answer:42"] = state
	model.startCommentPage(context.Background(), model.items[0], state, true)
	select {
	case <-source.started:
	case <-time.After(time.Second):
		t.Fatal("comment page request did not start")
	}
	model.applyCommentFetch(waitForCommentFetch(t, model.commentFetches))
	if state.loading || state.moreErr == nil {
		t.Fatalf("timed out page state=%#v", state)
	}
}

func TestFailedCommentPageWaitsForExplicitRetry(t *testing.T) {
	source := &commentPagingTestSource{offsets: make(chan int, 1)}
	state := &commentState{
		items:      []feedComment{{id: "1", author: "用户", content: "评论"}},
		loaded:     true,
		nextOffset: 10,
		moreErr:    errors.New("请求失败"),
	}
	model := &app{
		source:         source,
		items:          []feedItem{{key: "answer:42", id: "42", kind: "answer"}},
		comments:       map[string]*commentState{"answer:42": state},
		commentFetches: make(chan commentFetchResult, 1),
		commentMode:    true,
		metrics:        layoutMetrics{bodyHeight: 10, maxScroll: 20},
		scroll:         20,
	}
	model.maybePrefetchComments(context.Background())
	select {
	case offset := <-source.offsets:
		t.Fatalf("failed page was retried automatically at offset %d", offset)
	case <-time.After(30 * time.Millisecond):
	}
	if state.loading || state.moreErr == nil {
		t.Fatalf("failed page did not remain stable: %#v", state)
	}

	if !model.ensureMoreComments(context.Background()) {
		t.Fatal("space retry was not accepted")
	}
	waitForCommentOffset(t, source.offsets, 10)
	if !state.loading || state.moreErr != nil {
		t.Fatalf("explicit retry did not start cleanly: %#v", state)
	}
}

func TestDuplicateCommentPageStopsWithoutAdvancingOffset(t *testing.T) {
	state := &commentState{
		items:      []feedComment{{id: "1", author: "用户", content: "已有评论"}},
		loaded:     true,
		loading:    true,
		nextOffset: 14,
	}
	model := &app{comments: map[string]*commentState{"answer:42": state}}
	model.applyCommentFetch(commentFetchResult{
		key:    "answer:42",
		append: true,
		offset: 14,
		response: map[string]any{
			"data": []any{map[string]any{"id": "1", "author": map[string]any{"name": "用户"}, "content": "重复评论"}},
			"paging": map[string]any{
				"is_end": false,
				"next":   "https://www.zhihu.com/api/v4/comments?offset=14",
			},
		},
	})
	if state.loading || state.moreErr == nil {
		t.Fatalf("duplicate page did not stop loading: %#v", state)
	}
	if state.nextOffset != 14 || len(state.items) != 1 {
		t.Fatalf("duplicate page advanced state: %#v", state)
	}
}

func TestSpaceCanLeaveFeedItemWhileCommentsAreLoading(t *testing.T) {
	model := &app{
		items: []feedItem{
			{key: "answer:42", id: "42", kind: "answer"},
			{key: "answer:43", id: "43", kind: "answer"},
		},
		comments: map[string]*commentState{
			"answer:42": {loaded: true, loading: true},
		},
		commentMode: true,
		metrics:     layoutMetrics{bodyHeight: 10, bodyLines: 10, maxScroll: 0},
	}
	model.pageDownWithConfirmation(context.Background(), 8)
	if model.boundarySwitchKey != " " || model.index != 0 {
		t.Fatalf("first space boundary=%q index=%d", model.boundarySwitchKey, model.index)
	}
	model.pageDownWithConfirmation(context.Background(), 8)
	if model.index != 1 || model.commentMode {
		t.Fatalf("second space index=%d commentMode=%v", model.index, model.commentMode)
	}
}

func TestCommentRelationshipsLoadOutsidePaginationPath(t *testing.T) {
	source := &blockingRelationSource{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	model := &app{
		source: source,
		comments: map[string]*commentState{
			"answer:42": {items: []feedComment{{id: "100", author: "Alice", authorToken: "alice", content: "评论"}}, loaded: true},
		},
	}
	model.startCommentRelationshipFetch(context.Background(), "answer:42")
	select {
	case <-source.started:
	case <-time.After(time.Second):
		t.Fatal("relationship lookup did not start")
	}
	state := model.comments["answer:42"]
	if state.loading || state.items[0].content != "评论" {
		t.Fatalf("relationship lookup blocked comments: %#v", state)
	}
	close(source.release)
	select {
	case result := <-model.commentRelationFetches:
		model.applyCommentRelationshipFetch(result)
	case <-time.After(time.Second):
		t.Fatal("relationship lookup did not finish")
	}
	if !state.items[0].isFollowed {
		t.Fatalf("relationship was not applied: %#v", state.items[0])
	}
}

func TestCommentChildrenLoadOutsidePaginationPath(t *testing.T) {
	source := &blockingChildSource{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	model := &app{
		source: source,
		comments: map[string]*commentState{
			"answer:42": {
				items:  []feedComment{{id: "100", author: "Alice", content: "根评论", childComments: 1}},
				loaded: true,
			},
		},
	}
	model.startCommentChildFetch(context.Background(), "answer:42")
	select {
	case <-source.started:
	case <-time.After(time.Second):
		t.Fatal("child comment lookup did not start")
	}
	state := model.comments["answer:42"]
	if state.loading || state.items[0].content != "根评论" {
		t.Fatalf("child comment lookup blocked comments: %#v", state)
	}
	close(source.release)
	select {
	case result := <-model.commentChildFetches:
		model.applyCommentChildFetch(result)
	case <-time.After(time.Second):
		t.Fatal("child comment lookup did not finish")
	}
	if len(state.items[0].children) != 1 || state.items[0].children[0].content != "子评论" {
		t.Fatalf("child comments were not applied: %#v", state.items[0])
	}
}

func TestCommentRepliesFormFileTreeFromParentIDs(t *testing.T) {
	comments := []feedComment{
		{id: "101", parentID: "100", author: "A", content: "回复根评论"},
		{id: "102", parentID: "101", author: "B", replyTo: "A", content: "回复 A"},
		{id: "103", parentID: "100", author: "C", content: "另一条回复"},
		{id: "104", parentID: "102", author: "D", replyTo: "B", content: "回复 B"},
	}
	tree := nestCommentReplies(comments, "100")
	if len(tree) != 2 || len(tree[0].children) != 1 || len(tree[0].children[0].children) != 1 {
		t.Fatalf("reply tree=%#v", tree)
	}
	state := &commentState{
		items: []feedComment{{
			id:            "100",
			author:        "根作者",
			content:       "根评论",
			childComments: 4,
			children:      tree,
		}},
		expandedChildren: map[string]bool{"100": true},
		loaded:           true,
	}
	view, _ := formatCommentView(feedItem{}, state, 0)
	var laidOut strings.Builder
	for _, line := range layoutBodyLines(view, 100) {
		laidOut.WriteString(styledLineText(line))
		laidOut.WriteByte('\n')
	}
	for _, want := range []string{
		"   ├─ A",
		"   │  └─ B",
		"   │     └─ D",
		"   └─ C",
	} {
		if !strings.Contains(laidOut.String(), want) {
			t.Fatalf("comment filetree does not contain %q:\n%s", want, laidOut.String())
		}
	}
	if strings.Contains(laidOut.String(), "B 回复 A") || strings.Contains(laidOut.String(), "D 回复 B") {
		t.Fatalf("resolved tree repeats reply targets:\n%s", laidOut.String())
	}
}

func TestOrphanReplyKeepsReplyTargetLabel(t *testing.T) {
	tree := nestCommentReplies([]feedComment{{
		id:       "101",
		parentID: "missing",
		author:   "A",
		replyTo:  "未加载用户",
		content:  "回复内容",
	}}, "100")
	state := &commentState{
		items:            []feedComment{{id: "100", author: "根作者", content: "根评论", childComments: 1, children: tree}},
		expandedChildren: map[string]bool{"100": true},
		loaded:           true,
	}
	view, _ := formatCommentView(feedItem{}, state, 0)
	if !strings.Contains(view, "A 回复 未加载用户") {
		t.Fatalf("orphan reply lost its target: %q", view)
	}
}

func TestSiblingCommentsHaveConnectedBreathingRoom(t *testing.T) {
	state := &commentState{
		items: []feedComment{{
			id:            "100",
			author:        "根作者",
			content:       "根评论",
			childComments: 2,
			children: []feedComment{
				{id: "101", author: "A", content: "第一条"},
				{id: "102", author: "B", content: "第二条"},
			},
		}},
		expandedChildren: map[string]bool{"100": true},
		loaded:           true,
	}
	view, _ := formatCommentView(feedItem{}, state, 0)
	lines := layoutBodyLines(view, 80)
	first, second, gap := -1, -1, -1
	for index, line := range lines {
		switch line.commentID {
		case "101":
			if first < 0 {
				first = index
			}
		case "102":
			if second < 0 {
				second = index
			}
		case "":
			if first >= 0 && second < 0 && strings.TrimSpace(line.middle) == "" && strings.Contains(line.text, "│") {
				gap = index
			}
		}
	}
	if first < 0 || gap <= first || second <= gap {
		t.Fatalf("sibling gap first=%d gap=%d second=%d lines=%#v", first, gap, second, lines)
	}
}

func TestParentAndFirstChildHaveConnectedBreathingRoom(t *testing.T) {
	state := &commentState{
		items: []feedComment{{
			id:            "100",
			author:        "根作者",
			content:       "根评论",
			childComments: 2,
			children: []feedComment{{
				id:       "101",
				author:   "A",
				content:  "第一层",
				children: []feedComment{{id: "102", author: "B", content: "第二层"}},
			}},
		}},
		expandedChildren: map[string]bool{"100": true},
		loaded:           true,
	}
	view, _ := formatCommentView(feedItem{}, state, 0)
	lines := layoutBodyLines(view, 80)
	firstChild := firstLineWithCommentID(lines, "101")
	grandchild := firstLineWithCommentID(lines, "102")
	for _, line := range []int{firstChild - 1, grandchild - 1} {
		if line < 0 || lines[line].commentID != "" || !strings.Contains(lines[line].text, "│") {
			t.Fatalf("parent-child gap missing before line %d: %#v", line+1, lines)
		}
	}
}

func firstLineWithCommentID(lines []styledLine, commentID string) int {
	for index, line := range lines {
		if line.commentID == commentID {
			return index
		}
	}
	return -1
}

func waitForCommentPost(t *testing.T, posts <-chan string, want string) {
	t.Helper()
	select {
	case got := <-posts:
		if got != want {
			t.Fatalf("comment post=%q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("comment post was not made")
	}
}

func waitForCommentOffset(t *testing.T, offsets <-chan int, want int) {
	t.Helper()
	select {
	case got := <-offsets:
		if got != want {
			t.Fatalf("comment offset=%d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("comment request was not made")
	}
}

func waitForCommentFetch(t *testing.T, fetches <-chan commentFetchResult) commentFetchResult {
	t.Helper()
	select {
	case result := <-fetches:
		return result
	case <-time.After(time.Second):
		t.Fatal("comment result was not delivered")
		return commentFetchResult{}
	}
}

package feedtui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const testCommentCursor = "601800174_11417294455_0"

type commentTestSource struct {
	calls chan string
	posts chan string
}

type commentPagingTestSource struct {
	commentTestSource
	cursors chan string
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

type commentChildPagingSource struct {
	cursors chan string
}

func (source *commentTestSource) GetSelfInfo(context.Context) (map[string]any, error) {
	return map[string]any{"id": "self"}, nil
}

func (source *blockingCommentPageSource) GetCommentsPage(ctx context.Context, _, _, _ string, _ int, _ string) (map[string]any, error) {
	source.started <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (source *blockingRelationSource) GetUserProfile(_ context.Context, urlToken string) (map[string]any, error) {
	source.started <- struct{}{}
	<-source.release
	return map[string]any{"url_token": urlToken, "is_followed": true}, nil
}

func (source *blockingChildSource) GetChildCommentsPage(context.Context, string, string, int) (map[string]any, error) {
	source.started <- struct{}{}
	<-source.release
	return map[string]any{"data": []any{map[string]any{
		"id":      "101",
		"author":  map[string]any{"name": "Bob", "url_token": "bob"},
		"content": "子评论",
	}}}, nil
}

func (source *commentChildPagingSource) GetChildCommentsPage(_ context.Context, rootID, cursor string, _ int) (map[string]any, error) {
	source.cursors <- cursor
	id := "101"
	end := false
	if cursor == testCommentCursor {
		id = "102"
		end = true
	}
	response := map[string]any{
		"data": []any{map[string]any{
			"id":                  id,
			"reply_to_comment_id": rootID,
			"author":              map[string]any{"name": "用户"},
			"content":             "回复 " + id,
		}},
		"paging": map[string]any{"is_end": end},
	}
	if !end {
		response["paging"].(map[string]any)["next"] = "https://www.zhihu.com/api/v4/comments?offset=" + testCommentCursor
	}
	return response, nil
}

func (source *commentPagingTestSource) GetCommentsPage(_ context.Context, _, _, cursor string, _ int, _ string) (map[string]any, error) {
	source.cursors <- cursor
	page := "第一页"
	id := "1"
	end := false
	if cursor == testCommentCursor {
		page = "第二页"
		id = "2"
		end = true
	}
	response := map[string]any{
		"data": []any{map[string]any{
			"id":      id,
			"author":  map[string]any{"name": "用户"},
			"content": page,
		}},
		"paging": map[string]any{"is_end": end},
	}
	if cursor == "" {
		response["paging"].(map[string]any)["next"] = "https://www.zhihu.com/api/v4/comments?offset=" + testCommentCursor
	}
	return response, nil
}

func (source *commentTestSource) GetFollowingFeed(context.Context, string, int) (map[string]any, error) {
	return nil, nil
}

func (source *commentTestSource) GetPin(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func (source *commentTestSource) GetQuestion(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func (source *commentTestSource) GetAnswer(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func (source *commentTestSource) GetArticle(context.Context, string) (map[string]any, error) {
	return nil, nil
}

func (source *commentTestSource) GetCommentsPage(_ context.Context, resourceType, resourceID, _ string, _ int, _ string) (map[string]any, error) {
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

func (source *commentTestSource) GetChildCommentsPage(context.Context, string, string, int) (map[string]any, error) {
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

func (source *commentTestSource) SetContentVote(context.Context, string, string, bool) (bool, error) {
	return true, nil
}

func (source *commentTestSource) FollowQuestion(context.Context, string) (bool, error) {
	return true, nil
}

func (source *commentTestSource) UnfollowQuestion(context.Context, string) (bool, error) {
	return true, nil
}

func (source *commentTestSource) LikeComment(context.Context, string) (bool, error) {
	return true, nil
}

func (source *commentTestSource) UnlikeComment(context.Context, string) (bool, error) {
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
	for _, want := range []string{"   ▾ 2 条回复", "   ├─ Bob（互相关注）", "   │  子评论", "   └─ 还有 1 条子回复未返回"} {
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

func TestToggleCommentsRestoresBodyPageAnchor(t *testing.T) {
	tests := []struct {
		name   string
		key    keyEvent
		scroll int
	}{
		{name: "space", key: " "},
		{name: "b", key: "b", scroll: 8},
		{name: "d", key: "d"},
		{name: "u", key: "u", scroll: 8},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := &app{
				items:    []feedItem{{key: "answer:42", id: "42", kind: "answer", commentCount: 1}},
				comments: map[string]*commentState{"answer:42": {loaded: true, items: []feedComment{{id: "100"}}}},
				scroll:   test.scroll,
				metrics:  layoutMetrics{bodyHeight: 8, bodyLines: 24, maxScroll: 16},
			}
			model.handleKey(context.Background(), test.key)
			bodyScroll := model.scroll
			bodyAnchorLine := model.pageAnchorLine
			if !model.pageAnchorVisible {
				t.Fatal("navigation key did not create a body page anchor")
			}

			model.handleKey(context.Background(), "c")
			if !model.commentMode || model.pageAnchorVisible {
				t.Fatalf("entered comments with commentMode=%v anchor=(%d,%v)", model.commentMode, model.pageAnchorLine, model.pageAnchorVisible)
			}
			model.handleKey(context.Background(), "c")
			if model.commentMode || model.scroll != bodyScroll || !model.pageAnchorVisible || model.pageAnchorLine != bodyAnchorLine {
				t.Fatalf("returned to body with commentMode=%v scroll=%d anchor=(%d,%v), want scroll=%d anchor=(%d,true)", model.commentMode, model.scroll, model.pageAnchorLine, model.pageAnchorVisible, bodyScroll, bodyAnchorLine)
			}
		})
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

func TestCommentSpaceLoadsNextPageBeforeSwitchingFeed(t *testing.T) {
	source := &commentPagingTestSource{cursors: make(chan string, 2)}
	model := &app{
		source:         source,
		items:          []feedItem{{key: "answer:42", id: "42", kind: "answer"}, {key: "answer:43", id: "43", kind: "answer"}},
		comments:       map[string]*commentState{},
		commentFetches: make(chan commentFetchResult, 2),
		commentMode:    true,
		metrics:        layoutMetrics{bodyHeight: 10, bodyLines: 10},
	}
	model.startComments(context.Background(), model.items[0])
	waitForCommentCursor(t, source.cursors, "")
	model.applyCommentFetch(waitForCommentFetch(t, model.commentFetches))
	state := model.currentCommentState()
	if state.end || state.nextCursor != testCommentCursor {
		t.Fatalf("initial paging state=%#v", state)
	}

	model.pageDownWithConfirmation(context.Background(), 8)
	waitForCommentCursor(t, source.cursors, testCommentCursor)
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
	source := &commentPagingTestSource{cursors: make(chan string, 1)}
	state := &commentState{
		items:      []feedComment{{id: "1", author: "用户", content: "评论"}},
		loaded:     true,
		nextCursor: testCommentCursor,
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
	case cursor := <-source.cursors:
		t.Fatalf("failed page was retried automatically at cursor %q", cursor)
	case <-time.After(30 * time.Millisecond):
	}
	if state.loading || state.moreErr == nil {
		t.Fatalf("failed page did not remain stable: %#v", state)
	}

	if !model.ensureMoreComments(context.Background()) {
		t.Fatal("space retry was not accepted")
	}
	waitForCommentCursor(t, source.cursors, testCommentCursor)
	if !state.loading || state.moreErr != nil {
		t.Fatalf("explicit retry did not start cleanly: %#v", state)
	}
}

func TestDuplicateCommentPageWithStalledCursorEndsVisibleComments(t *testing.T) {
	state := &commentState{
		items:      []feedComment{{id: "1", author: "用户", content: "已有评论"}},
		loaded:     true,
		loading:    true,
		nextCursor: testCommentCursor,
	}
	model := &app{comments: map[string]*commentState{"answer:42": state}}
	model.applyCommentFetch(commentFetchResult{
		key:    "answer:42",
		append: true,
		cursor: testCommentCursor,
		response: map[string]any{
			"data": []any{map[string]any{"id": "1", "author": map[string]any{"name": "用户"}, "content": "重复评论"}},
			"paging": map[string]any{
				"is_end": false,
				"next":   "https://www.zhihu.com/api/v4/comments?offset=" + testCommentCursor,
			},
		},
	})
	if state.loading || state.moreErr != nil || !state.end {
		t.Fatalf("stalled page did not end visible comments: %#v", state)
	}
	if state.nextCursor != "" || len(state.items) != 1 {
		t.Fatalf("duplicate page advanced state: %#v", state)
	}
}

func TestDuplicateCommentPageAdvancesWhenCursorChanges(t *testing.T) {
	const nextCursor = "601800175_11417294456_0"
	state := &commentState{
		items:      []feedComment{{id: "1", author: "用户", content: "已有评论"}},
		loaded:     true,
		loading:    true,
		nextCursor: testCommentCursor,
	}
	model := &app{comments: map[string]*commentState{"answer:42": state}}
	model.applyCommentFetch(commentFetchResult{
		key:    "answer:42",
		append: true,
		cursor: testCommentCursor,
		response: map[string]any{
			"data": []any{map[string]any{"id": "1", "author": map[string]any{"name": "用户"}, "content": "重复评论"}},
			"paging": map[string]any{
				"is_end": false,
				"next":   "https://www.zhihu.com/api/v4/comments?offset=" + nextCursor,
			},
		},
	})
	if state.loading || state.moreErr != nil || state.end || state.nextCursor != nextCursor || len(state.items) != 1 {
		t.Fatalf("advancing duplicate page state=%#v", state)
	}
}

func TestInitialCommentPageWithoutOpaqueCursorFailsFast(t *testing.T) {
	state := &commentState{loading: true}
	model := &app{comments: map[string]*commentState{"answer:42": state}}
	model.applyCommentFetch(commentFetchResult{
		key: "answer:42",
		response: map[string]any{
			"data": []any{map[string]any{"id": "1", "author": map[string]any{"name": "用户"}, "content": "评论"}},
			"paging": map[string]any{
				"is_end": false,
				"next":   "https://www.zhihu.com/api/v4/comments",
			},
		},
	})
	if !state.loaded || state.loading || len(state.items) != 1 || state.moreErr == nil {
		t.Fatalf("invalid first page did not fail fast: %#v", state)
	}
}

func TestCommentPagingCannotSwitchFeedItemsAtBoundaries(t *testing.T) {
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
	if model.boundarySwitchKey != "" || model.index != 0 || model.message != "正在加载更多评论" {
		t.Fatalf("loading bottom boundary=%q index=%d message=%q", model.boundarySwitchKey, model.index, model.message)
	}
	model.pageDownWithConfirmation(context.Background(), 8)
	if model.index != 0 || !model.commentMode {
		t.Fatalf("repeated space index=%d commentMode=%v", model.index, model.commentMode)
	}

	model.comments["answer:42"].loading = false
	model.comments["answer:42"].end = true
	model.pageDownWithConfirmation(context.Background(), 8)
	model.pageDownWithConfirmation(context.Background(), 8)
	if model.index != 0 || model.boundarySwitchKey != "" || model.message != "已到评论底部" {
		t.Fatalf("finished bottom index=%d boundary=%q message=%q", model.index, model.boundarySwitchKey, model.message)
	}

	model.pageUpWithConfirmation(context.Background(), 8)
	model.pageUpWithConfirmation(context.Background(), 8)
	if model.index != 0 || model.boundarySwitchKey != "" || model.message != "已到评论顶部" {
		t.Fatalf("top index=%d boundary=%q message=%q", model.index, model.boundarySwitchKey, model.message)
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

func TestCommentChildrenFollowOpaqueCursor(t *testing.T) {
	source := &commentChildPagingSource{cursors: make(chan string, 2)}
	children := fetchCommentChildren(context.Background(), source, []string{"100"})["100"]
	waitForCommentCursor(t, source.cursors, "")
	waitForCommentCursor(t, source.cursors, testCommentCursor)
	if len(children) != 2 || children[0].id != "101" || children[1].id != "102" {
		t.Fatalf("child comments=%#v", children)
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

func TestNestedCommentLinkUsesVisibleTextAndLinkStyle(t *testing.T) {
	child := parseComment(map[string]any{
		"id":      "101",
		"author":  map[string]any{"name": "子评论作者"},
		"content": `<p>训练视频：<a href="https://b23.tv/BW5gpyr">b23.tv/BW5gpyr</a>，三楼 36 秒</p>`,
	})
	state := &commentState{
		items: []feedComment{{
			id:            "100",
			author:        "根作者",
			content:       "根评论",
			childComments: 1,
			children:      []feedComment{child},
		}},
		expandedChildren: map[string]bool{"100": true},
		loaded:           true,
	}
	view, _ := formatCommentView(feedItem{}, state, 0)
	lines := layoutBodyLines(view, 100)
	foundLink := false
	for _, line := range lines {
		if line.commentID != "101" {
			continue
		}
		if strings.Contains(styledLineText(line), "inline-link-") {
			t.Fatalf("nested comment exposed inline link marker: %#v", line)
		}
		for _, segment := range line.segments {
			if segment.text == "b23.tv/BW5gpyr" && segment.style == ansiLink {
				foundLink = true
			}
		}
	}
	if !foundLink {
		t.Fatalf("nested comment link was not styled: %#v", lines)
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

func TestRootCommentsHaveTwoBlankRowsBetweenThem(t *testing.T) {
	codeComment := parseComment(map[string]any{
		"id":      "100",
		"author":  map[string]any{"name": "A"},
		"content": `<pre><code>code</code></pre>`,
	})
	for _, test := range []struct {
		name  string
		first feedComment
	}{
		{name: "prose", first: feedComment{id: "100", author: "A", content: "第一条"}},
		{name: "code block", first: codeComment},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &commentState{
				items:  []feedComment{test.first, {id: "200", author: "B", content: "第二条"}},
				loaded: true,
			}
			view, _ := formatCommentView(feedItem{}, state, 0)
			lines := layoutBodyLines(view, 80)
			second := firstLineWithCommentID(lines, "200")
			lastFirst := second - 1
			for lastFirst >= 0 && (lines[lastFirst].commentID != "100" || styledLineText(lines[lastFirst]) == "") {
				lastFirst--
			}
			blankRows := second - lastFirst - 1
			if lastFirst < 0 || blankRows != 2 {
				t.Fatalf("root comment gap=%d, want 2: %#v", blankRows, lines)
			}
			for _, line := range lines[lastFirst+1 : second] {
				if styledLineText(line) != "" {
					t.Fatalf("root comment separator contains content: %#v", lines)
				}
			}
		})
	}
}

func TestCommentContentKeepsAtMostOneBlankLine(t *testing.T) {
	comment := parseComment(map[string]any{
		"id":      "100",
		"author":  map[string]any{"name": "A"},
		"content": "第一行\n\n第二行\n \n\n第三行",
	})
	if comment.content != "第一行\n\n第二行\n\n第三行" {
		t.Fatalf("comment content=%q", comment.content)
	}
	view, _ := formatCommentView(feedItem{}, &commentState{items: []feedComment{comment}, loaded: true}, 0)
	lines := layoutBodyLines(view, 80)
	requireBlankRowsBetweenText(t, lines, "第一行", "第二行", 1)
	requireBlankRowsBetweenText(t, lines, "第二行", "第三行", 1)

	posted := parsePostedComment(map[string]any{"id": "200"}, "第一行\r\n\r\n第二行", "")
	if posted.content != "第一行\n\n第二行" {
		t.Fatalf("posted comment content=%q", posted.content)
	}
}

func TestCommentContentUsesOneBlankLineAcrossLayoutPaths(t *testing.T) {
	comment := parseComment(map[string]any{
		"id":     "100",
		"author": map[string]any{"name": "A"},
		"content": `<p><a href="https://example.com">链接段落</a></p><p>普通段落</p>` +
			`<pre><code>code</code></pre><p>末段</p>`,
	})
	view, _ := formatCommentView(feedItem{}, &commentState{items: []feedComment{comment}, loaded: true}, 0)
	lines := layoutBodyLines(view, 80)
	requireBlankRowsBetweenText(t, lines, "链接段落", "普通段落", 1)
	requireBlankRowsBetweenText(t, lines, "普通段落", "┌─ 代码", 1)
	requireBlankRowsBetweenText(t, lines, "└─", "末段", 1)
}

func requireBlankRowsBetweenText(t *testing.T, lines []styledLine, before, after string, want int) {
	t.Helper()
	beforeRow, afterRow := -1, -1
	for index, line := range lines {
		switch styledLineText(line) {
		case before:
			beforeRow = index
		case after:
			afterRow = index
		}
	}
	if beforeRow < 0 || afterRow <= beforeRow {
		t.Fatalf("cannot find %q before %q: %#v", before, after, lines)
	}
	if got := afterRow - beforeRow - 1; got != want {
		t.Fatalf("blank rows between %q and %q=%d, want %d: %#v", before, after, got, want, lines)
	}
}

func TestCommentLabelDistinguishesRootEndFromPendingReplies(t *testing.T) {
	state := &commentState{
		items:  []feedComment{{id: "100", author: "A", content: "根评论", childComments: 2}},
		loaded: true,
		end:    true,
	}
	_, label := formatCommentView(feedItem{commentCount: 3}, state, 0)
	if !strings.Contains(label, "已加载 1 条 · 2 条子回复未返回") || strings.Contains(label, "已到底") {
		t.Fatalf("comment label=%q", label)
	}
}

func TestCommentLabelExplainsWhereToRetryFailedPage(t *testing.T) {
	state := &commentState{
		items:   []feedComment{{id: "100", author: "A", content: "已加载评论"}},
		loaded:  true,
		moreErr: errors.New("请求失败"),
	}
	_, label := formatCommentView(feedItem{commentCount: 2}, state, 0)
	want := "评论区 · 共 2 条 · 已加载 1 条 · 加载更多失败，滚至评论区底部后按 space 重试"
	if label != want {
		t.Fatalf("comment label=%q, want %q", label, want)
	}
}

func TestCommentLabelExplainsFilteredTotalAtRootEnd(t *testing.T) {
	state := &commentState{
		items:  []feedComment{{id: "100", author: "A", content: "可见评论"}},
		loaded: true,
		end:    true,
	}
	_, label := formatCommentView(feedItem{commentCount: 2}, state, 0)
	if !strings.Contains(label, "可见评论已全部加载") {
		t.Fatalf("comment label=%q", label)
	}
}

func TestCommentLabelOmitsRedundantFullyLoadedStatus(t *testing.T) {
	state := &commentState{
		items:  []feedComment{{id: "100", author: "A", content: "评论"}},
		loaded: true,
		end:    true,
	}
	_, label := formatCommentView(feedItem{commentCount: 1}, state, 0)
	if label != "评论区 · 共 1 条 · 已加载 1 条" {
		t.Fatalf("comment label=%q", label)
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

func waitForCommentCursor(t *testing.T, cursors <-chan string, want string) {
	t.Helper()
	select {
	case got := <-cursors:
		if got != want {
			t.Fatalf("comment cursor=%q, want %q", got, want)
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

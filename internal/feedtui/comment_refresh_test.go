package feedtui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

type commentRefreshTestSource struct {
	commentTestSource
	requests chan commentRefreshPageRequest
}

type commentRefreshPageRequest struct {
	cursor string
	result chan commentRefreshPageResult
}

type commentRefreshPageResult struct {
	response map[string]any
	err      error
}

func (source *commentRefreshTestSource) GetCommentsPage(ctx context.Context, _, _, cursor string, _ int, _ string) (map[string]any, error) {
	request := commentRefreshPageRequest{cursor: cursor, result: make(chan commentRefreshPageResult, 1)}
	select {
	case source.requests <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case result := <-request.result:
		return result.response, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestReenterCommentsRefreshesFirstPageAndKeepsCachedItemsReadable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	source := &commentRefreshTestSource{requests: make(chan commentRefreshPageRequest, 2)}
	model := &app{
		source:         source,
		items:          []feedItem{{key: "pin:42", id: "42", kind: "pin", commentCount: 2}},
		comments:       map[string]*commentState{},
		commentFetches: make(chan commentFetchResult, 2),
	}
	model.toggleComments(ctx)
	request := waitForRefreshPageRequest(t, source.requests)
	if request.cursor != "" {
		t.Fatalf("first page cursor=%q", request.cursor)
	}
	request.result <- commentRefreshPageResult{response: refreshCommentResponse("1")}
	model.applyCommentFetch(waitForCommentFetch(t, model.commentFetches))
	state := model.currentCommentState()
	if len(state.items) != 1 || !state.end {
		t.Fatalf("initial comment state=%#v", state)
	}

	model.toggleComments(ctx)
	model.toggleComments(ctx)
	request = waitForRefreshPageRequest(t, source.requests)
	if request.cursor != "" || !state.loading || !state.refreshing || len(state.items) != 1 {
		t.Fatalf("refresh request cursor=%q state=%#v", request.cursor, state)
	}
	view, label := formatCommentView(model.items[0], state, 0, 80)
	if !strings.Contains(view, "评论 1") || !strings.Contains(label, "正在刷新评论") || strings.Contains(label, "正在加载更多") {
		t.Fatalf("refresh view=%q label=%q", view, label)
	}
	model.toggleComments(ctx)
	model.toggleComments(ctx)
	select {
	case duplicate := <-source.requests:
		t.Fatalf("refresh already in progress issued another request: cursor=%q", duplicate.cursor)
	case <-time.After(30 * time.Millisecond):
	}
	request.result <- commentRefreshPageResult{response: refreshCommentResponse("1", "2")}
	model.applyCommentFetch(waitForCommentFetch(t, model.commentFetches))
	if state.loading || state.refreshing || len(state.items) != 2 || state.items[1].id != "2" || !state.end {
		t.Fatalf("refreshed comment state=%#v", state)
	}
}

func TestCachedEmptyCommentsCanBeRequestedAgain(t *testing.T) {
	for _, test := range []struct {
		name            string
		hasCommentCount bool
		commentCount    int
	}{
		{name: "unknown count"},
		{name: "zero cached count", hasCommentCount: true},
		{name: "positive latest count", hasCommentCount: true, commentCount: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			source := &commentRefreshTestSource{requests: make(chan commentRefreshPageRequest, 1)}
			state := &commentState{loaded: true, end: true}
			model := &app{
				source: source,
				items: []feedItem{{key: "pin:42", id: "42", kind: "pin",
					hasCommentCount: test.hasCommentCount, commentCount: test.commentCount}},
				comments:       map[string]*commentState{"pin:42": state},
				commentFetches: make(chan commentFetchResult, 1),
			}
			model.toggleComments(ctx)
			request := waitForRefreshPageRequest(t, source.requests)
			if !model.commentMode || request.cursor != "" || !state.refreshing {
				t.Fatalf("cached empty comments were not refreshed: mode=%v cursor=%q state=%#v", model.commentMode, request.cursor, state)
			}
			view, _ := formatCommentView(model.items[0], state, 0, 80)
			if !strings.Contains(view, "正在刷新评论") {
				t.Fatalf("cached empty refresh view=%q", view)
			}
			request.result <- commentRefreshPageResult{response: refreshCommentResponse("1")}
			model.applyCommentFetch(waitForCommentFetch(t, model.commentFetches))
			if len(state.items) != 1 || state.items[0].id != "1" {
				t.Fatalf("new comments were not applied: %#v", state)
			}
		})
	}
}

func TestFailedCommentRefreshKeepsCachedItemsAndWaitsForReentry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	source := &commentRefreshTestSource{requests: make(chan commentRefreshPageRequest, 2)}
	state := &commentState{
		items:      []feedComment{{id: "1", author: "A", content: "缓存评论"}},
		loaded:     true,
		nextCursor: testCommentCursor,
	}
	model := &app{
		source:         source,
		items:          []feedItem{{key: "pin:42", id: "42", kind: "pin", commentCount: 2}},
		comments:       map[string]*commentState{"pin:42": state},
		commentFetches: make(chan commentFetchResult, 2),
		metrics:        layoutMetrics{bodyHeight: 10, maxScroll: 0},
	}
	model.toggleComments(ctx)
	request := waitForRefreshPageRequest(t, source.requests)
	request.result <- commentRefreshPageResult{err: errors.New("请求失败")}
	model.applyCommentFetch(waitForCommentFetch(t, model.commentFetches))
	if state.loading || state.refreshing || state.err == nil || len(state.items) != 1 || state.nextCursor != testCommentCursor {
		t.Fatalf("failed refresh discarded cached comments or paging: %#v", state)
	}
	view, label := formatCommentView(model.items[0], state, 0, 80)
	if !strings.Contains(view, "缓存评论") || !strings.Contains(label, "刷新评论失败，按 c 返回后再进入重试") {
		t.Fatalf("failed refresh view=%q label=%q", view, label)
	}
	model.maybePrefetchComments(ctx)
	select {
	case request := <-source.requests:
		t.Fatalf("failed refresh triggered automatic request: cursor=%q", request.cursor)
	case <-time.After(30 * time.Millisecond):
	}
	model.toggleComments(ctx)
	model.toggleComments(ctx)
	request = waitForRefreshPageRequest(t, source.requests)
	if request.cursor != "" || !state.refreshing || state.err != nil {
		t.Fatalf("reentry did not retry cleanly: cursor=%q state=%#v", request.cursor, state)
	}
	request.result <- commentRefreshPageResult{response: refreshCommentResponse("1", "2")}
	model.applyCommentFetch(waitForCommentFetch(t, model.commentFetches))
	if state.err != nil || len(state.items) != 2 {
		t.Fatalf("refresh retry did not apply new comments: %#v", state)
	}
}

func TestCommentRefreshReplacesOldPagesAndResetsCursor(t *testing.T) {
	const newCursor = "601800175_11417294456_0"
	state := &commentState{
		items:      []feedComment{{id: "1", content: "旧第一页"}, {id: "2", content: "旧第二页"}},
		loaded:     true,
		loading:    true,
		refreshing: true,
		end:        true,
		nextCursor: testCommentCursor,
	}
	model := &app{comments: map[string]*commentState{"pin:42": state}}
	response := refreshCommentResponse("3")
	response["paging"] = map[string]any{"is_end": false, "next": "https://www.zhihu.com/api/v4/comment_v5/pins/42/root_comment?offset=" + newCursor}
	model.applyCommentFetch(commentFetchResult{key: "pin:42", response: response})
	if state.loading || state.refreshing || state.end || state.nextCursor != newCursor || len(state.items) != 1 || state.items[0].id != "3" {
		t.Fatalf("refresh did not replace old pages and cursor: %#v", state)
	}
}

func TestInvalidCommentRefreshCursorKeepsCachedPageAndGeneration(t *testing.T) {
	state := &commentState{
		items:      []feedComment{{id: "1", content: "缓存评论"}},
		loaded:     true,
		loading:    true,
		refreshing: true,
		end:        true,
		nextCursor: testCommentCursor,
		generation: 7,
	}
	model := &app{comments: map[string]*commentState{"pin:42": state}}
	response := refreshCommentResponse("2")
	response["paging"] = map[string]any{"is_end": false, "next": "https://www.zhihu.com/api/v4/comment_v5/pins/42/root_comment"}
	model.applyCommentFetch(commentFetchResult{key: "pin:42", response: response})
	if state.loading || state.refreshing || state.err == nil || state.moreErr != nil || !state.end || state.nextCursor != testCommentCursor || state.generation != 7 {
		t.Fatalf("invalid refresh changed cached paging or generation: %#v", state)
	}
	if len(state.items) != 1 || state.items[0].id != "1" {
		t.Fatalf("invalid refresh replaced cached comments: %#v", state.items)
	}
}

func TestInvalidCommentAppendCursorKeepsCurrentPageForSpaceRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	source := &commentRefreshTestSource{requests: make(chan commentRefreshPageRequest, 1)}
	state := &commentState{
		items:      []feedComment{{id: "1", content: "缓存评论"}},
		loaded:     true,
		loading:    true,
		nextCursor: testCommentCursor,
		generation: 7,
	}
	model := &app{
		source:         source,
		items:          []feedItem{{key: "pin:42", id: "42", kind: "pin", commentCount: 2}},
		comments:       map[string]*commentState{"pin:42": state},
		commentFetches: make(chan commentFetchResult, 1),
		commentMode:    true,
	}
	response := refreshCommentResponse("2")
	response["paging"] = map[string]any{"is_end": false, "next": "https://www.zhihu.com/api/v4/comment_v5/pins/42/root_comment"}
	model.applyCommentFetch(commentFetchResult{key: "pin:42", response: response, append: true, cursor: testCommentCursor})
	if state.loading || state.err != nil || state.moreErr == nil || state.end || state.nextCursor != testCommentCursor || state.generation != 7 {
		t.Fatalf("invalid append changed current paging or generation: %#v", state)
	}
	if len(state.items) != 1 || state.items[0].id != "1" {
		t.Fatalf("invalid append added unconfirmed comments: %#v", state.items)
	}
	if !model.ensureMoreComments(ctx) {
		t.Fatal("space retry did not start after invalid append cursor")
	}
	request := waitForRefreshPageRequest(t, source.requests)
	if request.cursor != testCommentCursor {
		t.Fatalf("space retry cursor=%q, want %q", request.cursor, testCommentCursor)
	}
	request.result <- commentRefreshPageResult{response: refreshCommentResponse("2")}
	model.applyCommentFetch(waitForCommentFetch(t, model.commentFetches))
	if state.moreErr != nil || len(state.items) != 2 || !state.end || state.generation != 7 {
		t.Fatalf("space retry did not recover current page: %#v", state)
	}
}

func TestCommentResponseUpdatesTotalIncludingZeroAndFoldedCopies(t *testing.T) {
	for _, test := range []struct {
		name  string
		total int
		ids   []string
	}{
		{name: "current total", total: 2, ids: []string{"1", "2"}},
		{name: "zero total"},
	} {
		t.Run(test.name, func(t *testing.T) {
			const stateKey = "pin:42"
			model := &app{
				items: []feedItem{
					{key: stateKey, id: "42", kind: "pin", commentCount: 8, stats: "赞同 3  ·  评论 8  ·  收藏 2"},
					{key: "pin:43", id: "43", kind: "pin", commentCount: 9, stats: "评论 9", foldedItems: []feedItem{
						{key: stateKey, id: "42", kind: "pin", commentCount: 8, stats: "评论 8"},
					}},
				},
				comments: map[string]*commentState{stateKey: {loaded: true}},
			}
			response := refreshCommentResponse(test.ids...)
			response["counts"] = map[string]any{"total_counts": test.total}
			model.applyCommentFetch(commentFetchResult{key: stateKey, response: response})
			for _, item := range []feedItem{model.items[0], model.items[1].foldedItems[0]} {
				if !item.hasCommentCount || item.commentCount != test.total || !strings.Contains(item.stats, "评论 "+strconv.Itoa(test.total)) {
					t.Fatalf("current API total was not applied to item: %#v", item)
				}
			}
			if model.items[1].commentCount != 9 || model.items[1].stats != "评论 9" {
				t.Fatalf("comment total changed unrelated item: %#v", model.items[1])
			}
			if !strings.Contains(model.items[0].stats, "赞同 3") || !strings.Contains(model.items[0].stats, "收藏 2") {
				t.Fatalf("comment total discarded unrelated stats: %q", model.items[0].stats)
			}
		})
	}
}

func TestCommentRefreshAllowsCompletedChildrenToBeFetchedAgain(t *testing.T) {
	const stateKey = "pin:42"
	state := &commentState{
		items: []feedComment{{id: "100", content: "旧根评论", childComments: 1,
			children: []feedComment{{id: "101", content: "旧子回复"}}}},
		loaded:     true,
		generation: 1,
	}
	model := &app{
		source:               &commentTestSource{},
		comments:             map[string]*commentState{stateKey: state},
		commentChildComplete: map[string]struct{}{commentChildKey(stateKey, "100"): {}},
	}
	response := refreshCommentResponse("100")
	response["data"].([]any)[0].(map[string]any)["child_comment_count"] = 1
	model.applyCommentFetch(commentFetchResult{key: stateKey, response: response})
	if state.generation != 2 {
		t.Fatalf("refresh generation=%d, want 2", state.generation)
	}
	if _, complete := model.commentChildComplete[commentChildKey(stateKey, "100")]; complete {
		t.Fatal("refresh retained completed child-fetch cache")
	}
	model.startCommentChildFetch(context.Background(), stateKey)
	result := waitForRefreshChildFetch(t, model.commentChildFetches)
	if result.generation != state.generation {
		t.Fatalf("child request generation=%d, want %d", result.generation, state.generation)
	}
	model.applyCommentChildFetch(result)
	if len(state.items[0].children) != 1 || state.items[0].children[0].content != "子评论" {
		t.Fatalf("refreshed children were not fetched: %#v", state.items[0])
	}
}

func TestOldCommentChildResultCannotOverwriteRefreshedChildren(t *testing.T) {
	const stateKey = "pin:42"
	state := &commentState{
		items: []feedComment{{id: "100", content: "新根评论", childComments: 1,
			children: []feedComment{{id: "102", content: "新子回复"}}}},
		loaded:     true,
		generation: 2,
	}
	key := commentChildKey(stateKey, "100")
	model := &app{
		comments:             map[string]*commentState{stateKey: state},
		commentChildPending:  map[string]struct{}{key: {}},
		commentChildComplete: map[string]struct{}{},
	}
	model.applyCommentChildFetch(commentChildFetchResult{
		stateKey:   stateKey,
		rootIDs:    []string{"100"},
		generation: 1,
		children:   map[string][]feedComment{"100": {{id: "101", content: "旧子回复"}}},
	})
	if len(state.items[0].children) != 1 || state.items[0].children[0].id != "102" {
		t.Fatalf("old child result overwrote refreshed children: %#v", state.items[0])
	}
	if _, pending := model.commentChildPending[key]; pending {
		t.Fatal("old child result left request pending")
	}
	if _, complete := model.commentChildComplete[key]; complete {
		t.Fatal("old child result marked current generation complete")
	}
}

func TestStaleChildResultLeavesMissingChildrenAvailableForRetry(t *testing.T) {
	const stateKey = "pin:42"
	state := &commentState{
		items:      []feedComment{{id: "100", content: "新根评论", childComments: 1}},
		loaded:     true,
		generation: 2,
	}
	key := commentChildKey(stateKey, "100")
	model := &app{
		source:               &commentTestSource{},
		comments:             map[string]*commentState{stateKey: state},
		commentChildPending:  map[string]struct{}{key: {}},
		commentChildComplete: map[string]struct{}{},
	}
	model.applyCommentChildFetch(commentChildFetchResult{
		stateKey: stateKey, rootIDs: []string{"100"}, generation: 1,
		children: map[string][]feedComment{"100": {{id: "101", content: "旧子回复"}}},
	})
	model.startCommentChildFetch(context.Background(), stateKey)
	result := waitForRefreshChildFetch(t, model.commentChildFetches)
	if result.generation != state.generation {
		t.Fatalf("retried child request generation=%d, want %d", result.generation, state.generation)
	}
	model.applyCommentChildFetch(result)
	if len(state.items[0].children) != 1 || state.items[0].children[0].content != "子评论" {
		t.Fatalf("fresh child request did not recover missing children: %#v", state.items[0])
	}
}

func refreshCommentResponse(ids ...string) map[string]any {
	data := make([]any, 0, len(ids))
	for _, id := range ids {
		data = append(data, map[string]any{"id": id, "author": map[string]any{"name": "A"}, "content": "评论 " + id})
	}
	return map[string]any{"data": data, "paging": map[string]any{"is_end": true}}
}

func waitForRefreshPageRequest(t *testing.T, requests <-chan commentRefreshPageRequest) commentRefreshPageRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("comment refresh request was not made")
		return commentRefreshPageRequest{}
	}
}

func waitForRefreshChildFetch(t *testing.T, results <-chan commentChildFetchResult) commentChildFetchResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(time.Second):
		t.Fatal("child comment result was not delivered")
		return commentChildFetchResult{}
	}
}

package feedtui

import (
	"context"
	"strings"
	"testing"
	"time"
)

type commentTestSource struct {
	calls chan string
}

func (source *commentTestSource) GetFollowingFeed(context.Context, string, int) (map[string]any, error) {
	return nil, nil
}

func (source *commentTestSource) GetComments(_ context.Context, resourceType, resourceID string, _, _ int, _ string) (map[string]any, error) {
	source.calls <- resourceType + ":" + resourceID
	return map[string]any{
		"data": []any{map[string]any{
			"author": map[string]any{
				"member": map[string]any{"name": "Alice"},
			},
			"content":             `<p>评论正文</p><img src="comment.jpg">`,
			"vote_count":          8,
			"child_comment_count": 2,
		}},
	}, nil
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
	state := model.currentCommentState()
	if state == nil || len(state.items) != 1 {
		t.Fatalf("comment state=%#v", state)
	}
	view, label := formatCommentView(model.items[0], state, 0)
	for _, want := range []string{"Alice", "评论正文", "▣ 图片 1", "赞同 8", "回复 2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("comment view does not contain %q: %s", want, view)
		}
	}
	if !strings.Contains(label, "共 12 条") || !strings.Contains(label, "已加载 1 条") {
		t.Fatalf("comment label=%q", label)
	}

	model.scroll = 3
	model.toggleComments(context.Background())
	if model.commentMode || model.scroll != 5 {
		t.Fatalf("commentMode=%v scroll=%d", model.commentMode, model.scroll)
	}
}

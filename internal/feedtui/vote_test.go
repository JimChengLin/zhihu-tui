package feedtui

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

type voteTestSource struct {
	commentTestSource
	votes chan string
}

func (source *voteTestSource) SetContentVote(_ context.Context, contentType, contentID string, voted bool) (bool, error) {
	action := "neutral"
	if voted {
		action = "up"
	}
	source.votes <- contentType + ":" + action + ":" + contentID
	return true, nil
}

func (source *voteTestSource) LikeComment(_ context.Context, commentID string) (bool, error) {
	source.votes <- "comment-up:" + commentID
	return true, nil
}

func (source *voteTestSource) UnlikeComment(_ context.Context, commentID string) (bool, error) {
	source.votes <- "comment-neutral:" + commentID
	return true, nil
}

func TestToggleVoteUpdatesVisibleAndFoldedAnswer(t *testing.T) {
	source := &voteTestSource{votes: make(chan string, 2)}
	child := feedItem{
		key:          "answer:42:folded-event",
		id:           "42",
		kind:         "answer",
		stats:        "赞同 12  ·  评论 3",
		voteCount:    12,
		hasVoteCount: true,
		foldedParent: "folded:group",
	}
	model := &app{
		source: source,
		items: []feedItem{
			{key: "folded:group", kind: "folded_group", groupOpen: true, foldedItems: []feedItem{child}},
			{
				key:          "answer:42:visible-event",
				id:           "42",
				kind:         "answer",
				stats:        "赞同 12  ·  评论 3",
				voteCount:    12,
				hasVoteCount: true,
				foldedParent: "folded:group",
			},
		},
		index:       1,
		voteResults: make(chan voteResult, 1),
	}

	model.toggleVote(context.Background())
	waitForVote(t, source.votes, "answer:up:42")
	applyNextVoteResult(t, model)
	assertVoteState(t, model.items[1], true, 13)
	assertVoteState(t, model.items[0].foldedItems[0], true, 13)

	model.toggleVote(context.Background())
	waitForVote(t, source.votes, "answer:neutral:42")
	applyNextVoteResult(t, model)
	assertVoteState(t, model.items[1], false, 12)
	assertVoteState(t, model.items[0].foldedItems[0], false, 12)
}

func TestToggleVoteSupportsArticleAndPin(t *testing.T) {
	for _, kind := range []string{"article", "pin"} {
		t.Run(kind, func(t *testing.T) {
			source := &voteTestSource{votes: make(chan string, 1)}
			model := &app{
				source:      source,
				items:       []feedItem{{id: "42", kind: kind, stats: "赞同 3", voteCount: 3, hasVoteCount: true}},
				voteResults: make(chan voteResult, 1),
			}
			model.toggleVote(context.Background())
			waitForVote(t, source.votes, kind+":up:42")
			applyNextVoteResult(t, model)
			assertVoteState(t, model.items[0], true, 4)
		})
	}
}

func TestToggleVoteRejectsUnsupportedContent(t *testing.T) {
	model := &app{items: []feedItem{{kind: "question"}}}
	model.toggleVote(context.Background())
	if model.voting || model.message != "当前动态不支持赞同" {
		t.Fatalf("voting=%v message=%q", model.voting, model.message)
	}
}

func TestToggleVoteUpdatesFocusedChildComment(t *testing.T) {
	source := &voteTestSource{votes: make(chan string, 2)}
	state := &commentState{
		items: []feedComment{{
			id:       "100",
			author:   "根评论",
			children: []feedComment{{id: "101", author: "子评论", voteCount: 3}},
		}},
		loaded: true,
	}
	model := &app{
		source:            source,
		items:             []feedItem{{key: "answer:42", id: "42", kind: "answer"}},
		comments:          map[string]*commentState{"answer:42": state},
		commentMode:       true,
		pageAnchorVisible: true,
		pageAnchorLine:    2,
		metrics:           layoutMetrics{commentIDs: []string{"100", "100", "101"}},
		voteResults:       make(chan voteResult, 1),
	}

	model.toggleVote(context.Background())
	waitForVote(t, source.votes, "comment-up:101")
	applyNextVoteResult(t, model)
	child := state.items[0].children[0]
	if !child.voted || child.voteCount != 4 || model.message != "已赞同评论" {
		t.Fatalf("liked child=%#v message=%q", child, model.message)
	}

	model.toggleVote(context.Background())
	waitForVote(t, source.votes, "comment-neutral:101")
	applyNextVoteResult(t, model)
	child = state.items[0].children[0]
	if child.voted || child.voteCount != 3 || model.message != "已取消评论赞同" {
		t.Fatalf("unliked child=%#v message=%q", child, model.message)
	}
}

func TestCommentVoteRequiresBlueFocus(t *testing.T) {
	model := &app{
		items:       []feedItem{{key: "answer:42", id: "42", kind: "answer"}},
		comments:    map[string]*commentState{"answer:42": {items: []feedComment{{id: "100"}}, loaded: true}},
		commentMode: true,
	}
	model.toggleVote(context.Background())
	if model.voting || model.message != "先用 j/k 选择一条评论" {
		t.Fatalf("voting=%v message=%q", model.voting, model.message)
	}
}

func TestCommentVoteKeepsFocusSelectedByJK(t *testing.T) {
	source := &voteTestSource{votes: make(chan string, 1)}
	state := &commentState{
		items:  []feedComment{{id: "100", author: "用户", content: "评论"}},
		loaded: true,
	}
	model := &app{
		source:      source,
		items:       []feedItem{{key: "answer:42", id: "42", kind: "answer"}},
		comments:    map[string]*commentState{"answer:42": state},
		commentMode: true,
		metrics: layoutMetrics{
			bodyHeight: 4,
			commentIDs: []string{"100", "100"},
		},
		voteResults: make(chan voteResult, 1),
	}

	model.handleKey(context.Background(), "j")
	if !model.pageAnchorVisible || model.pageAnchorLine != 0 {
		t.Fatalf("comment focus=(%d, %v)", model.pageAnchorLine, model.pageAnchorVisible)
	}
	model.handleKey(context.Background(), "v")
	waitForVote(t, source.votes, "comment-up:100")
	if !model.pageAnchorVisible || model.pageAnchorLine != 0 {
		t.Fatalf("comment focus was cleared by v: (%d, %v)", model.pageAnchorLine, model.pageAnchorVisible)
	}
	applyNextVoteResult(t, model)
	if !state.items[0].voted || state.items[0].voteCount != 1 {
		t.Fatalf("comment vote state=%#v", state.items[0])
	}
}

func TestParseCommentReadsLikeRelationship(t *testing.T) {
	comment := parseComment(map[string]any{"id": "100", "content": "评论", "like_count": 7, "liked": true})
	if !comment.voted || comment.voteCount != 7 {
		t.Fatalf("comment=%#v", comment)
	}
}

func TestRenderLikedCommentState(t *testing.T) {
	state := &commentState{items: []feedComment{{id: "100", author: "用户", content: "评论", voteCount: 8, voted: true}}, loaded: true}
	view, _ := formatCommentView(feedItem{}, state, 0)
	if !strings.Contains(view, "✓ 赞同 8") {
		t.Fatalf("comment view=%q", view)
	}
}

func TestParseFeedItemReadsVoteRelationship(t *testing.T) {
	item, ok := parseFeedItem(map[string]any{
		"target": map[string]any{
			"type":         "answer",
			"id":           "42",
			"voteup_count": 7,
			"relationship": map[string]any{"voting": 1},
			"question":     map[string]any{"title": "问题"},
		},
	})
	if !ok || !item.voted || !item.hasVoteCount || item.voteCount != 7 {
		t.Fatalf("item=%#v ok=%v", item, ok)
	}
}

func TestRenderVoteState(t *testing.T) {
	model := &app{
		width:  100,
		height: 24,
		items: []feedItem{{
			kind:  "answer",
			title: "问题",
			body:  "回答",
			stats: "赞同 13",
			voted: true,
		}},
	}
	lines, _ := renderSingleApp(model)
	var rendered strings.Builder
	for _, line := range lines {
		rendered.WriteString(line.text)
		rendered.WriteByte('\n')
	}
	if !strings.Contains(rendered.String(), "✓ 已赞同  ·  赞同 13") {
		t.Fatalf("rendered vote state:\n%s", rendered.String())
	}
}

func waitForVote(t *testing.T, votes <-chan string, want string) {
	t.Helper()
	select {
	case got := <-votes:
		if got != want {
			t.Fatalf("vote request=%q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("vote request was not made")
	}
}

func applyNextVoteResult(t *testing.T, model *app) {
	t.Helper()
	select {
	case result := <-model.voteResults:
		model.applyVote(result)
	case <-time.After(time.Second):
		t.Fatal("vote result was not delivered")
	}
}

func assertVoteState(t *testing.T, item feedItem, voted bool, count int64) {
	t.Helper()
	if item.voted != voted || item.voteCount != count || !strings.Contains(item.stats, "赞同 "+strconv.FormatInt(count, 10)) {
		t.Fatalf("item=%#v", item)
	}
}

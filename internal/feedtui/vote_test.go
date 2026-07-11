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

func (source *voteTestSource) VoteUp(_ context.Context, answerID string) (bool, error) {
	source.votes <- "up:" + answerID
	return true, nil
}

func (source *voteTestSource) VoteNeutral(_ context.Context, answerID string) (bool, error) {
	source.votes <- "neutral:" + answerID
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
	waitForVote(t, source.votes, "up:42")
	applyNextVoteResult(t, model)
	assertVoteState(t, model.items[1], true, 13)
	assertVoteState(t, model.items[0].foldedItems[0], true, 13)

	model.toggleVote(context.Background())
	waitForVote(t, source.votes, "neutral:42")
	applyNextVoteResult(t, model)
	assertVoteState(t, model.items[1], false, 12)
	assertVoteState(t, model.items[0].foldedItems[0], false, 12)
}

func TestToggleVoteRejectsNonAnswer(t *testing.T) {
	model := &app{items: []feedItem{{kind: "pin"}}}
	model.toggleVote(context.Background())
	if model.voting || model.message != "当前仅支持赞同回答" {
		t.Fatalf("voting=%v message=%q", model.voting, model.message)
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

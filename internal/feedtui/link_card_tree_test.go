package feedtui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type linkCardTreeSource struct {
	mu      sync.Mutex
	details map[string]map[string]any
	calls   map[string]int
}

func (source *linkCardTreeSource) GetPin(_ context.Context, id string) (map[string]any, error) {
	return source.get("PIN", id)
}

func (source *linkCardTreeSource) GetQuestion(_ context.Context, id string) (map[string]any, error) {
	return source.get("QUESTION", id)
}

func (source *linkCardTreeSource) GetAnswer(_ context.Context, id string) (map[string]any, error) {
	return source.get("ANSWER", id)
}

func (source *linkCardTreeSource) GetArticle(_ context.Context, id string) (map[string]any, error) {
	return source.get("ARTICLE", id)
}

func (source *linkCardTreeSource) get(kind, id string) (map[string]any, error) {
	source.mu.Lock()
	defer source.mu.Unlock()

	key := kind + ":" + id
	source.calls[key]++
	detail, found := source.details[key]
	if !found {
		return nil, fmt.Errorf("missing link card detail for %s", key)
	}
	return detail, nil
}

func (source *linkCardTreeSource) callCount(kind, id string) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.calls[kind+":"+id]
}

func newLinkCardTreeSource(details map[string]map[string]any) *linkCardTreeSource {
	return &linkCardTreeSource{
		details: details,
		calls:   make(map[string]int),
	}
}

func linkCardTreeNode(kind, id, draftTitle string) map[string]any {
	return map[string]any{
		"type":              "link_card",
		"data_content_type": kind,
		"data_content_id":   id,
		"data_draft_title":  draftTitle,
	}
}

func linkCardTreePinDetail(title, excerpt string, children ...map[string]any) map[string]any {
	content := []any{map[string]any{
		"type":    "text",
		"content": title + " | <p>" + excerpt + "</p>",
	}}
	for _, child := range children {
		content = append(content, child)
	}
	return map[string]any{"content": content}
}

func linkCardTreeResponse(cards ...map[string]any) map[string]any {
	content := []any{map[string]any{"type": "text", "content": "外层想法正文。"}}
	for _, card := range cards {
		content = append(content, card)
	}
	return map[string]any{
		"data": []any{map[string]any{
			"target": map[string]any{
				"id":      "outer-pin",
				"type":    "pin",
				"content": content,
			},
		}},
	}
}

func layoutLinkCardTreeResponse(t *testing.T, response map[string]any) []styledLine {
	t.Helper()
	items := parseFeedItems(asSlice(response["data"]))
	if len(items) != 1 {
		t.Fatalf("items=%d, want 1", len(items))
	}
	return layoutBodyLines(items[0].body, 120)
}

func requireLinkCardTreeLine(t *testing.T, lines []styledLine, prefix, text, textStyle string) {
	t.Helper()
	for _, line := range lines {
		if line.text != prefix || line.middle != text {
			continue
		}
		if line.style != ansiDim || line.middleStyle != textStyle {
			t.Fatalf("link card line styles prefix=%q text=%q: prefix style=%q text style=%q", prefix, text, line.style, line.middleStyle)
		}
		return
	}
	t.Fatalf("link card tree has no line prefix=%q text=%q: %q", prefix, text, visibleLinkCardTree(lines))
}

func visibleLinkCardTree(lines []styledLine) string {
	visible := make([]string, len(lines))
	for index, line := range lines {
		visible[index] = styledLineText(line)
	}
	return strings.Join(visible, "\n")
}

func TestLinkCardTreeHydratesPinPinAnswerRecursively(t *testing.T) {
	answer := linkCardTreeNode("ANSWER", "answer-1", "引用回答")
	childPin := linkCardTreeNode("PIN", "pin-2", "引用想法")
	rootPin := linkCardTreeNode("PIN", "pin-1", "引用想法")
	source := newLinkCardTreeSource(map[string]map[string]any{
		"PIN:pin-1": linkCardTreePinDetail("一级想法", "一级摘要。", childPin),
		"PIN:pin-2": linkCardTreePinDetail("二级想法", "二级摘要。", answer),
		"ANSWER:answer-1": {
			"question":      map[string]any{"title": "内层问题"},
			"content":       "<p>回答摘要。</p>",
			"author":        map[string]any{"name": "答主"},
			"voteup_count":  12,
			"comment_count": 3,
		},
	})
	response := linkCardTreeResponse(rootPin)

	hydrateFeedLinkCards(context.Background(), source, response)

	for _, ref := range []struct {
		kind string
		id   string
	}{
		{kind: "PIN", id: "pin-1"},
		{kind: "PIN", id: "pin-2"},
		{kind: "ANSWER", id: "answer-1"},
	} {
		if calls := source.callCount(ref.kind, ref.id); calls != 1 {
			t.Fatalf("%s:%s calls=%d, want 1", ref.kind, ref.id, calls)
		}
	}

	lines := layoutLinkCardTreeResponse(t, response)
	requireLinkCardTreeLine(t, lines, "└─ ", "一级想法", ansiBlue)
	requireLinkCardTreeLine(t, lines, "   └─ ", "二级想法", ansiBlue)
	requireLinkCardTreeLine(t, lines, "      └─ ", "内层问题", ansiBlue)
	requireLinkCardTreeLine(t, lines, "         ", "答主  ·  赞同 12  ·  评论 3  ·  回答", ansiDim)
}

func TestLinkCardTreeConnectsSiblingReferences(t *testing.T) {
	firstAnswer := linkCardTreeNode("ANSWER", "answer-1", "引用回答")
	secondAnswer := linkCardTreeNode("ANSWER", "answer-2", "引用回答")
	rootPin := linkCardTreeNode("PIN", "pin-parent", "引用想法")
	source := newLinkCardTreeSource(map[string]map[string]any{
		"PIN:pin-parent": linkCardTreePinDetail("父想法", "父摘要。", firstAnswer, secondAnswer),
		"ANSWER:answer-1": {
			"question": map[string]any{"title": "第一个问题"},
			"content":  "<p>第一段回答。</p>",
		},
		"ANSWER:answer-2": {
			"question": map[string]any{"title": "第二个问题"},
			"content":  "<p>第二段回答。</p>",
		},
	})
	response := linkCardTreeResponse(rootPin)

	hydrateFeedLinkCards(context.Background(), source, response)

	lines := layoutLinkCardTreeResponse(t, response)
	requireLinkCardTreeLine(t, lines, "   ├─ ", "第一个问题", ansiBlue)
	requireLinkCardTreeLine(t, lines, "   │  ", "第一段回答。", "")
	requireLinkCardTreeLine(t, lines, "   └─ ", "第二个问题", ansiBlue)
}

func TestLinkCardTreeHydratesDuplicateReferenceOnce(t *testing.T) {
	first := linkCardTreeNode("ANSWER", "same-answer", "引用回答")
	second := linkCardTreeNode("ANSWER", "same-answer", "引用回答")
	source := newLinkCardTreeSource(map[string]map[string]any{
		"ANSWER:same-answer": {
			"question": map[string]any{"title": "同一个问题"},
			"content":  "<p>同一个回答摘要。</p>",
		},
	})
	response := linkCardTreeResponse(first, second)

	hydrateFeedLinkCards(context.Background(), source, response)

	if calls := source.callCount("ANSWER", "same-answer"); calls != 1 {
		t.Fatalf("duplicate answer calls=%d, want 1", calls)
	}
	rendered := visibleLinkCardTree(layoutLinkCardTreeResponse(t, response))
	if count := strings.Count(rendered, "同一个问题"); count != 2 {
		t.Fatalf("hydrated duplicate card count=%d, want 2: %q", count, rendered)
	}
}

func TestLinkCardTreeStopsReferenceCycle(t *testing.T) {
	backToA := linkCardTreeNode("PIN", "pin-a", "引用想法")
	toB := linkCardTreeNode("PIN", "pin-b", "引用想法")
	root := linkCardTreeNode("PIN", "pin-a", "引用想法")
	source := newLinkCardTreeSource(map[string]map[string]any{
		"PIN:pin-a": linkCardTreePinDetail("想法 A", "A 摘要。", toB),
		"PIN:pin-b": linkCardTreePinDetail("想法 B", "B 摘要。", backToA),
	})
	response := linkCardTreeResponse(root)

	hydrateFeedLinkCards(context.Background(), source, response)

	if calls := source.callCount("PIN", "pin-a"); calls != 1 {
		t.Fatalf("pin-a calls=%d, want 1", calls)
	}
	if calls := source.callCount("PIN", "pin-b"); calls != 1 {
		t.Fatalf("pin-b calls=%d, want 1", calls)
	}
	rendered := visibleLinkCardTree(layoutLinkCardTreeResponse(t, response))
	if !strings.Contains(rendered, "想法 A（循环引用）") {
		t.Fatalf("cycle marker is missing: %q", rendered)
	}
}

func TestLinkCardTreeTreatsFeedTargetAsCycleRoot(t *testing.T) {
	backToRoot := linkCardTreeNode("PIN", "pin-a", "外层想法 A")
	toB := linkCardTreeNode("PIN", "pin-b", "引用想法")
	source := newLinkCardTreeSource(map[string]map[string]any{
		"PIN:pin-b": linkCardTreePinDetail("想法 B", "B 摘要。", backToRoot),
	})
	response := map[string]any{
		"data": []any{map[string]any{
			"target": map[string]any{
				"id":   "pin-a",
				"type": "pin",
				"content": []any{
					map[string]any{"type": "text", "content": "外层想法 A 的正文。"},
					toB,
				},
			},
		}},
	}

	hydrateFeedLinkCards(context.Background(), source, response)

	if calls := source.callCount("PIN", "pin-b"); calls != 1 {
		t.Fatalf("pin-b calls=%d, want 1", calls)
	}
	if calls := source.callCount("PIN", "pin-a"); calls != 0 {
		t.Fatalf("feed root pin-a calls=%d, want 0", calls)
	}
	rendered := visibleLinkCardTree(layoutLinkCardTreeResponse(t, response))
	if !strings.Contains(rendered, "外层想法 A（循环引用）") {
		t.Fatalf("feed-root cycle marker is missing: %q", rendered)
	}
}

func TestLinkCardTreeStopsHydratingBeyondDepthLimit(t *testing.T) {
	details := make(map[string]map[string]any)
	for depth := 1; depth <= maxLinkCardDepth; depth++ {
		child := linkCardTreeNode("PIN", fmt.Sprintf("pin-%d", depth+1), fmt.Sprintf("第 %d 层", depth+1))
		details[fmt.Sprintf("PIN:pin-%d", depth)] = linkCardTreePinDetail(
			fmt.Sprintf("第 %d 层", depth),
			fmt.Sprintf("第 %d 层摘要。", depth),
			child,
		)
	}
	source := newLinkCardTreeSource(details)
	response := linkCardTreeResponse(linkCardTreeNode("PIN", "pin-1", "引用想法"))

	hydrateFeedLinkCards(context.Background(), source, response)

	for depth := 1; depth <= maxLinkCardDepth; depth++ {
		if calls := source.callCount("PIN", fmt.Sprintf("pin-%d", depth)); calls != 1 {
			t.Fatalf("pin-%d calls=%d, want 1", depth, calls)
		}
	}
	if calls := source.callCount("PIN", fmt.Sprintf("pin-%d", maxLinkCardDepth+1)); calls != 0 {
		t.Fatalf("pin beyond depth limit calls=%d, want 0", calls)
	}
	rendered := visibleLinkCardTree(layoutLinkCardTreeResponse(t, response))
	want := fmt.Sprintf("第 %d 层（引用超过 %d 层，停止展开）", maxLinkCardDepth+1, maxLinkCardDepth)
	if !strings.Contains(rendered, want) {
		t.Fatalf("depth limit marker %q is missing: %q", want, rendered)
	}
}

func TestQuestionLinkCardUsesQuestionTitleAndStats(t *testing.T) {
	question := linkCardTreeNode("QUESTION", "question-1", "引用问题")
	source := newLinkCardTreeSource(map[string]map[string]any{
		"QUESTION:question-1": {
			"title":          "你是因为什么才成为一个建制派的？",
			"follower_count": 390,
			"answer_count":   76,
			"comment_count":  8,
		},
	})
	response := linkCardTreeResponse(question)

	hydrateFeedLinkCards(context.Background(), source, response)

	if calls := source.callCount("QUESTION", "question-1"); calls != 1 {
		t.Fatalf("question calls=%d, want 1", calls)
	}
	lines := layoutLinkCardTreeResponse(t, response)
	requireLinkCardTreeLine(t, lines, "└─ ", "你是因为什么才成为一个建制派的？", ansiBlue)
	requireLinkCardTreeLine(t, lines, "   ", "关注 390  ·  回答 76  ·  评论 8  ·  问题", ansiDim)
}

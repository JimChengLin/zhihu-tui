package feedtui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type retryLinkCardSource struct {
	*commentTestSource
	attempts int
}

func TestPageScrollAmountKeepsThreeContextLines(t *testing.T) {
	for _, test := range []struct {
		bodyHeight int
		want       int
	}{
		{bodyHeight: 48, want: 45},
		{bodyHeight: 8, want: 5},
		{bodyHeight: 3, want: 1},
		{bodyHeight: 1, want: 1},
	} {
		if got := pageScrollAmount(test.bodyHeight); got != test.want {
			t.Fatalf("pageScrollAmount(%d)=%d, want %d", test.bodyHeight, got, test.want)
		}
	}
}

func (source *retryLinkCardSource) GetQuestion(context.Context, string) (map[string]any, error) {
	source.attempts++
	if source.attempts == 1 {
		return nil, errors.New("temporary failure")
	}
	return map[string]any{
		"title":          "重试成功的问题",
		"follower_count": 10,
		"answer_count":   2,
	}, nil
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
	if model.scroll != 5 || model.index != 0 || !model.pageAnchorVisible || model.pageAnchorLine != 8 {
		t.Fatalf("first space state: index=%d scroll=%d anchor=(%d, %v)", model.index, model.scroll, model.pageAnchorLine, model.pageAnchorVisible)
	}
	model.handleKey(ctx, " ")
	if model.scroll != 8 || model.index != 0 || model.boundarySwitchKey != "" {
		t.Fatalf("space landing at bottom armed item switch: index=%d scroll=%d key=%q", model.index, model.scroll, model.boundarySwitchKey)
	}
	if model.message != "" {
		t.Fatalf("bottom landing unexpectedly showed confirmation: %q", model.message)
	}
	if !model.pageAnchorVisible || model.pageAnchorLine != 13 {
		t.Fatalf("space continuation anchor=(%d, %v), want first unread line 13", model.pageAnchorLine, model.pageAnchorVisible)
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
	if model.scroll != 3 || model.index != 1 || !model.pageAnchorVisible || model.pageAnchorLine != 7 {
		t.Fatalf("first b state: index=%d scroll=%d anchor=(%d, %v)", model.index, model.scroll, model.pageAnchorLine, model.pageAnchorVisible)
	}
	model.handleKey(ctx, "b")
	if model.scroll != 0 || model.index != 1 || model.boundarySwitchKey != "" {
		t.Fatalf("b landing at top armed item switch: index=%d scroll=%d key=%q", model.index, model.scroll, model.boundarySwitchKey)
	}
	if model.message != "" {
		t.Fatalf("top landing unexpectedly showed confirmation: %q", model.message)
	}
	if !model.pageAnchorVisible || model.pageAnchorLine != 2 {
		t.Fatalf("b continuation anchor=(%d, %v), want first unread line 2", model.pageAnchorLine, model.pageAnchorVisible)
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

func TestNPRevisitRetriesFailedLinkCardDetail(t *testing.T) {
	linkCard := map[string]any{
		"type":              "link_card",
		"data_content_type": "QUESTION",
		"data_content_id":   "question-1",
		"data_draft_title":  "原始问题标题",
	}
	applyLinkCardResult(linkCard, nil, errors.New("initial failure"))
	raw := map[string]any{
		"target": map[string]any{
			"id":      "pin-1",
			"type":    "pin",
			"content": []any{linkCard},
		},
	}
	failedItem, ok := parseFeedItem(raw)
	if !ok {
		t.Fatal("failed item was not parsed")
	}
	source := &retryLinkCardSource{commentTestSource: &commentTestSource{}}
	model := &app{
		source:          source,
		items:           []feedItem{{key: "previous"}, failedItem},
		linkCardRetries: map[string]int{},
		linkCardFetches: make(chan linkCardRetryResult, 1),
	}

	initialFailure := model.items[1].body
	model.handleKey(context.Background(), "n")
	if model.items[1].body != initialFailure || strings.Contains(model.items[1].body, "正在重试") {
		t.Fatalf("retry changed the failure text before completion: %q", model.items[1].body)
	}
	applyNextLinkCardRetry(t, model)
	if source.attempts != 1 || !strings.Contains(model.items[1].body, "详情加载失败（已重试 1 次）") {
		t.Fatalf("first retry attempts=%d body=%q", source.attempts, model.items[1].body)
	}

	model.handleKey(context.Background(), "p")
	model.handleKey(context.Background(), "n")
	if !strings.Contains(model.items[1].body, "详情加载失败（已重试 1 次）") || strings.Contains(model.items[1].body, "正在重试") {
		t.Fatalf("second retry changed the failure text before completion: %q", model.items[1].body)
	}
	applyNextLinkCardRetry(t, model)
	if source.attempts != 2 {
		t.Fatalf("retry attempts=%d, want 2", source.attempts)
	}
	if strings.Contains(model.items[1].body, "详情加载失败") || !strings.Contains(model.items[1].body, "重试成功的问题") {
		t.Fatalf("successful retry did not replace the failure: %q", model.items[1].body)
	}
}

func applyNextLinkCardRetry(t *testing.T, model *app) {
	t.Helper()
	select {
	case result := <-model.linkCardFetches:
		model.applyLinkCardRetry(result)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for link card retry")
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

func TestHalfPageKeysKeepContinuationAnchorWithoutSwitchingItem(t *testing.T) {
	model := &app{
		items:   []feedItem{{key: "1"}, {key: "2"}},
		metrics: layoutMetrics{bodyHeight: 8, bodyLines: 24, maxScroll: 16},
	}
	model.handleKey(context.Background(), "d")
	if model.scroll != 4 || model.index != 0 || !model.pageAnchorVisible || model.pageAnchorLine != 8 {
		t.Fatalf("d scroll=%d index=%d anchor=(%d,%v)", model.scroll, model.index, model.pageAnchorLine, model.pageAnchorVisible)
	}
	model.handleKey(context.Background(), "u")
	if model.scroll != 0 || model.index != 0 || !model.pageAnchorVisible || model.pageAnchorLine != 3 {
		t.Fatalf("u scroll=%d index=%d anchor=(%d,%v)", model.scroll, model.index, model.pageAnchorLine, model.pageAnchorVisible)
	}
	model.scroll = model.metrics.maxScroll
	model.handleKey(context.Background(), "d")
	model.handleKey(context.Background(), "d")
	if model.index != 0 || model.boundarySwitchKey != "" || model.pageAnchorLine != 23 {
		t.Fatalf("bottom d index=%d boundary=%q anchor=%d", model.index, model.boundarySwitchKey, model.pageAnchorLine)
	}
}

func TestFKeyPagesDownAndConfirmsNextItem(t *testing.T) {
	model := &app{
		items:   []feedItem{{key: "1"}, {key: "2"}},
		metrics: layoutMetrics{bodyHeight: 8, bodyLines: 16, maxScroll: 8},
	}
	model.handleKey(context.Background(), "f")
	if model.scroll != 5 || model.index != 0 || model.pageAnchorLine != 8 {
		t.Fatalf("first f scroll=%d index=%d anchor=%d", model.scroll, model.index, model.pageAnchorLine)
	}
	model.handleKey(context.Background(), "f")
	if model.scroll != 8 || model.index != 0 || model.pageAnchorLine != 13 {
		t.Fatalf("second f scroll=%d index=%d anchor=%d", model.scroll, model.index, model.pageAnchorLine)
	}
	model.handleKey(context.Background(), "f")
	if model.index != 0 || model.boundarySwitchKey != "f" || !strings.Contains(model.message, "再按一次 f") {
		t.Fatalf("boundary f index=%d key=%q message=%q", model.index, model.boundarySwitchKey, model.message)
	}
	model.handleKey(context.Background(), "f")
	if model.index != 1 {
		t.Fatalf("confirmed f index=%d", model.index)
	}
}

func TestHalfPageScrollDoesNotUseControlDU(t *testing.T) {
	model := &app{
		items:   []feedItem{{key: "1"}},
		scroll:  4,
		metrics: layoutMetrics{bodyHeight: 8, bodyLines: 24, maxScroll: 16},
	}
	model.handleKey(context.Background(), keyCtrlD)
	model.handleKey(context.Background(), keyCtrlU)
	if model.scroll != 4 || model.pageAnchorVisible {
		t.Fatalf("Ctrl-D/U changed reading position: scroll=%d anchor=%v", model.scroll, model.pageAnchorVisible)
	}
}

func TestVimControlKeysScrollAndConfirmBoundaries(t *testing.T) {
	model := &app{
		items:   []feedItem{{key: "1"}, {key: "2"}},
		metrics: layoutMetrics{bodyHeight: 8, bodyLines: 24, maxScroll: 16},
	}
	model.handleKey(context.Background(), keyCtrlE)
	if model.scroll != 1 {
		t.Fatalf("Ctrl-E scroll=%d", model.scroll)
	}
	model.handleKey(context.Background(), keyCtrlY)
	if model.scroll != 0 {
		t.Fatalf("Ctrl-Y scroll=%d", model.scroll)
	}

	model.scroll = model.metrics.maxScroll
	model.handleKey(context.Background(), keyCtrlF)
	if model.boundarySwitchKey != keyCtrlF || !strings.Contains(model.message, "再按一次 Ctrl-F") {
		t.Fatalf("Ctrl-F boundary=%q message=%q", model.boundarySwitchKey, model.message)
	}
	model.handleKey(context.Background(), keyCtrlF)
	if model.index != 1 {
		t.Fatalf("confirmed Ctrl-F index=%d", model.index)
	}
}

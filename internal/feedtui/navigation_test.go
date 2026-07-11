package feedtui

import (
	"context"
	"strings"
	"testing"
)

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
	if model.scroll != 7 || model.index != 0 {
		t.Fatalf("first space did not move down seven eighths of a page: index=%d scroll=%d", model.index, model.scroll)
	}
	model.handleKey(ctx, " ")
	if model.scroll != 8 || model.index != 0 || model.boundarySwitchKey != "" {
		t.Fatalf("space landing at bottom armed item switch: index=%d scroll=%d key=%q", model.index, model.scroll, model.boundarySwitchKey)
	}
	if model.message != "" {
		t.Fatalf("bottom landing unexpectedly showed confirmation: %q", model.message)
	}
	if !model.pageAnchorVisible || model.pageAnchorLine != 14 {
		t.Fatalf("space continuation anchor=(%d, %v), want previous last line 14", model.pageAnchorLine, model.pageAnchorVisible)
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
	if model.scroll != 1 || model.index != 1 {
		t.Fatalf("first b did not move up seven eighths of a page: index=%d scroll=%d", model.index, model.scroll)
	}
	model.handleKey(ctx, "b")
	if model.scroll != 0 || model.index != 1 || model.boundarySwitchKey != "" {
		t.Fatalf("b landing at top armed item switch: index=%d scroll=%d key=%q", model.index, model.scroll, model.boundarySwitchKey)
	}
	if model.message != "" {
		t.Fatalf("top landing unexpectedly showed confirmation: %q", model.message)
	}
	if !model.pageAnchorVisible || model.pageAnchorLine != 1 {
		t.Fatalf("b continuation anchor=(%d, %v), want previous first line 1", model.pageAnchorLine, model.pageAnchorVisible)
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
	if model.scroll != 4 || model.index != 0 || !model.pageAnchorVisible || model.pageAnchorLine != 7 {
		t.Fatalf("d scroll=%d index=%d anchor=(%d,%v)", model.scroll, model.index, model.pageAnchorLine, model.pageAnchorVisible)
	}
	model.handleKey(context.Background(), "u")
	if model.scroll != 0 || model.index != 0 || !model.pageAnchorVisible || model.pageAnchorLine != 4 {
		t.Fatalf("u scroll=%d index=%d anchor=(%d,%v)", model.scroll, model.index, model.pageAnchorLine, model.pageAnchorVisible)
	}
	model.scroll = model.metrics.maxScroll
	model.handleKey(context.Background(), "d")
	model.handleKey(context.Background(), "d")
	if model.index != 0 || model.boundarySwitchKey != "" || model.pageAnchorLine != 23 {
		t.Fatalf("bottom d index=%d boundary=%q anchor=%d", model.index, model.boundarySwitchKey, model.pageAnchorLine)
	}
}

func TestVimControlKeysScrollAndConfirmBoundaries(t *testing.T) {
	model := &app{
		items:   []feedItem{{key: "1"}, {key: "2"}},
		metrics: layoutMetrics{bodyHeight: 8, bodyLines: 24, maxScroll: 16},
	}
	model.handleKey(context.Background(), keyCtrlD)
	if model.scroll != 4 || model.pageAnchorLine != 7 {
		t.Fatalf("Ctrl-D scroll=%d anchor=%d", model.scroll, model.pageAnchorLine)
	}
	model.handleKey(context.Background(), keyCtrlU)
	if model.scroll != 0 || model.pageAnchorLine != 4 {
		t.Fatalf("Ctrl-U scroll=%d anchor=%d", model.scroll, model.pageAnchorLine)
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

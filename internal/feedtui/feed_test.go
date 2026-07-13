package feedtui

import (
	"context"
	"strings"
	"testing"
)

type pinCardTestSource struct {
	detail       map[string]any
	answerDetail map[string]any
	calls        []string
	answerCalls  []string
}

func (source *pinCardTestSource) GetPin(_ context.Context, id string) (map[string]any, error) {
	source.calls = append(source.calls, id)
	return source.detail, nil
}

func (source *pinCardTestSource) GetAnswer(_ context.Context, id string) (map[string]any, error) {
	source.answerCalls = append(source.answerCalls, id)
	return source.answerDetail, nil
}

func TestParseFeedItemFormatsFollowingActivity(t *testing.T) {
	raw := map[string]any{
		"id":           "activity-1",
		"action_text":  `<a href="/people/alice">Alice</a>赞同了回答`,
		"created_time": 1_700_000_000,
		"target": map[string]any{
			"id":            "456",
			"type":          "answer",
			"content":       `<p>第一段。</p><p>第二段。<img src="x.jpg"></p>`,
			"voteup_count":  12000,
			"comment_count": 7,
			"author": map[string]any{
				"name":     "Bob",
				"headline": "第一行\n第二行",
			},
			"question": map[string]any{
				"id":    "123",
				"title": "测试问题",
			},
		},
	}

	item, ok := parseFeedItem(raw)
	if !ok {
		t.Fatal("parseFeedItem returned false")
	}
	if item.action != "Alice 赞同了回答" {
		t.Fatalf("action=%q", item.action)
	}
	if item.key != "answer:456:Alice 赞同了回答" {
		t.Fatalf("key=%q, want stable target and action identity", item.key)
	}
	if item.title != "测试问题" {
		t.Fatalf("title=%q", item.title)
	}
	if item.body != "第一段。\n\n第二段。\n▣ 图片 1" {
		t.Fatalf("body=%q", item.body)
	}
	if item.headline != "第一行 第二行" {
		t.Fatalf("headline=%q", item.headline)
	}
	if item.stats != "赞同 1.2万  ·  评论 7" {
		t.Fatalf("stats=%q", item.stats)
	}
	if !item.hasCommentCount || item.commentCount != 7 {
		t.Fatalf("comment count=%d known=%v", item.commentCount, item.hasCommentCount)
	}
	if item.url != "https://www.zhihu.com/question/123/answer/456" {
		t.Fatalf("url=%q", item.url)
	}
	if item.imageCount != 1 {
		t.Fatalf("imageCount=%d", item.imageCount)
	}
}

func TestParseFeedItemFormatsStructuredPinContent(t *testing.T) {
	raw := map[string]any{
		"id":          "activity-pin",
		"action_text": "Alice 发布了想法",
		"target": map[string]any{
			"id":   "789",
			"type": "pin",
			"content": []any{
				map[string]any{"type": "text", "content": "想法标题\n\n想法正文"},
				map[string]any{"type": "image", "url": "https://example.com/image.jpg"},
			},
			"author": map[string]any{"name": "Alice"},
		},
	}

	item, ok := parseFeedItem(raw)
	if !ok {
		t.Fatal("parseFeedItem returned false")
	}
	if item.title != "想法标题" {
		t.Fatalf("title=%q", item.title)
	}
	if item.body != "想法标题\n\n想法正文\n\n▣ 图片 1" {
		t.Fatalf("body=%q", item.body)
	}
	if item.imageCount != 1 {
		t.Fatalf("imageCount=%d", item.imageCount)
	}
}

func TestTitledPinSeparatesOfficialTitleFromBody(t *testing.T) {
	raw := map[string]any{
		"id":          "activity-pin",
		"action_text": "一直住顶楼发布了想法",
		"target": map[string]any{
			"id":   "789",
			"type": "pin",
			"content": []any{
				map[string]any{
					"type":    "text",
					"content": "关于丁克vs生孩子<br><p>这两天有个粉丝加我微信。</p><p>我的回答就不贴了。</p>",
				},
			},
			"author": map[string]any{"name": "一直住顶楼"},
		},
	}

	item, ok := parseFeedItem(raw)
	if !ok {
		t.Fatal("parseFeedItem returned false")
	}
	if item.title != "关于丁克vs生孩子" || item.pinTitle != item.title {
		t.Fatalf("title=%q pinTitle=%q", item.title, item.pinTitle)
	}
	if item.body != "这两天有个粉丝加我微信。\n\n我的回答就不贴了。" {
		t.Fatalf("body=%q", item.body)
	}

	model := &app{items: []feedItem{item}, width: 100, height: 16}
	lines, _ := renderSingleApp(model)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(rendered, "关于丁克vs生孩子") {
		t.Fatalf("titled pin has no visible title: %q", rendered)
	}
	if strings.Contains(rendered, "关于丁克vs生孩子 |") {
		t.Fatalf("titled pin has a redundant separator: %q", rendered)
	}
	if strings.Count(rendered, "关于丁克vs生孩子") != 1 {
		t.Fatalf("titled pin title rendered more than once: %q", rendered)
	}
	for _, line := range lines {
		if strings.Contains(line.text, "关于丁克vs生孩子") && line.style != ansiBold+ansiBlue {
			t.Fatalf("titled pin title style=%q, want blue title", line.style)
		}
	}
}

func TestTitleOnlyPinLeavesBodyBlank(t *testing.T) {
	raw := map[string]any{
		"target": map[string]any{
			"id":      "789",
			"type":    "pin",
			"content": []any{map[string]any{"type": "text", "content": "只有标题<br><p></p>"}},
		},
	}
	item, ok := parseFeedItem(raw)
	if !ok {
		t.Fatal("parseFeedItem returned false")
	}
	if item.pinTitle != "只有标题" || item.body != "" {
		t.Fatalf("pinTitle=%q body=%q", item.pinTitle, item.body)
	}

	lines, _ := renderSingleApp(&app{items: []feedItem{item}, width: 100, height: 16})
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(rendered, "只有标题") || strings.Contains(rendered, "没有正文摘要") {
		t.Fatalf("title-only pin did not preserve a blank body: %q", rendered)
	}
}

func TestSingleParagraphPinRendersAsCompleteBody(t *testing.T) {
	raw := map[string]any{
		"id":          "activity-pin",
		"action_text": "uncle creepy 发布了想法",
		"target": map[string]any{
			"id":      "789",
			"type":    "pin",
			"content": "最终判了五年 罪行是骗保 美国也是醉了[捂嘴]",
			"author":  map[string]any{"name": "uncle creepy"},
		},
	}
	item, ok := parseFeedItem(raw)
	if !ok {
		t.Fatal("parseFeedItem returned false")
	}
	if item.title != item.body {
		t.Fatalf("single-paragraph pin title=%q body=%q, want sidebar title and complete body", item.title, item.body)
	}
	model := &app{items: []feedItem{item}, width: 100, height: 16}
	lines, _ := renderSingleApp(model)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if strings.Contains(rendered, "这条动态没有正文摘要") {
		t.Fatalf("complete pin was rendered as missing content: %q", rendered)
	}
	contentLines := 0
	authorMentions := 0
	for _, line := range lines {
		authorMentions += strings.Count(line.text, item.author)
		if strings.Contains(line.text, item.body) {
			contentLines++
			if line.style != "" {
				t.Fatalf("pin body style=%q, want normal body text", line.style)
			}
		}
	}
	if contentLines != 1 {
		t.Fatalf("pin body rendered %d times, want once: %q", contentLines, rendered)
	}
	if authorMentions != 1 {
		t.Fatalf("pin author rendered %d times, want only the action line: %q", authorMentions, rendered)
	}
}

func TestPinLinkCardLoadsAndRendersReferencedPin(t *testing.T) {
	linkCard := map[string]any{
		"type":              "link_card",
		"data_content_type": "PIN",
		"data_content_id":   "linked-pin",
		"data_draft_title":  "引用想法",
		"url":               "https://www.zhihu.com/pin/linked-pin",
	}
	response := map[string]any{
		"data": []any{map[string]any{
			"action_text": "uncle creepy发布了想法",
			"target": map[string]any{
				"id":      "outer-pin",
				"type":    "pin",
				"author":  map[string]any{"name": "uncle creepy"},
				"content": []any{map[string]any{"type": "text", "content": "最终判了五年"}, linkCard},
			},
		}},
	}
	source := &pinCardTestSource{detail: map[string]any{
		"content": []any{
			map[string]any{"type": "text", "content": "北大才女用算法贩毒一年赚 1 亿美金，最终被判30年 | <p>正文</p>"},
			map[string]any{"type": "image", "url": "cover.jpg"},
		},
		"like_count":     69,
		"favorite_count": 40,
		"comment_count":  47,
	}}

	hydrateFeedLinkCards(context.Background(), source, response)
	if len(source.calls) != 1 || source.calls[0] != "linked-pin" {
		t.Fatalf("linked pin calls=%#v", source.calls)
	}
	items := parseFeedItems(asSlice(response["data"]))
	if len(items) != 1 {
		t.Fatalf("items=%#v", items)
	}
	for _, expected := range []string{
		"最终判了五年",
		"↳ 引用想法",
		"北大才女用算法贩毒一年赚 1 亿美金，最终被判30年",
		"赞同 69  ·  收藏 40  ·  评论 47",
		"▣ 图片",
	} {
		if !strings.Contains(items[0].body, expected) {
			t.Fatalf("pin body has no %q: %q", expected, items[0].body)
		}
	}
	if strings.Count(items[0].body, "引用想法") != 1 {
		t.Fatalf("generic card title was rendered twice: %q", items[0].body)
	}
}

func TestAnswerLinkCardLoadsAnswerFromURL(t *testing.T) {
	linkCard := map[string]any{
		"type":              "link_card",
		"data_content_type": "ANSWER",
		"data_content_id":   "legacy-id",
		"url":               "https://www.zhihu.com/question/1/answer/2058851474327738199",
	}
	response := map[string]any{"data": []any{map[string]any{
		"target": map[string]any{
			"id":      "outer-pin",
			"type":    "pin",
			"content": []any{linkCard},
		},
	}}}
	source := &pinCardTestSource{answerDetail: map[string]any{
		"author":         map[string]any{"name": "厂长L"},
		"question":       map[string]any{"title": "如何评价 GPT-5.6？"},
		"content":        "<p>回答的真实摘要。</p>",
		"voteup_count":   102,
		"comment_count":  9,
		"favlists_count": 15,
	}}

	hydrateFeedLinkCards(context.Background(), source, response)
	if len(source.answerCalls) != 1 || source.answerCalls[0] != "2058851474327738199" {
		t.Fatalf("answer card calls=%#v", source.answerCalls)
	}
	items := parseFeedItems(asSlice(response["data"]))
	if len(items) != 1 {
		t.Fatalf("items=%#v", items)
	}
	for _, want := range []string{
		"↳ 引用回答 · 厂长L",
		"如何评价 GPT-5.6？",
		"回答的真实摘要。",
		"赞同 102  ·  收藏 15  ·  评论 9",
	} {
		if !strings.Contains(items[0].body, want) {
			t.Fatalf("answer card has no %q: %q", want, items[0].body)
		}
	}
	if strings.Contains(items[0].body, "暂无摘要") || strings.Contains(items[0].body, "引用想法") {
		t.Fatalf("answer card used pin fallback: %q", items[0].body)
	}
}

func TestFeedItemKeyIgnoresVolatileActivityID(t *testing.T) {
	activity := func(activityID, actor string) map[string]any {
		return map[string]any{
			"id":          activityID,
			"action_text": actor + "赞同了回答",
			"target": map[string]any{
				"id":      "same-answer",
				"type":    "answer",
				"content": "正文",
				"question": map[string]any{
					"id":    "question",
					"title": "同一个问题",
				},
			},
		}
	}
	first, _ := parseFeedItem(activity("volatile-a", "Alice"))
	refreshed, _ := parseFeedItem(activity("volatile-b", "Alice"))
	otherActor, _ := parseFeedItem(activity("volatile-c", "Bob"))
	if first.key != refreshed.key {
		t.Fatalf("same feed changed key across refresh: %q != %q", first.key, refreshed.key)
	}
	if first.key == otherActor.key {
		t.Fatalf("different actors for the same target share key %q", first.key)
	}
}

func TestParseFeedItemsExpandsServerFoldedGroup(t *testing.T) {
	visible := feedTestRaw("visible", "同一个问题")
	visible["target"].(map[string]any)["id"] = "same-answer"
	folded := feedTestRaw("folded", "同一个问题")
	folded["target"].(map[string]any)["id"] = "same-answer"
	folded["action_text"] = "另一位用户赞同了回答"

	items := parseFeedItems([]any{
		visible,
		map[string]any{
			"id":         "group-1",
			"group_text": "还有 1 个动态被收起",
			"style_type": 0,
			"list":       []any{folded},
		},
	})
	if len(items) != 2 {
		t.Fatalf("items=%#v, want visible activity and collapsed group", items)
	}
	if len(items[1].foldedItems) != 1 || items[1].groupOpen {
		t.Fatalf("group=%#v, want one child collapsed by default", items[1])
	}
	child := items[1].foldedItems[0]
	if items[0].key == child.key {
		t.Fatalf("different activities for the same target share key %q", items[0].key)
	}
	if !child.serverFolded || child.action != "另一位用户 赞同了回答" {
		t.Fatalf("folded child=%#v, want complete activity with source marker", child)
	}

	model := &app{items: items, height: 20}
	sidebar := renderSidebar(model, 48)
	if !strings.Contains(sidebar[6].text, "▸ 还有 1 个动态被收起") || !strings.Contains(sidebar[7].text, "e/Enter 展开") {
		t.Fatalf("collapsed group has no disclosure control: %#v %#v", sidebar[6], sidebar[7])
	}
	model.index = 1
	model.width = 100
	groupLines, _ := renderSingleApp(model)
	groupPreview := strings.Join(styledLineTexts(groupLines), "\n")
	if !strings.Contains(groupPreview, "同一个问题") || !strings.Contains(groupPreview, "另一位用户 赞同了 匿名用户 的回答") || !strings.Contains(groupPreview, "正文") {
		t.Fatalf("collapsed group has no useful content preview: %q", groupPreview)
	}
	titleColumn, excerptColumn := -1, -1
	for _, line := range groupLines {
		if strings.Contains(line.text, "还有 1 个动态被收起") && line.style != ansiDim {
			t.Fatalf("folded group title style=%q, want neutral dim text", line.style)
		}
		if strings.Contains(line.text, "同一个问题") && line.style != ansiBlue {
			t.Fatalf("folded child title style=%q, want content-title blue", line.style)
		}
		trimmed := strings.TrimLeft(line.text, " ")
		switch trimmed {
		case "同一个问题":
			titleColumn = stringCellWidth(line.text) - stringCellWidth(trimmed)
		case "正文":
			excerptColumn = stringCellWidth(line.text) - stringCellWidth(trimmed)
		}
	}
	if titleColumn < 0 || excerptColumn != titleColumn {
		t.Fatalf("folded title column=%d excerpt column=%d, want left aligned", titleColumn, excerptColumn)
	}
	if strings.Contains(groupPreview, "知乎收起\n") || strings.Contains(groupPreview, "展开到左栏") {
		t.Fatalf("collapsed group still renders redundant labels or instructions: %q", groupPreview)
	}
	if !model.toggleFoldedGroup() || len(model.items) != 3 || !model.items[1].groupOpen {
		t.Fatalf("group did not expand: %#v", model.items)
	}
	sidebar = renderSidebar(model, 48)
	if !strings.Contains(sidebar[6].text, "▾ 还有 1 个动态被收起") || sidebar[9].text != "      同一个问题" || sidebar[10].text != "      另一位用户 赞同了回答" {
		t.Fatalf("expanded group or child is not identified: %#v", sidebar)
	}
	model.index = 2
	model.width = 100
	lines, _ := renderSingleApp(model)
	renderedChild := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(renderedChild, "另一位用户 赞同了回答") || strings.Contains(renderedChild, "知乎收起 ·") {
		t.Fatalf("expanded folded activity has a redundant container label: %q", renderedChild)
	}
	if !model.toggleFoldedGroup() || len(model.items) != 2 || model.index != 1 || model.items[1].groupOpen {
		t.Fatalf("group did not collapse from its child: index=%d items=%#v", model.index, model.items)
	}
}

func TestFoldedPreviewMarksOnlyTruncatedExcerpts(t *testing.T) {
	lines := layoutFoldedGroupPreview([]feedItem{
		{kind: "answer", title: "短回答", author: "甲", action: "某人赞同了回答", body: "令人感叹"},
		{kind: "answer", title: "多段回答", author: "乙", action: "某人赞同了回答", body: "第一段\n\n第二段"},
	}, 40)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(rendered, "令人感叹") || strings.Contains(rendered, "令人感叹…") {
		t.Fatalf("complete short answer has an unnecessary marker: %q", rendered)
	}
	if !strings.Contains(rendered, "第一段…") {
		t.Fatalf("multi-paragraph answer was not marked truncated: %q", rendered)
	}
	if strings.Contains(rendered, "（全文）") {
		t.Fatalf("folded preview still renders the redundant full-text marker: %q", rendered)
	}
	for _, line := range lines {
		if strings.Contains(line.text, "令人感叹") && (line.text != "令人感叹" || line.style != "") {
			t.Fatalf("folded answer excerpt=%#v, want left-aligned normal body text", line)
		}
	}
	firstExcerpt, secondTitle := -1, -1
	for index, line := range lines {
		if line.text == "令人感叹" {
			firstExcerpt = index
		}
		if line.text == "多段回答" {
			secondTitle = index
		}
	}
	if firstExcerpt < 0 || secondTitle-firstExcerpt-1 != paragraphGapLines {
		t.Fatalf("folded item gap=%d, want %d blank lines", secondTitle-firstExcerpt-1, paragraphGapLines)
	}
}

func TestFoldedPreviewDescribesAnswerActorAndAuthor(t *testing.T) {
	lines := layoutFoldedGroupPreview([]feedItem{
		{kind: "answer", title: "问题", author: "好好睡觉", action: "codedump 赞同了回答", body: "回答摘要"},
	}, 60)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(rendered, "codedump 赞同了 好好睡觉 的回答") {
		t.Fatalf("answer activity relationship is unclear: %q", rendered)
	}
	if strings.Contains(rendered, "答主 好好睡觉") {
		t.Fatalf("answer activity still uses the old role-first copy: %q", rendered)
	}
}

func TestFoldedPreviewDoesNotRepeatAuthorInOwnActivity(t *testing.T) {
	for _, item := range []feedItem{
		{kind: "pin", author: "一直住顶楼", action: "一直住顶楼 发布了想法"},
		{kind: "article", author: "作者甲", action: "作者甲 发布了文章"},
		{kind: "answer", author: "答主乙", action: "答主乙 回答了问题"},
	} {
		if got := foldedItemEventLabel(item); got != item.action {
			t.Fatalf("foldedItemEventLabel(%#v)=%q, want %q", item, got, item.action)
		}
	}
}

func TestFoldedPreviewLabelsContentAuthorRoles(t *testing.T) {
	lines := layoutFoldedGroupPreview([]feedItem{
		{kind: "question", title: "航天问题", author: "不方的圆", action: "codedump 关注了问题", body: "问题详情"},
		{kind: "article", title: "文章", author: "作者甲", action: "某人赞同了文章", body: "文章摘要"},
	}, 60)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(rendered, "codedump 关注了 不方的圆 的问题") {
		t.Fatalf("question author role is ambiguous: %q", rendered)
	}
	if !strings.Contains(rendered, "作者 作者甲 · 某人赞同了文章") {
		t.Fatalf("article author role is ambiguous: %q", rendered)
	}
}

func TestFoldedGroupKeyIgnoresVolatileRequestSegment(t *testing.T) {
	first := stableFoldedGroupKey("0_1783736483078040247_1783736159954_4")
	refreshed := stableFoldedGroupKey("0_1783736484122108502_1783736159954_4")
	otherGroup := stableFoldedGroupKey("0_1783736484141440881_1783734050973_4")
	if first != refreshed {
		t.Fatalf("same folded group changed key across refresh: %q != %q", first, refreshed)
	}
	if first == otherGroup {
		t.Fatalf("different folded groups share key %q", first)
	}
}

func TestFoldedGroupRefreshDoesNotDuplicateOrBlockReadBottom(t *testing.T) {
	groupRaw := func(volatile string) map[string]any {
		return map[string]any{
			"id":         "0_" + volatile + "_1783736159954_4",
			"group_text": "还有 {LIST_COUNT} 个用户的动态被收起",
			"list":       []any{feedTestRaw("folded-child", "组内动态")},
		}
	}
	response := func(volatile string) map[string]any {
		return map[string]any{
			"data": []any{
				feedTestRaw("top", "顶部动态"),
				groupRaw(volatile),
				feedTestRaw("after-group", "组后动态"),
			},
			"paging": map[string]any{"is_end": true},
		}
	}

	model := &app{generation: 1, items: parseFeedItems(asSlice(response("request-a")["data"])), height: 20}
	model.firstViewedKey = model.items[0].key
	model.furthestViewedKey = model.items[1].key
	model.captureRefreshBoundary()
	model.generation++
	model.applyFetch(fetchResult{response: response("request-b"), reset: true, generation: model.generation})
	if len(model.items) != 3 {
		t.Fatalf("refresh accumulated duplicate folded group: %#v", model.items)
	}
	model.index = 2
	model.markCurrentViewed()
	if model.furthestViewedKey != "answer:after-group" {
		t.Fatalf("furthestViewedKey=%q, want item after folded group", model.furthestViewedKey)
	}
	model.captureRefreshBoundary()
	model.generation++
	model.applyFetch(fetchResult{response: response("request-c"), reset: true, generation: model.generation})
	if model.lastReadBottomKey != "answer:after-group" {
		t.Fatalf("lastReadBottomKey=%q, want read boundary after folded group", model.lastReadBottomKey)
	}
	if len(model.items) != 3 {
		t.Fatalf("second refresh accumulated duplicate folded group: %#v", model.items)
	}
}

func TestRefreshMergesTopLevelAndFoldedRepresentationsByLeafKey(t *testing.T) {
	groupRaw := func(id string, children ...map[string]any) map[string]any {
		list := make([]any, len(children))
		for index := range children {
			list[index] = children[index]
		}
		return map[string]any{
			"id":         id,
			"group_text": "还有 {LIST_COUNT} 个用户的动态被收起",
			"list":       list,
		}
	}
	response := func(items ...any) map[string]any {
		return map[string]any{"data": items, "paging": map[string]any{"is_end": true}}
	}
	refresh := func(previous []any, next map[string]any) *app {
		model := &app{
			generation:           1,
			items:                parseFeedItems(previous),
			pendingRefreshTopKey: "refresh",
		}
		model.applyFetch(fetchResult{response: next, reset: true, generation: 1})
		return model
	}

	t.Run("top level to folded", func(t *testing.T) {
		activity := feedTestRaw("same", "同一条动态")
		model := refresh([]any{activity}, response(groupRaw("group", activity)))
		if len(model.items) != 1 || len(model.items[0].foldedItems) != 1 {
			t.Fatalf("top-level activity was retained beside its folded representation: %#v", model.items)
		}
	})

	t.Run("folded to top level", func(t *testing.T) {
		activity := feedTestRaw("same", "同一条动态")
		model := refresh([]any{groupRaw("group", activity)}, response(activity))
		if len(model.items) != 1 || len(model.items[0].foldedItems) != 0 || model.items[0].key != "answer:same" {
			t.Fatalf("folded activity was retained beside its top-level representation: %#v", model.items)
		}
	})

	t.Run("same group keeps only omitted children", func(t *testing.T) {
		oldA := feedTestRaw("old-a", "旧动态 A")
		oldB := feedTestRaw("old-b", "旧动态 B")
		newC := feedTestRaw("new-c", "新动态 C")
		model := refresh(
			[]any{groupRaw("group", oldA, oldB)},
			response(groupRaw("group", oldA, newC)),
		)
		if len(model.items) != 1 || len(model.items[0].foldedItems) != 3 {
			t.Fatalf("same folded group was duplicated or lost omitted children: %#v", model.items)
		}
		if model.items[0].title != "还有 3 个用户的动态被收起" {
			t.Fatalf("merged folded group title=%q", model.items[0].title)
		}
		keys := make(map[string]int)
		for _, child := range model.items[0].foldedItems {
			keys[child.key]++
		}
		for _, key := range []string{"answer:old-a", "answer:old-b", "answer:new-c"} {
			if keys[key] != 1 {
				t.Fatalf("leaf %q occurs %d times in %#v", key, keys[key], model.items)
			}
		}
		if _, isNew := model.newItemKeys["answer:new-c"]; !isNew {
			t.Fatalf("new folded child was not marked new: %#v", model.newItemKeys)
		}
	})
}

func TestCollapsedGroupInheritsAndExpandedGroupDistributesReadState(t *testing.T) {
	group := feedItem{
		key:         "group",
		title:       "还有 2 条动态被收起",
		action:      "知乎关注流收起的动态",
		foldedItems: []feedItem{{key: "child-top", title: "子项一"}, {key: "child-bottom", title: "子项二"}},
	}
	for index := range group.foldedItems {
		group.foldedItems[index].foldedParent = group.key
	}
	model := &app{
		items:             []feedItem{{key: "normal", title: "普通动态"}, group},
		index:             0,
		height:            20,
		lastReadTopKey:    "child-top",
		lastReadBottomKey: "child-bottom",
		newItemKeys:       map[string]struct{}{"child-top": {}},
	}

	sidebar := renderSidebar(model, 48)
	if !strings.HasPrefix(sidebar[7].text, "  上次读到↓↑ · ") || sidebar[6].style != ansiCyan {
		t.Fatalf("collapsed group did not inherit child boundaries over NEW: %#v %#v", sidebar[6], sidebar[7])
	}

	model.index = 1
	if !model.toggleFoldedGroup() {
		t.Fatal("group did not expand")
	}
	sidebar = renderSidebar(model, 48)
	if strings.Contains(sidebar[7].text, "上次读到") {
		t.Fatalf("expanded group kept inherited boundary: %#v", sidebar[7])
	}
	if !strings.HasPrefix(sidebar[10].text, "      上次读到↓ · ") || !strings.HasPrefix(sidebar[13].text, "      上次读到↑ · ") {
		t.Fatalf("expanded children did not recover exact boundaries: %#v", sidebar)
	}
}

func TestCollapsedGroupInheritsNewStateUntilExpanded(t *testing.T) {
	group := feedItem{
		key:         "group",
		title:       "还有 1 条动态被收起",
		foldedItems: []feedItem{{key: "new-child", title: "新子项", foldedParent: "group"}},
	}
	model := &app{
		items:       []feedItem{{key: "normal", title: "普通动态"}, group},
		height:      20,
		newItemKeys: map[string]struct{}{"new-child": {}},
	}
	sidebar := renderSidebar(model, 48)
	if sidebar[6].style != ansiGreen {
		t.Fatalf("collapsed group style=%q, want inherited green", sidebar[6].style)
	}
	model.index = 1
	model.toggleFoldedGroup()
	model.index = 0
	sidebar = renderSidebar(model, 48)
	if sidebar[6].style != "" || sidebar[9].style != ansiGreen {
		t.Fatalf("expanded NEW state was not moved to child: group=%q child=%q", sidebar[6].style, sidebar[9].style)
	}
}

func TestRefreshTracksNewChildrenInsideExistingFoldedGroup(t *testing.T) {
	groupRaw := func(children ...map[string]any) map[string]any {
		list := make([]any, len(children))
		for index := range children {
			list[index] = children[index]
		}
		return map[string]any{
			"id":         "stable-group",
			"group_text": "还有 {LIST_COUNT} 条动态被收起",
			"list":       list,
		}
	}
	oldChild := feedTestRaw("old-child", "旧子项")
	model := &app{
		generation:           2,
		items:                parseFeedItems([]any{groupRaw(oldChild)}),
		pendingRefreshTopKey: "stable-group",
		height:               14,
	}
	model.applyFetch(fetchResult{
		reset:      true,
		generation: 2,
		response: map[string]any{
			"data":   []any{groupRaw(oldChild, feedTestRaw("new-child", "新子项"))},
			"paging": map[string]any{"is_end": true},
		},
	})
	if _, isNew := model.newItemKeys["answer:new-child"]; !isNew {
		t.Fatalf("new folded child was not tracked: %v", model.newItemKeys)
	}
	if _, isNew := model.newItemKeys["stable-group"]; isNew {
		t.Fatalf("existing group was incorrectly marked directly new: %v", model.newItemKeys)
	}
	sidebar := renderSidebar(model, 48)
	if sidebar[3].style != ansiBold+ansiGreen {
		t.Fatalf("collapsed group did not inherit refreshed child state: %#v", sidebar[3])
	}
}

func TestViewedRangeUsesFoldedChildrenLogicalOrder(t *testing.T) {
	group := feedItem{
		key: "group",
		foldedItems: []feedItem{
			{key: "child-one", foldedParent: "group"},
			{key: "child-two", foldedParent: "group"},
		},
	}
	model := &app{
		items:             []feedItem{group, {key: "tail"}},
		firstViewedKey:    "child-one",
		furthestViewedKey: "child-two",
	}
	model.index = 1
	model.markCurrentViewed()
	if model.furthestViewedKey != "tail" {
		t.Fatalf("furthestViewedKey=%q, hidden children blocked downward progress", model.furthestViewedKey)
	}
	model.index = 0
	model.markCurrentViewed()
	if model.firstViewedKey != "group" {
		t.Fatalf("firstViewedKey=%q, logical group order was ignored", model.firstViewedKey)
	}
}

func TestApplyFetchDeduplicatesOverlappingPages(t *testing.T) {
	model := &app{generation: 1, loading: true}
	response := map[string]any{
		"data": []any{
			feedTestRaw("1", "问题一"),
			feedTestRaw("1", "问题一"),
			feedTestRaw("2", "问题二"),
		},
		"paging": map[string]any{
			"is_end": false,
			"next":   "https://www.zhihu.com/api/v3/moments?after_id=2",
		},
	}
	model.applyFetch(fetchResult{response: response, reset: true, generation: 1})
	if len(model.items) != 2 {
		t.Fatalf("items=%d, want 2", len(model.items))
	}
	if model.nextURL == "" || model.end {
		t.Fatalf("nextURL=%q end=%v", model.nextURL, model.end)
	}
}

func TestRefreshKeepsLoadedItemsMissingFromLatestSnapshot(t *testing.T) {
	model := &app{
		generation: 1,
		items: []feedItem{
			{key: "answer:1", title: "问题一"},
			{key: "answer:2", title: "问题二"},
			{key: "answer:3", title: "问题三"},
		},
		firstViewedKey:    "answer:1",
		furthestViewedKey: "answer:2",
	}
	model.captureRefreshBoundary()
	model.generation++
	model.applyFetch(fetchResult{
		reset:      true,
		generation: model.generation,
		response: map[string]any{
			"data": []any{
				feedTestRaw("new", "新问题"),
				feedTestRaw("1", "问题一"),
			},
			"paging": map[string]any{
				"is_end": false,
				"next":   "https://www.zhihu.com/api/v3/moments?after_id=next",
			},
		},
	})

	wantKeys := []string{"answer:new", "answer:1", "answer:2", "answer:3"}
	gotKeys := make([]string, len(model.items))
	for index, item := range model.items {
		gotKeys[index] = item.key
	}
	if strings.Join(gotKeys, "\n") != strings.Join(wantKeys, "\n") {
		t.Fatalf("refreshed keys=%q, want latest snapshot followed by retained history %q", gotKeys, wantKeys)
	}
	if _, isNew := model.newItemKeys["answer:new"]; !isNew || len(model.newItemKeys) != 1 {
		t.Fatalf("newItemKeys=%v, want only the unseen snapshot item", model.newItemKeys)
	}
	if model.nextURL == "" || model.end {
		t.Fatalf("paging was not kept from the latest snapshot: next=%q end=%v", model.nextURL, model.end)
	}
}

func TestRefreshMarksNewAndPreviouslyViewedRangeAfterSuccess(t *testing.T) {
	model := &app{
		generation: 1,
		items: []feedItem{
			{key: "answer:1", title: "问题一", action: "甲赞同了回答"},
			{key: "answer:2", title: "问题二", action: "乙赞同了回答"},
			{key: "answer:3", title: "问题三", action: "丙赞同了回答"},
		},
		height: 24,
	}
	model.index = 0
	model.markCurrentViewed()
	model.index = 1
	model.markCurrentViewed()
	model.captureRefreshBoundary()
	if model.lastReadTopKey != "" || model.lastReadBottomKey != "" {
		t.Fatalf("last-read range=(%q, %q) before refresh finishes", model.lastReadTopKey, model.lastReadBottomKey)
	}
	if model.pendingReadTopKey != "answer:1" || model.pendingReadBottomKey != "answer:2" {
		t.Fatalf("pending range=(%q, %q), want session first and furthest viewed items", model.pendingReadTopKey, model.pendingReadBottomKey)
	}
	if model.pendingRefreshTopKey != "answer:1" {
		t.Fatalf("pendingRefreshTopKey=%q, want the current feed head", model.pendingRefreshTopKey)
	}
	if len(model.newItemKeys) != 0 {
		t.Fatalf("newItemKeys=%v before refresh finishes", model.newItemKeys)
	}

	response := map[string]any{
		"data": []any{
			feedTestRaw("new", "新问题"),
			feedTestRaw("1", "问题一"),
			feedTestRaw("2", "问题二"),
			feedTestRaw("3", "问题三"),
		},
		"paging": map[string]any{"is_end": true},
	}
	model.generation++
	model.applyFetch(fetchResult{response: response, reset: true, generation: model.generation})

	if model.lastReadTopKey != "answer:1" || model.lastReadBottomKey != "answer:2" {
		t.Fatalf("last-read range=(%q, %q), want previous first and last viewed items", model.lastReadTopKey, model.lastReadBottomKey)
	}
	if model.pendingReadTopKey != "" || model.pendingReadBottomKey != "" || model.pendingRefreshTopKey != "" {
		t.Fatalf("pending state=(%q, %q, %q) after refresh finishes", model.pendingReadTopKey, model.pendingReadBottomKey, model.pendingRefreshTopKey)
	}
	if _, ok := model.newItemKeys["answer:new"]; !ok || len(model.newItemKeys) != 1 {
		t.Fatalf("newItemKeys=%v, want only the item before the previous first item", model.newItemKeys)
	}

	sidebar := renderSidebar(model, 40)
	if sidebar[3].text != "› 新问题" {
		t.Fatalf("first sidebar title=%q, want no numeric prefix", sidebar[3].text)
	}
	if strings.Contains(sidebar[1].text, "NEW") || strings.Contains(sidebar[4].text, "NEW") {
		t.Fatalf("sidebar still contains redundant NEW text: %#v", sidebar)
	}
	if sidebar[3].style != ansiBold+ansiGreen {
		t.Fatalf("new selected title style=%q, want green", sidebar[3].style)
	}
	if sidebar[4].style != ansiDim {
		t.Fatalf("new item summary style=%q, want the normal dim style", sidebar[4].style)
	}
	if !strings.HasPrefix(sidebar[7].text, "  上次读到↓ · ") {
		t.Fatalf("last-read top summary=%q", sidebar[7].text)
	}
	if !strings.HasPrefix(sidebar[10].text, "  上次读到↑ · ") {
		t.Fatalf("last-read bottom summary=%q", sidebar[10].text)
	}
	if strings.Contains(sidebar[13].text, "上次读到") {
		t.Fatalf("unread prefetched item was marked as viewed: %q", sidebar[13].text)
	}

	model.index = 0
	model.markCurrentViewed()
	model.index = 3
	model.markCurrentViewed()
	model.captureRefreshBoundary()
	if model.pendingReadTopKey != "answer:new" || model.pendingReadBottomKey != "answer:3" {
		t.Fatalf("cumulative pending range=(%q, %q), want process-lifetime endpoints", model.pendingReadTopKey, model.pendingReadBottomKey)
	}
	if model.pendingRefreshTopKey != "answer:new" {
		t.Fatalf("pendingRefreshTopKey=%q, want the latest feed head", model.pendingRefreshTopKey)
	}

	response = map[string]any{
		"data": []any{
			feedTestRaw("newer", "更新的问题"),
			feedTestRaw("new", "新问题"),
			feedTestRaw("1", "问题一"),
			feedTestRaw("2", "问题二"),
			feedTestRaw("3", "问题三"),
		},
		"paging": map[string]any{"is_end": true},
	}
	model.generation++
	model.applyFetch(fetchResult{response: response, reset: true, generation: model.generation})
	model.markCurrentViewed()
	if model.lastReadTopKey != "answer:new" || model.lastReadBottomKey != "answer:3" {
		t.Fatalf("cumulative last-read range=(%q, %q), want process-lifetime endpoints", model.lastReadTopKey, model.lastReadBottomKey)
	}
	if model.firstViewedKey != "answer:newer" || model.furthestViewedKey != "answer:3" {
		t.Fatalf("session viewed range=(%q, %q) regressed after refresh", model.firstViewedKey, model.furthestViewedKey)
	}
	if _, ok := model.newItemKeys["answer:newer"]; !ok || len(model.newItemKeys) != 1 {
		t.Fatalf("newItemKeys=%v, want only content from the latest refresh", model.newItemKeys)
	}

	model.captureRefreshBoundary()
	response = map[string]any{
		"data": []any{
			feedTestRaw("newest", "最新问题"),
			feedTestRaw("newer", "更新的问题"),
			feedTestRaw("new", "新问题"),
			feedTestRaw("1", "问题一"),
			feedTestRaw("2", "问题二"),
			feedTestRaw("3", "问题三"),
		},
		"paging": map[string]any{"is_end": true},
	}
	model.generation++
	model.applyFetch(fetchResult{response: response, reset: true, generation: model.generation})
	if model.lastReadTopKey != "answer:newer" {
		t.Fatalf("lastReadTopKey=%q, want the newest item displayed before refresh", model.lastReadTopKey)
	}
	if _, stillNew := model.newItemKeys["answer:newer"]; stillNew {
		t.Fatalf("previously displayed boundary is still marked new: %v", model.newItemKeys)
	}
	if _, isNew := model.newItemKeys["answer:newest"]; !isNew {
		t.Fatalf("latest refresh item is not marked new: %v", model.newItemKeys)
	}
	sidebar = renderSidebar(model, 40)
	if !strings.HasPrefix(sidebar[7].text, "  上次读到↓ · ") || sidebar[7].style != ansiCyan {
		t.Fatalf("displayed new item was not promoted to read boundary: %#v", sidebar[7])
	}
}

func TestSidebarReadBoundaryOverridesNewStyle(t *testing.T) {
	model := &app{
		items:             []feedItem{{key: "answer:1", title: "重叠状态", action: "某人赞同了回答"}},
		newItemKeys:       map[string]struct{}{"answer:1": {}},
		lastReadTopKey:    "answer:1",
		lastReadBottomKey: "answer:1",
		height:            14,
	}
	sidebar := renderSidebar(model, 40)
	if sidebar[3].style != ansiBold+ansiCyan {
		t.Fatalf("overlapping boundary title style=%q, want selected cyan", sidebar[3].style)
	}
	if !strings.HasPrefix(sidebar[4].text, "  上次读到↓↑ · ") || sidebar[4].style != ansiCyan {
		t.Fatalf("overlapping boundary summary=%#v, want read-range marker", sidebar[4])
	}
}

func feedTestRaw(id, title string) map[string]any {
	return map[string]any{
		"id": "answer:" + id,
		"target": map[string]any{
			"id":      id,
			"type":    "answer",
			"content": "正文",
			"question": map[string]any{
				"id":    "question-" + id,
				"title": title,
			},
		},
	}
}

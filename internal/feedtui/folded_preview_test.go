package feedtui

import (
	"strings"
	"testing"
)

func TestFoldedPreviewMarksOnlyTruncatedExcerpts(t *testing.T) {
	lines := layoutFoldedGroupPreview([]feedItem{
		{kind: "answer", title: "短回答", author: "甲", action: "某人赞同了回答", body: "令人感叹"},
		{kind: "answer", title: "多段回答", author: "乙", action: "某人赞同了回答", body: "第一段\n\n第二段"},
		{kind: "answer", title: "长回答", author: "丙", action: "某人赞同了回答", body: strings.Repeat("长", 50)},
	}, 40)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(rendered, "令人感叹") || strings.Contains(rendered, "令人感叹…") {
		t.Fatalf("complete short answer has an unnecessary marker: %q", rendered)
	}
	if !strings.Contains(rendered, "第一段 第二段") || strings.Contains(rendered, "第一段…") {
		t.Fatalf("newlines in a fitting excerpt were not rendered as spaces: %q", rendered)
	}
	if !strings.Contains(rendered, strings.Repeat("长", 19)+"…") {
		t.Fatalf("long answer was not marked truncated: %q", rendered)
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

func TestFoldedPreviewLimitsChildTitleToTwoLines(t *testing.T) {
	lines := layoutFoldedGroupPreview([]feedItem{
		{kind: "question", title: strings.Repeat("长", 12), author: "甲", action: "甲关注了问题"},
	}, 10)
	if len(lines) != 3 {
		t.Fatalf("folded preview lines=%#v, want two title lines and one activity line", lines)
	}
	if lines[0].text != strings.Repeat("长", 5) || lines[0].style != ansiBlue {
		t.Fatalf("first title line=%#v", lines[0])
	}
	if lines[1].text != strings.Repeat("长", 4)+"…" || lines[1].style != ansiBlue {
		t.Fatalf("second title line=%#v, want truncated blue title", lines[1])
	}
	if lines[2].style != ansiDim {
		t.Fatalf("activity line=%#v, want dim activity after title", lines[2])
	}
}

func TestFoldedPreviewOmitsMissingExcerpt(t *testing.T) {
	lines := layoutFoldedGroupPreview([]feedItem{
		{kind: "question", title: "如何评价游戏小丑牌？", author: "西行寺振凯", action: "codedump 关注了问题"},
	}, 60)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if strings.Contains(rendered, "暂无正文摘要") {
		t.Fatalf("folded preview rendered a meaningless empty excerpt: %q", rendered)
	}
	if len(lines) != 2 || lines[0].text != "如何评价游戏小丑牌？" || lines[1].text != "codedump 关注了 西行寺振凯 的问题" {
		t.Fatalf("folded preview=%#v, want only title and activity", lines)
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
	if !strings.Contains(rendered, "某人 赞同了 作者甲 的文章") {
		t.Fatalf("article author role is ambiguous: %q", rendered)
	}
}

func TestFoldedPreviewOmitsLinkCardMetadata(t *testing.T) {
	answer := map[string]any{
		"type":              "link_card",
		"data_content_type": "ANSWER",
		"data_content_id":   "answer-1",
		"card_detail": map[string]any{
			"question":      map[string]any{"title": "回答标题"},
			"excerpt":       "回答正文",
			"author":        map[string]any{"name": "引用答主"},
			"voteup_count":  77,
			"comment_count": 8,
		},
	}
	nested := map[string]any{
		"type":              "link_card",
		"data_content_type": "PIN",
		"data_content_id":   "pin-1",
		"card_detail": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "content": "内层想法 | <p>想法正文</p>"},
				answer,
			},
			"author":        map[string]any{"name": "引用想法作者"},
			"like_count":    23,
			"comment_count": 4,
		},
	}
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"answer", formatLinkCard(answer), "回答标题 回答正文"},
		{"nested", formatLinkCard(nested), "内层想法 想法正文 回答标题 回答正文"},
		{
			"legacy",
			linkCardTitleMarker + "回答标题\n" + linkCardExcerptMarker + "回答正文\n" + linkCardMetadataMarker + "引用答主  ·  赞同 77  ·  评论 8  ·  回答",
			"回答标题 回答正文",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := feedItem{
				kind:  "pin",
				title: "外层想法",
				body:  "外层正文\n" + test.body,
				stats: "赞同 321  ·  评论 12",
			}
			want := "外层正文 " + test.want
			if got := foldedItemExcerpt(item); got != want {
				t.Fatalf("folded excerpt=%q, want %q", got, want)
			}
			preview := strings.Join(styledLineTexts(layoutFoldedGroupPreview([]feedItem{item}, 200)), "\n")
			if !strings.Contains(preview, want) || strings.Contains(preview, "赞同") || strings.Contains(preview, "评论") {
				t.Fatalf("folded preview includes statistics or loses content: %q", preview)
			}
			body := visibleLinkCardTree(layoutBodyLines(test.body, 200))
			if !strings.Contains(body, "引用答主") || !strings.Contains(body, "赞同 77") || !strings.Contains(body, "评论 8") {
				t.Fatalf("full body lost link-card metadata: %q", body)
			}
		})
	}
}

func TestFoldedPreviewPreservesStatWordsInBody(t *testing.T) {
	item := feedItem{
		kind:  "pin",
		title: "想法",
		body:  "正文提到赞同 77 和评论 8。\n" + linkCardExcerptMarker + "引用正文也提到赞同 77。",
	}
	const want = "正文提到赞同 77 和评论 8。 引用正文也提到赞同 77。"
	if got := foldedItemExcerpt(item); got != want {
		t.Fatalf("folded excerpt=%q, want %q", got, want)
	}
}

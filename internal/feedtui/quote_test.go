package feedtui

import (
	"strings"
	"testing"
)

func TestHTMLQuoteKeepsBoundaryAndParagraphs(t *testing.T) {
	body, _ := contentText(`<p>前文。</p><BLOCKQUOTE class="quote"><p>第一段。</p><p>第二段。</p></BLOCKQUOTE><p>后文。</p>`)
	lines := layoutBodyLines(body, 40)
	want := "前文。\n\n\n│ 第一段。\n│ \n│ \n│ 第二段。\n\n\n后文。"
	if got := tableLayoutText(lines); got != want {
		t.Fatalf("quote layout:\n%s\nwant:\n%s", got, want)
	}
	for _, line := range lines {
		if strings.HasPrefix(line.text, "│ ") {
			rendered, _ := renderStyledLine(line, 40)
			if !strings.Contains(rendered, styleText(line.text, ansiQuote)) {
				t.Fatalf("quote line has no quote color: %q", rendered)
			}
			if line.middle != "" && !strings.Contains(rendered, styleText(line.middle, ansiQuote)) {
				t.Fatalf("quote text has no quote color: %q", rendered)
			}
		} else if line.style != "" {
			t.Fatalf("surrounding prose inherited quote style: %#v", line)
		}
	}
}

func TestHTMLQuoteWrapsWithRailAndPreservesLinks(t *testing.T) {
	body, _ := contentText(`<blockquote>开头 <a href="https://example.com">一段很长的链接文字跨越换行</a> 后文 🇳🇱 ending.</blockquote>`)
	for _, width := range []int{12, 24, 40} {
		lines := layoutBodyLines(body, width)
		var visible, linked strings.Builder
		for _, line := range lines {
			text := styledLineText(line)
			if !strings.HasPrefix(text, "│ ") || stringCellWidth(text) > width {
				t.Fatalf("width %d: invalid quote line %q", width, text)
			}
			visible.WriteString(strings.TrimPrefix(text, "│ "))
			for _, segment := range line.segments {
				if segment.style == ansiLink {
					linked.WriteString(segment.text)
				} else if segment.style != ansiQuote {
					t.Fatalf("ordinary quote text lost color: %#v", segment)
				}
			}
		}
		if got := strings.ReplaceAll(visible.String(), " ", ""); got != "开头一段很长的链接文字跨越换行后文🇳🇱ending." {
			t.Fatalf("wrapped quote lost text: %q", visible.String())
		}
		if linked.String() != "一段很长的链接文字跨越换行" {
			t.Fatalf("wrapped quote link lost style: %q", linked.String())
		}
	}
}

func TestHTMLQuoteNestingAndAdjacentBlocks(t *testing.T) {
	body, _ := contentText(`<blockquote>外层<blockquote>内层</blockquote>回到外层</blockquote><blockquote>另一段引用</blockquote><p>正文</p>`)
	got := tableLayoutText(layoutBodyLines(body, 40))
	for _, want := range []string{"│ 外层\n", "│ │ 内层\n", "│ 回到外层\n\n\n│ 另一段引用\n\n\n正文"} {
		if !strings.Contains(got, want) {
			t.Fatalf("nested/adjacent quotes lost %q:\n%s", want, got)
		}
	}
}

func TestHTMLQuotePreservesCodeTablesAndImages(t *testing.T) {
	body, images := contentText(`<blockquote><pre><code>  sample()</code></pre><table><tr><td>单元格</td></tr></table><img src="https://example.com/image.png"></blockquote><p>正文</p>`)
	if images != 1 {
		t.Fatalf("image count=%d, want 1", images)
	}
	got := tableLayoutText(layoutBodyLines(body, 40))
	for _, want := range []string{"│ ┌─ 代码", "│ │   sample()", "│ │ 单元格 │", "│ ▣ 图片 1", "\n\n\n正文"} {
		if !strings.Contains(got, want) {
			t.Fatalf("quote lost %q:\n%s", want, got)
		}
	}
	if strings.ContainsRune(got, '\ue000') {
		t.Fatalf("quote leaked block markers:\n%s", got)
	}

	body, _ = contentText(`<pre><code>&lt;blockquote&gt;代码示例&lt;/blockquote&gt;</code></pre><blockquote>实际引用</blockquote>`)
	if strings.Count(body, quoteStartMarker) != 1 || strings.Count(body, quoteEndMarker) != 1 {
		t.Fatalf("blockquote markup in code introduced quote boundaries: %q", body)
	}
}

func TestHTMLQuoteMarkersDoNotLeakIntoPreviewsOrTableCells(t *testing.T) {
	source := `<blockquote><p>引用内容</p><p>另一段</p></blockquote>`
	item, ok := parseFeedItem(map[string]any{"target": map[string]any{"id": "1", "type": "pin", "content": source}})
	if !ok {
		t.Fatal("quote-only pin was not parsed")
	}
	for name, value := range map[string]string{
		"title":          item.title,
		"folded excerpt": foldedItemExcerpt(item),
		"link excerpt":   linkCardExcerpt(map[string]any{"content": source}),
	} {
		if strings.ContainsRune(value, '\ue000') {
			t.Fatalf("%s contains quote markers: %q", name, value)
		}
	}
	if got := linkCardExcerpt(map[string]any{"content": source}); got != "引用内容 另一段" {
		t.Fatalf("link excerpt lost quote text: %q", got)
	}

	body, _ := contentText(`<table><tr><td>` + source + `</td></tr></table>`)
	lines := layoutBodyLines(body, 40)
	for _, line := range lines {
		if strings.ContainsRune(styledLineText(line), '\ue000') {
			t.Fatalf("table cell leaked quote markers: %#v", line)
		}
	}
	if got := tableLayoutText(lines); !strings.Contains(got, "│ │ 引用内容") {
		t.Fatalf("table cell lost quote rail:\n%s", got)
	}
}

func TestHTMLQuoteInCommentsKeepsIdentityAndTree(t *testing.T) {
	content, _ := contentText(`<p>前文</p><blockquote><p>引用内容</p><p><a href="https://example.com">链接</a></p></blockquote><p>回复正文</p>`)
	for _, tree := range []bool{false, true} {
		var source strings.Builder
		source.WriteString(commentStartMarker + "101" + commentMarkerEnd + "\n")
		formatComment(&source, feedComment{id: "101", author: "作者", content: content}, "   └─ ", "      ", tree, "", "")
		lines := layoutBodyLines(source.String(), 40)
		for _, line := range lines {
			if line.commentID != "101" {
				t.Fatalf("quote line lost comment identity: %#v", line)
			}
		}
		got := tableLayoutText(lines)
		prefix := ""
		if tree {
			prefix = "      "
		}
		if strings.ContainsRune(got, '\ue000') || !strings.Contains(got, prefix+"│ 引用内容\n"+prefix+"│ \n"+prefix+"│ 链接\n") || !strings.HasSuffix(got, prefix+"回复正文") {
			t.Fatalf("comment tree=%v lost quote layout:\n%s", tree, got)
		}
	}
}

func TestHTMLQuoteInNarrowTableKeepsLabelsAndLinkStyle(t *testing.T) {
	body, _ := contentText(`<table><tr><th><blockquote>引文</blockquote></th><th>说明</th><th>代码</th><th>很长的列标题需要独占一行</th></tr><tr><td><blockquote><a href="https://example.com">很长的引用链接文字需要换行</a></blockquote></td><td>前文<blockquote>引用内容</blockquote>后文</td><td><pre><code>  sample()</code></pre></td><td><blockquote>末列引用</blockquote></td></tr></table>`)
	for _, width := range []int{24, 36} {
		lines := layoutBodyLines(body, width)
		got := tableLayoutText(lines)
		for _, want := range []string{"引文：│ ", "说明：前文", "│ 引用内容", "后文", "代码：┌─ 代码", "│   sample()", "│ 末列引用"} {
			if !strings.Contains(got, want) {
				t.Fatalf("width %d: stacked table lost %q:\n%s", width, want, got)
			}
		}
		if !strings.Contains(strings.Join(strings.Fields(got), ""), "很长的列标题需要独占一行：") {
			t.Fatalf("wrapped column label lost text:\n%s", got)
		}
		var linked strings.Builder
		for _, line := range lines {
			text := styledLineText(line)
			if strings.ContainsRune(text, '\ue000') || stringCellWidth(text) > width {
				t.Fatalf("width %d: invalid table line %q", width, text)
			}
			for _, segment := range line.segments {
				if segment.style == ansiLink {
					linked.WriteString(segment.text)
				}
			}
		}
		if linked.String() != "很长的引用链接文字需要换行" {
			t.Fatalf("stacked quote lost linked text: %q", linked.String())
		}
	}
}

func TestHTMLQuoteRailKeepsItsColorAroundCode(t *testing.T) {
	body, _ := contentText(`<blockquote><pre><code>  sample()</code></pre></blockquote>`)
	lines := layoutBodyLines(body, 40)
	for _, line := range lines {
		rendered, _ := renderStyledLine(line, 40)
		if line.text != "│ " || line.style != ansiQuote || !strings.Contains(rendered, styleText("│ ", ansiQuote)) {
			t.Fatalf("outer quote rail inherited code style: %#v", line)
		}
		if line.middleStyle != ansiCode || !strings.Contains(rendered, styleText(line.middle, ansiCode)) {
			t.Fatalf("quoted code lost its style: %#v", line)
		}
	}
}

func TestHTMLQuoteWithCodeFitsNarrowTableColumns(t *testing.T) {
	body, _ := contentText(`<table><tr><td><blockquote><pre><code>  x</code></pre></blockquote></td><td>另一列有很长的正文需要换行</td></tr></table>`)
	for _, width := range []int{23, 24, 40} {
		lines := layoutBodyLines(body, width)
		if !strings.Contains(tableLayoutText(lines), "│ │   x") {
			t.Fatalf("width %d: table lost quoted code:\n%s", width, tableLayoutText(lines))
		}
		for _, line := range lines {
			if stringCellWidth(styledLineText(line)) > width {
				t.Fatalf("width %d: nested code overflowed: %q", width, styledLineText(line))
			}
		}
	}
}

func TestEmptyQuotedCommentKeepsIdentity(t *testing.T) {
	for _, source := range []string{"", "<blockquote></blockquote>", "<blockquote><p></p></blockquote>"} {
		body, _ := contentText(source)
		for _, line := range layoutContentLines(body, 40, "101") {
			if line.commentID != "101" {
				t.Fatalf("empty quoted comment lost identity: %#v", line)
			}
		}
	}
}

func TestHTMLQuoteWithoutClosingTagKeepsExcerpt(t *testing.T) {
	got := tableLayoutText(layoutBodyLines(bodyText(`<blockquote><p>截断的引用`), 40))
	if got != "│ 截断的引用" {
		t.Fatalf("incomplete quote lost content: %q", got)
	}
}

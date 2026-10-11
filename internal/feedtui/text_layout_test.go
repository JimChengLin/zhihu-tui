package feedtui

import (
	"strings"
	"testing"
)

func TestCodeBlockKeepsSemanticBoundaryAndFormatting(t *testing.T) {
	body, _ := contentText(`<p>前文。</p><pre><code class="language-text">2021-03-15 &gt;&gt;&gt; zig
  indented

done</code></pre><p>后文。</p>`)
	lines := layoutBodyLines(body, 40)

	want := []styledLine{
		{text: "前文。"},
		{},
		{},
		{text: "┌─ 代码", style: ansiCode},
		{text: "│ 2021-03-15 >>> zig", style: ansiCode},
		{text: "│   indented", style: ansiCode},
		{text: "│ ", style: ansiCode},
		{text: "│ done", style: ansiCode},
		{text: "└─", style: ansiCode},
		{},
		{},
		{text: "后文。"},
	}
	if len(lines) != len(want) {
		t.Fatalf("layoutBodyLines()=%#v, want %#v", lines, want)
	}
	for index := range want {
		if lines[index].text != want[index].text || lines[index].style != want[index].style {
			t.Fatalf("layoutBodyLines()[%d]=%#v, want %#v", index, lines[index], want[index])
		}
	}
}

func TestCodeBlockWrapsWithoutLosingWhitespaceOrGraphemes(t *testing.T) {
	body, _ := contentText(`<pre><code>  🇳🇱abcdef</code></pre>`)
	lines := layoutBodyLines(body, 8)
	want := []string{"┌─ 代码", "│   🇳🇱ab", "│ cdef", "└─"}
	if got := styledLineTexts(lines); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("layoutBodyLines()=%q, want %q", got, want)
	}
}

func TestStandaloneClassCodeIsRenderedAsBlock(t *testing.T) {
	body, _ := contentText(`<p>运行结果：</p><code class="language-text">first
second</code><p>普通 <code>inline()</code> 代码。</p>`)
	lines := layoutBodyLines(body, 40)
	rendered := strings.Join(styledLineTexts(lines), "\n")
	if !strings.Contains(rendered, "┌─ 代码\n│ first\n│ second\n└─") {
		t.Fatalf("class code was not rendered as a block: %q", rendered)
	}
	if !strings.Contains(rendered, "普通 inline() 代码。") {
		t.Fatalf("inline code was not kept in prose: %q", rendered)
	}
}

func TestBodyLinkUsesSoftVioletWithoutChangingVisibleText(t *testing.T) {
	body, _ := contentText(`<p><a href="https://www.zhihu.com/question/1">链接标题</a>，普通正文。</p>`)
	if got := stripInlineLinkMarkers(body); got != "链接标题，普通正文。" {
		t.Fatalf("contentText()=%q, want visible text unchanged", got)
	}

	lines := layoutBodyLines(body, 40)
	if len(lines) != 1 || styledLineText(lines[0]) != "链接标题，普通正文。" {
		t.Fatalf("body=%q, layoutBodyLines()=%#v", body, lines)
	}
	if len(lines[0].segments) != 2 {
		t.Fatalf("segments=%#v, want linked and normal spans", lines[0].segments)
	}
	if lines[0].segments[0] != (styledSegment{text: "链接标题", style: ansiLink}) {
		t.Fatalf("linked segment=%#v", lines[0].segments[0])
	}
	if lines[0].segments[1] != (styledSegment{text: "，普通正文。"}) {
		t.Fatalf("normal segment=%#v", lines[0].segments[1])
	}

	rendered, _ := renderStyledLine(lines[0], 40)
	if !strings.Contains(rendered, styleText("链接标题", ansiLink)) {
		t.Fatalf("rendered link does not use link color: %q", rendered)
	}
	if strings.Contains(rendered, styleText("，普通正文。", ansiLink)) {
		t.Fatalf("normal body text inherited link color: %q", rendered)
	}
}

func TestBodyLinkKeepsStyleAcrossWrappedLines(t *testing.T) {
	body, _ := contentText(`<p>前文 <a href="https://www.zhihu.com/question/1">一段很长的链接文字跨越换行</a> 后文</p>`)
	lines := layoutBodyLines(body, 12)
	visible := make([]string, len(lines))
	for index := range lines {
		visible[index] = styledLineText(lines[index])
	}
	if got := strings.Join(visible, ""); strings.ReplaceAll(got, " ", "") != "前文一段很长的链接文字跨越换行后文" {
		t.Fatalf("body=%q, lines=%#v, wrapped visible text changed: %q", body, lines, got)
	}

	linkText := ""
	for _, line := range lines {
		for _, segment := range line.segments {
			if segment.style == ansiLink {
				linkText += segment.text
			}
		}
	}
	if linkText != "一段很长的链接文字跨越换行" {
		t.Fatalf("wrapped link text=%q", linkText)
	}
}

func TestImageLinkBecomesImagePlaceholder(t *testing.T) {
	body, images := contentText(`<p>评论正文[尴尬]<a data-caption="" href="https://picx.zhimg.com/v2-test_r.jpg?source=comment">查看图片</a></p>`)
	if images != 1 {
		t.Fatalf("image count=%d, want 1", images)
	}
	if strings.Contains(body, "查看图片") || strings.Contains(body, inlineLinkStartMarker) {
		t.Fatalf("image link was left as an inline link: %q", body)
	}
	if !strings.Contains(body, "▣ 图片 1") {
		t.Fatalf("image placeholder missing: %q", body)
	}
}

func TestImagesAndInlineLinksKeepDocumentOrder(t *testing.T) {
	body, images := contentText(`<p><a href="https://www.zhihu.com/question/1">普通链接</a></p>
<img src="https://picx.zhimg.com/first.jpg">
<a href="https://picx.zhimg.com/second.jpg">查看图片</a>
<a href="https://example.com/image.webp"><img src="https://example.com/thumb.webp"></a>`)
	if images != 3 {
		t.Fatalf("image count=%d, want 3", images)
	}
	if !strings.Contains(body, inlineLinkStartMarker+"普通链接"+inlineLinkEndMarker) {
		t.Fatalf("ordinary link lost link styling markers: %q", body)
	}
	first := strings.Index(body, "▣ 图片 1")
	second := strings.Index(body, "▣ 图片 2")
	third := strings.Index(body, "▣ 图片 3")
	if first < 0 || second <= first || third <= second {
		t.Fatalf("image placeholders are out of document order: %q", body)
	}
}

func TestLazyImageDoesNotCountNoscriptFallback(t *testing.T) {
	const content = `<p>前文。</p><figure data-size="normal"><noscript><img src="https://picx.zhimg.com/v2-first_r.jpg" data-size="normal" /></noscript><img src="data:image/svg+xml;utf8,&lt;svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 690 1000'&gt;&lt;/svg&gt;" data-actualsrc="https://picx.zhimg.com/v2-first_r.jpg" class="origin_image zh-lightbox-thumb lazy" /></figure><p>后文。</p>`
	const want = "前文。\n\n▣ 图片 1\n\n后文。"
	body, images := contentText(content)
	if images != 1 {
		t.Fatalf("image count=%d, want 1", images)
	}
	if body != want {
		t.Fatalf("body=%q, want %q", body, want)
	}
	item, ok := parseFeedItem(map[string]any{
		"target": map[string]any{
			"id":          "123",
			"type":        "answer",
			"content":     content,
			"content_img": []any{"https://picx.zhimg.com/v2-first_r.jpg"},
			"question":    map[string]any{"title": "测试问题"},
		},
	})
	if !ok {
		t.Fatal("parseFeedItem returned false")
	}
	if item.imageCount != 1 || item.body != want {
		t.Fatalf("feed image count=%d, body=%q; want 1 and %q", item.imageCount, item.body, want)
	}
}

func TestLazyImagesAndImageLinksKeepOrderWithPreviousImages(t *testing.T) {
	body, images := contentTextFrom(`<figure><NOSCRIPT data-fallback="image">
<img src="https://picx.zhimg.com/first.jpg">
</NOSCRIPT><img src="https://picx.zhimg.com/first.jpg" class="lazy"></figure>
<p><a href="https://picx.zhimg.com/second.jpg">查看图片</a></p>
<figure><noscript><img src="https://picx.zhimg.com/third.jpg"></noscript><img src="https://picx.zhimg.com/third.jpg" class="lazy"></figure>`, 4)
	if images != 3 {
		t.Fatalf("image count=%d, want 3", images)
	}
	if want := "▣ 图片 5\n\n▣ 图片 6\n\n▣ 图片 7"; body != want {
		t.Fatalf("body=%q, want %q", body, want)
	}
}

func TestRepeatedVisibleImageIsCountedAtEachPosition(t *testing.T) {
	body, images := contentText(`<img src="https://picx.zhimg.com/same.jpg"><p>中间正文。</p><img src="https://picx.zhimg.com/same.jpg">`)
	if images != 2 {
		t.Fatalf("image count=%d, want 2", images)
	}
	if want := "▣ 图片 1\n\n中间正文。\n\n▣ 图片 2"; body != want {
		t.Fatalf("body=%q, want %q", body, want)
	}
}

func TestWrapTextKeepsClosingPunctuationOnPreviousLine(t *testing.T) {
	lines := wrapText("中文中文。下一句", 8)
	if len(lines) < 2 {
		t.Fatalf("lines=%q", lines)
	}
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "。") {
			t.Fatalf("closing punctuation started a line: %q", lines)
		}
	}
}

func TestWrapTextMovesContentInsteadOfOverflowingForClosingPunctuation(t *testing.T) {
	text := "比肩cuBLAS的性能。"
	lines := wrapText(text, 16)
	if strings.Join(lines, "") != text {
		t.Fatalf("wrapped text changed content: %q", lines)
	}
	for _, line := range lines {
		if width := stringCellWidth(line); width > 16 {
			t.Fatalf("wrapped line width=%d exceeds 16 cells: %q", width, line)
		}
		if strings.Contains(line, "…") {
			t.Fatalf("wrapped line contains a synthetic ellipsis: %q", line)
		}
	}
	if len(lines) < 2 || lines[1] != "能。" {
		t.Fatalf("closing punctuation was not kept with preceding content: %q", lines)
	}
}

func TestWrapTextDoesNotSplitShortASCIITokens(t *testing.T) {
	lines := wrapText("这是一个 Zig community 和 100% 测试", 18)
	for _, line := range lines {
		if line == "Z" || strings.HasSuffix(line, " 10") || strings.HasPrefix(line, "0%") {
			t.Fatalf("ASCII token was split: %q", lines)
		}
	}
}

func TestTextLayoutKeepsGraphemeClustersTogether(t *testing.T) {
	tests := []struct {
		name    string
		cluster string
		width   int
	}{
		{name: "Luxembourg flag", cluster: "🇱🇺", width: 2},
		{name: "Netherlands flag", cluster: "🇳🇱", width: 2},
		{name: "skin tone", cluster: "👍🏽", width: 2},
		{name: "ZWJ family", cluster: "👨‍👩‍👧‍👦", width: 2},
		{name: "variation selector", cluster: "❤️", width: 2},
		{name: "combining accent", cluster: "é", width: 1},
		{name: "keycap", cluster: "1️⃣", width: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			units := textUnits("x" + test.cluster + "y")
			if len(units) != 3 || units[1] != test.cluster {
				t.Fatalf("textUnits()=%q, cluster was split", units)
			}
			if width := stringCellWidth(test.cluster); width != test.width {
				t.Fatalf("stringCellWidth()=%d, want %d", width, test.width)
			}
			text := "1234" + test.cluster
			lines := wrapText(text, 4)
			if strings.Join(lines, "") != text || len(lines) != 2 || lines[1] != test.cluster {
				t.Fatalf("wrapText()=%q, cluster was split", lines)
			}
			if truncated := truncateCells(text+"x", 5); truncated != "1234…" {
				t.Fatalf("truncateCells()=%q, cluster was split", truncated)
			}
		})
	}
}

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

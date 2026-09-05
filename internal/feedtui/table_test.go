package feedtui

import (
	"regexp"
	"strings"
	"testing"
)

func TestHTMLTableKeepsRowsColumnsAndSurroundingProse(t *testing.T) {
	body, _ := contentText(`<p>综合对比</p><table><thead><tr><th>维度</th><th>CLAPS</th></tr></thead><tbody><tr><td>缓存</td><td>内存</td></tr><tr><td>磁盘</td><td></td></tr></tbody></table><p>后文</p>`)
	got := tableLayoutText(layoutBodyLines(body, 40))
	want := `综合对比


┌──────┬───────┐
│ 维度 │ CLAPS │
├──────┼───────┤
│ 缓存 │ 内存  │
├──────┼───────┤
│ 磁盘 │       │
└──────┴───────┘


后文`
	if got != want {
		t.Fatalf("table layout:\n%s\nwant:\n%s", got, want)
	}
	lines := layoutBodyLines(body, 40)
	for _, line := range lines {
		if strings.Contains(styledLineText(line), "CLAPS") {
			rendered, _ := renderStyledLine(line, 40)
			if !strings.Contains(rendered, styleText("CLAPS", ansiBold)) {
				t.Fatalf("header lost bold styling: %q", rendered)
			}
		}
	}
}

func TestHTMLTableWrapsComparisonWithoutLosingCells(t *testing.T) {
	rows := [][]string{
		{"维度", "CLAPS（字节）", "Azure SQL Hyperscale（微软）", "CloudJump III（阿里）"},
		{"系统背景", "ByteStore 分布式文件系统", "Azure SQL 云数据库", "阿里云 MySQL 云数据库"},
		{"主要技术", "弹性代理池、One-hop 读、TierTable", "文件分片（striping）、RBPEX 写回", "BPE、OSS Buffer、语义感知分层"},
		{"写路径", "前台写 TierTable（内存），异步刷 WAL", "写本地 SSD 即返回，后台异步刷 Azure", "经 OSS Buffer 聚合后异步写 OSS"},
		{"读路径", "优先 Client 缓存直连 ChunkServer", "本地 RBPEX → Azure Storage + 日志重放", "DRAM → BPE → ESSD → OSS 逐层回填"},
		{"字符宽度", "中文🇳🇱👨‍👩‍👧‍👦", "é & SQL", "长单词abcdefghijklmno"},
	}
	var source strings.Builder
	source.WriteString(`<table class="data-table">`)
	for _, row := range rows {
		source.WriteString("<tr>")
		for _, cell := range row {
			source.WriteString("<td>" + cell + "</td>")
		}
		source.WriteString("</tr>")
	}
	source.WriteString("</table>")
	body, _ := contentText(source.String())
	for _, width := range []int{60, 80, 112} {
		lines := layoutBodyLines(body, width)
		rowIndex := 0
		cells := make([]string, 4)
		for _, line := range lines {
			text := styledLineText(line)
			if got := stringCellWidth(text); got > width {
				t.Fatalf("width %d: line uses %d cells: %q", width, got, text)
			}
			if strings.HasPrefix(text, "│") {
				parts := strings.Split(text, "│")
				if len(parts) != 6 {
					t.Fatalf("width %d: broken column boundaries: %q", width, text)
				}
				for column := range cells {
					cells[column] += parts[column+1]
				}
			}
			if strings.HasPrefix(text, "├") || strings.HasPrefix(text, "└") {
				for column, cell := range cells {
					got := strings.Join(strings.Fields(cell), "")
					want := strings.Join(strings.Fields(rows[rowIndex][column]), "")
					if got != want {
						t.Fatalf("width %d, row %d, column %d: got %q, want %q", width, rowIndex, column, got, want)
					}
					cells[column] = ""
				}
				rowIndex++
			}
		}
		if rowIndex != len(rows) {
			t.Fatalf("width %d: rendered %d rows, want %d", width, rowIndex, len(rows))
		}
	}
}

func TestHTMLTablePreservesCellBreaksLinksAndImages(t *testing.T) {
	body, images := contentText(`<TABLE><caption>表格标题</caption><TR><TD><p>第一段</p><p><a href="https://example.com">链接 &amp; SQL</a><br>下一行</p></TD><TD><img src="test.png"></TD></TR><TR><TD>尾行</TD></TR></TABLE>`)
	if images != 1 {
		t.Fatalf("image count=%d, want 1", images)
	}
	lines := layoutBodyLines(body, 60)
	rendered := tableLayoutText(lines)
	for _, text := range []string{"表格标题", "第一段", "链接 & SQL", "下一行", "▣ 图片 1", "尾行"} {
		if !strings.Contains(rendered, text) {
			t.Fatalf("table lost %q:\n%s", text, rendered)
		}
	}
	for _, line := range lines {
		for _, segment := range line.segments {
			if segment.text == "链接 & SQL" && segment.style == ansiLink {
				return
			}
		}
	}
	t.Fatalf("table link lost styling: %#v", lines)
}

func TestHTMLTableUsesColumnLabelsInNarrowWindow(t *testing.T) {
	body, _ := contentText(`<table><tr><th>维度</th><th>CLAPS</th><th>Azure SQL</th><th>CloudJump III</th></tr><tr><td>缓存</td><td>TierTable</td><td>RBPEX</td><td>BPE</td></tr><tr><td>层级</td><td>代理层</td><td>存储层</td><td>引擎层</td></tr></table>`)
	lines := layoutBodyLines(body, 24)
	for _, line := range lines {
		if stringCellWidth(styledLineText(line)) > 24 {
			t.Fatalf("narrow table overflowed: %q", styledLineText(line))
		}
	}
	rendered := tableLayoutText(lines)
	for _, want := range []string{"维度：缓存", "CLAPS：TierTable", "Azure SQL：RBPEX", "CloudJump III：BPE", "维度：层级", "CLAPS：代理层", "Azure SQL：存储层", "CloudJump III：引擎层"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("narrow table lost %q:\n%s", want, rendered)
		}
	}
}

func TestHTMLTableMarkersDoNotLeakIntoTitlesOrExcerpts(t *testing.T) {
	source := `<table><tr><td>缓存</td><td>TierTable</td></tr></table>`
	item, ok := parseFeedItem(map[string]any{"target": map[string]any{"id": "1", "type": "pin", "content": source}})
	if !ok {
		t.Fatal("table-only pin was not parsed")
	}
	for name, value := range map[string]string{
		"title":          item.title,
		"folded excerpt": foldedItemExcerpt(item),
		"link excerpt":   linkCardExcerpt(map[string]any{"content": source}),
	} {
		if strings.ContainsRune(value, '\ue000') {
			t.Fatalf("%s contains table markers: %q", name, value)
		}
	}
	if got := foldedItemExcerpt(item); got != "缓存 TierTable" {
		t.Fatalf("folded excerpt=%q", got)
	}
	if got := linkCardExcerpt(map[string]any{"content": source}); got != "缓存 TierTable" {
		t.Fatalf("link excerpt=%q", got)
	}
	if !strings.Contains(tableLayoutText(layoutBodyLines(item.body, 40)), "│ 缓存 │ TierTable │") {
		t.Fatalf("table-only pin lost its first row: %q", item.body)
	}
}

func TestHTMLTablesKeepCommentIdentityAndSeparateBlocks(t *testing.T) {
	body, _ := contentText(`<table><tr><td>第一张</td></tr></table><p>中间</p><table><tr><td>第二张</td></tr></table>`)
	body = commentStartMarker + "comment-1" + commentMarkerEnd + "\n" + body
	lines := layoutBodyLines(body, 40)
	for _, line := range lines {
		if line.commentID != "comment-1" {
			t.Fatalf("table line lost comment identity: %#v", line)
		}
	}
	rendered := tableLayoutText(lines)
	if strings.Count(rendered, "┌") != 2 || !strings.Contains(rendered, "└────────┘\n\n中间\n\n┌") {
		t.Fatalf("table blocks were not separated:\n%s", rendered)
	}
}

func TestHTMLTableMarkupInCodeDoesNotIntroduceTableMarkers(t *testing.T) {
	body, _ := contentText(`<pre><code>&lt;table&gt;&lt;tr&gt;&lt;td&gt;代码示例&lt;/td&gt;&lt;/tr&gt;&lt;/table&gt;</code></pre><table><tr><td>实际表格</td></tr></table>`)
	rendered := tableLayoutText(layoutBodyLines(body, 40))
	if strings.ContainsRune(rendered, '\ue000') || !strings.Contains(rendered, "│ 代码示例\n└─") || !strings.Contains(rendered, "│ 实际表格 │") {
		t.Fatalf("table markup in code changed block boundaries:\n%s", rendered)
	}
}

func TestHTMLTableExcerptWithoutClosingTagsKeepsItsContent(t *testing.T) {
	rendered := tableLayoutText(layoutBodyLines(bodyText(`<table><tr><td>维度<td>CLAPS<tr><td>缓存<td>内存`), 40))
	if !strings.Contains(rendered, "│ 维度 │ CLAPS │") || !strings.Contains(rendered, "│ 缓存 │ 内存  │") {
		t.Fatalf("incomplete table lost content:\n%s", rendered)
	}
}

func TestRenderAppPreservesTableBordersAndCells(t *testing.T) {
	body, _ := contentText(`<table><tr><th>维度</th><th>CLAPS</th></tr><tr><td>缓存</td><td>内存</td></tr></table>`)
	ansiPattern := regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	for _, width := range []int{60, 100, 160} {
		model := &app{items: []feedItem{{kind: "article", title: "综合对比", body: body}}, width: width, height: 30}
		lines, _ := renderApp(model)
		var frame strings.Builder
		if err := writeFrame(&frame, lines, width, model.height); err != nil {
			t.Fatal(err)
		}
		plain := ansiPattern.ReplaceAllString(frame.String(), "")
		if !strings.Contains(plain, "│ 维度 │ CLAPS │") || !strings.Contains(plain, "│ 缓存 │ 内存  │") {
			t.Fatalf("terminal width %d lost table content:\n%s", width, plain)
		}
		for _, line := range strings.Split(plain, "\r\n") {
			if stringCellWidth(line) >= width {
				t.Fatalf("terminal width %d overflowed: %q", width, line)
			}
		}
	}
}

func tableLayoutText(lines []styledLine) string {
	texts := make([]string, len(lines))
	for index, line := range lines {
		texts[index] = styledLineText(line)
	}
	return strings.Join(texts, "\n")
}

package feedtui

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

const (
	ansiReset = "\033[0m"
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"
	ansiCyan  = "\033[36m"
	ansiBlue  = "\033[38;5;75m"
	ansiGreen = "\033[38;5;114m"
	ansiRed   = "\033[38;5;203m"
)

type styledLine struct {
	text  string
	style string
}

type layoutMetrics struct {
	bodyHeight int
	bodyLines  int
	maxScroll  int
}

func renderApp(model *app) ([]styledLine, layoutMetrics) {
	if model.width < 42 || model.height < 14 {
		return renderTooSmall(model.width, model.height), layoutMetrics{}
	}
	if model.showHelp {
		return renderHelp(model.width, model.height), layoutMetrics{}
	}
	if len(model.items) == 0 {
		return renderEmpty(model), layoutMetrics{}
	}

	item := model.items[model.index]
	contentWidth := minInt(model.width-6, 100)
	left := maxInt(2, (model.width-contentWidth)/2)
	line := func(text, style string) styledLine {
		return styledLine{text: strings.Repeat(" ", left) + text, style: style}
	}
	lines := []styledLine{
		{text: headerText(model), style: ansiBold + ansiCyan},
		{},
	}

	action := item.action
	if relative := formatRelativeTime(item.createdAt, time.Now()); relative != "" {
		action += "  ·  " + relative
	}
	lines = append(lines, line(truncateCells(action, contentWidth), ansiDim))

	titleLines := wrapText(item.title, contentWidth)
	if len(titleLines) > 3 {
		titleLines = titleLines[:3]
		titleLines[2] = truncateCells(strings.TrimSuffix(titleLines[2], "…")+"…", contentWidth)
	}
	for _, titleLine := range titleLines {
		lines = append(lines, line(titleLine, ansiBold+ansiBlue))
	}

	authorLine := item.author
	if item.headline != "" {
		authorLine += "  ·  " + item.headline
	}
	lines = append(lines, line(truncateCells(authorLine, contentWidth), ansiDim))
	meta := item.stats
	if item.hasImage {
		if meta != "" {
			meta += "  ·  "
		}
		meta += "含图片"
	}
	if meta != "" {
		lines = append(lines, line(truncateCells(meta, contentWidth), ansiGreen))
	}
	lines = append(lines, line(strings.Repeat("─", contentWidth), ansiDim))

	body := item.body
	if body == "" {
		body = "这条动态没有正文摘要；按 o 在知乎中查看完整内容。"
	}
	bodyLines := wrapText(body, contentWidth)
	fixedBottom := 4
	bodyHeight := model.height - len(lines) - fixedBottom
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	maxScroll := maxInt(0, len(bodyLines)-bodyHeight)
	if model.scroll > maxScroll {
		model.scroll = maxScroll
	}
	end := minInt(len(bodyLines), model.scroll+bodyHeight)
	for _, bodyLine := range bodyLines[model.scroll:end] {
		lines = append(lines, line(bodyLine, ""))
	}
	for len(lines) < model.height-fixedBottom {
		lines = append(lines, styledLine{})
	}

	lines = append(lines, line(strings.Repeat("─", contentWidth), ansiDim))
	status := fmt.Sprintf("第 %d / %d 条", model.index+1, len(model.items))
	if len(bodyLines) > bodyHeight {
		status += fmt.Sprintf("  ·  正文 %d–%d / %d 行", model.scroll+1, end, len(bodyLines))
	}
	if model.loading {
		loadingText := "正在预取后续动态"
		if model.refreshing {
			loadingText = "正在刷新关注流"
		}
		status += "  ·  " + spinnerFrames[model.spinner%len(spinnerFrames)] + " " + loadingText
	} else if model.end {
		status += "  ·  已到当前关注流末尾"
	}
	if model.message != "" {
		status += "  ·  " + model.message
	}
	lines = append(lines, line(truncateCells(status, contentWidth), ansiDim))
	hints := "j/k 滚动  space/b 翻页  n/p·h/l 切换  o 打开  r 刷新  ? 帮助  q 退出"
	lines = append(lines, line(truncateCells(hints, contentWidth), ansiCyan))
	lines = append(lines, styledLine{})

	return fitHeight(lines, model.height), layoutMetrics{
		bodyHeight: bodyHeight,
		bodyLines:  len(bodyLines),
		maxScroll:  maxScroll,
	}
}

func renderTooSmall(width, height int) []styledLine {
	return []styledLine{
		{text: "知乎关注", style: ansiBold + ansiCyan},
		{},
		{text: fmt.Sprintf("终端当前为 %d×%d，至少需要 42×14。", width, height), style: ansiRed},
		{text: "请放大终端窗口，或按 q 退出。", style: ansiDim},
	}
}

func renderEmpty(model *app) []styledLine {
	lines := []styledLine{{text: headerText(model), style: ansiBold + ansiCyan}, {}, {}}
	if model.err != nil {
		lines = append(lines,
			styledLine{text: "关注流加载失败", style: ansiBold + ansiRed},
			styledLine{text: truncateCells(model.err.Error(), maxInt(20, model.width-4))},
			styledLine{},
			styledLine{text: "按 r 重试，按 q 退出。", style: ansiCyan},
		)
	} else if model.loading {
		lines = append(lines,
			styledLine{text: spinnerFrames[model.spinner%len(spinnerFrames)] + " 正在加载你的知乎关注流…", style: ansiBlue},
			styledLine{},
			styledLine{text: "首次加载完成后会自动预取，之后切换无需等待。", style: ansiDim},
		)
	} else {
		lines = append(lines,
			styledLine{text: "关注流里暂时没有可显示的动态。", style: ansiDim},
			styledLine{text: "按 r 刷新，按 q 退出。", style: ansiCyan},
		)
	}
	return fitHeight(lines, model.height)
}

func renderHelp(width, height int) []styledLine {
	contentWidth := minInt(width-6, 76)
	left := maxInt(2, (width-contentWidth)/2)
	pad := strings.Repeat(" ", left)
	lines := []styledLine{
		{text: pad + "知乎关注 · 快捷键", style: ansiBold + ansiCyan},
		{},
		{text: pad + "j / ↓       向下滚动；正文到底后进入下一条"},
		{text: pad + "k / ↑       向上滚动；正文顶部时回到上一条"},
		{text: pad + "space / f    向下翻一页；页尾后进入下一条"},
		{text: pad + "b / PageUp   向上翻一页"},
		{text: pad + "d / u        向下 / 向上翻半页"},
		{text: pad + "n/p · h/l · ←/→  下一条 / 上一条"},
		{text: pad + "g / G        第一条 / 最后一条已加载动态"},
		{text: pad + "o            用默认浏览器打开当前动态"},
		{text: pad + "r            从头刷新关注流"},
		{text: pad + "q / Ctrl-C   退出并恢复终端"},
		{},
		{text: pad + "按 ? 返回阅读。", style: ansiCyan},
	}
	return fitHeight(lines, height)
}

func headerText(model *app) string {
	text := " 知乎关注 "
	if len(model.items) > 0 {
		text += fmt.Sprintf("· %s · 已加载 %d 条", typeLabel(model.items[model.index].kind), len(model.items))
	}
	return truncateCells(text, maxInt(1, model.width-1))
}

func fitHeight(lines []styledLine, height int) []styledLine {
	if len(lines) > height {
		return lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, styledLine{})
	}
	return lines
}

func writeFrame(out interface{ Write([]byte) (int, error) }, lines []styledLine, width, height int) error {
	var builder strings.Builder
	builder.WriteString("\033[H")
	for row := 0; row < height; row++ {
		builder.WriteString("\033[2K")
		if row < len(lines) {
			text := truncateCells(lines[row].text, maxInt(1, width-1))
			if lines[row].style != "" && text != "" {
				builder.WriteString(lines[row].style)
				builder.WriteString(text)
				builder.WriteString(ansiReset)
			} else {
				builder.WriteString(text)
			}
		}
		if row+1 < height {
			builder.WriteString("\r\n")
		}
	}
	_, err := out.Write([]byte(builder.String()))
	return err
}

func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	paragraphs := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(paragraphs))
	for index, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			if index > 0 && len(lines) > 0 && lines[len(lines)-1] != "" {
				lines = append(lines, "")
			}
			continue
		}
		var builder strings.Builder
		cells := 0
		for _, token := range textTokens(paragraph) {
			if token == " " {
				if cells > 0 && cells < width {
					builder.WriteByte(' ')
					cells++
				}
				continue
			}
			tokenWidth := stringCellWidth(token)
			if cells > 0 && cells+tokenWidth > width {
				runes := []rune(token)
				if len(runes) == 1 && isClosingPunctuation(runes[0]) && cells+tokenWidth <= width+2 {
					builder.WriteString(token)
					cells += tokenWidth
					continue
				}
				lines = append(lines, strings.TrimSpace(builder.String()))
				builder.Reset()
				cells = 0
			}
			if tokenWidth <= width {
				builder.WriteString(token)
				cells += tokenWidth
				continue
			}
			for _, r := range token {
				runeWidth := runeCellWidth(r)
				if cells > 0 && cells+runeWidth > width {
					lines = append(lines, strings.TrimSpace(builder.String()))
					builder.Reset()
					cells = 0
				}
				builder.WriteRune(r)
				cells += runeWidth
			}
		}
		if builder.Len() > 0 {
			lines = append(lines, strings.TrimSpace(builder.String()))
		}
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func textTokens(text string) []string {
	tokens := make([]string, 0, len([]rune(text)))
	var ascii strings.Builder
	flushASCII := func() {
		if ascii.Len() > 0 {
			tokens = append(tokens, ascii.String())
			ascii.Reset()
		}
	}
	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			flushASCII()
			if len(tokens) == 0 || tokens[len(tokens)-1] != " " {
				tokens = append(tokens, " ")
			}
		case r >= 0x21 && r <= 0x7e:
			ascii.WriteRune(r)
		default:
			flushASCII()
			tokens = append(tokens, string(r))
		}
	}
	flushASCII()
	return tokens
}

func isClosingPunctuation(r rune) bool {
	return strings.ContainsRune("，。！？；：、）】》〉」』…,.!?;:)]}", r)
}

func truncateCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if stringCellWidth(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	var builder strings.Builder
	cells := 0
	for _, r := range text {
		runeWidth := runeCellWidth(r)
		if cells+runeWidth > width-1 {
			break
		}
		builder.WriteRune(r)
		cells += runeWidth
	}
	return builder.String() + "…"
}

func stringCellWidth(text string) int {
	width := 0
	for _, r := range text {
		width += runeCellWidth(r)
	}
	return width
}

func runeCellWidth(r rune) int {
	if r == 0 || r == '\n' || r == '\r' || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
		return 0
	}
	if r >= 0x1100 && (r <= 0x115f ||
		r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x1f300 && r <= 0x1faff) ||
		(r >= 0x20000 && r <= 0x3fffd)) {
		return 2
	}
	return 1
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

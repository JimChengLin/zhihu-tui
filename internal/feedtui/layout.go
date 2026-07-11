package feedtui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/rivo/uniseg"
)

const (
	ansiReset         = "\033[0m"
	ansiBold          = "\033[1m"
	ansiDim           = "\033[2m"
	ansiCyan          = "\033[36m"
	ansiBlue          = "\033[38;5;75m"
	ansiGreen         = "\033[38;5;114m"
	ansiRed           = "\033[38;5;203m"
	minReadingWidth   = 96
	maxReadingWidth   = 112
	paragraphGapLines = 2
)

type styledLine struct {
	text        string
	style       string
	suffix      string
	suffixStyle string
	raw         bool
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
	if !model.zenMode && model.width >= 120 && model.height >= 20 {
		return renderWideApp(model)
	}
	return renderSingleApp(model)
}

func renderSingleApp(model *app) ([]styledLine, layoutMetrics) {
	item := model.items[model.index]
	contentWidth := adaptiveReadingWidth(model.width)
	left := maxInt(2, (model.width-contentWidth)/2)
	line := func(text, style string) styledLine {
		return styledLine{text: strings.Repeat(" ", left) + text, style: style}
	}
	lines := []styledLine{{}}
	if !model.hideFeedHeader {
		lines = []styledLine{line(headerText(model), ansiBold+ansiCyan), {}}
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
	if meta != "" {
		lines = append(lines, line(truncateCells(meta, contentWidth), ansiGreen))
	}
	body := item.body
	if model.commentMode {
		var commentLabel string
		body, commentLabel = formatCommentView(item, model.currentCommentState(), model.spinner)
		lines = append(lines, line(truncateCells(commentLabel, contentWidth), ansiBold+ansiCyan))
	}
	lines = append(lines, line(strings.Repeat("─", contentWidth), ansiDim))

	if body == "" {
		body = "这条动态没有正文摘要；按 o 在知乎中查看完整内容。"
	}
	bodyLines := addParagraphSpacing(wrapText(body, contentWidth))
	fixedBottom := 4
	availableBodyHeight := model.height - len(lines) - fixedBottom
	bodyHeight := availableBodyHeight
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	if len(bodyLines) < bodyHeight {
		bodyHeight = maxInt(1, len(bodyLines))
	}
	maxScroll := maxInt(0, len(bodyLines)-bodyHeight)
	if model.scroll > maxScroll {
		model.scroll = maxScroll
	}
	end := minInt(len(bodyLines), model.scroll+bodyHeight)
	thumbStart, thumbSize := scrollbarThumb(bodyHeight, len(bodyLines), model.scroll, maxScroll)
	for row, bodyLine := range bodyLines[model.scroll:end] {
		if maxScroll == 0 {
			lines = append(lines, line(bodyLine, ""))
			continue
		}
		bar := "┊"
		if row >= thumbStart && row < thumbStart+thumbSize {
			bar = "┃"
		}
		body := line(padCells(bodyLine, contentWidth)+" ", "")
		if model.pageAnchorVisible && model.scroll+row == model.pageAnchorLine {
			anchorText := bodyLine
			if strings.TrimSpace(anchorText) == "" {
				anchorText = strings.Repeat("┄", contentWidth)
			}
			body.text = strings.Repeat(" ", maxInt(0, left-2)) + "▸ " + padCells(anchorText, contentWidth) + " "
			body.style = ansiBlue
		}
		body.suffix = bar
		body.suffixStyle = ansiDim
		lines = append(lines, body)
	}
	for len(lines) < model.height-fixedBottom {
		lines = append(lines, styledLine{})
	}

	lines = append(lines, line(strings.Repeat("─", contentWidth), ansiDim))
	statusParts := make([]string, 0, 4)
	if !model.hideItemPosition {
		statusParts = append(statusParts, fmt.Sprintf("第 %d / %d 条", model.index+1, len(model.items)))
	}
	if model.commentMode {
		statusParts = append(statusParts, "评论区")
	}
	if len(bodyLines) > bodyHeight {
		statusParts = append(statusParts, fmt.Sprintf("正文 %d–%d / %d 行", model.scroll+1, end, len(bodyLines)))
	}
	if model.loading {
		loadingText := "正在预取后续动态"
		if model.refreshing {
			loadingText = "正在刷新关注流"
		}
		statusParts = append(statusParts, spinnerFrames[model.spinner%len(spinnerFrames)]+" "+loadingText)
	} else if model.end {
		statusParts = append(statusParts, "已到当前关注流末尾")
	}
	if model.message != "" {
		statusParts = append(statusParts, model.message)
	}
	status := strings.Join(statusParts, "  ·  ")
	lines = append(lines, line(truncateCells(status, contentWidth), ansiDim))
	hints := "j/k 滚动  space/b 7/8页/确认切换  n/p 直接切换  c 评论  z 专注  o 打开  r 刷新  ? 帮助  q 退出"
	if model.zenMode {
		hints = "j/k 滚动  space/b 7/8页/确认切换  n/p 直接切换  c 评论  z 双栏  o 打开  r 刷新  ? 帮助  q 退出"
	}
	lines = append(lines, line(truncateCells(hints, contentWidth), ansiCyan))
	lines = append(lines, styledLine{})

	return fitHeight(lines, model.height), layoutMetrics{
		bodyHeight: bodyHeight,
		bodyLines:  len(bodyLines),
		maxScroll:  maxScroll,
	}
}

func scrollbarThumb(trackHeight, contentHeight, scroll, maxScroll int) (int, int) {
	if maxScroll <= 0 || trackHeight <= 0 || contentHeight <= 0 {
		return 0, 0
	}
	thumbSize := maxInt(1, trackHeight*trackHeight/contentHeight)
	thumbSize = minInt(trackHeight, thumbSize)
	thumbStart := (trackHeight - thumbSize) * scroll / maxScroll
	return thumbStart, thumbSize
}

func padCells(text string, width int) string {
	text = truncateCells(text, width)
	return text + strings.Repeat(" ", maxInt(0, width-stringCellWidth(text)))
}

func adaptiveReadingWidth(viewportWidth int) int {
	available := maxInt(1, viewportWidth-6)
	target := viewportWidth * 3 / 4
	target = maxInt(minReadingWidth, minInt(maxReadingWidth, target))
	return minInt(available, target)
}

func addParagraphSpacing(lines []string) []string {
	result := make([]string, 0, len(lines)+len(lines)/4)
	for _, line := range lines {
		if line != "" {
			result = append(result, line)
			continue
		}
		if len(result) == 0 || result[len(result)-1] == "" {
			continue
		}
		for range paragraphGapLines {
			result = append(result, "")
		}
	}
	return result
}

func renderWideApp(model *app) ([]styledLine, layoutMetrics) {
	sidebarWidth := minInt(44, maxInt(30, model.width/4))
	mainWidth := model.width - sidebarWidth - 3
	mainModel := *model
	mainModel.width = mainWidth
	mainModel.hideFeedHeader = true
	mainModel.hideItemPosition = true
	mainLines, metrics := renderSingleApp(&mainModel)
	model.scroll = mainModel.scroll

	sidebarLines := renderSidebar(model, sidebarWidth)
	lines := make([]styledLine, model.height)
	for row := 0; row < model.height; row++ {
		lines[row] = mergeColumns(sidebarLines[row], sidebarWidth, mainLines[row], mainWidth)
	}
	return lines, metrics
}

func renderSidebar(model *app, width int) []styledLine {
	lines := make([]styledLine, model.height)
	lines[0] = styledLine{text: " 关注动态", style: ansiBold + ansiCyan}
	status := fmt.Sprintf(" 已加载 %d 条 · 当前第 %d 条", len(model.items), model.index+1)
	lines[1] = styledLine{text: truncateCells(status, width-1), style: ansiDim}

	visibleItems := maxInt(1, (model.height-5)/3)
	visibleItems = minInt(visibleItems, len(model.items))
	start := maxInt(0, minInt(model.sidebarStart, len(model.items)-visibleItems))
	if model.index < start {
		start = model.index
	} else if model.index >= start+visibleItems {
		start = model.index - visibleItems + 1
	}
	model.sidebarStart = start
	separator := strings.Repeat("─", width-1)
	if start > 0 {
		prefix := fmt.Sprintf(" ↑ 前面还有 %d 条 ", start)
		separator = prefix + strings.Repeat("─", maxInt(0, width-1-stringCellWidth(prefix)))
	}
	lines[2] = styledLine{text: separator, style: ansiDim}
	row := 3
	for index := start; index < start+visibleItems && row+1 < model.height-2; index++ {
		item := model.items[index]
		marker := "  "
		style := ""
		summaryPrefix := "  "
		summaryStyle := ansiDim
		_, isNew := model.newItemKeys[item.key]
		isReadTop := item.key == model.lastReadTopKey
		isReadBottom := item.key == model.lastReadBottomKey
		isReadBoundary := isReadTop || isReadBottom
		if isReadTop && isReadBottom {
			style = ansiCyan
			summaryPrefix = "  上次读到↓↑ · "
			summaryStyle = ansiCyan
		} else if isReadTop {
			style = ansiCyan
			summaryPrefix = "  上次读到↓ · "
			summaryStyle = ansiCyan
		} else if isReadBottom {
			style = ansiCyan
			summaryPrefix = "  上次读到↑ · "
			summaryStyle = ansiCyan
		} else if isNew {
			style = ansiGreen
		}
		if index == model.index {
			marker = "› "
			if isNew && !isReadBoundary {
				style = ansiBold + ansiGreen
			} else {
				style = ansiBold + ansiCyan
			}
		}
		titleWidth := maxInt(1, width-stringCellWidth(marker)-1)
		title := truncateCells(item.title, titleWidth)
		lines[row] = styledLine{text: marker + title, style: style}
		summary := firstNonEmpty(item.action, item.author, typeLabel(item.kind))
		lines[row+1] = styledLine{text: summaryPrefix + truncateCells(summary, maxInt(1, width-stringCellWidth(summaryPrefix)-1)), style: summaryStyle}
		row += 3
	}
	return lines
}

func mergeColumns(left styledLine, leftWidth int, right styledLine, rightWidth int) styledLine {
	leftText, leftTextWidth := renderStyledLine(left, leftWidth)
	leftPadding := strings.Repeat(" ", maxInt(0, leftWidth-leftTextWidth))
	rightText, _ := renderStyledLine(right, maxInt(1, rightWidth-1))
	text := leftText + leftPadding + styleText(" │ ", ansiDim) + rightText
	return styledLine{text: text, raw: true}
}

func renderStyledLine(line styledLine, maxWidth int) (string, int) {
	suffix := truncateCells(line.suffix, maxWidth)
	suffixWidth := stringCellWidth(suffix)
	text := truncateCells(line.text, maxInt(0, maxWidth-suffixWidth))
	return styleText(text, line.style) + styleText(suffix, line.suffixStyle), stringCellWidth(text) + suffixWidth
}

func styleText(text, style string) string {
	if text == "" || style == "" {
		return text
	}
	return style + text + ansiReset
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
		{text: pad + "j / ↓       向下滚动；正文底部停止"},
		{text: pad + "k / ↑       向上滚动；正文顶部停止"},
		{text: pad + "space        向下 7/8 页；到底后再按一次切换下一条"},
		{text: pad + "b            向上 7/8 页；到顶后再按一次切换上一条"},
		{text: pad + "d / u        向下 / 向上半页，不切换动态"},
		{text: pad + "n/p · h/l · ←/→  下一条 / 上一条"},
		{text: pad + "g / G        第一条 / 最后一条已加载动态"},
		{text: pad + "c            加载评论 / 返回正文"},
		{text: pad + "z            专注阅读 / 恢复双栏"},
		{text: pad + "o            用默认浏览器打开当前动态"},
		{text: pad + "r            刷新；新标题变绿 / 标记进程阅读范围"},
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
			if lines[row].raw {
				builder.WriteString(lines[row].text)
			} else {
				text, _ := renderStyledLine(lines[row], maxInt(1, width-1))
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
				if len(runes) == 1 && isClosingPunctuation(runes[0]) {
					current := textUnits(strings.TrimSpace(builder.String()))
					last := current[len(current)-1]
					prefix := strings.TrimSpace(strings.Join(current[:len(current)-1], ""))
					if prefix != "" {
						lines = append(lines, prefix)
					}
					builder.Reset()
					builder.WriteString(last)
					builder.WriteString(token)
					cells = stringCellWidth(last) + tokenWidth
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
			for _, unit := range textUnits(token) {
				unitWidth := stringCellWidth(unit)
				if cells > 0 && cells+unitWidth > width {
					lines = append(lines, strings.TrimSpace(builder.String()))
					builder.Reset()
					cells = 0
				}
				builder.WriteString(unit)
				cells += unitWidth
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
	for _, unit := range textUnits(text) {
		runes := []rune(unit)
		r := runes[0]
		switch {
		case len(runes) > 1:
			flushASCII()
			tokens = append(tokens, unit)
		case unicode.IsSpace(r):
			flushASCII()
			if len(tokens) == 0 || tokens[len(tokens)-1] != " " {
				tokens = append(tokens, " ")
			}
		case r >= 0x21 && r <= 0x7e:
			ascii.WriteRune(r)
		default:
			flushASCII()
			tokens = append(tokens, unit)
		}
	}
	flushASCII()
	return tokens
}

func textUnits(text string) []string {
	graphemes := uniseg.NewGraphemes(text)
	units := make([]string, 0, len([]rune(text)))
	for graphemes.Next() {
		units = append(units, graphemes.Str())
	}
	return units
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
	for _, unit := range textUnits(text) {
		unitWidth := stringCellWidth(unit)
		if cells+unitWidth > width-1 {
			break
		}
		builder.WriteString(unit)
		cells += unitWidth
	}
	return builder.String() + "…"
}

func stringCellWidth(text string) int {
	return uniseg.StringWidth(text)
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

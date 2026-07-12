package feedtui

import (
	"fmt"
	"strings"
	"time"
)

const (
	ansiReset         = "\033[0m"
	ansiBold          = "\033[1m"
	ansiDim           = "\033[2m"
	ansiCyan          = "\033[36m"
	ansiBlue          = "\033[38;5;75m"
	ansiGreen         = "\033[38;5;114m"
	ansiRed           = "\033[38;5;203m"
	ansiCode          = "\033[38;5;245m"
	minReadingWidth   = 96
	maxReadingWidth   = 112
	paragraphGapLines = 2
)

type styledLine struct {
	text        string
	style       string
	middle      string
	middleStyle string
	tail        string
	tailStyle   string
	padding     int
	suffix      string
	suffixStyle string
	hasCursor   bool
	cursorCell  int
	raw         bool
	commentID   string
}

type layoutMetrics struct {
	bodyHeight int
	bodyLines  int
	maxScroll  int
	commentIDs []string
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
	if action != "" {
		lines = append(lines, line(truncateCells(action, contentWidth), ansiDim))
	}

	title := item.title
	if len(item.foldedItems) > 0 {
		if item.groupOpen {
			title = "▾ " + title
		} else {
			title = "▸ " + title
		}
	}
	if item.kind != "pin" || item.body == "" {
		titleLines := wrapText(title, contentWidth)
		if len(titleLines) > 3 {
			titleLines = titleLines[:3]
			titleLines[2] = truncateCells(strings.TrimSuffix(titleLines[2], "…")+"…", contentWidth)
		}
		titleStyle := ansiBold + ansiBlue
		if len(item.foldedItems) > 0 {
			titleStyle = ansiDim
		}
		for _, titleLine := range titleLines {
			lines = append(lines, line(titleLine, titleStyle))
		}
	}

	authorLine := item.author
	if item.headline == "" && strings.HasPrefix(item.action, item.author+" ") {
		authorLine = ""
	}
	if item.headline != "" {
		authorLine += "  ·  " + item.headline
	}
	if authorLine != "" {
		lines = append(lines, line(truncateCells(authorLine, contentWidth), ansiDim))
	}
	meta := item.stats
	if model.commentMode {
		meta = withoutCommentStat(meta)
	}
	if item.voted {
		if meta == "" {
			meta = "✓ 已赞同"
		} else {
			meta = "✓ 已赞同  ·  " + meta
		}
	}
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

	if body == "" && len(item.foldedItems) == 0 && item.kind != "pin" && !model.commentMode {
		body = "这条动态没有正文摘要；按 o 在知乎中查看完整内容。"
	}
	bodyLines := layoutBodyLines(body, contentWidth)
	if len(item.foldedItems) > 0 && !model.commentMode {
		bodyLines = layoutFoldedGroupPreview(item.foldedItems, contentWidth)
	}
	if model.composing {
		bodyLines = insertCommentComposer(bodyLines, model, contentWidth)
	}
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
			if model.pageAnchorVisible && row == model.pageAnchorLine {
				anchorText := styledLineText(bodyLine)
				if strings.TrimSpace(anchorText) == "" {
					anchorText = strings.Repeat("┄", contentWidth)
				}
				lines = append(lines, styledLine{
					text:  strings.Repeat(" ", maxInt(0, left-2)) + "▸ " + anchorText,
					style: ansiBlue,
				})
				continue
			}
			body := bodyLine
			body.text = strings.Repeat(" ", left) + bodyLine.text
			if body.hasCursor {
				body.cursorCell += left
			}
			lines = append(lines, body)
			continue
		}
		bar := "┊"
		if row >= thumbStart && row < thumbStart+thumbSize {
			bar = "┃"
		}
		body := bodyLine
		body.text = strings.Repeat(" ", left) + bodyLine.text
		if body.hasCursor {
			body.cursorCell += left
		}
		semanticWidth := stringCellWidth(body.text) + stringCellWidth(body.middle) + stringCellWidth(body.tail)
		body.padding = maxInt(0, left+contentWidth+1-semanticWidth)
		if model.pageAnchorVisible && model.scroll+row == model.pageAnchorLine {
			anchorText := styledLineText(bodyLine)
			if strings.TrimSpace(anchorText) == "" {
				anchorText = strings.Repeat("┄", contentWidth)
			}
			body.text = strings.Repeat(" ", maxInt(0, left-2)) + "▸ " + padCells(anchorText, contentWidth) + " "
			body.style = ansiBlue
			body.middle = ""
			body.tail = ""
			body.padding = 0
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
		state := model.currentCommentState()
		if state != nil && state.loaded && !state.loading && state.err == nil && len(state.items) == 0 {
			statusParts = append(statusParts, "暂无评论")
		}
	}
	if len(bodyLines) > bodyHeight {
		statusParts = append(statusParts, fmt.Sprintf("%s %d–%d / %d 行", model.readingAreaLabel(), model.scroll+1, end, len(bodyLines)))
	}
	if model.voting {
		statusParts = append(statusParts, spinnerFrames[model.spinner%len(spinnerFrames)]+" "+model.message)
	} else if model.loading {
		loadingText := "正在预取后续动态"
		if model.refreshing {
			loadingText = "正在刷新关注流"
		}
		statusParts = append(statusParts, spinnerFrames[model.spinner%len(spinnerFrames)]+" "+loadingText)
	} else if model.end {
		statusParts = append(statusParts, "已到当前关注流末尾")
	}
	if model.message != "" && !model.voting {
		statusParts = append(statusParts, model.message)
	}
	status := strings.Join(statusParts, "  ·  ")
	lines = append(lines, line(truncateCells(status, contentWidth), ansiDim))
	hints := footerHints(model, contentWidth)
	lines = append(lines, line(truncateCells(hints, contentWidth), ansiCyan))
	lines = append(lines, styledLine{})

	return fitHeight(lines, model.height), layoutMetrics{
		bodyHeight: bodyHeight,
		bodyLines:  len(bodyLines),
		maxScroll:  maxScroll,
		commentIDs: commentLineIDs(bodyLines),
	}
}

func footerHints(model *app, width int) string {
	full := "j/k 滚动  f/b 翻页  d/u 半页  n/p 切换  v 赞同  w 评论  c 评论  z 专注  o 打开  r 刷新  ? 帮助  q 退出"
	compact := "j/k 滚动  f/b 页  d/u 半页  n/p 切换  c 评论  r 刷新  ? 帮助  q 退出"
	switch {
	case model.composing:
		full = "←/→ · C-b/C-f 移动  Home/End · C-a/C-e 首尾  Backspace/Delete · C-d 删除  Enter 发送  Esc 取消"
		compact = "←/→ 移动  Home/End 首尾  BS/Del 删除  Enter 发送  Esc 取消"
	case model.commentMode:
		full = "j/k 选评论  f/b 翻页  d/u 半页  v 赞同  e 展开  w 回复  c 正文  ? 帮助  q 退出"
		compact = "j/k 选择  f/b 页  d/u 半页  e 展开  c 正文  ? 帮助  q 退出"
	case model.zenMode:
		full = "j/k 滚动  f/b 翻页  d/u 半页  n/p 切换  v 赞同  w 评论  c 评论  z 双栏  o 打开  r 刷新  ? 帮助  q 退出"
	}
	if stringCellWidth(full) <= width {
		return full
	}
	return compact
}

func styledLineText(line styledLine) string {
	return line.text + line.middle + line.tail
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

func layoutBodyLines(body string, width int) []styledLine {
	var result []styledLine
	var prose []string
	currentCommentID := ""
	appendParagraphGap := func() {
		blankLines := 0
		for index := len(result) - 1; index >= 0 && result[index].text == ""; index-- {
			blankLines++
		}
		for blankLines < paragraphGapLines {
			result = append(result, styledLine{})
			blankLines++
		}
	}
	flushProse := func() {
		if len(prose) == 0 {
			return
		}
		for _, text := range addParagraphSpacing(wrapText(strings.Join(prose, "\n"), width)) {
			result = append(result, styledLine{text: text, commentID: currentCommentID})
		}
		prose = prose[:0]
	}

	inCodeBlock := false
	for _, sourceLine := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(sourceLine, commentStartMarker) && strings.HasSuffix(sourceLine, commentMarkerEnd) {
			flushProse()
			currentCommentID = strings.TrimSuffix(strings.TrimPrefix(sourceLine, commentStartMarker), commentMarkerEnd)
			continue
		}
		if strings.HasPrefix(sourceLine, commentTreeMarker) {
			flushProse()
			marked := strings.TrimPrefix(sourceLine, commentTreeMarker)
			prefix, text, found := strings.Cut(marked, commentMarkerEnd)
			if found {
				lineWidth := maxInt(1, width-stringCellWidth(prefix))
				for _, wrapped := range wrapText(text, lineWidth) {
					result = append(result, styledLine{
						text:      prefix,
						style:     ansiDim,
						middle:    wrapped,
						commentID: currentCommentID,
					})
				}
				continue
			}
		}
		switch sourceLine {
		case codeBlockStartMarker:
			flushProse()
			if len(result) > 0 {
				appendParagraphGap()
			}
			result = append(result, styledLine{text: "┌─ 代码", style: ansiCode, commentID: currentCommentID})
			inCodeBlock = true
		case codeBlockEndMarker:
			result = append(result, styledLine{text: "└─", style: ansiCode, commentID: currentCommentID})
			appendParagraphGap()
			inCodeBlock = false
		default:
			if !inCodeBlock {
				prose = append(prose, sourceLine)
				continue
			}
			for _, codeLine := range wrapCellsPreserve(sourceLine, maxInt(1, width-2)) {
				result = append(result, styledLine{text: "│ " + codeLine, style: ansiCode, commentID: currentCommentID})
			}
		}
	}
	flushProse()
	for len(result) > 0 && result[len(result)-1].text == "" {
		result = result[:len(result)-1]
	}
	if len(result) == 0 {
		return []styledLine{{}}
	}
	return result
}

func insertCommentComposer(bodyLines []styledLine, model *app, width int) []styledLine {
	if len(model.composeTargets) == 0 {
		return bodyLines
	}
	target := model.composeTargets[0]
	insertAt := len(bodyLines)
	if target.commentID != "" && model.composeInsertLine >= 0 {
		insertAt = minInt(len(bodyLines), model.composeInsertLine+1)
		for insertAt > 0 && strings.TrimSpace(styledLineText(bodyLines[insertAt-1])) == "" {
			insertAt--
		}
	}
	composer := inlineCommentComposerLines(model, target, width)
	result := make([]styledLine, 0, len(bodyLines)+len(composer)+1)
	result = append(result, bodyLines[:insertAt]...)
	result = append(result, composer...)
	result = append(result, styledLine{commentID: target.commentID})
	result = append(result, bodyLines[insertAt:]...)
	return result
}

func inlineCommentComposerLines(model *app, target commentComposeTarget, width int) []styledLine {
	indent := strings.Repeat(" ", target.indent)
	innerWidth := maxInt(1, width-target.indent-4)
	lines := []styledLine{{
		text:        indent + "╭─ ",
		style:       ansiDim,
		middle:      truncateCells(target.label, maxInt(1, width-3)),
		middleStyle: ansiDim,
		commentID:   target.commentID,
	}}
	inputLines, cursorLine, cursorCell := wrapComposerInput(model.composeInput, model.composeCursor, innerWidth)
	for index, inputLine := range inputLines {
		line := styledLine{
			text:      indent + "│  ",
			style:     ansiDim,
			middle:    inputLine,
			commentID: target.commentID,
		}
		if index == cursorLine {
			line.hasCursor = true
			line.cursorCell = stringCellWidth(line.text) + cursorCell
		}
		lines = append(lines, line)
	}
	if model.composeError != "" {
		lines = append(lines, styledLine{
			text:        indent + "│  ",
			style:       ansiDim,
			middle:      truncateCells(model.composeError, maxInt(1, width-3)),
			middleStyle: ansiRed,
			commentID:   target.commentID,
		})
	} else if model.commentSubmitting {
		lines = append(lines, styledLine{
			text:        indent + "│  ",
			style:       ansiDim,
			middle:      spinnerFrames[model.spinner%len(spinnerFrames)] + " 正在发送",
			middleStyle: ansiDim,
			commentID:   target.commentID,
		})
	}
	lines = append(lines, styledLine{text: indent + "╰─", style: ansiDim, commentID: target.commentID})
	return lines
}

func wrapComposerInput(value string, cursor, width int) ([]string, int, int) {
	width = maxInt(1, width)
	units := textUnits(value)
	cursor = minInt(maxInt(0, cursor), len(units))
	lines := []string{""}
	line, cells := 0, 0
	cursorLine, cursorCell := 0, 0
	for index, unit := range units {
		unitWidth := stringCellWidth(unit)
		if cells > 0 && cells+unitWidth > width {
			lines = append(lines, "")
			line++
			cells = 0
			if cursor == index {
				cursorLine, cursorCell = line, 0
			}
		}
		lines[line] += unit
		cells += unitWidth
		if cursor == index+1 {
			cursorLine, cursorCell = line, cells
		}
	}
	if cursor == len(units) && cells >= width {
		lines = append(lines, "")
		cursorLine, cursorCell = line+1, 0
	}
	return lines, cursorLine, cursorCell
}

func commentLineIDs(lines []styledLine) []string {
	ids := make([]string, len(lines))
	for index := range lines {
		ids[index] = lines[index].commentID
	}
	return ids
}

func layoutFoldedGroupPreview(children []feedItem, width int) []styledLine {
	lines := make([]styledLine, 0, len(children)*5)
	for index, child := range children {
		if index > 0 {
			for range paragraphGapLines {
				lines = append(lines, styledLine{})
			}
		}
		for _, titleLine := range wrapText(child.title, width) {
			lines = append(lines, styledLine{text: titleLine, style: ansiBlue})
		}
		meta := foldedItemEventLabel(child)
		lines = append(lines, styledLine{text: truncateCells(meta, width), style: ansiDim})

		excerpt, hasMore := foldedItemExcerpt(child)
		if excerpt == "" {
			continue
		}
		excerptWidth := width
		excerptLines := wrapText(excerpt, excerptWidth)
		truncated := len(excerptLines) > 2
		if len(excerptLines) > 2 {
			excerptLines = excerptLines[:2]
		}
		if truncated || hasMore {
			last := len(excerptLines) - 1
			excerptLines[last] = truncateCells(strings.TrimSuffix(excerptLines[last], "…")+"…", excerptWidth)
		}
		for _, excerptLine := range excerptLines {
			lines = append(lines, styledLine{text: excerptLine})
		}
	}
	return lines
}

func foldedItemEventLabel(item feedItem) string {
	author := firstNonEmpty(item.author, "匿名用户")
	if item.kind == "answer" {
		for _, verb := range []string{"赞同了回答", "收藏了回答"} {
			actor := strings.TrimSpace(strings.TrimSuffix(item.action, verb))
			if actor != "" && actor != item.action {
				return actor + " " + strings.TrimSuffix(verb, "回答") + " " + author + " 的回答"
			}
		}
	}
	if item.kind == "question" {
		const verb = "关注了问题"
		actor := strings.TrimSpace(strings.TrimSuffix(item.action, verb))
		if actor != "" && actor != item.action {
			return actor + " 关注了 " + author + " 的问题"
		}
	}
	return foldedItemAuthorLabel(item) + " · " + item.action
}

func foldedItemAuthorLabel(item feedItem) string {
	author := firstNonEmpty(item.author, "匿名用户")
	switch item.kind {
	case "answer":
		return "答主 " + author
	case "question":
		return "提问者 " + author
	case "article":
		return "作者 " + author
	case "pin":
		return "想法作者 " + author
	default:
		return author
	}
}

func foldedItemExcerpt(item feedItem) (text string, hasMore bool) {
	meaningful := make([]string, 0, 2)
	for _, sourceLine := range strings.Split(item.body, "\n") {
		text := compactLine(sourceLine)
		if text == "" || text == codeBlockStartMarker || text == codeBlockEndMarker {
			continue
		}
		meaningful = append(meaningful, text)
	}
	if item.kind == "pin" && len(meaningful) > 0 && meaningful[0] == compactLine(item.title) {
		meaningful = meaningful[1:]
	}
	if len(meaningful) == 0 {
		if item.kind == "pin" {
			return "", false
		}
		return "暂无正文摘要", false
	}
	return meaningful[0], len(meaningful) > 1
}

func wrapCellsPreserve(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	var lines []string
	var builder strings.Builder
	cells := 0
	for _, unit := range textUnits(text) {
		unitWidth := stringCellWidth(unit)
		if cells > 0 && cells+unitWidth > width {
			lines = append(lines, builder.String())
			builder.Reset()
			cells = 0
		}
		builder.WriteString(unit)
		cells += unitWidth
	}
	if builder.Len() > 0 {
		lines = append(lines, builder.String())
	}
	return lines
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
		hierarchyIndent := ""
		if item.foldedParent != "" {
			hierarchyIndent = "    "
		}
		marker := "  "
		style := ""
		summaryPrefix := "  " + hierarchyIndent
		summaryStyle := ansiDim
		isNew, isReadTop, isReadBottom := sidebarItemState(model, item)
		isReadBoundary := isReadTop || isReadBottom
		if isReadTop && isReadBottom {
			style = ansiCyan
			summaryPrefix = "  " + hierarchyIndent + "上次读到↓↑ · "
			summaryStyle = ansiCyan
		} else if isReadTop {
			style = ansiCyan
			summaryPrefix = "  " + hierarchyIndent + "上次读到↓ · "
			summaryStyle = ansiCyan
		} else if isReadBottom {
			style = ansiCyan
			summaryPrefix = "  " + hierarchyIndent + "上次读到↑ · "
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
		titlePrefix := ""
		if len(item.foldedItems) > 0 {
			if item.groupOpen {
				titlePrefix = "▾ "
			} else {
				titlePrefix = "▸ "
			}
		} else if item.foldedParent != "" {
			titlePrefix = hierarchyIndent
		}
		titleWidth := maxInt(1, width-stringCellWidth(marker)-1)
		title := truncateCells(titlePrefix+item.title, titleWidth)
		lines[row] = styledLine{text: marker + title, style: style}
		summary := firstNonEmpty(item.action, item.author, typeLabel(item.kind))
		if len(item.foldedItems) > 0 {
			if item.groupOpen {
				summary = "e/Enter 收起"
			} else {
				summary = "e/Enter 展开"
			}
		}
		lines[row+1] = styledLine{text: summaryPrefix + truncateCells(summary, maxInt(1, width-stringCellWidth(summaryPrefix)-1)), style: summaryStyle}
		row += 3
	}
	return lines
}

func sidebarItemState(model *app, item feedItem) (isNew, isReadTop, isReadBottom bool) {
	_, isNew = model.newItemKeys[item.key]
	isReadTop = item.key == model.lastReadTopKey
	isReadBottom = item.key == model.lastReadBottomKey
	if item.groupOpen {
		return isNew, isReadTop, isReadBottom
	}
	for _, child := range item.foldedItems {
		childNew, childReadTop, childReadBottom := sidebarItemState(model, child)
		isNew = isNew || childNew
		isReadTop = isReadTop || childReadTop
		isReadBottom = isReadBottom || childReadBottom
	}
	return isNew, isReadTop, isReadBottom
}

func mergeColumns(left styledLine, leftWidth int, right styledLine, rightWidth int) styledLine {
	leftText, leftTextWidth := renderStyledLine(left, leftWidth)
	leftPadding := strings.Repeat(" ", maxInt(0, leftWidth-leftTextWidth))
	rightText, _ := renderStyledLine(right, maxInt(1, rightWidth-1))
	text := leftText + leftPadding + styleText(" │ ", ansiDim) + rightText
	line := styledLine{text: text, raw: true}
	if right.hasCursor {
		line.hasCursor = true
		line.cursorCell = leftWidth + 3 + right.cursorCell
	} else if left.hasCursor {
		line.hasCursor = true
		line.cursorCell = left.cursorCell
	}
	return line
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
	entries := []struct {
		keys        string
		description string
	}{
		{"j/k · ↓/↑", "正文滚动；评论区逐条移动蓝色焦点"},
		{"f · space · Ctrl-F", "向下翻页；正文到底后再按一次切换下一条"},
		{"b · Ctrl-B", "向上翻页；正文到顶后再按一次切换上一条"},
		{"d / u", "向下 / 向上半页，保留蓝色续读焦点"},
		{"Ctrl-E / Ctrl-Y", "向下 / 向上滚动一行"},
		{"n/p · h/l · ←/→", "下一条 / 上一条"},
		{"g / G", "第一条 / 最后一条已加载动态"},
		{"v", "赞同回答或蓝色焦点评论 / 取消赞同"},
		{"w", "写评论 / 回复蓝色焦点所在评论"},
		{"c", "加载评论 / 返回正文"},
		{"e / Enter", "展开 / 收起蓝色焦点评论的回复或知乎聚合动态"},
		{"z", "专注阅读 / 恢复双栏"},
		{"o", "用默认浏览器打开当前动态"},
		{"r", "刷新；新标题变绿 / 标记进程阅读范围"},
		{"q / Ctrl-C", "退出并恢复终端"},
	}
	keyWidth := 0
	for _, entry := range entries {
		keyWidth = maxInt(keyWidth, stringCellWidth(entry.keys))
	}
	lines := []styledLine{
		{text: pad + "快捷键", style: ansiBold + ansiCyan},
		{},
	}
	for _, entry := range entries {
		lines = append(lines, styledLine{text: pad + padCells(entry.keys, keyWidth) + "  " + entry.description})
	}
	lines = append(lines, styledLine{}, styledLine{text: pad + "按 ? 返回阅读。", style: ansiCyan})
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

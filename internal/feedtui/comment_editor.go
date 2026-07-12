package feedtui

import (
	"context"
	"strings"
	"time"
)

func (model *app) startCommentComposer() {
	if len(model.items) == 0 {
		return
	}
	item := model.items[model.index]
	if !supportsComments(item) {
		model.setMessage("当前动态类型不支持评论", 3*time.Second)
		return
	}
	target := commentComposeTarget{label: "评论当前" + typeLabel(item.kind)}
	model.composeInsertLine = -1
	if model.commentMode {
		focusLine, commentID := model.focusedComment()
		if commentID == "" {
			model.setMessage("先用 space/b 将蓝色焦点停在要回复的评论上", 4*time.Second)
			return
		}
		comment, found := findCommentByID(model.currentCommentState(), commentID)
		if !found {
			model.setMessage("蓝色焦点不在评论上", 3*time.Second)
			return
		}
		target = commentComposeTarget{
			commentID: comment.id,
			label:     "回复 " + comment.author,
			indent:    commentDepthByID(model.currentCommentState(), comment.id) * 3,
		}
		model.composeInsertLine = focusLine
		for model.composeInsertLine+1 < len(model.metrics.commentIDs) && model.metrics.commentIDs[model.composeInsertLine+1] == commentID {
			model.composeInsertLine++
		}
	}
	model.composeTargets = []commentComposeTarget{target}
	model.composeTarget = 0
	model.composeInput = ""
	model.composeCursor = 0
	model.composeError = ""
	model.composing = true
	model.commentSubmitting = false
	model.boundarySwitchKey = ""
	model.clearMessage()
	if target.commentID == "" {
		model.clearPageAnchor()
		model.scroll = model.metrics.maxScroll + 4
		return
	}
}

func (model *app) handleCommentComposerKey(ctx context.Context, key keyEvent) bool {
	if model.commentSubmitting {
		return false
	}
	switch key {
	case keyEscape, keyCtrlG:
		model.composing = false
		model.composeInput = ""
		model.composeCursor = 0
		model.composeError = ""
		model.setMessage("已取消写评论", 2*time.Second)
	case keyBackspace:
		model.composeInput, model.composeCursor = deleteTextUnitBefore(model.composeInput, model.composeCursor)
		model.composeError = ""
	case keyDelete, keyCtrlD:
		model.composeInput = deleteTextUnitAt(model.composeInput, model.composeCursor)
		model.composeError = ""
	case keyLeft, keyCtrlB:
		model.composeCursor = maxInt(0, model.composeCursor-1)
	case keyRight, keyCtrlF:
		model.composeCursor = minInt(len(textUnits(model.composeInput)), model.composeCursor+1)
	case keyHome, keyCtrlA:
		model.composeCursor = 0
	case keyEnd, keyCtrlE:
		model.composeCursor = len(textUnits(model.composeInput))
	case "\r":
		model.submitComment(ctx)
	default:
		if isPrintableKey(key) {
			model.composeInput, model.composeCursor = insertTextAt(model.composeInput, model.composeCursor, string(key))
			model.composeError = ""
		}
	}
	return false
}

func (model *app) focusedComment() (int, string) {
	if !model.pageAnchorVisible || len(model.metrics.commentIDs) == 0 {
		return -1, ""
	}
	line := minInt(maxInt(0, model.pageAnchorLine), len(model.metrics.commentIDs)-1)
	if model.metrics.commentIDs[line] != "" {
		return line, model.metrics.commentIDs[line]
	}
	for distance := 1; distance < len(model.metrics.commentIDs); distance++ {
		if before := line - distance; before >= 0 && model.metrics.commentIDs[before] != "" {
			return before, model.metrics.commentIDs[before]
		}
		if after := line + distance; after < len(model.metrics.commentIDs) && model.metrics.commentIDs[after] != "" {
			return after, model.metrics.commentIDs[after]
		}
	}
	return -1, ""
}

func (model *app) moveCommentFocus(ctx context.Context, direction int) {
	if !model.commentMode || len(model.metrics.commentIDs) == 0 {
		model.setMessage("当前不在评论区", 2*time.Second)
		return
	}
	positions := make([]commentFocusPosition, 0)
	seen := make(map[string]struct{})
	for line, commentID := range model.metrics.commentIDs {
		if commentID == "" {
			continue
		}
		if _, duplicate := seen[commentID]; duplicate {
			continue
		}
		seen[commentID] = struct{}{}
		positions = append(positions, commentFocusPosition{id: commentID, line: line})
	}
	if len(positions) == 0 {
		model.setMessage("评论仍在加载", 2*time.Second)
		return
	}

	current := -1
	_, currentID := model.focusedComment()
	for index := range positions {
		if positions[index].id == currentID {
			current = index
			break
		}
	}
	if current < 0 {
		if direction > 0 {
			current = firstCommentAtOrAfter(positions, model.scroll) - 1
		} else {
			current = lastCommentAtOrBefore(positions, model.scroll+model.metrics.bodyHeight-1) + 1
		}
	}
	next := current + direction
	if next < 0 {
		model.setMessage("已经是第一条评论", 2*time.Second)
		return
	}
	if next >= len(positions) {
		state := model.currentCommentState()
		if state != nil && !state.end {
			if !state.loading {
				model.startCommentPage(ctx, model.items[model.index], state, true)
			}
			model.setMessage("正在加载更多评论", 2*time.Second)
			return
		}
		model.setMessage("已经是最后一条评论", 2*time.Second)
		return
	}

	model.boundarySwitchKey = ""
	model.clearMessage()
	model.setPageAnchor(positions[next].line)
	if positions[next].line < model.scroll {
		model.scroll = positions[next].line
	} else if positions[next].line >= model.scroll+model.metrics.bodyHeight {
		model.scroll = maxInt(0, positions[next].line-model.metrics.bodyHeight+1)
	}
}

func firstCommentAtOrAfter(positions []commentFocusPosition, line int) int {
	for index := range positions {
		if positions[index].line >= line {
			return index
		}
	}
	return len(positions) - 1
}

func lastCommentAtOrBefore(positions []commentFocusPosition, line int) int {
	for index := len(positions) - 1; index >= 0; index-- {
		if positions[index].line <= line {
			return index
		}
	}
	return 0
}

func (model *app) submitComment(ctx context.Context) {
	content := strings.TrimSpace(model.composeInput)
	if content == "" {
		model.composeError = "评论内容不能为空"
		return
	}
	if len(model.items) == 0 || len(model.composeTargets) == 0 {
		return
	}
	item := model.items[model.index]
	target := model.composeTargets[model.composeTarget]
	model.commentSubmitting = true
	model.composeError = ""
	model.spinner = 0
	go func() {
		var response map[string]any
		var err error
		if target.commentID == "" {
			response, err = model.source.CreateComment(ctx, item.kind, item.id, content)
		} else {
			response, err = model.source.ReplyCommentToResource(ctx, item.kind, item.id, target.commentID, content)
		}
		select {
		case model.commentPosts <- commentPostResult{
			itemKey:  item.key,
			targetID: target.commentID,
			content:  content,
			response: response,
			reply:    target.commentID != "",
			err:      err,
		}:
		case <-ctx.Done():
		}
	}()
}

func (model *app) applyCommentPost(ctx context.Context, result commentPostResult) {
	model.commentSubmitting = false
	if result.err != nil {
		model.composeError = "发送失败：" + result.err.Error()
		return
	}
	model.composing = false
	model.composeInput = ""
	model.composeCursor = 0
	model.composeError = ""
	if len(model.items) > 0 {
		current := model.items[model.index]
		incrementCommentCount(model.items, current.kind, current.id)
	}
	state := model.comments[result.itemKey]
	posted := parsePostedComment(result.response, result.content, result.targetID)
	inserted := false
	if state != nil && state.loaded && posted.id != "" {
		if result.reply {
			var rootID string
			rootID, inserted = appendReplyToComment(state.items, result.targetID, posted)
			if inserted {
				if state.expandedChildren == nil {
					state.expandedChildren = map[string]bool{}
				}
				state.expandedChildren[rootID] = true
			}
		} else {
			state.items = append([]feedComment{posted}, state.items...)
			inserted = true
		}
	}
	if len(model.items) > 0 && model.items[model.index].key == result.itemKey {
		model.commentMode = true
		if inserted && !result.reply {
			model.scroll = 0
			model.setPageAnchor(0)
		}
		if !inserted && (state == nil || !state.loaded) {
			model.scroll = 0
			model.startComments(ctx, model.items[model.index])
		}
	}
	if result.reply {
		model.setMessage("回复已发布", 2*time.Second)
	} else {
		model.setMessage("评论已发布", 2*time.Second)
	}
}

func parsePostedComment(response map[string]any, content, targetID string) feedComment {
	raw := response
	if data := mapValue(response["data"]); len(data) > 0 {
		raw = data
	}
	comment := parseComment(raw)
	if strings.TrimSpace(toString(raw["content"])) == "" {
		comment.content = content
	}
	if len(mapValue(raw["author"])) == 0 && len(mapValue(raw["user"])) == 0 {
		comment.author = "我"
	}
	comment.parentID = targetID
	if comment.createdAt == 0 {
		comment.createdAt = time.Now().Unix()
	}
	return comment
}

func appendReplyToComment(roots []feedComment, targetID string, reply feedComment) (string, bool) {
	for index := range roots {
		root := &roots[index]
		if root.id == targetID {
			reply.parentID = targetID
			root.children = append([]feedComment{reply}, root.children...)
			root.childComments++
			return root.id, true
		}
		if appendNestedReply(root.children, targetID, reply) {
			root.childComments++
			return root.id, true
		}
	}
	return "", false
}

func appendNestedReply(comments []feedComment, targetID string, reply feedComment) bool {
	for index := range comments {
		comment := &comments[index]
		if comment.id == targetID {
			reply.parentID = targetID
			reply.nestedReply = true
			comment.children = append([]feedComment{reply}, comment.children...)
			comment.childComments++
			return true
		}
		if appendNestedReply(comment.children, targetID, reply) {
			comment.childComments++
			return true
		}
	}
	return false
}

func incrementCommentCount(items []feedItem, kind, id string) {
	for index := range items {
		item := &items[index]
		if item.kind == kind && item.id == id {
			item.commentCount++
			item.hasCommentCount = true
			item.stats = replaceCommentStat(item.stats, item.commentCount)
		}
		incrementCommentCount(item.foldedItems, kind, id)
	}
}

func dropLastTextUnit(value string) string {
	units := textUnits(value)
	if len(units) == 0 {
		return ""
	}
	return strings.Join(units[:len(units)-1], "")
}

func insertTextAt(value string, cursor int, inserted string) (string, int) {
	units := textUnits(value)
	cursor = minInt(maxInt(0, cursor), len(units))
	prefix := strings.Join(units[:cursor], "") + inserted
	result := prefix + strings.Join(units[cursor:], "")
	return result, len(textUnits(prefix))
}

func deleteTextUnitBefore(value string, cursor int) (string, int) {
	units := textUnits(value)
	cursor = minInt(maxInt(0, cursor), len(units))
	if cursor == 0 {
		return value, cursor
	}
	return strings.Join(append(units[:cursor-1], units[cursor:]...), ""), cursor - 1
}

func deleteTextUnitAt(value string, cursor int) string {
	units := textUnits(value)
	cursor = minInt(maxInt(0, cursor), len(units))
	if cursor == len(units) {
		return value
	}
	return strings.Join(append(units[:cursor], units[cursor+1:]...), "")
}

func isPrintableKey(key keyEvent) bool {
	runes := []rune(string(key))
	return len(runes) == 1 && runes[0] >= ' '
}

package feedtui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func collapsedFeedItems(items []feedItem) []feedItem {
	result := make([]feedItem, 0, len(items))
	for _, item := range items {
		if item.foldedParent != "" {
			continue
		}
		item.groupOpen = false
		result = append(result, item)
	}
	return result
}

func collectFeedItemKeys(item feedItem, keys map[string]struct{}) {
	if len(item.foldedItems) == 0 {
		keys[item.key] = struct{}{}
		return
	}
	for _, child := range item.foldedItems {
		collectFeedItemKeys(child, keys)
	}
}

func takeUnrepresentedFeedLeaves(item feedItem, represented map[string]struct{}) (feedItem, int, bool) {
	if len(item.foldedItems) == 0 {
		if _, exists := represented[item.key]; exists {
			return feedItem{}, 0, false
		}
		represented[item.key] = struct{}{}
		return item, 1, true
	}

	children := make([]feedItem, 0, len(item.foldedItems))
	leaves := 0
	for _, child := range item.foldedItems {
		child, childLeaves, kept := takeUnrepresentedFeedLeaves(child, represented)
		if !kept {
			continue
		}
		child.foldedParent = item.key
		children = append(children, child)
		leaves += childLeaves
	}
	if len(children) == 0 {
		return feedItem{}, 0, false
	}
	item.foldedItems = children
	item.groupOpen = false
	item.title = updateFoldedGroupCount(item.title, len(children))
	return item, leaves, true
}

func appendOrMergeFeedGroup(items []feedItem, item feedItem) []feedItem {
	if len(item.foldedItems) == 0 {
		return append(items, item)
	}
	for index := range items {
		if items[index].key != item.key || len(items[index].foldedItems) == 0 {
			continue
		}
		items[index].foldedItems = append(items[index].foldedItems, item.foldedItems...)
		items[index].title = updateFoldedGroupCount(items[index].title, len(items[index].foldedItems))
		return items
	}
	return append(items, item)
}

func updateFoldedGroupCount(title string, count int) string {
	return foldedGroupCountPattern.ReplaceAllStringFunc(title, func(match string) string {
		return strings.TrimRight(match, "0123456789") + strconv.Itoa(count)
	})
}

func countUnseenFeedItemKeys(item feedItem, previous map[string]struct{}) int {
	if len(item.foldedItems) == 0 {
		if _, existed := previous[item.key]; existed {
			return 0
		}
		return 1
	}
	count := 0
	for _, child := range item.foldedItems {
		count += countUnseenFeedItemKeys(child, previous)
	}
	return count
}

func markUnseenFeedItemKeys(item feedItem, previous, unseen map[string]struct{}) {
	if len(item.foldedItems) == 0 {
		if _, existed := previous[item.key]; !existed {
			unseen[item.key] = struct{}{}
		}
	}
	for _, child := range item.foldedItems {
		markUnseenFeedItemKeys(child, previous, unseen)
	}
}

func (model *app) toggleFoldedGroup() bool {
	if len(model.items) == 0 {
		return false
	}
	groupIndex := model.index
	if parentKey := model.items[groupIndex].foldedParent; parentKey != "" {
		for groupIndex >= 0 && model.items[groupIndex].key != parentKey {
			groupIndex--
		}
		if groupIndex < 0 {
			return false
		}
	}
	group := model.items[groupIndex]
	if len(group.foldedItems) == 0 {
		return false
	}
	model.clearPageAnchor()
	model.clearBoundarySwitch()
	model.commentMode = false
	model.bodyScroll = 0
	model.scroll = 0
	model.index = groupIndex
	if group.groupOpen {
		end := groupIndex + 1
		for end < len(model.items) && model.items[end].foldedParent == group.key {
			end++
		}
		model.items = append(model.items[:groupIndex+1], model.items[end:]...)
		model.items[groupIndex].groupOpen = false
		model.setMessage(fmt.Sprintf("已收起 %d 条动态", len(group.foldedItems)), 2*time.Second)
		return true
	}

	existing := make(map[string]struct{}, len(model.items))
	for _, item := range model.items {
		existing[item.key] = struct{}{}
	}
	children := make([]feedItem, 0, len(group.foldedItems))
	for _, child := range group.foldedItems {
		if _, duplicate := existing[child.key]; duplicate {
			continue
		}
		children = append(children, child)
	}
	model.items[groupIndex].groupOpen = true
	tail := append([]feedItem(nil), model.items[groupIndex+1:]...)
	model.items = append(model.items[:groupIndex+1], children...)
	model.items = append(model.items, tail...)
	model.setMessage(fmt.Sprintf("已展开 %d 条动态", len(children)), 2*time.Second)
	return true
}

func (model *app) captureRefreshBoundary() {
	model.pendingReadTopKey = ""
	model.pendingReadBottomKey = ""
	model.pendingRefreshTopKey = ""
	if len(model.items) == 0 {
		return
	}
	model.pendingReadTopKey = model.firstViewedKey
	model.pendingReadBottomKey = model.furthestViewedKey
	model.pendingRefreshTopKey = model.items[0].key
}

func (model *app) lineDown() {
	model.clearPageAnchor()
	if model.scroll < model.metrics.maxScroll {
		model.scroll++
		model.clearMessage()
		return
	}
	model.setMessage("已到"+model.readingAreaLabel()+"底部", 2*time.Second)
}

func (model *app) lineUp() {
	model.clearPageAnchor()
	if model.scroll > 0 {
		model.scroll--
		model.clearMessage()
		return
	}
	model.setMessage("已到"+model.readingAreaLabel()+"顶部", 2*time.Second)
}

func (model *app) pageDownWithConfirmation(ctx context.Context, amount int) {
	model.pageDownWithBoundary(ctx, amount, " ", "space")
}

func (model *app) pageDownWithBoundary(ctx context.Context, amount int, key keyEvent, keyLabel string) {
	if model.scroll < model.metrics.maxScroll {
		previousLastLine := minInt(model.metrics.bodyLines-1, model.scroll+model.metrics.bodyHeight-1)
		model.clearBoundarySwitch()
		model.scroll = minInt(model.metrics.maxScroll, model.scroll+amount)
		model.setPageAnchor(previousLastLine)
		model.clearMessage()
		return
	}
	if model.ensureMoreComments(ctx) {
		model.setPageAnchor(model.metrics.bodyLines - 1)
		return
	}
	if model.commentMode {
		model.clearBoundarySwitch()
		model.setPageAnchor(model.metrics.bodyLines - 1)
		if model.currentCommentsLoading() {
			model.setMessage("正在加载更多评论", 2*time.Second)
		} else {
			model.setMessage("已到评论底部", 2*time.Second)
		}
		return
	}
	if model.consumeBoundarySwitch(key) {
		model.moveNext(ctx)
		return
	}
	model.setPageAnchor(model.metrics.bodyLines - 1)
	model.armBoundarySwitch(key, "已到"+model.readingAreaLabel()+"底部，再按一次 "+keyLabel+" 切换下一条")
}

func (model *app) pageUpWithConfirmation(amount int) {
	model.pageUpWithBoundary(amount, "b", "b")
}

func (model *app) pageUpWithBoundary(amount int, key keyEvent, keyLabel string) {
	if model.scroll > 0 {
		previousFirstLine := model.scroll
		model.clearBoundarySwitch()
		model.scroll = maxInt(0, model.scroll-amount)
		model.setPageAnchor(previousFirstLine)
		model.clearMessage()
		return
	}
	if model.commentMode {
		model.clearBoundarySwitch()
		model.setPageAnchor(0)
		model.setMessage("已到评论顶部", 2*time.Second)
		return
	}
	if model.consumeBoundarySwitch(key) {
		model.movePrevious(true)
		return
	}
	model.setPageAnchor(0)
	model.armBoundarySwitch(key, "已到"+model.readingAreaLabel()+"顶部，再按一次 "+keyLabel+" 切换上一条")
}

func (model *app) scrollDown(amount int) {
	model.clearBoundarySwitch()
	if model.scroll < model.metrics.maxScroll {
		previousLastLine := minInt(model.metrics.bodyLines-1, model.scroll+model.metrics.bodyHeight-1)
		model.scroll = minInt(model.metrics.maxScroll, model.scroll+amount)
		model.setPageAnchor(previousLastLine)
		model.clearMessage()
		return
	}
	model.setPageAnchor(maxInt(0, model.metrics.bodyLines-1))
	model.setMessage("已到"+model.readingAreaLabel()+"底部", 2*time.Second)
}

func (model *app) scrollUp(amount int) {
	model.clearBoundarySwitch()
	if model.scroll > 0 {
		previousFirstLine := model.scroll
		model.scroll = maxInt(0, model.scroll-amount)
		model.setPageAnchor(previousFirstLine)
		model.clearMessage()
		return
	}
	model.setPageAnchor(0)
	model.setMessage("已到"+model.readingAreaLabel()+"顶部", 2*time.Second)
}

func (model *app) scrollViewportDown() {
	if model.scroll >= model.metrics.maxScroll {
		model.setMessage("已到"+model.readingAreaLabel()+"底部", 2*time.Second)
		return
	}
	model.scroll++
	if model.pageAnchorVisible && model.pageAnchorLine < model.scroll {
		model.pageAnchorLine = model.scroll
	}
	model.clearMessage()
}

func (model *app) scrollViewportUp() {
	if model.scroll <= 0 {
		model.setMessage("已到"+model.readingAreaLabel()+"顶部", 2*time.Second)
		return
	}
	model.scroll--
	bottom := model.scroll + model.metrics.bodyHeight - 1
	if model.pageAnchorVisible && model.pageAnchorLine > bottom {
		model.pageAnchorLine = bottom
	}
	model.clearMessage()
}

func (model *app) readingAreaLabel() string {
	if model.commentMode {
		return "评论"
	}
	return "正文"
}

func (model *app) armBoundarySwitch(key keyEvent, message string) {
	model.boundarySwitchKey = key
	model.setMessage(message, 4*time.Second)
}

func (model *app) consumeBoundarySwitch(key keyEvent) bool {
	confirmed := model.boundarySwitchKey == key
	model.clearBoundarySwitch()
	return confirmed
}

func (model *app) clearBoundarySwitch() {
	model.clearPageAnchor()
}

func (model *app) clearMessage() {
	model.message = ""
	model.messageUntil = time.Time{}
}

func (model *app) setPageAnchor(line int) {
	model.pageAnchorLine = line
	model.pageAnchorVisible = line >= 0
}

func (model *app) clearPageAnchor() {
	model.pageAnchorLine = 0
	model.pageAnchorVisible = false
	if model.boundarySwitchKey != "" {
		model.boundarySwitchKey = ""
		model.clearMessage()
	}
}

func (model *app) moveNext(ctx context.Context) {
	model.clearPageAnchor()
	if model.index+1 < len(model.items) {
		model.commentMode = false
		model.bodyScroll = 0
		model.index++
		model.scroll = 0
		model.message = ""
		return
	}
	if !model.end {
		model.startFetch(ctx, false)
		model.setMessage("正在加载后续动态", 2*time.Second)
		return
	}
	model.setMessage("已经是最后一条动态", 2*time.Second)
}

func (model *app) movePrevious(atEnd bool) {
	model.clearPageAnchor()
	if model.index == 0 || len(model.items) == 0 {
		model.setMessage("已经是第一条动态", 2*time.Second)
		return
	}
	model.commentMode = false
	model.bodyScroll = 0
	model.index--
	model.scroll = 0
	if atEnd {
		model.scroll = int(^uint(0) >> 1)
	}
	model.message = ""
}

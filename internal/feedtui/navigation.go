package feedtui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const pageContextLines = 3

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

func collectFeedLeavesByKey(items []feedItem) map[string]feedItem {
	leaves := make(map[string]feedItem)
	var collect func(feedItem)
	collect = func(item feedItem) {
		if len(item.foldedItems) == 0 {
			if latest, found := leaves[item.key]; found {
				mergeOlderVoteActors(&latest, item)
				leaves[item.key] = latest
			} else {
				leaves[item.key] = item
			}
			return
		}
		for _, child := range item.foldedItems {
			collect(child)
		}
	}
	for _, item := range items {
		collect(item)
	}
	return leaves
}

func updateFeedLeaves(items []feedItem, latest map[string]feedItem) []feedItem {
	result := make([]feedItem, len(items))
	for index, item := range items {
		result[index] = updateFeedItemLeaves(item, latest)
	}
	return result
}

func updateFeedItemLeaves(item feedItem, latest map[string]feedItem) feedItem {
	if len(item.foldedItems) == 0 {
		updated, found := latest[item.key]
		if !found {
			return item
		}
		mergeOlderVoteActors(&updated, item)
		updated.foldedParent = item.foldedParent
		updated.serverFolded = item.serverFolded
		return updated
	}

	children := make([]feedItem, len(item.foldedItems))
	for index, child := range item.foldedItems {
		children[index] = updateFeedItemLeaves(child, latest)
	}
	item.foldedItems = children
	return item
}

func mergeFeedVoteActors(items []feedItem, incoming feedItem) {
	if len(incoming.foldedItems) > 0 {
		for _, child := range incoming.foldedItems {
			mergeFeedVoteActors(items, child)
		}
		return
	}
	if existing := findFeedLeaf(items, incoming.key); existing != nil {
		mergeOlderVoteActors(existing, incoming)
	}
}

func findFeedLeaf(items []feedItem, key string) *feedItem {
	for index := range items {
		if len(items[index].foldedItems) == 0 {
			if items[index].key == key {
				return &items[index]
			}
			continue
		}
		if found := findFeedLeaf(items[index].foldedItems, key); found != nil {
			return found
		}
	}
	return nil
}

func mergeOlderVoteActors(newer *feedItem, older feedItem) {
	if newer.voteAction == "" || newer.voteAction != older.voteAction || len(older.voteActors) == 0 {
		return
	}
	combined := append(append([]string(nil), older.voteActors...), newer.voteActors...)
	seen := make(map[string]struct{}, len(combined))
	actors := make([]string, 0, len(combined))
	for index := len(combined) - 1; index >= 0; index-- {
		name := combined[index]
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		actors = append(actors, name)
	}
	for left, right := 0, len(actors)-1; left < right; left, right = left+1, right-1 {
		actors[left], actors[right] = actors[right], actors[left]
	}
	newer.voteActors = actors
	newer.action = actors[len(actors)-1] + " " + newer.voteAction
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
	if len(children) == 1 {
		child := children[0]
		child.foldedParent = item.foldedParent
		child.serverFolded = false
		return child, leaves, true
	}
	item.foldedItems = children
	item.groupOpen = false
	item.title = updateFoldedGroupCount(item.title, len(children))
	return item, leaves, true
}

func detachFoldedGroup(item feedItem) feedItem {
	if len(item.foldedItems) == 0 {
		return item
	}
	item.key = "folded:new:" + item.foldedItems[0].key
	for index := range item.foldedItems {
		item.foldedItems[index].foldedParent = item.key
	}
	return item
}

func appendOrMergeFeedGroup(items []feedItem, item feedItem) []feedItem {
	return insertOrMergeFeedGroup(items, item, len(items))
}

func insertOrMergeFeedGroup(items []feedItem, item feedItem, index int) []feedItem {
	if len(item.foldedItems) == 0 {
		return insertFeedItem(items, item, index)
	}
	for index := range items {
		if items[index].key != item.key || len(items[index].foldedItems) == 0 {
			continue
		}
		items[index].foldedItems = append(items[index].foldedItems, item.foldedItems...)
		items[index].title = updateFoldedGroupCount(items[index].title, len(items[index].foldedItems))
		return items
	}
	return insertFeedItem(items, item, index)
}

func insertFeedItem(items []feedItem, item feedItem, index int) []feedItem {
	index = minInt(maxInt(0, index), len(items))
	items = append(items, feedItem{})
	copy(items[index+1:], items[index:])
	items[index] = item
	return items
}

func updateFoldedGroupCount(title string, count int) string {
	return foldedGroupCountPattern.ReplaceAllStringFunc(title, func(match string) string {
		return strings.TrimRight(match, "0123456789") + strconv.Itoa(count)
	})
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

func pageScrollAmount(bodyHeight int) int {
	return maxInt(1, bodyHeight-pageContextLines)
}

func (model *app) pageDownWithBoundary(ctx context.Context, amount int, key keyEvent, keyLabel string) {
	if model.scroll < model.metrics.maxScroll {
		previousLastLine := minInt(model.metrics.bodyLines-1, model.scroll+model.metrics.bodyHeight-1)
		model.clearBoundarySwitch()
		model.scroll = minInt(model.metrics.maxScroll, model.scroll+amount)
		model.setPageAnchor(previousLastLine + 1)
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

func (model *app) pageUpWithConfirmation(ctx context.Context, amount int) {
	model.pageUpWithBoundary(ctx, amount, "b", "b")
}

func (model *app) pageUpWithBoundary(ctx context.Context, amount int, key keyEvent, keyLabel string) {
	if model.scroll > 0 {
		previousFirstLine := model.scroll
		model.clearBoundarySwitch()
		model.scroll = maxInt(0, model.scroll-amount)
		model.setPageAnchor(previousFirstLine - 1)
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
		model.movePrevious(ctx, true)
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
		model.setPageAnchor(previousLastLine + 1)
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
		model.setPageAnchor(previousFirstLine - 1)
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
		model.startCurrentLinkCardRetry(ctx)
		return
	}
	if !model.end {
		model.startFetch(ctx, false)
		model.setMessage("正在加载后续动态", 2*time.Second)
		return
	}
	model.setMessage("已经是最后一条动态", 2*time.Second)
}

func (model *app) movePrevious(ctx context.Context, atEnd bool) {
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
	model.startCurrentLinkCardRetry(ctx)
}

func (model *app) startCurrentLinkCardRetry(ctx context.Context) {
	if len(model.items) == 0 {
		return
	}
	item := model.items[model.index]
	if item.raw == nil {
		return
	}
	if _, pending := model.linkCardRetries[item.key]; pending {
		return
	}
	raw := cloneFeedMap(item.raw)
	if !incrementFailedLinkCardRetryCounts(mapValue(raw["target"])["content"]) {
		return
	}
	model.linkCardRetries[item.key] = model.generation
	generation := model.generation
	go func() {
		retryFailedFeedLinkCards(ctx, model.source, raw)
		select {
		case model.linkCardFetches <- linkCardRetryResult{itemKey: item.key, raw: raw, generation: generation}:
		case <-ctx.Done():
		}
	}()
}

func incrementFailedLinkCardRetryCounts(content any) bool {
	incremented := false
	for _, node := range linkCardsInContent(content) {
		if toString(node["card_error"]) != "" {
			node["card_retry_count"] = toInt64(node["card_retry_count"]) + 1
			incremented = true
			continue
		}
		if incrementFailedLinkCardRetryCounts(mapValue(node["card_detail"])["content"]) {
			incremented = true
		}
	}
	return incremented
}

func (model *app) applyLinkCardRetry(result linkCardRetryResult) {
	if model.linkCardRetries[result.itemKey] == result.generation {
		delete(model.linkCardRetries, result.itemKey)
	}
	if result.generation != model.generation {
		return
	}
	updated, ok := parseFeedItem(result.raw)
	if !ok {
		return
	}
	for index := range model.items {
		updateFeedItemLinkCards(&model.items[index], result.itemKey, result.raw, updated.body)
	}
}

func updateFeedItemLinkCards(item *feedItem, key string, raw map[string]any, body string) {
	if item.key == key {
		item.raw = raw
		item.body = body
	}
	for index := range item.foldedItems {
		updateFeedItemLinkCards(&item.foldedItems[index], key, raw, body)
	}
}

func cloneFeedMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = cloneFeedValue(value)
	}
	return cloned
}

func cloneFeedValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneFeedMap(value)
	case []any:
		cloned := make([]any, len(value))
		for index := range value {
			cloned[index] = cloneFeedValue(value[index])
		}
		return cloned
	default:
		return value
	}
}

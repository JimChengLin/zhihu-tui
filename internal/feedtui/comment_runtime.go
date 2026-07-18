package feedtui

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (model *app) toggleComments(ctx context.Context) {
	if len(model.items) == 0 {
		return
	}
	if model.commentMode {
		model.clearPageAnchor()
		model.commentMode = false
		model.scroll = model.bodyScroll
		model.pageAnchorLine = model.bodyPageAnchorLine
		model.pageAnchorVisible = model.bodyPageAnchorVisible
		return
	}
	item := model.items[model.index]
	if !supportsComments(item) {
		model.setMessage("当前动态类型不支持评论", 3*time.Second)
		return
	}
	state := model.comments[item.key]
	knownEmpty := state != nil && state.loaded && state.err == nil && len(state.items) == 0
	if item.hasCommentCount && item.commentCount == 0 || knownEmpty {
		model.setMessage("暂无评论", 3*time.Second)
		return
	}
	model.bodyScroll = model.scroll
	model.bodyPageAnchorLine = model.pageAnchorLine
	model.bodyPageAnchorVisible = model.pageAnchorVisible
	model.clearPageAnchor()
	model.scroll = 0
	model.commentMode = true
	model.startComments(ctx, item)
}

func (model *app) startComments(ctx context.Context, item feedItem) {
	if model.comments == nil {
		model.comments = map[string]*commentState{}
	}
	state := model.comments[item.key]
	if state == nil {
		state = &commentState{}
		model.comments[item.key] = state
	}
	if state.loading || state.loaded && state.err == nil {
		return
	}
	model.startCommentPage(ctx, item, state, false)
}

func (model *app) startCommentPage(ctx context.Context, item feedItem, state *commentState, appendPage bool) {
	state.loading = true
	if appendPage {
		state.moreErr = nil
	} else {
		state.err = nil
	}
	model.spinner = 0
	cursor := ""
	if appendPage {
		cursor = state.nextCursor
	}
	go func() {
		requestCtx, cancel := context.WithTimeout(ctx, commentPageTimeout)
		defer cancel()
		response, err := model.source.GetCommentsPage(requestCtx, item.kind, item.id, cursor, commentPageSize, "score")
		select {
		case model.commentFetches <- commentFetchResult{key: item.key, response: response, err: err, append: appendPage, cursor: cursor}:
		case <-ctx.Done():
		}
	}()
}

func (model *app) applyCommentFetch(result commentFetchResult) {
	state := model.comments[result.key]
	if state == nil {
		state = &commentState{}
		model.comments[result.key] = state
	}
	state.loading = false
	if result.err != nil {
		if result.append && state.loaded {
			state.moreErr = result.err
		} else {
			state.err = result.err
			state.loaded = true
		}
		return
	}
	page := parseComments(asSlice(result.response["data"]))
	nextCursor, end := commentPaging(result.response)
	var pageErr error
	if !end && nextCursor == "" {
		pageErr = errors.New("知乎评论分页没有返回有效游标")
	}
	if result.append {
		previousCount := len(state.items)
		state.items = appendUniqueComments(state.items, page)
		if !end && (len(state.items) == previousCount || nextCursor == result.cursor) {
			pageErr = errors.New("知乎评论分页没有返回新内容")
		}
	} else {
		state.items = page
	}
	state.loaded = true
	state.err = nil
	if pageErr != nil {
		state.moreErr = pageErr
		return
	}
	state.moreErr = nil
	state.nextCursor, state.end = nextCursor, end
}

func appendUniqueComments(existing, incoming []feedComment) []feedComment {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	for _, comment := range existing {
		seen[comment.id] = struct{}{}
	}
	for _, comment := range incoming {
		if _, duplicate := seen[comment.id]; duplicate && comment.id != "" {
			continue
		}
		seen[comment.id] = struct{}{}
		existing = append(existing, comment)
	}
	return existing
}

func (model *app) maybePrefetchComments(ctx context.Context) {
	state := model.currentCommentState()
	if state == nil || !state.loaded || state.loading || state.end || state.moreErr != nil || len(model.items) == 0 {
		return
	}
	remainingLines := model.metrics.maxScroll - model.scroll
	if remainingLines > maxInt(1, model.metrics.bodyHeight/2) {
		return
	}
	model.startCommentPage(ctx, model.items[model.index], state, true)
}

func (model *app) ensureMoreComments(ctx context.Context) bool {
	if !model.commentMode {
		return false
	}
	state := model.currentCommentState()
	if state == nil || !state.loaded {
		model.setMessage("正在加载更多评论", 2*time.Second)
		return true
	}
	if state.loading {
		return false
	}
	if state.end {
		return false
	}
	model.clearBoundarySwitch()
	model.startCommentPage(ctx, model.items[model.index], state, true)
	model.setMessage("正在加载更多评论", 2*time.Second)
	return true
}

func (model *app) currentCommentState() *commentState {
	if len(model.items) == 0 || model.comments == nil {
		return nil
	}
	return model.comments[model.items[model.index].key]
}

func (model *app) currentCommentsLoading() bool {
	state := model.currentCommentState()
	return model.commentMode && state != nil && state.loading
}

func commentChildKey(stateKey, rootID string) string {
	return stateKey + "\x00" + rootID
}

func (model *app) startCommentChildFetch(ctx context.Context, stateKey string) {
	state := model.comments[stateKey]
	if state == nil || len(state.items) == 0 {
		return
	}
	if model.commentChildComplete == nil {
		model.commentChildComplete = map[string]struct{}{}
	}
	if model.commentChildPending == nil {
		model.commentChildPending = map[string]struct{}{}
	}
	if model.commentChildFetches == nil {
		model.commentChildFetches = make(chan commentChildFetchResult, 1)
	}
	var rootIDs []string
	for _, root := range state.items {
		if root.id == "" || root.childComments <= countLoadedComments(root.children) {
			continue
		}
		key := commentChildKey(stateKey, root.id)
		if _, complete := model.commentChildComplete[key]; complete {
			continue
		}
		if _, pending := model.commentChildPending[key]; pending {
			continue
		}
		model.commentChildPending[key] = struct{}{}
		rootIDs = append(rootIDs, root.id)
	}
	if len(rootIDs) == 0 {
		return
	}
	go func() {
		children := fetchCommentChildren(ctx, model.source, rootIDs)
		select {
		case model.commentChildFetches <- commentChildFetchResult{stateKey: stateKey, rootIDs: rootIDs, children: children}:
		case <-ctx.Done():
		}
	}()
}

func (model *app) applyCommentChildFetch(result commentChildFetchResult) {
	state := model.comments[result.stateKey]
	for _, rootID := range result.rootIDs {
		key := commentChildKey(result.stateKey, rootID)
		delete(model.commentChildPending, key)
		children, fetched := result.children[rootID]
		if !fetched {
			continue
		}
		model.commentChildComplete[key] = struct{}{}
		if state == nil {
			continue
		}
		for index := range state.items {
			if state.items[index].id == rootID {
				state.items[index].children = children
				break
			}
		}
	}
}

func (model *app) toggleFocusedCommentChildren(ctx context.Context) {
	state := model.currentCommentState()
	if state == nil || !state.loaded {
		model.setMessage("评论仍在加载", 2*time.Second)
		return
	}
	_, focusedID := model.focusedComment()
	if focusedID == "" {
		model.setMessage("先用 j/k 选择一条评论", 3*time.Second)
		return
	}
	rootIndex := -1
	for index := range state.items {
		if state.items[index].id == focusedID || commentContainsID(state.items[index].children, focusedID) {
			rootIndex = index
			break
		}
	}
	if rootIndex < 0 {
		model.setMessage("蓝色焦点不在评论上", 2*time.Second)
		return
	}
	root := &state.items[rootIndex]
	if root.childComments == 0 && len(root.children) == 0 {
		model.setMessage("这条评论没有回复", 2*time.Second)
		return
	}
	if state.expandedChildren == nil {
		state.expandedChildren = map[string]bool{}
	}
	if state.expandedChildren[root.id] {
		delete(state.expandedChildren, root.id)
		for line, commentID := range model.metrics.commentIDs {
			if commentID == root.id {
				model.setPageAnchor(line)
				break
			}
		}
		model.setMessage("已收起回复", 2*time.Second)
		return
	}
	state.expandedChildren[root.id] = true
	if len(root.children) == 0 && root.childComments > 0 {
		model.startCommentChildFetch(ctx, model.items[model.index].key)
		model.setMessage("正在加载回复", 2*time.Second)
		return
	}
	model.setMessage(fmt.Sprintf("已展开 %d 条回复", len(root.children)), 2*time.Second)
}

func commentContainsID(comments []feedComment, commentID string) bool {
	for _, comment := range comments {
		if comment.id == commentID || commentContainsID(comment.children, commentID) {
			return true
		}
	}
	return false
}

func (model *app) startCommentRelationshipFetch(ctx context.Context, stateKey string) {
	state := model.comments[stateKey]
	if state == nil || len(state.items) == 0 {
		return
	}
	if model.commentRelations == nil {
		model.commentRelations = map[string]commentRelation{}
	}
	if model.commentRelationPending == nil {
		model.commentRelationPending = map[string]struct{}{}
	}
	if model.commentRelationFetches == nil {
		model.commentRelationFetches = make(chan commentRelationFetchResult, 1)
	}
	applyCommentRelations(state.items, model.commentRelations)
	var tokens []string
	for _, token := range commentTokens(state.items) {
		if _, cached := model.commentRelations[token]; cached {
			continue
		}
		if _, pending := model.commentRelationPending[token]; pending {
			continue
		}
		model.commentRelationPending[token] = struct{}{}
		tokens = append(tokens, token)
	}
	if len(tokens) == 0 {
		return
	}
	go func() {
		relations := fetchCommentRelations(ctx, model.source, tokens)
		select {
		case model.commentRelationFetches <- commentRelationFetchResult{tokens: tokens, relations: relations}:
		case <-ctx.Done():
		}
	}()
}

func (model *app) applyCommentRelationshipFetch(result commentRelationFetchResult) {
	for _, token := range result.tokens {
		delete(model.commentRelationPending, token)
		model.commentRelations[token] = result.relations[token]
	}
	for _, state := range model.comments {
		applyCommentRelations(state.items, result.relations)
	}
}

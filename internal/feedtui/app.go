package feedtui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const feedPageSize = 10

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
var foldedGroupCountPattern = regexp.MustCompile(`^(还有\s*)\d+`)

type feedSource interface {
	linkCardSource
	GetFollowingFeed(context.Context, string, int) (map[string]any, error)
	GetComments(context.Context, string, string, int, int, string) (map[string]any, error)
	GetChildComments(context.Context, string, int, int) (map[string]any, error)
	GetUserProfile(context.Context, string) (map[string]any, error)
	CreateComment(context.Context, string, string, string) (map[string]any, error)
	ReplyCommentToResource(context.Context, string, string, string, string) (map[string]any, error)
	VoteUp(context.Context, string) (bool, error)
	VoteNeutral(context.Context, string) (bool, error)
}

type linkCardSource interface {
	GetPin(context.Context, string) (map[string]any, error)
	GetAnswer(context.Context, string) (map[string]any, error)
}

type app struct {
	source                 feedSource
	items                  []feedItem
	index                  int
	scroll                 int
	sidebarStart           int
	width                  int
	height                 int
	nextURL                string
	end                    bool
	loading                bool
	refreshing             bool
	err                    error
	message                string
	messageUntil           time.Time
	boundarySwitchKey      keyEvent
	pageAnchorLine         int
	pageAnchorVisible      bool
	showHelp               bool
	zenMode                bool
	hideFeedHeader         bool
	hideItemPosition       bool
	spinner                int
	generation             int
	metrics                layoutMetrics
	fetches                chan fetchResult
	lastReadTopKey         string
	lastReadBottomKey      string
	pendingReadTopKey      string
	pendingReadBottomKey   string
	pendingRefreshTopKey   string
	newItemKeys            map[string]struct{}
	firstViewedKey         string
	furthestViewedKey      string
	commentMode            bool
	bodyScroll             int
	comments               map[string]*commentState
	commentFetches         chan commentFetchResult
	commentRelations       map[string]commentRelation
	commentRelationPending map[string]struct{}
	commentRelationFetches chan commentRelationFetchResult
	commentChildComplete   map[string]struct{}
	commentChildPending    map[string]struct{}
	commentChildFetches    chan commentChildFetchResult
	composing              bool
	composeInput           string
	composeTargets         []commentComposeTarget
	composeTarget          int
	composeInsertLine      int
	composeError           string
	commentSubmitting      bool
	commentPosts           chan commentPostResult
	voting                 bool
	voteResults            chan voteResult
}

type fetchResult struct {
	response   map[string]any
	err        error
	reset      bool
	generation int
}

type voteResult struct {
	answerID string
	voted    bool
	ok       bool
	err      error
}

type commentPostResult struct {
	itemKey string
	reply   bool
	err     error
}

type commentFocusPosition struct {
	id   string
	line int
}

type keyEvent string

const (
	keyUp        keyEvent = "up"
	keyDown      keyEvent = "down"
	keyLeft      keyEvent = "left"
	keyRight     keyEvent = "right"
	keyPageUp    keyEvent = "page-up"
	keyPageDown  keyEvent = "page-down"
	keyCtrlC     keyEvent = "ctrl-c"
	keyCtrlG     keyEvent = "ctrl-g"
	keyEscape    keyEvent = "escape"
	keyTab       keyEvent = "tab"
	keyBackspace keyEvent = "backspace"
)

// Run starts an alternate-screen terminal reader for the current user's
// following feed and restores the terminal before returning.
func Run(ctx context.Context, source feedSource, in, out *os.File) error {
	if !isTerminal(in) || !isTerminal(out) {
		return fmt.Errorf("zhihu feed --tui requires an interactive terminal")
	}
	state, err := makeRaw(in)
	if err != nil {
		return fmt.Errorf("enable terminal raw mode: %w", err)
	}
	defer restoreTerminal(in, state)

	if _, err := fmt.Fprint(out, "\033[?1049h\033[?25l\033[2J\033[H"); err != nil {
		return err
	}
	defer fmt.Fprint(out, "\033[?25h\033[?1049l")

	width, height, err := terminalSize(out)
	if err != nil {
		return fmt.Errorf("read terminal size: %w", err)
	}
	model := &app{
		source:                 source,
		width:                  width,
		height:                 height,
		fetches:                make(chan fetchResult, 1),
		comments:               map[string]*commentState{},
		commentFetches:         make(chan commentFetchResult, 2),
		commentRelations:       map[string]commentRelation{},
		commentRelationPending: map[string]struct{}{},
		commentRelationFetches: make(chan commentRelationFetchResult, 4),
		commentChildComplete:   map[string]struct{}{},
		commentChildPending:    map[string]struct{}{},
		commentChildFetches:    make(chan commentChildFetchResult, 4),
		commentPosts:           make(chan commentPostResult, 1),
		voteResults:            make(chan voteResult, 1),
	}
	keys := make(chan keyEvent)
	readErrors := make(chan error, 1)
	go readKeys(in, keys, readErrors)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGWINCH)
	defer signal.Stop(signals)

	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	model.startFetch(ctx, true)
	if err := model.render(out); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sig := <-signals:
			if sig == syscall.SIGWINCH {
				model.clearPageAnchor()
				width, height, err = terminalSize(out)
				if err != nil {
					return fmt.Errorf("read resized terminal: %w", err)
				}
				model.width, model.height = width, height
				if err := model.render(out); err != nil {
					return err
				}
				continue
			}
			return nil
		case err := <-readErrors:
			if errors.Is(err, os.ErrClosed) {
				return nil
			}
			return fmt.Errorf("read terminal input: %w", err)
		case key := <-keys:
			if model.handleKey(ctx, key) {
				return nil
			}
			model.maybePrefetch(ctx)
			if err := model.render(out); err != nil {
				return err
			}
		case fetched := <-model.fetches:
			model.applyFetch(fetched)
			model.maybePrefetch(ctx)
			if err := model.render(out); err != nil {
				return err
			}
		case fetched := <-model.commentFetches:
			model.applyCommentFetch(fetched)
			if err := model.render(out); err != nil {
				return err
			}
			model.maybePrefetch(ctx)
			state := model.comments[fetched.key]
			if state != nil && !state.loading {
				model.startCommentChildFetch(ctx, fetched.key)
				model.startCommentRelationshipFetch(ctx, fetched.key)
			}
		case fetched := <-model.commentRelationFetches:
			model.applyCommentRelationshipFetch(fetched)
			if err := model.render(out); err != nil {
				return err
			}
		case fetched := <-model.commentChildFetches:
			model.applyCommentChildFetch(fetched)
			model.startCommentRelationshipFetch(ctx, fetched.stateKey)
			if err := model.render(out); err != nil {
				return err
			}
		case result := <-model.voteResults:
			model.applyVote(result)
			if err := model.render(out); err != nil {
				return err
			}
		case result := <-model.commentPosts:
			model.applyCommentPost(ctx, result)
			if err := model.render(out); err != nil {
				return err
			}
		case now := <-ticker.C:
			commentLoading := model.currentCommentsLoading()
			needsRender := model.loading || commentLoading || model.voting || model.composing
			if needsRender {
				model.spinner++
			}
			if model.message != "" && !model.messageUntil.IsZero() && now.After(model.messageUntil) {
				model.message = ""
				model.messageUntil = time.Time{}
				needsRender = true
			}
			if needsRender {
				if err := model.render(out); err != nil {
					return err
				}
			}
		}
	}
}

func (model *app) startFetch(ctx context.Context, reset bool) {
	if model.loading && !reset {
		return
	}
	model.loading = true
	model.refreshing = reset
	model.err = nil
	model.spinner = 0
	if reset {
		model.generation++
	}
	generation := model.generation
	nextURL := model.nextURL
	if reset {
		nextURL = ""
	}
	go func() {
		response, err := model.source.GetFollowingFeed(ctx, nextURL, feedPageSize)
		if err == nil {
			hydrateFeedLinkCards(ctx, model.source, response)
		}
		select {
		case model.fetches <- fetchResult{response: response, err: err, reset: reset, generation: generation}:
		case <-ctx.Done():
		}
	}()
}

func (model *app) applyFetch(result fetchResult) {
	if result.generation != model.generation {
		return
	}
	model.loading = false
	model.refreshing = false
	if result.err != nil {
		model.err = result.err
		if len(model.items) > 0 {
			model.setMessage("后续动态加载失败："+result.err.Error(), 5*time.Second)
		}
		return
	}

	newItems := parseFeedItems(asSlice(result.response["data"]))
	previousItems := collapsedFeedItems(model.items)
	previousKeys := make(map[string]struct{}, len(previousItems))
	for _, item := range previousItems {
		collectFeedItemKeys(item, previousKeys)
	}
	refreshingExistingFeed := result.reset && model.pendingRefreshTopKey != ""
	if result.reset {
		model.items = nil
		model.index = 0
		model.scroll = 0
		model.sidebarStart = 0
		model.end = false
		model.nextURL = ""
		if model.pendingRefreshTopKey != "" {
			model.lastReadTopKey = model.pendingReadTopKey
			model.lastReadBottomKey = model.pendingReadBottomKey
			model.pendingReadTopKey = ""
			model.pendingReadBottomKey = ""
			model.pendingRefreshTopKey = ""
			model.newItemKeys = make(map[string]struct{})
		}
	}
	added := 0
	if result.reset {
		representedLeaves := make(map[string]struct{})
		for _, item := range newItems {
			item, leafCount, kept := takeUnrepresentedFeedLeaves(item, representedLeaves)
			if !kept {
				continue
			}
			model.items = appendOrMergeFeedGroup(model.items, item)
			if refreshingExistingFeed {
				markUnseenFeedItemKeys(item, previousKeys, model.newItemKeys)
				added += countUnseenFeedItemKeys(item, previousKeys)
			} else {
				added += leafCount
			}
		}
	} else {
		seen := make(map[string]struct{}, len(model.items)+len(newItems))
		for _, item := range model.items {
			seen[item.key] = struct{}{}
		}
		for _, item := range newItems {
			if _, exists := seen[item.key]; exists {
				continue
			}
			seen[item.key] = struct{}{}
			model.items = append(model.items, item)
			added++
		}
	}
	if refreshingExistingFeed {
		representedLeaves := make(map[string]struct{})
		for _, item := range model.items {
			collectFeedItemKeys(item, representedLeaves)
		}
		for _, item := range previousItems {
			item, _, kept := takeUnrepresentedFeedLeaves(item, representedLeaves)
			if !kept {
				continue
			}
			model.items = appendOrMergeFeedGroup(model.items, item)
		}
	}

	paging := mapValue(result.response["paging"])
	model.nextURL = strings.TrimSpace(toString(paging["next"]))
	model.end = truthy(paging["is_end"]) || model.nextURL == ""
	model.err = nil
	if result.reset && len(model.items) > 0 {
		model.setMessage(fmt.Sprintf("已刷新 %d 条", added), 2*time.Second)
	} else if added > 0 {
		model.setMessage(fmt.Sprintf("已预取 %d 条", added), 2*time.Second)
	}
	if added == 0 && len(model.items) == 0 {
		model.end = true
	}
}

func (model *app) maybePrefetch(ctx context.Context) {
	if model.commentMode {
		model.maybePrefetchComments(ctx)
		return
	}
	if model.loading || model.end || len(model.items) == 0 {
		return
	}
	if model.index >= len(model.items)-3 {
		model.startFetch(ctx, false)
	}
}

func (model *app) handleKey(ctx context.Context, key keyEvent) bool {
	if key == keyCtrlC {
		return true
	}
	if model.composing {
		return model.handleCommentComposerKey(ctx, key)
	}
	if key == "q" {
		return true
	}
	if model.showHelp {
		if key == "?" {
			model.showHelp = false
		}
		return false
	}
	preservesCommentFocus := model.commentMode && (key == "j" || key == "k" || key == "J" || key == "K" || key == keyDown || key == keyUp || key == "e" || key == "\r")
	if key != model.boundarySwitchKey && key != "w" && !preservesCommentFocus {
		model.clearBoundarySwitch()
	}
	switch key {
	case "?":
		model.showHelp = true
	case "r":
		model.clearPageAnchor()
		model.captureRefreshBoundary()
		model.commentMode = false
		model.bodyScroll = 0
		model.scroll = 0
		model.startFetch(ctx, true)
		model.message = ""
	case "c":
		model.toggleComments(ctx)
	case "w":
		model.startCommentComposer()
	case "J":
		model.moveCommentFocus(ctx, 1)
	case "K":
		model.moveCommentFocus(ctx, -1)
	case "v":
		model.toggleVote(ctx)
	case "e":
		if model.commentMode {
			model.toggleFocusedCommentChildren(ctx)
		} else {
			model.toggleFoldedGroup()
		}
	case "z":
		model.clearPageAnchor()
		model.zenMode = !model.zenMode
		if model.zenMode {
			model.setMessage("已进入专注模式", 2*time.Second)
		} else {
			model.setMessage("已恢复双栏模式", 2*time.Second)
		}
	case "n", "l", keyRight:
		model.moveNext(ctx)
	case "p", "h", keyLeft:
		model.movePrevious(false)
	case "j", keyDown:
		if model.commentMode {
			model.moveCommentFocus(ctx, 1)
		} else {
			model.lineDown()
		}
	case "k", keyUp:
		if model.commentMode {
			model.moveCommentFocus(ctx, -1)
		} else {
			model.lineUp()
		}
	case " ":
		model.pageDownWithConfirmation(ctx, maxInt(1, model.metrics.bodyHeight*7/8))
	case "b":
		model.pageUpWithConfirmation(maxInt(1, model.metrics.bodyHeight*7/8))
	case "\r":
		if model.commentMode {
			model.toggleFocusedCommentChildren(ctx)
		} else if !model.toggleFoldedGroup() {
			model.scrollDown(maxInt(1, model.metrics.bodyHeight/2))
		}
	case "f", keyPageDown, "d":
		model.scrollDown(maxInt(1, model.metrics.bodyHeight/2))
	case keyPageUp, "u":
		model.scrollUp(maxInt(1, model.metrics.bodyHeight/2))
	case "g":
		if len(model.items) > 0 {
			model.clearPageAnchor()
			model.commentMode = false
			model.bodyScroll = 0
			model.index, model.scroll = 0, 0
		}
	case "G":
		if len(model.items) > 0 {
			model.clearPageAnchor()
			model.commentMode = false
			model.bodyScroll = 0
			model.index, model.scroll = len(model.items)-1, 0
		}
	case "o":
		model.openCurrent()
	}
	return false
}

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
	if model.consumeBoundarySwitch(" ") {
		model.moveNext(ctx)
		return
	}
	model.setPageAnchor(model.metrics.bodyLines - 1)
	model.armBoundarySwitch(" ", "已到"+model.readingAreaLabel()+"底部，再按一次 space 切换下一条")
}

func (model *app) pageUpWithConfirmation(amount int) {
	if model.scroll > 0 {
		previousFirstLine := model.scroll
		model.clearBoundarySwitch()
		model.scroll = maxInt(0, model.scroll-amount)
		model.setPageAnchor(previousFirstLine)
		model.clearMessage()
		return
	}
	if model.consumeBoundarySwitch("b") {
		model.movePrevious(true)
		return
	}
	model.setPageAnchor(0)
	model.armBoundarySwitch("b", "已到"+model.readingAreaLabel()+"顶部，再按一次 b 切换上一条")
}

func (model *app) scrollDown(amount int) {
	model.clearPageAnchor()
	if model.scroll < model.metrics.maxScroll {
		model.scroll = minInt(model.metrics.maxScroll, model.scroll+amount)
		model.clearMessage()
		return
	}
	model.setMessage("已到"+model.readingAreaLabel()+"底部", 2*time.Second)
}

func (model *app) scrollUp(amount int) {
	model.clearPageAnchor()
	if model.scroll > 0 {
		model.scroll = maxInt(0, model.scroll-amount)
		model.clearMessage()
		return
	}
	model.setMessage("已到"+model.readingAreaLabel()+"顶部", 2*time.Second)
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

func (model *app) toggleComments(ctx context.Context) {
	model.clearPageAnchor()
	if len(model.items) == 0 {
		return
	}
	if model.commentMode {
		model.commentMode = false
		model.scroll = model.bodyScroll
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
	offset := 0
	if appendPage {
		offset = state.nextOffset
	}
	go func() {
		requestCtx, cancel := context.WithTimeout(ctx, commentPageTimeout)
		defer cancel()
		response, err := model.source.GetComments(requestCtx, item.kind, item.id, offset, commentPageSize, "score")
		select {
		case model.commentFetches <- commentFetchResult{key: item.key, response: response, err: err, append: appendPage, offset: offset}:
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
	nextOffset, end := commentPaging(result.response, result.offset, len(page))
	if result.append {
		previousCount := len(state.items)
		state.items = appendUniqueComments(state.items, page)
		if !end && (len(state.items) == previousCount || nextOffset <= result.offset) {
			state.loaded = true
			state.err = nil
			state.moreErr = errors.New("知乎评论分页没有返回新内容")
			return
		}
	} else {
		state.items = page
	}
	state.loaded = true
	state.err = nil
	state.moreErr = nil
	state.nextOffset, state.end = nextOffset, end
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
		model.composeError = ""
		model.setMessage("已取消写评论", 2*time.Second)
	case keyBackspace:
		model.composeInput = dropLastTextUnit(model.composeInput)
		model.composeError = ""
	case "\r":
		model.submitComment(ctx)
	default:
		if isPrintableKey(key) {
			model.composeInput += string(key)
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
		var err error
		if target.commentID == "" {
			_, err = model.source.CreateComment(ctx, item.kind, item.id, content)
		} else {
			_, err = model.source.ReplyCommentToResource(ctx, item.kind, item.id, target.commentID, content)
		}
		select {
		case model.commentPosts <- commentPostResult{itemKey: item.key, reply: target.commentID != "", err: err}:
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
	model.composeError = ""
	if len(model.items) > 0 {
		current := model.items[model.index]
		incrementCommentCount(model.items, current.kind, current.id)
	}
	state := model.comments[result.itemKey]
	if state != nil {
		state.loaded = false
		state.err = nil
	}
	if len(model.items) > 0 && model.items[model.index].key == result.itemKey {
		model.commentMode = true
		model.scroll = 0
		model.startComments(ctx, model.items[model.index])
	}
	if result.reply {
		model.setMessage("回复已发布", 2*time.Second)
	} else {
		model.setMessage("评论已发布", 2*time.Second)
	}
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

func isPrintableKey(key keyEvent) bool {
	runes := []rune(string(key))
	return len(runes) == 1 && runes[0] >= ' '
}

func (model *app) toggleVote(ctx context.Context) {
	if len(model.items) == 0 {
		return
	}
	if model.voting {
		model.setMessage("赞同请求处理中", 2*time.Second)
		return
	}
	item := model.items[model.index]
	if item.kind != "answer" {
		if len(item.foldedItems) > 0 {
			model.setMessage("请先展开并选择具体回答", 3*time.Second)
		} else {
			model.setMessage("当前仅支持赞同回答", 3*time.Second)
		}
		return
	}

	voted := !item.voted
	model.voting = true
	model.spinner = 0
	if voted {
		model.message = "正在赞同"
	} else {
		model.message = "正在取消赞同"
	}
	model.messageUntil = time.Time{}
	go func() {
		var ok bool
		var err error
		if voted {
			ok, err = model.source.VoteUp(ctx, item.id)
		} else {
			ok, err = model.source.VoteNeutral(ctx, item.id)
		}
		select {
		case model.voteResults <- voteResult{answerID: item.id, voted: voted, ok: ok, err: err}:
		case <-ctx.Done():
		}
	}()
}

func (model *app) applyVote(result voteResult) {
	model.voting = false
	action := "赞同"
	if !result.voted {
		action = "取消赞同"
	}
	if result.err != nil {
		model.setMessage(action+"失败："+result.err.Error(), 4*time.Second)
		return
	}
	if !result.ok {
		model.setMessage(action+"失败：知乎未接受请求", 4*time.Second)
		return
	}
	updateVoteInItems(model.items, result.answerID, result.voted)
	if result.voted {
		model.setMessage("已赞同", 2*time.Second)
	} else {
		model.setMessage("已取消赞同", 2*time.Second)
	}
}

func updateVoteInItems(items []feedItem, answerID string, voted bool) {
	for index := range items {
		updateFeedItemVote(&items[index], answerID, voted)
	}
}

func updateFeedItemVote(item *feedItem, answerID string, voted bool) {
	if item.kind == "answer" && item.id == answerID && item.voted != voted {
		if item.hasVoteCount {
			if voted {
				item.voteCount++
			} else if item.voteCount > 0 {
				item.voteCount--
			}
			item.stats = replaceVoteStat(item.stats, item.voteCount)
		}
		item.voted = voted
	}
	for index := range item.foldedItems {
		updateFeedItemVote(&item.foldedItems[index], answerID, voted)
	}
}

func (model *app) openCurrent() {
	if len(model.items) == 0 || model.items[model.index].url == "" {
		model.setMessage("当前动态没有可打开的网页链接", 3*time.Second)
		return
	}
	var command *exec.Cmd
	if runtime.GOOS == "darwin" {
		command = exec.Command("open", model.items[model.index].url)
	} else {
		command = exec.Command("xdg-open", model.items[model.index].url)
	}
	if err := command.Start(); err != nil {
		model.setMessage("打开浏览器失败："+err.Error(), 4*time.Second)
		return
	}
	model.setMessage("已在浏览器中打开", 2*time.Second)
}

func (model *app) setMessage(message string, duration time.Duration) {
	model.message = message
	model.messageUntil = time.Now().Add(duration)
}

func (model *app) render(out *os.File) error {
	lines, metrics := renderApp(model)
	model.metrics = metrics
	if err := writeFrame(out, lines, model.width, model.height); err != nil {
		return err
	}
	if !model.showHelp && model.width >= 42 && model.height >= 14 {
		model.markCurrentViewed()
	}
	return nil
}

func (model *app) markCurrentViewed() {
	if len(model.items) == 0 {
		return
	}
	currentKey := model.items[model.index].key
	positions := logicalFeedPositions(model.items)
	currentPosition := positions[currentKey]
	if model.firstViewedKey == "" {
		model.firstViewedKey = currentKey
	} else if firstPosition, found := positions[model.firstViewedKey]; found && currentPosition < firstPosition {
		model.firstViewedKey = currentKey
	}
	if model.furthestViewedKey == "" {
		model.furthestViewedKey = currentKey
		return
	}
	if furthestPosition, found := positions[model.furthestViewedKey]; found && currentPosition > furthestPosition {
		model.furthestViewedKey = currentKey
	}
}

func logicalFeedPositions(items []feedItem) map[string]int {
	positions := make(map[string]int, len(items))
	position := 0
	var add func(feedItem)
	add = func(item feedItem) {
		positions[item.key] = position
		position++
		for _, child := range item.foldedItems {
			add(child)
		}
	}
	for _, item := range items {
		if item.foldedParent == "" {
			add(item)
		}
	}
	return positions
}

type terminalKeyDecoder struct {
	escape []byte
	utf8   []byte
}

func (decoder *terminalKeyDecoder) hasPendingEscape() bool {
	return len(decoder.escape) > 0
}

func (decoder *terminalKeyDecoder) push(value byte) []keyEvent {
	if len(decoder.escape) > 0 {
		return decoder.pushEscape(value)
	}
	if value == 27 {
		decoder.escape = append(decoder.escape[:0], value)
		return nil
	}
	return decoder.pushNormal(value)
}

func (decoder *terminalKeyDecoder) pushEscape(value byte) []keyEvent {
	decoder.escape = append(decoder.escape, value)
	switch len(decoder.escape) {
	case 2:
		if value == '[' || value == 'O' {
			return nil
		}
		return decoder.flushEscape()
	case 3:
		switch value {
		case 'A':
			return decoder.finishEscape(keyUp)
		case 'B':
			return decoder.finishEscape(keyDown)
		case 'C':
			return decoder.finishEscape(keyRight)
		case 'D':
			return decoder.finishEscape(keyLeft)
		case '5', '6':
			if decoder.escape[1] == '[' {
				return nil
			}
		}
		return decoder.flushEscape()
	case 4:
		if decoder.escape[1] == '[' && value == '~' {
			if decoder.escape[2] == '5' {
				return decoder.finishEscape(keyPageUp)
			}
			if decoder.escape[2] == '6' {
				return decoder.finishEscape(keyPageDown)
			}
		}
		return decoder.flushEscape()
	default:
		return decoder.flushEscape()
	}
}

func (decoder *terminalKeyDecoder) finishEscape(key keyEvent) []keyEvent {
	decoder.escape = decoder.escape[:0]
	return []keyEvent{key}
}

func (decoder *terminalKeyDecoder) flushEscape() []keyEvent {
	if len(decoder.escape) == 0 {
		return nil
	}
	remainder := append([]byte(nil), decoder.escape[1:]...)
	decoder.escape = decoder.escape[:0]
	events := []keyEvent{keyEscape}
	for _, value := range remainder {
		events = append(events, decoder.push(value)...)
	}
	return events
}

func (decoder *terminalKeyDecoder) pushNormal(value byte) []keyEvent {
	if len(decoder.utf8) == 0 && value < utf8.RuneSelf {
		switch value {
		case 3:
			return []keyEvent{keyCtrlC}
		case 7:
			return []keyEvent{keyCtrlG}
		case 8, 127:
			return []keyEvent{keyBackspace}
		case '\t':
			return []keyEvent{keyTab}
		case '\r', '\n':
			return []keyEvent{"\r"}
		default:
			return []keyEvent{keyEvent(string(value))}
		}
	}
	decoder.utf8 = append(decoder.utf8, value)
	if !utf8.FullRune(decoder.utf8) {
		return nil
	}
	r, size := utf8.DecodeRune(decoder.utf8)
	decoder.utf8 = decoder.utf8[size:]
	events := []keyEvent{keyEvent(string(r))}
	for len(decoder.utf8) > 0 && utf8.FullRune(decoder.utf8) {
		r, size = utf8.DecodeRune(decoder.utf8)
		decoder.utf8 = decoder.utf8[size:]
		events = append(events, keyEvent(string(r)))
	}
	return events
}

func readKeys(in *os.File, keys chan<- keyEvent, errs chan<- error) {
	bytes := make(chan byte, 256)
	readErrors := make(chan error, 1)
	go func() {
		buffer := make([]byte, 64)
		for {
			count, err := in.Read(buffer)
			for _, value := range buffer[:count] {
				bytes <- value
			}
			if err != nil {
				readErrors <- err
				return
			}
		}
	}()

	var decoder terminalKeyDecoder
	var escapeTimer *time.Timer
	var escapeTimeout <-chan time.Time
	updateEscapeTimer := func() {
		if !decoder.hasPendingEscape() {
			if escapeTimer != nil && !escapeTimer.Stop() {
				select {
				case <-escapeTimer.C:
				default:
				}
			}
			escapeTimeout = nil
			return
		}
		if escapeTimer == nil {
			escapeTimer = time.NewTimer(50 * time.Millisecond)
		} else {
			if !escapeTimer.Stop() {
				select {
				case <-escapeTimer.C:
				default:
				}
			}
			escapeTimer.Reset(50 * time.Millisecond)
		}
		escapeTimeout = escapeTimer.C
	}
	emit := func(events []keyEvent) {
		for _, key := range events {
			keys <- key
		}
	}

	for {
		select {
		case value := <-bytes:
			emit(decoder.push(value))
			updateEscapeTimer()
		case <-escapeTimeout:
			emit(decoder.flushEscape())
			escapeTimeout = nil
		case err := <-readErrors:
			emit(decoder.flushEscape())
			errs <- err
			return
		}
	}
}

func readKey(reader *bufio.Reader) (keyEvent, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	switch first {
	case 3:
		return keyCtrlC, nil
	case 7:
		return keyCtrlG, nil
	case 8, 127:
		return keyBackspace, nil
	case '\t':
		return keyTab, nil
	case '\r', '\n':
		return "\r", nil
	case 27:
		second, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, os.ErrClosed) || errors.Is(err, io.EOF) {
				return keyEscape, nil
			}
			return "", err
		}
		if second != '[' && second != 'O' {
			if err := reader.UnreadByte(); err != nil {
				return "", err
			}
			return keyEscape, nil
		}
		third, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return keyEscape, nil
			}
			return "", err
		}
		switch third {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		case 'C':
			return keyRight, nil
		case 'D':
			return keyLeft, nil
		case '5', '6':
			if _, err := reader.ReadByte(); err != nil {
				return "", err
			}
			if third == '5' {
				return keyPageUp, nil
			}
			return keyPageDown, nil
		default:
			return "", nil
		}
	default:
		if first >= 0x80 {
			if err := reader.UnreadByte(); err != nil {
				return "", err
			}
			r, _, err := reader.ReadRune()
			if err != nil {
				return "", err
			}
			return keyEvent(string(r)), nil
		}
		return keyEvent(string(first)), nil
	}
}

func truthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "true" || typed == "1"
	default:
		return false
	}
}

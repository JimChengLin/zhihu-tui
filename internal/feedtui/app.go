package feedtui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const feedPageSize = 10

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
var foldedGroupCountPattern = regexp.MustCompile(`^(还有\s*)\d+`)

type feedSource interface {
	pinSource
	GetFollowingFeed(context.Context, string, int) (map[string]any, error)
	GetComments(context.Context, string, string, int, int, string) (map[string]any, error)
}

type pinSource interface {
	GetPin(context.Context, string) (map[string]any, error)
}

type app struct {
	source               feedSource
	items                []feedItem
	index                int
	scroll               int
	sidebarStart         int
	width                int
	height               int
	nextURL              string
	end                  bool
	loading              bool
	refreshing           bool
	err                  error
	message              string
	messageUntil         time.Time
	boundarySwitchKey    keyEvent
	pageAnchorLine       int
	pageAnchorVisible    bool
	showHelp             bool
	zenMode              bool
	hideFeedHeader       bool
	hideItemPosition     bool
	spinner              int
	generation           int
	metrics              layoutMetrics
	fetches              chan fetchResult
	lastReadTopKey       string
	lastReadBottomKey    string
	pendingReadTopKey    string
	pendingReadBottomKey string
	pendingRefreshTopKey string
	newItemKeys          map[string]struct{}
	firstViewedKey       string
	furthestViewedKey    string
	commentMode          bool
	bodyScroll           int
	comments             map[string]*commentState
	commentFetches       chan commentFetchResult
}

type fetchResult struct {
	response   map[string]any
	err        error
	reset      bool
	generation int
}

type keyEvent string

const (
	keyUp       keyEvent = "up"
	keyDown     keyEvent = "down"
	keyLeft     keyEvent = "left"
	keyRight    keyEvent = "right"
	keyPageUp   keyEvent = "page-up"
	keyPageDown keyEvent = "page-down"
	keyCtrlC    keyEvent = "ctrl-c"
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
		source:         source,
		width:          width,
		height:         height,
		fetches:        make(chan fetchResult, 1),
		comments:       map[string]*commentState{},
		commentFetches: make(chan commentFetchResult, 2),
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
		case now := <-ticker.C:
			commentLoading := model.currentCommentsLoading()
			needsRender := model.loading || commentLoading
			if model.loading || commentLoading {
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
	if model.loading || model.end || len(model.items) == 0 {
		return
	}
	if model.index >= len(model.items)-3 {
		model.startFetch(ctx, false)
	}
}

func (model *app) handleKey(ctx context.Context, key keyEvent) bool {
	if key == keyCtrlC || key == "q" {
		return true
	}
	if model.showHelp {
		if key == "?" {
			model.showHelp = false
		}
		return false
	}
	if key != model.boundarySwitchKey {
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
	case "e":
		model.toggleFoldedGroup()
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
		model.lineDown()
	case "k", keyUp:
		model.lineUp()
	case " ":
		model.pageDownWithConfirmation(ctx, maxInt(1, model.metrics.bodyHeight*7/8))
	case "b":
		model.pageUpWithConfirmation(maxInt(1, model.metrics.bodyHeight*7/8))
	case "\r":
		if !model.toggleFoldedGroup() {
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
	model.setMessage("已到正文底部", 2*time.Second)
}

func (model *app) lineUp() {
	model.clearPageAnchor()
	if model.scroll > 0 {
		model.scroll--
		model.clearMessage()
		return
	}
	model.setMessage("已到正文顶部", 2*time.Second)
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
	if model.consumeBoundarySwitch(" ") {
		model.moveNext(ctx)
		return
	}
	model.setPageAnchor(model.metrics.bodyLines - 1)
	model.armBoundarySwitch(" ", "已到正文底部，再按一次 space 切换下一条")
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
	model.armBoundarySwitch("b", "已到正文顶部，再按一次 b 切换上一条")
}

func (model *app) scrollDown(amount int) {
	model.clearPageAnchor()
	if model.scroll < model.metrics.maxScroll {
		model.scroll = minInt(model.metrics.maxScroll, model.scroll+amount)
		model.clearMessage()
		return
	}
	model.setMessage("已到正文底部", 2*time.Second)
}

func (model *app) scrollUp(amount int) {
	model.clearPageAnchor()
	if model.scroll > 0 {
		model.scroll = maxInt(0, model.scroll-amount)
		model.clearMessage()
		return
	}
	model.setMessage("已到正文顶部", 2*time.Second)
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
	state.loading = true
	state.err = nil
	model.spinner = 0
	go func() {
		response, err := model.source.GetComments(ctx, item.kind, item.id, 0, commentPageSize, "score")
		select {
		case model.commentFetches <- commentFetchResult{key: item.key, response: response, err: err}:
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
	state.loaded = true
	state.err = result.err
	if result.err == nil {
		state.items = parseComments(asSlice(result.response["data"]))
	}
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

func readKeys(in *os.File, keys chan<- keyEvent, errs chan<- error) {
	reader := bufio.NewReader(in)
	for {
		key, err := readKey(reader)
		if err != nil {
			errs <- err
			return
		}
		keys <- key
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
	case '\r', '\n':
		return "\r", nil
	case 27:
		second, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if second != '[' && second != 'O' {
			return keyEvent(string(second)), nil
		}
		third, err := reader.ReadByte()
		if err != nil {
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

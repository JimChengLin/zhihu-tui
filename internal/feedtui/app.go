package feedtui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const feedPageSize = 10

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
var foldedGroupCountPattern = regexp.MustCompile(`^(还有\s*)\d+`)

type feedSource interface {
	linkCardSource
	GetFollowingFeed(context.Context, string, int) (map[string]any, error)
	GetCommentsPage(context.Context, string, string, string, int, string) (map[string]any, error)
	GetChildComments(context.Context, string, int, int) (map[string]any, error)
	GetUserProfile(context.Context, string) (map[string]any, error)
	CreateComment(context.Context, string, string, string) (map[string]any, error)
	ReplyCommentToResource(context.Context, string, string, string, string) (map[string]any, error)
	SetContentVote(context.Context, string, string, bool) (bool, error)
	LikeComment(context.Context, string) (bool, error)
	UnlikeComment(context.Context, string) (bool, error)
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
	composeCursor          int
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
	contentKind string
	contentID   string
	itemKey     string
	commentID   string
	voted       bool
	ok          bool
	err         error
}

type commentPostResult struct {
	itemKey  string
	targetID string
	content  string
	response map[string]any
	reply    bool
	err      error
}

type commentFocusPosition struct {
	id   string
	line int
}

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
			needsRender := model.loading || commentLoading || model.voting || model.commentSubmitting
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
	preservesCommentFocus := model.commentMode && (key == "j" || key == "k" || key == "J" || key == "K" || key == keyDown || key == keyUp || key == keyCtrlE || key == keyCtrlY || key == "e" || key == "v" || key == "\r")
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
	case "f":
		model.pageDownWithBoundary(ctx, maxInt(1, model.metrics.bodyHeight*7/8), "f", "f")
	case keyCtrlF:
		model.pageDownWithBoundary(ctx, maxInt(1, model.metrics.bodyHeight*7/8), keyCtrlF, "Ctrl-F")
	case "b":
		model.pageUpWithConfirmation(maxInt(1, model.metrics.bodyHeight*7/8))
	case keyCtrlB:
		model.pageUpWithBoundary(maxInt(1, model.metrics.bodyHeight*7/8), keyCtrlB, "Ctrl-B")
	case keyCtrlE:
		model.scrollViewportDown()
	case keyCtrlY:
		model.scrollViewportUp()
	case "\r":
		if model.commentMode {
			model.toggleFocusedCommentChildren(ctx)
		} else if !model.toggleFoldedGroup() {
			model.scrollDown(maxInt(1, model.metrics.bodyHeight/2))
		}
	case keyPageDown, "d":
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

package feedtui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const feedPageSize = 10

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type feedSource interface {
	GetFollowingFeed(context.Context, string, int) (map[string]any, error)
	GetComments(context.Context, string, string, int, int, string) (map[string]any, error)
}

type app struct {
	source         feedSource
	items          []feedItem
	index          int
	scroll         int
	width          int
	height         int
	nextURL        string
	end            bool
	loading        bool
	refreshing     bool
	err            error
	message        string
	messageUntil   time.Time
	showHelp       bool
	spinner        int
	generation     int
	metrics        layoutMetrics
	fetches        chan fetchResult
	commentMode    bool
	bodyScroll     int
	comments       map[string]*commentState
	commentFetches chan commentFetchResult
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
	if result.reset {
		model.items = nil
		model.index = 0
		model.scroll = 0
		model.end = false
		model.nextURL = ""
	}
	seen := make(map[string]struct{}, len(model.items)+len(newItems))
	for _, item := range model.items {
		seen[item.key] = struct{}{}
	}
	added := 0
	for _, item := range newItems {
		if _, exists := seen[item.key]; exists {
			continue
		}
		seen[item.key] = struct{}{}
		model.items = append(model.items, item)
		added++
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
	switch key {
	case "?":
		model.showHelp = true
	case "r":
		model.commentMode = false
		model.bodyScroll = 0
		model.scroll = 0
		model.startFetch(ctx, true)
		model.message = ""
	case "c":
		model.toggleComments(ctx)
	case "n", "l", keyRight:
		model.moveNext(ctx)
	case "p", "h", keyLeft:
		model.movePrevious(false)
	case "j", keyDown:
		model.lineDown(ctx)
	case "k", keyUp:
		model.lineUp()
	case " ", "f", keyPageDown, "\r":
		model.pageDown(ctx, maxInt(1, model.metrics.bodyHeight-1))
	case "b", keyPageUp:
		model.pageUp(maxInt(1, model.metrics.bodyHeight-1))
	case "d":
		model.pageDown(ctx, maxInt(1, model.metrics.bodyHeight/2))
	case "u":
		model.pageUp(maxInt(1, model.metrics.bodyHeight/2))
	case "g":
		if len(model.items) > 0 {
			model.commentMode = false
			model.bodyScroll = 0
			model.index, model.scroll = 0, 0
		}
	case "G":
		if len(model.items) > 0 {
			model.commentMode = false
			model.bodyScroll = 0
			model.index, model.scroll = len(model.items)-1, 0
		}
	case "o":
		model.openCurrent()
	}
	return false
}

func (model *app) lineDown(ctx context.Context) {
	if model.scroll < model.metrics.maxScroll {
		model.scroll++
		return
	}
	model.moveNext(ctx)
}

func (model *app) lineUp() {
	if model.scroll > 0 {
		model.scroll--
		return
	}
	model.movePrevious(true)
}

func (model *app) pageDown(ctx context.Context, amount int) {
	if model.scroll < model.metrics.maxScroll {
		model.scroll = minInt(model.metrics.maxScroll, model.scroll+amount)
		return
	}
	model.moveNext(ctx)
}

func (model *app) pageUp(amount int) {
	if model.scroll > 0 {
		model.scroll = maxInt(0, model.scroll-amount)
		return
	}
	model.movePrevious(true)
}

func (model *app) moveNext(ctx context.Context) {
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
	return writeFrame(out, lines, model.width, model.height)
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

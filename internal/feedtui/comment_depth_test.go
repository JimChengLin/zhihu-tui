package feedtui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCommentTreeStopsGrowingAfterSixReplyLevels(t *testing.T) {
	for _, depth := range []int{6, 7, 30} {
		t.Run(fmt.Sprintf("depth-%d", depth), func(t *testing.T) {
			state := deepCommentState(depth)
			view, _ := formatCommentView(feedItem{}, state, 0, 112)
			lines := layoutBodyLines(view, 112)
			id := deepCommentID(depth)
			body := deepCommentBodyLine(t, lines, id)
			if got := stringCellWidth(body.text); got != 21 {
				t.Fatalf("depth %d: body prefix width=%d, want 21: %#v", depth, got, body)
			}
			text := deepCommentVisibleText(lines, id)
			if depth == 6 {
				if strings.Contains(text, "第6层") || strings.Contains(text, "回复") {
					t.Fatalf("tree boundary became a flat reply: %q", text)
				}
			} else {
				for _, want := range []string{
					fmt.Sprintf("第%d层", depth),
					"回复" + deepCommentAuthor(depth-1),
				} {
					if !strings.Contains(text, want) {
						t.Fatalf("flat reply is missing %q: %q", want, text)
					}
				}
			}
			wantIDs := make([]string, 0, depth+1)
			for index := 0; index <= depth; index++ {
				wantIDs = append(wantIDs, deepCommentID(index))
			}
			if got := deepCommentOrder(lines); !reflect.DeepEqual(got, wantIDs) {
				t.Fatalf("comment order=%q, want %q", got, wantIDs)
			}
			deepCommentRequireWidth(t, lines, 112)
			if got := countLoadedComments(state.items); got != depth+1 {
				t.Fatalf("rendering changed the reply tree: loaded=%d", got)
			}
		})
	}
}

func TestDeepCommentLayoutAdaptsToReadingWidth(t *testing.T) {
	state := deepCommentState(30)
	for _, test := range []struct {
		width         int
		lastTreeDepth int
		prefixWidth   int
	}{
		{width: 36, lastTreeDepth: 2, prefixWidth: 9},
		{width: 80, lastTreeDepth: 5, prefixWidth: 18},
		{width: 112, lastTreeDepth: 6, prefixWidth: 21},
	} {
		t.Run(fmt.Sprintf("width-%d", test.width), func(t *testing.T) {
			view, _ := formatCommentView(feedItem{}, state, 0, test.width)
			lines := layoutBodyLines(view, test.width)
			for _, depth := range []int{test.lastTreeDepth, test.lastTreeDepth + 1, 30} {
				id := deepCommentID(depth)
				body := deepCommentBodyLine(t, lines, id)
				if got := stringCellWidth(body.text); got != test.prefixWidth {
					t.Fatalf("depth %d: body prefix width=%d, want %d", depth, got, test.prefixWidth)
				}
				text := deepCommentVisibleText(lines, id)
				level := fmt.Sprintf("第%d层", depth)
				if got := strings.Contains(text, level); got != (depth > test.lastTreeDepth) {
					t.Fatalf("depth %d: level annotation in %q, want flat=%v", depth, text, depth > test.lastTreeDepth)
				}
				if depth > test.lastTreeDepth && !strings.Contains(text, "回复"+deepCommentAuthor(depth-1)) {
					t.Fatalf("depth %d: reply target is missing: %q", depth, text)
				}
			}
			deepCommentRequireWidth(t, lines, test.width)
			if got := len(deepCommentOrder(lines)); got != 31 {
				t.Fatalf("narrow layout lost comments: got %d, want 31", got)
			}
		})
	}
}

func TestFlatRepliesKeepBranchesAndReturnToShallowSiblings(t *testing.T) {
	state := deepCommentState(9)
	parent := &state.items[0]
	for range 7 {
		parent = &parent.children[0]
	}
	parent.children = append(parent.children, feedComment{
		id:          "deep-sibling",
		author:      "DeepSibling",
		parentID:    parent.id,
		nestedReply: true,
		content:     "深层分叉正文",
	})
	state.items[0].children = append(state.items[0].children, feedComment{
		id:       "shallow-sibling",
		author:   "ShallowSibling",
		parentID: state.items[0].id,
		content:  "浅层兄弟正文",
	})
	state.items[0].childComments = countLoadedComments(state.items[0].children)
	view, _ := formatCommentView(feedItem{}, state, 0, 112)
	lines := layoutBodyLines(view, 112)
	wantIDs := make([]string, 0, 12)
	for depth := 0; depth <= 9; depth++ {
		wantIDs = append(wantIDs, deepCommentID(depth))
	}
	wantIDs = append(wantIDs, "deep-sibling", "shallow-sibling")
	if got := deepCommentOrder(lines); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("branched comment order=%q, want %q", got, wantIDs)
	}
	if text := deepCommentVisibleText(lines, "deep-sibling"); !strings.Contains(text, "第8层") || !strings.Contains(text, "回复"+deepCommentAuthor(7)) {
		t.Fatalf("deep sibling lost its actual parent or depth: %q", text)
	}
	shallow := firstLineWithCommentID(lines, "shallow-sibling")
	if shallow < 0 || !strings.Contains(styledLineText(lines[shallow]), "   └─ ShallowSibling") {
		t.Fatalf("shallow sibling did not return to the tree: %v", lines)
	}
	if text := deepCommentVisibleText(lines, "shallow-sibling"); strings.Contains(text, "第") {
		t.Fatalf("flat depth leaked into shallow sibling: %q", text)
	}
	gapCount := 0
	for _, line := range lines {
		if strings.Trim(styledLineText(line), " │├└─") != "" {
			continue
		}
		gapCount++
		if line.commentID != "" {
			t.Fatalf("reply separator inherited comment %q: %#v", line.commentID, line)
		}
	}
	if gapCount == 0 {
		t.Fatal("reply branches have no separator rows")
	}
	deepCommentRequireWidth(t, lines, 112)
}

func TestDeepCommentComposerAdaptsToReadingWidth(t *testing.T) {
	target := commentComposeTarget{
		commentID: deepCommentID(30),
		label:     "回复 " + strings.Repeat("长用户名", 20),
		indent:    90,
	}
	input := strings.Repeat("中文ab ", 20)
	model := &app{
		composeInput:  input,
		composeCursor: len(textUnits(input)) - 7,
		composeError:  strings.Repeat("提交失败", 20),
	}
	for _, test := range []struct {
		width       int
		prefixWidth int
	}{
		{width: 112, prefixWidth: 21},
		{width: 36, prefixWidth: 9},
		{width: 80, prefixWidth: 18},
		{width: 112, prefixWidth: 21},
	} {
		lines := inlineCommentComposerLines(model, target, test.width)
		if got := stringCellWidth(lines[0].text); got != test.prefixWidth {
			t.Fatalf("width %d: composer prefix=%d, want %d", test.width, got, test.prefixWidth)
		}
		deepCommentRequireWidth(t, lines, test.width)
		var rebuilt strings.Builder
		cursorCount, consumed := 0, 0
		for _, line := range lines[1 : len(lines)-2] {
			rebuilt.WriteString(line.middle)
			units := textUnits(line.middle)
			if line.hasCursor {
				cursorCount++
				withinLine := model.composeCursor - consumed
				if withinLine < 0 || withinLine > len(units) {
					t.Fatalf("width %d: cursor is attached to the wrong input line: %#v", test.width, line)
				}
				wantCell := stringCellWidth(line.text) + stringCellWidth(strings.Join(units[:withinLine], ""))
				if line.cursorCell != wantCell || line.cursorCell >= test.width {
					t.Fatalf("width %d: cursor cell=%d, want %d within width", test.width, line.cursorCell, wantCell)
				}
			}
			consumed += len(units)
		}
		if rebuilt.String() != input || cursorCount != 1 {
			t.Fatalf("width %d: input=%q cursor count=%d", test.width, rebuilt.String(), cursorCount)
		}
		for _, line := range lines {
			if line.commentID != target.commentID {
				t.Fatalf("composer lost reply target: %#v", line)
			}
		}
	}
}

func TestFlatReplyKeepsReplyAndCollapseTargets(t *testing.T) {
	state := deepCommentState(30)
	view, _ := formatCommentView(feedItem{}, state, 0, 80)
	lines := layoutBodyLines(view, 80)
	focusedID := deepCommentID(30)
	model := &app{
		items:             []feedItem{{key: "answer:42", id: "42", kind: "answer"}},
		comments:          map[string]*commentState{"answer:42": state},
		commentMode:       true,
		pageAnchorVisible: true,
		pageAnchorLine:    firstLineWithCommentID(lines, focusedID),
		metrics:           layoutMetrics{bodyHeight: 20, commentIDs: commentLineIDs(lines)},
	}
	model.handleKey(context.Background(), "w")
	if !model.composing || len(model.composeTargets) != 1 {
		t.Fatalf("flat reply did not open its composer: %#v", model.composeTargets)
	}
	target := model.composeTargets[0]
	if target.commentID != focusedID || target.label != "回复 "+deepCommentAuthor(30) || target.indent != 90 {
		t.Fatalf("flat display changed the actual reply target: %#v", target)
	}
	model.handleCommentComposerKey(context.Background(), keyEscape)
	model.handleKey(context.Background(), "e")
	if state.expandedChildren[deepCommentID(0)] {
		t.Fatal("e on a flat reply did not collapse its root")
	}
	if got, want := model.pageAnchorLine, firstLineWithCommentID(lines, deepCommentID(0)); got != want {
		t.Fatalf("collapsed focus=%d, want root at %d", got, want)
	}
	view, _ = formatCommentView(feedItem{}, state, 0, 80)
	if got := deepCommentOrder(layoutBodyLines(view, 80)); !reflect.DeepEqual(got, []string{deepCommentID(0)}) {
		t.Fatalf("collapsed replies remain selectable: %q", got)
	}
}

func deepCommentState(depth int) *commentState {
	var child feedComment
	for current := depth; current >= 0; current-- {
		comment := feedComment{
			id:          deepCommentID(current),
			author:      deepCommentAuthor(current),
			nestedReply: current > 0,
			content:     fmt.Sprintf("正文%02d", current),
		}
		if current > 0 {
			comment.parentID = deepCommentID(current - 1)
		}
		if current < depth {
			comment.children = []feedComment{child}
		}
		child = comment
	}
	child.childComments = depth
	return &commentState{
		items:            []feedComment{child},
		expandedChildren: map[string]bool{child.id: true},
		loaded:           true,
		end:              true,
	}
}

func deepCommentID(depth int) string {
	return fmt.Sprintf("depth-%02d", depth)
}

func deepCommentAuthor(depth int) string {
	return fmt.Sprintf("Author%02d", depth)
}

func deepCommentBodyLine(t *testing.T, lines []styledLine, id string) styledLine {
	t.Helper()
	for _, line := range lines {
		if line.commentID == id && strings.Contains(styledLineText(line), "正文") {
			return line
		}
	}
	t.Fatalf("comment %q lost its body", id)
	return styledLine{}
}

func deepCommentVisibleText(lines []styledLine, id string) string {
	var builder strings.Builder
	for _, line := range lines {
		if line.commentID == id {
			builder.WriteString(strings.TrimPrefix(styledLineText(line), line.text))
		}
	}
	return strings.ReplaceAll(builder.String(), " ", "")
}

func deepCommentOrder(lines []styledLine) []string {
	var ids []string
	seen := make(map[string]bool)
	for _, line := range lines {
		if line.commentID != "" && !seen[line.commentID] {
			ids = append(ids, line.commentID)
			seen[line.commentID] = true
		}
	}
	return ids
}

func deepCommentRequireWidth(t *testing.T, lines []styledLine, width int) {
	t.Helper()
	for _, line := range lines {
		if got := stringCellWidth(styledLineText(line)); got > width {
			t.Fatalf("line width=%d exceeds %d: %#v", got, width, line)
		}
	}
}

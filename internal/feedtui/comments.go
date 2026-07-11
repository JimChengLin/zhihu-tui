package feedtui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"zhihucli2/internal/display"
)

const commentPageSize = 20
const commentRelationshipWorkers = 4
const commentChildWorkers = 4
const commentStartMarker = "\ue000comment-start:"
const commentMarkerEnd = "\ue001"
const commentTreeMarker = "\ue002comment-tree:"

var commentPageTimeout = 10 * time.Second

type feedComment struct {
	id            string
	author        string
	authorToken   string
	replyTo       string
	parentID      string
	nestedReply   bool
	content       string
	isFollowing   bool
	isFollowed    bool
	voteCount     int
	childComments int
	createdAt     int64
	children      []feedComment
}

type commentState struct {
	items            []feedComment
	expandedChildren map[string]bool
	loading          bool
	loaded           bool
	end              bool
	nextCursor       string
	err              error
	moreErr          error
}

type commentFetchResult struct {
	key      string
	response map[string]any
	err      error
	append   bool
	cursor   string
}

type commentComposeTarget struct {
	commentID string
	label     string
	indent    int
}

func parseComments(data []any) []feedComment {
	comments := make([]feedComment, 0, len(data))
	for _, raw := range data {
		comment := parseComment(mapValue(raw))
		if comment.content != "" {
			comments = append(comments, comment)
		}
	}
	return comments
}

func parseComment(raw map[string]any) feedComment {
	author := mapValue(raw["author"])
	authorMember := mapValue(author["member"])
	replyTo := mapValue(raw["reply_to_author"])
	replyToMember := mapValue(replyTo["member"])
	replyToComment := mapValue(raw["reply_to_comment"])
	content, _ := contentText(toString(raw["content"]))
	relationship := author
	if len(authorMember) > 0 {
		relationship = authorMember
	}
	comment := feedComment{
		id:          toString(raw["id"]),
		authorToken: strings.TrimSpace(toString(relationship["url_token"])),
		author: firstNonEmpty(
			toString(authorMember["name"]),
			toString(author["name"]),
			toString(mapValue(raw["user"])["name"]),
			"匿名用户",
		),
		replyTo: firstNonEmpty(
			toString(replyToMember["name"]),
			toString(replyTo["name"]),
		),
		parentID: firstNonEmpty(
			toString(raw["reply_to_comment_id"]),
			toString(raw["reply_comment_id"]),
			toString(replyToComment["id"]),
		),
		content:       content,
		isFollowing:   truthy(relationship["is_following"]),
		isFollowed:    truthy(relationship["is_followed"]),
		voteCount:     int(toInt64(firstNonEmptyAny(raw["vote_count"], raw["like_count"], 0))),
		childComments: int(toInt64(raw["child_comment_count"])),
		createdAt:     toInt64(firstNonEmptyAny(raw["created_time"], raw["created"], 0)),
	}
	for _, childRaw := range asSlice(raw["child_comments"]) {
		child := parseComment(mapValue(childRaw))
		if child.content != "" {
			comment.children = append(comment.children, child)
		}
	}
	if len(comment.children) > 0 {
		comment.children = nestCommentReplies(comment.children, comment.id)
	}
	return comment
}

func formatCommentView(item feedItem, state *commentState, spinner int) (string, string) {
	label := "评论区"
	if item.commentCount > 0 {
		label += fmt.Sprintf(" · 共 %s 条", display.FormatCount(item.commentCount))
	}
	if state == nil || state.loading && !state.loaded {
		return spinnerFrames[spinner%len(spinnerFrames)] + " 正在加载评论…", label
	}
	if state.err != nil {
		return "评论加载失败：" + state.err.Error() + "\n\n按 c 返回正文，再按 c 重试。", label
	}
	if len(state.items) == 0 {
		return "", label
	}
	label += fmt.Sprintf(" · 已加载 %d 条", countLoadedComments(state.items))
	if state.loading {
		label += " · " + spinnerFrames[spinner%len(spinnerFrames)] + " 正在加载更多"
	} else if state.moreErr != nil {
		label += " · 加载更多失败，按 space 重试"
	} else if state.end {
		label += " · 已到底"
	}
	var builder strings.Builder
	for index, comment := range state.items {
		if index > 0 {
			writeCommentRootGap(&builder)
		}
		writeCommentMarker(&builder, comment.id)
		expanded := state.expandedChildren[comment.id]
		formatComment(&builder, comment, fmt.Sprintf("%d. ", index+1), "", false, "")
		if comment.childComments > 0 {
			builder.WriteString("\n")
			writeCommentMarker(&builder, comment.id)
			disclosure := "▸ "
			if expanded {
				disclosure = "▾ "
			}
			writeCommentTreeLine(&builder, "   "+disclosure, fmt.Sprintf("%d 条回复", comment.childComments))
		}
		if !expanded {
			continue
		}
		remaining := comment.childComments - countLoadedComments(comment.children)
		if len(comment.children) > 0 {
			writeCommentTreeGap(&builder, "   │  ")
		}
		formatCommentTree(&builder, comment.children, "   ", remaining > 0)
		if remaining > 0 {
			builder.WriteString("\n")
			writeCommentMarker(&builder, comment.id)
			writeCommentTreeLine(&builder, "   └─ ", fmt.Sprintf("还有 %d 条回复未加载", remaining))
		}
	}
	return builder.String(), label
}

func writeCommentRootGap(builder *strings.Builder) {
	for range 2 {
		builder.WriteString("\n")
		builder.WriteString(commentStartMarker)
		builder.WriteString(commentMarkerEnd)
		builder.WriteString("\n")
		writeCommentTreeLine(builder, "", "")
	}
	builder.WriteString("\n")
}

func formatCommentTree(builder *strings.Builder, comments []feedComment, prefix string, hasFollowing bool) {
	for index, comment := range comments {
		builder.WriteString("\n")
		writeCommentMarker(builder, comment.id)
		last := index == len(comments)-1 && !hasFollowing
		branch := "├─ "
		childPrefix := prefix + "│  "
		if last {
			branch = "└─ "
			childPrefix = prefix + "   "
		}
		formatComment(builder, comment, prefix+branch, childPrefix, true, "")
		if len(comment.children) > 0 {
			writeCommentTreeGap(builder, childPrefix+"│  ")
		}
		formatCommentTree(builder, comment.children, childPrefix, false)
		if index < len(comments)-1 || hasFollowing {
			writeCommentTreeGap(builder, prefix+"│  ")
		}
	}
}

func writeCommentTreeGap(builder *strings.Builder, prefix string) {
	builder.WriteString("\n")
	builder.WriteString(commentStartMarker)
	builder.WriteString(commentMarkerEnd)
	builder.WriteString("\n")
	writeCommentTreeLine(builder, prefix, "")
}

func writeCommentMarker(builder *strings.Builder, commentID string) {
	if commentID == "" {
		return
	}
	builder.WriteString(commentStartMarker)
	builder.WriteString(commentID)
	builder.WriteString(commentMarkerEnd)
	builder.WriteString("\n")
}

func commentPaging(response map[string]any) (string, bool) {
	paging := mapValue(response["paging"])
	nextURL := strings.TrimSpace(toString(paging["next"]))
	end := truthy(paging["is_end"]) || nextURL == ""
	nextCursor := ""
	if parsed, err := url.Parse(nextURL); err == nil {
		nextCursor = parsed.Query().Get("offset")
	}
	return nextCursor, end
}

func countLoadedComments(comments []feedComment) int {
	count := 0
	for _, comment := range comments {
		count += 1 + countLoadedComments(comment.children)
	}
	return count
}

func formatComment(builder *strings.Builder, comment feedComment, prefix, contentPrefix string, tree bool, replyMarker string) {
	var header strings.Builder
	header.WriteString(comment.author)
	if relationship := commentRelationshipLabel(comment); relationship != "" {
		header.WriteString("（")
		header.WriteString(relationship)
		header.WriteString("）")
	}
	if comment.replyTo != "" && !comment.nestedReply {
		header.WriteString(" 回复 ")
		header.WriteString(comment.replyTo)
	}
	meta := make([]string, 0, 3)
	if comment.voteCount > 0 {
		meta = append(meta, fmt.Sprintf("赞同 %d", comment.voteCount))
	}
	if comment.childComments > 0 && replyMarker != "" {
		meta = append(meta, fmt.Sprintf("%s回复 %d", replyMarker, comment.childComments))
	}
	if relative := formatRelativeTime(comment.createdAt, time.Now()); relative != "" {
		meta = append(meta, relative)
	}
	if len(meta) > 0 {
		header.WriteString("  ·  ")
		header.WriteString(strings.Join(meta, "  ·  "))
	}
	if tree {
		writeCommentTreeLine(builder, prefix, header.String())
	} else {
		builder.WriteString(prefix)
		builder.WriteString(header.String())
	}
	builder.WriteString("\n")
	for index, line := range strings.Split(comment.content, "\n") {
		if index > 0 {
			builder.WriteString("\n")
		}
		if tree {
			writeCommentTreeLine(builder, contentPrefix, line)
		} else {
			builder.WriteString(line)
		}
	}
}

func writeCommentTreeLine(builder *strings.Builder, prefix, text string) {
	builder.WriteString(commentTreeMarker)
	builder.WriteString(prefix)
	builder.WriteString(commentMarkerEnd)
	builder.WriteString(text)
}

func commentRelationshipLabel(comment feedComment) string {
	switch {
	case comment.isFollowing && comment.isFollowed:
		return "互相关注"
	case comment.isFollowing:
		return "我关注"
	case comment.isFollowed:
		return "关注我"
	default:
		return ""
	}
}

func findCommentByID(state *commentState, commentID string) (feedComment, bool) {
	if state == nil {
		return feedComment{}, false
	}
	var find func([]feedComment) (feedComment, bool)
	find = func(comments []feedComment) (feedComment, bool) {
		for _, comment := range comments {
			if comment.id == commentID {
				return comment, true
			}
			if child, found := find(comment.children); found {
				return child, true
			}
		}
		return feedComment{}, false
	}
	return find(state.items)
}

func commentDepthByID(state *commentState, commentID string) int {
	if state == nil {
		return 0
	}
	var find func([]feedComment, int) (int, bool)
	find = func(comments []feedComment, depth int) (int, bool) {
		for _, comment := range comments {
			if comment.id == commentID {
				return depth, true
			}
			if childDepth, found := find(comment.children, depth+1); found {
				return childDepth, true
			}
		}
		return 0, false
	}
	depth, _ := find(state.items, 0)
	return depth
}

type commentAuthorRelationshipSource interface {
	GetUserProfile(context.Context, string) (map[string]any, error)
}

type commentChildSource interface {
	GetChildComments(context.Context, string, int, int) (map[string]any, error)
}

type commentRelation struct {
	isFollowing bool
	isFollowed  bool
	known       bool
}

type commentRelationFetchResult struct {
	tokens    []string
	relations map[string]commentRelation
}

func fetchCommentRelations(ctx context.Context, source commentAuthorRelationshipSource, tokens []string) map[string]commentRelation {
	jobs := make(chan string, len(tokens))
	type result struct {
		token    string
		relation commentRelation
	}
	results := make(chan result, len(tokens))
	for _, token := range tokens {
		jobs <- token
	}
	close(jobs)
	workers := minInt(commentRelationshipWorkers, len(tokens))
	for range workers {
		go func() {
			for token := range jobs {
				profile, err := source.GetUserProfile(ctx, token)
				relation := commentRelation{}
				if err == nil {
					relation = commentRelation{
						isFollowing: truthy(profile["is_following"]),
						isFollowed:  truthy(profile["is_followed"]),
						known:       true,
					}
				}
				results <- result{token: token, relation: relation}
			}
		}()
	}
	relations := make(map[string]commentRelation, len(tokens))
	for range tokens {
		fetched := <-results
		relations[fetched.token] = fetched.relation
	}
	return relations
}

func commentTokens(comments []feedComment) []string {
	seen := make(map[string]struct{})
	var tokens []string
	var collect func([]feedComment)
	collect = func(items []feedComment) {
		for _, comment := range items {
			if comment.authorToken != "" {
				if _, duplicate := seen[comment.authorToken]; !duplicate {
					seen[comment.authorToken] = struct{}{}
					tokens = append(tokens, comment.authorToken)
				}
			}
			collect(comment.children)
		}
	}
	collect(comments)
	return tokens
}

func applyCommentRelations(comments []feedComment, relations map[string]commentRelation) {
	for index := range comments {
		comment := &comments[index]
		if relation, found := relations[comment.authorToken]; found && relation.known {
			comment.isFollowing = relation.isFollowing
			comment.isFollowed = relation.isFollowed
		}
		applyCommentRelations(comment.children, relations)
	}
}

type commentChildFetchResult struct {
	stateKey string
	rootIDs  []string
	children map[string][]feedComment
}

func fetchCommentChildren(ctx context.Context, source commentChildSource, rootIDs []string) map[string][]feedComment {
	jobs := make(chan string, len(rootIDs))
	type result struct {
		rootID   string
		children []feedComment
		ok       bool
	}
	results := make(chan result, len(rootIDs))
	for _, rootID := range rootIDs {
		jobs <- rootID
	}
	close(jobs)
	workers := minInt(commentChildWorkers, len(rootIDs))
	for range workers {
		go func() {
			for rootID := range jobs {
				response, err := source.GetChildComments(ctx, rootID, 0, commentPageSize)
				comments := parseComments(asSlice(response["data"]))
				if err == nil {
					comments = nestCommentReplies(comments, rootID)
				}
				results <- result{
					rootID:   rootID,
					children: comments,
					ok:       err == nil,
				}
			}
		}()
	}
	children := make(map[string][]feedComment, len(rootIDs))
	for range rootIDs {
		fetched := <-results
		if fetched.ok {
			children[fetched.rootID] = fetched.children
		}
	}
	return children
}

func nestCommentReplies(comments []feedComment, rootID string) []feedComment {
	type node struct {
		comment  feedComment
		children []*node
	}
	nodes := make([]node, len(comments))
	byID := make(map[string]*node, len(comments))
	for index := range comments {
		nodes[index].comment = comments[index]
		byID[comments[index].id] = &nodes[index]
	}
	roots := make([]*node, 0, len(comments))
	for index := range nodes {
		current := &nodes[index]
		parent := byID[current.comment.parentID]
		if current.comment.parentID == "" || current.comment.parentID == rootID || parent == nil {
			roots = append(roots, current)
			continue
		}
		current.comment.nestedReply = true
		parent.children = append(parent.children, current)
	}
	var materialize func(*node) feedComment
	materialize = func(current *node) feedComment {
		comment := current.comment
		children := append([]feedComment(nil), comment.children...)
		for _, child := range current.children {
			children = append(children, materialize(child))
		}
		comment.children = children
		return comment
	}
	result := make([]feedComment, 0, len(roots))
	for _, root := range roots {
		result = append(result, materialize(root))
	}
	return result
}

func supportsComments(item feedItem) bool {
	if item.id == "" {
		return false
	}
	switch item.kind {
	case "answer", "article", "pin", "question":
		return true
	default:
		return false
	}
}

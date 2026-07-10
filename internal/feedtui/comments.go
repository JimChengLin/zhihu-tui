package feedtui

import (
	"fmt"
	"strings"
	"time"

	"zhihucli2/internal/display"
)

const commentPageSize = 20

type feedComment struct {
	author        string
	replyTo       string
	content       string
	voteCount     int
	childComments int
	createdAt     int64
}

type commentState struct {
	items   []feedComment
	loading bool
	loaded  bool
	err     error
}

type commentFetchResult struct {
	key      string
	response map[string]any
	err      error
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
	content, _ := contentText(toString(raw["content"]))
	return feedComment{
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
		content:       content,
		voteCount:     int(toInt64(firstNonEmptyAny(raw["vote_count"], raw["like_count"], 0))),
		childComments: int(toInt64(raw["child_comment_count"])),
		createdAt:     toInt64(firstNonEmptyAny(raw["created_time"], raw["created"], 0)),
	}
}

func formatCommentView(item feedItem, state *commentState, spinner int) (string, string) {
	label := "评论区"
	if item.commentCount > 0 {
		label += fmt.Sprintf(" · 共 %s 条", display.FormatCount(item.commentCount))
	}
	if state == nil || state.loading {
		return spinnerFrames[spinner%len(spinnerFrames)] + " 正在加载评论…", label
	}
	if state.err != nil {
		return "评论加载失败：" + state.err.Error() + "\n\n按 c 返回正文，再按 c 重试。", label
	}
	if len(state.items) == 0 {
		return "暂无评论。按 c 返回正文。", label
	}
	label += fmt.Sprintf(" · 已加载 %d 条", len(state.items))
	var builder strings.Builder
	for index, comment := range state.items {
		if index > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(fmt.Sprintf("%d. %s", index+1, comment.author))
		if comment.replyTo != "" {
			builder.WriteString(" 回复 ")
			builder.WriteString(comment.replyTo)
		}
		meta := make([]string, 0, 3)
		if comment.voteCount > 0 {
			meta = append(meta, fmt.Sprintf("赞同 %d", comment.voteCount))
		}
		if comment.childComments > 0 {
			meta = append(meta, fmt.Sprintf("回复 %d", comment.childComments))
		}
		if relative := formatRelativeTime(comment.createdAt, time.Now()); relative != "" {
			meta = append(meta, relative)
		}
		if len(meta) > 0 {
			builder.WriteString("  ·  ")
			builder.WriteString(strings.Join(meta, "  ·  "))
		}
		builder.WriteString("\n")
		builder.WriteString(comment.content)
	}
	return builder.String(), label
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

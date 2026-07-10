package feedtui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"zhihucli2/internal/display"
)

var htmlBreakPattern = regexp.MustCompile(`(?i)<(?:br\s*/?|/?(?:p|div|li|blockquote|h[1-6]))[^>]*>`)
var repeatedBlankLinesPattern = regexp.MustCompile(`\n[\t ]*\n(?:[\t ]*\n)+`)

type feedItem struct {
	key       string
	kind      string
	action    string
	title     string
	author    string
	headline  string
	body      string
	stats     string
	createdAt int64
	url       string
	hasImage  bool
}

func parseFeedItems(data []any) []feedItem {
	items := make([]feedItem, 0, len(data))
	for _, raw := range data {
		item, ok := parseFeedItem(mapValue(raw))
		if ok {
			items = append(items, item)
		}
	}
	return items
}

func parseFeedItem(raw map[string]any) (feedItem, bool) {
	target := mapValue(raw["target"])
	if len(target) == 0 {
		return feedItem{}, false
	}

	kind := strings.TrimSpace(toString(target["type"]))
	id := strings.TrimSpace(toString(target["id"]))
	question := mapValue(target["question"])
	author := mapValue(target["author"])
	actor := mapValue(raw["actor"])
	title := plainText(firstNonEmpty(
		toString(target["title"]),
		toString(question["title"]),
		toString(target["name"]),
	))
	body := plainText(firstNonEmpty(
		toString(target["content"]),
		toString(target["excerpt_new"]),
		toString(target["excerpt"]),
		toString(target["detail"]),
	))
	if title == "" {
		title = firstNonEmpty(firstParagraph(body), typeLabel(kind), "一条关注动态")
		if title == firstParagraph(body) {
			body = strings.TrimSpace(strings.TrimPrefix(body, title))
		}
	}

	authorName := firstNonEmpty(toString(author["name"]), toString(actor["name"]), "匿名用户")
	action := normalizeAction(toString(raw["action_text"]))
	if action == "" {
		action = formatVerb(toString(raw["verb"]), firstNonEmpty(toString(actor["name"]), authorName))
	}
	if action == "" {
		action = typeLabel(kind)
	}

	createdAt := toInt64(firstNonEmptyAny(raw["created_time"], target["created_time"], target["created"], 0))
	url := feedItemURL(kind, id, toString(question["id"]), toString(target["url"]))
	key := kind + ":" + id
	if id == "" {
		key = toString(raw["id"])
	}
	if key == "" {
		key = title + ":" + authorName
	}

	return feedItem{
		key:       key,
		kind:      kind,
		action:    action,
		title:     title,
		author:    authorName,
		headline:  compactLine(plainText(toString(author["headline"]))),
		body:      body,
		stats:     feedStats(target),
		createdAt: createdAt,
		url:       url,
		hasImage:  strings.Contains(strings.ToLower(toString(target["content"])), "<img"),
	}, true
}

func plainText(value string) string {
	if value == "" {
		return ""
	}
	value = htmlBreakPattern.ReplaceAllString(value, "\n")
	value = display.StripHTML(value)
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	value = strings.TrimSpace(strings.Join(lines, "\n"))
	return repeatedBlankLinesPattern.ReplaceAllString(value, "\n\n")
}

func firstParagraph(value string) string {
	if before, _, ok := strings.Cut(value, "\n"); ok {
		return strings.TrimSpace(before)
	}
	return strings.TrimSpace(value)
}

func compactLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func normalizeAction(value string) string {
	value = compactLine(plainText(value))
	for _, verb := range []string{
		"赞同了回答", "收藏了回答", "回答了问题", "关注了问题", "关注了话题",
		"发布了文章", "赞同了文章", "收藏了文章", "发布了想法", "赞同了想法",
	} {
		prefix := strings.TrimSpace(strings.TrimSuffix(value, verb))
		if prefix != value && prefix != "" {
			return prefix + " " + verb
		}
	}
	return value
}

func feedStats(target map[string]any) string {
	parts := make([]string, 0, 3)
	if value, ok := firstPresent(target["voteup_count"], mapValue(mapValue(target["reaction"])["statistics"])["like_count"], target["like_count"]); ok {
		parts = append(parts, "赞同 "+display.FormatCount(value))
	}
	if value, ok := firstPresent(target["comment_count"]); ok {
		parts = append(parts, "评论 "+display.FormatCount(value))
	}
	if value, ok := firstPresent(target["favorite_count"], target["favlists_count"], mapValue(mapValue(target["reaction"])["statistics"])["favorites"]); ok {
		parts = append(parts, "收藏 "+display.FormatCount(value))
	}
	return strings.Join(parts, "  ·  ")
}

func feedItemURL(kind, id, questionID, apiURL string) string {
	if id == "" {
		if strings.HasPrefix(apiURL, "https://www.zhihu.com/") || strings.HasPrefix(apiURL, "https://zhuanlan.zhihu.com/") {
			return apiURL
		}
		return ""
	}
	switch kind {
	case "answer":
		if questionID != "" {
			return "https://www.zhihu.com/question/" + questionID + "/answer/" + id
		}
		return "https://www.zhihu.com/answer/" + id
	case "article":
		return "https://zhuanlan.zhihu.com/p/" + id
	case "pin":
		return "https://www.zhihu.com/pin/" + id
	case "question":
		return "https://www.zhihu.com/question/" + id
	default:
		if strings.HasPrefix(apiURL, "https://www.zhihu.com/") || strings.HasPrefix(apiURL, "https://zhuanlan.zhihu.com/") {
			return apiURL
		}
		return ""
	}
}

func typeLabel(kind string) string {
	switch kind {
	case "answer":
		return "回答"
	case "article":
		return "文章"
	case "pin":
		return "想法"
	case "question":
		return "问题"
	case "column":
		return "专栏"
	case "collection":
		return "收藏夹"
	default:
		return "关注动态"
	}
}

func formatVerb(verb, actor string) string {
	label := ""
	switch verb {
	case "ANSWER_CREATE":
		label = "回答了问题"
	case "ANSWER_VOTE_UP":
		label = "赞同了回答"
	case "MEMBER_CREATE_ARTICLE":
		label = "发布了文章"
	case "MEMBER_VOTEUP_ARTICLE":
		label = "赞同了文章"
	case "QUESTION_FOLLOW":
		label = "关注了问题"
	case "TOPIC_FOLLOW":
		label = "关注了话题"
	case "MEMBER_CREATE_PIN":
		label = "发布了想法"
	}
	if label == "" {
		return ""
	}
	if actor == "" {
		return label
	}
	return actor + " " + label
}

func formatRelativeTime(timestamp int64, now time.Time) string {
	if timestamp <= 0 {
		return ""
	}
	when := time.Unix(timestamp, 0)
	delta := now.Sub(when)
	switch {
	case delta < 0:
		return when.Format("01-02 15:04")
	case delta < time.Minute:
		return "刚刚"
	case delta < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(delta/time.Minute))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(delta/time.Hour))
	case now.Year() == when.Year():
		return when.Format("01-02 15:04")
	default:
		return when.Format("2006-01-02")
	}
}

func mapValue(value any) map[string]any {
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return map[string]any{}
}

func asSlice(value any) []any {
	if items, ok := value.([]any); ok {
		return items
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyAny(values ...any) any {
	for _, value := range values {
		if toString(value) != "" && toString(value) != "0" {
			return value
		}
	}
	return values[len(values)-1]
}

func firstPresent(values ...any) (any, bool) {
	for _, value := range values {
		if value != nil && toString(value) != "" {
			return value, true
		}
	}
	return nil, false
}

func toString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return fmt.Sprint(typed)
	}
}

func toInt64(value any) int64 {
	number, _ := strconv.ParseInt(toString(value), 10, 64)
	return number
}

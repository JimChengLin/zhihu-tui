package feedtui

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/JimChengLin/zhihu-tui/internal/display"
)

var htmlBreakPattern = regexp.MustCompile(`(?i)<(?:br\s*/?|/?(?:p|div|li|blockquote|h[1-6]))[^>]*>`)
var repeatedBlankLinesPattern = regexp.MustCompile(`\n[\t ]*\n(?:[\t ]*\n)+`)
var imageTagPattern = regexp.MustCompile(`(?is)<img\b[^>]*>`)
var codeBlockPattern = regexp.MustCompile(`(?is)<pre\b[^>]*>(.*?)</pre\s*>`)
var classCodeBlockPattern = regexp.MustCompile(`(?is)<code\b[^>]*\bclass\s*=\s*(?:"[^"]*"|'[^']*')[^>]*>(.*?)</code\s*>`)
var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)
var pinTitlePattern = regexp.MustCompile(`(?is)^\s*([^<\r\n]+?)\s*<br\s*/?>\s*<p(?:\s[^>]*)?>`)

const (
	codeBlockStartMarker   = "\ue000code-block-start\ue001"
	codeBlockEndMarker     = "\ue000code-block-end\ue001"
	linkCardTitleMarker    = "\ue000link-card-title\ue001"
	linkCardQuoteMarker    = "\ue000link-card-quote\ue001"
	linkCardExcerptMarker  = "\ue000link-card-excerpt\ue001"
	linkCardMetadataMarker = "\ue000link-card-metadata\ue001"
)

type feedItem struct {
	key             string
	id              string
	kind            string
	action          string
	title           string
	pinTitle        string
	author          string
	headline        string
	body            string
	stats           string
	createdAt       int64
	url             string
	imageCount      int
	commentCount    int
	hasCommentCount bool
	voteCount       int64
	hasVoteCount    bool
	voted           bool
	serverFolded    bool
	foldedItems     []feedItem
	foldedParent    string
	groupOpen       bool
}

func parseFeedItems(data []any) []feedItem {
	items := make([]feedItem, 0, len(data))
	for _, raw := range data {
		activity := mapValue(raw)
		item, ok := parseFeedItem(activity)
		if ok {
			items = append(items, item)
		}
		groupedItems := make([]feedItem, 0, len(asSlice(activity["list"])))
		for _, groupedRaw := range asSlice(activity["list"]) {
			grouped, ok := parseFeedItem(mapValue(groupedRaw))
			if !ok {
				continue
			}
			grouped.serverFolded = true
			groupedItems = append(groupedItems, grouped)
		}
		if len(groupedItems) > 0 {
			items = append(items, foldedGroupItem(activity, groupedItems))
		}
	}
	return items
}

func foldedGroupItem(raw map[string]any, children []feedItem) feedItem {
	key := stableFoldedGroupKey(toString(raw["id"]))
	if key == "" {
		key = "folded:" + children[0].key
	}
	title := plainText(toString(raw["group_text"]))
	title = strings.ReplaceAll(title, "{LIST_COUNT}", strconv.Itoa(len(children)))
	if title == "" {
		title = fmt.Sprintf("还有 %d 条动态被知乎收起", len(children))
	}
	for index := range children {
		children[index].foldedParent = key
	}
	return feedItem{
		key:         key,
		kind:        "folded_group",
		title:       title,
		foldedItems: children,
	}
}

func stableFoldedGroupKey(rawID string) string {
	rawID = strings.TrimSpace(rawID)
	parts := strings.Split(rawID, "_")
	if len(parts) == 4 {
		return "folded:" + parts[2] + ":" + parts[3]
	}
	return rawID
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
	pinTitle := ""
	if kind == "pin" {
		pinTitle = firstNonEmpty(title, pinContentTitle(target["content"]))
	}
	body, imageCount := feedContentText(target["content"])
	if body == "" {
		body = plainText(firstNonEmpty(
			toString(target["excerpt_new"]),
			toString(target["excerpt"]),
			toString(target["detail"]),
		))
	}
	if referenced := referencedImageCount(raw, target); referenced > imageCount {
		body = appendImagePlaceholders(body, imageCount+1, referenced)
		imageCount = referenced
	}
	if pinTitle != "" && firstParagraph(body) == pinTitle {
		body = strings.TrimSpace(strings.TrimPrefix(body, pinTitle))
		title = pinTitle
	}
	if title == "" {
		bodyTitle := firstParagraph(body)
		if strings.HasPrefix(bodyTitle, "▣ 图片 ") {
			bodyTitle = ""
		}
		title = firstNonEmpty(bodyTitle, typeLabel(kind), "一条关注动态")
		if title == firstParagraph(body) && kind != "pin" {
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
	if actionIdentity := normalizeAction(toString(raw["action_text"])); actionIdentity != "" {
		key += ":" + actionIdentity
	}
	if key == ":" {
		key = strings.TrimSpace(toString(raw["id"]))
	}
	if key == "" {
		key = title + ":" + authorName
	}

	voteCount, hasVoteCount := feedVoteCount(target)
	commentCount, hasCommentCount := firstPresent(target["comment_count"])
	return feedItem{
		key:             key,
		id:              id,
		kind:            kind,
		action:          action,
		title:           title,
		pinTitle:        pinTitle,
		author:          authorName,
		headline:        compactLine(plainText(toString(author["headline"]))),
		body:            body,
		stats:           feedStats(target),
		createdAt:       createdAt,
		url:             url,
		imageCount:      imageCount,
		commentCount:    int(toInt64(commentCount)),
		hasCommentCount: hasCommentCount,
		voteCount:       voteCount,
		hasVoteCount:    hasVoteCount,
		voted:           feedItemVoted(target),
	}, true
}

func pinContentTitle(value any) string {
	content := ""
	if text, ok := value.(string); ok {
		content = text
	} else {
		for _, rawNode := range asSlice(value) {
			node := mapValue(rawNode)
			if strings.EqualFold(toString(node["type"]), "text") {
				content = toString(node["content"])
				break
			}
		}
	}
	match := pinTitlePattern.FindStringSubmatch(content)
	if len(match) != 2 {
		return ""
	}
	return plainText(match[1])
}

func contentText(value string) (string, int) {
	return contentTextFrom(value, 0)
}

func contentTextFrom(value string, previousImages int) (string, int) {
	imageCount := 0
	for _, pattern := range []*regexp.Regexp{codeBlockPattern, classCodeBlockPattern} {
		value = pattern.ReplaceAllStringFunc(value, func(block string) string {
			match := pattern.FindStringSubmatch(block)
			code := htmlBreakPattern.ReplaceAllString(match[1], "\n")
			code = htmlTagPattern.ReplaceAllString(code, "")
			code = html.UnescapeString(strings.ReplaceAll(code, "\r\n", "\n"))
			code = strings.Trim(code, "\n\r")
			return "\n" + codeBlockStartMarker + "\n" + code + "\n" + codeBlockEndMarker + "\n"
		})
	}
	value = imageTagPattern.ReplaceAllStringFunc(value, func(string) string {
		imageCount++
		return fmt.Sprintf("\n▣ 图片 %d\n", previousImages+imageCount)
	})
	return plainText(value), imageCount
}

func feedContentText(value any) (string, int) {
	if text, ok := value.(string); ok {
		return contentText(text)
	}
	parts := make([]string, 0)
	imageCount := 0
	for _, rawNode := range asSlice(value) {
		node := mapValue(rawNode)
		switch strings.ToLower(toString(node["type"])) {
		case "image":
			imageCount++
			parts = append(parts, fmt.Sprintf("▣ 图片 %d", imageCount))
		case "link_card":
			parts = append(parts, formatLinkCard(node))
		default:
			text, nestedImages := contentTextFrom(toString(node["content"]), imageCount)
			if text != "" {
				parts = append(parts, text)
			}
			imageCount += nestedImages
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), imageCount
}

func hydrateFeedLinkCards(ctx context.Context, source linkCardSource, response map[string]any) {
	type cardRef struct {
		kind string
		id   string
	}
	nodesByRef := make(map[cardRef][]map[string]any)
	var collectActivity func(map[string]any)
	collectActivity = func(activity map[string]any) {
		target := mapValue(activity["target"])
		for _, rawNode := range asSlice(target["content"]) {
			node := mapValue(rawNode)
			if !strings.EqualFold(toString(node["type"]), "link_card") {
				continue
			}
			kind := strings.ToUpper(strings.TrimSpace(toString(node["data_content_type"])))
			if kind != "PIN" && kind != "ANSWER" && kind != "ARTICLE" {
				continue
			}
			id := linkCardContentID(node, kind)
			if id != "" {
				ref := cardRef{kind: kind, id: id}
				nodesByRef[ref] = append(nodesByRef[ref], node)
			}
		}
		for _, rawChild := range asSlice(activity["list"]) {
			collectActivity(mapValue(rawChild))
		}
	}
	for _, rawActivity := range asSlice(response["data"]) {
		collectActivity(mapValue(rawActivity))
	}

	type result struct {
		ref    cardRef
		detail map[string]any
		err    error
	}
	results := make(chan result, len(nodesByRef))
	for ref := range nodesByRef {
		go func() {
			var detail map[string]any
			var err error
			if ref.kind == "ANSWER" {
				detail, err = source.GetAnswer(ctx, ref.id)
			} else if ref.kind == "ARTICLE" {
				detail, err = source.GetArticle(ctx, ref.id)
			} else {
				detail, err = source.GetPin(ctx, ref.id)
			}
			results <- result{ref: ref, detail: detail, err: err}
		}()
	}
	for range nodesByRef {
		result := <-results
		for _, node := range nodesByRef[result.ref] {
			if result.err != nil {
				node["card_error"] = result.err.Error()
				continue
			}
			node["card_detail"] = result.detail
		}
	}
}

func linkCardContentID(node map[string]any, kind string) string {
	id := strings.TrimSpace(toString(node["data_content_id"]))
	if kind != "ANSWER" && kind != "ARTICLE" {
		return id
	}
	parsed, err := url.Parse(toString(node["url"]))
	if err != nil {
		return id
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for index := range parts {
		if kind == "ANSWER" && parts[index] == "answer" && index+1 < len(parts) {
			return parts[index+1]
		}
		if kind == "ARTICLE" && parts[index] == "p" && index+1 < len(parts) {
			return parts[index+1]
		}
	}
	return id
}

func formatLinkCard(node map[string]any) string {
	detail := mapValue(node["card_detail"])
	kind := strings.ToUpper(strings.TrimSpace(toString(node["data_content_type"])))
	if kind == "ANSWER" {
		return formatAnswerLinkCard(node, detail)
	}
	if kind == "ARTICLE" {
		return formatArticleLinkCard(node, detail)
	}
	title := firstNonEmpty(pinLinkCardTitle(detail), toString(node["data_draft_title"]))
	if title == "引用想法" {
		title = ""
	}
	lines := make([]string, 0, 3)
	if title != "" {
		if toString(node["card_error"]) != "" {
			title += "（详情加载失败）"
		}
		lines = append(lines, linkCardTitleMarker+"↳ "+title)
	} else if toString(node["card_error"]) != "" {
		lines = append(lines, linkCardTitleMarker+"↳ 引用想法（详情加载失败）")
	}
	if excerpt := pinLinkCardExcerpt(detail); excerpt != "" {
		if title == "" && toString(node["card_error"]) == "" {
			lines = append(lines, linkCardQuoteMarker+"↳ "+excerpt)
		} else {
			lines = append(lines, linkCardExcerptMarker+excerpt)
		}
	}
	if stats := linkCardStats(detail); stats != "" {
		lines = append(lines, linkCardMetadataMarker+stats+"  ·  想法")
	}
	return strings.Join(lines, "\n")
}

func formatAnswerLinkCard(node, detail map[string]any) string {
	title := firstNonEmpty(toString(mapValue(detail["question"])["title"]), toString(node["data_draft_title"]), "引用回答")
	if toString(node["card_error"]) != "" {
		title += "（详情加载失败）"
	}
	lines := []string{linkCardTitleMarker + "↳ " + plainText(title)}
	if excerpt := linkCardExcerpt(detail); excerpt != "" {
		lines = append(lines, linkCardExcerptMarker+excerpt)
	}
	metadata := make([]string, 0, 3)
	if author := strings.TrimSpace(toString(mapValue(detail["author"])["name"])); author != "" {
		metadata = append(metadata, author)
	}
	if stats := linkCardStats(detail); stats != "" {
		metadata = append(metadata, stats)
	}
	metadata = append(metadata, "回答")
	lines = append(lines, linkCardMetadataMarker+strings.Join(metadata, "  ·  "))
	return strings.Join(lines, "\n")
}

func formatArticleLinkCard(node, detail map[string]any) string {
	title := firstNonEmpty(toString(detail["title"]), toString(node["data_draft_title"]), "引用文章")
	if toString(node["card_error"]) != "" {
		title += "（详情加载失败）"
	}
	lines := []string{linkCardTitleMarker + "↳ " + plainText(title)}
	if excerpt := linkCardExcerpt(detail); excerpt != "" {
		lines = append(lines, linkCardExcerptMarker+excerpt)
	}
	metadata := make([]string, 0, 3)
	if author := strings.TrimSpace(toString(mapValue(detail["author"])["name"])); author != "" {
		metadata = append(metadata, author)
	}
	if stats := linkCardStats(detail); stats != "" {
		metadata = append(metadata, stats)
	}
	metadata = append(metadata, "文章")
	lines = append(lines, linkCardMetadataMarker+strings.Join(metadata, "  ·  "))
	return strings.Join(lines, "\n")
}

func linkCardExcerpt(detail map[string]any) string {
	value := firstNonEmpty(toString(detail["excerpt_new"]), toString(detail["excerpt"]), toString(detail["content"]))
	return truncateCells(compactLine(plainText(value)), 512)
}

func pinLinkCardTitle(detail map[string]any) string {
	content := pinLinkCardContent(detail)
	before, _, found := strings.Cut(content, " | ")
	if !found {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(firstParagraph(plainText(before)), "|"))
}

func pinLinkCardExcerpt(detail map[string]any) string {
	content := pinLinkCardContent(detail)
	if _, after, found := strings.Cut(content, " | "); found {
		content = after
	}
	return truncateCells(compactLine(plainText(content)), 512)
}

func pinLinkCardContent(detail map[string]any) string {
	for _, rawNode := range asSlice(detail["content"]) {
		node := mapValue(rawNode)
		if strings.EqualFold(toString(node["type"]), "text") {
			return toString(node["content"])
		}
	}
	return toString(detail["excerpt_title"])
}

func linkCardStats(detail map[string]any) string {
	parts := make([]string, 0, 3)
	if value, ok := firstPresent(detail["voteup_count"], detail["like_count"], mapValue(mapValue(detail["reaction"])["statistics"])["up_vote_count"]); ok {
		parts = append(parts, "赞同 "+display.FormatCount(value))
	}
	if value, ok := firstPresent(detail["favorite_count"], detail["favlists_count"]); ok {
		parts = append(parts, "收藏 "+display.FormatCount(value))
	}
	if value, ok := firstPresent(detail["comment_count"]); ok {
		parts = append(parts, "评论 "+display.FormatCount(value))
	}
	return strings.Join(parts, "  ·  ")
}

func referencedImageCount(raw, target map[string]any) int {
	count := 0
	for _, key := range []string{"thumbnail", "image_url"} {
		if toString(target[key]) != "" || toString(raw[key]) != "" {
			count = maxInt(count, 1)
		}
	}
	count = maxInt(count, len(asSlice(target["content_img"])))
	childrenWithImages := 0
	for _, child := range asSlice(raw["children"]) {
		if toString(mapValue(child)["thumbnail"]) != "" {
			childrenWithImages++
		}
	}
	return maxInt(count, childrenWithImages)
}

func appendImagePlaceholders(body string, first, last int) string {
	if first > last {
		return body
	}
	var placeholders []string
	for index := first; index <= last; index++ {
		placeholders = append(placeholders, fmt.Sprintf("▣ 图片 %d", index))
	}
	if body == "" {
		return strings.Join(placeholders, "\n\n")
	}
	return body + "\n\n" + strings.Join(placeholders, "\n\n")
}

func plainText(value string) string {
	if value == "" {
		return ""
	}
	value = htmlBreakPattern.ReplaceAllString(value, "\n")
	value = display.StripHTML(value)
	value = stripSpuriousZWNJ(value)
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	inCodeBlock := false
	for i := range lines {
		switch lines[i] {
		case codeBlockStartMarker:
			inCodeBlock = true
		case codeBlockEndMarker:
			inCodeBlock = false
		default:
			if inCodeBlock {
				lines[i] = strings.TrimRight(lines[i], " \t")
			} else {
				lines[i] = strings.Join(strings.Fields(lines[i]), " ")
			}
		}
	}
	value = strings.TrimSpace(strings.Join(lines, "\n"))
	return repeatedBlankLinesPattern.ReplaceAllString(value, "\n\n")
}

func stripSpuriousZWNJ(value string) string {
	if !strings.ContainsRune(value, '\u200c') {
		return value
	}
	runes := []rune(value)
	var cleaned strings.Builder
	cleaned.Grow(len(value))
	for index, current := range runes {
		if current == '\u200c' && !hasArabicNeighbor(runes, index) {
			continue
		}
		cleaned.WriteRune(current)
	}
	return cleaned.String()
}

func hasArabicNeighbor(runes []rune, index int) bool {
	return index > 0 && unicode.Is(unicode.Arabic, runes[index-1]) ||
		index+1 < len(runes) && unicode.Is(unicode.Arabic, runes[index+1])
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

func feedVoteCount(target map[string]any) (int64, bool) {
	value, ok := firstPresent(
		target["voteup_count"],
		mapValue(mapValue(target["reaction"])["statistics"])["like_count"],
		target["like_count"],
	)
	return toInt64(value), ok
}

func feedItemVoted(target map[string]any) bool {
	relationship := mapValue(target["relationship"])
	voting := relationship["voting"]
	if truthy(voting) || toInt64(voting) > 0 || strings.EqualFold(toString(voting), "up") {
		return true
	}
	relation := mapValue(mapValue(target["reaction"])["relation"])
	return truthy(relation["liked"]) || strings.EqualFold(toString(relation["vote"]), "up")
}

func replaceVoteStat(stats string, count int64) string {
	parts := strings.Split(stats, "  ·  ")
	vote := "赞同 " + display.FormatCount(count)
	for index := range parts {
		if strings.HasPrefix(parts[index], "赞同 ") {
			parts[index] = vote
			return strings.Join(parts, "  ·  ")
		}
	}
	if stats == "" {
		return vote
	}
	return vote + "  ·  " + stats
}

func replaceCommentStat(stats string, count int) string {
	parts := strings.Split(stats, "  ·  ")
	comment := "评论 " + display.FormatCount(count)
	for index := range parts {
		if strings.HasPrefix(parts[index], "评论 ") {
			parts[index] = comment
			return strings.Join(parts, "  ·  ")
		}
	}
	if stats == "" {
		return comment
	}
	return stats + "  ·  " + comment
}

func withoutCommentStat(stats string) string {
	parts := strings.Split(stats, "  ·  ")
	kept := parts[:0]
	for _, part := range parts {
		if !strings.HasPrefix(part, "评论 ") {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "  ·  ")
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

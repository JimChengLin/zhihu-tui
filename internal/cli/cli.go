package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"zhihucli2/internal/auth"
	"zhihucli2/internal/client"
	"zhihucli2/internal/config"
	"zhihucli2/internal/display"
)

type optionSpec struct {
	flag     string
	name     string
	hasValue bool
	repeat   bool
}

type parsedOptions struct {
	values      map[string][]string
	positionals []string
}

const defaultNotificationLimit = 10
const notificationHistoryRetention = 90 * 24 * time.Hour
const notificationActorCacheTTL = 24 * time.Hour
const notificationBellInterval = time.Hour
const monitorStatusFallbackColumns = 100

type notificationSeenState struct {
	signature  string
	signatures map[string]struct{}
	actors     map[string]struct{}
	mergeCount int
	createTime int64
}

type notificationActorCacheEntry struct {
	text     string
	cachedAt time.Time
}

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printRootHelp(stdout)
		return 0
	}
	if args[0] == "--version" || args[0] == "version" {
		fmt.Fprintf(stdout, "zhihu-cli %s\n", config.Version)
		return 0
	}
	if args[0] == "-v" || args[0] == "--verbose" {
		args = args[1:]
		if len(args) == 0 {
			printRootHelp(stdout)
			return 0
		}
	}

	ctx := context.Background()
	cmd, rest := args[0], args[1:]
	if wantsHelp(rest) {
		printCommandHelp(stdout, cmd)
		return 0
	}
	var err error
	switch cmd {
	case "login":
		err = runLogin(ctx, rest, stdout)
	case "logout":
		err = runLogout(rest, stdout)
	case "status":
		err = runStatus(rest, stdout)
	case "whoami":
		err = runWhoami(ctx, rest, stdout)
	case "search":
		err = runSearch(ctx, rest, stdout)
	case "hot":
		err = runHot(ctx, rest, stdout)
	case "question":
		err = runQuestion(ctx, rest, stdout)
	case "answers":
		err = runAnswers(ctx, rest, stdout)
	case "answer":
		err = runAnswer(ctx, rest, stdout)
	case "feed":
		err = runFeed(ctx, rest, stdout)
	case "feeds":
		err = runFeeds(ctx, rest, stdout)
	case "topic":
		err = runTopic(ctx, rest, stdout)
	case "user":
		err = runUser(ctx, rest, stdout)
	case "user-answers":
		err = runUserAnswers(ctx, rest, stdout)
	case "user-articles":
		err = runUserArticles(ctx, rest, stdout)
	case "followers":
		err = runFollowers(ctx, rest, stdout)
	case "following":
		err = runFollowing(ctx, rest, stdout)
	case "vote":
		err = runVote(ctx, rest, stdout)
	case "follow-question":
		err = runFollowQuestion(ctx, rest, stdout)
	case "collections":
		err = runCollections(ctx, rest, stdout)
	case "notifications":
		err = runNotifications(ctx, rest, stdout)
	case "reply-comment":
		err = runReplyComment(ctx, rest, stdout)
	case "ask":
		err = runAsk(ctx, rest, stdout)
	case "pin":
		err = runPin(ctx, rest, stdout)
	case "article":
		err = runArticle(ctx, rest, stdout)
	case "delete-question":
		err = runDelete(ctx, rest, stdout, "question")
	case "delete-pin":
		err = runDelete(ctx, rest, stdout, "pin")
	case "delete-article":
		err = runDelete(ctx, rest, stdout, "article")
	case "delete-comment":
		err = runDelete(ctx, rest, stdout, "comment")
	default:
		fmt.Fprintln(stderr, display.Error("unknown command: "+cmd))
		printRootHelp(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, display.Error(err.Error()))
		return 1
	}
	return 0
}

func printRootHelp(w io.Writer) {
	fmt.Fprintln(w, "zhihu-cli - Zhihu from your terminal")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  zhihu <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  login, logout, status, whoami")
	fmt.Fprintln(w, "  search, hot, question, answers, answer, feed, feeds, topic")
	fmt.Fprintln(w, "  user, user-answers, user-articles, followers, following")
	fmt.Fprintln(w, "  vote, follow-question, collections, notifications")
	fmt.Fprintln(w, "  reply-comment")
	fmt.Fprintln(w, "  ask, pin, article, delete-question, delete-pin, delete-article, delete-comment")
}

func printCommandHelp(w io.Writer, cmd string) {
	switch cmd {
	case "login":
		fmt.Fprintln(w, "Usage: zhihu login [--qrcode] [--cookie COOKIE]")
	case "search":
		fmt.Fprintln(w, "Usage: zhihu search QUERY [-t TYPE] [-l LIMIT] [-a ANSWERS] [--json]")
	case "hot":
		fmt.Fprintln(w, "Usage: zhihu hot [-l LIMIT] [--json]")
	case "question":
		fmt.Fprintln(w, "Usage: zhihu question QUESTION_ID [--json]")
	case "answers":
		fmt.Fprintln(w, "Usage: zhihu answers QUESTION_ID [-l LIMIT] [--sort default|created] [--json]")
	case "answer":
		fmt.Fprintln(w, "Usage: zhihu answer ANSWER_ID [-c] [-l LIMIT] [--json]")
	case "feed":
		fmt.Fprintln(w, "Usage: zhihu feed [-l LIMIT] [--json]")
	case "feeds":
		fmt.Fprintln(w, "Usage: zhihu feeds [-l LIMIT] [-c COMMENT_LIMIT]")
	case "topic":
		fmt.Fprintln(w, "Usage: zhihu topic TOPIC_ID [--json]")
	case "user":
		fmt.Fprintln(w, "Usage: zhihu user URL_TOKEN [--json]")
	case "user-answers", "user-articles", "followers", "following":
		fmt.Fprintf(w, "Usage: zhihu %s URL_TOKEN [-l LIMIT] [--json]\n", cmd)
	case "vote":
		fmt.Fprintln(w, "Usage: zhihu vote ANSWER_ID [--neutral]")
	case "follow-question":
		fmt.Fprintln(w, "Usage: zhihu follow-question QUESTION_ID [--unfollow]")
	case "collections":
		fmt.Fprintln(w, "Usage: zhihu collections [-l LIMIT] [--json]")
	case "notifications":
		fmt.Fprintln(w, "Usage: zhihu notifications [-l LIMIT] [--offset OFFSET] [--monitor] [--interval SECONDS] [--debug-log PATH] [--json]")
	case "reply-comment":
		fmt.Fprintln(w, "Usage: zhihu reply-comment COMMENT_ID CONTENT [--resource-type TYPE --resource-id ID] [--json]")
	case "ask":
		fmt.Fprintln(w, "Usage: zhihu ask TITLE [-d DETAIL] [-t TOPIC] [-i IMAGE]")
	case "pin":
		fmt.Fprintln(w, "Usage: zhihu pin TITLE [-c CONTENT] [-i IMAGE]")
	case "article":
		fmt.Fprintln(w, "Usage: zhihu article TITLE CONTENT [-t TOPIC] [-i IMAGE]")
	case "delete-question", "delete-pin", "delete-article", "delete-comment":
		fmt.Fprintf(w, "Usage: zhihu %s ID -y\n", cmd)
	default:
		printRootHelp(w)
	}
}

func runLogin(ctx context.Context, args []string, out io.Writer) error {
	if wantsHelp(args) {
		fmt.Fprintln(out, "Usage: zhihu login [--qrcode] [--cookie COOKIE]")
		return nil
	}
	opts, err := parseOptions(args, specs(
		opt("--qrcode", "qrcode", false),
		opt("--cookie", "cookie", true),
	))
	if err != nil {
		return err
	}
	if cookieStr, ok := opts.value("cookie"); ok {
		parsed := auth.ParseCookieString(cookieStr)
		if !auth.HasRequiredCookies(parsed) {
			return fmt.Errorf("invalid cookie; missing required cookies: %s", strings.Join(auth.MissingRequiredCookies(parsed), ", "))
		}
		if err := auth.SaveCookies(auth.CookieString(parsed)); err != nil {
			return err
		}
		fmt.Fprintln(out, display.Success("cookie saved"))
		return nil
	}
	if opts.has("qrcode") {
		_, err := auth.QRCodeLogin(ctx, out)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, display.Success("login successful; cookie saved"))
		return nil
	}
	cookieStr, ok, err := auth.GetCookieString()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("not authenticated; run zhihu login --qrcode or zhihu login --cookie")
	}
	c, err := newClientFromCookie(cookieStr)
	if err != nil {
		return err
	}
	defer c.Close()
	info, err := c.GetSelfInfo(ctx)
	if err != nil {
		return err
	}
	if toString(info["id"]) == "" && toString(info["name"]) == "" {
		return fmt.Errorf("saved cookie did not return user info")
	}
	fmt.Fprintln(out, display.Success("already authenticated"))
	return nil
}

func runLogout(args []string, out io.Writer) error {
	if wantsHelp(args) {
		fmt.Fprintln(out, "Usage: zhihu logout")
		return nil
	}
	removed, err := auth.ClearCookies()
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		fmt.Fprintln(out, display.Warning("no saved credentials to clear"))
		return nil
	}
	fmt.Fprintln(out, display.Success("logged out; removed: "+strings.Join(removed, ", ")))
	return nil
}

func runStatus(args []string, out io.Writer) error {
	if wantsHelp(args) {
		fmt.Fprintln(out, "Usage: zhihu status")
		return nil
	}
	_, ok, err := auth.GetSavedCookieString()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("not authenticated")
	}
	fmt.Fprintln(out, display.Success("authenticated (saved cookie)"))
	fmt.Fprintln(out, "hint: run zhihu whoami to view profile")
	return nil
}

func runWhoami(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("--json", "json", false)))
	if err != nil {
		return err
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	info, err := c.GetSelfInfo(ctx)
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, info)
	}
	printProfile(out, info, "Me")
	return nil
}

func runSearch(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(
		opt("-t", "type", true), opt("--type", "type", true),
		opt("-l", "limit", true), opt("--limit", "limit", true),
		opt("-a", "answers", true), opt("--answers", "answers", true),
		opt("--json", "json", false),
	))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 1 {
		return fmt.Errorf("usage: zhihu search QUERY")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	result, err := c.Search(ctx, opts.positionals[0], opts.str("type", "general"), 0, opts.int("limit", 10))
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, result)
	}
	data := asSlice(result["data"])
	if len(data) == 0 {
		fmt.Fprintf(out, "No results for %q\n", opts.positionals[0])
		return nil
	}
	for i, item := range data {
		obj := mapValue(item)
		if nested, ok := asMap(obj["object"]); ok {
			obj = nested
		}
		itemType := firstNonEmpty(toString(mapValue(item)["type"]), toString(obj["type"]), "-")
		title := display.StripHTML(firstNonEmpty(toString(obj["title"]), toString(obj["name"]), "-"))
		fmt.Fprintf(out, "%d. [%s] %s\n", i+1, itemType, title)
		if id := toString(obj["id"]); id != "" {
			fmt.Fprintf(out, "   ID: %s\n", id)
		}
	}
	return nil
}

func runHot(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("-l", "limit", true), opt("--limit", "limit", true), opt("--json", "json", false)))
	if err != nil {
		return err
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	result, err := c.GetHotList(ctx, opts.int("limit", 50))
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, result)
	}
	data := asSlice(result["data"])
	if len(data) == 0 {
		fmt.Fprintln(out, "Hot list is empty")
		return nil
	}
	fmt.Fprintln(out, "Trending")
	for i, item := range data {
		m := mapValue(item)
		target := mapValue(firstMap(m["target"], m["question"], item))
		title := display.StripHTML(firstNonEmpty(toString(target["title"]), "-"))
		heat := firstNonEmpty(toString(m["detail_text"]), display.FormatCount(mapValue(m["reaction"])["pv"])+" views")
		fmt.Fprintf(out, "%d. %s\n   %s\n", i+1, title, heat)
	}
	return nil
}

func runQuestion(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("--json", "json", false)))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 1 {
		return fmt.Errorf("usage: zhihu question QUESTION_ID")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	q, err := c.GetQuestion(ctx, opts.positionals[0])
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, q)
	}
	fmt.Fprintln(out, display.StripHTML(toString(q["title"])))
	if detail := display.StripHTML(toString(q["detail"])); detail != "" {
		fmt.Fprintln(out, detail)
	}
	fmt.Fprintln(out, display.StatsLine(map[string]any{"Answers": q["answer_count"], "Followers": q["follower_count"], "Views": q["visit_count"]}))
	return nil
}

func runAnswers(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("-l", "limit", true), opt("--limit", "limit", true), opt("--sort", "sort", true), opt("--json", "json", false)))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 1 {
		return fmt.Errorf("usage: zhihu answers QUESTION_ID")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	result, err := c.GetQuestionAnswers(ctx, opts.positionals[0], 0, opts.int("limit", 5), opts.str("sort", "default"))
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, result)
	}
	data := asSlice(result["data"])
	if len(data) == 0 {
		fmt.Fprintln(out, "No answers yet")
		return nil
	}
	for i, item := range data {
		ans := mapValue(item)
		author := toString(mapValue(ans["author"])["name"])
		excerpt := display.Truncate(display.StripHTML(firstNonEmpty(toString(ans["excerpt"]), toString(ans["content"]))), 90)
		fmt.Fprintf(out, "%d. %s - %s (%s upvotes)\n", i+1, firstNonEmpty(author, "Anonymous"), excerpt, display.FormatCount(ans["voteup_count"]))
	}
	return nil
}

func runAnswer(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("--json", "json", false), opt("-c", "comments", false), opt("--comments", "comments", false), opt("-l", "limit", true), opt("--limit", "limit", true)))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 1 {
		return fmt.Errorf("usage: zhihu answer ANSWER_ID")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	ans, err := c.GetAnswer(ctx, opts.positionals[0])
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, ans)
	}
	author := firstNonEmpty(toString(mapValue(ans["author"])["name"]), "Anonymous")
	fmt.Fprintf(out, "Answer by %s\n\n%s\n", author, display.StripHTML(toString(ans["content"])))
	fmt.Fprintln(out, display.StatsLine(map[string]any{"Upvotes": ans["voteup_count"], "Comments": ans["comment_count"]}))
	if opts.has("comments") {
		limit := opts.int("limit", 20)
		result, err := c.GetAnswerComments(ctx, opts.positionals[0], 0, limit, "normal")
		if err != nil {
			return err
		}
		for i, raw := range asSlice(result["data"]) {
			comment := mapValue(raw)
			fmt.Fprintf(out, "%d. %s (%s likes)\n", i+1, display.StripHTML(toString(comment["content"])), display.FormatCount(comment["vote_count"]))
		}
	}
	return nil
}

func runFeed(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("-l", "limit", true), opt("--limit", "limit", true), opt("--json", "json", false)))
	if err != nil {
		return err
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	result, err := c.GetFeed(ctx, opts.int("limit", 10))
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, result)
	}
	return printFeed(out, result)
}

func runFeeds(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("-l", "limit", true), opt("--limit", "limit", true), opt("-c", "comment-limit", true), opt("--comment-limit", "comment-limit", true)))
	if err != nil {
		return err
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	result, err := c.GetFeed(ctx, opts.int("limit", 6))
	if err != nil {
		return err
	}
	data := asSlice(result["data"])
	if len(data) == 0 {
		fmt.Fprintln(out, "Feed is empty")
		return nil
	}
	commentLimit := opts.int("comment-limit", 10)
	for i, raw := range data {
		target := mapValue(mapValue(raw)["target"])
		itemType := toString(target["type"])
		itemID := toString(target["id"])
		title := display.StripHTML(firstNonEmpty(toString(target["title"]), toString(mapValue(target["question"])["title"]), toString(target["excerpt"]), "-"))
		fmt.Fprintf(out, "%d. [%s] %s\n", i+1, itemType, title)
		if commentLimit > 0 && itemType == "answer" && itemID != "" {
			comments, err := c.GetAnswerComments(ctx, itemID, 0, commentLimit, "normal")
			if err == nil {
				for j, rawComment := range asSlice(comments["data"]) {
					comment := mapValue(rawComment)
					fmt.Fprintf(out, "   %d. %s\n", j+1, display.StripHTML(toString(comment["content"])))
				}
			}
		}
	}
	return nil
}

func runTopic(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("--json", "json", false)))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 1 {
		return fmt.Errorf("usage: zhihu topic TOPIC_ID")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	topic, err := c.GetTopic(ctx, opts.positionals[0])
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, topic)
	}
	fmt.Fprintf(out, "# %s\n", toString(topic["name"]))
	if intro := display.StripHTML(toString(topic["introduction"])); intro != "" {
		fmt.Fprintln(out, intro)
	}
	fmt.Fprintln(out, display.StatsLine(map[string]any{"Followers": topic["followers_count"], "Questions": topic["questions_count"]}))
	hot, err := c.GetTopicHotQuestions(ctx, opts.positionals[0], 0, 10)
	if err == nil {
		for i, raw := range asSlice(hot["data"]) {
			item := mapValue(raw)
			fmt.Fprintf(out, "%d. %s\n", i+1, display.StripHTML(toString(item["title"])))
		}
	}
	return nil
}

func runUser(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("--json", "json", false)))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 1 {
		return fmt.Errorf("usage: zhihu user URL_TOKEN")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	info, err := c.GetUserProfile(ctx, opts.positionals[0])
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, info)
	}
	printProfile(out, info, "@"+opts.positionals[0])
	return nil
}

func runUserAnswers(ctx context.Context, args []string, out io.Writer) error {
	return runUserList(ctx, args, out, "answers")
}

func runUserArticles(ctx context.Context, args []string, out io.Writer) error {
	return runUserList(ctx, args, out, "articles")
}

func runFollowers(ctx context.Context, args []string, out io.Writer) error {
	return runUserList(ctx, args, out, "followers")
}

func runFollowing(ctx context.Context, args []string, out io.Writer) error {
	return runUserList(ctx, args, out, "following")
}

func runUserList(ctx context.Context, args []string, out io.Writer, kind string) error {
	opts, err := parseOptions(args, specs(opt("-l", "limit", true), opt("--limit", "limit", true), opt("--json", "json", false)))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 1 {
		return fmt.Errorf("usage: zhihu %s URL_TOKEN", commandNameForKind(kind))
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	limit := opts.int("limit", 10)
	var result map[string]any
	switch kind {
	case "answers":
		result, err = c.GetUserAnswers(ctx, opts.positionals[0], 0, limit, "created")
	case "articles":
		result, err = c.GetUserArticles(ctx, opts.positionals[0], 0, limit, "created")
	case "followers":
		result, err = c.GetFollowers(ctx, opts.positionals[0], 0, limit)
	case "following":
		result, err = c.GetFollowing(ctx, opts.positionals[0], 0, limit)
	}
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, result)
	}
	data := asSlice(result["data"])
	if len(data) == 0 {
		fmt.Fprintf(out, "No %s found\n", kind)
		return nil
	}
	for i, raw := range data {
		item := mapValue(raw)
		switch kind {
		case "answers":
			fmt.Fprintf(out, "%d. %s (%s upvotes)\n", i+1, display.StripHTML(toString(mapValue(item["question"])["title"])), display.FormatCount(item["voteup_count"]))
		case "articles":
			fmt.Fprintf(out, "%d. %s (%s upvotes)\n", i+1, display.StripHTML(toString(item["title"])), display.FormatCount(item["voteup_count"]))
		default:
			fmt.Fprintf(out, "%d. %s - %s\n", i+1, toString(item["name"]), toString(item["headline"]))
		}
	}
	return nil
}

func runVote(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("--up", "up", false), opt("--neutral", "neutral", false)))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 1 {
		return fmt.Errorf("usage: zhihu vote [--neutral] ANSWER_ID")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	var ok bool
	if opts.has("neutral") {
		ok, err = c.VoteNeutral(ctx, opts.positionals[0])
	} else {
		ok, err = c.VoteUp(ctx, opts.positionals[0])
	}
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("vote request was not accepted by the server")
	}
	if opts.has("neutral") {
		fmt.Fprintln(out, display.Success("cancelled vote on answer "+opts.positionals[0]))
	} else {
		fmt.Fprintln(out, display.Success("upvoted answer "+opts.positionals[0]))
	}
	return nil
}

func runFollowQuestion(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("--unfollow", "unfollow", false)))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 1 {
		return fmt.Errorf("usage: zhihu follow-question [--unfollow] QUESTION_ID")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	var ok bool
	if opts.has("unfollow") {
		ok, err = c.UnfollowQuestion(ctx, opts.positionals[0])
	} else {
		ok, err = c.FollowQuestion(ctx, opts.positionals[0])
	}
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("follow request was not accepted by the server")
	}
	if opts.has("unfollow") {
		fmt.Fprintln(out, display.Success("unfollowed question "+opts.positionals[0]))
	} else {
		fmt.Fprintln(out, display.Success("followed question "+opts.positionals[0]))
	}
	return nil
}

func runCollections(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("-l", "limit", true), opt("--limit", "limit", true), opt("--json", "json", false)))
	if err != nil {
		return err
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	result, err := c.GetCollections(ctx, 0, opts.int("limit", 10))
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, result)
	}
	data := asSlice(result["data"])
	if len(data) == 0 {
		fmt.Fprintln(out, "No collections found")
		return nil
	}
	for i, raw := range data {
		item := mapValue(raw)
		fmt.Fprintf(out, "%d. %s (%s items)\n", i+1, toString(item["title"]), display.FormatCount(firstNonEmptyAny(item["item_count"], item["answer_count"], 0)))
	}
	return nil
}

func runNotifications(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(
		opt("-l", "limit", true),
		opt("--limit", "limit", true),
		opt("--offset", "offset", true),
		opt("--json", "json", false),
		opt("--monitor", "monitor", false),
		opt("--interval", "interval", true),
		opt("--debug-log", "debug-log", true),
	))
	if err != nil {
		return err
	}
	if opts.has("monitor") && opts.has("json") {
		return fmt.Errorf("notifications --monitor does not support --json")
	}
	if opts.has("debug-log") && !opts.has("monitor") {
		return fmt.Errorf("notifications --debug-log requires --monitor")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	formatter := newNotificationFormatter(c)
	limit := opts.int("limit", defaultNotificationLimit)
	if opts.has("monitor") {
		interval := time.Duration(opts.int("interval", 60)) * time.Second
		if interval <= 0 {
			return fmt.Errorf("--interval must be greater than 0")
		}
		debugLog, err := openNotificationDebugLog(opts.str("debug-log", ""))
		if err != nil {
			return err
		}
		if debugLog != nil {
			defer debugLog.Close()
		}
		return runNotificationsMonitor(ctx, c, formatter, out, limit, interval, debugLog)
	}
	result, err := c.GetNotifications(ctx, limit, opts.int("offset", 0), "all")
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, result)
	}
	if err := printNotifications(ctx, out, formatter, result, false); err != nil {
		return err
	}
	paging := mapValue(result["paging"])
	nextURL := toString(paging["next"])
	if !truthy(paging["is_end"]) && strings.Contains(nextURL, "offset=") {
		if parsed, err := url.Parse(nextURL); err == nil {
			if nextOffset := parsed.Query().Get("offset"); nextOffset != "" {
				fmt.Fprintf(out, "hint: zhihu notifications --offset %s -l %d\n", nextOffset, limit)
			}
		}
	}
	return nil
}

func runNotificationsMonitor(ctx context.Context, c *client.Client, formatter *notificationFormatter, out io.Writer, limit int, interval time.Duration, debugLog *notificationDebugLogger) error {
	result, err := c.GetNotifications(ctx, limit, 0, "all")
	if err != nil {
		return err
	}
	seen := map[string]notificationSeenState{}
	startedAt := time.Now()
	if err := debugLog.Log(startedAt, "monitor_start", map[string]any{
		"limit":    limit,
		"interval": interval.String(),
	}); err != nil {
		return err
	}
	data := asSlice(result["data"])
	for i, raw := range data {
		notification := mapValue(raw)
		key, signature := notificationState(notification)
		if key != "" {
			rememberNotificationState(seen, notification, startedAt)
		}
		if err := debugLog.LogNotification(startedAt, "initial", i, notification, key, signature, "seed", "initial_fetch", notificationSeenState{}, false); err != nil {
			return err
		}
	}
	if err := printNotifications(ctx, out, formatter, result, false); err != nil {
		return err
	}
	fmt.Fprintf(out, "Monitoring notifications every %s. Press Ctrl+C to stop.\n", interval)
	monitorOut := newMonitorOutput(out)
	monitorOut.Status(time.Now(), "waiting")

	var lastBellAt time.Time
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			checkedAt := time.Now()
			pruned := pruneNotificationHistory(seen, checkedAt)
			if err := debugLog.Log(checkedAt, "refresh_start", map[string]any{
				"seen_count": len(seen),
				"pruned":     pruned,
			}); err != nil {
				return err
			}
			result, err := c.GetNotifications(ctx, limit, 0, "all")
			if err != nil {
				if logErr := debugLog.Log(checkedAt, "refresh_error", map[string]any{"error": err.Error()}); logErr != nil {
					return logErr
				}
				monitorOut.Status(checkedAt, "refresh failed: "+err.Error())
				continue
			}
			newItems := make([]any, 0)
			newStates := make(map[string]notificationSeenState)
			for i, raw := range asSlice(result["data"]) {
				notification := mapValue(raw)
				key, signature := notificationState(notification)
				if key == "" {
					if err := debugLog.LogNotification(checkedAt, "refresh", i, notification, key, signature, "skip", "empty_key", notificationSeenState{}, false); err != nil {
						return err
					}
					continue
				}
				seenState, ok := notificationKnownState(seen, newStates, key)
				if ok {
					known, reason := notificationSeenStateContains(seenState, notification, signature)
					decision := "new"
					if known {
						decision = "seen"
					}
					if err := debugLog.LogNotification(checkedAt, "refresh", i, notification, key, signature, decision, reason, seenState, true); err != nil {
						return err
					}
					if known {
						continue
					}
				} else {
					if err := debugLog.LogNotification(checkedAt, "refresh", i, notification, key, signature, "new", "missing_key", notificationSeenState{}, false); err != nil {
						return err
					}
				}
				newItems = append(newItems, raw)
				rememberNotificationState(newStates, notification, checkedAt)
			}
			if len(newItems) == 0 {
				monitorOut.Status(checkedAt, "no new notifications")
				continue
			}
			formatter.clearTargetCache()
			lines, err := formatNotificationItems(ctx, formatter, oldestFirstNotifications(newItems))
			if err != nil {
				if logErr := debugLog.Log(checkedAt, "format_error", map[string]any{"error": err.Error(), "new_count": len(newItems)}); logErr != nil {
					return logErr
				}
				monitorOut.Status(checkedAt, "error: "+err.Error())
				continue
			}
			for key, state := range newStates {
				seen[key] = mergeNotificationSeenState(seen[key], state)
			}
			sendBell := shouldSendNotificationBell(checkedAt, lastBellAt)
			if err := debugLog.Log(checkedAt, "new_notifications", map[string]any{"count": len(newItems), "bell": sendBell}); err != nil {
				return err
			}
			if sendBell {
				notifyTTY()
				lastBellAt = checkedAt
			}
			monitorOut.NewSeparator(checkedAt, len(newItems), sendBell)
			writeNotificationLines(out, lines)
			monitorOut.Status(checkedAt, "waiting")
		}
	}
}

type notificationDebugLogger struct {
	path string
	file *os.File
	enc  *json.Encoder
}

func openNotificationDebugLog(path string) (*notificationDebugLogger, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	expanded, err := expandHomePath(path)
	if err != nil {
		return nil, err
	}
	if dir := filepath.Dir(expanded); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create debug log directory: %w", err)
		}
	}
	file, err := os.OpenFile(expanded, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open notification debug log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, fmt.Errorf("chmod notification debug log: %w", err)
	}
	return &notificationDebugLogger{path: expanded, file: file, enc: json.NewEncoder(file)}, nil
}

func expandHomePath(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

func (l *notificationDebugLogger) Close() error {
	if l == nil {
		return nil
	}
	return l.file.Close()
}

func (l *notificationDebugLogger) Log(ts time.Time, event string, fields map[string]any) error {
	if l == nil {
		return nil
	}
	record := map[string]any{
		"ts":    ts.Format(time.RFC3339Nano),
		"event": event,
	}
	for key, value := range fields {
		record[key] = value
	}
	if err := l.enc.Encode(record); err != nil {
		return fmt.Errorf("write notification debug log %s: %w", l.path, err)
	}
	return nil
}

func (l *notificationDebugLogger) LogNotification(ts time.Time, phase string, index int, n map[string]any, key, signature, decision, reason string, seenState notificationSeenState, hadSeenState bool) error {
	if l == nil {
		return nil
	}
	content := mapValue(n["content"])
	contentTarget := mapValue(content["target"])
	target := mapValue(n["target"])
	groupType, groupID := notificationGroupTarget(target, contentTarget)
	fields := map[string]any{
		"phase":                    phase,
		"index":                    index,
		"decision":                 decision,
		"reason":                   reason,
		"key":                      key,
		"signature":                signature,
		"notification_id":          toString(n["id"]),
		"type":                     toString(n["type"]),
		"create_time":              toString(n["create_time"]),
		"merge_count":              toString(n["merge_count"]),
		"verb":                     strings.TrimSpace(toString(content["verb"])),
		"target_type":              toString(target["type"]),
		"target_resource_type":     toString(target["resource_type"]),
		"target_id":                toString(target["id"]),
		"target_url_token":         toString(target["url_token"]),
		"content_target_link":      toString(contentTarget["link"]),
		"content_target_text":      truncateWithDots(compactPlainText(toString(contentTarget["text"])), 200),
		"group_target_type":        groupType,
		"group_target_id":          groupID,
		"actors":                   notificationDebugActors(asSlice(content["actors"])),
		"seen_state_found":         hadSeenState,
		"seen_state_signature":     "",
		"seen_state_signatures":    []string{},
		"seen_state_actors":        []string{},
		"seen_state_merge_count":   0,
		"seen_state_create_time":   int64(0),
		"seen_state_create_time_s": "",
	}
	if hadSeenState {
		fields["seen_state_signature"] = seenState.signature
		fields["seen_state_signatures"] = sortedStringSet(seenState.signatures)
		fields["seen_state_actors"] = sortedStringSet(seenState.actors)
		fields["seen_state_merge_count"] = seenState.mergeCount
		fields["seen_state_create_time"] = seenState.createTime
		if seenState.createTime > 0 {
			fields["seen_state_create_time_s"] = time.Unix(seenState.createTime, 0).Format(time.RFC3339)
		}
	}
	return l.Log(ts, "notification_state", fields)
}

func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func notificationDebugActors(actors []any) []map[string]string {
	out := make([]map[string]string, 0, len(actors))
	for _, raw := range actors {
		actor := mapValue(raw)
		out = append(out, map[string]string{
			"name":      toString(actor["name"]),
			"url_token": toString(actor["url_token"]),
			"id":        toString(actor["id"]),
			"link":      toString(actor["link"]),
		})
	}
	return out
}

func printNotifications(ctx context.Context, out io.Writer, formatter *notificationFormatter, result map[string]any, omitEmpty bool) error {
	data := asSlice(result["data"])
	if len(data) == 0 {
		if omitEmpty {
			return nil
		}
		fmt.Fprintln(out, "No notifications")
		return nil
	}
	fmt.Fprintln(out, "Notifications")
	return printNotificationItems(ctx, out, formatter, oldestFirstNotifications(data))
}

func printNotificationItems(ctx context.Context, out io.Writer, formatter *notificationFormatter, data []any) error {
	lines, err := formatNotificationItems(ctx, formatter, data)
	if err != nil {
		return err
	}
	writeNotificationLines(out, lines)
	return nil
}

func formatNotificationItems(ctx context.Context, formatter *notificationFormatter, data []any) ([]string, error) {
	lines := make([]string, 0, len(data))
	for _, raw := range data {
		line, err := formatter.format(ctx, mapValue(raw))
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	return lines, nil
}

func writeNotificationLines(out io.Writer, lines []string) {
	for i, line := range lines {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, line)
	}
}

func notifyTTY() {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer tty.Close()
	fmt.Fprint(tty, terminalNotificationSequence())
}

func terminalNotificationSequence() string {
	return "\a"
}

func shouldSendNotificationBell(now, last time.Time) bool {
	return last.IsZero() || !now.Before(last.Add(notificationBellInterval))
}

type monitorOutput struct {
	out        io.Writer
	statusRows int
}

func newMonitorOutput(out io.Writer) *monitorOutput {
	return &monitorOutput{out: out}
}

func (m *monitorOutput) Status(t time.Time, status string) {
	line := monitorStatusLine(t, status)
	m.clearStatus()
	fmt.Fprint(m.out, line)
	m.statusRows = monitorStatusRows(line)
}

func (m *monitorOutput) NewSeparator(t time.Time, count int, bell bool) {
	m.clearStatus()
	fmt.Fprint(m.out, monitorNewSeparator(t, count, bell))
}

func (m *monitorOutput) clearStatus() {
	if m.statusRows <= 0 {
		return
	}
	fmt.Fprint(m.out, monitorClearStatus(m.statusRows))
	m.statusRows = 0
}

func monitorClearStatus(rows int) string {
	if rows <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\r\033[2K")
	for i := 1; i < rows; i++ {
		b.WriteString("\033[1A\r\033[2K")
	}
	return b.String()
}

func monitorStatusRows(line string) int {
	return monitorStatusRowsWithColumns(line, monitorStatusColumns())
}

func monitorStatusRowsWithColumns(line string, columns int) int {
	line = strings.TrimPrefix(line, "\r\033[2K")
	line = strings.TrimLeft(line, "\r")
	if columns <= 0 {
		return 1
	}
	width := len([]rune(line))
	if width == 0 {
		return 1
	}
	return (width-1)/columns + 1
}

func monitorStatusLine(t time.Time, status string) string {
	return monitorStatusLineWithColumns(t, status, monitorStatusColumns())
}

func monitorStatusLineWithColumns(t time.Time, status string, columns int) string {
	return "\r\033[2K" + monitorStatusTextWithColumns(t, status, columns)
}

func monitorStatusText(t time.Time, status string) string {
	return monitorStatusTextWithColumns(t, status, monitorStatusColumns())
}

func monitorStatusTextWithColumns(t time.Time, status string, columns int) string {
	status = strings.Join(strings.Fields(status), " ")
	prefix := fmt.Sprintf("Last check: %s · ", t.Format("15:04:05"))
	status = truncateStatusLine(status, columns-len([]rune(prefix)))
	return prefix + status
}

func monitorStatusColumns() int {
	if columns, ok := terminalColumns(); ok {
		return columns
	}
	columns, err := strconv.Atoi(os.Getenv("COLUMNS"))
	if err == nil && columns > 0 {
		return columns
	}
	return monitorStatusFallbackColumns
}

func truncateStatusLine(status string, maxLen int) string {
	if status == "" || maxLen <= 0 {
		return ""
	}
	runes := []rune(status)
	if len(runes) <= maxLen {
		return status
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

func monitorNewSeparator(t time.Time, count int, bell bool) string {
	label := "New notifications"
	if bell {
		label = "🔔 New notifications"
	}
	return fmt.Sprintf("\r\033[2K\n----- %s @ %s (%d new) -----\n", label, t.Format("15:04:05"), count)
}

func runReplyComment(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("--json", "json", false), opt("--resource-type", "resource-type", true), opt("--resource-id", "resource-id", true)))
	if err != nil {
		return err
	}
	if len(opts.positionals) < 2 {
		return fmt.Errorf("usage: zhihu reply-comment COMMENT_ID CONTENT")
	}
	commentID := strings.TrimSpace(opts.positionals[0])
	content := strings.TrimSpace(strings.Join(opts.positionals[1:], " "))
	if commentID == "" || content == "" {
		return fmt.Errorf("usage: zhihu reply-comment COMMENT_ID CONTENT")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	var result map[string]any
	resourceType, hasResourceType := opts.value("resource-type")
	resourceID, hasResourceID := opts.value("resource-id")
	switch {
	case hasResourceType && hasResourceID:
		result, err = c.ReplyCommentToResource(ctx, resourceType, resourceID, commentID, content)
	case hasResourceType || hasResourceID:
		return fmt.Errorf("--resource-type and --resource-id must be provided together")
	default:
		result, err = c.ReplyComment(ctx, commentID, content)
	}
	if err != nil {
		return err
	}
	if opts.has("json") {
		return printJSON(out, result)
	}
	id := firstNonEmpty(toString(result["id"]), toString(mapValue(result["data"])["id"]))
	if id == "" {
		fmt.Fprintln(out, display.Success("comment reply posted"))
		return nil
	}
	fmt.Fprintf(out, "%s\n", display.Success("comment reply posted; ID: "+id))
	return nil
}

func runAsk(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("-d", "detail", true), opt("--detail", "detail", true), opt("-t", "topic", true), opt("--topic", "topic", true), opt("-i", "image", true), opt("--image", "image", true)))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 1 || strings.TrimSpace(opts.positionals[0]) == "" {
		return fmt.Errorf("usage: zhihu ask TITLE")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	images, err := uploadImages(ctx, c, opts.values["image"], "question", out)
	if err != nil {
		return err
	}
	result, err := c.CreateQuestion(ctx, strings.TrimSpace(opts.positionals[0]), opts.str("detail", ""), opts.values["topic"], images)
	if err != nil {
		return err
	}
	id := toString(result["id"])
	if id == "" {
		fmt.Fprintln(out, display.Warning("question may have been created but no ID returned"))
		return nil
	}
	fmt.Fprintf(out, "%s\nhttps://www.zhihu.com/question/%s\n", display.Success("question created; ID: "+id), id)
	return nil
}

func runPin(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("-c", "content", true), opt("--content", "content", true), opt("-i", "image", true), opt("--image", "image", true)))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 1 || strings.TrimSpace(opts.positionals[0]) == "" {
		return fmt.Errorf("usage: zhihu pin TITLE")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	images, err := uploadImages(ctx, c, opts.values["image"], "pin", out)
	if err != nil {
		return err
	}
	result, err := c.CreatePin(ctx, strings.TrimSpace(opts.positionals[0]), opts.str("content", ""), images)
	if err != nil {
		return err
	}
	id := toString(result["id"])
	if id == "" {
		fmt.Fprintln(out, display.Warning("pin may have been created but no ID returned"))
		return nil
	}
	fmt.Fprintf(out, "%s\nhttps://www.zhihu.com/pin/%s\n", display.Success("pin published; ID: "+id), id)
	return nil
}

func runArticle(ctx context.Context, args []string, out io.Writer) error {
	opts, err := parseOptions(args, specs(opt("-t", "topic", true), opt("--topic", "topic", true), opt("-i", "image", true), opt("--image", "image", true)))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 2 || strings.TrimSpace(opts.positionals[0]) == "" || strings.TrimSpace(opts.positionals[1]) == "" {
		return fmt.Errorf("usage: zhihu article TITLE CONTENT")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	images, err := uploadImages(ctx, c, opts.values["image"], "article", out)
	if err != nil {
		return err
	}
	body := "<p>" + strings.TrimSpace(opts.positionals[1]) + "</p>"
	result, err := c.CreateArticle(ctx, strings.TrimSpace(opts.positionals[0]), body, opts.values["topic"], images)
	if err != nil {
		return err
	}
	id := toString(result["id"])
	if id == "" {
		fmt.Fprintln(out, display.Warning("article may have been published but no ID returned"))
		return nil
	}
	fmt.Fprintf(out, "%s\nhttps://zhuanlan.zhihu.com/p/%s\n", display.Success("article published; ID: "+id), id)
	return nil
}

func runDelete(ctx context.Context, args []string, out io.Writer, kind string) error {
	opts, err := parseOptions(args, specs(opt("-y", "yes", false), opt("--yes", "yes", false)))
	if err != nil {
		return err
	}
	if len(opts.positionals) != 1 {
		return fmt.Errorf("usage: zhihu delete-%s ID [-y]", kind)
	}
	if !opts.has("yes") {
		return fmt.Errorf("refusing to delete without -y/--yes")
	}
	c, err := authenticatedClient()
	if err != nil {
		return err
	}
	defer c.Close()
	var ok bool
	switch kind {
	case "question":
		ok, err = c.DeleteQuestion(ctx, opts.positionals[0])
	case "pin":
		ok, err = c.DeletePin(ctx, opts.positionals[0])
	case "article":
		ok, err = c.DeleteArticle(ctx, opts.positionals[0])
	case "comment":
		ok, err = c.DeleteComment(ctx, opts.positionals[0])
	}
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("delete request was not accepted by the server")
	}
	fmt.Fprintf(out, "%s\n", display.Success(kind+" "+opts.positionals[0]+" deleted"))
	return nil
}

func authenticatedClient() (*client.Client, error) {
	cookieStr, ok, err := auth.GetCookieString()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("not authenticated; run zhihu login")
	}
	return newClientFromCookie(cookieStr)
}

func newClientFromCookie(cookieStr string) (*client.Client, error) {
	cookies := auth.ParseCookieString(cookieStr)
	if !auth.HasRequiredCookies(cookies) {
		return nil, fmt.Errorf("saved cookie is missing required cookies: %s", strings.Join(auth.MissingRequiredCookies(cookies), ", "))
	}
	return client.New(cookies), nil
}

func uploadImages(ctx context.Context, c *client.Client, paths []string, source string, out io.Writer) ([]map[string]any, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	infos := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		fmt.Fprintln(out, display.Info("uploading image: "+path))
		info, err := c.UploadImage(ctx, path, source)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func parseOptions(args []string, specs map[string]optionSpec) (parsedOptions, error) {
	out := parsedOptions{values: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out.positionals = append(out.positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			key, value, hasInlineValue := strings.Cut(arg, "=")
			spec, ok := specs[key]
			if !ok {
				return out, fmt.Errorf("unknown option: %s", key)
			}
			if spec.hasValue {
				if !hasInlineValue {
					i++
					if i >= len(args) {
						return out, fmt.Errorf("option %s requires a value", key)
					}
					value = args[i]
				}
				if !spec.repeat && len(out.values[spec.name]) > 0 {
					out.values[spec.name] = []string{value}
				} else {
					out.values[spec.name] = append(out.values[spec.name], value)
				}
			} else {
				if hasInlineValue {
					return out, fmt.Errorf("option %s does not take a value", key)
				}
				out.values[spec.name] = []string{"true"}
			}
			continue
		}
		out.positionals = append(out.positionals, arg)
	}
	return out, nil
}

func specs(items ...optionSpec) map[string]optionSpec {
	out := make(map[string]optionSpec, len(items))
	for _, item := range items {
		out[item.flag] = item
	}
	return out
}

func opt(flag, canonical string, hasValue bool) optionSpec {
	return optionSpec{flag: flag, name: canonical, hasValue: hasValue, repeat: trueForRepeated(canonical)}
}

func trueForRepeated(name string) bool {
	return name == "topic" || name == "image"
}

func wantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func (p parsedOptions) has(key string) bool {
	values := p.values[key]
	return len(values) > 0 && values[len(values)-1] == "true"
}

func (p parsedOptions) value(key string) (string, bool) {
	values := p.values[key]
	if len(values) == 0 {
		return "", false
	}
	return values[len(values)-1], true
}

func (p parsedOptions) str(key, fallback string) string {
	if value, ok := p.value(key); ok {
		return value
	}
	return fallback
}

func (p parsedOptions) int(key string, fallback int) int {
	value, ok := p.value(key)
	if !ok {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func printJSON(out io.Writer, v any) error {
	text, err := display.ToPrettyJSON(v)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, text)
	return nil
}

func printProfile(out io.Writer, info map[string]any, title string) {
	fmt.Fprintf(out, "%s\n", title)
	fmt.Fprintf(out, "Name: %s\n", firstNonEmpty(toString(info["name"]), "Unknown"))
	if headline := toString(info["headline"]); headline != "" {
		fmt.Fprintf(out, "Headline: %s\n", headline)
	}
	if desc := display.StripHTML(toString(info["description"])); desc != "" {
		fmt.Fprintf(out, "Bio: %s\n", display.Truncate(desc, 120))
	}
	fmt.Fprintf(out, "Answers: %s\n", display.FormatCount(info["answer_count"]))
	fmt.Fprintf(out, "Articles: %s\n", display.FormatCount(info["articles_count"]))
	fmt.Fprintf(out, "Followers: %s\n", display.FormatCount(info["follower_count"]))
	fmt.Fprintf(out, "Following: %s\n", display.FormatCount(info["following_count"]))
	fmt.Fprintf(out, "Upvotes: %s\n", display.FormatCount(info["voteup_count"]))
}

func printFeed(out io.Writer, result map[string]any) error {
	data := asSlice(result["data"])
	if len(data) == 0 {
		fmt.Fprintln(out, "Feed is empty")
		return nil
	}
	fmt.Fprintln(out, "Recommended Feed")
	for i, raw := range data {
		target := mapValue(mapValue(raw)["target"])
		title := display.StripHTML(firstNonEmpty(toString(target["title"]), toString(mapValue(target["question"])["title"]), toString(target["excerpt"]), "-"))
		fmt.Fprintf(out, "%d. [%s] %s - %s\n", i+1, firstNonEmpty(toString(target["type"]), "-"), title, firstNonEmpty(toString(mapValue(target["author"])["name"]), "-"))
	}
	return nil
}

type notificationFormatter struct {
	client      *client.Client
	actorCache  map[string]notificationActorCacheEntry
	targetCache map[string]string
	now         func() time.Time
}

func newNotificationFormatter(c *client.Client) *notificationFormatter {
	return &notificationFormatter{
		client:      c,
		actorCache:  map[string]notificationActorCacheEntry{},
		targetCache: map[string]string{},
		now:         time.Now,
	}
}

func (f *notificationFormatter) clearTargetCache() {
	f.targetCache = map[string]string{}
}

func (f *notificationFormatter) format(ctx context.Context, n map[string]any) (string, error) {
	content := mapValue(n["content"])
	target := mapValue(content["target"])
	targetText := compactPlainText(toString(target["text"]))
	verb := strings.TrimSpace(toString(content["verb"]))
	actorText, err := f.formatActors(ctx, asSlice(content["actors"]))
	if err != nil {
		return "", err
	}
	summary := formatNotificationSummary(actorText, verb, targetText)
	lines := make([]string, 0, 4)
	if summary != "" {
		lines = append(lines, summary)
	}
	comment := incomingCommentSnippet(n)
	if comment != "" {
		lines = append(lines, "  评论："+comment)
	}
	if targetText != "" && targetText != summary {
		if comment != "" {
			lines = append(lines, "  "+notificationTargetLabel(toString(target["link"]))+"："+targetText)
		} else {
			lines = append(lines, "  "+targetText)
		}
	}
	if targetMeta, err := f.formatNotificationMeta(ctx, n, comment != "", toString(target["link"])); err != nil {
		return "", err
	} else if targetMeta != "" {
		lines = append(lines, "  "+targetMeta)
	}
	if len(lines) == 0 {
		return "-", nil
	}
	return strings.Join(lines, "\n"), nil
}

func formatNotificationSummary(actorText, verb, targetText string) string {
	switch {
	case actorText != "" && verb != "":
		return actorText + " " + verb
	case targetText != "":
		return targetText
	default:
		return verb
	}
}

func (f *notificationFormatter) formatActors(ctx context.Context, actors []any) (string, error) {
	if len(actors) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(actors))
	now := f.now()
	for _, raw := range actors {
		actor := mapValue(raw)
		name := toString(actor["name"])
		if name == "" {
			continue
		}
		token := toString(actor["url_token"])
		if token == "" {
			parts = append(parts, name)
			continue
		}
		if cached, ok := f.actorCache[token]; ok && now.Sub(cached.cachedAt) < notificationActorCacheTTL {
			parts = append(parts, cached.text)
			continue
		}
		profile, err := f.client.GetUserProfile(ctx, token)
		if err != nil {
			return "", err
		}
		enriched := formatActorWithProfile(name, profile)
		f.actorCache[token] = notificationActorCacheEntry{text: enriched, cachedAt: now}
		parts = append(parts, enriched)
	}
	return strings.Join(parts, ", "), nil
}

func formatActorWithProfile(name string, profile map[string]any) string {
	details := make([]string, 0, 2)
	isFollowing := truthy(profile["is_following"])
	isFollowed := truthy(profile["is_followed"])
	switch {
	case isFollowing && isFollowed:
		details = append(details, "互相关注")
	case isFollowing:
		details = append(details, "我关注")
	case isFollowed:
		details = append(details, "关注我")
	}
	if followerCount := toString(profile["follower_count"]); followerCount != "" {
		details = append(details, "粉丝 "+display.FormatCount(profile["follower_count"]))
	}
	if len(details) == 0 {
		return name
	}
	return name + "（" + strings.Join(details, "，") + "）"
}

func (f *notificationFormatter) formatNotificationMeta(ctx context.Context, n map[string]any, hasIncomingComment bool, rawLink string) (string, error) {
	if shouldUseSelfFollowerStats(n) {
		return f.formatSelfFollowerMeta(ctx)
	}
	if shouldUseCommentStats(n, hasIncomingComment) {
		return f.formatCommentMeta(ctx, n)
	}
	return f.formatTargetMeta(ctx, rawLink)
}

func shouldUseSelfFollowerStats(n map[string]any) bool {
	return strings.TrimSpace(toString(mapValue(n["content"])["verb"])) == "关注了你"
}

func (f *notificationFormatter) formatSelfFollowerMeta(ctx context.Context) (string, error) {
	info, err := f.client.GetSelfInfo(ctx)
	if err != nil {
		return "", err
	}
	if followerCount := toString(info["follower_count"]); followerCount != "" {
		return "我的粉丝 " + display.FormatCount(info["follower_count"]), nil
	}
	urlToken := toString(info["url_token"])
	if urlToken == "" {
		return "", nil
	}
	profile, err := f.client.GetUserProfile(ctx, urlToken)
	if err != nil {
		return "", err
	}
	if followerCount := toString(profile["follower_count"]); followerCount != "" {
		return "我的粉丝 " + display.FormatCount(profile["follower_count"]), nil
	}
	return "", nil
}

func shouldUseCommentStats(n map[string]any, hasIncomingComment bool) bool {
	if hasIncomingComment {
		return false
	}
	return toString(mapValue(n["target"])["type"]) == "comment"
}

func (f *notificationFormatter) formatCommentMeta(ctx context.Context, n map[string]any) (string, error) {
	commentID := toString(mapValue(n["target"])["id"])
	if commentID == "" {
		return "", nil
	}
	cacheKey := "comment:" + commentID
	if cached, ok := f.targetCache[cacheKey]; ok {
		return cached, nil
	}
	data, err := f.client.GetComment(ctx, commentID)
	if err != nil {
		return "", err
	}
	meta := formatCommentStats(data)
	f.targetCache[cacheKey] = meta
	return meta, nil
}

func (f *notificationFormatter) formatTargetMeta(ctx context.Context, rawLink string) (string, error) {
	if rawLink == "" {
		return "", nil
	}
	if cached, ok := f.targetCache[rawLink]; ok {
		return cached, nil
	}
	target, ok := parseNotificationTarget(rawLink)
	if !ok {
		f.targetCache[rawLink] = ""
		return "", nil
	}
	var data map[string]any
	var err error
	switch target.kind {
	case "answer":
		data, err = f.client.GetAnswer(ctx, target.id)
	case "article":
		data, err = f.client.GetArticle(ctx, target.id)
	case "pin":
		data, err = f.client.GetPin(ctx, target.id)
	}
	if err != nil {
		return "", err
	}
	meta := formatTargetStats(target.kind, data)
	f.targetCache[rawLink] = meta
	return meta, nil
}

func formatTargetStats(kind string, data map[string]any) string {
	stats := make([]string, 0, 4)
	switch kind {
	case "answer":
		appendFirstCount(&stats, "赞同", data["voteup_count"])
		appendFirstCount(&stats, "收藏", data["favorite_count"], data["favlists_count"])
		appendFirstCount(&stats, "感谢", data["thanks_count"])
	case "article":
		appendFirstCount(&stats, "赞同", data["voteup_count"])
		appendFirstCount(&stats, "喜欢", data["liked_count"], data["like_count"])
		appendFirstCount(&stats, "收藏", data["favorite_count"], data["favlists_count"])
	case "pin":
		appendFirstCount(&stats, "赞同", data["reaction_count"], data["voteup_count"])
		appendFirstCount(&stats, "喜欢", data["like_count"], data["liked_count"])
		appendFirstCount(&stats, "收藏", data["favorite_count"], data["favlists_count"])
	}
	return strings.Join(stats, " · ")
}

func formatCommentStats(data map[string]any) string {
	stats := make([]string, 0, 1)
	appendFirstCount(&stats, "评论赞同", data["vote_count"], data["like_count"])
	return strings.Join(stats, " · ")
}

func appendFirstCount(stats *[]string, label string, values ...any) {
	value, ok := firstPresentAny(values...)
	if !ok {
		return
	}
	*stats = append(*stats, label+" "+display.FormatCount(value))
}

type notificationTarget struct {
	kind string
	id   string
}

func parseNotificationTarget(rawLink string) (notificationTarget, bool) {
	parsed, err := url.Parse(rawLink)
	if err != nil {
		return notificationTarget{}, false
	}
	path := strings.Trim(parsed.Path, "/")
	parts := strings.Split(path, "/")
	switch {
	case parsed.Host == "zhuanlan.zhihu.com" && len(parts) == 2 && parts[0] == "p" && parts[1] != "":
		return notificationTarget{kind: "article", id: parts[1]}, true
	case len(parts) == 2 && parts[0] == "pin" && parts[1] != "":
		return notificationTarget{kind: "pin", id: parts[1]}, true
	case len(parts) == 2 && parts[0] == "answer" && parts[1] != "":
		return notificationTarget{kind: "answer", id: parts[1]}, true
	case len(parts) >= 4 && parts[0] == "question" && parts[2] == "answer" && parts[3] != "":
		return notificationTarget{kind: "answer", id: parts[3]}, true
	default:
		return notificationTarget{}, false
	}
}

func notificationTargetLabel(rawLink string) string {
	target, ok := parseNotificationTarget(rawLink)
	if !ok {
		return "内容"
	}
	switch target.kind {
	case "pin":
		return "想法"
	case "answer":
		return "回答"
	case "article":
		return "文章"
	default:
		return "内容"
	}
}

func notificationState(n map[string]any) (string, string) {
	key := notificationGroupKey(n)
	if key == "" {
		key = notificationID(n)
	}
	if key == "" {
		key = formatNotificationBase(n)
	}
	return key, notificationSignature(n)
}

func rememberNotificationState(seen map[string]notificationSeenState, n map[string]any, now time.Time) {
	key, signature := notificationState(n)
	if key == "" {
		return
	}
	seen[key] = mergeNotificationSeenState(seen[key], newNotificationSeenState(n, signature, now))
}

func newNotificationSeenState(n map[string]any, signature string, now time.Time) notificationSeenState {
	createTime := notificationCreateTime(n)
	if createTime == 0 {
		createTime = now.Unix()
	}
	actorKeys := notificationActorKeys(n)
	state := notificationSeenState{
		signature:  signature,
		signatures: map[string]struct{}{},
		actors:     map[string]struct{}{},
		mergeCount: notificationMergeCountValue(n, len(actorKeys)),
		createTime: createTime,
	}
	if signature != "" {
		state.signatures[signature] = struct{}{}
	}
	for _, actorKey := range actorKeys {
		state.actors[actorKey] = struct{}{}
	}
	if len(state.actors) > state.mergeCount {
		state.mergeCount = len(state.actors)
	}
	return state
}

func mergeNotificationSeenState(current, incoming notificationSeenState) notificationSeenState {
	if incoming.createTime == 0 {
		return current
	}
	if current.createTime == 0 {
		return incoming
	}
	if current.signatures == nil {
		current.signatures = map[string]struct{}{}
	}
	if current.actors == nil {
		current.actors = map[string]struct{}{}
	}
	if current.signature != "" {
		current.signatures[current.signature] = struct{}{}
	}
	if incoming.signature != "" {
		current.signature = incoming.signature
		current.signatures[incoming.signature] = struct{}{}
	}
	for signature := range incoming.signatures {
		current.signatures[signature] = struct{}{}
	}
	for actorKey := range incoming.actors {
		current.actors[actorKey] = struct{}{}
	}
	if incoming.mergeCount > current.mergeCount {
		current.mergeCount = incoming.mergeCount
	}
	if len(current.actors) > current.mergeCount {
		current.mergeCount = len(current.actors)
	}
	if incoming.createTime > current.createTime {
		current.createTime = incoming.createTime
	}
	return current
}

func notificationKnownState(seen, pending map[string]notificationSeenState, key string) (notificationSeenState, bool) {
	state, ok := seen[key]
	if pendingState, pendingOK := pending[key]; pendingOK {
		state = mergeNotificationSeenState(state, pendingState)
		ok = true
	}
	return state, ok
}

func notificationSeenStateContains(state notificationSeenState, n map[string]any, signature string) (bool, string) {
	if _, ok := state.signatures[signature]; ok {
		return true, "same_signature"
	}
	actorKeys := notificationActorKeys(n)
	if len(actorKeys) == 0 {
		return false, "signature_changed"
	}
	for _, actorKey := range actorKeys {
		if _, ok := state.actors[actorKey]; !ok {
			return false, "new_actor"
		}
	}
	if mergeCount := notificationMergeCountValue(n, len(actorKeys)); mergeCount > state.mergeCount {
		return false, "merge_count_changed"
	}
	return true, "same_actors"
}

func pruneNotificationHistory(seen map[string]notificationSeenState, now time.Time) int {
	cutoff := now.Add(-notificationHistoryRetention).Unix()
	pruned := 0
	for key, state := range seen {
		if state.createTime < cutoff {
			delete(seen, key)
			pruned++
		}
	}
	return pruned
}

func notificationGroupKey(n map[string]any) string {
	content := mapValue(n["content"])
	contentTarget := mapValue(content["target"])
	target := mapValue(n["target"])
	verb := strings.TrimSpace(toString(content["verb"]))
	targetType, targetID := notificationGroupTarget(target, contentTarget)
	if verb == "" || targetID == "" {
		return ""
	}
	return strings.Join([]string{verb, targetType, targetID}, "|")
}

func notificationGroupTarget(target, contentTarget map[string]any) (string, string) {
	targetType := firstNonEmpty(toString(target["type"]), toString(target["resource_type"]))
	if targetType == "comment" {
		return targetType, firstNonEmpty(toString(target["id"]), toString(target["url_token"]), toString(contentTarget["link"]))
	}
	if parsed, ok := parseNotificationTarget(toString(contentTarget["link"])); ok {
		return parsed.kind, parsed.id
	}
	return targetType, firstNonEmpty(toString(target["id"]), toString(target["url_token"]), toString(contentTarget["link"]))
}

func notificationSignature(n map[string]any) string {
	actorKeys := notificationActorKeys(n)
	return strings.Join([]string{notificationMergeCount(n, len(actorKeys)), strings.Join(actorKeys, ",")}, "|")
}

func notificationActorKeys(n map[string]any) []string {
	content := mapValue(n["content"])
	actors := asSlice(content["actors"])
	actorKeys := make([]string, 0, len(actors))
	for _, raw := range actors {
		actor := mapValue(raw)
		actorKey := firstNonEmpty(toString(actor["url_token"]), toString(actor["link"]), toString(actor["name"]))
		if actorKey != "" {
			actorKeys = append(actorKeys, actorKey)
		}
	}
	sort.Strings(actorKeys)
	return actorKeys
}

func notificationMergeCount(n map[string]any, actorCount int) string {
	mergeCount := notificationMergeCountValue(n, actorCount)
	if mergeCount == 0 {
		return ""
	}
	return strconv.Itoa(mergeCount)
}

func notificationMergeCountValue(n map[string]any, actorCount int) int {
	mergeCount := toString(n["merge_count"])
	parsed, err := strconv.Atoi(mergeCount)
	if err != nil || parsed < 0 {
		parsed = 0
	}
	if actorCount > parsed {
		return actorCount
	}
	return parsed
}

func notificationID(n map[string]any) string {
	if id := toString(n["id"]); id != "" {
		return id
	}
	content := mapValue(n["content"])
	target := mapValue(content["target"])
	actors := asSlice(content["actors"])
	names := make([]string, 0, len(actors))
	for _, actor := range actors {
		names = append(names, toString(mapValue(actor)["url_token"]))
	}
	return strings.Join([]string{
		toString(n["type"]),
		toString(n["create_time"]),
		toString(content["verb"]),
		toString(target["link"]),
		strings.Join(names, ","),
	}, "|")
}

func oldestFirstNotifications(data []any) []any {
	ordered := append([]any(nil), data...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return notificationCreateTime(mapValue(ordered[i])) < notificationCreateTime(mapValue(ordered[j]))
	})
	return ordered
}

func notificationCreateTime(n map[string]any) int64 {
	t, err := strconv.ParseInt(toString(n["create_time"]), 10, 64)
	if err != nil {
		return 0
	}
	return t
}

func incomingCommentSnippet(n map[string]any) string {
	target := mapValue(n["target"])
	if toString(target["type"]) != "comment" {
		return ""
	}
	verb := toString(mapValue(n["content"])["verb"])
	if !shouldShowIncomingComment(verb) {
		return ""
	}
	return truncateWithDots(compactPlainText(toString(target["content"])), 140)
}

func shouldShowIncomingComment(verb string) bool {
	if strings.Contains(verb, "喜欢") {
		return false
	}
	return strings.Contains(verb, "评论") || strings.Contains(verb, "回复") || strings.Contains(verb, "提到")
}

func compactPlainText(text string) string {
	return strings.Join(strings.Fields(display.StripHTML(text)), " ")
}

func truncateWithDots(text string, maxLen int) string {
	if text == "" || maxLen <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return string(runes[:maxLen]) + "..."
}

func formatNotificationBase(n map[string]any) string {
	content := mapValue(n["content"])
	target := mapValue(content["target"])
	targetText := display.StripHTML(toString(target["text"]))
	verb := strings.TrimSpace(toString(content["verb"]))
	actors := asSlice(content["actors"])
	names := make([]string, 0, len(actors))
	for _, actor := range actors {
		name := toString(mapValue(actor)["name"])
		if name != "" {
			names = append(names, name)
		}
	}
	line := ""
	if len(names) > 0 && verb != "" {
		line = strings.Join(names, ", ") + " " + verb
	} else if targetText != "" {
		line = targetText
	} else {
		line = verb
	}
	if targetText != "" && line != targetText {
		line += " - " + targetText
	}
	if strings.TrimSpace(line) == "" {
		return "-"
	}
	return strings.TrimSpace(line)
}

func commandNameForKind(kind string) string {
	switch kind {
	case "answers":
		return "user-answers"
	case "articles":
		return "user-articles"
	default:
		return kind
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
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

func firstPresentAny(values ...any) (any, bool) {
	for _, value := range values {
		if toString(value) != "" {
			return value, true
		}
	}
	return nil, false
}

func firstMap(values ...any) any {
	for _, value := range values {
		if _, ok := asMap(value); ok {
			return value
		}
	}
	return map[string]any{}
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func mapValue(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return fmt.Sprint(x)
	}
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1"
	case int:
		return x != 0
	case float64:
		return x != 0
	default:
		return false
	}
}

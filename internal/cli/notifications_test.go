package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"zhihucli2/internal/client"
)

func testNotificationFormatter(t *testing.T, handler http.HandlerFunc) (*notificationFormatter, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	c := client.NewWithHTTP(map[string]string{"z_c0": "token"}, server.Client(), client.Endpoints{
		APIV4:       server.URL + "/api/v4",
		ZhuanlanAPI: server.URL + "/zhuanlan/api",
	})
	return newNotificationFormatter(c), func() {
		c.Close()
		server.Close()
	}
}

func writeNotificationTestJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("write json: %v", err)
	}
}

func TestParseNotificationTarget(t *testing.T) {
	tests := []struct {
		link string
		kind string
		id   string
		ok   bool
	}{
		{link: "https://www.zhihu.com/pin/123", kind: "pin", id: "123", ok: true},
		{link: "https://www.zhihu.com/answer/456", kind: "answer", id: "456", ok: true},
		{link: "https://www.zhihu.com/question/1/answer/456", kind: "answer", id: "456", ok: true},
		{link: "https://zhuanlan.zhihu.com/p/789", kind: "article", id: "789", ok: true},
		{link: "https://www.zhihu.com/question/1", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.link, func(t *testing.T) {
			got, ok := parseNotificationTarget(tt.link)
			if ok != tt.ok {
				t.Fatalf("ok=%v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if got.kind != tt.kind || got.id != tt.id {
				t.Fatalf("target=%+v, want kind=%s id=%s", got, tt.kind, tt.id)
			}
		})
	}
}

func TestFormatActorWithProfile(t *testing.T) {
	tests := []struct {
		name    string
		profile map[string]any
		want    string
	}{
		{
			name:    "alice",
			profile: map[string]any{"is_following": true, "is_followed": true, "follower_count": 12000},
			want:    "alice（互相关注，粉丝 1.2万）",
		},
		{
			name:    "bob",
			profile: map[string]any{"is_following": true, "is_followed": false, "follower_count": 27},
			want:    "bob（我关注，粉丝 27）",
		},
		{
			name:    "carol",
			profile: map[string]any{"is_following": false, "is_followed": true, "follower_count": 0},
			want:    "carol（关注我，粉丝 0）",
		},
		{
			name:    "dave",
			profile: map[string]any{},
			want:    "dave",
		},
	}
	for _, tt := range tests {
		if got := formatActorWithProfile(tt.name, tt.profile); got != tt.want {
			t.Fatalf("formatActorWithProfile=%q, want %q", got, tt.want)
		}
	}
}

func TestNotificationFormatterActorCacheUsesTTL(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	calls := 0
	formatter, closeServer := testNotificationFormatter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/members/alice" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		counts := []int{1, 2}
		if calls >= len(counts) {
			t.Fatalf("unexpected request %d", calls+1)
		}
		count := counts[calls]
		calls++
		writeNotificationTestJSON(t, w, http.StatusOK, map[string]any{"follower_count": count})
	})
	defer closeServer()
	formatter.now = func() time.Time { return now }
	actors := []any{map[string]any{"name": "Alice", "url_token": "alice"}}

	first, err := formatter.formatActors(context.Background(), actors)
	if err != nil {
		t.Fatalf("first formatActors: %v", err)
	}
	if first != "Alice（粉丝 1）" {
		t.Fatalf("first=%q", first)
	}
	now = now.Add(23 * time.Hour)
	second, err := formatter.formatActors(context.Background(), actors)
	if err != nil {
		t.Fatalf("second formatActors: %v", err)
	}
	if second != "Alice（粉丝 1）" {
		t.Fatalf("second=%q", second)
	}
	if calls != 1 {
		t.Fatalf("calls=%d, want 1 before TTL expires", calls)
	}
	now = now.Add(2 * time.Hour)
	third, err := formatter.formatActors(context.Background(), actors)
	if err != nil {
		t.Fatalf("third formatActors: %v", err)
	}
	if third != "Alice（粉丝 2）" {
		t.Fatalf("third=%q", third)
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want 2 after TTL expires", calls)
	}
}

func TestFormatTargetStats(t *testing.T) {
	tests := []struct {
		name string
		kind string
		data map[string]any
		want string
	}{
		{
			name: "answer",
			kind: "answer",
			data: map[string]any{"voteup_count": 19, "favlists_count": 2, "thanks_count": 1},
			want: "赞同 19 · 收藏 2 · 感谢 1",
		},
		{
			name: "article zero like",
			kind: "article",
			data: map[string]any{"voteup_count": 2, "liked_count": 0, "favlists_count": 1},
			want: "赞同 2 · 喜欢 0 · 收藏 1",
		},
		{
			name: "pin",
			kind: "pin",
			data: map[string]any{"reaction_count": 19, "like_count": 19, "favorite_count": 5},
			want: "赞同 19 · 喜欢 19 · 收藏 5",
		},
		{
			name: "missing fields",
			kind: "article",
			data: map[string]any{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTargetStats(tt.kind, tt.data); got != tt.want {
				t.Fatalf("formatTargetStats=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestNotificationFormatterClearTargetCacheRefreshesTargetMeta(t *testing.T) {
	calls := 0
	formatter, closeServer := testNotificationFormatter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/pins/123" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		counts := []int{26, 27}
		if calls >= len(counts) {
			t.Fatalf("unexpected request %d", calls+1)
		}
		count := counts[calls]
		calls++
		writeNotificationTestJSON(t, w, http.StatusOK, map[string]any{
			"reaction_count": count,
			"like_count":     count,
			"favorite_count": 12,
		})
	})
	defer closeServer()

	link := "https://www.zhihu.com/pin/123"
	first, err := formatter.formatTargetMeta(context.Background(), link)
	if err != nil {
		t.Fatalf("first formatTargetMeta: %v", err)
	}
	if first != "赞同 26 · 喜欢 26 · 收藏 12" {
		t.Fatalf("first=%q", first)
	}
	formatter.clearTargetCache()
	second, err := formatter.formatTargetMeta(context.Background(), link)
	if err != nil {
		t.Fatalf("second formatTargetMeta: %v", err)
	}
	if second != "赞同 27 · 喜欢 27 · 收藏 12" {
		t.Fatalf("second=%q", second)
	}
}

func TestFormatCommentStats(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
		want string
	}{
		{
			name: "vote count",
			data: map[string]any{"vote_count": 7},
			want: "评论赞同 7",
		},
		{
			name: "like count fallback",
			data: map[string]any{"like_count": 3},
			want: "评论赞同 3",
		},
		{
			name: "zero",
			data: map[string]any{"vote_count": 0},
			want: "评论赞同 0",
		},
		{
			name: "missing",
			data: map[string]any{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCommentStats(tt.data); got != tt.want {
				t.Fatalf("formatCommentStats=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatSelfFollowerMeta(t *testing.T) {
	formatter, closeServer := testNotificationFormatter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/me" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		writeNotificationTestJSON(t, w, http.StatusOK, map[string]any{"follower_count": 12345})
	})
	defer closeServer()

	got, err := formatter.formatSelfFollowerMeta(context.Background())
	if err != nil {
		t.Fatalf("formatSelfFollowerMeta: %v", err)
	}
	if want := "我的粉丝 1.2万"; got != want {
		t.Fatalf("formatSelfFollowerMeta=%q, want %q", got, want)
	}
}

func TestFormatSelfFollowerMetaFallsBackToProfile(t *testing.T) {
	formatter, closeServer := testNotificationFormatter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/me":
			writeNotificationTestJSON(t, w, http.StatusOK, map[string]any{"url_token": "me"})
		case "/api/v4/members/me":
			writeNotificationTestJSON(t, w, http.StatusOK, map[string]any{"follower_count": 99})
		default:
			t.Fatalf("path=%s", r.URL.Path)
		}
	})
	defer closeServer()

	got, err := formatter.formatSelfFollowerMeta(context.Background())
	if err != nil {
		t.Fatalf("formatSelfFollowerMeta: %v", err)
	}
	if want := "我的粉丝 99"; got != want {
		t.Fatalf("formatSelfFollowerMeta=%q, want %q", got, want)
	}
}

func TestShouldUseSelfFollowerStats(t *testing.T) {
	if !shouldUseSelfFollowerStats(map[string]any{"content": map[string]any{"verb": "关注了你"}}) {
		t.Fatal("follow notification should use self follower stats")
	}
	if shouldUseSelfFollowerStats(map[string]any{"content": map[string]any{"verb": "赞同了你的回答"}}) {
		t.Fatal("non-follow notification should not use self follower stats")
	}
}

func TestShouldUseCommentStats(t *testing.T) {
	n := map[string]any{"target": map[string]any{"type": "comment"}}
	if !shouldUseCommentStats(n, false) {
		t.Fatal("comment notification without incoming comment should use comment stats")
	}
	if shouldUseCommentStats(n, true) {
		t.Fatal("incoming comment notification should keep target stats")
	}
	if shouldUseCommentStats(map[string]any{"target": map[string]any{"type": "answer"}}, false) {
		t.Fatal("answer notification should not use comment stats")
	}
}

func TestOldestFirstNotifications(t *testing.T) {
	input := []any{
		map[string]any{"id": "newest", "create_time": 300},
		map[string]any{"id": "oldest", "create_time": 100},
		map[string]any{"id": "middle", "create_time": 200},
	}
	got := oldestFirstNotifications(input)
	want := []string{"oldest", "middle", "newest"}
	for i, id := range want {
		if gotID := toString(mapValue(got[i])["id"]); gotID != id {
			t.Fatalf("ordered[%d]=%q, want %q", i, gotID, id)
		}
	}
	if gotID := toString(mapValue(input[0])["id"]); gotID != "newest" {
		t.Fatalf("oldestFirstNotifications mutated input, first id=%q", gotID)
	}
}

func TestIncomingCommentSnippet(t *testing.T) {
	long := strings.Repeat("字", 141)
	tests := []struct {
		name string
		n    map[string]any
		want string
	}{
		{
			name: "reply to me",
			n: map[string]any{
				"content": map[string]any{"verb": "回复了你的评论"},
				"target":  map[string]any{"type": "comment", "content": "<p>你好<br>世界</p>"},
			},
			want: "你好世界",
		},
		{
			name: "long comment",
			n: map[string]any{
				"content": map[string]any{"verb": "评论了你的回答"},
				"target":  map[string]any{"type": "comment", "content": "<p>" + long + "</p>"},
			},
			want: strings.Repeat("字", 140) + "...",
		},
		{
			name: "hidden URL prefix",
			n: map[string]any{
				"content": map[string]any{"verb": "回复了你的评论"},
				"target": map[string]any{
					"type":    "comment",
					"content": `用啥<a href="https://link.zhihu.com/" class="external"><span class="invisible">https://</span><span class="visible">1.6</span><span class="invisible"></span></a> 上<a href="https://link.zhihu.com/" class="external"><span class="invisible">https://</span><span class="visible">2.0</span></a> lite`,
				},
			},
			want: "用啥1.6 上2.0 lite",
		},
		{
			name: "like my comment",
			n: map[string]any{
				"content": map[string]any{"verb": "喜欢了你的评论"},
				"target":  map[string]any{"type": "comment", "content": "<p>这是我自己的评论</p>"},
			},
			want: "",
		},
		{
			name: "answer notification",
			n: map[string]any{
				"content": map[string]any{"verb": "赞同了你的回答"},
				"target":  map[string]any{"type": "answer", "content": "<p>不是评论</p>"},
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := incomingCommentSnippet(tt.n); got != tt.want {
				t.Fatalf("incomingCommentSnippet=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestNotificationTargetLabel(t *testing.T) {
	tests := []struct {
		link string
		want string
	}{
		{link: "https://www.zhihu.com/pin/123", want: "想法"},
		{link: "https://www.zhihu.com/answer/456", want: "回答"},
		{link: "https://zhuanlan.zhihu.com/p/789", want: "文章"},
		{link: "https://www.zhihu.com/question/123", want: "内容"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := notificationTargetLabel(tt.link); got != tt.want {
				t.Fatalf("notificationTargetLabel=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestNotificationGroupKeyIgnoresNotificationID(t *testing.T) {
	first := map[string]any{
		"id": "old-notification",
		"content": map[string]any{
			"verb": "赞同了你的想法",
			"target": map[string]any{
				"link": "https://www.zhihu.com/pin/123",
			},
		},
		"target": map[string]any{
			"type": "pin",
			"id":   "123",
		},
	}
	second := map[string]any{
		"id": "new-notification",
		"content": map[string]any{
			"verb": "赞同了你的想法",
			"target": map[string]any{
				"link": "https://www.zhihu.com/pin/123",
			},
		},
		"target": map[string]any{
			"type": "pin",
			"id":   "123",
		},
	}
	if got, want := notificationGroupKey(first), notificationGroupKey(second); got != want {
		t.Fatalf("group keys differ: %q != %q", got, want)
	}
}

func TestNotificationGroupKeyUsesStableLinkedTarget(t *testing.T) {
	first := map[string]any{
		"content": map[string]any{
			"verb": "赞同了你的回答",
			"target": map[string]any{
				"link": "https://www.zhihu.com/question/1/answer/456",
			},
		},
		"target": map[string]any{
			"type": "answer",
			"id":   "volatile-id",
		},
	}
	second := map[string]any{
		"content": map[string]any{
			"verb": "赞同了你的回答",
			"target": map[string]any{
				"link": "https://www.zhihu.com/question/1/answer/456",
			},
		},
		"target": map[string]any{
			"resource_type": "answer",
		},
	}
	if got, want := notificationGroupKey(first), notificationGroupKey(second); got != want {
		t.Fatalf("group keys differ: %q != %q", got, want)
	}
}

func TestNotificationGroupKeyKeepsDifferentCommentsSeparate(t *testing.T) {
	first := map[string]any{
		"content": map[string]any{
			"verb": "喜欢了你的评论",
			"target": map[string]any{
				"link": "https://www.zhihu.com/question/1/answer/456",
			},
		},
		"target": map[string]any{
			"type": "comment",
			"id":   "comment-a",
		},
	}
	second := map[string]any{
		"content": map[string]any{
			"verb": "喜欢了你的评论",
			"target": map[string]any{
				"link": "https://www.zhihu.com/question/1/answer/456",
			},
		},
		"target": map[string]any{
			"type": "comment",
			"id":   "comment-b",
		},
	}
	if notificationGroupKey(first) == notificationGroupKey(second) {
		t.Fatal("different comment notifications should not share a group key")
	}
}

func TestNotificationSignatureTracksMergedActors(t *testing.T) {
	oneActor := map[string]any{
		"merge_count": 1,
		"content": map[string]any{"actors": []any{
			map[string]any{"url_token": "alice"},
		}},
	}
	twoActors := map[string]any{
		"merge_count": 2,
		"content": map[string]any{"actors": []any{
			map[string]any{"url_token": "bob"},
			map[string]any{"url_token": "alice"},
		}},
	}
	if notificationSignature(oneActor) == notificationSignature(twoActors) {
		t.Fatal("signature should change when merged actors change")
	}
	reordered := map[string]any{
		"merge_count": 2,
		"content": map[string]any{"actors": []any{
			map[string]any{"url_token": "alice"},
			map[string]any{"url_token": "bob"},
		}},
	}
	if got, want := notificationSignature(twoActors), notificationSignature(reordered); got != want {
		t.Fatalf("signature should ignore actor order: %q != %q", got, want)
	}
}

func TestNotificationSignatureNormalizesSingleActorMergeCount(t *testing.T) {
	withoutMergeCount := map[string]any{
		"content": map[string]any{"actors": []any{
			map[string]any{"url_token": "alice"},
		}},
	}
	withMergeCount := map[string]any{
		"merge_count": 1,
		"content": map[string]any{"actors": []any{
			map[string]any{"url_token": "alice"},
		}},
	}
	if got, want := notificationSignature(withoutMergeCount), notificationSignature(withMergeCount); got != want {
		t.Fatalf("signatures differ: %q != %q", got, want)
	}
}

func TestNotificationSeenStateTracksMultipleActorsForSameGroup(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	notification := func(actor string) map[string]any {
		return map[string]any{
			"create_time": 123,
			"merge_count": 1,
			"content": map[string]any{
				"verb": "赞同了你的回答",
				"target": map[string]any{
					"link": "https://www.zhihu.com/question/1/answer/456",
					"text": "问题标题",
				},
				"actors": []any{
					map[string]any{"url_token": actor},
				},
			},
			"target": map[string]any{
				"type": "answer",
				"id":   "456",
			},
		}
	}
	seen := map[string]notificationSeenState{}
	rememberNotificationState(seen, notification("swz128"), now)
	rememberNotificationState(seen, notification("z-buffer"), now)

	key, signature := notificationState(notification("swz128"))
	state, ok := seen[key]
	if !ok {
		t.Fatal("state should be stored")
	}
	if known, reason := notificationSeenStateContains(state, notification("swz128"), signature); !known || reason != "same_signature" {
		t.Fatalf("swz128 known=%v reason=%s", known, reason)
	}
	_, zSignature := notificationState(notification("z-buffer"))
	if known, reason := notificationSeenStateContains(state, notification("z-buffer"), zSignature); !known || reason != "same_signature" {
		t.Fatalf("z-buffer known=%v reason=%s", known, reason)
	}
	_, newSignature := notificationState(notification("alice"))
	if known, reason := notificationSeenStateContains(state, notification("alice"), newSignature); known || reason != "new_actor" {
		t.Fatalf("alice known=%v reason=%s", known, reason)
	}
	if state.mergeCount != 2 {
		t.Fatalf("mergeCount=%d, want 2", state.mergeCount)
	}
}

func TestNotificationSeenStateUsesNotificationIDAlias(t *testing.T) {
	now := time.Date(2026, 7, 10, 11, 21, 0, 0, time.Local)
	complete := map[string]any{
		"id":          "notification-1",
		"create_time": 123,
		"merge_count": 1,
		"content": map[string]any{
			"verb": "喜欢了你的评论",
			"target": map[string]any{
				"link": "https://www.zhihu.com/answer/456",
				"text": "问题标题",
			},
			"actors": []any{
				map[string]any{"url_token": "lin-zhao-mou"},
			},
		},
		"target": map[string]any{
			"type":          "comment",
			"id":            "comment-1",
			"resource_type": "answer",
		},
	}
	incomplete := map[string]any{
		"id":          "notification-1",
		"create_time": 123,
		"merge_count": 1,
		"content": map[string]any{
			"verb": "喜欢了你的评论",
			"target": map[string]any{
				"text": "问题标题",
			},
			"actors": []any{
				map[string]any{"url_token": "lin-zhao-mou"},
			},
		},
	}
	seen := map[string]notificationSeenState{}
	rememberNotificationState(seen, complete, now)

	key, signature := notificationState(incomplete)
	if key != "notification-1" {
		t.Fatalf("incomplete key=%q, want notification id", key)
	}
	state, ok := notificationKnownState(seen, nil, key)
	if !ok {
		t.Fatal("notification id alias should be known")
	}
	if known, reason := notificationSeenStateContains(state, incomplete, signature); !known || reason != "same_signature" {
		t.Fatalf("known=%v reason=%s", known, reason)
	}
}

func TestPruneNotificationHistory(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	seen := map[string]notificationSeenState{
		"recent": {
			signature:  "a",
			createTime: now.Add(-89 * 24 * time.Hour).Unix(),
		},
		"old": {
			signature:  "b",
			createTime: now.Add(-91 * 24 * time.Hour).Unix(),
		},
	}
	if pruned := pruneNotificationHistory(seen, now); pruned != 1 {
		t.Fatalf("pruned=%d, want 1", pruned)
	}
	if _, ok := seen["recent"]; !ok {
		t.Fatal("recent notification state should be retained")
	}
	if _, ok := seen["old"]; ok {
		t.Fatal("old notification state should be pruned")
	}
}

func TestNewNotificationSeenStateUsesNotificationCreateTime(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	state := newNotificationSeenState(map[string]any{"create_time": 123}, "sig", now)
	if state.signature != "sig" || state.createTime != 123 {
		t.Fatalf("state=%+v", state)
	}
	fallback := newNotificationSeenState(map[string]any{}, "sig", now)
	if fallback.createTime != now.Unix() {
		t.Fatalf("fallback createTime=%d, want %d", fallback.createTime, now.Unix())
	}
}

func TestTerminalNotificationSequence(t *testing.T) {
	if got := terminalNotificationSequence(); got != "\a" {
		t.Fatalf("terminalNotificationSequence=%q, want BEL", got)
	}
}

func TestShouldSendNotificationBell(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.Local)
	if !shouldSendNotificationBell(now, time.Time{}) {
		t.Fatal("first notification should send bell")
	}
	if shouldSendNotificationBell(now.Add(59*time.Minute), now) {
		t.Fatal("bell should be throttled within one hour")
	}
	if !shouldSendNotificationBell(now.Add(time.Hour), now) {
		t.Fatal("bell should be sent again after one hour")
	}
}

func TestMonitorLines(t *testing.T) {
	tm := time.Date(2026, 7, 8, 15, 4, 5, 0, time.Local)
	if got, want := monitorStatusLineWithColumns(tm, "no new notifications", "next refresh in 42s", 100), "\r\033[2KLast check: 15:04:05 · next refresh in 42s · no new notifications"; got != want {
		t.Fatalf("monitorStatusLine=%q, want %q", got, want)
	}
	if got, want := monitorStatusLineWithColumns(tm, "error: API request failed\nwith status 500:", "next refresh in 42s", 100), "\r\033[2KLast check: 15:04:05 · next refresh in 42s · error: API request failed with status 500:"; got != want {
		t.Fatalf("monitorStatusLine error=%q, want %q", got, want)
	}
	if got, want := monitorStatusLineWithColumns(tm, "waiting", "refreshing", 100), "\r\033[2KLast check: 15:04:05 · refreshing · waiting"; got != want {
		t.Fatalf("monitorStatusLine refreshing=%q, want %q", got, want)
	}
	if got, want := monitorNewSeparator(tm, 2, false), "\r\033[2K\n----- New notifications @ 15:04:05 (2 new) -----\n"; got != want {
		t.Fatalf("monitorNewSeparator=%q, want %q", got, want)
	}
	if got, want := monitorNewSeparator(tm, 2, true), "\r\033[2K\n----- 🔔 New notifications @ 15:04:05 (2 new) -----\n"; got != want {
		t.Fatalf("monitorNewSeparator bell=%q, want %q", got, want)
	}
}

func TestMonitorStatusLineTruncatesLongStatus(t *testing.T) {
	tm := time.Date(2026, 7, 8, 15, 4, 5, 0, time.Local)
	status := "refresh failed: " + strings.Repeat("x", 80)
	want := "\r\033[2KLast check: 15:04:05 · next refresh in 42s · refresh failed: xxxxxx..."
	if got := monitorStatusLineWithColumns(tm, status, "next refresh in 42s", 70); got != want {
		t.Fatalf("monitorStatusLine=%q, want %q", got, want)
	}
}

func TestMonitorOutputUpdatesCountdown(t *testing.T) {
	var out strings.Builder
	tm := time.Date(2026, 7, 8, 15, 4, 5, 0, time.Local)
	monitor := newMonitorOutput(&out)
	monitor.Status(tm, "waiting", "next refresh in 60s")
	first := monitorStatusLine(tm, "waiting", "next refresh in 60s")
	monitor.Tick("next refresh in 59s")
	want := first + monitorClearStatus(monitorStatusRows(first)) + monitorStatusLine(tm, "waiting", "next refresh in 59s")
	if got := out.String(); got != want {
		t.Fatalf("output=%q, want %q", got, want)
	}
}

func TestMonitorRefreshStatus(t *testing.T) {
	now := time.Date(2026, 7, 8, 15, 4, 5, 0, time.Local)
	tests := []struct {
		name            string
		next            time.Time
		refreshInFlight bool
		want            string
	}{
		{name: "full minute", next: now.Add(time.Minute), want: "next refresh in 60s"},
		{name: "round up partial second", next: now.Add(59001 * time.Millisecond), want: "next refresh in 60s"},
		{name: "whole seconds", next: now.Add(59 * time.Second), want: "next refresh in 59s"},
		{name: "due", next: now, want: "next refresh in 0s"},
		{name: "refreshing", refreshInFlight: true, want: "refreshing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := monitorRefreshStatus(now, tt.next, tt.refreshInFlight); got != tt.want {
				t.Fatalf("monitorRefreshStatus=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestMonitorStatusRowsAndClear(t *testing.T) {
	if got := monitorStatusRowsWithColumns("\r\033[2K12345678901", 10); got != 2 {
		t.Fatalf("monitorStatusRows=%d, want 2", got)
	}
	if got, want := monitorClearStatus(3), "\r\033[2K\033[1A\r\033[2K\033[1A\r\033[2K"; got != want {
		t.Fatalf("monitorClearStatus=%q, want %q", got, want)
	}
}

func TestMonitorOutputClearsPreviousStatusBeforeSeparator(t *testing.T) {
	var out strings.Builder
	tm := time.Date(2026, 7, 8, 15, 4, 5, 0, time.Local)
	monitor := &monitorOutput{out: &out, statusRows: 3}
	monitor.NewSeparator(tm, 1, true)
	want := monitorClearStatus(3) + monitorNewSeparator(tm, 1, true)
	if got := out.String(); got != want {
		t.Fatalf("output=%q, want %q", got, want)
	}
}

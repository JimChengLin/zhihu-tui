package cli

import (
	"strings"
	"testing"
	"time"
)

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

func TestTerminalNotificationSequence(t *testing.T) {
	if got := terminalNotificationSequence(); got != "\a" {
		t.Fatalf("terminalNotificationSequence=%q, want BEL", got)
	}
}

func TestMonitorLines(t *testing.T) {
	tm := time.Date(2026, 7, 8, 15, 4, 5, 0, time.Local)
	if got, want := monitorStatusLine(tm, "no new notifications"), "\r\033[2KLast check: 15:04:05 · no new notifications"; got != want {
		t.Fatalf("monitorStatusLine=%q, want %q", got, want)
	}
	if got, want := monitorStatusLine(tm, "error: API request failed\nwith status 500:"), "\r\033[2KLast check: 15:04:05 · error: API request failed with status 500:"; got != want {
		t.Fatalf("monitorStatusLine error=%q, want %q", got, want)
	}
	if got, want := monitorNewSeparator(tm, 2), "\r\033[2K----- New notifications @ 15:04:05 (2 new) -----\n"; got != want {
		t.Fatalf("monitorNewSeparator=%q, want %q", got, want)
	}
}

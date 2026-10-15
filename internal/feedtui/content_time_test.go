package feedtui

import (
	"strings"
	"testing"
	"time"
)

func TestContentTimeUsesTargetCreationAndUpdateTimes(t *testing.T) {
	publishedAt := time.Date(2026, time.January, 31, 11, 58, 0, 0, time.Local).Unix()
	updatedAt := time.Date(2026, time.October, 8, 20, 33, 0, 0, time.Local).Unix()
	activityAt := time.Date(2026, time.October, 15, 16, 0, 0, 0, time.Local).Unix()
	for _, tt := range []struct {
		name          string
		times         map[string]any
		wantPublished int64
		wantUpdated   int64
		want          string
	}{
		{
			name:          "updated_time",
			times:         map[string]any{"created_time": publishedAt, "updated_time": updatedAt},
			wantPublished: publishedAt,
			wantUpdated:   updatedAt,
			want:          "编辑于 2026-10-08 20:33",
		},
		{
			name:          "created_and_updated_aliases",
			times:         map[string]any{"created": publishedAt, "updated": updatedAt},
			wantPublished: publishedAt,
			wantUpdated:   updatedAt,
			want:          "编辑于 2026-10-08 20:33",
		},
		{
			name:          "without_update",
			times:         map[string]any{"created_time": publishedAt},
			wantPublished: publishedAt,
			want:          "发布于 2026-01-31 11:58",
		},
		{
			name:          "update_equals_creation",
			times:         map[string]any{"created_time": publishedAt, "updated_time": publishedAt},
			wantPublished: publishedAt,
			wantUpdated:   publishedAt,
			want:          "发布于 2026-01-31 11:58",
		},
		{
			name:          "update_precedes_creation",
			times:         map[string]any{"created_time": updatedAt, "updated_time": publishedAt},
			wantPublished: updatedAt,
			wantUpdated:   publishedAt,
			want:          "发布于 2026-10-08 20:33",
		},
		{
			name:        "without_creation",
			times:       map[string]any{"updated": updatedAt},
			wantUpdated: updatedAt,
			want:        "编辑于 2026-10-08 20:33",
		},
		{
			name: "without_target_times",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target := map[string]any{
				"id":      "456",
				"type":    "answer",
				"content": "<p>正文最后一行</p>",
				"question": map[string]any{
					"id":           "123",
					"title":        "测试问题",
					"created_time": publishedAt - 86400,
					"updated_time": updatedAt + 86400,
				},
			}
			for key, value := range tt.times {
				target[key] = value
			}
			item, ok := parseFeedItem(map[string]any{
				"id":           "activity-1",
				"created_time": activityAt,
				"updated_time": activityAt + 60,
				"updated":      activityAt + 120,
				"target":       target,
			})
			if !ok {
				t.Fatal("parseFeedItem returned false")
			}
			if item.publishedAt != tt.wantPublished || item.updatedAt != tt.wantUpdated {
				t.Fatalf("content times=(%d, %d), want (%d, %d)", item.publishedAt, item.updatedAt, tt.wantPublished, tt.wantUpdated)
			}
			if item.createdAt != activityAt {
				t.Fatalf("activity time=%d, want %d", item.createdAt, activityAt)
			}

			model := &app{items: []feedItem{item}, width: 100, height: 24}
			lines, _ := renderSingleApp(model)
			contentRow, timeRow := -1, -1
			var contentTimes []string
			for row, line := range lines {
				text := strings.TrimSpace(styledLineText(line))
				if text == "正文最后一行" {
					contentRow = row
				}
				if !strings.HasPrefix(text, "编辑于 ") && !strings.HasPrefix(text, "发布于 ") {
					continue
				}
				contentTimes = append(contentTimes, text)
				timeRow = row
				if line.style != ansiDim || !line.noPageAnchor {
					t.Fatalf("content time must be dim and excluded from page anchors: %#v", line)
				}
			}
			if tt.want == "" {
				if len(contentTimes) != 0 {
					t.Fatalf("missing target times rendered content time: %q", contentTimes)
				}
				return
			}
			if len(contentTimes) != 1 || contentTimes[0] != tt.want {
				t.Fatalf("content times=%q, want [%q]", contentTimes, tt.want)
			}
			if contentRow < 0 || timeRow <= contentRow {
				t.Fatalf("content row=%d time row=%d", contentRow, timeRow)
			}
		})
	}
}

func TestCommentViewOmitsEditedContentTime(t *testing.T) {
	item, ok := parseFeedItem(map[string]any{
		"target": map[string]any{
			"id":           "456",
			"type":         "answer",
			"content":      "正文内容",
			"created_time": time.Date(2026, time.January, 31, 11, 58, 0, 0, time.Local).Unix(),
			"updated_time": time.Date(2026, time.October, 8, 20, 33, 0, 0, time.Local).Unix(),
			"question":     map[string]any{"id": "123", "title": "测试问题"},
		},
	})
	if !ok {
		t.Fatal("parseFeedItem returned false")
	}
	model := &app{items: []feedItem{item}, width: 100, height: 24, commentMode: true}
	lines, _ := renderSingleApp(model)
	for _, line := range lines {
		text := strings.TrimSpace(styledLineText(line))
		if strings.HasPrefix(text, "编辑于 ") || strings.HasPrefix(text, "发布于 ") {
			t.Fatalf("comment view repeats content time: %q", text)
		}
	}
}

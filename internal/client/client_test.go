package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	endpoints := Endpoints{
		APIV4:             server.URL + "/api/v4",
		APIV3:             server.URL + "/api/v3",
		ZhuanlanAPI:       server.URL + "/zhuanlan/api",
		ImageAPI:          server.URL + "/images",
		OSSUploadURL:      server.URL + "/oss",
		ContentPublishURL: server.URL + "/api/v4/content/publish",
		ContentDraftsURL:  server.URL + "/api/v4/content/drafts",
	}
	c := NewWithHTTP(map[string]string{"z_c0": "token", "_xsrf": "xsrf", "d_c0": "device"}, server.Client(), endpoints)
	c.pollInterval = 0
	return c, server
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("write json: %v", err)
	}
}

func TestGetSelfInfoSendsCookiesAndHeaders(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/me" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("Cookie"), "z_c0=token") {
			t.Fatalf("missing cookie header: %q", r.Header.Get("Cookie"))
		}
		if r.Header.Get("x-xsrftoken") != "xsrf" {
			t.Fatalf("x-xsrftoken=%q", r.Header.Get("x-xsrftoken"))
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"name": "TestUser", "answer_count": 42})
	})
	defer server.Close()

	info, err := c.GetSelfInfo(context.Background())
	if err != nil {
		t.Fatalf("GetSelfInfo: %v", err)
	}
	if info["name"] != "TestUser" {
		t.Fatalf("name=%v", info["name"])
	}
}

func TestGetUserProfileIncludesRelationshipFields(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/members/alice" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		include := r.URL.Query().Get("include")
		if !strings.Contains(include, "is_following") || !strings.Contains(include, "is_followed") {
			t.Fatalf("include=%q", include)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"name": "alice", "follower_count": 12, "is_following": true, "is_followed": false})
	})
	defer server.Close()

	profile, err := c.GetUserProfile(context.Background(), "alice")
	if err != nil {
		t.Fatalf("GetUserProfile: %v", err)
	}
	if profile["is_following"] != true {
		t.Fatalf("is_following=%v", profile["is_following"])
	}
}

func TestGetPinArticleAndComment(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/pins/123":
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 123, "reaction_count": 9})
		case "/zhuanlan/api/articles/456":
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 456, "voteup_count": 8})
		case "/api/v4/comments/789":
			writeJSON(t, w, http.StatusOK, map[string]any{"id": 789, "vote_count": 7})
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	})
	defer server.Close()

	pin, err := c.GetPin(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetPin: %v", err)
	}
	if pin["reaction_count"].(json.Number).String() != "9" {
		t.Fatalf("pin=%#v", pin)
	}
	article, err := c.GetArticle(context.Background(), "456")
	if err != nil {
		t.Fatalf("GetArticle: %v", err)
	}
	if article["voteup_count"].(json.Number).String() != "8" {
		t.Fatalf("article=%#v", article)
	}
	comment, err := c.GetComment(context.Background(), "789")
	if err != nil {
		t.Fatalf("GetComment: %v", err)
	}
	if comment["vote_count"].(json.Number).String() != "7" {
		t.Fatalf("comment=%#v", comment)
	}
}

func TestGetSelfInfoStatusErrors(t *testing.T) {
	tests := []struct {
		status  int
		want    any
		message string
	}{
		{http.StatusUnauthorized, LoginError{}, ""},
		{http.StatusNotFound, DataFetchError{}, "err"},
		{http.StatusForbidden, DataFetchError{}, "err"},
		{http.StatusInternalServerError, DataFetchError{}, "err"},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("err"))
			})
			defer server.Close()
			_, err := c.GetSelfInfo(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}
			switch tt.want.(type) {
			case LoginError:
				var target LoginError
				if !errors.As(err, &target) {
					t.Fatalf("err=%T, want LoginError", err)
				}
			case DataFetchError:
				var target DataFetchError
				if !errors.As(err, &target) {
					t.Fatalf("err=%T, want DataFetchError", err)
				}
				if target.StatusCode != tt.status {
					t.Fatalf("status code=%d, want %d", target.StatusCode, tt.status)
				}
				if got, want := IsNotFoundError(err), tt.status == http.StatusNotFound; got != want {
					t.Fatalf("IsNotFoundError=%v, want %v", got, want)
				}
			}
			if tt.message != "" && !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("err=%q, want response body %q", err, tt.message)
			}
		})
	}
}

func TestSearchParams(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/search_v3" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "Go" {
			t.Fatalf("q=%q", r.URL.Query().Get("q"))
		}
		if r.URL.Query().Get("limit") != "5" {
			t.Fatalf("limit=%q", r.URL.Query().Get("limit"))
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"data": []any{map[string]any{"id": 1}}})
	})
	defer server.Close()

	result, err := c.Search(context.Background(), "Go", "general", 0, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result["data"].([]any)) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestGetFollowingFeedUsesMomentsAndPagingURL(t *testing.T) {
	requestNumber := 0
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requestNumber++
		if r.URL.Path != "/api/v3/moments" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if requestNumber == 1 {
			if r.URL.Query().Get("limit") != "10" {
				t.Fatalf("limit=%q", r.URL.Query().Get("limit"))
			}
		} else if r.URL.Query().Get("after_id") != "next-page" {
			t.Fatalf("after_id=%q", r.URL.Query().Get("after_id"))
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"data":   []any{map[string]any{"id": requestNumber}},
			"paging": map[string]any{"is_end": requestNumber == 2},
		})
	})
	defer server.Close()

	first, err := c.GetFollowingFeed(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("GetFollowingFeed initial: %v", err)
	}
	if len(first["data"].([]any)) != 1 {
		t.Fatalf("first=%#v", first)
	}
	nextURL := server.URL + "/api/v3/moments?after_id=next-page"
	if _, err := c.GetFollowingFeed(context.Background(), nextURL, 10); err != nil {
		t.Fatalf("GetFollowingFeed next: %v", err)
	}
}

func TestGetCommentsSupportsFeedResourceTypes(t *testing.T) {
	wantPaths := []string{
		"/api/v4/comment_v5/answers/1/root_comment",
		"/api/v4/comment_v5/articles/2/root_comment",
		"/api/v4/comment_v5/pins/3/root_comment",
		"/api/v4/comment_v5/questions/4/root_comment",
	}
	requestNumber := 0
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if requestNumber >= len(wantPaths) || r.URL.Path != wantPaths[requestNumber] {
			t.Fatalf("request %d path=%s", requestNumber+1, r.URL.Path)
		}
		requestNumber++
		if r.URL.Query().Get("limit") != "20" || r.URL.Query().Get("order_by") != "score" || r.URL.Query().Get("offset") != "" {
			t.Fatalf("query=%s", r.URL.RawQuery)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"data": []any{}})
	})
	defer server.Close()

	for index, resourceType := range []string{"answer", "article", "pin", "question"} {
		if _, err := c.GetComments(context.Background(), resourceType, strconv.Itoa(index+1), 0, 20, "normal"); err != nil {
			t.Fatalf("GetComments(%s): %v", resourceType, err)
		}
	}
	if _, err := c.GetComments(context.Background(), "collection", "5", 0, 20, "normal"); err == nil {
		t.Fatal("GetComments(collection) unexpectedly succeeded")
	}
}

func TestGetCommentsPagePreservesOpaqueCursor(t *testing.T) {
	const cursor = "601800174_11417294455_0"
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/comment_v5/answers/123/root_comment" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.URL.Query().Get("offset") != cursor || r.URL.Query().Get("limit") != "20" || r.URL.Query().Get("order_by") != "score" {
			t.Fatalf("query=%s", r.URL.RawQuery)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"data": []any{map[string]any{"id": 456}}})
	})
	defer server.Close()

	result, err := c.GetCommentsPage(context.Background(), "answer", "123", cursor, 20, "score")
	if err != nil {
		t.Fatalf("GetCommentsPage: %v", err)
	}
	if len(result["data"].([]any)) != 1 {
		t.Fatalf("result=%#v", result)
	}
}

func TestGetChildComments(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/comment_v5/comment/789/child_comment" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "20" || r.URL.Query().Get("offset") != "10" {
			t.Fatalf("query=%s", r.URL.RawQuery)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"data": []any{map[string]any{"id": 456}}})
	})
	defer server.Close()

	result, err := c.GetChildComments(context.Background(), "789", 10, 20)
	if err != nil {
		t.Fatalf("GetChildComments: %v", err)
	}
	if len(result["data"].([]any)) != 1 {
		t.Fatalf("result=%#v", result)
	}
}

func TestCreateComment(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v4/comment_v5/answers/123/comment" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["content"] != "新评论" {
			t.Fatalf("payload=%#v", payload)
		}
		if _, exists := payload["reply_comment_id"]; exists {
			t.Fatalf("root comment unexpectedly has reply target: %#v", payload)
		}
		writeJSON(t, w, http.StatusCreated, map[string]any{"id": 456})
	})
	defer server.Close()

	result, err := c.CreateComment(context.Background(), "answer", "123", " 新评论 ")
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if result["id"].(json.Number).String() != "456" {
		t.Fatalf("result=%#v", result)
	}
}

func TestReplyCommentToResource(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v4/comment_v5/answers/123/comment" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["content"] != "回复内容" || payload["reply_comment_id"] != "789" {
			t.Fatalf("payload=%#v", payload)
		}
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"id":      999,
			"content": "回复内容",
			"author":  map[string]any{"name": "当前用户"},
		})
	})
	defer server.Close()

	result, err := c.ReplyCommentToResource(context.Background(), "answer", "123", "789", " 回复内容 ")
	if err != nil {
		t.Fatalf("ReplyCommentToResource: %v", err)
	}
	if result["id"].(json.Number).String() != "999" {
		t.Fatalf("result=%#v", result)
	}
}

func TestSetCommentLiked(t *testing.T) {
	methods := make(chan string, 2)
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/comments/789/like" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		methods <- r.Method
		w.WriteHeader(http.StatusNoContent)
	})
	defer server.Close()

	if ok, err := c.LikeComment(context.Background(), "789"); err != nil || !ok {
		t.Fatalf("LikeComment ok=%v err=%v", ok, err)
	}
	if ok, err := c.UnlikeComment(context.Background(), "789"); err != nil || !ok {
		t.Fatalf("UnlikeComment ok=%v err=%v", ok, err)
	}
	if first, second := <-methods, <-methods; first != http.MethodPost || second != http.MethodDelete {
		t.Fatalf("methods=%q,%q", first, second)
	}
}

func TestSetContentVoteUsesContentRoutes(t *testing.T) {
	tests := []struct {
		contentType string
		path        string
	}{
		{contentType: "answer", path: "/api/v4/answers/123/voters"},
		{contentType: "article", path: "/api/v4/articles/123/voters"},
	}
	for _, test := range tests {
		t.Run(test.contentType, func(t *testing.T) {
			var voteTypes []string
			c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != test.path {
					t.Fatalf("request=%s %s", r.Method, r.URL.Path)
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				voteTypes = append(voteTypes, payload["type"].(string))
				w.WriteHeader(http.StatusOK)
			})
			defer server.Close()

			if ok, err := c.SetContentVote(context.Background(), test.contentType, "123", true); err != nil || !ok {
				t.Fatalf("vote up ok=%v err=%v", ok, err)
			}
			if ok, err := c.SetContentVote(context.Background(), test.contentType, "123", false); err != nil || !ok {
				t.Fatalf("vote neutral ok=%v err=%v", ok, err)
			}
			if strings.Join(voteTypes, ",") != "up,neutral" {
				t.Fatalf("vote types=%v", voteTypes)
			}
		})
	}
}

func TestSetContentVoteLikesPin(t *testing.T) {
	methods := make(chan string, 2)
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/pins/123/likers" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		methods <- r.Method
		w.WriteHeader(http.StatusNoContent)
	})
	defer server.Close()

	if ok, err := c.SetContentVote(context.Background(), "pin", "123", true); err != nil || !ok {
		t.Fatalf("like pin ok=%v err=%v", ok, err)
	}
	if ok, err := c.SetContentVote(context.Background(), "pin", "123", false); err != nil || !ok {
		t.Fatalf("unlike pin ok=%v err=%v", ok, err)
	}
	if first, second := <-methods, <-methods; first != http.MethodPost || second != http.MethodDelete {
		t.Fatalf("methods=%q,%q", first, second)
	}
}

func TestSetContentVoteRejectsUnsupportedContent(t *testing.T) {
	c, server := testClient(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("unsupported content made a request")
	})
	defer server.Close()

	if _, err := c.SetContentVote(context.Background(), "question", "123", true); err == nil {
		t.Fatal("unsupported content vote succeeded")
	}
}

func TestGetAnswerIncludesCounts(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/answers/123" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		include := r.URL.Query().Get("include")
		for _, field := range []string{"voteup_count", "favlists_count", "thanks_count"} {
			if !strings.Contains(include, field) {
				t.Fatalf("include=%q missing %s", include, field)
			}
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"id": 123, "voteup_count": 19, "favlists_count": 2, "thanks_count": 1})
	})
	defer server.Close()

	answer, err := c.GetAnswer(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetAnswer: %v", err)
	}
	if answer["favlists_count"].(json.Number).String() != "2" {
		t.Fatalf("answer=%#v", answer)
	}
}

func TestReplyCommentPostsChildComment(t *testing.T) {
	var calls []string
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/v4/comments/789":
			if r.Method != http.MethodGet {
				t.Fatalf("method=%s", r.Method)
			}
			writeJSON(t, w, http.StatusOK, map[string]any{
				"id":            789,
				"resource_type": "pin",
				"target":        map[string]any{"id": 123, "type": "pin"},
			})
		case "/api/v4/comment_v5/pins/123/comment":
			if r.Method != http.MethodPost {
				t.Fatalf("method=%s", r.Method)
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["content"] != "Thanks" {
				t.Fatalf("payload=%#v", payload)
			}
			if payload["reply_comment_id"] != "789" {
				t.Fatalf("payload=%#v", payload)
			}
			if payload["unfriendly_check"] != "strict" {
				t.Fatalf("payload=%#v", payload)
			}
			writeJSON(t, w, http.StatusCreated, map[string]any{"id": 456})
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	})
	defer server.Close()

	result, err := c.ReplyComment(context.Background(), "789", " Thanks ")
	if err != nil {
		t.Fatalf("ReplyComment: %v", err)
	}
	if result["id"].(json.Number).String() != "456" {
		t.Fatalf("id=%v", result["id"])
	}
	if strings.Join(calls, ", ") != "GET /api/v4/comments/789, POST /api/v4/comment_v5/pins/123/comment" {
		t.Fatalf("calls=%v", calls)
	}
}

func TestDeleteComment(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method=%s", r.Method)
		}
		if r.URL.Path != "/api/v4/comments/789" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	defer server.Close()

	ok, err := c.DeleteComment(context.Background(), "789")
	if err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	if !ok {
		t.Fatal("DeleteComment was not accepted")
	}
}

func TestMarkNotificationsReadPostsReadAll(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		if r.URL.Path != "/api/v4/notifications/v2/default/actions/readall" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("x-xsrftoken") != "xsrf" {
			t.Fatalf("x-xsrftoken=%q", r.Header.Get("x-xsrftoken"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) != 0 {
			t.Fatalf("body=%q, want empty", string(body))
		}
		w.WriteHeader(http.StatusNoContent)
	})
	defer server.Close()

	if err := c.MarkNotificationsRead(context.Background(), " default "); err != nil {
		t.Fatalf("MarkNotificationsRead: %v", err)
	}
}

func TestMarkAllNotificationsReadPostsEveryTab(t *testing.T) {
	var calls []string
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	if err := c.MarkAllNotificationsRead(context.Background()); err != nil {
		t.Fatalf("MarkAllNotificationsRead: %v", err)
	}
	want := "/api/v4/notifications/v2/default/actions/readall, /api/v4/notifications/v2/follow/actions/readall, /api/v4/notifications/v2/vote_thank/actions/readall"
	if got := strings.Join(calls, ", "); got != want {
		t.Fatalf("calls=%s, want %s", got, want)
	}
}

func TestMarkNotificationsReadRejectsUnknownTab(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})
	defer server.Close()

	err := c.MarkNotificationsRead(context.Background(), "recent")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported notification tab") {
		t.Fatalf("err=%v", err)
	}
}

func TestGetHotListWrapsListResponse(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, []any{map[string]any{"title": "hot"}})
	})
	defer server.Close()

	result, err := c.GetHotList(context.Background(), 2)
	if err != nil {
		t.Fatalf("GetHotList: %v", err)
	}
	if len(result["data"].([]any)) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestCreateQuestionPayload(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/questions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["title"] != "Question?" || payload["detail"] != "Detail" {
			t.Fatalf("payload=%#v", payload)
		}
		topics := payload["topic_url_tokens"].([]any)
		if len(topics) != 2 {
			t.Fatalf("topics=%#v", topics)
		}
		writeJSON(t, w, http.StatusCreated, map[string]any{"id": 123})
	})
	defer server.Close()

	result, err := c.CreateQuestion(context.Background(), "Question?", "Detail", []string{"1", "2"}, nil)
	if err != nil {
		t.Fatalf("CreateQuestion: %v", err)
	}
	if result["id"].(json.Number).String() != "123" {
		t.Fatalf("id=%v", result["id"])
	}
}

func TestCreatePinUsesDraftAndPublish(t *testing.T) {
	var calls []string
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case "/api/v4/content/drafts":
			writeJSON(t, w, http.StatusOK, map[string]any{"data": map[string]any{"content_id": "draft1"}})
		case "/api/v4/content/publish":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["action"] != "pin" {
				t.Fatalf("payload action=%v", payload["action"])
			}
			data := payload["data"].(map[string]any)
			title := data["title"].(map[string]any)
			if title["title"] != "Hello" {
				t.Fatalf("title=%#v", title)
			}
			writeJSON(t, w, http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"result": `{"id":999,"type":"pin"}`}})
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	})
	defer server.Close()

	result, err := c.CreatePin(context.Background(), "Hello", "Body", nil)
	if err != nil {
		t.Fatalf("CreatePin: %v", err)
	}
	if result["id"].(json.Number).String() != "999" {
		t.Fatalf("id=%v", result["id"])
	}
	if len(calls) != 2 {
		t.Fatalf("calls=%v", calls)
	}
}

func TestGetCollectionsRequiresURLToken(t *testing.T) {
	c, server := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{})
	})
	defer server.Close()

	_, err := c.GetCollections(context.Background(), 0, 10)
	if err == nil {
		t.Fatal("expected error")
	}
	var loginErr LoginError
	if !errors.As(err, &loginErr) {
		t.Fatalf("err=%T, want LoginError", err)
	}
}

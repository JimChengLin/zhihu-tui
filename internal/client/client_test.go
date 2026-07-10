package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
		status int
		want   any
	}{
		{http.StatusUnauthorized, LoginError{}},
		{http.StatusForbidden, LoginError{}},
		{http.StatusInternalServerError, DataFetchError{}},
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

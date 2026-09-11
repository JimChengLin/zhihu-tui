package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type retryTestTransport func(*http.Request) (*http.Response, error)

func (f retryTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type retryTestBody struct {
	io.Reader
	closed bool
}

func (b *retryTestBody) Close() error {
	b.closed = true
	return nil
}

func retryTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       &retryTestBody{Reader: strings.NewReader(body)},
	}
}

func newRetryTestClient(transport retryTestTransport) *Client {
	return NewWithHTTP(nil, &http.Client{Transport: transport}, DefaultEndpoints())
}

func TestReadRequestRetriesTransientStatus(t *testing.T) {
	for _, status := range []int{403, 408, 429, 500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls []time.Time
				var bodies []*retryTestBody
				c := newRetryTestClient(func(req *http.Request) (*http.Response, error) {
					if len(bodies) > 0 && !bodies[len(bodies)-1].closed {
						t.Fatal("previous response was not closed before retry")
					}
					if req.URL.Path != "/api/v4/me" {
						t.Fatalf("unexpected path: %s", req.URL.Path)
					}
					calls = append(calls, time.Now())
					resp := retryTestResponse(status, `{"error":{"code":10003,"message":"temporary"}}`)
					if len(calls) == 3 {
						resp = retryTestResponse(http.StatusOK, `{"name":"ok"}`)
					}
					bodies = append(bodies, resp.Body.(*retryTestBody))
					return resp, nil
				})

				info, err := c.GetSelfInfo(context.Background())
				if err != nil || info["name"] != "ok" {
					t.Fatalf("info=%v, err=%v", info, err)
				}
				if len(calls) != 3 {
					t.Fatalf("attempts=%d, want 3", len(calls))
				}
				for i, minDelay := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond} {
					if delay := calls[i+1].Sub(calls[i]); delay < minDelay || delay >= 2*minDelay {
						t.Fatalf("retry delay=%s, want [%s, %s)", delay, minDelay, 2*minDelay)
					}
				}
				if !bodies[2].closed {
					t.Fatal("final response was not closed by GetSelfInfo")
				}
			})
		})
	}
}

func TestReadRequestReturnsThirdFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		c := newRetryTestClient(func(*http.Request) (*http.Response, error) {
			calls++
			if calls == 3 {
				return retryTestResponse(http.StatusServiceUnavailable, "last failure"), nil
			}
			return retryTestResponse(http.StatusInternalServerError, "earlier failure"), nil
		})
		_, err := c.GetSelfInfo(context.Background())
		var fetchErr DataFetchError
		if !errors.As(err, &fetchErr) || fetchErr.StatusCode != http.StatusServiceUnavailable || !strings.Contains(err.Error(), "last failure") {
			t.Fatalf("did not preserve final status and body: %v", err)
		}
		if calls != 3 {
			t.Fatalf("attempts=%d, want 3", calls)
		}
	})
}

func TestReadRequestRetriesNetworkErrors(t *testing.T) {
	for _, succeeds := range []bool{false, true} {
		name := "exhausted"
		if succeeds {
			name = "recovered"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				networkErr := errors.New("connection reset")
				c := newRetryTestClient(func(*http.Request) (*http.Response, error) {
					calls++
					if succeeds && calls == 3 {
						return retryTestResponse(http.StatusOK, ""), nil
					}
					return nil, networkErr
				})
				resp, err := c.do(context.Background(), http.MethodHead, "https://www.zhihu.com/api/v4/me", nil, nil)
				if succeeds {
					if err != nil {
						t.Fatal(err)
					}
					resp.Body.Close()
				} else if !errors.Is(err, networkErr) {
					t.Fatalf("err=%v, want original network error", err)
				}
				if calls != 3 {
					t.Fatalf("attempts=%d, want 3", calls)
				}
			})
		})
	}
}

func TestReadRequestDoesNotRetryPermanentErrors(t *testing.T) {
	for _, status := range []int{200, 400, 401, 403, 404, 501} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			c := newRetryTestClient(func(*http.Request) (*http.Response, error) {
				calls++
				// Even a successful HTTP response with invalid JSON must fail fast.
				return retryTestResponse(status, "invalid JSON"), nil
			})
			if _, err := c.GetSelfInfo(context.Background()); err == nil {
				t.Fatal("expected error")
			}
			if calls != 1 {
				t.Fatalf("attempts=%d, want 1", calls)
			}
		})
	}
}

func TestForbiddenReadReturnsThirdFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const body = `{"error":{"message":"请求参数异常，请升级客户端后重试。","code":10003}}`
		calls := 0
		c := newRetryTestClient(func(*http.Request) (*http.Response, error) {
			calls++
			return retryTestResponse(http.StatusForbidden, body), nil
		})
		_, err := c.GetAnswer(context.Background(), "123")
		var fetchErr DataFetchError
		if !errors.As(err, &fetchErr) || fetchErr.StatusCode != http.StatusForbidden || !strings.Contains(err.Error(), body) {
			t.Fatalf("did not preserve final 403 error: %v", err)
		}
		if calls != 3 {
			t.Fatalf("attempts=%d, want 3", calls)
		}
	})
}

func TestOtherForbiddenResponsesAreNotRetriedOrConsumed(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{"other code", `{"error":{"code":10001,"message":"forbidden"}}`},
		{"missing code", `{"error":{"message":"forbidden"}}`},
		{"invalid JSON", `{"error":`},
		{"large response", strings.Repeat("x", 8192)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			response := retryTestResponse(http.StatusForbidden, tt.body)
			originalBody := response.Body.(*retryTestBody)
			c := newRetryTestClient(func(*http.Request) (*http.Response, error) {
				calls++
				return response, nil
			})
			resp, err := c.do(context.Background(), http.MethodGet, "https://www.zhihu.com/api/v4/me", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || string(body) != tt.body {
				t.Fatalf("response body changed: got %d bytes, want %d, err=%v", len(body), len(tt.body), err)
			}
			if calls != 1 || !originalBody.closed {
				t.Fatalf("attempts=%d, original body closed=%v", calls, originalBody.closed)
			}
		})
	}
}

func TestMutationRequestsAreNeverRetried(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, status := range []int{0, 403, 429, 503} {
			name := http.StatusText(status)
			if status == 0 {
				name = "network error"
			}
			t.Run(method+"/"+name, func(t *testing.T) {
				calls := 0
				c := newRetryTestClient(func(req *http.Request) (*http.Response, error) {
					calls++
					defer req.Body.Close()
					body, err := io.ReadAll(req.Body)
					if err != nil || string(body) != `{"content":"hello"}` || req.Method != method {
						t.Fatalf("method=%s, body=%q, err=%v", req.Method, body, err)
					}
					if status == 0 {
						return nil, errors.New("connection reset after accepting mutation")
					}
					return retryTestResponse(status, `{"error":{"code":10003,"message":"temporary"}}`), nil
				})
				resp, err := c.doJSONRequest(context.Background(), method, "https://www.zhihu.com/api/v4/comments", map[string]string{"content": "hello"}, nil)
				if status == 0 {
					if err == nil {
						t.Fatal("expected network error")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					resp.Body.Close()
					if resp.StatusCode != status {
						t.Fatalf("status=%d, want %d", resp.StatusCode, status)
					}
				}
				if calls != 1 {
					t.Fatalf("mutation was replayed: attempts=%d", calls)
				}
			})
		}
	}
}

func TestReadRequestRetryAfter(t *testing.T) {
	for _, kind := range []string{"seconds", "date", "invalid", "past"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				header := "2"
				minDelay := 2 * time.Second
				switch kind {
				case "date":
					header = start.Add(2 * time.Second).UTC().Format(http.TimeFormat)
				case "invalid":
					header = "invalid"
					minDelay = 250 * time.Millisecond
				case "past":
					header = start.Add(-time.Hour).UTC().Format(http.TimeFormat)
					minDelay = 250 * time.Millisecond
				}
				calls := 0
				c := newRetryTestClient(func(*http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						resp := retryTestResponse(http.StatusTooManyRequests, "slow down")
						resp.Header.Set("Retry-After", header)
						return resp, nil
					}
					return retryTestResponse(http.StatusOK, `{}`), nil
				})
				if _, err := c.GetSelfInfo(context.Background()); err != nil {
					t.Fatal(err)
				}
				if elapsed := time.Since(start); elapsed < minDelay || elapsed >= minDelay+250*time.Millisecond {
					t.Fatalf("elapsed=%s, want [%s, %s)", elapsed, minDelay, minDelay+250*time.Millisecond)
				}
				if calls != 2 {
					t.Fatalf("attempts=%d, want 2", calls)
				}
			})
		})
	}
}

func TestReadRequestCancellationStopsRetries(t *testing.T) {
	for _, preCanceled := range []bool{false, true} {
		name := "during backoff"
		if preCanceled {
			name = "before request"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				wantCalls := 1
				if preCanceled {
					cancel()
					wantCalls = 0
				} else {
					go func() {
						time.Sleep(50 * time.Millisecond)
						cancel()
					}()
				}
				calls := 0
				c := newRetryTestClient(func(*http.Request) (*http.Response, error) {
					calls++
					return retryTestResponse(http.StatusServiceUnavailable, "unavailable"), nil
				})
				start := time.Now()
				_, err := c.do(ctx, http.MethodGet, "https://www.zhihu.com/api/v4/me", nil, nil)
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v, want context.Canceled", err)
				}
				if calls != wantCalls || time.Since(start) > 50*time.Millisecond {
					t.Fatalf("cancellation did not stop promptly: attempts=%d, elapsed=%s", calls, time.Since(start))
				}
			})
		})
	}
}

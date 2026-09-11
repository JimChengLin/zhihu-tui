package client

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

const requestMaxAttempts = 3

func (c *Client) doWithRetry(req *http.Request) (*http.Response, error) {
	// Never replay a mutation: the server may have applied it before failing.
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return c.httpClient.Do(req)
	}
	for attempt := 0; ; attempt++ {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		resp, err := c.httpClient.Do(req)
		if attempt == requestMaxAttempts-1 || (err == nil && !retryableResponse(resp)) {
			return resp, err
		}
		// With an error, Client.Do can return an already-closed redirect response.
		if err != nil {
			resp = nil
		}
		delay := requestRetryDelay(attempt, resp)
		if resp != nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
		}
		timer := time.NewTimer(delay)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
}

func retryableResponse(resp *http.Response) bool {
	switch resp.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	case http.StatusForbidden:
	default:
		return false
	}
	// Zhihu can transiently return 403/code 10003 for a valid read request.
	// Restore the bytes we inspect so callers still receive the original error.
	body := resp.Body
	data, err := io.ReadAll(io.LimitReader(body, 4096))
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(data), body), body}
	if err != nil {
		return false
	}
	var payload struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(data, &payload) == nil && payload.Error.Code == 10003
}

func requestRetryDelay(attempt int, resp *http.Response) time.Duration {
	base := 250 * time.Millisecond << attempt
	delay := base
	if resp != nil {
		retryAfter := resp.Header.Get("Retry-After")
		if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
			delay = max(delay, time.Duration(seconds)*time.Second)
		} else if date, err := http.ParseTime(retryAfter); err == nil {
			delay = max(delay, time.Until(date))
		}
	}
	return delay + rand.N(base)
}

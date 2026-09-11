package client

import (
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
		if attempt == requestMaxAttempts-1 || (err == nil && !retryableStatus(resp.StatusCode)) {
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

func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
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

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JimChengLin/zhihu-tui/internal/config"
)

type cookieFile struct {
	Cookies map[string]string `json:"cookies"`
}

func ParseCookieString(cookieStr string) map[string]string {
	result := make(map[string]string)
	for _, item := range strings.Split(cookieStr, ";") {
		item = strings.TrimSpace(item)
		if item == "" || !strings.Contains(item, "=") {
			continue
		}
		key, value, _ := strings.Cut(item, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		result[key] = strings.TrimSpace(value)
	}
	return result
}

func CookieString(cookies map[string]string) string {
	if len(cookies) == 0 {
		return ""
	}
	keys := make([]string, 0, len(cookies))
	for key := range cookies {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+cookies[key])
	}
	return strings.Join(parts, "; ")
}

func HasRequiredCookies(cookies map[string]string) bool {
	for key := range config.RequiredCookies {
		if cookies[key] == "" {
			return false
		}
	}
	return true
}

func MissingRequiredCookies(cookies map[string]string) []string {
	missing := make([]string, 0, len(config.RequiredCookies))
	for key := range config.RequiredCookies {
		if cookies[key] == "" {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing
}

func SaveCookies(cookieStr string) error {
	cookies := ParseCookieString(cookieStr)
	if len(cookies) == 0 {
		return errors.New("empty cookie string")
	}
	dir, err := config.ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	path, err := config.CookieFile()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cookieFile{Cookies: cookies}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cookies: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write cookie file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set cookie file mode: %w", err)
	}
	return nil
}

func GetSavedCookieString() (string, bool, error) {
	path, err := config.CookieFile()
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read cookie file: %w", err)
	}
	var parsed cookieFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", false, fmt.Errorf("parse cookie file: %w", err)
	}
	if !HasRequiredCookies(parsed.Cookies) {
		return "", false, fmt.Errorf("cookie file is missing required cookies: %s", strings.Join(MissingRequiredCookies(parsed.Cookies), ", "))
	}
	return CookieString(parsed.Cookies), true, nil
}

func GetCookieString() (string, bool, error) {
	return GetSavedCookieString()
}

func ClearCookies() ([]string, error) {
	path, err := config.CookieFile()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat cookie file: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return nil, fmt.Errorf("remove cookie file: %w", err)
	}
	return []string{filepath.Base(path)}, nil
}

func RenderQRHalfBlocks(matrix [][]bool) string {
	if len(matrix) == 0 {
		return ""
	}
	border := 2
	width := len(matrix[0]) + border*2
	padded := make([][]bool, 0, len(matrix)+border*2)
	for i := 0; i < border; i++ {
		padded = append(padded, make([]bool, width))
	}
	for _, row := range matrix {
		out := make([]bool, width)
		copy(out[border:], row)
		padded = append(padded, out)
	}
	for i := 0; i < border; i++ {
		padded = append(padded, make([]bool, width))
	}

	var b strings.Builder
	for y := 0; y < len(padded); y += 2 {
		if y > 0 {
			b.WriteByte('\n')
		}
		top := padded[y]
		bottom := make([]bool, width)
		if y+1 < len(padded) {
			bottom = padded[y+1]
		}
		for x := 0; x < width; x++ {
			switch {
			case top[x] && bottom[x]:
				b.WriteRune('█')
			case top[x]:
				b.WriteRune('▀')
			case bottom[x]:
				b.WriteRune('▄')
			default:
				b.WriteByte(' ')
			}
		}
	}
	return b.String()
}

func QRCodeLogin(ctx context.Context, out io.Writer) (string, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", fmt.Errorf("create cookie jar: %w", err)
	}
	httpClient := &http.Client{Jar: jar, Timeout: config.DefaultTimeout}
	if err := getForCookies(ctx, httpClient, config.ZhihuLoginURL); err != nil {
		return "", err
	}
	if err := postJSONForCookies(ctx, httpClient, config.ZhihuBaseURL+"/udid", map[string]any{}); err != nil {
		return "", err
	}
	if err := getForCookies(ctx, httpClient, config.ZhihuOAuthCaptcha); err != nil {
		return "", err
	}

	qrReq, err := newRequest(ctx, http.MethodPost, config.ZhihuQRCodeAPI, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}
	qrReq.Header.Set("Content-Type", "application/json")
	setXSRFHeader(qrReq, jar)
	resp, err := httpClient.Do(qrReq)
	if err != nil {
		return "", fmt.Errorf("request QR code: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return "", fmt.Errorf("request QR code returned %d: %s", resp.StatusCode, string(body))
	}
	var qrData map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&qrData); err != nil {
		return "", fmt.Errorf("decode QR response: %w", err)
	}
	token := stringField(qrData, "token")
	if token == "" {
		token = stringField(qrData, "qrcode_token")
	}
	link := stringField(qrData, "link")
	if token == "" || link == "" {
		return "", errors.New("QR API did not return token and link")
	}
	if err := saveQRCodeLink(link); err != nil {
		return "", err
	}
	fmt.Fprintf(out, "Open this link with Zhihu app to login:\n%s\n", link)

	scanURL := config.ZhihuQRCodeAPI + "/" + url.PathEscape(token) + "/scan_info"
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		req, err := newRequest(ctx, http.MethodGet, scanURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Referer", config.ZhihuBaseURL+"/signin?next=%2F")
		req.Header.Set("Accept", "*/*")
		req.Header.Set("x-requested-with", "fetch")
		req.Header.Set("x-zse-93", "101_3_3.0")
		setXSRFHeader(req, jar)
		resp, err := httpClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("poll QR login: %w", err)
		}
		var info map[string]any
		if resp.Body != nil {
			_ = json.NewDecoder(resp.Body).Decode(&info)
			resp.Body.Close()
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			return "", fmt.Errorf("poll QR login returned %d", resp.StatusCode)
		}
		applyCookiesFromScanInfo(jar, info)
		cookies := cookiesFromJar(jar)
		if HasRequiredCookies(cookies) {
			cookieStr := CookieString(cookies)
			if err := SaveCookies(cookieStr); err != nil {
				return "", err
			}
			return cookieStr, nil
		}
	}
	return "", errors.New("QR login timed out before required cookies were received")
}

func getForCookies(ctx context.Context, c *http.Client, url string) error {
	req, err := newRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("GET %s returned %d: %s", url, resp.StatusCode, string(body))
	}
	return nil
}

func postJSONForCookies(ctx context.Context, c *http.Client, url string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := newRequest(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, string(body))
	}
	return nil
}

func newRequest(ctx context.Context, method, target string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	for key, value := range config.BrowserHeaders() {
		req.Header.Set(key, value)
	}
	return req, nil
}

func setXSRFHeader(req *http.Request, jar http.CookieJar) {
	u, _ := url.Parse(config.ZhihuBaseURL)
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == "_xsrf" {
			req.Header.Set("x-xsrftoken", cookie.Value)
			return
		}
	}
}

func saveQRCodeLink(link string) error {
	dir, err := config.ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	path, err := config.QRCodeTextFile()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(link+"\n"), 0o600); err != nil {
		return fmt.Errorf("write QR login link: %w", err)
	}
	return nil
}

func applyCookiesFromScanInfo(jar http.CookieJar, info map[string]any) {
	if info == nil {
		return
	}
	zhihuURL, _ := url.Parse(config.ZhihuBaseURL)
	if raw := stringField(info, "cookie"); raw != "" {
		jar.SetCookies(zhihuURL, cookiesFromString(raw))
	}
	if raw := stringField(info, "cookies"); raw != "" {
		jar.SetCookies(zhihuURL, cookiesFromString(raw))
	}
	if zc0 := stringField(info, "z_c0"); zc0 != "" {
		jar.SetCookies(zhihuURL, []*http.Cookie{{Name: "z_c0", Value: zc0}})
	}
}

func cookiesFromString(raw string) []*http.Cookie {
	parsed := ParseCookieString(raw)
	cookies := make([]*http.Cookie, 0, len(parsed))
	for name, value := range parsed {
		cookies = append(cookies, &http.Cookie{Name: name, Value: value})
	}
	return cookies
}

func cookiesFromJar(jar http.CookieJar) map[string]string {
	out := make(map[string]string)
	for _, host := range []string{config.ZhihuBaseURL, config.ZhihuAPIV4, config.ZhihuAPIV3} {
		u, _ := url.Parse(host)
		for _, cookie := range jar.Cookies(u) {
			out[cookie.Name] = cookie.Value
		}
	}
	return out
}

func stringField(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	switch v := data[key].(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return ""
	}
}

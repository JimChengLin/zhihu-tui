package client

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/JimChengLin/zhihu-tui/internal/config"
)

type LoginError struct {
	Message string
}

func (e LoginError) Error() string {
	return e.Message
}

type DataFetchError struct {
	Message string
}

func (e DataFetchError) Error() string {
	return e.Message
}

type Endpoints struct {
	APIV4             string
	APIV3             string
	ZhuanlanAPI       string
	ImageAPI          string
	OSSUploadURL      string
	ContentPublishURL string
	ContentDraftsURL  string
}

func DefaultEndpoints() Endpoints {
	return Endpoints{
		APIV4:             config.ZhihuAPIV4,
		APIV3:             config.ZhihuAPIV3,
		ZhuanlanAPI:       config.ZhihuZhuanlanAPI,
		ImageAPI:          config.ZhihuImageAPI,
		OSSUploadURL:      config.ZhihuOSSUploadURL,
		ContentPublishURL: config.ZhihuContentPublishURL,
		ContentDraftsURL:  config.ZhihuContentDraftsURL,
	}
}

type Client struct {
	httpClient    *http.Client
	cookies       map[string]string
	endpoints     Endpoints
	pollInterval  time.Duration
	pollMaxRounds int
}

var notificationReadTabs = []string{"default", "follow", "vote_thank"}

func New(cookies map[string]string) *Client {
	return NewWithHTTP(cookies, &http.Client{Timeout: config.DefaultTimeout}, DefaultEndpoints())
}

func NewWithHTTP(cookies map[string]string, httpClient *http.Client, endpoints Endpoints) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: config.DefaultTimeout}
	}
	if endpoints.APIV4 == "" {
		endpoints = DefaultEndpoints()
	}
	copied := make(map[string]string, len(cookies))
	for key, value := range cookies {
		copied[key] = value
	}
	return &Client{
		httpClient:    httpClient,
		cookies:       copied,
		endpoints:     endpoints,
		pollInterval:  2 * time.Second,
		pollMaxRounds: 15,
	}
}

func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
}

func (c *Client) GetSelfInfo(ctx context.Context) (map[string]any, error) {
	result, err := c.getJSON(ctx, c.endpoints.APIV4+"/me", nil)
	if err != nil {
		return nil, err
	}
	if m, ok := result.(map[string]any); ok {
		return m, nil
	}
	return map[string]any{}, nil
}

func (c *Client) Search(ctx context.Context, keyword, searchType string, offset, limit int) (map[string]any, error) {
	params := url.Values{
		"gk_version":      {"gz-gaokao"},
		"t":               {searchType},
		"q":               {keyword},
		"correction":      {"1"},
		"offset":          {strconv.Itoa(offset)},
		"limit":           {strconv.Itoa(limit)},
		"filter_fields":   {"lc_idx"},
		"lc_idx":          {"0"},
		"show_all_topics": {"0"},
		"search_source":   {"Normal"},
	}
	return c.getMap(ctx, c.endpoints.APIV4+"/search_v3", params)
}

func (c *Client) GetHotList(ctx context.Context, limit int) (map[string]any, error) {
	params := url.Values{"domain": {"0"}, "limit": {strconv.Itoa(limit)}}
	result, err := c.getJSON(ctx, c.endpoints.APIV4+"/creators/rank/hot", params)
	if err != nil {
		return nil, err
	}
	if m, ok := result.(map[string]any); ok {
		return m, nil
	}
	if list, ok := result.([]any); ok {
		return map[string]any{"data": list}, nil
	}
	return map[string]any{"data": []any{}}, nil
}

func (c *Client) GetQuestion(ctx context.Context, questionID string) (map[string]any, error) {
	params := url.Values{"include": {"data[*].author,answer_count,follower_count,visit_count,comment_count,created_time,updated_time,detail,topics"}}
	return c.getMap(ctx, c.endpoints.APIV4+"/questions/"+url.PathEscape(questionID), params)
}

func (c *Client) GetQuestionAnswers(ctx context.Context, questionID string, offset, limit int, sortBy string) (map[string]any, error) {
	params := url.Values{
		"include": {"data[*].content,voteup_count,comment_count,created_time,updated_time,author"},
		"offset":  {strconv.Itoa(offset)},
		"limit":   {strconv.Itoa(limit)},
		"sort_by": {sortBy},
	}
	return c.getMap(ctx, c.endpoints.APIV4+"/questions/"+url.PathEscape(questionID)+"/answers", params)
}

func (c *Client) GetAnswer(ctx context.Context, answerID string) (map[string]any, error) {
	params := url.Values{"include": {"content,voteup_count,comment_count,created_time,updated_time,author,question,favlists_count,thanks_count"}}
	return c.getMap(ctx, c.endpoints.APIV4+"/answers/"+url.PathEscape(answerID), params)
}

func (c *Client) GetPin(ctx context.Context, pinID string) (map[string]any, error) {
	return c.getMap(ctx, c.endpoints.APIV4+"/pins/"+url.PathEscape(pinID), nil)
}

func (c *Client) GetArticle(ctx context.Context, articleID string) (map[string]any, error) {
	return c.getMap(ctx, c.endpoints.ZhuanlanAPI+"/articles/"+url.PathEscape(articleID), nil)
}

func (c *Client) GetComment(ctx context.Context, commentID string) (map[string]any, error) {
	return c.getMap(ctx, c.endpoints.APIV4+"/comments/"+url.PathEscape(commentID), nil)
}

func (c *Client) GetUserProfile(ctx context.Context, urlToken string) (map[string]any, error) {
	params := url.Values{"include": {"answer_count,articles_count,follower_count,following_count,voteup_count,thanked_count,favorite_count,favorited_count,gender,badge,description,business,educations,employments,locations,is_following,is_followed"}}
	return c.getMap(ctx, c.endpoints.APIV4+"/members/"+url.PathEscape(urlToken), params)
}

func (c *Client) GetUserAnswers(ctx context.Context, urlToken string, offset, limit int, sortBy string) (map[string]any, error) {
	params := url.Values{
		"include": {"data[*].content,voteup_count,comment_count,created_time,question"},
		"offset":  {strconv.Itoa(offset)},
		"limit":   {strconv.Itoa(limit)},
		"sort_by": {sortBy},
	}
	return c.getMap(ctx, c.endpoints.APIV4+"/members/"+url.PathEscape(urlToken)+"/answers", params)
}

func (c *Client) GetUserArticles(ctx context.Context, urlToken string, offset, limit int, sortBy string) (map[string]any, error) {
	params := url.Values{
		"include": {"data[*].content,voteup_count,comment_count,created_time,updated_time"},
		"offset":  {strconv.Itoa(offset)},
		"limit":   {strconv.Itoa(limit)},
		"sort_by": {sortBy},
	}
	return c.getMap(ctx, c.endpoints.APIV4+"/members/"+url.PathEscape(urlToken)+"/articles", params)
}

func (c *Client) GetFollowers(ctx context.Context, urlToken string, offset, limit int) (map[string]any, error) {
	params := url.Values{
		"include": {"data[*].answer_count,articles_count,follower_count"},
		"offset":  {strconv.Itoa(offset)},
		"limit":   {strconv.Itoa(limit)},
	}
	return c.getMap(ctx, c.endpoints.APIV4+"/members/"+url.PathEscape(urlToken)+"/followers", params)
}

func (c *Client) GetFollowing(ctx context.Context, urlToken string, offset, limit int) (map[string]any, error) {
	params := url.Values{
		"include": {"data[*].answer_count,articles_count,follower_count"},
		"offset":  {strconv.Itoa(offset)},
		"limit":   {strconv.Itoa(limit)},
	}
	return c.getMap(ctx, c.endpoints.APIV4+"/members/"+url.PathEscape(urlToken)+"/followees", params)
}

func (c *Client) GetFeed(ctx context.Context, limit int) (map[string]any, error) {
	params := url.Values{"page_number": {"1"}, "limit": {strconv.Itoa(limit)}, "action": {"down"}}
	result, err := c.getJSON(ctx, c.endpoints.APIV3+"/feed/topstory/recommend", params)
	if err != nil {
		return nil, err
	}
	if m, ok := result.(map[string]any); ok {
		return m, nil
	}
	if list, ok := result.([]any); ok {
		return map[string]any{"data": list}, nil
	}
	return map[string]any{"data": []any{}}, nil
}

// GetFollowingFeed returns one page from the feed made up of activity from
// people, questions, topics, and columns the current user follows. Pass the
// paging.next URL from the previous response to load another page.
func (c *Client) GetFollowingFeed(ctx context.Context, nextURL string, limit int) (map[string]any, error) {
	target := nextURL
	var params url.Values
	if target == "" {
		target = c.endpoints.APIV3 + "/moments"
		params = url.Values{"limit": {strconv.Itoa(limit)}}
	}
	return c.getMap(ctx, target, params)
}

func (c *Client) GetTopic(ctx context.Context, topicID string) (map[string]any, error) {
	return c.getMap(ctx, c.endpoints.APIV4+"/topics/"+url.PathEscape(topicID), nil)
}

func (c *Client) GetTopicHotQuestions(ctx context.Context, topicID string, offset, limit int) (map[string]any, error) {
	params := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(limit)}}
	return c.getMap(ctx, c.endpoints.APIV4+"/topics/"+url.PathEscape(topicID)+"/feeds/essence", params)
}

func (c *Client) FollowQuestion(ctx context.Context, questionID string) (bool, error) {
	resp, err := c.do(ctx, http.MethodPost, c.endpoints.APIV4+"/questions/"+url.PathEscape(questionID)+"/followers", nil, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent, nil
}

func (c *Client) UnfollowQuestion(ctx context.Context, questionID string) (bool, error) {
	resp, err := c.do(ctx, http.MethodDelete, c.endpoints.APIV4+"/questions/"+url.PathEscape(questionID)+"/followers", nil, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent, nil
}

func (c *Client) UploadImage(ctx context.Context, filePath, source string) (map[string]any, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, DataFetchError{Message: fmt.Sprintf("image file not found: %s", filePath)}
	}
	sum := md5.Sum(data)
	registerPayload := map[string]any{
		"image_hash": hex.EncodeToString(sum[:]),
		"source":     source,
	}
	respData, err := c.postJSON(ctx, c.endpoints.ImageAPI, registerPayload, nil)
	if err != nil {
		return nil, err
	}
	uploadFile, ok := asMap(respData["upload_file"])
	if !ok {
		return nil, DataFetchError{Message: "image registration response missing upload_file"}
	}
	imageID := toString(uploadFile["image_id"])
	state := toInt(uploadFile["state"])
	switch state {
	case 2:
		token, ok := asMap(respData["upload_token"])
		if !ok {
			return nil, DataFetchError{Message: "image registration response missing upload_token"}
		}
		objKey := toString(uploadFile["object_key"])
		if objKey == "" {
			return nil, DataFetchError{Message: "image registration response missing object_key"}
		}
		if err := c.uploadToOSS(ctx, objKey, data, token); err != nil {
			return nil, err
		}
	case 1:
	default:
		return nil, DataFetchError{Message: fmt.Sprintf("unexpected image state: %d", state)}
	}
	if imageID == "" {
		return nil, DataFetchError{Message: "image registration response missing image_id"}
	}
	imageInfo, err := c.pollImage(ctx, imageID)
	if err != nil {
		return nil, err
	}
	width, height := imageDimensions(filePath)
	imageInfo["width"] = width
	imageInfo["height"] = height
	return imageInfo, nil
}

func (c *Client) CreateQuestion(ctx context.Context, title, detail string, topicIDs []string, imageInfos []map[string]any) (map[string]any, error) {
	if len(imageInfos) > 0 {
		payload := map[string]any{
			"action": "question",
			"data": map[string]any{
				"title": map[string]any{"title": title},
				"topic": map[string]any{"topics": topicIDs},
				"hybrid": map[string]any{
					"html":       detail + buildImgHTML(imageInfos),
					"textLength": len([]rune(detail)),
				},
				"extra_info":     map[string]any{"publisher": "pc"},
				"questionConfig": map[string]any{"type": "0"},
				"draft":          map[string]any{"disabled": 1},
			},
		}
		return c.contentPublish(ctx, payload)
	}
	payload := map[string]any{"title": title, "detail": detail}
	if len(topicIDs) > 0 {
		payload["topic_url_tokens"] = topicIDs
	}
	return c.postJSON(ctx, c.endpoints.APIV4+"/questions", payload, map[int]bool{http.StatusOK: true, http.StatusCreated: true})
}

func (c *Client) CreatePin(ctx context.Context, title, content string, imageInfos []map[string]any) (map[string]any, error) {
	draftID, err := c.createContentDraft(ctx, "pin")
	if err != nil {
		return nil, err
	}
	traceID := fmt.Sprintf("%d,%d", time.Now().UnixMilli(), time.Now().UnixNano())
	bodyHTML := ""
	textLength := len([]rune(strings.TrimSpace(content)))
	if strings.TrimSpace(content) != "" {
		bodyHTML = "<p>" + strings.TrimSpace(content) + "</p>"
	}
	data := map[string]any{
		"publish":            map[string]any{"traceId": traceID},
		"commentsPermission": map[string]any{"comment_permission": "all"},
		"extra_info":         map[string]any{"view_permission": "all", "publisher": "pc"},
		"draft":              map[string]any{"disabled": 1, "id": draftID},
		"title":              map[string]any{"title": title},
		"hybrid":             map[string]any{"html": bodyHTML, "textLength": textLength},
	}
	if len(imageInfos) > 0 {
		bodyHTML = content + buildImgHTML(imageInfos)
		data["hybrid"] = map[string]any{"html": bodyHTML, "textLength": len([]rune(title)) + len([]rune(content))}
		data["media"] = map[string]any{"medias": buildMedia(imageInfos)}
	}
	return c.contentPublish(ctx, map[string]any{"action": "pin", "data": data})
}

func (c *Client) CreateArticle(ctx context.Context, title, content string, topicIDs []string, imageInfos []map[string]any) (map[string]any, error) {
	if len(imageInfos) > 0 {
		draftID, err := c.createContentDraft(ctx, "article")
		if err != nil {
			return nil, err
		}
		payload := map[string]any{
			"action": "article",
			"data": map[string]any{
				"title": map[string]any{"title": title},
				"hybrid": map[string]any{
					"html":       content + buildImgHTML(imageInfos),
					"textLength": len([]rune(content)),
				},
				"extra_info":         map[string]any{"publisher": "pc"},
				"draft":              map[string]any{"disabled": 1, "id": draftID},
				"commentsPermission": map[string]any{"comment_permission": "anyone"},
			},
		}
		return c.contentPublish(ctx, payload)
	}
	draft, err := c.postJSON(ctx, c.endpoints.ZhuanlanAPI+"/articles/drafts", map[string]any{}, map[int]bool{http.StatusOK: true})
	if err != nil {
		return nil, fmt.Errorf("create article draft failed: %w", err)
	}
	draftID := toString(draft["id"])
	if draftID == "" {
		return nil, DataFetchError{Message: "draft created but no ID returned"}
	}
	patchData := map[string]any{"title": title, "content": content}
	if len(topicIDs) > 0 {
		patchData["topics"] = topicIDs
	}
	if err := c.patchJSONNoBody(ctx, c.endpoints.ZhuanlanAPI+"/articles/"+url.PathEscape(draftID)+"/draft", patchData, map[int]bool{http.StatusOK: true, http.StatusNoContent: true}); err != nil {
		return nil, fmt.Errorf("update article draft failed: %w", err)
	}
	return c.putJSON(ctx, c.endpoints.ZhuanlanAPI+"/articles/"+url.PathEscape(draftID)+"/publish", map[string]any{"column": nil, "commentPermission": "anyone"}, map[int]bool{http.StatusOK: true})
}

func (c *Client) DeleteQuestion(ctx context.Context, questionID string) (bool, error) {
	return c.deleteAccepted(ctx, c.endpoints.APIV4+"/questions/"+url.PathEscape(questionID), "question")
}

func (c *Client) DeletePin(ctx context.Context, pinID string) (bool, error) {
	return c.deleteAccepted(ctx, c.endpoints.APIV4+"/pins/"+url.PathEscape(pinID), "pin")
}

func (c *Client) DeleteArticle(ctx context.Context, articleID string) (bool, error) {
	return c.deleteAccepted(ctx, c.endpoints.ZhuanlanAPI+"/articles/"+url.PathEscape(articleID), "article")
}

func (c *Client) DeleteComment(ctx context.Context, commentID string) (bool, error) {
	return c.deleteAccepted(ctx, c.endpoints.APIV4+"/comments/"+url.PathEscape(commentID), "comment")
}

func (c *Client) GetCollections(ctx context.Context, offset, limit int) (map[string]any, error) {
	me, err := c.GetSelfInfo(ctx)
	if err != nil {
		return nil, err
	}
	urlToken := toString(me["url_token"])
	if urlToken == "" {
		return nil, LoginError{Message: "cannot retrieve user info; confirm login status"}
	}
	params := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(limit)}}
	return c.getMap(ctx, c.endpoints.APIV4+"/members/"+url.PathEscape(urlToken)+"/favlists", params)
}

func (c *Client) GetNotifications(ctx context.Context, limit, offset int, entryName string) (map[string]any, error) {
	params := url.Values{"limit": {strconv.Itoa(limit)}, "entry_name": {entryName}, "offset": {strconv.Itoa(offset)}}
	return c.getMap(ctx, c.endpoints.APIV4+"/notifications/v2/recent", params)
}

func (c *Client) MarkNotificationsRead(ctx context.Context, tab string) error {
	tab = strings.TrimSpace(tab)
	if !isNotificationReadTab(tab) {
		return fmt.Errorf("unsupported notification tab %q; use one of: %s", tab, strings.Join(notificationReadTabs, ", "))
	}
	resp, err := c.do(ctx, http.MethodPost, c.endpoints.APIV4+"/notifications/v2/"+url.PathEscape(tab)+"/actions/readall", nil, nil)
	if err != nil {
		return DataFetchError{Message: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()
	return checkExpectedStatus(resp, map[int]bool{http.StatusOK: true, http.StatusNoContent: true}, "mark notifications read")
}

func (c *Client) MarkAllNotificationsRead(ctx context.Context) error {
	for _, tab := range notificationReadTabs {
		if err := c.MarkNotificationsRead(ctx, tab); err != nil {
			return err
		}
	}
	return nil
}

func isNotificationReadTab(tab string) bool {
	for _, candidate := range notificationReadTabs {
		if tab == candidate {
			return true
		}
	}
	return false
}

func (c *Client) vote(ctx context.Context, answerID, voteType string) (bool, error) {
	resp, err := c.doJSONRequest(ctx, http.MethodPost, c.endpoints.APIV4+"/answers/"+url.PathEscape(answerID)+"/voters", map[string]any{"type": voteType}, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

func (c *Client) getMap(ctx context.Context, target string, params url.Values) (map[string]any, error) {
	result, err := c.getJSON(ctx, target, params)
	if err != nil {
		return nil, err
	}
	m, ok := result.(map[string]any)
	if !ok {
		return nil, DataFetchError{Message: "API returned non-object JSON"}
	}
	return m, nil
}

func (c *Client) getJSON(ctx context.Context, target string, params url.Values) (any, error) {
	if params != nil {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + params.Encode()
	}
	resp, err := c.do(ctx, http.MethodGet, target, nil, nil)
	if err != nil {
		return nil, DataFetchError{Message: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, "API request"); err != nil {
		return nil, err
	}
	var data any
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return nil, DataFetchError{Message: fmt.Sprintf("invalid JSON response: %v", err)}
	}
	return data, nil
}

func (c *Client) postJSON(ctx context.Context, target string, payload any, okStatuses map[int]bool) (map[string]any, error) {
	resp, err := c.doJSONRequest(ctx, http.MethodPost, target, payload, nil)
	if err != nil {
		return nil, DataFetchError{Message: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()
	if okStatuses == nil {
		okStatuses = map[int]bool{http.StatusOK: true}
	}
	if err := checkExpectedStatus(resp, okStatuses, "API request"); err != nil {
		return nil, err
	}
	return decodeMap(resp.Body)
}

func (c *Client) putJSON(ctx context.Context, target string, payload any, okStatuses map[int]bool) (map[string]any, error) {
	resp, err := c.doJSONRequest(ctx, http.MethodPut, target, payload, nil)
	if err != nil {
		return nil, DataFetchError{Message: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()
	if err := checkExpectedStatus(resp, okStatuses, "API request"); err != nil {
		return nil, err
	}
	return decodeMap(resp.Body)
}

func (c *Client) patchJSONNoBody(ctx context.Context, target string, payload any, okStatuses map[int]bool) error {
	resp, err := c.doJSONRequest(ctx, http.MethodPatch, target, payload, nil)
	if err != nil {
		return DataFetchError{Message: fmt.Sprintf("request failed: %v", err)}
	}
	defer resp.Body.Close()
	return checkExpectedStatus(resp, okStatuses, "API request")
}

func (c *Client) doJSONRequest(ctx context.Context, method, target string, payload any, headers map[string]string) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Content-Type"] = "application/json"
	return c.do(ctx, method, target, bytes.NewReader(body), headers)
}

func (c *Client) do(ctx context.Context, method, target string, body io.Reader, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	for key, value := range config.BrowserHeaders() {
		req.Header.Set(key, value)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	for name, value := range c.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	if xsrf := c.cookies["_xsrf"]; xsrf != "" {
		req.Header.Set("x-xsrftoken", xsrf)
	}
	return c.httpClient.Do(req)
}

func checkStatus(resp *http.Response, label string) error {
	return checkExpectedStatus(resp, map[int]bool{http.StatusOK: true}, label)
}

func checkExpectedStatus(resp *http.Response, okStatuses map[int]bool, label string) error {
	if resp.StatusCode == http.StatusUnauthorized {
		return LoginError{Message: "session expired or not logged in"}
	}
	if resp.StatusCode == http.StatusForbidden {
		return LoginError{Message: "access denied; check login status"}
	}
	if okStatuses[resp.StatusCode] {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	return DataFetchError{Message: fmt.Sprintf("%s failed with status %d: %s", label, resp.StatusCode, string(body))}
}

func decodeMap(r io.Reader) (map[string]any, error) {
	var data map[string]any
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		if errors.Is(err, io.EOF) {
			return map[string]any{}, nil
		}
		return nil, DataFetchError{Message: fmt.Sprintf("invalid JSON response: %v", err)}
	}
	return data, nil
}

func (c *Client) uploadToOSS(ctx context.Context, objKey string, data []byte, token map[string]any) error {
	contentType := "image/jpeg"
	date := time.Now().UTC().Format(http.TimeFormat)
	securityToken := toString(token["access_token"])
	accessID := toString(token["access_id"])
	accessKey := toString(token["access_key"])
	if securityToken == "" || accessID == "" || accessKey == "" {
		return DataFetchError{Message: "OSS upload token is incomplete"}
	}
	stringToSign := "PUT\n\n" + contentType + "\n" + date + "\n" +
		"x-oss-security-token:" + securityToken + "\n" +
		"/zhihu-pics/" + objKey
	mac := hmac.New(sha1.New, []byte(accessKey))
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	headers := map[string]string{
		"Content-Type":         contentType,
		"Date":                 date,
		"x-oss-security-token": securityToken,
		"Authorization":        "OSS " + accessID + ":" + signature,
	}
	resp, err := c.do(ctx, http.MethodPut, c.endpoints.OSSUploadURL+"/"+objKey, bytes.NewReader(data), headers)
	if err != nil {
		return DataFetchError{Message: fmt.Sprintf("OSS upload failed: %v", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return DataFetchError{Message: fmt.Sprintf("OSS upload failed (%d): %s", resp.StatusCode, string(body))}
	}
	return nil
}

func (c *Client) pollImage(ctx context.Context, imageID string) (map[string]any, error) {
	for i := 0; i < c.pollMaxRounds; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.pollInterval):
			}
		}
		data, err := c.getMap(ctx, c.endpoints.ImageAPI+"/"+url.PathEscape(imageID), nil)
		if err != nil {
			return nil, err
		}
		if toString(data["status"]) == "success" {
			return map[string]any{
				"src":           data["src"],
				"original_src":  data["original_src"],
				"watermark":     defaultString(data["watermark"], "watermark"),
				"watermark_src": defaultString(data["watermark_src"], ""),
			}, nil
		}
	}
	return nil, DataFetchError{Message: "image processing timed out"}
}

func (c *Client) createContentDraft(ctx context.Context, action string) (string, error) {
	data, err := c.postJSON(ctx, c.endpoints.ContentDraftsURL, map[string]any{"action": action}, map[int]bool{http.StatusOK: true})
	if err != nil {
		return "", fmt.Errorf("create draft failed: %w", err)
	}
	nested, ok := asMap(data["data"])
	if !ok {
		return "", DataFetchError{Message: "draft response missing data"}
	}
	contentID := toString(nested["content_id"])
	if contentID == "" {
		return "", DataFetchError{Message: "draft created but no content_id returned"}
	}
	return contentID, nil
}

func (c *Client) contentPublish(ctx context.Context, payload map[string]any) (map[string]any, error) {
	data, err := c.postJSON(ctx, c.endpoints.ContentPublishURL, payload, map[int]bool{http.StatusOK: true, http.StatusCreated: true})
	if err != nil {
		return nil, err
	}
	if code, ok := data["code"]; ok && toInt(code) != 0 {
		msg := toString(data["message"])
		if msg == "" {
			msg = toString(data["toast_message"])
		}
		if msg == "" {
			msg = "unknown error"
		}
		return nil, DataFetchError{Message: "publish failed: " + msg}
	}
	nested, ok := asMap(data["data"])
	if ok {
		if result := toString(nested["result"]); result != "" {
			var parsed map[string]any
			dec := json.NewDecoder(strings.NewReader(result))
			dec.UseNumber()
			if err := dec.Decode(&parsed); err == nil {
				return parsed, nil
			}
		}
	}
	return data, nil
}

func (c *Client) deleteAccepted(ctx context.Context, target, contentType string) (bool, error) {
	resp, err := c.do(ctx, http.MethodDelete, target, nil, nil)
	if err != nil {
		return false, DataFetchError{Message: fmt.Sprintf("delete %s failed: %v", contentType, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return false, LoginError{Message: "session expired or not logged in"}
	}
	if resp.StatusCode == http.StatusForbidden {
		return false, DataFetchError{Message: "no permission to delete this " + contentType}
	}
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent, nil
}

func buildImgHTML(imageInfos []map[string]any) string {
	var b strings.Builder
	for _, info := range imageInfos {
		src := toString(info["src"])
		original := defaultString(info["original_src"], src)
		watermark := defaultString(info["watermark"], "watermark")
		watermarkSrc := defaultString(info["watermark_src"], "")
		width := toInt(info["width"])
		height := toInt(info["height"])
		b.WriteString(fmt.Sprintf(`<img src="%s" data-caption="" data-size="normal" data-rawwidth="%d" data-rawheight="%d" data-watermark="%s" data-original-src="%s" data-watermark-src="%s" data-private-watermark-src=""/>`, src, width, height, watermark, original, watermarkSrc))
	}
	return b.String()
}

func buildMedia(imageInfos []map[string]any) []map[string]any {
	medias := make([]map[string]any, 0, len(imageInfos))
	for _, info := range imageInfos {
		src := toString(info["src"])
		medias = append(medias, map[string]any{
			"image": map[string]any{
				"width":        toInt(info["width"]),
				"height":       toInt(info["height"]),
				"url":          src,
				"originalUrl":  defaultString(info["original_src"], src),
				"watermark":    defaultString(info["watermark"], "watermark"),
				"watermarkUrl": defaultString(info["watermark_src"], ""),
			},
		})
	}
	return medias
}

func imageDimensions(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
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

func defaultString(v any, fallback string) string {
	if s := toString(v); s != "" {
		return s
	}
	return fallback
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	default:
		return 0
	}
}

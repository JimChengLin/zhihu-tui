package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) GetAnswerComments(ctx context.Context, answerID string, offset, limit int, order string) (map[string]any, error) {
	return c.GetComments(ctx, "answer", answerID, offset, limit, order)
}

func (c *Client) GetComments(ctx context.Context, resourceType, resourceID string, offset, limit int, order string) (map[string]any, error) {
	resourceType = strings.TrimSpace(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	switch resourceType {
	case "answer", "article", "pin", "question":
	default:
		return nil, DataFetchError{Message: "comments are not supported for resource type: " + resourceType}
	}
	if resourceID == "" {
		return nil, DataFetchError{Message: "comment resource ID cannot be empty"}
	}
	if order == "" || order == "normal" {
		order = "score"
	}
	params := url.Values{
		"offset":   {""},
		"limit":    {strconv.Itoa(limit)},
		"order_by": {order},
	}
	if offset > 0 {
		params.Set("offset", strconv.Itoa(offset))
	}
	return c.getMap(ctx, c.endpoints.APIV4+"/comment_v5/"+resourceType+"s/"+url.PathEscape(resourceID)+"/root_comment", params)
}

func (c *Client) GetCommentsPage(ctx context.Context, resourceType, resourceID, cursor string, limit int, order string) (map[string]any, error) {
	resourceType = strings.TrimSpace(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	switch resourceType {
	case "answer", "article", "pin", "question":
	default:
		return nil, DataFetchError{Message: "comments are not supported for resource type: " + resourceType}
	}
	if resourceID == "" {
		return nil, DataFetchError{Message: "comment resource ID cannot be empty"}
	}
	if order == "" || order == "normal" {
		order = "score"
	}
	params := url.Values{
		"offset":   {cursor},
		"limit":    {strconv.Itoa(limit)},
		"order_by": {order},
	}
	return c.getMap(ctx, c.endpoints.APIV4+"/comment_v5/"+resourceType+"s/"+url.PathEscape(resourceID)+"/root_comment", params)
}

func (c *Client) GetChildComments(ctx context.Context, commentID string, offset, limit int) (map[string]any, error) {
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return nil, DataFetchError{Message: "root comment ID cannot be empty"}
	}
	params := url.Values{
		"offset": {""},
		"limit":  {strconv.Itoa(limit)},
	}
	if offset > 0 {
		params.Set("offset", strconv.Itoa(offset))
	}
	return c.getMap(ctx, c.endpoints.APIV4+"/comment_v5/comment/"+url.PathEscape(commentID)+"/child_comment", params)
}

func (c *Client) ReplyComment(ctx context.Context, commentID, content string) (map[string]any, error) {
	comment, err := c.GetComment(ctx, commentID)
	if err != nil {
		return nil, err
	}
	target, _ := asMap(comment["target"])
	resourceType := firstNonEmptyString(toString(target["resource_type"]), toString(target["type"]), toString(comment["resource_type"]))
	resourceID := firstNonEmptyString(toString(target["id"]), toString(target["url_token"]), toString(comment["resource_id"]))
	if resourceType == "" || resourceID == "" {
		return nil, DataFetchError{Message: "comment response missing reply target"}
	}
	return c.ReplyCommentToResource(ctx, resourceType, resourceID, commentID, content)
}

func (c *Client) ReplyCommentToResource(ctx context.Context, resourceType, resourceID, commentID, content string) (map[string]any, error) {
	resourceType = strings.TrimSpace(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	commentID = strings.TrimSpace(commentID)
	content = strings.TrimSpace(content)
	if resourceType == "" || resourceID == "" || commentID == "" {
		return nil, DataFetchError{Message: "comment reply target cannot be empty"}
	}
	if content == "" {
		return nil, DataFetchError{Message: "comment reply content cannot be empty"}
	}
	payload := map[string]any{
		"content":           content,
		"reply_comment_id":  commentID,
		"selected_settings": []string{},
		"unfriendly_check":  "strict",
	}
	return c.postJSON(ctx, c.endpoints.APIV4+"/comment_v5/"+url.PathEscape(resourceType)+"s/"+url.PathEscape(resourceID)+"/comment", payload, map[int]bool{http.StatusOK: true, http.StatusCreated: true})
}

func (c *Client) CreateComment(ctx context.Context, resourceType, resourceID, content string) (map[string]any, error) {
	resourceType = strings.TrimSpace(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	content = strings.TrimSpace(content)
	if resourceType == "" || resourceID == "" {
		return nil, DataFetchError{Message: "comment target cannot be empty"}
	}
	if content == "" {
		return nil, DataFetchError{Message: "comment content cannot be empty"}
	}
	payload := map[string]any{
		"content":           content,
		"selected_settings": []string{},
		"unfriendly_check":  "strict",
	}
	return c.postJSON(ctx, c.endpoints.APIV4+"/comment_v5/"+url.PathEscape(resourceType)+"s/"+url.PathEscape(resourceID)+"/comment", payload, map[int]bool{http.StatusOK: true, http.StatusCreated: true})
}

func (c *Client) VoteUp(ctx context.Context, answerID string) (bool, error) {
	return c.vote(ctx, answerID, "up")
}

func (c *Client) VoteNeutral(ctx context.Context, answerID string) (bool, error) {
	return c.vote(ctx, answerID, "neutral")
}

func (c *Client) LikeComment(ctx context.Context, commentID string) (bool, error) {
	return c.setCommentLiked(ctx, commentID, true)
}

func (c *Client) UnlikeComment(ctx context.Context, commentID string) (bool, error) {
	return c.setCommentLiked(ctx, commentID, false)
}

func (c *Client) setCommentLiked(ctx context.Context, commentID string, liked bool) (bool, error) {
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return false, DataFetchError{Message: "comment ID cannot be empty"}
	}
	method := http.MethodPost
	if !liked {
		method = http.MethodDelete
	}
	resp, err := c.do(ctx, method, c.endpoints.APIV4+"/comments/"+url.PathEscape(commentID)+"/like", nil, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if err := checkExpectedStatus(resp, map[int]bool{http.StatusOK: true, http.StatusCreated: true, http.StatusNoContent: true}, "comment like"); err != nil {
		return false, err
	}
	return true, nil
}

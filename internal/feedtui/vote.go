package feedtui

import (
	"context"
	"time"
)

func (model *app) toggleVote(ctx context.Context) {
	if len(model.items) == 0 {
		return
	}
	item := model.items[model.index]
	if model.voting {
		action := "赞同"
		if item.kind == "question" {
			action = "关注"
		}
		model.setMessage(action+"请求处理中", 2*time.Second)
		return
	}
	if model.commentMode {
		model.toggleCommentVote(ctx)
		return
	}
	if item.kind == "question" {
		model.toggleQuestionFollow(ctx, item)
		return
	}
	if !supportsContentVote(item.kind) {
		if len(item.foldedItems) > 0 {
			model.setMessage("请先展开并选择具体动态", 3*time.Second)
		} else {
			model.setMessage("当前动态不支持赞同", 3*time.Second)
		}
		return
	}

	voted := !item.voted
	model.voting = true
	model.spinner = 0
	if voted {
		model.message = "正在赞同"
	} else {
		model.message = "正在取消赞同"
	}
	model.messageUntil = time.Time{}
	go func() {
		ok, err := model.source.SetContentVote(ctx, item.kind, item.id, voted)
		select {
		case model.voteResults <- voteResult{contentKind: item.kind, contentID: item.id, voted: voted, ok: ok, err: err}:
		case <-ctx.Done():
		}
	}()
}

func (model *app) toggleQuestionFollow(ctx context.Context, item feedItem) {
	followed := !item.followed
	model.voting = true
	model.spinner = 0
	if followed {
		model.message = "正在关注问题"
	} else {
		model.message = "正在取消关注问题"
	}
	model.messageUntil = time.Time{}
	go func() {
		var ok bool
		var err error
		if followed {
			ok, err = model.source.FollowQuestion(ctx, item.id)
		} else {
			ok, err = model.source.UnfollowQuestion(ctx, item.id)
		}
		select {
		case model.voteResults <- voteResult{contentKind: item.kind, contentID: item.id, voted: followed, ok: ok, err: err}:
		case <-ctx.Done():
		}
	}()
}

func supportsContentVote(kind string) bool {
	switch kind {
	case "answer", "article", "pin":
		return true
	default:
		return false
	}
}

func (model *app) toggleCommentVote(ctx context.Context) {
	_, commentID := model.focusedComment()
	if commentID == "" {
		model.setMessage("先用 j/k 选择一条评论", 3*time.Second)
		return
	}
	comment, found := findCommentByID(model.currentCommentState(), commentID)
	if !found {
		model.setMessage("蓝色焦点不在评论上", 2*time.Second)
		return
	}
	voted := !comment.voted
	model.voting = true
	model.spinner = 0
	if voted {
		model.message = "正在赞同评论"
	} else {
		model.message = "正在取消评论赞同"
	}
	model.messageUntil = time.Time{}
	itemKey := model.items[model.index].key
	go func() {
		var ok bool
		var err error
		if voted {
			ok, err = model.source.LikeComment(ctx, commentID)
		} else {
			ok, err = model.source.UnlikeComment(ctx, commentID)
		}
		select {
		case model.voteResults <- voteResult{itemKey: itemKey, commentID: commentID, voted: voted, ok: ok, err: err}:
		case <-ctx.Done():
		}
	}()
}

func (model *app) applyVote(result voteResult) {
	model.voting = false
	if result.commentID != "" {
		model.applyCommentVote(result)
		return
	}
	if result.contentKind == "question" {
		model.applyQuestionFollow(result)
		return
	}
	action := "赞同"
	if !result.voted {
		action = "取消赞同"
	}
	if result.err != nil {
		model.setMessage(action+"失败："+result.err.Error(), 4*time.Second)
		return
	}
	if !result.ok {
		model.setMessage(action+"失败：知乎未接受请求", 4*time.Second)
		return
	}
	updateVoteInItems(model.items, result.contentKind, result.contentID, result.voted)
	if result.voted {
		model.setMessage("已赞同", 2*time.Second)
	} else {
		model.setMessage("已取消赞同", 2*time.Second)
	}
}

func (model *app) applyQuestionFollow(result voteResult) {
	action := "关注问题"
	if !result.voted {
		action = "取消关注问题"
	}
	if result.err != nil {
		model.setMessage(action+"失败："+result.err.Error(), 4*time.Second)
		return
	}
	if !result.ok {
		model.setMessage(action+"失败：知乎未接受请求", 4*time.Second)
		return
	}
	updateQuestionFollowInItems(model.items, result.contentID, result.voted)
	if result.voted {
		model.setMessage("已关注问题", 2*time.Second)
	} else {
		model.setMessage("已取消关注问题", 2*time.Second)
	}
}

func (model *app) applyCommentVote(result voteResult) {
	action := "赞同评论"
	if !result.voted {
		action = "取消评论赞同"
	}
	if result.err != nil {
		model.setMessage(action+"失败："+result.err.Error(), 4*time.Second)
		return
	}
	if !result.ok {
		model.setMessage(action+"失败：知乎未接受请求", 4*time.Second)
		return
	}
	state := model.comments[result.itemKey]
	if state == nil || !updateCommentVote(state.items, result.commentID, result.voted) {
		model.setMessage(action+"成功，当前评论已不在列表中", 3*time.Second)
		return
	}
	if result.voted {
		model.setMessage("已赞同评论", 2*time.Second)
	} else {
		model.setMessage("已取消评论赞同", 2*time.Second)
	}
}

func updateCommentVote(comments []feedComment, commentID string, voted bool) bool {
	for index := range comments {
		comment := &comments[index]
		if comment.id == commentID {
			if comment.voted == voted {
				return true
			}
			comment.voted = voted
			if voted {
				comment.voteCount++
			} else if comment.voteCount > 0 {
				comment.voteCount--
			}
			return true
		}
		if updateCommentVote(comment.children, commentID, voted) {
			return true
		}
	}
	return false
}

func updateVoteInItems(items []feedItem, contentKind, contentID string, voted bool) {
	for index := range items {
		updateFeedItemVote(&items[index], contentKind, contentID, voted)
	}
}

func updateFeedItemVote(item *feedItem, contentKind, contentID string, voted bool) {
	if item.kind == contentKind && item.id == contentID && item.voted != voted {
		if item.hasVoteCount {
			if voted {
				item.voteCount++
			} else if item.voteCount > 0 {
				item.voteCount--
			}
			item.stats = replaceVoteStat(item.stats, item.voteCount)
		}
		item.voted = voted
	}
	for index := range item.foldedItems {
		updateFeedItemVote(&item.foldedItems[index], contentKind, contentID, voted)
	}
}

func updateQuestionFollowInItems(items []feedItem, questionID string, followed bool) {
	for index := range items {
		updateFeedItemQuestionFollow(&items[index], questionID, followed)
	}
}

func updateFeedItemQuestionFollow(item *feedItem, questionID string, followed bool) {
	if item.kind == "question" && item.id == questionID && item.followed != followed {
		if item.hasFollowerCount {
			if followed {
				item.followerCount++
			} else if item.followerCount > 0 {
				item.followerCount--
			}
			item.stats = replaceFollowerStat(item.stats, item.followerCount)
		}
		item.followed = followed
	}
	for index := range item.foldedItems {
		updateFeedItemQuestionFollow(&item.foldedItems[index], questionID, followed)
	}
}

package larkmsg

import (
	"context"
	"errors"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/kevinmatthe/zaplog"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

func AddReaction(ctx context.Context, reactionType, msgID string) (reactionID string, err error) {
	_, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("message.id", msgID), attribute.String("reaction.type", reactionType))
	defer span.End()

	req := larkim.NewCreateMessageReactionReqBuilder().Body(larkim.NewCreateMessageReactionReqBodyBuilder().ReactionType(larkim.NewEmojiBuilder().EmojiType(reactionType).Build()).Build()).MessageId(msgID).Build()
	resp, err := lark_dal.Client().Im.V1.MessageReaction.Create(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("AddReaction", zaplog.Error(err))
		otel.RecordError(span, err)
		return "", err
	}
	if !resp.Success() {
		logs.L().Ctx(ctx).Error("AddReaction", zaplog.String("Error", resp.Error()))
		err = errors.New(resp.Error())
		otel.RecordError(span, err)
		return "", err
	}
	go utils.AddTrace2DB(ctx, msgID)
	return *resp.Data.ReactionId, err
}

func AddReactionAsync(ctx context.Context, reactionType, msgID string) (err error) {
	_, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("message.id", msgID), attribute.String("reaction.type", reactionType))
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req := larkim.NewCreateMessageReactionReqBuilder().Body(larkim.NewCreateMessageReactionReqBodyBuilder().ReactionType(larkim.NewEmojiBuilder().EmojiType(reactionType).Build()).Build()).MessageId(msgID).Build()
	go func() {
		resp, err := lark_dal.Client().Im.V1.MessageReaction.Create(ctx, req)
		if err != nil {
			logs.L().Ctx(ctx).Error("AddReaction", zap.Error(err))
			return
		}
		if !resp.Success() {
			logs.L().Ctx(ctx).Error("AddReaction", zap.String("respError", resp.Error()))
			return
		}
		utils.AddTrace2DB(ctx, msgID)
	}()
	return nil
}

// AddReactionWithCallback 异步添加反应，返回一个回调函数.
// 回调可在任何时候调用，用于撤回该反应.
// 如果反应添加失败，回调调用时不会有任何效果.
func AddReactionWithCallback(ctx context.Context, reactionType, msgID string) (removeCallback func()) {
	type pendingOp struct {
		mu         sync.Mutex
		reactionID string
		done       chan struct{}
	}

	p := &pendingOp{done: make(chan struct{})}

	req := larkim.NewCreateMessageReactionReqBuilder().Body(larkim.NewCreateMessageReactionReqBodyBuilder().ReactionType(larkim.NewEmojiBuilder().EmojiType(reactionType).Build()).Build()).MessageId(msgID).Build()
	go func() {
		resp, err := lark_dal.Client().Im.V1.MessageReaction.Create(ctx, req)
		if err != nil {
			logs.L().Ctx(ctx).Error("AddReactionWithCallback", zap.Error(err))
			close(p.done)
			return
		}
		if !resp.Success() {
			logs.L().Ctx(ctx).Error("AddReactionWithCallback", zap.String("respError", resp.Error()))
			close(p.done)
			return
		}
		p.mu.Lock()
		p.reactionID = *resp.Data.ReactionId
		p.mu.Unlock()
		close(p.done)
		utils.AddTrace2DB(ctx, msgID)
	}()

	return func() {
		p.mu.Lock()
		reactionID := p.reactionID
		p.mu.Unlock()

		// 如果 reactionID 还没获取到，等待它
		if reactionID == "" {
			<-p.done
			p.mu.Lock()
			reactionID = p.reactionID
			p.mu.Unlock()
		}

		if reactionID != "" {
			RemoveReactionAsync(ctx, reactionID, msgID)
		}
	}
}

func RemoveReactionAsync(ctx context.Context, reactionID, msgID string) (err error) {
	_, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("message.id", msgID), attribute.String("reaction.id", reactionID))
	defer span.End()
	defer func() { otel.RecordError(span, err) }()
	req := larkim.NewDeleteMessageReactionReqBuilder().MessageId(msgID).ReactionId(reactionID).Build()
	go func() {
		resp, err := lark_dal.Client().Im.V1.MessageReaction.Delete(ctx, req)
		if err != nil {
			logs.L().Ctx(ctx).Error("RemoveReaction", zap.Error(err))
			return
		}
		if !resp.Success() {
			logs.L().Ctx(ctx).Error("RemoveReaction", zap.String("respError", resp.Error()))
			err = errors.New(resp.Error())
			return
		}
		utils.AddTrace2DB(ctx, msgID)
	}()
	return
}

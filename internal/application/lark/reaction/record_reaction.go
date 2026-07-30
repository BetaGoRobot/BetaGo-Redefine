package reaction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

var _ Op = &RecordReactionOperator{}

// RecordReactionOperator Repeat
//
//	@author heyuhengmatt
//	@update 2024-07-17 01:36:07
type RecordReactionOperator struct {
	OpBase
	feedbackSink conversationeval.FeedbackSink
}

func (r *RecordReactionOperator) Name() string {
	return "RecordReaction"
}

// Run  Repeat
//
//	@receiver r
//	@param ctx
//	@param event
//	@return err
func (r *RecordReactionOperator) Run(ctx context.Context, event *larkim.P2MessageReactionCreatedV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(event), 256)...)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	chatID, err := larkmsg.GetChatIDFromMsgID(ctx, *event.Event.MessageId)
	if err != nil {
		return err
	}
	if *event.Event.OperatorType != "user" {
		return nil
	}
	if feedbackErr := r.observeFeedback(ctx, event, chatID); feedbackErr != nil {
		logs.L().Ctx(ctx).Warn(
			"record evaluation reaction feedback failed",
			zap.Error(feedbackErr),
		)
	}
	openID := botidentity.ReactionOpenID(event)
	if openID == "" {
		logs.L().Ctx(ctx).Warn("skip reaction record without open_id",
			zap.String("message_id", *event.Event.MessageId),
		)
		return nil
	}
	userName, err := larkuser.GetUserNameCache(ctx, chatID, openID)
	if err != nil {
		return err
	}
	ins := query.Q.InteractionStat
	return ins.WithContext(ctx).Create(&model.InteractionStat{
		OpenID:     openID,
		GuildID:    chatID,
		MsgID:      *event.Event.MessageId,
		UserName:   userName,
		ActionType: "add_reaction",
	})
}

func (r *RecordReactionOperator) observeFeedback(
	ctx context.Context,
	event *larkim.P2MessageReactionCreatedV1,
	chatID string,
) error {
	if r == nil || r.feedbackSink == nil {
		return nil
	}
	if event == nil || event.Event == nil || event.Event.MessageId == nil ||
		event.Event.ReactionType == nil || event.Event.ReactionType.EmojiType == nil {
		return conversationeval.ErrInvalidContract
	}
	actionMillis, err := strconv.ParseInt(
		strings.TrimSpace(valueOrEmpty(event.Event.ActionTime)),
		10,
		64,
	)
	if err != nil || actionMillis <= 0 {
		return fmt.Errorf("%w: invalid reaction action_time", conversationeval.ErrInvalidContract)
	}
	eventID := ""
	if event.EventV2Base != nil && event.EventV2Base.Header != nil {
		eventID = strings.TrimSpace(event.EventV2Base.Header.EventID)
	}
	if eventID == "" {
		eventID = stableReactionEventID(
			*event.Event.MessageId,
			botidentity.ReactionOpenID(event),
			*event.Event.ReactionType.EmojiType,
			valueOrEmpty(event.Event.ActionTime),
		)
	}
	return r.feedbackSink.ObserveReaction(ctx, conversationeval.ReactionFeedback{
		EventID: eventID, ChatID: strings.TrimSpace(chatID),
		ActorOpenID:     botidentity.ReactionOpenID(event),
		TargetMessageID: strings.TrimSpace(*event.Event.MessageId),
		ReactionType:    strings.TrimSpace(*event.Event.ReactionType.EmojiType),
		OccurredAt:      time.UnixMilli(actionMillis),
	})
}

func stableReactionEventID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "reaction_feedback_" + hex.EncodeToString(sum[:16])
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

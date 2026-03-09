package ops

import (
	"context"

	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/ratelimit"
	infraconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	"github.com/BetaGoRobot/go_utils/reflecting"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var _ Op = &ChatMsgOperator{}

// ChatMsgOperator  RepeatMsg Op
//
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:51
type ChatMsgOperator struct {
	OpBase
}

func (r *ChatMsgOperator) Name() string {
	return "ChatMsgOperator"
}

// FeatureInfo 返回功能信息
func (r *ChatMsgOperator) FeatureInfo() *xhandler.FeatureInfo {
	return &xhandler.FeatureInfo{
		ID:          "chat",
		Name:        "聊天功能",
		Description: "随机触发的聊天功能",
		Default:     true,
	}
}

// Depends 声明此 Operator 依赖的 Fetcher
func (r *ChatMsgOperator) Depends() []xhandler.Fetcher[larkim.P2MessageReceiveV1, xhandler.BaseMetaData] {
	return []xhandler.Fetcher[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]{
		IntentRecognizeFetcher,
	}
}

// PreRun Repeat
//
//	@receiver r *ImitateMsgOperator
//	@param ctx context.Context
//	@param event *larkim.P2MessageReceiveV1
//	@return err error
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:35
func (r *ChatMsgOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	if command.LarkRootCommand.IsCommand(ctx, larkmsg.PreGetTextMsg(ctx, event).GetText()) {
		return errors.Wrap(xerror.ErrStageSkip, r.Name()+" is command")
	}

	if larkmsg.IsMentioned(event.Event.Message.Mentions) {
		return errors.Wrap(xerror.ErrStageSkip, r.Name()+" is mentioned, should handle by reply_chat_op")
	}
	return
}

// Run Repeat
//
//	@receiver r *ImitateMsgOperator
//	@param ctx context.Context
//	@param event *larkim.P2MessageReceiveV1
//	@return err error
//	@author heyuhengmatt
//	@update 2024-07-17 01:35:41
func (r *ChatMsgOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	chatID := *event.Event.Message.ChatId
	decider := ratelimit.GetDecider()

	// 优先尝试使用意图识别结果
	if analysis, ok := GetIntentAnalysisFromMeta(meta); ok {
		// 使用频控决策器决定是否回复
		decision := decider.DecideIntentReply(ctx, chatID, analysis)
		if decision.Allowed {
			logs.L().Ctx(ctx).Info("decided to reply by intent recognition with rate limit",
				zap.String("intent_type", string(analysis.IntentType)),
				zap.Int("confidence", analysis.ReplyConfidence),
				zap.String("reason", analysis.Reason),
				zap.String("ratelimit_reason", decision.Reason),
			)
			// sendMsg
			err := xcommand.BindCLI(handlers.Chat)(ctx, event, meta)
			if err != nil {
				return err
			}
			// 记录回复
			decider.RecordReply(ctx, chatID, ratelimit.TriggerTypeIntent)
			return nil
		}
		logs.L().Ctx(ctx).Info("skipped reply by intent recognition",
			zap.String("intent_type", string(analysis.IntentType)),
			zap.Bool("need_reply", analysis.NeedReply),
			zap.Int("confidence", analysis.ReplyConfidence),
			zap.String("ratelimit_reason", decision.Reason),
		)
		// 意图识别说不需要回复或被频控拒绝，直接返回
		return nil
	}

	// 意图识别不可用，回退到原有的随机数机制（带频控）
	logs.L().Ctx(ctx).Info("intent recognition not available, fallback to random rate with rate limit")
	return r.runWithFallbackRate(ctx, event, meta)
}

// runWithFallbackRate 使用回退概率机制
func (r *ChatMsgOperator) runWithFallbackRate(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	chatID := *event.Event.Message.ChatId
	decider := ratelimit.GetDecider()

	// 使用配置的回退概率，默认使用 ImitateDefaultRate
	realRate := infraconfig.Get().RateConfig.IntentFallbackRate
	if realRate <= 0 {
		realRate = infraconfig.Get().RateConfig.ImitateDefaultRate
	}

	// 群聊定制化
	ins := query.Q.ImitateRateCustom
	configList, err := ins.WithContext(ctx).Where(ins.GuildID.Eq(chatID)).Find()
	if err != nil {
		logs.L().Ctx(ctx).Error("get imitate config from db failed", zap.Error(err))
		// 继续使用默认概率
	} else if len(configList) > 0 {
		realRate = int(configList[0].Rate)
	}

	baseProbability := float64(realRate) / 100

	// 使用频控决策器
	decision := decider.DecideRandomReply(ctx, chatID, baseProbability)
	if decision.Allowed {
		logs.L().Ctx(ctx).Info("decided to reply by random rate with rate limit",
			zap.Int("rate", realRate),
			zap.String("ratelimit_reason", decision.Reason),
		)
		// sendMsg
		err := xcommand.BindCLI(handlers.Chat)(ctx, event, meta)
		if err != nil {
			return err
		}
		// 记录回复
		decider.RecordReply(ctx, chatID, ratelimit.TriggerTypeRandom)
	} else {
		logs.L().Ctx(ctx).Info("skipped reply by random rate",
			zap.Int("rate", realRate),
			zap.String("ratelimit_reason", decision.Reason),
		)
	}
	return nil
}

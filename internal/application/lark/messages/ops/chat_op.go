package ops

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/ratelimit"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
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
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	if err := skipIfCommand(ctx, r.Name(), event); err != nil {
		return err
	}

	if err := skipIfMentioned(r.Name(), event); err != nil {
		return err
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
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	chatID := *event.Event.Message.ChatId
	openID := messageOpenID(event, meta)
	decider := ratelimit.GetDecider()

	// 优先尝试使用意图识别结果
	if analysis, ok := GetIntentAnalysisFromMeta(meta); ok {
		// 使用频控决策器决定是否回复
		decision := decider.DecideIntentReply(ctx, chatID, openID, analysis)
		if decision.Allowed {
			// 先记录，再回复；因为回复的时延可能高的一批。。
			decider.RecordReply(ctx, chatID, decision.TriggerType)
			logs.L().Ctx(ctx).Info("decided to reply by intent recognition with rate limit",
				zap.String("intent_type", string(analysis.IntentType)),
				zap.Int("confidence", analysis.ReplyConfidence),
				zap.String("reply_mode", string(analysis.ReplyMode)),
				zap.Int("user_willingness", analysis.UserWillingness),
				zap.Int("interrupt_risk", analysis.InterruptRisk),
				zap.String("reason", analysis.Reason),
				zap.String("trigger_type", string(decision.TriggerType)),
				zap.String("ratelimit_reason", decision.Reason),
			)
			// sendMsg
			err := xcommand.BindCLI(handlers.Chat)(ctx, event, meta)
			if err != nil {
				return err
			}
			return nil
		}
		logs.L().Ctx(ctx).Info("skipped reply by intent recognition",
			zap.String("intent_type", string(analysis.IntentType)),
			zap.Bool("need_reply", analysis.NeedReply),
			zap.Int("confidence", analysis.ReplyConfidence),
			zap.String("reply_mode", string(analysis.ReplyMode)),
			zap.Int("user_willingness", analysis.UserWillingness),
			zap.Int("interrupt_risk", analysis.InterruptRisk),
			zap.String("trigger_type", string(decision.TriggerType)),
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
	configAccessor := messageConfigAccessor(ctx, event, meta)

	// 使用配置的回退概率，默认使用 ImitateDefaultRate
	realRate := configAccessor.IntentFallbackRate()
	if realRate <= 0 {
		realRate = configAccessor.ImitateDefaultRate()
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
		// 先记录，再回复；因为回复的时延可能高的一批。。
		decider.RecordReply(ctx, chatID, ratelimit.TriggerTypeRandom)
		logs.L().Ctx(ctx).Info("decided to reply by random rate with rate limit",
			zap.Int("rate", realRate),
			zap.String("ratelimit_reason", decision.Reason),
		)
		// sendMsg
		err := xcommand.BindCLI(handlers.Chat)(ctx, event, meta)
		if err != nil {
			return err
		}
	} else {
		logs.L().Ctx(ctx).Info("skipped reply by random rate",
			zap.Int("rate", realRate),
			zap.String("ratelimit_reason", decision.Reason),
		)
	}
	return nil
}

func currentChatType(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.ChatType == nil {
		return ""
	}
	return strings.TrimSpace(*event.Event.Message.ChatType)
}

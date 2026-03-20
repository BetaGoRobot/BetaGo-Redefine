package ops

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/ratelimit"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

var _ Op = &AgenticChatMsgOperator{}

type AgenticChatMsgOperator struct {
	OpBase
}

func (r *AgenticChatMsgOperator) Name() string {
	return "AgenticChatMsgOperator"
}

func (r *AgenticChatMsgOperator) FeatureInfo() *xhandler.FeatureInfo {
	return &xhandler.FeatureInfo{
		ID:          "chat",
		Name:        "聊天功能",
		Description: "随机触发的聊天功能",
		Default:     true,
	}
}

func (r *AgenticChatMsgOperator) Depends() []xhandler.Fetcher[larkim.P2MessageReceiveV1, xhandler.BaseMetaData] {
	return []xhandler.Fetcher[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]{
		IntentRecognizeFetcher,
	}
}

func (r *AgenticChatMsgOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	if err := skipIfCommand(ctx, r.Name(), event); err != nil {
		return err
	}
	if err := skipIfMentioned(r.Name(), event); err != nil {
		return err
	}
	return nil
}

func (r *AgenticChatMsgOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	chatID := *event.Event.Message.ChatId
	decider := ratelimit.GetDecider()
	if observation, ok := runtimeMessageObservation(ctx, event, meta); ok &&
		shouldDirectRouteRuntime(observation, agentruntime.TriggerTypeFollowUp, agentruntime.TriggerTypeReplyToBot) {
		decider.RecordReply(ctx, chatID, ratelimit.TriggerTypeMention)
		return agenticChatInvoker(runtimeOwnershipContext(ctx, observation), event, meta)
	}

	if analysis, ok := GetIntentAnalysisFromMeta(meta); ok {
		decision := decider.DecideIntentReply(ctx, chatID, analysis)
		if decision.Allowed {
			decider.RecordReply(ctx, chatID, ratelimit.TriggerTypeIntent)
			logs.L().Ctx(ctx).Info("decided to reply by intent recognition with rate limit(agentic)",
				zap.String("intent_type", string(analysis.IntentType)),
				zap.Int("confidence", analysis.ReplyConfidence),
				zap.String("reason", analysis.Reason),
				zap.String("ratelimit_reason", decision.Reason),
			)
			err := agenticChatInvoker(ctx, event, meta)
			if err != nil {
				return err
			}
			return nil
		}
		logs.L().Ctx(ctx).Info("skipped reply by intent recognition(agentic)",
			zap.String("intent_type", string(analysis.IntentType)),
			zap.Bool("need_reply", analysis.NeedReply),
			zap.Int("confidence", analysis.ReplyConfidence),
			zap.String("ratelimit_reason", decision.Reason),
		)
		return nil
	}

	logs.L().Ctx(ctx).Info("intent recognition not available, fallback to random rate with rate limit(agentic)")
	return r.runWithFallbackRate(ctx, event, meta)
}

func (r *AgenticChatMsgOperator) runWithFallbackRate(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	chatID := *event.Event.Message.ChatId
	decider := ratelimit.GetDecider()
	configAccessor := messageConfigAccessor(ctx, event, meta)

	realRate := configAccessor.IntentFallbackRate()
	if realRate <= 0 {
		realRate = configAccessor.ImitateDefaultRate()
	}

	ins := query.Q.ImitateRateCustom
	configList, err := ins.WithContext(ctx).Where(ins.GuildID.Eq(chatID)).Find()
	if err != nil {
		logs.L().Ctx(ctx).Error("get imitate config from db failed", zap.Error(err))
	} else if len(configList) > 0 {
		realRate = int(configList[0].Rate)
	}

	baseProbability := float64(realRate) / 100
	decision := decider.DecideRandomReply(ctx, chatID, baseProbability)
	if decision.Allowed {
		decider.RecordReply(ctx, chatID, ratelimit.TriggerTypeRandom)
		logs.L().Ctx(ctx).Info("decided to reply by random rate with rate limit(agentic)",
			zap.Int("rate", realRate),
			zap.String("ratelimit_reason", decision.Reason),
		)
		err := agenticChatInvoker(ctx, event, meta)
		if err != nil {
			return err
		}
	} else {
		logs.L().Ctx(ctx).Info("skipped reply by random rate(agentic)",
			zap.Int("rate", realRate),
			zap.String("ratelimit_reason", decision.Reason),
		)
	}
	return nil
}

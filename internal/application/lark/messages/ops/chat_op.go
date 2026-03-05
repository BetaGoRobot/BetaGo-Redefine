package ops

import (
	"context"

	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
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
		return errors.Wrap(xerror.ErrStageSkip, r.Name()+" Not Mentioned")
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

	// 优先尝试使用意图识别结果
	if analysis, ok := GetIntentAnalysisFromMeta(meta); ok {
		if shouldReplyByIntent(analysis) {
			logs.L().Ctx(ctx).Info("decided to reply by intent recognition",
				zap.String("intent_type", string(analysis.IntentType)),
				zap.Int("confidence", analysis.ReplyConfidence),
				zap.String("reason", analysis.Reason),
			)
			// sendMsg
			err := handlers.ChatHandler("chat")(ctx, event, meta)
			if err != nil {
				return err
			}
			return nil
		}
		logs.L().Ctx(ctx).Info("skipped reply by intent recognition",
			zap.String("intent_type", string(analysis.IntentType)),
			zap.Bool("need_reply", analysis.NeedReply),
			zap.Int("confidence", analysis.ReplyConfidence),
		)
		// 意图识别说不需要回复，直接返回
		return nil
	}

	// 意图识别不可用，回退到原有的随机数机制
	logs.L().Ctx(ctx).Info("intent recognition not available, fallback to random rate")
	return r.runWithFallbackRate(ctx, event, meta)
}

// shouldReplyByIntent 根据意图分析结果判断是否应该回复
func shouldReplyByIntent(analysis *intent.IntentAnalysis) bool {
	rateConfig := config.Get().RateConfig

	// 如果明确需要回复且置信度超过阈值
	if analysis.NeedReply && analysis.ReplyConfidence >= rateConfig.IntentReplyThreshold {
		return true
	}

	// 如果是问题类型，即使置信度稍低也回复
	if analysis.IntentType == intent.IntentTypeQuestion && analysis.ReplyConfidence >= 50 {
		return true
	}

	// 根据建议动作判断
	if analysis.SuggestAction == intent.SuggestActionChat && analysis.ReplyConfidence >= rateConfig.IntentReplyThreshold {
		return true
	}

	return false
}

// runWithFallbackRate 使用回退概率机制
func (r *ChatMsgOperator) runWithFallbackRate(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	// 使用配置的回退概率，默认使用 ImitateDefaultRate
	realRate := config.Get().RateConfig.IntentFallbackRate
	if realRate <= 0 {
		realRate = config.Get().RateConfig.ImitateDefaultRate
	}

	// 群聊定制化
	ins := query.Q.ImitateRateCustom
	configList, err := ins.WithContext(ctx).Where(ins.GuildID.Eq(*event.Event.Message.ChatId)).Find()
	if err != nil {
		logs.L().Ctx(ctx).Error("get imitate config from db failed", zap.Error(err))
		// 继续使用默认概率
	} else if len(configList) > 0 {
		realRate = int(configList[0].Rate)
	}

	if utils.Prob(float64(realRate) / 100) {
		logs.L().Ctx(ctx).Info("decided to reply by random rate", zap.Int("rate", realRate))
		// sendMsg
		err := handlers.ChatHandler("chat")(ctx, event, meta)
		if err != nil {
			return err
		}
	}
	return nil
}

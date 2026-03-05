package ops

import (
	"context"
	"encoding/json"

	"github.com/pkg/errors"
	"go.uber.org/zap"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/command"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	infraconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	"github.com/BetaGoRobot/go_utils/reflecting"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var _ Op = &IntentRecognizeOperator{}
var _ xhandler.Fetcher[larkim.P2MessageReceiveV1, xhandler.BaseMetaData] = &IntentRecognizeOperator{}

// IntentRecognizeOperator 意图识别 Operator（同时也是 Fetcher）
//
//	@author heyuhengmatt
//	@update 2025-03-02
type IntentRecognizeOperator struct {
	OpBase
}

const (
	// MetaKeyIntentAnalysis 存储意图分析结果的 meta key
	MetaKeyIntentAnalysis = "intent_analysis"
)

// 全局单例 IntentRecognizeFetcher
var IntentRecognizeFetcher = &IntentRecognizeOperator{}

func (r *IntentRecognizeOperator) Name() string {
	return "IntentRecognizeOperator"
}

// Fetch 实现 Fetcher 接口，执行意图识别
func (r *IntentRecognizeOperator) Fetch(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	defer span.End()
	defer func() { span.RecordError(err) }()

	// 检查功能是否启用
	if !appconfig.GetManager().IsFeatureEnabled(ctx, "intent_recognize", *event.Event.Message.ChatId, *event.Event.Sender.SenderId.OpenId) {
		return errors.Wrap(xerror.ErrStageSkip, r.Name()+" feature blocked")
	}

	// 检查是否启用了意图识别
	if !infraconfig.Get().RateConfig.IntentRecognitionEnabled {
		return errors.Wrap(xerror.ErrStageSkip, r.Name()+" intent recognition disabled")
	}

	// 跳过命令消息
	if command.LarkRootCommand.IsCommand(ctx, larkmsg.PreGetTextMsg(ctx, event).GetText()) {
		return errors.Wrap(xerror.ErrStageSkip, r.Name()+" command message skipped")
	}

	textMsg := larkmsg.PreGetTextMsg(ctx, event)
	text := textMsg.GetText()

	if text == "" {
		logs.L().Ctx(ctx).Warn("empty message, skip intent recognition")
		return nil
	}

	// 调用意图识别
	analysis, err := intent.AnalyzeMessage(ctx, text)
	if err != nil {
		logs.L().Ctx(ctx).Error("intent analysis failed", zap.Error(err))
		// 不返回错误，让后续流程继续使用回退机制
		return nil
	}

	// 将结果序列化为 JSON 存入 meta.Extra
	analysisJSON, err := json.Marshal(analysis)
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to marshal intent analysis", zap.Error(err))
		return nil
	}

	meta.SetExtra(MetaKeyIntentAnalysis, string(analysisJSON))

	logs.L().Ctx(ctx).Info("intent recognition completed",
		zap.String("intent_type", string(analysis.IntentType)),
		zap.Bool("need_reply", analysis.NeedReply),
		zap.Int("confidence", analysis.ReplyConfidence),
	)

	return nil
}

// PreRun 意图识别前置检查（作为 Operator 时使用）
func (r *IntentRecognizeOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	// 作为 Operator 时，直接跳过，因为已经作为 Fetcher 执行过了
	return errors.Wrap(xerror.ErrStageSkip, r.Name()+" already executed as fetcher")
}

// Run 执行意图识别（作为 Operator 时使用）
func (r *IntentRecognizeOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	return nil
}

// PostRun 意图识别后置处理（作为 Operator 时使用）
func (r *IntentRecognizeOperator) PostRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	return nil
}

// GetIntentAnalysisFromMeta 从 meta 中获取意图分析结果
func GetIntentAnalysisFromMeta(meta *xhandler.BaseMetaData) (*intent.IntentAnalysis, bool) {
	analysisJSON, ok := meta.GetExtra(MetaKeyIntentAnalysis)
	if !ok || analysisJSON == "" {
		return nil, false
	}

	var analysis intent.IntentAnalysis
	if err := json.Unmarshal([]byte(analysisJSON), &analysis); err != nil {
		return nil, false
	}

	return &analysis, true
}

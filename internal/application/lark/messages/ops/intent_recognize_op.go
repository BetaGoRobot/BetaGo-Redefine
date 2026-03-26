package ops

import (
	"context"
	"strings"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/pkg/errors"
	"go.uber.org/zap"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var (
	_ Op                                                                 = &IntentRecognizeOperator{}
	_ xhandler.Fetcher[larkim.P2MessageReceiveV1, xhandler.BaseMetaData] = &IntentRecognizeOperator{}
)

// IntentRecognizeOperator 意图识别 Operator（同时也是 Fetcher）
//
//	@author heyuhengmatt
//	@update 2025-03-02
type IntentRecognizeOperator struct {
	OpBase
	configAccessor  func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) intentRecognizeConfig
	runtimeObserver func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) (agentruntime.ShadowObservation, bool)
	analyzer        func(context.Context, string) (*intent.IntentAnalysis, error)
}

type intentRecognizeConfig interface {
	IntentRecognitionEnabled() bool
	ChatMode() appconfig.ChatMode
}

// 全局单例 IntentRecognizeFetcher
var IntentRecognizeFetcher = &IntentRecognizeOperator{}

func (r *IntentRecognizeOperator) Name() string {
	return "IntentRecognizeOperator"
}

// FeatureInfo 返回功能信息
func (r *IntentRecognizeOperator) FeatureInfo() *xhandler.FeatureInfo {
	return &xhandler.FeatureInfo{
		ID:          "intent_recognize",
		Name:        "意图识别功能",
		Description: "使用AI识别用户消息意图",
		Default:     true,
	}
}

// Fetch 实现 Fetcher 接口，执行意图识别
func (r *IntentRecognizeOperator) Fetch(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	accessor := r.intentConfigAccessor(ctx, event, meta)
	if meta != nil {
		meta.SetIntentInteractionMode(interactionModeFromChatMode(accessor.ChatMode()))
	}

	// 检查是否启用了意图识别（TOML 配置的总开关）
	if !accessor.IntentRecognitionEnabled() {
		return errors.Wrap(xerror.ErrStageSkip, r.Name()+" intent recognition disabled")
	}

	if isCommandMessage(ctx, event) && !strings.EqualFold(runtimeCommandName(ctx, event), "bb") {
		meta.SetIntentInteractionMode(intent.InteractionModeStandard)
		return nil
	}

	text := messageText(ctx, event)
	if text == "" {
		logs.L().Ctx(ctx).Warn("empty message, skip intent recognition")
		return nil
	}

	configMode := accessor.ChatMode().Normalize()
	observation, observed := r.observeRuntimeIntent(ctx, event, meta)
	if shouldSkipIntentAnalysisForContinuation(observation, observed) {
		analysis := continuationIntentAnalysis(observation)
		return storeIntentAnalysis(meta, analysis)
	}
	eligible := shouldUseIntentDrivenInteractionMode(ctx, event, observation, observed)

	// 调用意图识别
	analysis, err := r.analyzeIntent(ctx, text)
	if err != nil {
		logs.L().Ctx(ctx).Error("intent analysis failed", zap.Error(err))
		meta.SetIntentInteractionMode(resolveInteractionMode(
			configMode,
			intent.InteractionModeStandard,
			observation,
			observed,
			eligible,
		))
		return nil
	}

	analysis.InteractionMode = resolveInteractionMode(
		configMode,
		analysis.InteractionMode,
		observation,
		observed,
		eligible,
	)
	if shouldForceDirectReplyMode(event, observation, observed) {
		analysis.ReplyMode = intent.ReplyModeDirect
		analysis.NeedReply = true
		analysis.UserWillingness = 100
		analysis.InterruptRisk = 0
	}
	analysis.Sanitize()

	if err := storeIntentAnalysis(meta, analysis); err != nil {
		logs.L().Ctx(ctx).Error("failed to store intent analysis", zap.Error(err))
		return nil
	}

	logs.L().Ctx(ctx).Info("intent recognition completed",
		zap.String("intent_type", string(analysis.IntentType)),
		zap.Bool("need_reply", analysis.NeedReply),
		zap.Int("confidence", analysis.ReplyConfidence),
		zap.String("interaction_mode", string(analysis.InteractionMode)),
	)

	return nil
}

func (r *IntentRecognizeOperator) intentConfigAccessor(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) intentRecognizeConfig {
	if r != nil && r.configAccessor != nil {
		return r.configAccessor(ctx, event, meta)
	}
	return messageConfigAccessor(ctx, event, meta)
}

func (r *IntentRecognizeOperator) observeRuntimeIntent(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (agentruntime.ShadowObservation, bool) {
	if r != nil && r.runtimeObserver != nil {
		return r.runtimeObserver(ctx, event, meta)
	}
	return observePotentialRuntimeMessage(ctx, event, meta)
}

func (r *IntentRecognizeOperator) analyzeIntent(ctx context.Context, text string) (*intent.IntentAnalysis, error) {
	if r != nil && r.analyzer != nil {
		return r.analyzer(ctx, text)
	}
	return intent.AnalyzeMessage(ctx, text)
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
	if meta == nil {
		return nil, false
	}
	return meta.GetIntentAnalysis()
}

func resolveInteractionMode(
	_ appconfig.ChatMode,
	predicted intent.InteractionMode,
	observation agentruntime.ShadowObservation,
	observed bool,
	eligible bool,
) intent.InteractionMode {
	if observed && (strings.TrimSpace(observation.AttachToRunID) != "" || strings.TrimSpace(observation.SupersedeRunID) != "") {
		return intent.InteractionModeAgentic
	}
	if !eligible {
		return intent.InteractionModeStandard
	}
	return predicted.Normalize()
}

func shouldSkipIntentAnalysisForContinuation(observation agentruntime.ShadowObservation, observed bool) bool {
	if !observed {
		return false
	}
	if strings.TrimSpace(observation.AttachToRunID) != "" || strings.TrimSpace(observation.SupersedeRunID) != "" {
		return true
	}
	switch observation.TriggerType {
	case agentruntime.TriggerTypeReplyToBot, agentruntime.TriggerTypeFollowUp:
		return true
	default:
		return false
	}
}

func continuationIntentAnalysis(observation agentruntime.ShadowObservation) *intent.IntentAnalysis {
	return &intent.IntentAnalysis{
		IntentType:      intent.IntentTypeChat,
		NeedReply:       true,
		ReplyConfidence: 100,
		Reason:          strings.TrimSpace(observation.Reason),
		SuggestAction:   intent.SuggestActionChat,
		ReplyMode:       intent.ReplyModeDirect,
		UserWillingness: 100,
		InterruptRisk:   0,
		InteractionMode: intent.InteractionModeAgentic,
		ReasoningEffort: intent.DefaultReasoningEffort(intent.InteractionModeAgentic),
	}
}

func storeIntentAnalysis(meta *xhandler.BaseMetaData, analysis *intent.IntentAnalysis) error {
	if meta == nil || analysis == nil {
		return nil
	}
	meta.SetIntentAnalysis(analysis)
	return nil
}

func shouldUseIntentDrivenInteractionMode(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	observation agentruntime.ShadowObservation,
	observed bool,
) bool {
	if strings.EqualFold(currentChatType(event), "p2p") {
		return true
	}
	if runtimeIsMentioned(event) {
		return true
	}
	if isCommandMessage(ctx, event) {
		return strings.EqualFold(runtimeCommandName(ctx, event), "bb")
	}
	return observed && observation.TriggerType == agentruntime.TriggerTypeReplyToBot
}

func shouldForceDirectReplyMode(
	event *larkim.P2MessageReceiveV1,
	observation agentruntime.ShadowObservation,
	observed bool,
) bool {
	if strings.EqualFold(currentChatType(event), "p2p") {
		return true
	}
	if runtimeIsMentioned(event) {
		return true
	}
	if !observed {
		return false
	}
	switch observation.TriggerType {
	case agentruntime.TriggerTypeMention,
		agentruntime.TriggerTypeReplyToBot,
		agentruntime.TriggerTypeFollowUp,
		agentruntime.TriggerTypeP2P:
		return true
	default:
		return false
	}
}

// interactionModeFromChatMode maps the configured chat mode to the seeded interaction mode.
func interactionModeFromChatMode(mode appconfig.ChatMode) intent.InteractionMode {
	if mode.Normalize() == appconfig.ChatModeAgentic {
		return intent.InteractionModeAgentic
	}
	return intent.InteractionModeStandard
}

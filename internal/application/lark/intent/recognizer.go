package intent

import (
	"context"
	"encoding/json"
	"errors"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intentmeta"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/bytedance/gg/gptr"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

var responseTextWithCacheFn = ark_dal.ResponseTextWithCache

type IntentType = intentmeta.IntentType

const (
	IntentTypeQuestion = intentmeta.IntentTypeQuestion
	IntentTypeChat     = intentmeta.IntentTypeChat
	IntentTypeShare    = intentmeta.IntentTypeShare
	IntentTypeCommand  = intentmeta.IntentTypeCommand
	IntentTypeIgnore   = intentmeta.IntentTypeIgnore
)

type SuggestAction = intentmeta.SuggestAction

const (
	SuggestActionChat   = intentmeta.SuggestActionChat
	SuggestActionReact  = intentmeta.SuggestActionReact
	SuggestActionRepeat = intentmeta.SuggestActionRepeat
	SuggestActionIgnore = intentmeta.SuggestActionIgnore
)

type InteractionMode = intentmeta.InteractionMode

const (
	InteractionModeStandard = intentmeta.InteractionModeStandard
	InteractionModeAgentic  = intentmeta.InteractionModeAgentic
)

type IntentAnalysis = intentmeta.IntentAnalysis

// 系统提示词
const intentSystemPrompt = `你是一个群聊消息意图分析助手。你的任务是分析用户的消息，判断机器人是否应该主动回复。

请根据以下指南进行分析：
1. 意图类型：
   - question: 用户在提问（包含"什么"、"怎么"、"为什么"、"吗"、"?"等疑问词）
   - chat: 用户在进行日常对话或闲聊
   - share: 用户在分享内容（链接、图片、新闻等）
   - command: 用户在使用命令（通常以/开头）
   - ignore: 无意义内容、单纯的情绪抒发、或者明显不需要回复的消息

2. 是否需要回复：
   - question: 通常需要回复
   - chat: 根据上下文判断，可以选择性回复
   - share: 可以简单回应或点赞
   - command: 由命令处理器处理
   - ignore: 不需要回复

3. 回复置信度：0-100，越高表示越应该回复

4. 建议动作：
   - chat: 使用聊天功能回复
   - react: 发送表情反应
   - repeat: 重复用户的话
   - ignore: 忽略

5. interaction_mode 用于决定消息应该走哪条回复链路：
   - agentic: 明确要完成任务、需要多步规划或能力编排、很可能触发审批/等待回调/等待 schedule/持续跟进
	- 分析类任务不是单点事实问答。如果用户要求你综合多方信息、资料、上下文、历史数据、公开信息或工具结果，再去分析原因、研判趋势、给出归因/框架/结论，这类请求优先判定为 agentic
	- 即使用户只发来一句话，只要任务本质上是研究、调查、归因、比较多个因素、汇总多来源信息后再回答，也应该判定为 agentic
	- 判断 interaction_mode 时，重点看下面 3 个维度：
		1) 是否需要综合多来源信息，而不是回答单点事实
		2) 是否需要归因、比较多个因素、形成分析框架或结论
		3) 是否预期会触发工具检索、持续跟进、或多步执行
	- 正反例：
		- “金价今天多少”更接近 standard
		- “为什么今天金价涨了这么多，简单说下”通常更接近 standard
		- “综合各方信息资源，帮我分析金价剧烈波动的主要原因”更接近 agentic
		- “结合历史和最新信息，研判后续走势并说明主要驱动因素”更接近 agentic
   - standard: 单轮问答、寒暄、解释、轻聊天、简单追问、单点事实查询。
   

6. reasoning_effort 用于给后续 agentic 对话提供思考深度建议：
   - minimal: 几乎不需要推理，简单接话或直接执行单步任务
   - low: 需要少量分析，任务较明确
   - medium: 需要多步分析或权衡，是默认的 agentic 深度
   - high: 明显要求深入分析、复杂规划、强约束推演
   - 如果 interaction_mode=standard，通常返回 minimal
   - 如果 interaction_mode=agentic，请务必给出最合适的 reasoning_effort

请以JSON格式返回分析结果，格式如下：
{
  "intent_type": "question|chat|share|command|ignore",
  "need_reply": true/false,
  "reply_confidence": 85,
  "reason": "用户在提问，需要回答",
  "suggest_action": "chat|react|repeat|ignore",
  "interaction_mode": "standard|agentic",
  "reasoning_effort": "minimal|low|medium|high"
}`

// AnalyzeMessage 分析消息意图
func AnalyzeMessage(ctx context.Context, message string) (analysis *IntentAnalysis, err error) {
	return analyzeMessage(ctx, message, appconfig.NewAccessor(ctx, "", "").IntentLiteModel())
}

func analyzeMessage(ctx context.Context, message, modelID string) (analysis *IntentAnalysis, err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	span.SetAttributes(
		attribute.Key("message.len").Int(len(message)),
		attribute.Key("message.preview").String(message),
		attribute.Key("reasoning_effort").String(responses.ReasoningEffort_minimal.String()),
	)

	if message == "" {
		return nil, errors.New("empty message")
	}

	responseText, err := responseTextWithCacheFn(ctx, ark_dal.CachedResponseRequest{
		CacheScene:   "intent",
		SystemPrompt: intentSystemPrompt,
		UserPrompt:   message,
		ModelID:      modelID,
		Text: &responses.ResponsesText{
			Format: &responses.TextFormat{
				Type: responses.TextType_json_object,
			},
		},
		// Reasoning: &responses.ResponsesReasoning{
		// 	Effort: responses.ReasoningEffort_minimal,
		// },
		Thinking: &responses.ResponsesThinking{
			Type: gptr.Of(responses.ThinkingType_disabled),
		},
	})
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to create responses for intent analysis", zap.Error(err))
		return nil, err
	}

	if responseText == "" {
		return nil, errors.New("empty response from model")
	}

	// 解析 JSON
	analysis = &IntentAnalysis{}
	if err := json.Unmarshal([]byte(responseText), analysis); err != nil {
		logs.L().Ctx(ctx).Error("failed to unmarshal intent analysis", zap.String("response", responseText), zap.Error(err))
		return nil, err
	}

	// 校验并设置默认值
	analysis.Sanitize()

	span.SetAttributes(
		attribute.Key("intent_type").String(string(analysis.IntentType)),
		attribute.Key("need_reply").Bool(analysis.NeedReply),
		attribute.Key("reply_confidence").Int(analysis.ReplyConfidence),
		attribute.Key("suggest_action").String(string(analysis.SuggestAction)),
		attribute.Key("interaction_mode").String(string(analysis.InteractionMode)),
		attribute.Key("recommended_reasoning_effort").String(analysis.ReasoningEffort.String()),
	)

	logs.L().Ctx(ctx).Info("intent analysis completed",
		zap.String("intent_type", string(analysis.IntentType)),
		zap.Bool("need_reply", analysis.NeedReply),
		zap.Int("confidence", analysis.ReplyConfidence),
		zap.String("reason", analysis.Reason),
		zap.String("interaction_mode", string(analysis.InteractionMode)),
		zap.String("reasoning_effort", analysis.ReasoningEffort.String()),
	)

	return analysis, nil
}

// DefaultReasoningEffort returns the fallback reasoning depth for the given interaction mode.
func DefaultReasoningEffort(mode InteractionMode) responses.ReasoningEffort_Enum {
	return intentmeta.DefaultReasoningEffort(mode)
}

// NormalizeReasoningEffort validates a model-returned effort and falls back by interaction mode.
func NormalizeReasoningEffort(
	effort responses.ReasoningEffort_Enum,
	mode InteractionMode,
) responses.ReasoningEffort_Enum {
	return intentmeta.NormalizeReasoningEffort(effort, mode)
}

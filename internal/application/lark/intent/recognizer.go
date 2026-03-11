package intent

import (
	"context"
	"encoding/json"
	"errors"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/bytedance/gg/gptr"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

// IntentType 定义意图类型
type IntentType string

const (
	IntentTypeQuestion IntentType = "question" // 询问：用户在提问，需要回答
	IntentTypeChat     IntentType = "chat"     // 聊天：日常对话，可以回复
	IntentTypeShare    IntentType = "share"    // 分享：用户在分享内容，可以互动
	IntentTypeCommand  IntentType = "command"  // 命令：用户在使用命令
	IntentTypeIgnore   IntentType = "ignore"   // 忽略：不需要回复
)

// SuggestAction 建议动作
type SuggestAction string

const (
	SuggestActionChat   SuggestAction = "chat"   // 聊天回复
	SuggestActionReact  SuggestAction = "react"  // 发送表情反应
	SuggestActionRepeat SuggestAction = "repeat" // 重复消息
	SuggestActionIgnore SuggestAction = "ignore" // 忽略
)

// IntentAnalysis 意图分析结果
type IntentAnalysis struct {
	IntentType      IntentType    `json:"intent_type"`      // 意图类型
	NeedReply       bool          `json:"need_reply"`       // 是否需要回复
	ReplyConfidence int           `json:"reply_confidence"` // 回复置信度 0-100
	Reason          string        `json:"reason"`           // 判断理由
	SuggestAction   SuggestAction `json:"suggest_action"`   // 建议动作
}

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

请以JSON格式返回分析结果，格式如下：
{
  "intent_type": "question|chat|share|command|ignore",
  "need_reply": true/false,
  "reply_confidence": 85,
  "reason": "用户在提问，需要回答",
  "suggest_action": "chat|react|repeat|ignore"
}`

// AnalyzeMessage 分析消息意图
func AnalyzeMessage(ctx context.Context, message string) (analysis *IntentAnalysis, err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	span.SetAttributes(
		attribute.Key("message.len").Int(len(message)),
		attribute.Key("message.preview").String(message),
	)

	if message == "" {
		return nil, errors.New("empty message")
	}

	// 构建请求
	items := buildInputItems(intentSystemPrompt, message)
	input := &responses.ResponsesInput{
		Union: &responses.ResponsesInput_ListValue{
			ListValue: &responses.InputItemList{
				ListValue: items,
			},
		},
	}

	req := &responses.ResponsesRequest{
		Model: appconfig.NewAccessor(ctx, "", "").IntentLiteModel(),
		Input: input,
		Store: gptr.Of(true),
		Text: &responses.ResponsesText{
			Format: &responses.TextFormat{
				Type: responses.TextType_json_object,
			},
		},
	}

	// 调用 LLM
	resp, err := ark_dal.CreateResponses(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to create responses for intent analysis", zap.Error(err))
		return nil, err
	}

	// 解析响应
	var responseText string
	for _, output := range resp.GetOutput() {
		if msg := output.GetOutputMessage(); msg != nil {
			if content := msg.GetContent(); len(content) > 0 {
				responseText = content[0].GetText().GetText()
				break
			}
		}
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
	analysis.sanitize()

	span.SetAttributes(
		attribute.Key("intent_type").String(string(analysis.IntentType)),
		attribute.Key("need_reply").Bool(analysis.NeedReply),
		attribute.Key("reply_confidence").Int(analysis.ReplyConfidence),
		attribute.Key("suggest_action").String(string(analysis.SuggestAction)),
	)

	logs.L().Ctx(ctx).Info("intent analysis completed",
		zap.String("intent_type", string(analysis.IntentType)),
		zap.Bool("need_reply", analysis.NeedReply),
		zap.Int("confidence", analysis.ReplyConfidence),
		zap.String("reason", analysis.Reason),
	)

	return analysis, nil
}

// sanitize 校验并设置默认值
func (a *IntentAnalysis) sanitize() {
	// 验证意图类型
	switch a.IntentType {
	case IntentTypeQuestion, IntentTypeChat, IntentTypeShare, IntentTypeCommand, IntentTypeIgnore:
	// 有效类型
	default:
		a.IntentType = IntentTypeChat
	}

	// 验证建议动作
	switch a.SuggestAction {
	case SuggestActionChat, SuggestActionReact, SuggestActionRepeat, SuggestActionIgnore:
	// 有效动作
	default:
		a.SuggestAction = SuggestActionIgnore
	}

	// 确保置信度在 0-100 范围内
	if a.ReplyConfidence < 0 {
		a.ReplyConfidence = 0
	}
	if a.ReplyConfidence > 100 {
		a.ReplyConfidence = 100
	}

	// 根据意图类型自动设置 NeedReply
	if a.IntentType == IntentTypeQuestion {
		a.NeedReply = true
	} else if a.IntentType == IntentTypeIgnore {
		a.NeedReply = false
	}
}

// buildInputItems 构建输入项
func buildInputItems(sysPrompt, userPrompt string) []*responses.InputItem {
	res := make([]*responses.InputItem, 0)
	if sysPrompt != "" {
		res = append(res, &responses.InputItem{
			Union: &responses.InputItem_InputMessage{
				InputMessage: &responses.ItemInputMessage{
					Role: responses.MessageRole_system,
					Content: []*responses.ContentItem{
						{
							Union: &responses.ContentItem_Text{
								Text: &responses.ContentItemText{
									Type: responses.ContentItemType_input_text,
									Text: sysPrompt,
								},
							},
						},
					},
				},
			},
		})
	}
	if userPrompt != "" {
		res = append(res, &responses.InputItem{
			Union: &responses.InputItem_InputMessage{
				InputMessage: &responses.ItemInputMessage{
					Role: responses.MessageRole_user,
					Content: []*responses.ContentItem{
						{
							Union: &responses.ContentItem_Text{
								Text: &responses.ContentItemText{
									Type: responses.ContentItemType_input_text,
									Text: userPrompt,
								},
							},
						},
					},
				},
			},
		})
	}
	return res
}

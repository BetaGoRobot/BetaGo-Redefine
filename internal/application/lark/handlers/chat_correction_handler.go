package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// ==========================================
// store_correction Tool
// ==========================================

type StoreCorrectionArgs struct {
	OriginalContext string `json:"original_context"` // 原始上下文/对话摘要
	Correction      string `json:"correction"`       // 正确的回复
	Reason          string `json:"reason"`           // 可选，纠正原因
}

type ChatCorrection struct {
	Timestamp        string `json:"timestamp"`
	UserID           string `json:"user_id"`
	UserName         string `json:"user_name"`
	OriginalContext  string `json:"original_context"`
	Correction       string `json:"correction"`
	Reason           string `json:"reason,omitempty"`
}

type chatCorrectionHandler struct{}

var StoreCorrection chatCorrectionHandler

const correctionResultKey = "correction_result"

func (chatCorrectionHandler) ParseTool(raw string) (StoreCorrectionArgs, error) {
	parsed := StoreCorrectionArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return StoreCorrectionArgs{}, err
	}
	if parsed.OriginalContext == "" || parsed.Correction == "" {
		return StoreCorrectionArgs{}, fmt.Errorf("original_context and correction are required")
	}
	return parsed, nil
}

func (chatCorrectionHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "store_correction",
		Desc: "当用户纠正AI的回复时，调用此工具记录纠正内容。AI会自动识别纠错意图（如用户说'不是的，应该是xxx'）并调用此工具。",
		Params: arktools.NewParams("object").
			AddProp("original_context", &arktools.Prop{
				Type: "string",
				Desc: "原始对话上下文或AI的回复摘要",
			}).
			AddProp("correction", &arktools.Prop{
				Type: "string",
				Desc: "用户指定的正确回复或纠正",
			}).
			AddProp("reason", &arktools.Prop{
				Type: "string",
				Desc: "可选，纠正原因",
			}).
			AddRequired("original_context").
			AddRequired("correction"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(correctionResultKey)
			return result
		},
	}
}

func (chatCorrectionHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg StoreCorrectionArgs) error {
	chatID := currentChatID(data, metaData)
	openID := currentOpenID(data, metaData)
	if chatID == "" {
		return fmt.Errorf("chat_id is required")
	}
	// 获取用户名
	userName, _ := larkuser.GetUserNameCache(ctx, chatID, openID)

	correction := ChatCorrection{
		Timestamp:       time.Now().Format(time.RFC3339),
		UserID:          openID,
		UserName:        userName,
		OriginalContext: arg.OriginalContext,
		Correction:      arg.Correction,
		Reason:          arg.Reason,
	}

	cfgManager := config.GetManager()
	// Read existing corrections (chat-scoped, so openID is empty)
	existingJSON := cfgManager.GetString(ctx, config.KeyChatCorrections, chatID, "")
	var corrections []ChatCorrection
	if existingJSON != "" {
		if err := json.Unmarshal([]byte(existingJSON), &corrections); err != nil {
			corrections = []ChatCorrection{}
		}
	}

	// Append new correction
	corrections = append(corrections, correction)

	// Save back
	newJSON, err := json.Marshal(corrections)
	if err != nil {
		return fmt.Errorf("failed to marshal corrections: %w", err)
	}
	if err := cfgManager.SetString(ctx, config.KeyChatCorrections, config.ScopeChat, chatID, "", string(newJSON)); err != nil {
		return fmt.Errorf("failed to save correction: %w", err)
	}

	msg := fmt.Sprintf("✅ 纠正已记录\n\n原始: %s\n纠正: %s", truncate(arg.OriginalContext, 50), truncate(arg.Correction, 50))
	metaData.SetExtra(correctionResultKey, msg)
	return sendCompatibleText(ctx, data, metaData, msg, "_storeCorrection", false)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ==========================================
// set_chat_context Tool
// ==========================================

type SetChatContextArgs struct {
	ContextType string `json:"context_type"` // "extra_context" or "persona"
	Content     string `json:"content"`       // 上下文内容
}

type chatContextHandler struct{}

var SetChatContext chatContextHandler

const chatContextResultKey = "chat_context_result"

func (chatContextHandler) ParseTool(raw string) (SetChatContextArgs, error) {
	parsed := SetChatContextArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return SetChatContextArgs{}, err
	}
	if parsed.ContextType != "extra_context" && parsed.ContextType != "persona" {
		return SetChatContextArgs{}, fmt.Errorf("context_type must be 'extra_context' or 'persona'")
	}
	if parsed.Content == "" {
		return SetChatContextArgs{}, fmt.Errorf("content is required")
	}
	return parsed, nil
}

func (chatContextHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "set_chat_context",
		Desc: "设置该群聊的专属人设或额外上下文。persona会完全替换默认system prompt，extra_context会附加到system prompt之后。",
		Params: arktools.NewParams("object").
			AddProp("context_type", &arktools.Prop{
				Type: "string",
				Desc: "上下文类型: extra_context(附加到system prompt) 或 persona(替换默认system prompt)",
				Enum: []any{"extra_context", "persona"},
			}).
			AddProp("content", &arktools.Prop{
				Type: "string",
				Desc: "要设置的上下文内容",
			}).
			AddRequired("context_type").
			AddRequired("content"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(chatContextResultKey)
			return result
		},
	}
}

func (chatContextHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg SetChatContextArgs) error {
	chatID := currentChatID(data, metaData)
	if chatID == "" {
		return fmt.Errorf("chat_id is required")
	}

	cfgManager := config.GetManager()
	var key config.ConfigKey
	switch arg.ContextType {
	case "extra_context":
		key = config.KeyChatExtraContext
	case "persona":
		key = config.KeyChatPersona
	default:
		return fmt.Errorf("invalid context_type: %s", arg.ContextType)
	}

	if err := cfgManager.SetString(ctx, key, config.ScopeChat, chatID, "", arg.Content); err != nil {
		return fmt.Errorf("failed to set chat context: %w", err)
	}

	typeLabel := map[string]string{"extra_context": "额外上下文", "persona": "人设"}[arg.ContextType]
	msg := fmt.Sprintf("✅ %s已设置\n\n内容: %s", typeLabel, truncate(arg.Content, 100))
	metaData.SetExtra(chatContextResultKey, msg)
	return sendCompatibleText(ctx, data, metaData, msg, "_setChatContext", false)
}

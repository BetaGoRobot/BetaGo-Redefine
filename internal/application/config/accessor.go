package config

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

// Accessor 统一配置访问接口
// 这是一个便捷的包装器，提供简洁的配置访问方法
type Accessor struct {
	ctx    context.Context
	chatID string
	userID string
}

// NewAccessor 创建配置访问器
func NewAccessor(ctx context.Context, chatID, userID string) *Accessor {
	return &Accessor{
		ctx:    ctx,
		chatID: chatID,
		userID: userID,
	}
}

// NewAccessorFromMeta 从 meta data 创建配置访问器
func NewAccessorFromMeta(ctx context.Context, meta *xhandler.BaseMetaData) *Accessor {
	return &Accessor{
		ctx:    ctx,
		chatID: meta.ChatID,
		userID: meta.UserID,
	}
}

// ==========================================
// 概率配置
// ==========================================

// ReactionDefaultRate 获取默认反应概率
func (a *Accessor) ReactionDefaultRate() int {
	return GetManager().GetInt(a.ctx, KeyReactionDefaultRate, a.chatID, a.userID)
}

// ReactionFollowDefaultRate 获取跟随反应概率
func (a *Accessor) ReactionFollowDefaultRate() int {
	return GetManager().GetInt(a.ctx, KeyReactionFollowDefaultRate, a.chatID, a.userID)
}

// RepeatDefaultRate 获取默认重复概率
func (a *Accessor) RepeatDefaultRate() int {
	return GetManager().GetInt(a.ctx, KeyRepeatDefaultRate, a.chatID, a.userID)
}

// ImitateDefaultRate 获取默认模仿概率
func (a *Accessor) ImitateDefaultRate() int {
	return GetManager().GetInt(a.ctx, KeyImitateDefaultRate, a.chatID, a.userID)
}

// IntentFallbackRate 获取意图识别失败回退概率
func (a *Accessor) IntentFallbackRate() int {
	return GetManager().GetInt(a.ctx, KeyIntentFallbackRate, a.chatID, a.userID)
}

// IntentReplyThreshold 获取意图回复阈值
func (a *Accessor) IntentReplyThreshold() int {
	return GetManager().GetInt(a.ctx, KeyIntentReplyThreshold, a.chatID, a.userID)
}

// ==========================================
// 开关配置
// ==========================================

// IntentRecognitionEnabled 检查是否启用意图识别
func (a *Accessor) IntentRecognitionEnabled() bool {
	return GetManager().GetBool(a.ctx, KeyIntentRecognitionEnabled, a.chatID, a.userID)
}

// ==========================================
// 功能开关
// ==========================================

// IsFeatureEnabled 检查功能是否启用（保留用于兼容，建议使用带 defaultEnabled 的版本）
func (a *Accessor) IsFeatureEnabled(feature string) bool {
	return GetManager().IsFeatureEnabled(a.ctx, feature, true, a.chatID, a.userID)
}

// ==========================================
// 全局便捷函数（不绑定特定上下文）
// ==========================================

// GetReactionDefaultRate 获取默认反应概率
func GetReactionDefaultRate(ctx context.Context, chatID, userID string) int {
	return GetManager().GetInt(ctx, KeyReactionDefaultRate, chatID, userID)
}

// GetReactionFollowDefaultRate 获取跟随反应概率
func GetReactionFollowDefaultRate(ctx context.Context, chatID, userID string) int {
	return GetManager().GetInt(ctx, KeyReactionFollowDefaultRate, chatID, userID)
}

// GetRepeatDefaultRate 获取默认重复概率
func GetRepeatDefaultRate(ctx context.Context, chatID, userID string) int {
	return GetManager().GetInt(ctx, KeyRepeatDefaultRate, chatID, userID)
}

// GetImitateDefaultRate 获取默认模仿概率
func GetImitateDefaultRate(ctx context.Context, chatID, userID string) int {
	return GetManager().GetInt(ctx, KeyImitateDefaultRate, chatID, userID)
}

// GetIntentFallbackRate 获取意图识别失败回退概率
func GetIntentFallbackRate(ctx context.Context, chatID, userID string) int {
	return GetManager().GetInt(ctx, KeyIntentFallbackRate, chatID, userID)
}

// GetIntentReplyThreshold 获取意图回复阈值
func GetIntentReplyThreshold(ctx context.Context, chatID, userID string) int {
	return GetManager().GetInt(ctx, KeyIntentReplyThreshold, chatID, userID)
}

// IsIntentRecognitionEnabled 检查是否启用意图识别
func IsIntentRecognitionEnabled(ctx context.Context, chatID, userID string) bool {
	return GetManager().GetBool(ctx, KeyIntentRecognitionEnabled, chatID, userID)
}

// IsFeatureEnabled 检查功能是否启用（保留用于兼容，建议使用带 defaultEnabled 的版本）
func IsFeatureEnabled(ctx context.Context, feature, chatID, userID string) bool {
	return GetManager().IsFeatureEnabled(ctx, feature, true, chatID, userID)
}

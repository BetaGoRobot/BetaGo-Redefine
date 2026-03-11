package config

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

// Accessor 统一配置访问接口
// 这是一个便捷的包装器，提供简洁的配置访问方法
type Accessor struct {
	ctx     context.Context
	chatID  string
	openID  string
	manager *Manager
}

// NewAccessor 创建配置访问器
func NewAccessor(ctx context.Context, chatID, openID string) *Accessor {
	return &Accessor{
		ctx:     ctx,
		chatID:  chatID,
		openID:  openID,
		manager: GetManager(), // 默认使用全局管理器（向后兼容）
	}
}

// NewAccessorWithManager 使用指定的管理器创建配置访问器
func NewAccessorWithManager(ctx context.Context, chatID, openID string, manager *Manager) *Accessor {
	return &Accessor{
		ctx:     ctx,
		chatID:  chatID,
		openID:  openID,
		manager: manager,
	}
}

// NewAccessorFromMeta 从 meta data 创建配置访问器
func NewAccessorFromMeta(ctx context.Context, meta *xhandler.BaseMetaData) *Accessor {
	return &Accessor{
		ctx:     ctx,
		chatID:  meta.ChatID,
		openID:  meta.OpenID,
		manager: GetManager(), // 默认使用全局管理器（向后兼容）
	}
}

// NewAccessorFromMetaWithManager 从 meta data 创建配置访问器（使用指定管理器）
func NewAccessorFromMetaWithManager(ctx context.Context, meta *xhandler.BaseMetaData, manager *Manager) *Accessor {
	return &Accessor{
		ctx:     ctx,
		chatID:  meta.ChatID,
		openID:  meta.OpenID,
		manager: manager,
	}
}

// ==========================================
// 概率配置
// ==========================================

// ReactionDefaultRate 获取默认反应概率
func (a *Accessor) ReactionDefaultRate() int {
	return a.manager.GetInt(a.ctx, KeyReactionDefaultRate, a.chatID, a.openID)
}

// ReactionFollowDefaultRate 获取跟随反应概率
func (a *Accessor) ReactionFollowDefaultRate() int {
	return a.manager.GetInt(a.ctx, KeyReactionFollowDefaultRate, a.chatID, a.openID)
}

// RepeatDefaultRate 获取默认重复概率
func (a *Accessor) RepeatDefaultRate() int {
	return a.manager.GetInt(a.ctx, KeyRepeatDefaultRate, a.chatID, a.openID)
}

// ImitateDefaultRate 获取默认模仿概率
func (a *Accessor) ImitateDefaultRate() int {
	return a.manager.GetInt(a.ctx, KeyImitateDefaultRate, a.chatID, a.openID)
}

// IntentFallbackRate 获取意图识别失败回退概率
func (a *Accessor) IntentFallbackRate() int {
	return a.manager.GetInt(a.ctx, KeyIntentFallbackRate, a.chatID, a.openID)
}

// IntentReplyThreshold 获取意图回复阈值
func (a *Accessor) IntentReplyThreshold() int {
	return a.manager.GetInt(a.ctx, KeyIntentReplyThreshold, a.chatID, a.openID)
}

// ==========================================
// 开关配置
// ==========================================

// IntentRecognitionEnabled 检查是否启用意图识别
func (a *Accessor) IntentRecognitionEnabled() bool {
	return a.manager.GetBool(a.ctx, KeyIntentRecognitionEnabled, a.chatID, a.openID)
}

// MusicCardInThread 检查音乐卡片是否默认回帖中发送
func (a *Accessor) MusicCardInThread() bool {
	return a.manager.GetBool(a.ctx, KeyMusicCardInThread, a.chatID, a.openID)
}

// WithDrawReplace 检查是否使用伪撤回替代真实撤回
func (a *Accessor) WithDrawReplace() bool {
	return a.manager.GetBool(a.ctx, KeyWithDrawReplace, a.chatID, a.openID)
}

// ChatReasoningModel 获取推理模型
func (a *Accessor) ChatReasoningModel() string {
	return a.manager.GetString(a.ctx, KeyChatReasoningModel, a.chatID, a.openID)
}

// ChatNormalModel 获取普通聊天模型
func (a *Accessor) ChatNormalModel() string {
	return a.manager.GetString(a.ctx, KeyChatNormalModel, a.chatID, a.openID)
}

// IntentLiteModel 获取意图识别模型
func (a *Accessor) IntentLiteModel() string {
	return a.manager.GetString(a.ctx, KeyIntentLiteModel, a.chatID, a.openID)
}

// LarkMsgIndex 获取消息索引
func (a *Accessor) LarkMsgIndex() string {
	return a.manager.GetString(a.ctx, KeyLarkMsgIndex, a.chatID, a.openID)
}

// LarkChunkIndex 获取 chunk 索引
func (a *Accessor) LarkChunkIndex() string {
	return a.manager.GetString(a.ctx, KeyLarkChunkIndex, a.chatID, a.openID)
}

// ==========================================
// 功能开关
// ==========================================

// IsFeatureEnabled 检查功能是否启用（保留用于兼容，建议使用带 defaultEnabled 的版本）
func (a *Accessor) IsFeatureEnabled(feature string) bool {
	return a.manager.IsFeatureEnabled(a.ctx, feature, true, a.chatID, a.openID)
}

// IsFeatureEnabledWithDefault 检查功能是否启用
func (a *Accessor) IsFeatureEnabledWithDefault(feature string, defaultEnabled bool) bool {
	return a.manager.IsFeatureEnabled(a.ctx, feature, defaultEnabled, a.chatID, a.openID)
}

// ==========================================
// 全局便捷函数（保留用于向后兼容）
// ==========================================

// GetReactionDefaultRate 获取默认反应概率
func GetReactionDefaultRate(ctx context.Context, chatID, openID string) int {
	return GetManager().GetInt(ctx, KeyReactionDefaultRate, chatID, openID)
}

// GetReactionFollowDefaultRate 获取跟随反应概率
func GetReactionFollowDefaultRate(ctx context.Context, chatID, openID string) int {
	return GetManager().GetInt(ctx, KeyReactionFollowDefaultRate, chatID, openID)
}

// GetRepeatDefaultRate 获取默认重复概率
func GetRepeatDefaultRate(ctx context.Context, chatID, openID string) int {
	return GetManager().GetInt(ctx, KeyRepeatDefaultRate, chatID, openID)
}

// GetImitateDefaultRate 获取默认模仿概率
func GetImitateDefaultRate(ctx context.Context, chatID, openID string) int {
	return GetManager().GetInt(ctx, KeyImitateDefaultRate, chatID, openID)
}

// GetIntentFallbackRate 获取意图识别失败回退概率
func GetIntentFallbackRate(ctx context.Context, chatID, openID string) int {
	return GetManager().GetInt(ctx, KeyIntentFallbackRate, chatID, openID)
}

// GetIntentReplyThreshold 获取意图回复阈值
func GetIntentReplyThreshold(ctx context.Context, chatID, openID string) int {
	return GetManager().GetInt(ctx, KeyIntentReplyThreshold, chatID, openID)
}

// IsIntentRecognitionEnabled 检查是否启用意图识别
func IsIntentRecognitionEnabled(ctx context.Context, chatID, openID string) bool {
	return GetManager().GetBool(ctx, KeyIntentRecognitionEnabled, chatID, openID)
}

func GetMusicCardInThread(ctx context.Context, chatID, openID string) bool {
	return GetManager().GetBool(ctx, KeyMusicCardInThread, chatID, openID)
}

func GetWithDrawReplace(ctx context.Context, chatID, openID string) bool {
	return GetManager().GetBool(ctx, KeyWithDrawReplace, chatID, openID)
}

func GetChatReasoningModel(ctx context.Context, chatID, openID string) string {
	return GetManager().GetString(ctx, KeyChatReasoningModel, chatID, openID)
}

func GetChatNormalModel(ctx context.Context, chatID, openID string) string {
	return GetManager().GetString(ctx, KeyChatNormalModel, chatID, openID)
}

func GetIntentLiteModel(ctx context.Context, chatID, openID string) string {
	return GetManager().GetString(ctx, KeyIntentLiteModel, chatID, openID)
}

func GetLarkMsgIndex(ctx context.Context, chatID, openID string) string {
	return GetManager().GetString(ctx, KeyLarkMsgIndex, chatID, openID)
}

func GetLarkChunkIndex(ctx context.Context, chatID, openID string) string {
	return GetManager().GetString(ctx, KeyLarkChunkIndex, chatID, openID)
}

// IsFeatureEnabled 检查功能是否启用（保留用于兼容，建议使用带 defaultEnabled 的版本）
func IsFeatureEnabled(ctx context.Context, feature, chatID, openID string) bool {
	return GetManager().IsFeatureEnabled(ctx, feature, true, chatID, openID)
}

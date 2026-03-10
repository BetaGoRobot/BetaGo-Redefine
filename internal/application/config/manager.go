package config

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// ConfigKey 配置键类型
type ConfigKey string

const (
	// 概率配置
	KeyReactionDefaultRate       ConfigKey = "reaction_default_rate"
	KeyReactionFollowDefaultRate ConfigKey = "reaction_follow_default_rate"
	KeyRepeatDefaultRate         ConfigKey = "repeat_default_rate"
	KeyImitateDefaultRate        ConfigKey = "imitate_default_rate"
	KeyIntentFallbackRate        ConfigKey = "intent_fallback_rate"
	KeyIntentReplyThreshold      ConfigKey = "intent_reply_threshold"

	// 开关配置
	KeyIntentRecognitionEnabled ConfigKey = "intent_recognition_enabled"
)

// ConfigScope 配置作用域
type ConfigScope string

const (
	ScopeGlobal ConfigScope = "global"
	ScopeChat   ConfigScope = "chat"
	ScopeUser   ConfigScope = "user"
)

// Feature 功能定义
type Feature struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Category       string `json:"category"`
	DefaultEnabled bool   `json:"default_enabled"` // 默认是否启用
}

var currentBotIdentity = botidentity.Current

// IsValidFeature 检查功能名称是否有效（始终返回 true，兼容旧代码）
func IsValidFeature(name string) bool {
	return true
}

// buildConfigKey 构建带作用域的配置键
// 格式: bot:app_id:bot_open_id:scope:chat_id:user_id:key
func buildConfigKey(scope ConfigScope, chatID, userID string, key ConfigKey) string {
	return namespaceConfigKey(buildRawConfigKey(scope, chatID, userID, key))
}

func buildRawConfigKey(scope ConfigScope, chatID, userID string, key ConfigKey) string {
	parts := []string{string(scope)}
	if chatID != "" {
		parts = append(parts, chatID)
	}
	if userID != "" {
		parts = append(parts, userID)
	}
	parts = append(parts, string(key))
	return strings.Join(parts, ":")
}

// buildFeatureBlockKey 构建功能屏蔽的配置键
func buildFeatureBlockKey(scope ConfigScope, chatID, userID, feature string) string {
	return namespaceConfigKey(buildRawFeatureBlockKey(scope, chatID, userID, feature))
}

func buildRawFeatureBlockKey(scope ConfigScope, chatID, userID, feature string) string {
	parts := []string{"feature_block", string(scope)}
	if chatID != "" {
		parts = append(parts, chatID)
	}
	if userID != "" {
		parts = append(parts, userID)
	}
	parts = append(parts, feature)
	return strings.Join(parts, ":")
}

func namespaceConfigKey(rawKey string) string {
	if rawKey == "" {
		return ""
	}
	namespace := currentBotNamespacePrefix()
	if namespace == "" {
		return rawKey
	}
	return namespace + ":" + rawKey
}

func currentBotNamespacePrefix() string {
	identity := currentBotIdentity()
	if !identity.Valid() {
		return ""
	}
	return strings.Join([]string{
		"bot",
		strings.TrimSpace(identity.AppID),
		strings.TrimSpace(identity.BotOpenID),
	}, ":")
}

func buildConfigListPrefix(scope ConfigScope, chatID, userID string) string {
	rawPrefix := buildRawConfigListPrefix(scope, chatID, userID)
	namespace := currentBotNamespacePrefix()
	switch {
	case namespace == "":
		return rawPrefix
	case rawPrefix == "":
		return namespace + ":"
	default:
		return namespace + ":" + rawPrefix
	}
}

func buildRawConfigListPrefix(scope ConfigScope, chatID, userID string) string {
	switch scope {
	case ScopeGlobal:
		return "global:"
	case ScopeChat:
		if chatID != "" {
			return fmt.Sprintf("chat:%s:", chatID)
		}
		return "chat:"
	case ScopeUser:
		if chatID != "" && userID != "" {
			return fmt.Sprintf("user:%s:%s:", chatID, userID)
		}
		if userID != "" {
			return fmt.Sprintf("user::%s:", userID)
		}
		return "user:"
	default:
		return ""
	}
}

func stripBotNamespace(fullKey string) string {
	parts := strings.SplitN(fullKey, ":", 4)
	if len(parts) == 4 && parts[0] == "bot" {
		return parts[3]
	}
	return fullKey
}

// Manager 配置管理器
type Manager struct {
	cache           map[string]string
	mu              sync.RWMutex
	getFeaturesFunc func() []Feature // 获取功能列表的回调
}

type ConfigReadOptions struct {
	BypassCache bool
}

// NewManager 创建新的配置管理器
func NewManager() *Manager {
	return &Manager{
		cache: make(map[string]string),
	}
}

// SetGetFeaturesFunc 设置获取功能列表的回调
func (m *Manager) SetGetFeaturesFunc(fn func() []Feature) {
	m.getFeaturesFunc = fn
}

// ==========================================
// 配置读取方法（支持多级优先级）
// ==========================================

// GetInt 获取整数配置
// 优先级: chat:user > user > chat > global > toml > default
func (m *Manager) GetInt(ctx context.Context, key ConfigKey, chatID, userID string) int {
	// 1. 尝试 chat:user 级别
	if chatID != "" && userID != "" {
		if val, ok := m.getConfig(ctx, ScopeUser, chatID, userID, key); ok {
			if intVal, err := strconv.Atoi(val); err == nil {
				return intVal
			}
		}
	}

	// 2. 尝试 user 级别
	if userID != "" {
		if val, ok := m.getConfig(ctx, ScopeUser, "", userID, key); ok {
			if intVal, err := strconv.Atoi(val); err == nil {
				return intVal
			}
		}
	}

	// 3. 尝试 chat 级别
	if chatID != "" {
		if val, ok := m.getConfig(ctx, ScopeChat, chatID, "", key); ok {
			if intVal, err := strconv.Atoi(val); err == nil {
				return intVal
			}
		}
	}

	// 4. 尝试 global 级别
	if val, ok := m.getConfig(ctx, ScopeGlobal, "", "", key); ok {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}

	// 5. 回退到 TOML 配置
	return m.getIntFromToml(key)
}

// GetBool 获取布尔配置
func (m *Manager) GetBool(ctx context.Context, key ConfigKey, chatID, userID string) bool {
	// 1. 尝试 chat:user 级别
	if chatID != "" && userID != "" {
		if val, ok := m.getConfig(ctx, ScopeUser, chatID, userID, key); ok {
			if boolVal, err := strconv.ParseBool(val); err == nil {
				return boolVal
			}
		}
	}

	// 2. 尝试 user 级别
	if userID != "" {
		if val, ok := m.getConfig(ctx, ScopeUser, "", userID, key); ok {
			if boolVal, err := strconv.ParseBool(val); err == nil {
				return boolVal
			}
		}
	}

	// 3. 尝试 chat 级别
	if chatID != "" {
		if val, ok := m.getConfig(ctx, ScopeChat, chatID, "", key); ok {
			if boolVal, err := strconv.ParseBool(val); err == nil {
				return boolVal
			}
		}
	}

	// 4. 尝试 global 级别
	if val, ok := m.getConfig(ctx, ScopeGlobal, "", "", key); ok {
		if boolVal, err := strconv.ParseBool(val); err == nil {
			return boolVal
		}
	}

	// 5. 回退到 TOML 配置
	return m.getBoolFromToml(key)
}

// getConfig 从数据库获取配置
func (m *Manager) getConfig(ctx context.Context, scope ConfigScope, chatID, userID string, key ConfigKey) (string, bool) {
	return m.getConfigWithOptions(ctx, scope, chatID, userID, key, ConfigReadOptions{})
}

func (m *Manager) getConfigWithOptions(ctx context.Context, scope ConfigScope, chatID, userID string, key ConfigKey, options ConfigReadOptions) (string, bool) {
	fullKey := buildConfigKey(scope, chatID, userID, key)
	return m.getConfigByFullKeyWithOptions(ctx, fullKey, options)
}

// getConfigByFullKey 通过完整键获取配置
func (m *Manager) getConfigByFullKey(ctx context.Context, fullKey string) (string, bool) {
	return m.getConfigByFullKeyWithOptions(ctx, fullKey, ConfigReadOptions{})
}

func (m *Manager) getConfigByFullKeyWithOptions(ctx context.Context, fullKey string, options ConfigReadOptions) (string, bool) {
	ctx, span := otel.StartNamed(ctx, "config.get")
	defer span.End()
	span.SetAttributes(
		attribute.String("config.key.preview", otel.PreviewString(fullKey, 128)),
		attribute.Int("config.key.len", len(fullKey)),
		attribute.Bool("config.bypass_cache", options.BypassCache),
	)
	if !options.BypassCache {
		m.mu.RLock()
		if val, ok := m.cache[fullKey]; ok {
			m.mu.RUnlock()
			span.AddEvent("config.cache.hit")
			return val, true
		}
		m.mu.RUnlock()
	}
	span.AddEvent("config.cache.miss")

	ins := query.Q.DynamicConfig
	if options.BypassCache {
		if noCacheQuery := infraDB.QueryWithoutCache(); noCacheQuery != nil {
			ins = noCacheQuery.DynamicConfig
		}
	}

	cfgs, err := ins.WithContext(ctx).
		Where(ins.Key.Eq(fullKey)).
		Find()

	if err == nil && len(cfgs) > 0 {
		cfg := cfgs[0]
		span.SetAttributes(attribute.Bool("config.found", true))
		m.mu.Lock()
		m.cache[fullKey] = cfg.Value
		m.mu.Unlock()
		return cfg.Value, true
	}
	otel.RecordError(span, err)
	span.SetAttributes(attribute.Bool("config.found", false))

	return "", false
}

// getIntFromToml 从 TOML 配置获取整数
func (m *Manager) getIntFromToml(key ConfigKey) int {
	cfg := config.Get()
	if cfg == nil || cfg.RateConfig == nil {
		return m.getDefaultInt(key)
	}

	switch key {
	case KeyReactionDefaultRate:
		return cfg.RateConfig.ReactionDefaultRate
	case KeyReactionFollowDefaultRate:
		return cfg.RateConfig.ReactionFollowDefaultRate
	case KeyRepeatDefaultRate:
		return cfg.RateConfig.RepeatDefaultRate
	case KeyImitateDefaultRate:
		return cfg.RateConfig.ImitateDefaultRate
	case KeyIntentFallbackRate:
		return cfg.RateConfig.IntentFallbackRate
	case KeyIntentReplyThreshold:
		return cfg.RateConfig.IntentReplyThreshold
	default:
		return m.getDefaultInt(key)
	}
}

// getBoolFromToml 从 TOML 配置获取布尔值
func (m *Manager) getBoolFromToml(key ConfigKey) bool {
	cfg := config.Get()
	if cfg == nil || cfg.RateConfig == nil {
		return m.getDefaultBool(key)
	}

	switch key {
	case KeyIntentRecognitionEnabled:
		return cfg.RateConfig.IntentRecognitionEnabled
	default:
		return m.getDefaultBool(key)
	}
}

// getDefaultInt 获取默认整数值
func (m *Manager) getDefaultInt(key ConfigKey) int {
	switch key {
	case KeyReactionDefaultRate:
		return 30
	case KeyReactionFollowDefaultRate:
		return 10
	case KeyRepeatDefaultRate:
		return 10
	case KeyImitateDefaultRate:
		return 50
	case KeyIntentFallbackRate:
		return 10
	case KeyIntentReplyThreshold:
		return 70
	default:
		return 0
	}
}

// getDefaultBool 获取默认布尔值
func (m *Manager) getDefaultBool(key ConfigKey) bool {
	switch key {
	case KeyIntentRecognitionEnabled:
		return true
	default:
		return false
	}
}

// ==========================================
// 配置设置方法
// ==========================================

// SetInt 设置整数配置
func (m *Manager) SetInt(ctx context.Context, key ConfigKey, scope ConfigScope, chatID, userID string, value int) error {
	return m.SetString(ctx, key, scope, chatID, userID, strconv.Itoa(value))
}

// SetBool 设置布尔配置
func (m *Manager) SetBool(ctx context.Context, key ConfigKey, scope ConfigScope, chatID, userID string, value bool) error {
	return m.SetString(ctx, key, scope, chatID, userID, strconv.FormatBool(value))
}

// SetString 设置字符串配置
func (m *Manager) SetString(ctx context.Context, key ConfigKey, scope ConfigScope, chatID, userID string, value string) error {
	fullKey := buildConfigKey(scope, chatID, userID, key)
	return m.setConfigByFullKey(ctx, fullKey, value)
}

// setConfigByFullKey 通过完整键设置配置
func (m *Manager) setConfigByFullKey(ctx context.Context, fullKey, value string) (err error) {
	ctx, span := otel.StartNamed(ctx, "config.set")
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	span.SetAttributes(
		attribute.String("config.key.preview", otel.PreviewString(fullKey, 128)),
		attribute.Int("config.key.len", len(fullKey)),
		attribute.String("config.value.preview", otel.PreviewString(value, 128)),
		attribute.Int("config.value.len", len(value)),
	)
	err = query.Q.Transaction(func(tx *query.Query) error {
		existing, err := tx.DynamicConfig.WithContext(ctx).
			Where(query.DynamicConfig.Key.Eq(fullKey)).
			First()

		if err == nil && existing != nil {
			span.AddEvent("config.update")
			_, err = tx.DynamicConfig.WithContext(ctx).
				Where(query.DynamicConfig.Key.Eq(fullKey)).
				Update(query.DynamicConfig.Value, value)
		} else {
			span.AddEvent("config.create")
			err = tx.DynamicConfig.WithContext(ctx).
				Create(&model.DynamicConfig{
					Key:   fullKey,
					Value: value,
				})
		}

		if err != nil {
			return err
		}

		m.mu.Lock()
		m.cache[fullKey] = value
		m.mu.Unlock()

		return nil
	})
	return err
}

// DeleteConfig 删除配置
func (m *Manager) DeleteConfig(ctx context.Context, key ConfigKey, scope ConfigScope, chatID, userID string) error {
	fullKey := buildConfigKey(scope, chatID, userID, key)
	return m.deleteConfigByFullKey(ctx, fullKey)
}

// deleteConfigByFullKey 通过完整键删除配置
func (m *Manager) deleteConfigByFullKey(ctx context.Context, fullKey string) (err error) {
	ctx, span := otel.StartNamed(ctx, "config.delete")
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	span.SetAttributes(
		attribute.String("config.key.preview", otel.PreviewString(fullKey, 128)),
		attribute.Int("config.key.len", len(fullKey)),
	)
	_, err = query.Q.DynamicConfig.WithContext(ctx).
		Where(query.DynamicConfig.Key.Eq(fullKey)).
		Delete()

	if err == nil {
		m.mu.Lock()
		delete(m.cache, fullKey)
		m.mu.Unlock()
	}

	return err
}

// ==========================================
// 配置列表查询
// ==========================================

// ConfigEntry 配置条目
type ConfigEntry struct {
	Scope  ConfigScope `json:"scope"`
	ChatID string      `json:"chat_id,omitempty"`
	UserID string      `json:"user_id,omitempty"`
	Key    ConfigKey   `json:"key"`
	Value  string      `json:"value"`
}

// ListConfigs 列出指定作用域的配置
func (m *Manager) ListConfigs(ctx context.Context, scope ConfigScope, chatID, userID string) (entries []ConfigEntry, err error) {
	ctx, span := otel.StartNamed(ctx, "config.list")
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	if scope == "" {
		scope = ScopeChat
	}
	prefix := buildConfigListPrefix(scope, chatID, userID)
	span.SetAttributes(
		attribute.String("config.scope", string(scope)),
		attribute.String("config.chat_id", chatID),
		attribute.String("config.user_id", userID),
		attribute.String("config.prefix.preview", otel.PreviewString(prefix, 128)),
	)

	var results []*model.DynamicConfig

	if prefix != "" {
		results, err = query.Q.DynamicConfig.WithContext(ctx).
			Where(query.DynamicConfig.Key.Like(prefix + "%")).
			Find()
	} else {
		results, err = query.Q.DynamicConfig.WithContext(ctx).Find()
	}

	if err != nil {
		return nil, err
	}

	entries = make([]ConfigEntry, 0, len(results))
	for _, cfg := range results {
		if entry, ok := parseConfigKey(cfg.Key, cfg.Value); ok {
			entries = append(entries, entry)
		}
	}
	span.SetAttributes(attribute.Int("config.entries.count", len(entries)))

	return entries, nil
}

// parseConfigKey 解析配置键
func parseConfigKey(fullKey, value string) (ConfigEntry, bool) {
	parts := strings.Split(stripBotNamespace(fullKey), ":")
	if len(parts) < 2 {
		return ConfigEntry{}, false
	}

	scope := ConfigScope(parts[0])
	key := ConfigKey(parts[len(parts)-1])

	switch scope {
	case ScopeGlobal:
		if len(parts) == 2 {
			return ConfigEntry{Scope: scope, Key: key, Value: value}, true
		}
	case ScopeChat:
		if len(parts) == 3 {
			return ConfigEntry{Scope: scope, ChatID: parts[1], Key: key, Value: value}, true
		}
	case ScopeUser:
		if len(parts) == 3 {
			return ConfigEntry{Scope: scope, UserID: parts[1], Key: key, Value: value}, true
		}
		if len(parts) == 4 {
			return ConfigEntry{Scope: scope, ChatID: parts[1], UserID: parts[2], Key: key, Value: value}, true
		}
	}
	return ConfigEntry{}, false
}

// ==========================================
// 功能屏蔽管理（支持用户级别）
// ==========================================

// IsFeatureEnabled 检查功能是否启用
// 优先级: chat_user > user > chat > global > legacy function_enablings
// 返回 true 表示启用，false 表示禁用
func (m *Manager) IsFeatureEnabled(ctx context.Context, feature string, defaultEnabled bool, chatID, userID string) bool {
	ctx, span := otel.StartNamed(ctx, "config.feature_check")
	defer span.End()
	span.SetAttributes(
		attribute.String("feature.name", feature),
		attribute.Bool("feature.default_enabled", defaultEnabled),
		attribute.String("feature.chat_id", chatID),
		attribute.String("feature.user_id", userID),
	)
	// 1. 检查 chat_user 级别
	if chatID != "" && userID != "" {
		if m.isFeatureBlockedAtScope(ctx, ScopeUser, chatID, userID, feature) {
			span.AddEvent("feature.blocked", trace.WithAttributes(attribute.String("feature.scope", string(ScopeUser))))
			logs.L().Ctx(ctx).Debug("feature blocked at chat_user level",
				zap.String("feature", feature),
				zap.String("chat_id", chatID),
				zap.String("user_id", userID),
			)
			return false
		}
	}

	// 2. 检查 user 级别
	if userID != "" {
		if m.isFeatureBlockedAtScope(ctx, ScopeUser, "", userID, feature) {
			span.AddEvent("feature.blocked", trace.WithAttributes(attribute.String("feature.scope", "user")))
			logs.L().Ctx(ctx).Debug("feature blocked at user level",
				zap.String("feature", feature),
				zap.String("user_id", userID),
			)
			return false
		}
	}

	// 3. 检查 chat 级别（使用 dynamic_configs）
	if chatID != "" {
		if m.isFeatureBlockedAtScope(ctx, ScopeChat, chatID, "", feature) {
			span.AddEvent("feature.blocked", trace.WithAttributes(attribute.String("feature.scope", string(ScopeChat))))
			logs.L().Ctx(ctx).Debug("feature blocked at chat level",
				zap.String("feature", feature),
				zap.String("chat_id", chatID),
			)
			return false
		}
	}

	// 4. 检查 global 级别
	if m.isFeatureBlockedAtScope(ctx, ScopeGlobal, "", "", feature) {
		span.AddEvent("feature.blocked", trace.WithAttributes(attribute.String("feature.scope", string(ScopeGlobal))))
		logs.L().Ctx(ctx).Debug("feature blocked at global level",
			zap.String("feature", feature),
		)
		return false
	}

	// 5. 兼容检查旧的 function_enablings 表
	if chatID != "" {
		identity := currentBotIdentity()
		if identity.Valid() {
			fcQuery := query.Q.FunctionEnabling.WithContext(ctx).
				Where(query.FunctionEnabling.GuildID.Eq(chatID)).
				Where(query.FunctionEnabling.Function.Eq(feature))
			if identity.AppID != "" {
				fcQuery = fcQuery.Where(query.FunctionEnabling.AppID.Eq(identity.AppID))
			}
			if identity.BotOpenID != "" {
				fcQuery = fcQuery.Where(query.FunctionEnabling.BotOpenID.Eq(identity.BotOpenID))
			}

			fcs, err := fcQuery.Find()

			if err == nil && len(fcs) > 0 {
				fc := fcs[0]
				if fc.Disable {
					span.AddEvent("feature.blocked", trace.WithAttributes(attribute.String("feature.scope", "legacy")))
					logs.L().Ctx(ctx).Debug("feature disabled in legacy table",
						zap.String("feature", feature),
						zap.String("chat_id", chatID),
					)
					return false
				}
			}
		}
	}

	// 6. 返回功能的默认值
	span.SetAttributes(attribute.Bool("feature.enabled", defaultEnabled))
	return defaultEnabled
}

// FeatureCheckFunc 适配 xhandler.FeatureCheckFunc 的检查函数
func (m *Manager) FeatureCheckFunc() xhandler.FeatureCheckFunc {
	return func(ctx context.Context, featureID string, defaultEnabled bool, chatID, userID string) bool {
		return m.IsFeatureEnabled(ctx, featureID, defaultEnabled, chatID, userID)
	}
}

// isFeatureBlockedAtScope 检查特定作用域是否屏蔽了功能
func (m *Manager) isFeatureBlockedAtScope(ctx context.Context, scope ConfigScope, chatID, userID, feature string) bool {
	key := buildFeatureBlockKey(scope, chatID, userID, feature)
	val, ok := m.getConfigByFullKey(ctx, key)
	if !ok {
		return false
	}
	blocked, _ := strconv.ParseBool(val)
	return blocked
}

// BlockFeature 屏蔽功能
func (m *Manager) BlockFeature(ctx context.Context, feature string, scope ConfigScope, chatID, userID, remark string) error {
	if !IsValidFeature(feature) {
		return fmt.Errorf("invalid feature: %s", feature)
	}

	key := buildFeatureBlockKey(scope, chatID, userID, feature)
	return m.setConfigByFullKey(ctx, key, "true")
}

// UnblockFeature 取消屏蔽功能
func (m *Manager) UnblockFeature(ctx context.Context, feature string, scope ConfigScope, chatID, userID string) error {
	if !IsValidFeature(feature) {
		return fmt.Errorf("invalid feature: %s", feature)
	}

	key := buildFeatureBlockKey(scope, chatID, userID, feature)
	return m.deleteConfigByFullKey(ctx, key)
}

// ListBlockedFeatures 列出被屏蔽的功能
func (m *Manager) ListBlockedFeatures(ctx context.Context, scope ConfigScope, chatID, userID string) ([]string, error) {
	blocked := make([]string, 0)
	for _, f := range m.GetAllFeatures() {
		if !m.IsFeatureEnabled(ctx, f.Name, f.DefaultEnabled, chatID, userID) {
			blocked = append(blocked, f.Name)
		}
	}
	return blocked, nil
}

// DisableFeature 屏蔽功能（兼容旧接口，仅支持 chat 级别）
func (m *Manager) DisableFeature(ctx context.Context, feature string, chatID string) error {
	return m.BlockFeature(ctx, feature, ScopeChat, chatID, "", "")
}

// EnableFeature 启用功能（兼容旧接口，仅支持 chat 级别）
func (m *Manager) EnableFeature(ctx context.Context, feature string, chatID string) error {
	return m.UnblockFeature(ctx, feature, ScopeChat, chatID, "")
}

// BlockFeatureGlobal 全局屏蔽功能
func (m *Manager) BlockFeatureGlobal(ctx context.Context, feature string, remark string) error {
	return m.BlockFeature(ctx, feature, ScopeGlobal, "", "", remark)
}

// UnblockFeatureGlobal 取消全局屏蔽功能
func (m *Manager) UnblockFeatureGlobal(ctx context.Context, feature string) error {
	return m.UnblockFeature(ctx, feature, ScopeGlobal, "", "")
}

// BlockFeatureChat 在指定聊天中屏蔽功能
func (m *Manager) BlockFeatureChat(ctx context.Context, feature, chatID string, remark string) error {
	return m.BlockFeature(ctx, feature, ScopeChat, chatID, "", remark)
}

// UnblockFeatureChat 取消在指定聊天中屏蔽功能
func (m *Manager) UnblockFeatureChat(ctx context.Context, feature, chatID string) error {
	return m.UnblockFeature(ctx, feature, ScopeChat, chatID, "")
}

// BlockFeatureUser 屏蔽指定用户的功能
func (m *Manager) BlockFeatureUser(ctx context.Context, feature, userID string, remark string) error {
	return m.BlockFeature(ctx, feature, ScopeUser, "", userID, remark)
}

// UnblockFeatureUser 取消屏蔽指定用户的功能
func (m *Manager) UnblockFeatureUser(ctx context.Context, feature, userID string) error {
	return m.UnblockFeature(ctx, feature, ScopeUser, "", userID)
}

// BlockFeatureChatUser 在指定聊天中屏蔽指定用户的功能
func (m *Manager) BlockFeatureChatUser(ctx context.Context, feature, chatID, userID string, remark string) error {
	return m.BlockFeature(ctx, feature, ScopeUser, chatID, userID, remark)
}

// UnblockFeatureChatUser 取消在指定聊天中屏蔽指定用户的功能
func (m *Manager) UnblockFeatureChatUser(ctx context.Context, feature, chatID, userID string) error {
	return m.UnblockFeature(ctx, feature, ScopeUser, chatID, userID)
}

// ClearCache 清除缓存
func (m *Manager) ClearCache() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache = make(map[string]string)
}

// GetAllFeatures 获取所有功能列表
func (m *Manager) GetAllFeatures() []Feature {
	if m.getFeaturesFunc != nil {
		return m.getFeaturesFunc()
	}
	return nil
}

// ==========================================
// 全局便捷函数（保留用于向后兼容）
// ==========================================

var (
	globalManager *Manager
	globalOnce    sync.Once
)

// GetManager 获取配置管理器单例（保留用于向后兼容）
func GetManager() *Manager {
	globalOnce.Do(func() {
		globalManager = NewManager()
	})
	return globalManager
}

// SetGetFeaturesFunc 设置获取功能列表的回调（保留用于向后兼容）
func SetGetFeaturesFunc(fn func() []Feature) {
	GetManager().SetGetFeaturesFunc(fn)
}

// GetAllConfigKeys 获取所有配置键列表
func GetAllConfigKeys() []ConfigKey {
	return []ConfigKey{
		KeyReactionDefaultRate,
		KeyReactionFollowDefaultRate,
		KeyRepeatDefaultRate,
		KeyImitateDefaultRate,
		KeyIntentFallbackRate,
		KeyIntentReplyThreshold,
		KeyIntentRecognitionEnabled,
	}
}

// GetConfigDescription 获取配置描述
func GetConfigDescription(key ConfigKey) string {
	switch key {
	case KeyReactionDefaultRate:
		return "默认回应表情概率 (0-100)"
	case KeyReactionFollowDefaultRate:
		return "跟随回应表情概率 (0-100)"
	case KeyRepeatDefaultRate:
		return "默认复读消息概率 (0-100)"
	case KeyImitateDefaultRate:
		return "默认模仿发言概率 (0-100)"
	case KeyIntentFallbackRate:
		return "意图识别失败回退概率 (0-100)"
	case KeyIntentReplyThreshold:
		return "意图识别回复阈值 (0-100)"
	case KeyIntentRecognitionEnabled:
		return "是否启用意图识别"
	default:
		return "未知配置"
	}
}

// GetAllFeatures 获取所有功能列表（保留用于向后兼容）
func GetAllFeatures() []Feature {
	return GetManager().GetAllFeatures()
}

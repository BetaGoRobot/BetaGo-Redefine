package config

import (
	"context"
	"strconv"
)

type configLookupCandidate struct {
	scope        ConfigScope
	chatID       string
	userID       string
	displayScope string
}

type ConfigCardViewOptions struct {
	BypassCache bool
}

// ==========================================
// 配置卡片数据结构
// ==========================================

// ConfigCardData 配置卡片数据
type ConfigCardData struct {
	Configs []ConfigItem `json:"configs"`
}

// ConfigItem 单个配置项
type ConfigItem struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Value       string `json:"value"`
	ValueType   string `json:"value_type"`  // "int" | "bool"
	Scope       string `json:"scope"`       // "global" | "chat" | "user" | "default"
	IsEditable  bool   `json:"is_editable"` // 是否可编辑
	ChatID      string `json:"chat_id,omitempty"`
	UserID      string `json:"user_id,omitempty"`
}

// ==========================================
// 功能卡片数据结构
// ==========================================

// FeatureCardData 功能卡片数据
type FeatureCardData struct {
	Features []FeatureItem `json:"features"`
}

// FeatureItem 单个功能项
type FeatureItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	IsEnabled   bool   `json:"is_enabled"`
	// 不同级别的屏蔽状态
	BlockedAtChat     bool `json:"blocked_at_chat"`
	BlockedAtUser     bool `json:"blocked_at_user"`
	BlockedAtChatUser bool `json:"blocked_at_chat_user"`
}

// ==========================================
// 功能操作请求/响应
// ==========================================

// FeatureAction 功能操作类型
type FeatureAction string

const (
	FeatureActionBlockChat       FeatureAction = "block_chat"
	FeatureActionUnblockChat     FeatureAction = "unblock_chat"
	FeatureActionBlockUser       FeatureAction = "block_user"
	FeatureActionUnblockUser     FeatureAction = "unblock_user"
	FeatureActionBlockChatUser   FeatureAction = "block_chat_user"
	FeatureActionUnblockChatUser FeatureAction = "unblock_chat_user"
)

// FeatureActionRequest 功能操作请求（从卡片回调中解析）
type FeatureActionRequest struct {
	Action  FeatureAction `json:"action"`
	Feature string        `json:"feature"`
	ChatID  string        `json:"chat_id"`
	UserID  string        `json:"user_id"`
}

// FeatureActionResponse 功能操作响应
type FeatureActionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ==========================================
// 配置操作请求/响应
// ==========================================

// ConfigAction 配置操作类型
type ConfigAction string

const (
	ConfigActionSet    ConfigAction = "set"
	ConfigActionDelete ConfigAction = "delete"
)

// ConfigActionRequest 配置操作请求
type ConfigActionRequest struct {
	Action      ConfigAction `json:"action"`
	Key         string       `json:"key"`
	Value       string       `json:"value"`
	Scope       string       `json:"scope"`
	ChatID      string       `json:"chat_id"`
	UserID      string       `json:"user_id"`
	ActorUserID string       `json:"actor_user_id"`
}

// ConfigActionResponse 配置操作响应
type ConfigActionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ==========================================
// 卡片数据生成函数
// ==========================================

// GetConfigCardData 获取配置卡片数据
func GetConfigCardData(ctx context.Context, viewScope, chatID, userID string) (*ConfigCardData, error) {
	return GetConfigCardDataWithOptions(ctx, viewScope, chatID, userID, ConfigCardViewOptions{})
}

func GetConfigCardDataWithOptions(ctx context.Context, viewScope, chatID, userID string, options ConfigCardViewOptions) (*ConfigCardData, error) {
	mgr := GetManager()
	allKeys := GetAllConfigKeys()
	viewScope = normalizeConfigScope(viewScope)

	items := make([]ConfigItem, 0, len(allKeys))
	for _, key := range allKeys {
		item := ConfigItem{
			Key:         string(key),
			Description: GetConfigDescription(key),
			ValueType:   configValueTypeForKey(key),
			IsEditable:  true,
			ChatID:      chatID,
			UserID:      userID,
		}
		item.Value, item.Scope = resolveConfigDisplayValue(
			viewScope,
			key,
			chatID,
			userID,
			func(candidate configLookupCandidate, key ConfigKey) (string, bool) {
				return mgr.getConfigWithOptions(ctx, candidate.scope, candidate.chatID, candidate.userID, key, ConfigReadOptions{
					BypassCache: options.BypassCache,
				})
			},
			func(key ConfigKey) string {
				return configDefaultDisplayValue(mgr, key)
			},
		)

		items = append(items, item)
	}

	return &ConfigCardData{Configs: items}, nil
}

func resolveConfigDisplayValue(
	viewScope string,
	key ConfigKey,
	chatID, userID string,
	lookup func(candidate configLookupCandidate, key ConfigKey) (string, bool),
	fallback func(key ConfigKey) string,
) (string, string) {
	for _, candidate := range configLookupChain(viewScope, chatID, userID) {
		if val, ok := lookup(candidate, key); ok {
			return val, candidate.displayScope
		}
	}
	return fallback(key), "default"
}

func configLookupChain(viewScope, chatID, userID string) []configLookupCandidate {
	candidates := make([]configLookupCandidate, 0, 4)

	switch normalizeConfigScope(viewScope) {
	case "global":
		candidates = append(candidates, configLookupCandidate{
			scope:        ScopeGlobal,
			displayScope: "global",
		})
	case "chat":
		if chatID != "" {
			candidates = append(candidates, configLookupCandidate{
				scope:        ScopeChat,
				chatID:       chatID,
				displayScope: "chat",
			})
		}
		candidates = append(candidates, configLookupCandidate{
			scope:        ScopeGlobal,
			displayScope: "global",
		})
	case "user":
		if chatID != "" && userID != "" {
			candidates = append(candidates, configLookupCandidate{
				scope:        ScopeUser,
				chatID:       chatID,
				userID:       userID,
				displayScope: "user",
			})
		}
		if userID != "" {
			candidates = append(candidates, configLookupCandidate{
				scope:        ScopeUser,
				userID:       userID,
				displayScope: "user",
			})
		}
		if chatID != "" {
			candidates = append(candidates, configLookupCandidate{
				scope:        ScopeChat,
				chatID:       chatID,
				displayScope: "chat",
			})
		}
		candidates = append(candidates, configLookupCandidate{
			scope:        ScopeGlobal,
			displayScope: "global",
		})
	default:
		return configLookupChain("chat", chatID, userID)
	}

	return candidates
}

func configValueTypeForKey(key ConfigKey) string {
	switch key {
	case KeyIntentRecognitionEnabled:
		return "bool"
	default:
		return "int"
	}
}

func configDefaultDisplayValue(mgr *Manager, key ConfigKey) string {
	switch configValueTypeForKey(key) {
	case "bool":
		return strconv.FormatBool(mgr.getBoolFromToml(key))
	default:
		return strconv.Itoa(mgr.getIntFromToml(key))
	}
}

// GetFeatureCardData 获取功能卡片数据
func GetFeatureCardData(ctx context.Context, chatID, userID string) (*FeatureCardData, error) {
	mgr := GetManager()
	allFeatures := GetAllFeatures()

	items := make([]FeatureItem, 0, len(allFeatures))
	for _, f := range allFeatures {
		item := FeatureItem{
			Name:        f.Name,
			Description: f.Description,
			Category:    f.Category,
			IsEnabled:   mgr.IsFeatureEnabled(ctx, f.Name, f.DefaultEnabled, chatID, userID),
		}

		// 检查各级别屏蔽状态
		item.BlockedAtChat = mgr.isFeatureBlockedAtScope(ctx, ScopeChat, chatID, "", f.Name)
		item.BlockedAtUser = mgr.isFeatureBlockedAtScope(ctx, ScopeUser, "", userID, f.Name)
		item.BlockedAtChatUser = mgr.isFeatureBlockedAtScope(ctx, ScopeUser, chatID, userID, f.Name)

		items = append(items, item)
	}

	return &FeatureCardData{Features: items}, nil
}

// ==========================================
// 操作处理函数
// ==========================================

// HandleFeatureAction 处理功能操作
func HandleFeatureAction(ctx context.Context, req *FeatureActionRequest) (*FeatureActionResponse, error) {
	mgr := GetManager()

	var err error
	var msg string

	switch req.Action {
	case FeatureActionBlockChat:
		err = mgr.BlockFeature(ctx, req.Feature, ScopeChat, req.ChatID, "", "")
		msg = "已在当前群屏蔽该功能"
	case FeatureActionUnblockChat:
		err = mgr.UnblockFeature(ctx, req.Feature, ScopeChat, req.ChatID, "")
		msg = "已在当前群取消屏蔽该功能"
	case FeatureActionBlockUser:
		err = mgr.BlockFeature(ctx, req.Feature, ScopeUser, "", req.UserID, "")
		msg = "已对该用户屏蔽该功能"
	case FeatureActionUnblockUser:
		err = mgr.UnblockFeature(ctx, req.Feature, ScopeUser, "", req.UserID)
		msg = "已对该用户取消屏蔽该功能"
	case FeatureActionBlockChatUser:
		err = mgr.BlockFeature(ctx, req.Feature, ScopeUser, req.ChatID, req.UserID, "")
		msg = "已在当前群对该用户屏蔽该功能"
	case FeatureActionUnblockChatUser:
		err = mgr.UnblockFeature(ctx, req.Feature, ScopeUser, req.ChatID, req.UserID)
		msg = "已在当前群对该用户取消屏蔽该功能"
	default:
		return &FeatureActionResponse{
			Success: false,
			Message: "未知操作: " + string(req.Action),
		}, nil
	}

	if err != nil {
		return &FeatureActionResponse{
			Success: false,
			Message: "操作失败: " + err.Error(),
		}, err
	}

	return &FeatureActionResponse{
		Success: true,
		Message: msg,
	}, nil
}

// HandleConfigAction 处理配置操作
func HandleConfigAction(ctx context.Context, req *ConfigActionRequest) (*ConfigActionResponse, error) {
	mgr := GetManager()

	if req.Action != ConfigActionSet && req.Action != ConfigActionDelete {
		return &ConfigActionResponse{
			Success: false,
			Message: "未知操作: " + string(req.Action),
		}, nil
	}

	// 验证配置键
	validKey := false
	allKeys := GetAllConfigKeys()
	for _, k := range allKeys {
		if string(k) == req.Key {
			validKey = true
			break
		}
	}
	if !validKey {
		return &ConfigActionResponse{
			Success: false,
			Message: "无效的配置键: " + req.Key,
		}, nil
	}

	// 解析 scope
	var scope ConfigScope
	chatID := req.ChatID
	userID := req.UserID
	switch req.Scope {
	case "global":
		if err := ensureGlobalConfigMutationAllowed(ctx, req.ActorUserID, req.UserID); err != nil {
			return &ConfigActionResponse{
				Success: false,
				Message: err.Error(),
			}, err
		}
		scope = ScopeGlobal
		chatID = ""
		userID = ""
	case "chat":
		scope = ScopeChat
		userID = ""
	case "user":
		scope = ScopeUser
	default:
		return &ConfigActionResponse{
			Success: false,
			Message: "无效的作用域: " + req.Scope,
		}, nil
	}

	configKey := ConfigKey(req.Key)
	var err error

	if req.Action == ConfigActionDelete {
		err = mgr.DeleteConfig(ctx, configKey, scope, chatID, userID)
		if err != nil {
			return &ConfigActionResponse{
				Success: false,
				Message: "删除失败: " + err.Error(),
			}, err
		}
		return &ConfigActionResponse{
			Success: true,
			Message: "已恢复默认值",
		}, nil
	}

	switch configKey {
	case KeyIntentRecognitionEnabled:
		boolVal, boolErr := strconv.ParseBool(req.Value)
		if boolErr != nil {
			return &ConfigActionResponse{
				Success: false,
				Message: "值必须是 true/false",
			}, nil
		}
		err = mgr.SetBool(ctx, configKey, scope, chatID, userID, boolVal)
	default:
		intVal, intErr := strconv.Atoi(req.Value)
		if intErr != nil {
			return &ConfigActionResponse{
				Success: false,
				Message: "值必须是整数",
			}, nil
		}
		if intVal < 0 || intVal > 100 {
			return &ConfigActionResponse{
				Success: false,
				Message: "值必须在 0-100 之间",
			}, nil
		}
		err = mgr.SetInt(ctx, configKey, scope, chatID, userID, intVal)
	}

	if err != nil {
		return &ConfigActionResponse{
			Success: false,
			Message: "设置失败: " + err.Error(),
		}, err
	}

	return &ConfigActionResponse{
		Success: true,
		Message: "配置已更新",
	}, nil
}

package config

import (
	"context"
	"strconv"
)

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
	FeatureActionBlockChat     FeatureAction = "block_chat"
	FeatureActionUnblockChat   FeatureAction = "unblock_chat"
	FeatureActionBlockUser     FeatureAction = "block_user"
	FeatureActionUnblockUser   FeatureAction = "unblock_user"
	FeatureActionBlockChatUser FeatureAction = "block_chat_user"
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
	ConfigActionSet ConfigAction = "set"
)

// ConfigActionRequest 配置操作请求
type ConfigActionRequest struct {
	Action ConfigAction `json:"action"`
	Key    string       `json:"key"`
	Value  string       `json:"value"`
	Scope  string       `json:"scope"`
	ChatID string       `json:"chat_id"`
	UserID string       `json:"user_id"`
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
func GetConfigCardData(ctx context.Context, chatID, userID string) (*ConfigCardData, error) {
	mgr := GetManager()
	allKeys := GetAllConfigKeys()

	items := make([]ConfigItem, 0, len(allKeys))
	for _, key := range allKeys {
		item := ConfigItem{
			Key:         string(key),
			Description: GetConfigDescription(key),
			IsEditable:  true,
			ChatID:      chatID,
			UserID:      userID,
		}

		// 检查各优先级的配置
		found := false
		scopes := []struct {
			scope ConfigScope
			cid   string
			uid   string
			name  string
		}{
			{ScopeUser, chatID, userID, "user"},
			{ScopeUser, "", userID, "user"},
			{ScopeChat, chatID, "", "chat"},
			{ScopeGlobal, "", "", "global"},
		}

		for _, s := range scopes {
			if val, ok := mgr.getConfig(ctx, s.scope, s.cid, s.uid, key); ok {
				item.Value = val
				item.Scope = s.name
				found = true
				break
			}
		}

		// 使用默认值
		if !found {
			switch key {
			case KeyIntentRecognitionEnabled:
				item.Value = strconv.FormatBool(mgr.GetBool(ctx, key, chatID, userID))
				item.ValueType = "bool"
			default:
				item.Value = strconv.Itoa(mgr.GetInt(ctx, key, chatID, userID))
				item.ValueType = "int"
			}
			item.Scope = "default"
		} else {
			// 设置值类型
			switch key {
			case KeyIntentRecognitionEnabled:
				item.ValueType = "bool"
			default:
				item.ValueType = "int"
			}
		}

		items = append(items, item)
	}

	return &ConfigCardData{Configs: items}, nil
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

	if req.Action != ConfigActionSet {
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
	switch req.Scope {
	case "global":
		scope = ScopeGlobal
	case "chat":
		scope = ScopeChat
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

	switch configKey {
	case KeyIntentRecognitionEnabled:
		boolVal, boolErr := strconv.ParseBool(req.Value)
		if boolErr != nil {
			return &ConfigActionResponse{
				Success: false,
				Message: "值必须是 true/false",
			}, nil
		}
		err = mgr.SetBool(ctx, configKey, scope, req.ChatID, req.UserID, boolVal)
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
		err = mgr.SetInt(ctx, configKey, scope, req.ChatID, req.UserID, intVal)
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

// ==========================================
// 便捷的卡片回调 value 生成
// ==========================================

// BuildFeatureActionValue 构建功能操作的 value
func BuildFeatureActionValue(action FeatureAction, feature, chatID, userID string) map[string]any {
	return map[string]any{
		"type":    "feature_action",
		"action":  string(action),
		"feature": feature,
		"chat_id": chatID,
		"user_id": userID,
	}
}

// BuildConfigActionValue 构建配置操作的 value
func BuildConfigActionValue(action ConfigAction, key, value, scope, chatID, userID string) map[string]any {
	return map[string]any{
		"type":    "config_action",
		"action":  string(action),
		"key":     key,
		"value":   value,
		"scope":   scope,
		"chat_id": chatID,
		"user_id": userID,
	}
}

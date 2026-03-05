package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	"github.com/BetaGoRobot/go_utils/reflecting"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

// ==========================================
// 配置管理命令
// ==========================================

// ConfigListHandler 列出配置
// 使用方式: /config list [scope]
func ConfigListHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	argMap, _ := parseArgs(args...)
	scopeStr := argMap["scope"]
	if scopeStr == "" {
		scopeStr = "chat"
	}

	var scope config.ConfigScope
	chatID := *data.Event.Message.ChatId
	userID := *data.Event.Sender.SenderId.OpenId

	switch scopeStr {
	case "global":
		scope = config.ScopeGlobal
	case "chat":
		scope = config.ScopeChat
	case "user":
		scope = config.ScopeUser
	default:
		return errors.New("invalid scope, use: global, chat, user")
	}

	entries, err := config.GetManager().ListConfigs(ctx, scope, chatID, userID)
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to list configs", zap.Error(err))
		return err
	}

	allKeys := config.GetAllConfigKeys()
	lines := make([]map[string]string, 0)

	for _, entry := range entries {
		lines = append(lines, map[string]string{
			"title1": string(entry.Scope),
			"title2": string(entry.Key),
			"title3": entry.Value,
			"title4": config.GetConfigDescription(entry.Key),
		})
	}

	configuredKeys := make(map[string]bool)
	for _, entry := range entries {
		configuredKeys[string(entry.Key)] = true
	}

	mgr := config.GetManager()
	for _, key := range allKeys {
		if !configuredKeys[string(key)] {
			var defaultValue string
			switch key {
			case config.KeyIntentRecognitionEnabled:
				defaultValue = strconv.FormatBool(mgr.GetBool(ctx, key, chatID, userID))
			default:
				defaultValue = strconv.Itoa(mgr.GetInt(ctx, key, chatID, userID))
			}
			lines = append(lines, map[string]string{
				"title1": "default",
				"title2": string(key),
				"title3": defaultValue,
				"title4": config.GetConfigDescription(key),
			})
		}
	}

	cardContent := larktpl.NewCardContent(
		ctx,
		larktpl.FourColSheetTemplate,
	).
		AddVariable("title1", "Scope").
		AddVariable("title2", "Key").
		AddVariable("title3", "Value").
		AddVariable("title4", "Description").
		AddVariable("table_raw_array_1", lines)

	err = larkmsg.ReplyCard(ctx, cardContent, *data.Event.Message.MessageId, "_configList", false)
	return err
}

// ConfigSetHandler 设置配置
// 使用方式: /config set key=xxx value=xxx [scope=global/chat/user]
func ConfigSetHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	argMap, _ := parseArgs(args...)
	key := argMap["key"]
	value := argMap["value"]
	scopeStr := argMap["scope"]

	if key == "" || value == "" {
		return errors.New("usage: /config set key=xxx value=xxx [scope=global/chat/user]")
	}

	if scopeStr == "" {
		scopeStr = "chat"
	}

	var scope config.ConfigScope
	chatID := *data.Event.Message.ChatId
	userID := *data.Event.Sender.SenderId.OpenId

	switch scopeStr {
	case "global":
		scope = config.ScopeGlobal
		chatID = ""
		userID = ""
	case "chat":
		scope = config.ScopeChat
		userID = ""
	case "user":
		scope = config.ScopeUser
	default:
		return errors.New("invalid scope, use: global, chat, user")
	}

	validKey := false
	allKeys := config.GetAllConfigKeys()
	for _, k := range allKeys {
		if string(k) == key {
			validKey = true
			break
		}
	}
	if !validKey {
		return fmt.Errorf("invalid config key: %s, available keys: %v", key, allKeys)
	}

	configKey := config.ConfigKey(key)
	mgr := config.GetManager()
	switch configKey {
	case config.KeyIntentRecognitionEnabled:
		boolVal, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("value must be true/false")
		}
		err = mgr.SetBool(ctx, configKey, scope, chatID, userID, boolVal)
	default:
		intVal, err := strconv.Atoi(value)
		if err != nil {
			return errors.New("value must be integer")
		}
		if intVal < 0 || intVal > 100 {
			return errors.New("value must be between 0 and 100")
		}
		err = mgr.SetInt(ctx, configKey, scope, chatID, userID, intVal)
	}

	if err != nil {
		logs.L().Ctx(ctx).Error("failed to set config", zap.Error(err))
		return err
	}

	msg := fmt.Sprintf("✅ 配置已设置\n\nKey: %s\nValue: %s\nScope: %s", key, value, scopeStr)
	err = larkmsg.ReplyCardText(ctx, msg, *data.Event.Message.MessageId, "_configSet", false)
	return err
}

// ConfigDeleteHandler 删除配置
// 使用方式: /config delete key=xxx [scope=global/chat/user]
func ConfigDeleteHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	argMap, _ := parseArgs(args...)
	key := argMap["key"]
	scopeStr := argMap["scope"]

	if key == "" {
		return errors.New("usage: /config delete key=xxx [scope=global/chat/user]")
	}

	if scopeStr == "" {
		scopeStr = "chat"
	}

	var scope config.ConfigScope
	chatID := *data.Event.Message.ChatId
	userID := *data.Event.Sender.SenderId.OpenId

	switch scopeStr {
	case "global":
		scope = config.ScopeGlobal
		chatID = ""
		userID = ""
	case "chat":
		scope = config.ScopeChat
		userID = ""
	case "user":
		scope = config.ScopeUser
	default:
		return errors.New("invalid scope, use: global, chat, user")
	}

	err = config.GetManager().DeleteConfig(ctx, config.ConfigKey(key), scope, chatID, userID)
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to delete config", zap.Error(err))
		return err
	}

	msg := fmt.Sprintf("✅ 配置已删除\n\nKey: %s\nScope: %s", key, scopeStr)
	err = larkmsg.ReplyCardText(ctx, msg, *data.Event.Message.MessageId, "_configDelete", false)
	return err
}

// ==========================================
// 功能开关命令
// ==========================================

// FeatureListHandler 列出功能
// 使用方式: /feature list [scope]
func FeatureListHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	chatID := *data.Event.Message.ChatId
	userID := *data.Event.Sender.SenderId.OpenId
	mgr := config.GetManager()

	allFeatures := config.GetAllFeatures()
	lines := make([]map[string]string, 0, len(allFeatures))

	for _, f := range allFeatures {
		status := "✅ Enabled"
		if !mgr.IsFeatureEnabled(ctx, f.Name, chatID, userID) {
			status = "❌ Blocked"
		}
		lines = append(lines, map[string]string{
			"title1": f.Name,
			"title2": f.Description,
			"title3": f.Category,
			"title4": status,
		})
	}

	cardContent := larktpl.NewCardContent(
		ctx,
		larktpl.FourColSheetTemplate,
	).
		AddVariable("title1", "Name").
		AddVariable("title2", "Description").
		AddVariable("title3", "Category").
		AddVariable("title4", "Status").
		AddVariable("table_raw_array_1", lines)

	err = larkmsg.ReplyCard(ctx, cardContent, *data.Event.Message.MessageId, "_featureList", false)
	return err
}

// FeatureBlockHandler 屏蔽功能
// 使用方式: /feature block feature=xxx [scope=chat/user/chat_user]
func FeatureBlockHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	argMap, _ := parseArgs(args...)
	feature := argMap["feature"]
	scopeStr := argMap["scope"]

	if feature == "" {
		return errors.New("usage: /feature block feature=xxx [scope=chat/user/chat_user]")
	}

	if scopeStr == "" {
		scopeStr = "chat"
	}

	var scope config.ConfigScope
	chatID := *data.Event.Message.ChatId
	userID := *data.Event.Sender.SenderId.OpenId

	switch scopeStr {
	case "chat":
		scope = config.ScopeChat
		userID = ""
	case "user":
		scope = config.ScopeUser
		chatID = ""
	case "chat_user":
		scope = config.ScopeUser
	default:
		return errors.New("invalid scope, use: chat, user, chat_user")
	}

	mgr := config.GetManager()
	err = mgr.BlockFeature(ctx, feature, scope, chatID, userID, "")
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to block feature", zap.Error(err))
		return err
	}

	msg := fmt.Sprintf("✅ 功能已屏蔽\n\nFeature: %s\nScope: %s", feature, scopeStr)
	err = larkmsg.ReplyCardText(ctx, msg, *data.Event.Message.MessageId, "_featureBlock", false)
	return err
}

// FeatureUnblockHandler 取消屏蔽功能
// 使用方式: /feature unblock feature=xxx [scope=chat/user/chat_user]
func FeatureUnblockHandler(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	argMap, _ := parseArgs(args...)
	feature := argMap["feature"]
	scopeStr := argMap["scope"]

	if feature == "" {
		return errors.New("usage: /feature unblock feature=xxx [scope=chat/user/chat_user]")
	}

	if scopeStr == "" {
		scopeStr = "chat"
	}

	var scope config.ConfigScope
	chatID := *data.Event.Message.ChatId
	userID := *data.Event.Sender.SenderId.OpenId

	switch scopeStr {
	case "chat":
		scope = config.ScopeChat
		userID = ""
	case "user":
		scope = config.ScopeUser
		chatID = ""
	case "chat_user":
		scope = config.ScopeUser
	default:
		return errors.New("invalid scope, use: chat, user, chat_user")
	}

	mgr := config.GetManager()
	err = mgr.UnblockFeature(ctx, feature, scope, chatID, userID)
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to unblock feature", zap.Error(err))
		return err
	}

	msg := fmt.Sprintf("✅ 功能已取消屏蔽\n\nFeature: %s\nScope: %s", feature, scopeStr)
	err = larkmsg.ReplyCardText(ctx, msg, *data.Event.Message.MessageId, "_featureUnblock", false)
	return err
}

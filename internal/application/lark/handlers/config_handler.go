package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
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

type ConfigSetArgs struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Scope string `json:"scope"`
}

type ConfigListArgs struct {
	Scope string `json:"scope"`
}

type ConfigDeleteArgs struct {
	Key   string `json:"key"`
	Scope string `json:"scope"`
}

type FeatureListArgs struct{}

type FeatureBlockArgs struct {
	Feature string `json:"feature"`
	Scope   string `json:"scope"`
}

type FeatureUnblockArgs struct {
	Feature string `json:"feature"`
	Scope   string `json:"scope"`
}

type configSetHandler struct{}
type configListHandler struct{}
type configDeleteHandler struct{}
type featureListHandler struct{}
type featureBlockHandler struct{}
type featureUnblockHandler struct{}

var ConfigSet configSetHandler
var ConfigList configListHandler
var ConfigDelete configDeleteHandler
var FeatureList featureListHandler
var FeatureBlock featureBlockHandler
var FeatureUnblock featureUnblockHandler

func (configSetHandler) ParseCLI(args []string) (ConfigSetArgs, error) {
	argMap, _ := parseArgs(args...)
	parsed := ConfigSetArgs{
		Key:   argMap["key"],
		Value: argMap["value"],
		Scope: argMap["scope"],
	}
	if parsed.Key == "" || parsed.Value == "" {
		return ConfigSetArgs{}, errors.New("usage: /config set key=xxx value=xxx [scope=global/chat/user]")
	}
	if parsed.Scope == "" {
		parsed.Scope = "chat"
	}
	return parsed, nil
}

func (configSetHandler) ParseTool(raw string) (ConfigSetArgs, error) {
	parsed := ConfigSetArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ConfigSetArgs{}, err
	}
	if parsed.Key == "" || parsed.Value == "" {
		return ConfigSetArgs{}, errors.New("usage: /config set key=xxx value=xxx [scope=global/chat/user]")
	}
	if parsed.Scope == "" {
		parsed.Scope = "chat"
	}
	return parsed, nil
}

func (configSetHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "config_set",
		Desc: "设置机器人配置项",
		Params: arktools.NewParams("object").
			AddProp("key", &arktools.Prop{
				Type: "string",
				Desc: "配置键名",
			}).
			AddProp("value", &arktools.Prop{
				Type: "string",
				Desc: "配置值。布尔配置传 true/false，数值配置传整数字符串",
			}).
			AddProp("scope", &arktools.Prop{
				Type: "string",
				Desc: "配置范围，可选值：chat、user、global。默认 chat",
			}).
			AddRequired("key").
			AddRequired("value"),
	}
}

func (configListHandler) ParseCLI(args []string) (ConfigListArgs, error) {
	argMap, _ := parseArgs(args...)
	scope := argMap["scope"]
	if scope == "" {
		scope = "chat"
	}
	return ConfigListArgs{Scope: scope}, nil
}

func (configListHandler) ParseTool(raw string) (ConfigListArgs, error) {
	parsed := ConfigListArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ConfigListArgs{}, err
	}
	if parsed.Scope == "" {
		parsed.Scope = "chat"
	}
	return parsed, nil
}

func (configListHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "config_list",
		Desc: "列出当前上下文可见的配置项",
		Params: arktools.NewParams("object").
			AddProp("scope", &arktools.Prop{
				Type: "string",
				Desc: "配置范围，可选值：chat、user、global。默认 chat",
			}),
	}
}

func (configListHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ConfigListArgs) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	scopeStr := arg.Scope

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

func (configSetHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ConfigSetArgs) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	key := arg.Key
	value := arg.Value
	scopeStr := arg.Scope

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

func (configDeleteHandler) ParseCLI(args []string) (ConfigDeleteArgs, error) {
	argMap, _ := parseArgs(args...)
	parsed := ConfigDeleteArgs{
		Key:   argMap["key"],
		Scope: argMap["scope"],
	}
	if parsed.Key == "" {
		return ConfigDeleteArgs{}, errors.New("usage: /config delete key=xxx [scope=global/chat/user]")
	}
	if parsed.Scope == "" {
		parsed.Scope = "chat"
	}
	return parsed, nil
}

func (configDeleteHandler) ParseTool(raw string) (ConfigDeleteArgs, error) {
	parsed := ConfigDeleteArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return ConfigDeleteArgs{}, err
	}
	if parsed.Key == "" {
		return ConfigDeleteArgs{}, errors.New("usage: /config delete key=xxx [scope=global/chat/user]")
	}
	if parsed.Scope == "" {
		parsed.Scope = "chat"
	}
	return parsed, nil
}

func (configDeleteHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "config_delete",
		Desc: "删除机器人配置项",
		Params: arktools.NewParams("object").
			AddProp("key", &arktools.Prop{
				Type: "string",
				Desc: "配置键名",
			}).
			AddProp("scope", &arktools.Prop{
				Type: "string",
				Desc: "配置范围，可选值：chat、user、global。默认 chat",
			}).
			AddRequired("key"),
	}
}

func (configDeleteHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ConfigDeleteArgs) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	key := arg.Key
	scopeStr := arg.Scope

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

func (featureListHandler) ParseCLI(args []string) (FeatureListArgs, error) {
	return FeatureListArgs{}, nil
}

func (featureListHandler) ParseTool(raw string) (FeatureListArgs, error) {
	if err := parseEmptyToolArgs(raw); err != nil {
		return FeatureListArgs{}, err
	}
	return FeatureListArgs{}, nil
}

func (featureListHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name:   "feature_list",
		Desc:   "列出当前群聊的功能开关状态",
		Params: arktools.NewParams("object"),
	}
}

func (featureListHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg FeatureListArgs) (err error) {
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
		if !mgr.IsFeatureEnabled(ctx, f.Name, f.DefaultEnabled, chatID, userID) {
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

func (featureBlockHandler) ParseCLI(args []string) (FeatureBlockArgs, error) {
	argMap, _ := parseArgs(args...)
	parsed := FeatureBlockArgs{
		Feature: argMap["feature"],
		Scope:   argMap["scope"],
	}
	if parsed.Feature == "" {
		return FeatureBlockArgs{}, errors.New("usage: /feature block feature=xxx [scope=chat/user/chat_user]")
	}
	if parsed.Scope == "" {
		parsed.Scope = "chat"
	}
	return parsed, nil
}

func (featureBlockHandler) ParseTool(raw string) (FeatureBlockArgs, error) {
	parsed := FeatureBlockArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return FeatureBlockArgs{}, err
	}
	if parsed.Feature == "" {
		return FeatureBlockArgs{}, errors.New("usage: /feature block feature=xxx [scope=chat/user/chat_user]")
	}
	if parsed.Scope == "" {
		parsed.Scope = "chat"
	}
	return parsed, nil
}

func (featureBlockHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "feature_block",
		Desc: "屏蔽指定机器人功能",
		Params: arktools.NewParams("object").
			AddProp("feature", &arktools.Prop{
				Type: "string",
				Desc: "功能名称",
			}).
			AddProp("scope", &arktools.Prop{
				Type: "string",
				Desc: "生效范围，可选值：chat、user、chat_user。默认 chat",
			}).
			AddRequired("feature"),
	}
}

func (featureBlockHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg FeatureBlockArgs) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	feature := arg.Feature
	scopeStr := arg.Scope

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

func (featureUnblockHandler) ParseCLI(args []string) (FeatureUnblockArgs, error) {
	argMap, _ := parseArgs(args...)
	parsed := FeatureUnblockArgs{
		Feature: argMap["feature"],
		Scope:   argMap["scope"],
	}
	if parsed.Feature == "" {
		return FeatureUnblockArgs{}, errors.New("usage: /feature unblock feature=xxx [scope=chat/user/chat_user]")
	}
	if parsed.Scope == "" {
		parsed.Scope = "chat"
	}
	return parsed, nil
}

func (featureUnblockHandler) ParseTool(raw string) (FeatureUnblockArgs, error) {
	parsed := FeatureUnblockArgs{}
	if err := utils.UnmarshalStringPre(raw, &parsed); err != nil {
		return FeatureUnblockArgs{}, err
	}
	if parsed.Feature == "" {
		return FeatureUnblockArgs{}, errors.New("usage: /feature unblock feature=xxx [scope=chat/user/chat_user]")
	}
	if parsed.Scope == "" {
		parsed.Scope = "chat"
	}
	return parsed, nil
}

func (featureUnblockHandler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: "feature_unblock",
		Desc: "取消屏蔽指定机器人功能",
		Params: arktools.NewParams("object").
			AddProp("feature", &arktools.Prop{
				Type: "string",
				Desc: "功能名称",
			}).
			AddProp("scope", &arktools.Prop{
				Type: "string",
				Desc: "生效范围，可选值：chat、user、chat_user。默认 chat",
			}).
			AddRequired("feature"),
	}
}

func (featureUnblockHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg FeatureUnblockArgs) (err error) {
	ctx, span := otel.T().Start(ctx, reflecting.GetCurrentFunc())
	span.SetAttributes(attribute.Key("event").String(larkcore.Prettify(data)))
	defer span.End()
	defer func() { span.RecordError(err) }()

	feature := arg.Feature
	scopeStr := arg.Scope

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

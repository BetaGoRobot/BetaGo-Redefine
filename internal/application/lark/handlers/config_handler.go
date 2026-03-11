package handlers

import (
	"context"
	"errors"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
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
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	chatID := currentChatID(data, metaData)
	openID := currentOpenID(data, metaData)
	cardData, err := config.BuildConfigCardJSONWithOptions(ctx, arg.Scope, chatID, openID, config.ConfigCardViewOptions{
		BypassCache:        true,
		LastModifierOpenID: openID,
	})
	if err != nil {
		return err
	}
	return sendCompatibleCardJSON(ctx, data, metaData, cardData, "_configList", false)
}

func (configSetHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg ConfigSetArgs) (err error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req := &config.ConfigActionRequest{
		Action:      config.ConfigActionSet,
		Key:         arg.Key,
		Value:       arg.Value,
		Scope:       arg.Scope,
		ChatID:      currentChatID(data, metaData),
		OpenID:      currentOpenID(data, metaData),
		ActorOpenID: currentOpenID(data, metaData),
	}
	resp, err := config.HandleConfigAction(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to set config", zap.Error(err), zap.String("message", resp.Message))
		return err
	}

	msg := fmt.Sprintf("✅ %s\n\nKey: %s\nValue: %s\nScope: %s", resp.Message, req.Key, req.Value, req.Scope)
	return sendCompatibleText(ctx, data, metaData, msg, "_configSet", false)
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
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req := &config.ConfigActionRequest{
		Action:      config.ConfigActionDelete,
		Key:         arg.Key,
		Scope:       arg.Scope,
		ChatID:      currentChatID(data, metaData),
		OpenID:      currentOpenID(data, metaData),
		ActorOpenID: currentOpenID(data, metaData),
	}
	resp, err := config.HandleConfigAction(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to delete config", zap.Error(err), zap.String("message", resp.Message))
		return err
	}

	msg := fmt.Sprintf("✅ %s\n\nKey: %s\nScope: %s", resp.Message, req.Key, req.Scope)
	return sendCompatibleText(ctx, data, metaData, msg, "_configDelete", false)
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
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	chatID := currentChatID(data, metaData)
	openID := currentOpenID(data, metaData)
	rawCard, err := config.BuildFeatureCardWithOptions(ctx, chatID, openID, config.FeatureCardViewOptions{
		LastModifierOpenID: openID,
	})
	if err != nil {
		return err
	}
	content, err := rawCard.JSON()
	if err != nil {
		return err
	}
	return sendCompatibleRawCard(ctx, data, metaData, content, "_featureList", false)
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
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req, reqErr := buildFeatureActionRequest(arg.Scope, arg.Feature, currentChatID(data, metaData), currentOpenID(data, metaData), true)
	if reqErr != nil {
		return reqErr
	}
	resp, err := config.HandleFeatureAction(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to block feature", zap.Error(err), zap.String("message", resp.Message))
		return err
	}

	msg := fmt.Sprintf("✅ %s\n\nFeature: %s\nScope: %s", resp.Message, req.Feature, arg.Scope)
	return sendCompatibleText(ctx, data, metaData, msg, "_featureBlock", false)
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
	ctx, span := otel.Start(ctx)
	span.SetAttributes(otel.PreviewAttrs("event", larkcore.Prettify(data), 256)...)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req, reqErr := buildFeatureActionRequest(arg.Scope, arg.Feature, currentChatID(data, metaData), currentOpenID(data, metaData), false)
	if reqErr != nil {
		return reqErr
	}
	resp, err := config.HandleFeatureAction(ctx, req)
	if err != nil {
		logs.L().Ctx(ctx).Error("failed to unblock feature", zap.Error(err), zap.String("message", resp.Message))
		return err
	}

	msg := fmt.Sprintf("✅ %s\n\nFeature: %s\nScope: %s", resp.Message, req.Feature, arg.Scope)
	return sendCompatibleText(ctx, data, metaData, msg, "_featureUnblock", false)
}

func buildFeatureActionRequest(scopeStr, feature, chatID, openID string, block bool) (*config.FeatureActionRequest, error) {
	req := &config.FeatureActionRequest{
		Feature: feature,
		ChatID:  chatID,
		OpenID:  openID,
	}

	switch scopeStr {
	case "chat":
		req.OpenID = ""
		if block {
			req.Action = config.FeatureActionBlockChat
		} else {
			req.Action = config.FeatureActionUnblockChat
		}
	case "user":
		req.ChatID = ""
		if block {
			req.Action = config.FeatureActionBlockUser
		} else {
			req.Action = config.FeatureActionUnblockUser
		}
	case "chat_user":
		if block {
			req.Action = config.FeatureActionBlockChatUser
		} else {
			req.Action = config.FeatureActionUnblockChatUser
		}
	default:
		return nil, errors.New("invalid scope, use: chat, user, chat_user")
	}

	return req, nil
}

func (configSetHandler) CommandDescription() string {
	return "设置配置项"
}

func (configListHandler) CommandDescription() string {
	return "查看配置项"
}

func (configDeleteHandler) CommandDescription() string {
	return "删除配置项"
}

func (featureListHandler) CommandDescription() string {
	return "查看功能开关"
}

func (featureBlockHandler) CommandDescription() string {
	return "屏蔽功能"
}

func (featureUnblockHandler) CommandDescription() string {
	return "取消屏蔽功能"
}

func (configSetHandler) CommandExamples() []string {
	return []string{
		"/config set --key=intent_recognition_enabled --value=true",
		"/config set --key=intent_recognition_enabled --value=false --scope=global",
	}
}

func (configListHandler) CommandExamples() []string {
	return []string{
		"/config list",
		"/config list --scope=user",
	}
}

func (configDeleteHandler) CommandExamples() []string {
	return []string{
		"/config delete --key=intent_recognition_enabled",
		"/config delete --key=intent_recognition_enabled --scope=global",
	}
}

func (featureListHandler) CommandExamples() []string {
	return []string{
		"/feature list",
	}
}

func (featureBlockHandler) CommandExamples() []string {
	return []string{
		"/feature block --feature=chat",
		"/feature block --feature=chat --scope=user",
	}
}

func (featureUnblockHandler) CommandExamples() []string {
	return []string{
		"/feature unblock --feature=chat",
		"/feature unblock --feature=chat --scope=user",
	}
}

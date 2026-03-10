package config

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
)

func BuildConfigCard(ctx context.Context, scope, chatID, userID string) (larkmsg.RawCard, error) {
	return BuildConfigCardWithOptions(ctx, scope, chatID, userID, ConfigCardViewOptions{})
}

func BuildConfigCardWithOptions(ctx context.Context, scope, chatID, userID string, options ConfigCardViewOptions) (larkmsg.RawCard, error) {
	scope = normalizeConfigScope(scope)
	cardData, err := GetConfigCardDataWithOptions(ctx, scope, chatID, userID, options)
	if err != nil {
		return nil, err
	}

	elements := make([]any, 0, len(cardData.Configs)*4+3)
	elements = append(elements, markdownBlock(fmt.Sprintf("当前查看/写入作用域: `%s`  当前上下文: chat=`%s` user=`%s`", scope, shortID(chatID), shortID(userID))))
	elements = append(elements, buildConfigScopeRow(scope, chatID, userID))

	for idx, item := range cardData.Configs {
		elements = append(elements, buildConfigItemSection(item, scope))
		if idx < len(cardData.Configs)-1 {
			elements = append(elements, divider())
		}
	}

	elements = append(elements, hintBlock(fmt.Sprintf("Chat=%s User=%s", shortID(chatID), shortID(userID))))
	return newRawCard("配置面板", elements), nil
}

func BuildFeatureCard(ctx context.Context, chatID, userID string) (larkmsg.RawCard, error) {
	cardData, err := GetFeatureCardData(ctx, chatID, userID)
	if err != nil {
		return nil, err
	}

	elements := make([]any, 0, len(cardData.Features)*3+2)
	elements = append(elements, markdownBlock(fmt.Sprintf("当前上下文: chat=`%s` user=`%s`", shortID(chatID), shortID(userID))))

	for idx, item := range cardData.Features {
		elements = append(elements, buildFeatureItemBlock(item))
		elements = append(elements, buildFeatureActionRow(item, chatID, userID))
		if idx < len(cardData.Features)-1 {
			elements = append(elements, divider())
		}
	}

	elements = append(elements, hintBlock("点击按钮直接调整屏蔽状态"))
	return newRawCard("功能开关", elements), nil
}

func buildConfigItemBlock(item ConfigItem) map[string]any {
	content := fmt.Sprintf("**%s**  `%s`\n%s\n当前值: `**%s**`  来源: `**%s**`",
		item.Key,
		item.ValueType,
		item.Description,
		item.Value,
		item.Scope,
	)
	return markdownBlock(content)
}

func buildConfigItemSection(item ConfigItem, scope string) map[string]any {
	if item.ValueType == "int" {
		return buildConfigCustomValueForm(item, scope)
	} else {
		return buildConfigColumns(buildConfigItemBlock(item), []any{buildConfigActionRow(item, scope)})
	}
}

func buildConfigActionRow(item ConfigItem, scope string) map[string]any {
	buttons := make([]map[string]any, 0, 2)
	if item.ValueType == "bool" {
		buttons = append(buttons,
			buildButton("启用", "primary", BuildConfigActionValue(ConfigActionSet, item.Key, "true", scope, item.ChatID, item.UserID)),
			buildButton("停用", "danger", BuildConfigActionValue(ConfigActionSet, item.Key, "false", scope, item.ChatID, item.UserID)),
		)
	}
	buttons = append(buttons, buildButton("默认", "laser", BuildConfigActionValue(ConfigActionDelete, item.Key, "", scope, item.ChatID, item.UserID)))
	return buttonRow("flow", buttons...)
}

func buildConfigCustomValueForm(item ConfigItem, scope string) map[string]any {
	formName := "form_" + sanitizeComponentName(item.Key)
	formField := configFormFieldName(item.Key)
	submitName := "btn_submit_" + sanitizeComponentName(item.Key)
	resetName := "btn_reset_" + sanitizeComponentName(item.Key)
	resetDefaultName := "btn_restore_" + sanitizeComponentName(item.Key)
	placeholder := fmt.Sprintf("输入 0-100，自定义覆盖当前值 %s", item.Value)

	return map[string]any{
		"tag":                "form",
		"name":               formName,
		"vertical_spacing":   "8px",
		"horizontal_spacing": "8px",
		"elements": []any{
			buildConfigColumns(
				buildConfigItemBlock(item),
				[]any{
					map[string]any{
						"tag":           "input",
						"name":          formField,
						"width":         "fill",
						"placeholder":   plainText(placeholder),
						"default_value": item.Value,
					},
					buttonRow(
						"none",
						buildFormButton(
							submitName,
							"提交",
							"primary_filled",
							"submit",
							BuildConfigFormActionValue(item.Key, scope, item.ChatID, item.UserID, formField),
						),
						buildFormButton(
							resetName,
							"重置",
							"default",
							"reset",
							nil,
						),
						buildFormButtonWithSize(
							resetDefaultName,
							"默认",
							"laser",
							"",
							"",
							BuildConfigActionValue(ConfigActionDelete, item.Key, "", scope, item.ChatID, item.UserID),
						),
					),
				},
			),
		},
	}
}

func buildConfigColumns(infoBlock map[string]any, controls []any) map[string]any {
	return larkmsg.ColumnSet([]any{
		larkmsg.Column([]any{infoBlock}, larkmsg.ColumnOptions{
			Width:         "weighted",
			Weight:        3,
			VerticalAlign: "top",
		}),
		larkmsg.Column(controls, larkmsg.ColumnOptions{
			Width:         "weighted",
			Weight:        2,
			VerticalAlign: "top",
		}),
	}, larkmsg.ColumnSetOptions{
		HorizontalSpacing: "16px",
		FlexMode:          "stretch",
	})
}

func buildConfigScopeRow(selectedScope, chatID, userID string) map[string]any {
	scopeOptions := []struct {
		Label string
		Scope string
	}{
		{Label: "Global", Scope: "global"},
		{Label: "Chat", Scope: "chat"},
		{Label: "User", Scope: "user"},
	}
	buttons := make([]map[string]any, 0, len(scopeOptions))
	for _, item := range scopeOptions {
		buttonType := "default"
		if item.Scope == selectedScope {
			buttonType = "primary"
		}
		buttons = append(buttons, buildButton(item.Label, buttonType, BuildConfigViewValue(item.Scope, chatID, userID)))
	}
	return buttonRow("flow", buttons...)
}

func buildFeatureItemBlock(item FeatureItem) map[string]any {
	statusBits := []string{}
	if item.BlockedAtChat {
		statusBits = append(statusBits, "chat")
	}
	if item.BlockedAtUser {
		statusBits = append(statusBits, "user")
	}
	if item.BlockedAtChatUser {
		statusBits = append(statusBits, "chat_user")
	}
	status := "enabled"
	if len(statusBits) > 0 {
		status = "blocked at " + strings.Join(statusBits, ", ")
	}

	content := fmt.Sprintf("**%s**\n%s\n分类: `%s`  状态: `%s`", item.Name, item.Description, item.Category, status)
	return markdownBlock(content)
}

func buildFeatureActionRow(item FeatureItem, chatID, userID string) map[string]any {
	return buttonRow(
		"flow",
		buildFeatureToggleButton("群", item.BlockedAtChat, item.Name, chatID, userID, FeatureActionBlockChat, FeatureActionUnblockChat),
		buildFeatureToggleButton("用户", item.BlockedAtUser, item.Name, chatID, userID, FeatureActionBlockUser, FeatureActionUnblockUser),
		buildFeatureToggleButton("群内用户", item.BlockedAtChatUser, item.Name, chatID, userID, FeatureActionBlockChatUser, FeatureActionUnblockChatUser),
	)
}

func buildFeatureToggleButton(label string, blocked bool, feature, chatID, userID string, blockAction, unblockAction FeatureAction) map[string]any {
	action := blockAction
	buttonLabel := "屏蔽" + label
	buttonType := "danger"
	if blocked {
		action = unblockAction
		buttonLabel = "取消" + label
		buttonType = "primary"
	}

	return buildButton(buttonLabel, buttonType, BuildFeatureActionValue(action, feature, chatID, userID))
}

func buildButton(text, buttonType string, payload map[string]string) map[string]any {
	return buildButtonWithSize(text, buttonType, "", payload)
}

func buildButtonWithSize(text, buttonType, size string, payload map[string]string) map[string]any {
	return larkmsg.Button(text, larkmsg.ButtonOptions{
		Type:    buttonType,
		Size:    size,
		Payload: toAnyMap(payload),
	})
}

func buildFormButton(name, text, buttonType, formActionType string, payload map[string]string) map[string]any {
	return buildFormButtonWithSize(name, text, buttonType, formActionType, "", payload)
}

func buildFormButtonWithSize(name, text, buttonType, formActionType, size string, payload map[string]string) map[string]any {
	return larkmsg.Button(text, larkmsg.ButtonOptions{
		Type:           buttonType,
		Size:           size,
		Name:           name,
		FormActionType: formActionType,
		Payload:        toAnyMap(payload),
	})
}

func buttonRow(flexMode string, buttons ...map[string]any) map[string]any {
	return larkmsg.ButtonRow(flexMode, buttons...)
}

func markdownBlock(content string) map[string]any {
	return larkmsg.Markdown(content)
}

func hintBlock(content string) map[string]any {
	return larkmsg.HintMarkdown(content)
}

func divider() map[string]any {
	return larkmsg.Divider()
}

func plainText(content string) map[string]any {
	return larkmsg.PlainText(content)
}

func callbackBehaviors(payload map[string]string) []any {
	return larkmsg.CallbackBehaviors(toAnyMap(payload))
}

func newRawCard(title string, elements []any) larkmsg.RawCard {
	return larkmsg.NewCardV2(title, elements, larkmsg.CardV2Options{
		HeaderTemplate:  "blue",
		VerticalSpacing: "8px",
	})
}

func toAnyMap(payload map[string]string) map[string]any {
	if len(payload) == 0 {
		return map[string]any{}
	}
	keys := slices.Sorted(maps.Keys(payload))
	values := make(map[string]any, len(keys))
	for _, key := range keys {
		values[key] = payload[key]
	}
	return values
}

func configFormFieldName(key string) string {
	return "input_" + sanitizeComponentName(key)
}

func sanitizeComponentName(value string) string {
	value = strings.NewReplacer("-", "_", ".", "_", ":", "_", " ", "_").Replace(value)
	if value == "" {
		return "field"
	}
	return value
}

func shortID(id string) string {
	if id == "" {
		return "-"
	}
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func normalizeConfigScope(scope string) string {
	switch scope {
	case "global", "chat", "user":
		return scope
	default:
		return "chat"
	}
}

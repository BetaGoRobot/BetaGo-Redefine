package config

import (
	"context"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
)

func BuildConfigCard(ctx context.Context, scope, chatID, openID string) (larkmsg.RawCard, error) {
	return BuildConfigCardWithOptions(ctx, scope, chatID, openID, ConfigCardViewOptions{})
}

func BuildConfigCardWithOptions(ctx context.Context, scope, chatID, openID string, options ConfigCardViewOptions) (larkmsg.RawCard, error) {
	scope = normalizeConfigScope(scope)
	options.SelectedKey = normalizeSelectedConfigKey(options.SelectedKey)
	cardData, err := GetConfigCardDataWithOptions(ctx, scope, chatID, openID, options)
	if err != nil {
		return nil, err
	}
	view := ConfigViewState{
		Scope:              scope,
		ChatID:             chatID,
		OpenID:             openID,
		LastModifierOpenID: options.LastModifierOpenID,
		SelectedKey:        options.SelectedKey,
	}

	elements := make([]any, 0, len(cardData.Configs)*4+3)
	elements = append(elements, markdownBlock(fmt.Sprintf("当前查看/写入作用域: `%s`  当前上下文: chat=`%s` user=`%s`", scope, shortID(chatID), shortID(openID))))
	elements = append(elements, buildConfigScopeRow(view))
	elements = append(elements, buildConfigPickerForm(view))
	configSections := make([][]any, 0, len(cardData.Configs))
	for _, item := range cardData.Configs {
		configSections = append(configSections, []any{buildConfigItemSection(item, view)})
	}
	elements = larkmsg.AppendSectionsWithDividers(elements, configSections...)

	elements = append(elements, hintBlock(fmt.Sprintf("Chat=%s User=%s", shortID(chatID), shortID(openID))))
	return newRawCard(ctx, "配置面板", elements, larkmsg.StandardCardFooterOptions{
		RefreshPayload:     larkmsg.StringMapToAnyMap(BuildConfigViewValueWithState(view)),
		LastModifierOpenID: options.LastModifierOpenID,
		ActionHistory: larkmsg.CardActionHistoryOptions{
			Enabled:        true,
			OpenMessageID:  options.MessageID,
			PendingRecords: options.PendingHistory,
		},
	}), nil
}

func BuildFeatureCard(ctx context.Context, chatID, openID string) (larkmsg.RawCard, error) {
	return BuildFeatureCardWithOptions(ctx, chatID, openID, FeatureCardViewOptions{})
}

func BuildFeatureCardWithOptions(ctx context.Context, chatID, openID string, options FeatureCardViewOptions) (larkmsg.RawCard, error) {
	cardData, err := GetFeatureCardData(ctx, chatID, openID)
	if err != nil {
		return nil, err
	}
	view := FeatureViewState{
		ChatID:             chatID,
		OpenID:             openID,
		LastModifierOpenID: options.LastModifierOpenID,
	}

	elements := make([]any, 0, len(cardData.Features)*3+2)
	elements = append(elements, markdownBlock(fmt.Sprintf("当前上下文: chat=`%s` user=`%s`", shortID(chatID), shortID(openID))))
	featureSections := make([][]any, 0, len(cardData.Features))
	for _, item := range cardData.Features {
		featureSections = append(featureSections, []any{
			buildFeatureItemBlock(item),
			buildFeatureActionRow(item, view),
		})
	}
	elements = larkmsg.AppendSectionsWithDividers(elements, featureSections...)

	elements = append(elements, hintBlock("点击按钮直接调整屏蔽状态"))
	return newRawCard(ctx, "功能开关", elements, larkmsg.StandardCardFooterOptions{
		RefreshPayload:     larkmsg.StringMapToAnyMap(BuildFeatureViewValueWithState(view)),
		LastModifierOpenID: options.LastModifierOpenID,
		ActionHistory: larkmsg.CardActionHistoryOptions{
			Enabled:        true,
			OpenMessageID:  options.MessageID,
			PendingRecords: options.PendingHistory,
		},
	}), nil
}

func buildConfigItemBlock(item ConfigItem) map[string]any {
	content := fmt.Sprintf("**%s**  `%s`\n%s\n当前值:  `%s`  [来源:  `%s`]",
		item.Key,
		item.ValueType,
		item.Description,
		item.Value,
		item.Scope,
	)
	return markdownBlock(content)
}

func buildConfigItemSection(item ConfigItem, view ConfigViewState) map[string]any {
	if !item.IsEditable {
		return buildConfigColumns(buildConfigItemBlock(item), []any{
			hintBlock("只读：启动时加载，请修改 TOML 并重启后生效"),
		})
	}
	switch item.ValueType {
	case "bool":
		return buildConfigColumns(buildConfigItemBlock(item), []any{buildConfigActionRow(item, view)})
	default:
		return buildConfigValueForm(item, view)
	}
}

func buildConfigActionRow(item ConfigItem, view ConfigViewState) map[string]any {
	buttons := make([]map[string]any, 0, 2)
	if item.ValueType == "bool" {
		buttons = append(buttons,
			buildButton("启用", "primary_filled", BuildConfigActionValueWithState(ConfigActionSet, item.Key, "true", withConfigItemView(view, item))),
			buildButton("停用", "danger", BuildConfigActionValueWithState(ConfigActionSet, item.Key, "false", withConfigItemView(view, item))),
		)
	}
	buttons = append(buttons, buildButton("默认", "laser", BuildConfigActionValueWithState(ConfigActionDelete, item.Key, "", withConfigItemView(view, item))))
	return buttonRow("flow", buttons...)
}

func buildConfigValueForm(item ConfigItem, view ConfigViewState) map[string]any {
	formName := "form_" + sanitizeComponentName(item.Key)
	formField := configFormFieldName(item.Key)
	submitName := "btn_submit_" + sanitizeComponentName(item.Key)
	resetName := "btn_reset_" + sanitizeComponentName(item.Key)
	resetDefaultName := "btn_restore_" + sanitizeComponentName(item.Key)

	return map[string]any{
		"tag":                "form",
		"name":               formName,
		"vertical_spacing":   "8px",
		"horizontal_spacing": "8px",
		"elements": []any{
			buildConfigColumns(
				buildConfigItemBlock(item),
				[]any{
					buildConfigValueInput(item, formField),
					buttonRow(
						"none",
						buildFormButton(
							submitName,
							"提交",
							"primary_filled",
							"submit",
							BuildConfigFormActionValueWithState(item.Key, withConfigItemView(view, item), formField),
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
							BuildConfigActionValueWithState(ConfigActionDelete, item.Key, "", withConfigItemView(view, item)),
						),
					),
				},
			),
		},
	}
}

func buildConfigColumns(infoBlock map[string]any, controls []any) map[string]any {
	return larkmsg.SplitColumns(
		[]any{infoBlock},
		controls,
		larkmsg.SplitColumnsOptions{
			Left: larkmsg.ColumnOptions{
				Weight:        3,
				VerticalAlign: "top",
			},
			Right: larkmsg.ColumnOptions{
				Weight:        2,
				VerticalAlign: "top",
			},
			Row: larkmsg.ColumnSetOptions{
				HorizontalSpacing: "16px",
				FlexMode:          "stretch",
			},
		},
	)
}

func buildConfigScopeRow(view ConfigViewState) map[string]any {
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
		if item.Scope == view.Scope {
			buttonType = "primary_filled"
		}
		nextView := view
		nextView.Scope = item.Scope
		buttons = append(buttons, buildButton(item.Label, buttonType, BuildConfigViewValueWithState(nextView)))
	}
	return buttonRow("flow", buttons...)
}

func buildConfigPickerForm(view ConfigViewState) map[string]any {
	keyField := "config_selected_key"
	options := allConfigKeyOptions()
	initialOption := view.SelectedKey
	if !hasSelectStaticValue(options, initialOption) {
		initialOption = firstSelectStaticValue(options)
	}
	return map[string]any{
		"tag":                "form",
		"name":               "form_config_selected_key",
		"vertical_spacing":   "8px",
		"horizontal_spacing": "8px",
		"elements": []any{
			buildConfigColumns(
				markdownBlock("**配置筛选**\n用下拉选择要查看和修改的配置项，卡片下方只渲染当前选中的那一项。"),
				[]any{
					larkmsg.SelectStatic(keyField, larkmsg.SelectStaticOptions{
						Width:         "fill",
						InitialOption: initialOption,
						Options:       options,
						ElementID:     "config_selected_key",
					}),
					buttonRow(
						"none",
						buildFormButton(
							"btn_config_selected_key_submit",
							"查看",
							"primary_filled",
							"submit",
							BuildConfigViewFormValueWithState(view, keyField),
						),
					),
				},
			),
		},
	}
}

func buildConfigValueInput(item ConfigItem, formField string) map[string]any {
	if enumOptions := enumValueOptionsForConfigItem(item); len(enumOptions) > 0 {
		return larkmsg.SelectStatic(formField, larkmsg.SelectStaticOptions{
			Width:         "fill",
			Placeholder:   "选择预设值",
			InitialOption: initialSelectValueForCurrentItem(enumOptions, item.Value),
			Options:       enumOptions,
			ElementID:     formField,
		})
	}

	placeholder := "输入新的配置值"
	if def, ok := GetConfigDefinition(ConfigKey(item.Key)); ok && def.ValueType == "int" {
		placeholder = fmt.Sprintf("输入 %d-%d，自定义覆盖当前值 %s", def.IntMin, def.IntMax, item.Value)
	} else if item.ValueType == "string" {
		placeholder = fmt.Sprintf("输入字符串，覆盖当前值 %s", item.Value)
	}
	return larkmsg.TextInput(formField, larkmsg.TextInputOptions{
		Placeholder:  placeholder,
		DefaultValue: item.Value,
	})
}

func allConfigKeyOptions() []larkmsg.SelectStaticOption {
	keys := GetAllConfigKeys()
	options := make([]larkmsg.SelectStaticOption, 0, len(keys))
	for _, key := range keys {
		options = append(options, larkmsg.SelectStaticOption{
			Text:  fmt.Sprintf("%s | %s", GetConfigDescription(key), key),
			Value: string(key),
		})
	}
	return options
}

func enumValueOptionsForConfigItem(item ConfigItem) []larkmsg.SelectStaticOption {
	return toLarkSelectOptions(GetConfigEnumOptions(ConfigKey(item.Key), item.Value))
}

func toLarkSelectOptions(options []ConfigEnumOption) []larkmsg.SelectStaticOption {
	selectOptions := make([]larkmsg.SelectStaticOption, 0, len(options))
	for _, option := range options {
		value := strings.TrimSpace(option.Value)
		if value == "" {
			continue
		}
		selectOptions = append(selectOptions, larkmsg.SelectStaticOption{
			Text:  option.Text,
			Value: value,
		})
	}
	return selectOptions
}

func initialSelectValueForCurrentItem(options []larkmsg.SelectStaticOption, currentValue string) string {
	currentValue = strings.TrimSpace(currentValue)
	if currentValue != "" {
		for _, option := range options {
			if option.Value == currentValue {
				return currentValue
			}
		}
	}
	return firstSelectStaticValue(options)
}

func hasSelectStaticValue(options []larkmsg.SelectStaticOption, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, option := range options {
		if strings.TrimSpace(option.Value) == value {
			return true
		}
	}
	return false
}

func firstSelectStaticValue(options []larkmsg.SelectStaticOption) string {
	if len(options) == 0 {
		return ""
	}
	return options[0].Value
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

func buildFeatureActionRow(item FeatureItem, view FeatureViewState) map[string]any {
	return buttonRow(
		"flow",
		buildFeatureToggleButton("群", item.BlockedAtChat, item.Name, view, FeatureActionBlockChat, FeatureActionUnblockChat),
		buildFeatureToggleButton("用户", item.BlockedAtUser, item.Name, view, FeatureActionBlockUser, FeatureActionUnblockUser),
		buildFeatureToggleButton("群内用户", item.BlockedAtChatUser, item.Name, view, FeatureActionBlockChatUser, FeatureActionUnblockChatUser),
	)
}

func buildFeatureToggleButton(label string, blocked bool, feature string, view FeatureViewState, blockAction, unblockAction FeatureAction) map[string]any {
	action := blockAction
	buttonLabel := "屏蔽" + label
	buttonType := "danger"
	if blocked {
		action = unblockAction
		buttonLabel = "取消" + label
		buttonType = "primary_filled"
	}

	return buildButton(buttonLabel, buttonType, BuildFeatureActionValueWithState(action, feature, view))
}

func buildButton(text, buttonType string, payload map[string]string) map[string]any {
	return buildButtonWithSize(text, buttonType, "", payload)
}

func buildButtonWithSize(text, buttonType, size string, payload map[string]string) map[string]any {
	return larkmsg.Button(text, larkmsg.ButtonOptions{
		Type:    buttonType,
		Size:    size,
		Payload: larkmsg.StringMapToAnyMap(payload),
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
		Payload:        larkmsg.StringMapToAnyMap(payload),
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

func withConfigItemView(view ConfigViewState, item ConfigItem) ConfigViewState {
	view.ChatID = item.ChatID
	view.OpenID = item.OpenID
	return view
}

func newRawCard(ctx context.Context, title string, elements []any, footerOptions ...larkmsg.StandardCardFooterOptions) larkmsg.RawCard {
	return larkmsg.NewStandardPanelCard(ctx, title, elements, footerOptions...)
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

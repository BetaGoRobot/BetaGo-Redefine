package command

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type commandFormState struct {
	Target      *xcommand.Command[*larkim.P2MessageReceiveV1]
	PathTokens  []string
	RawCommand  string
	ArgSpecs    []xcommand.CommandArg
	ArgValues   map[string]string
	ArgPresent  map[string]bool
	ArgOrder    []string
	InputName   string
	InputValue  string
	Description string
}

type CommandFormViewMode string

const (
	CommandFormViewCompact  CommandFormViewMode = "compact"
	CommandFormViewExpanded CommandFormViewMode = "expanded"

	commandFormCompactOptionalLimit = 3
)

func CanBuildCommandForm(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawCommand string) bool {
	_, err := resolveCommandFormState(root, rawCommand)
	return err == nil
}

func BuildCommandFormCardJSON(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawCommand string) (larkmsg.RawCard, error) {
	return BuildCommandFormCardJSONWithViewMode(root, rawCommand, CommandFormViewCompact)
}

func BuildCommandFormCardJSONWithViewMode(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawCommand string, viewMode CommandFormViewMode) (larkmsg.RawCard, error) {
	state, err := resolveCommandFormState(root, rawCommand)
	if err != nil {
		return nil, err
	}
	viewMode = normalizeCommandFormViewMode(state, viewMode)

	elements := []any{
		larkmsg.Markdown(fmt.Sprintf("**命令**: `%s`", strings.Join(state.PathTokens, " "))),
	}
	if state.Description != "" {
		elements = append(elements, larkmsg.HintMarkdown(state.Description))
	}
	elements = append(elements, buildCommandPathNavigationElements(state.PathTokens)...)
	elements = append(elements, larkmsg.HintMarkdown("留空表示沿用当前命令中的同名参数；当前版本暂不支持通过表单清空已有参数。"))
	if toggle := buildCommandFormViewToggleButton(state, viewMode); toggle != nil {
		elements = append(elements, larkmsg.ButtonRow("none", toggle))
	}

	submitPayload := larkmsg.StringMapToAnyMap(
		cardaction.New(cardaction.ActionCommandSubmitForm).
			WithCommand(state.RawCommand).
			Payload(),
	)

	argSpecs := visibleCommandFormSpecs(state, viewMode)
	if len(state.ArgSpecs) == 0 {
		elements = append(elements,
			larkmsg.Markdown("这个命令没有结构化参数，点击下方按钮可直接执行。"),
			larkmsg.Divider(),
			larkmsg.ButtonRow("none",
				larkmsg.Button("执行命令", larkmsg.ButtonOptions{
					Type:    "primary",
					Payload: submitPayload,
				}),
			),
		)
	} else {
		formElements := make([]any, 0, len(argSpecs)*3+2)
		for idx, spec := range argSpecs {
			if idx > 0 {
				formElements = append(formElements, larkmsg.Divider())
			}
			formElements = append(formElements, buildCommandFormField(spec, state)...)
		}
		formElements = append(formElements,
			larkmsg.Divider(),
			larkmsg.ButtonRow("none",
				larkmsg.Button("填写后执行", larkmsg.ButtonOptions{
					Name:           "submit_command_form",
					Type:           "primary",
					FormActionType: "submit",
					Payload:        submitPayload,
				}),
			),
		)
		elements = append(elements, map[string]any{
			"tag":                "form",
			"name":               "command_form",
			"vertical_spacing":   "8px",
			"horizontal_spacing": "8px",
			"elements":           formElements,
		})
	}

	title := "/" + strings.Join(state.PathTokens, " ")
	return larkmsg.NewCardV2(title, elements, larkmsg.StandardPanelCardV2Options()), nil
}

func BuildCommandFormRawCommand(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawCommand string, formValues map[string]any) (string, error) {
	state, err := resolveCommandFormState(root, rawCommand)
	if err != nil {
		return "", err
	}

	argValues := make(map[string]string, len(state.ArgValues))
	for key, value := range state.ArgValues {
		argValues[key] = value
	}
	argPresent := make(map[string]bool, len(state.ArgPresent))
	for key, value := range state.ArgPresent {
		argPresent[key] = value
	}
	inputValue := state.InputValue

	for _, spec := range state.ArgSpecs {
		fieldValue, ok := formStringValue(formValues, spec.Name)
		if !ok || fieldValue == "" {
			continue
		}
		if spec.Input {
			inputValue = fieldValue
			continue
		}
		if spec.Flag {
			argPresent[spec.Name] = parseBoolSelection(fieldValue)
			if argPresent[spec.Name] {
				argValues[spec.Name] = ""
			} else {
				delete(argValues, spec.Name)
			}
			continue
		}
		argPresent[spec.Name] = true
		argValues[spec.Name] = fieldValue
	}

	var sb strings.Builder
	sb.WriteByte('/')
	sb.WriteString(strings.Join(state.PathTokens, " "))

	known := make(map[string]struct{}, len(state.ArgSpecs))
	for _, spec := range state.ArgSpecs {
		known[spec.Name] = struct{}{}
		if spec.Input {
			continue
		}
		if spec.Flag {
			if argPresent[spec.Name] {
				sb.WriteByte(' ')
				sb.WriteString("--")
				sb.WriteString(spec.Name)
			}
			continue
		}
		if value := strings.TrimSpace(argValues[spec.Name]); value != "" {
			sb.WriteByte(' ')
			sb.WriteString("--")
			sb.WriteString(spec.Name)
			sb.WriteByte('=')
			sb.WriteString(quoteCommandValue(value))
		}
	}

	extraNames := make([]string, 0, len(argValues))
	for _, name := range state.ArgOrder {
		if _, ok := known[name]; ok {
			continue
		}
		if !argPresent[name] {
			continue
		}
		extraNames = append(extraNames, name)
	}
	for name := range argValues {
		if _, ok := known[name]; ok {
			continue
		}
		if !argPresent[name] || containsString(extraNames, name) {
			continue
		}
		extraNames = append(extraNames, name)
	}
	sort.Strings(extraNames)
	for _, name := range extraNames {
		sb.WriteByte(' ')
		sb.WriteString("--")
		sb.WriteString(name)
		if value := strings.TrimSpace(argValues[name]); value != "" {
			sb.WriteByte('=')
			sb.WriteString(quoteCommandValue(value))
		}
	}

	if strings.TrimSpace(inputValue) != "" {
		sb.WriteByte(' ')
		sb.WriteString(inputValue)
	}

	return sb.String(), nil
}

func resolveCommandFormState(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawCommand string) (*commandFormState, error) {
	root = defaultCommandRoot(root)
	rawCommand = normalizeCommandInput(rawCommand)
	if rawCommand == "" {
		return nil, errors.New("empty command")
	}

	tokens := xcommand.GetCommand(context.Background(), rawCommand)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("invalid command: %s", rawCommand)
	}

	target, pathTokens, nextIdx := resolveCommandTarget(root, tokens)
	if target == nil || len(pathTokens) == 0 {
		return nil, fmt.Errorf("command not found: %s", rawCommand)
	}
	if target.Func == nil {
		return nil, fmt.Errorf("command is not executable: /%s", strings.Join(pathTokens, " "))
	}

	argValues, inputValue, argOrder, argPresent := parseCommandArgs(tokens[nextIdx:])
	argSpecs := target.GetArgSpecs()
	inputName := ""
	for _, spec := range argSpecs {
		if spec.Input {
			inputName = spec.Name
			break
		}
	}

	return &commandFormState{
		Target:      target,
		PathTokens:  pathTokens,
		RawCommand:  rawCommand,
		ArgSpecs:    argSpecs,
		ArgValues:   argValues,
		ArgPresent:  argPresent,
		ArgOrder:    argOrder,
		InputName:   inputName,
		InputValue:  inputValue,
		Description: strings.TrimSpace(target.Description),
	}, nil
}

func resolveCommandTarget(root *xcommand.Command[*larkim.P2MessageReceiveV1], tokens []string) (*xcommand.Command[*larkim.P2MessageReceiveV1], []string, int) {
	cur := root
	pathTokens := make([]string, 0, len(tokens))
	idx := 0
	for idx < len(tokens) {
		token := tokens[idx]
		if strings.HasPrefix(token, "--") {
			break
		}
		next, ok := cur.LookupSubCommand(token)
		if !ok {
			break
		}
		cur = next
		pathTokens = append(pathTokens, token)
		idx++
	}
	if cur != nil && cur.Func == nil && idx == len(tokens) {
		if sub := defaultSubCommand(cur); sub != nil {
			cur = sub
			pathTokens = append(pathTokens, sub.Name)
		}
	}
	return cur, pathTokens, idx
}

func defaultSubCommand(cmd *xcommand.Command[*larkim.P2MessageReceiveV1]) *xcommand.Command[*larkim.P2MessageReceiveV1] {
	if cmd == nil || strings.TrimSpace(cmd.DefaultSubCommand) == "" {
		return nil
	}
	return cmd.SubCommands[strings.TrimSpace(cmd.DefaultSubCommand)]
}

func defaultCommandRoot(root *xcommand.Command[*larkim.P2MessageReceiveV1]) *xcommand.Command[*larkim.P2MessageReceiveV1] {
	if root != nil {
		return root
	}
	return LarkRootCommand
}

func normalizeCommandInput(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return raw
}

func parseCommandArgs(tokens []string) (map[string]string, string, []string, map[string]bool) {
	values := make(map[string]string)
	present := make(map[string]bool)
	order := make([]string, 0, len(tokens))
	input := ""
	for idx, token := range tokens {
		if !strings.HasPrefix(token, "--") {
			input = strings.TrimSpace(strings.Join(tokens[idx:], " "))
			break
		}
		name := strings.TrimSpace(strings.TrimPrefix(token, "--"))
		value := ""
		if eqIdx := strings.IndexByte(name, '='); eqIdx >= 0 {
			value = name[eqIdx+1:]
			name = name[:eqIdx]
		}
		if name == "" {
			continue
		}
		if _, ok := values[name]; !ok {
			order = append(order, name)
		}
		values[name] = value
		present[name] = true
	}
	return values, input, order, present
}

func normalizeCommandFormViewMode(state *commandFormState, mode CommandFormViewMode) CommandFormViewMode {
	if mode != CommandFormViewExpanded {
		mode = CommandFormViewCompact
	}
	if !hasHiddenOptionalCommandFormSpecs(state) {
		return CommandFormViewExpanded
	}
	return mode
}

func hasHiddenOptionalCommandFormSpecs(state *commandFormState) bool {
	if state == nil {
		return false
	}
	_, hidden := splitCommandFormSpecs(state)
	return len(hidden) != 0
}

func visibleCommandFormSpecs(state *commandFormState, mode CommandFormViewMode) []xcommand.CommandArg {
	if state == nil {
		return nil
	}
	visible, hidden := splitCommandFormSpecs(state)
	if mode == CommandFormViewExpanded || len(hidden) == 0 {
		return state.ArgSpecs
	}
	return visible
}

func splitCommandFormSpecs(state *commandFormState) ([]xcommand.CommandArg, []xcommand.CommandArg) {
	if state == nil {
		return nil, nil
	}
	visible := make([]xcommand.CommandArg, 0, len(state.ArgSpecs))
	hidden := make([]xcommand.CommandArg, 0, len(state.ArgSpecs))
	remainingOptionalBudget := commandFormCompactOptionalLimit
	for _, spec := range state.ArgSpecs {
		if commandFormSpecAlwaysVisible(state, spec) {
			visible = append(visible, spec)
			continue
		}
		if remainingOptionalBudget > 0 {
			visible = append(visible, spec)
			remainingOptionalBudget--
			continue
		}
		hidden = append(hidden, spec)
	}
	return visible, hidden
}

func commandFormSpecAlwaysVisible(state *commandFormState, spec xcommand.CommandArg) bool {
	if spec.Required || spec.Input {
		return true
	}
	if state == nil {
		return false
	}
	return state.ArgPresent[spec.Name]
}

func buildCommandFormViewToggleButton(state *commandFormState, viewMode CommandFormViewMode) map[string]any {
	if state == nil || !hasHiddenOptionalCommandFormSpecs(state) {
		return nil
	}
	_, hidden := splitCommandFormSpecs(state)
	switch normalizeCommandFormViewMode(state, viewMode) {
	case CommandFormViewExpanded:
		return buildCommandOpenFormButton("收起可选参数", state.RawCommand, "default", CommandFormViewCompact)
	default:
		return buildCommandOpenFormButton(fmt.Sprintf("展开可选参数（%d）", len(hidden)), state.RawCommand, "default", CommandFormViewExpanded)
	}
}

func buildCommandFormField(spec xcommand.CommandArg, state *commandFormState) []any {
	token := spec.UsageToken()
	if spec.Input {
		token = spec.Name
	}

	label := fmt.Sprintf("**%s**", token)
	if spec.Description != "" {
		label += "\n" + spec.Description
	}
	currentValue, isDefaultValue := resolvedCommandFieldValue(spec, state)
	fieldElements := []any{
		larkmsg.Markdown(label),
	}
	if currentValue != "" {
		prefix := "当前值: "
		if isDefaultValue {
			prefix = "默认值: "
		}
		fieldElements = append(fieldElements, larkmsg.HintMarkdown(prefix+markdownInlineCode(currentValue)))
	}

	if len(spec.Options) != 0 {
		initialOption := ""
		if hasCommandArgOption(spec, currentValue) {
			initialOption = currentValue
		}
		fieldElements = append(fieldElements, larkmsg.SelectStatic(spec.Name, larkmsg.SelectStaticOptions{
			Placeholder:   buildCommandFieldPlaceholder(spec),
			Width:         "fill",
			InitialOption: initialOption,
			Options:       toSelectStaticOptions(spec.Options),
			ElementID:     "cmd_" + spec.Name,
		}))
		return fieldElements
	}

	required := spec.Required
	opts := larkmsg.TextInputOptions{
		Placeholder:  buildCommandFieldPlaceholder(spec),
		DefaultValue: currentValue,
		Required:     &required,
		ElementID:    "cmd_" + spec.Name,
	}
	if isMultilineCommandArg(spec.Name) {
		fieldElements = append(fieldElements, larkmsg.TextArea(spec.Name, opts))
	} else {
		fieldElements = append(fieldElements, larkmsg.TextInput(spec.Name, opts))
	}
	return fieldElements
}

func explicitCommandFieldValue(spec xcommand.CommandArg, state *commandFormState) string {
	if spec.Input {
		return strings.TrimSpace(state.InputValue)
	}
	if spec.Flag {
		if !state.ArgPresent[spec.Name] {
			return ""
		}
		if parseBoolSelection(state.ArgValues[spec.Name]) {
			return "true"
		}
		if strings.TrimSpace(state.ArgValues[spec.Name]) == "" {
			return "true"
		}
		return "false"
	}
	return strings.TrimSpace(state.ArgValues[spec.Name])
}

func resolvedCommandFieldValue(spec xcommand.CommandArg, state *commandFormState) (string, bool) {
	if currentValue := explicitCommandFieldValue(spec, state); currentValue != "" {
		return currentValue, false
	}
	if defaultValue := strings.TrimSpace(spec.DefaultValue); defaultValue != "" {
		return defaultValue, true
	}
	return "", false
}

func buildCommandFieldPlaceholder(spec xcommand.CommandArg) string {
	switch {
	case len(spec.Options) != 0:
		return "请选择 " + spec.Name
	case spec.Input && spec.Description != "":
		return spec.Description
	case spec.Input:
		return "请输入内容"
	case spec.Description != "":
		return spec.Description
	default:
		return "请输入 " + spec.Name
	}
}

func isMultilineCommandArg(name string) bool {
	switch name {
	case "query", "message", "reply", "tool_args", "content", "command":
		return true
	default:
		return false
	}
}

func formStringValue(formValues map[string]any, key string) (string, bool) {
	if len(formValues) == 0 {
		return "", false
	}
	value, ok := formValues[key]
	if !ok {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed), true
	case fmt.Stringer:
		return strings.TrimSpace(typed.String()), true
	default:
		return strings.TrimSpace(fmt.Sprint(typed)), true
	}
}

func quoteCommandValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if strings.ContainsAny(value, " \t\r\n\"") {
		return strconv.Quote(value)
	}
	return value
}

func markdownInlineCode(value string) string {
	value = strings.ReplaceAll(value, "`", "\\`")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return "`" + value + "`"
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func parseBoolSelection(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func toSelectStaticOptions(options []xcommand.CommandArgOption) []larkmsg.SelectStaticOption {
	result := make([]larkmsg.SelectStaticOption, 0, len(options))
	for _, option := range options {
		if strings.TrimSpace(option.Value) == "" {
			continue
		}
		result = append(result, larkmsg.SelectStaticOption{
			Text:  option.Label,
			Value: option.Value,
		})
	}
	return result
}

func hasCommandArgOption(spec xcommand.CommandArg, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, option := range spec.Options {
		if strings.TrimSpace(option.Value) == value {
			return true
		}
	}
	return false
}

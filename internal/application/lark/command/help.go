package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	helpCardArgCollapseThreshold     = 6
	helpCardExampleCollapseThreshold = 4
	helpCardSubCommandButtonsPerRow  = 3
	rootHelpButtonsPerRow            = 3
	commandPathButtonsPerRow         = 6
)

type HelpArgs struct {
	Command string `cli:"command,input" help:"命令路径，例如 config set"`
}

type helpHandler struct {
	root *xcommand.Command[*larkim.P2MessageReceiveV1]
}

func newHelpHandler(root *xcommand.Command[*larkim.P2MessageReceiveV1]) helpHandler {
	return helpHandler{root: root}
}

func (helpHandler) CommandDescription() string {
	return "查看命令帮助"
}

func (helpHandler) CommandExamples() []string {
	return []string{
		"/help",
		"/help config set",
	}
}

func (helpHandler) ParseCLI(args []string) (HelpArgs, error) {
	path := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "--") {
			break
		}
		path = append(path, arg)
	}
	return HelpArgs{Command: strings.TrimSpace(strings.Join(path, " "))}, nil
}

func (h helpHandler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg HelpArgs) error {
	_ = metaData
	return larkmsg.ReplyCardJSON(ctx, *data.Event.Message.MessageId, buildHelpCard(ctx, h.root, arg.Command), "_help", false)
}

func buildHelpCard(ctx context.Context, root *xcommand.Command[*larkim.P2MessageReceiveV1], rawPath string) larkmsg.RawCard {
	_ = ctx
	view := buildHelpView(root, rawPath)
	state := resolveHelpCardState(root, rawPath)
	elements := buildHelpCardElements(view, state)
	title := view.Title
	if title == "" {
		title = "命令帮助"
	}
	return larkmsg.NewCardV2(title, elements, larkmsg.StandardPanelCardV2Options())
}

func BuildHelpCardJSON(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawPath string) larkmsg.RawCard {
	return buildHelpCard(context.Background(), root, rawPath)
}

func buildHelpText(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawPath string) string {
	view := buildHelpView(root, rawPath)
	parts := make([]string, 0, 3)
	if view.Title != "" {
		parts = append(parts, view.Title)
	}
	if view.Subtitle != "" {
		parts = append(parts, view.Subtitle)
	}
	if view.Content != "" {
		parts = append(parts, view.Content)
	}
	return strings.Join(parts, "\n")
}

type helpView struct {
	Title         string
	Subtitle      string
	Content       string
	DisplayPath   string
	CanonicalPath string
}

type helpCardState struct {
	Target            *xcommand.Command[*larkim.P2MessageReceiveV1]
	RawTokens         []string
	ActionCommand     string
	ActionState       *commandFormState
	UsedDefaultAction bool
}

func resolveHelpCardState(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawPath string) *helpCardState {
	target := lookupHelpTarget(root, rawPath)
	if target == nil {
		return nil
	}

	rawTokens := helpPathTokens(rawPath)
	actionCommand := ""
	if len(rawTokens) != 0 {
		actionCommand = "/" + strings.Join(rawTokens, " ")
	} else if target.Path() != "/" {
		actionCommand = target.Path()
	}

	state := &helpCardState{
		Target:        target,
		RawTokens:     rawTokens,
		ActionCommand: actionCommand,
	}
	if actionCommand == "" || actionCommand == "/" {
		return state
	}

	actionState, err := resolveCommandFormState(root, actionCommand)
	if err != nil {
		return state
	}
	state.ActionState = actionState
	if len(rawTokens) != 0 && len(actionState.PathTokens) > len(rawTokens) {
		state.UsedDefaultAction = true
	}
	return state
}

func helpPathTokens(rawPath string) []string {
	return strings.Fields(strings.TrimSpace(strings.TrimPrefix(rawPath, "/")))
}

func buildHelpView(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawPath string) helpView {
	target := lookupHelpTarget(root, rawPath)
	if target == nil {
		return renderHelpNotFound(root, rawPath)
	}
	if target.Path() == "/" {
		return renderRootHelp(root)
	}
	return renderCommandHelp(target, helpPathTokens(rawPath))
}

func lookupHelpTarget(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawPath string) *xcommand.Command[*larkim.P2MessageReceiveV1] {
	if root == nil {
		return nil
	}
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return root
	}
	parts := strings.Fields(rawPath)
	if len(parts) == 0 {
		return root
	}
	return root.Find(parts...)
}

func buildHelpCardElements(view helpView, state *helpCardState) []any {
	if state == nil || state.Target == nil {
		return buildHelpFallbackCardElements(view, nil)
	}
	if state.Target.Path() == "/" {
		return buildRootHelpCardElements(view, state.Target)
	}

	elements := []any{}
	if view.Subtitle != "" {
		elements = append(elements, larkmsg.HintMarkdown(view.Subtitle))
	}
	elements = append(elements, buildCommandPathNavigationElements(helpBasePathTokens(state))...)
	sections := [][]any{
		buildHelpUsageSection(view, state.Target),
		buildHelpArgsSection(state.Target),
		buildHelpExamplesSection(view, state.Target),
		buildHelpSubCommandSection(state),
	}
	elements = larkmsg.AppendSectionsWithDividers(elements, sections...)

	if buttons := buildHelpFooterButtons(state); len(buttons) != 0 {
		elements = append(elements, larkmsg.Divider(), larkmsg.ButtonRow("none", buttons...))
	}
	if len(elements) != 0 {
		return elements
	}
	return buildHelpFallbackCardElements(view, buildHelpActionButtonFromState(state))
}

func buildRootHelpCardElements(view helpView, root *xcommand.Command[*larkim.P2MessageReceiveV1]) []any {
	elements := buildHelpFallbackCardElements(view, nil)
	if root == nil || len(root.SubCommands) == 0 {
		return elements
	}
	commandButtons := make([]map[string]any, 0, len(root.SubCommands))
	for _, name := range root.GetSubCommands() {
		cmd := root.SubCommands[name]
		if cmd == nil {
			continue
		}
		label := cmd.Name
		if aliases := cmd.GetAliases(); len(aliases) != 0 {
			label += " / " + aliases[0]
		}
		commandButtons = append(commandButtons, buildCommandOpenHelpButton(label, cmd.Path(), "default"))
	}
	if len(commandButtons) == 0 {
		return elements
	}
	panelElements := make([]any, 0, (len(commandButtons)+rootHelpButtonsPerRow-1)/rootHelpButtonsPerRow)
	for start := 0; start < len(commandButtons); start += rootHelpButtonsPerRow {
		end := start + rootHelpButtonsPerRow
		if end > len(commandButtons) {
			end = len(commandButtons)
		}
		panelElements = append(panelElements, larkmsg.ButtonRow("none", commandButtons[start:end]...))
	}
	elements = append(elements, larkmsg.Divider(), larkmsg.CollapsiblePanel(
		"命令入口",
		panelElements,
		larkmsg.CollapsiblePanelOptions{Expanded: true},
	))
	return elements
}

func buildHelpFallbackCardElements(view helpView, action map[string]any) []any {
	elements := []any{}
	if view.Subtitle != "" {
		elements = append(elements, larkmsg.HintMarkdown(view.Subtitle))
	}
	if view.Content != "" {
		elements = append(elements, larkmsg.Markdown("```text\n"+view.Content+"\n```"))
	}
	if action != nil {
		elements = append(elements, larkmsg.Divider(), larkmsg.ButtonRow("none", action))
	}
	return elements
}

func buildHelpUsageSection(view helpView, cmd *xcommand.Command[*larkim.P2MessageReceiveV1]) []any {
	if cmd == nil {
		return nil
	}
	usagePath := firstNonEmptyString(view.DisplayPath, cmd.Path())
	section := []any{larkmsg.Markdown("```text\n" + renderUsageLineWithPath(cmd, usagePath) + "\n```")}
	if view.CanonicalPath != "" && view.CanonicalPath != usagePath {
		section = append(section, larkmsg.HintMarkdown("Canonical: "+view.CanonicalPath))
	}
	if aliases := visibleHelpAliases(view, cmd); len(aliases) != 0 {
		section = append(section, larkmsg.HintMarkdown("Aliases: "+strings.Join(aliases, ", ")))
	}
	return section
}

func visibleHelpAliases(view helpView, cmd *xcommand.Command[*larkim.P2MessageReceiveV1]) []string {
	if cmd == nil {
		return nil
	}
	aliases := cmd.GetAliases()
	if len(aliases) == 0 {
		return nil
	}
	currentRoot := firstPathToken(view.DisplayPath)
	if currentRoot == "" {
		return aliases
	}
	filtered := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if alias == currentRoot {
			continue
		}
		filtered = append(filtered, alias)
	}
	return filtered
}

func buildHelpArgsSection(cmd *xcommand.Command[*larkim.P2MessageReceiveV1]) []any {
	if cmd == nil {
		return nil
	}
	specs := cmd.GetArgSpecs()
	if len(specs) == 0 {
		return nil
	}

	required, optional := splitHelpArgSpecs(specs)
	if len(optional) == 0 || len(specs) < helpCardArgCollapseThreshold {
		return []any{
			larkmsg.Markdown("**参数**"),
			larkmsg.Markdown(renderHelpArgMarkdown(specs)),
		}
	}

	section := []any{larkmsg.Markdown("**参数**")}
	if len(required) != 0 {
		section = append(section, larkmsg.Markdown(renderHelpArgMarkdown(required)))
	}
	section = append(section, larkmsg.CollapsiblePanel(
		fmt.Sprintf("可选参数（%d）", len(optional)),
		[]any{larkmsg.Markdown(renderHelpArgMarkdown(optional))},
		larkmsg.CollapsiblePanelOptions{
			Expanded: false,
		},
	))
	return section
}

func splitHelpArgSpecs(specs []xcommand.CommandArg) ([]xcommand.CommandArg, []xcommand.CommandArg) {
	required := make([]xcommand.CommandArg, 0, len(specs))
	optional := make([]xcommand.CommandArg, 0, len(specs))
	for _, spec := range specs {
		if spec.Required || spec.Input {
			required = append(required, spec)
			continue
		}
		optional = append(optional, spec)
	}
	return required, optional
}

func renderHelpArgMarkdown(specs []xcommand.CommandArg) string {
	lines := make([]string, 0, len(specs))
	for _, spec := range specs {
		line := "- " + markdownInlineCode(spec.UsageToken())
		if spec.Description != "" {
			line += ": " + spec.Description
		}
		if defaultValue := strings.TrimSpace(spec.DefaultValue); defaultValue != "" {
			line += "，默认值: " + markdownInlineCode(defaultValue)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func buildHelpExamplesSection(view helpView, cmd *xcommand.Command[*larkim.P2MessageReceiveV1]) []any {
	if cmd == nil {
		return nil
	}
	examples := cmd.GetExamples()
	if len(examples) == 0 {
		return nil
	}
	examples = rewriteExamples(examples, view.CanonicalPath, view.DisplayPath)

	content := []any{larkmsg.Markdown(renderHelpExampleMarkdown(examples))}
	if len(examples) > helpCardExampleCollapseThreshold {
		return []any{
			larkmsg.CollapsiblePanel(
				fmt.Sprintf("示例（%d）", len(examples)),
				content,
				larkmsg.CollapsiblePanelOptions{Expanded: false},
			),
		}
	}
	return append([]any{larkmsg.Markdown("**示例**")}, content...)
}

func renderHelpExampleMarkdown(examples []string) string {
	lines := make([]string, 0, len(examples))
	for _, example := range examples {
		lines = append(lines, "- "+markdownInlineCode(example))
	}
	return strings.Join(lines, "\n")
}

func buildHelpSubCommandSection(state *helpCardState) []any {
	if state == nil || state.Target == nil || len(state.Target.SubCommands) == 0 {
		return nil
	}

	panelElements := []any{
		larkmsg.HintMarkdown("选择子命令后查看该子命令帮助。"),
		larkmsg.Markdown(renderHelpSubCommandMarkdown(state)),
	}
	panelElements = append(panelElements, buildHelpSubCommandButtons(state)...)

	return []any{
		larkmsg.CollapsiblePanel(
			"子命令入口",
			panelElements,
			larkmsg.CollapsiblePanelOptions{
				Expanded: len(state.Target.SubCommands) <= helpCardSubCommandButtonsPerRow,
			},
		),
	}
}

func renderHelpSubCommandMarkdown(state *helpCardState) string {
	baseTokens := helpBasePathTokens(state)
	lines := make([]string, 0, len(state.Target.SubCommands))
	for _, name := range state.Target.GetSubCommands() {
		child := state.Target.SubCommands[name]
		if child == nil {
			continue
		}
		commandText := "/" + strings.Join(append(append([]string{}, baseTokens...), child.Name), " ")
		line := "- " + markdownInlineCode(commandText)
		if state.Target.DefaultSubCommand == child.Name {
			line += "（默认）"
		}
		if child.Description != "" {
			line += ": " + child.Description
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func buildHelpSubCommandButtons(state *helpCardState) []any {
	baseTokens := helpBasePathTokens(state)
	buttons := make([]map[string]any, 0, len(state.Target.SubCommands))
	for _, name := range state.Target.GetSubCommands() {
		child := state.Target.SubCommands[name]
		if child == nil || child.Func == nil {
			continue
		}
		commandText := "/" + strings.Join(append(append([]string{}, baseTokens...), child.Name), " ")
		buttons = append(buttons, buildCompactCommandOpenHelpButton("/"+child.Name, commandText))
	}
	rows := make([]any, 0, (len(buttons)+helpCardSubCommandButtonsPerRow-1)/helpCardSubCommandButtonsPerRow)
	for start := 0; start < len(buttons); start += helpCardSubCommandButtonsPerRow {
		end := start + helpCardSubCommandButtonsPerRow
		if end > len(buttons) {
			end = len(buttons)
		}
		rows = append(rows, larkmsg.ButtonRow("flow", buttons[start:end]...))
	}
	return rows
}

func helpBasePathTokens(state *helpCardState) []string {
	if state == nil {
		return nil
	}
	if len(state.RawTokens) != 0 {
		return append([]string{}, state.RawTokens...)
	}
	if state.Target == nil {
		return nil
	}
	return strings.Fields(strings.TrimPrefix(state.Target.Path(), "/"))
}

func buildHelpFooterButtons(state *helpCardState) []map[string]any {
	buttons := make([]map[string]any, 0, 1)
	if action := buildHelpActionButtonFromState(state); action != nil {
		buttons = append(buttons, action)
	}
	return buttons
}

func buildCommandPathNavigationElements(tokens []string) []any {
	buttons := buildCommandPathNavigationButtons(tokens)
	if len(buttons) == 0 {
		return nil
	}
	rows := make([]any, 0, (len(buttons)+commandPathButtonsPerRow-1)/commandPathButtonsPerRow)
	for start := 0; start < len(buttons); start += commandPathButtonsPerRow {
		end := start + commandPathButtonsPerRow
		if end > len(buttons) {
			end = len(buttons)
		}
		rows = append(rows, buildCommandPathNavigationRow(buttons[start:end]))
	}
	return rows
}

func buildCommandPathNavigationButtons(tokens []string) []map[string]any {
	if len(tokens) == 0 {
		return nil
	}
	buttons := make([]map[string]any, 0, len(tokens)+1)
	buttons = append(buttons, buildCompactCommandOpenHelpButton("$", "/"))
	currentPath := make([]string, 0, len(tokens))
	for _, token := range tokens {
		currentPath = append(currentPath, token)
		buttons = append(buttons, buildCompactCommandOpenHelpButton("/"+token, "/"+strings.Join(currentPath, " ")))
	}
	result := make([]map[string]any, 0, len(buttons))
	for _, button := range buttons {
		if button != nil {
			result = append(result, button)
		}
	}
	return result
}

func buildCommandPathNavigationRow(buttons []map[string]any) map[string]any {
	columns := make([]any, 0, len(buttons))
	for _, button := range buttons {
		if button == nil {
			continue
		}
		columns = append(columns, larkmsg.Column([]any{button}, larkmsg.ColumnOptions{
			Width:         "auto",
			VerticalAlign: "center",
		}))
	}
	return larkmsg.ColumnSet(columns, larkmsg.ColumnSetOptions{
		HorizontalSpacing: "0px",
		FlexMode:          "none",
		HorizontalAlign:   "left",
	})
}

func renderRootHelp(root *xcommand.Command[*larkim.P2MessageReceiveV1]) helpView {
	lines := []string{
		"Usage: /help [command]",
		"Examples:",
		"  /help config set",
		"  /bb --help",
		"",
		"Commands:",
	}
	for _, name := range root.GetSubCommands() {
		cmd := root.SubCommands[name]
		if cmd == nil {
			continue
		}
		line := "  " + cmd.Path()
		if aliases := cmd.GetAliases(); len(aliases) != 0 {
			line += " (aliases: " + strings.Join(aliases, ", ") + ")"
		}
		if len(cmd.SubCommands) != 0 {
			line += " [" + strings.Join(cmd.GetSubCommands(), ", ") + "]"
		}
		if cmd.Description != "" {
			line += ": " + cmd.Description
		}
		lines = append(lines, line)
	}
	return helpView{
		Title:    "命令帮助",
		Subtitle: "BetaGo 命令总览",
		Content:  strings.Join(lines, "\n"),
	}
}

func renderCommandHelp(cmd *xcommand.Command[*larkim.P2MessageReceiveV1], rawTokens []string) helpView {
	displayPath := displayCommandPath(cmd, rawTokens)
	canonicalPath := cmd.Path()
	lines := []string{renderUsageLineWithPath(cmd, displayPath)}
	if displayPath != canonicalPath {
		lines = append(lines, "Canonical: "+canonicalPath)
	}
	if aliases := cmd.GetAliases(); len(aliases) != 0 {
		lines = append(lines, "Aliases: "+strings.Join(aliases, ", "))
	}

	if argLines := renderArgLines(cmd.GetArgSpecs()); len(argLines) != 0 {
		lines = append(lines, "")
		lines = append(lines, argLines...)
	}

	if exampleLines := renderExampleLines(cmd.GetExamples()); len(exampleLines) != 0 {
		lines = append(lines, "")
		lines = append(lines, rewriteExampleLines(exampleLines, canonicalPath, displayPath)...)
	}

	if len(cmd.SubCommands) != 0 {
		lines = append(lines, "", "SubCommands:")
		for _, name := range cmd.GetSubCommands() {
			child := cmd.SubCommands[name]
			if child == nil {
				continue
			}
			line := "  " + childDisplayPath(displayPath, child.Name)
			if child.Description != "" {
				line += ": " + child.Description
			}
			lines = append(lines, line)
		}
	}

	return helpView{
		Title:         displayPath,
		Subtitle:      cmd.Description,
		Content:       strings.Join(lines, "\n"),
		DisplayPath:   displayPath,
		CanonicalPath: canonicalPath,
	}
}

func renderUsageLine(cmd *xcommand.Command[*larkim.P2MessageReceiveV1]) string {
	return renderUsageLineWithPath(cmd, cmd.Path())
}

func renderUsageLineWithPath(cmd *xcommand.Command[*larkim.P2MessageReceiveV1], path string) string {
	line := "Usage: " + path
	if args := cmd.GetSupportArgs(); len(args) != 0 {
		line += " " + strings.Join(args, " ")
	}
	if subs := cmd.GetSubCommands(); len(subs) != 0 {
		line += " [" + strings.Join(subs, ", ") + "]"
	}
	return line
}

func displayCommandPath(cmd *xcommand.Command[*larkim.P2MessageReceiveV1], rawTokens []string) string {
	if len(rawTokens) == 0 {
		return cmd.Path()
	}
	return "/" + strings.Join(rawTokens, " ")
}

func childDisplayPath(parentDisplayPath, childName string) string {
	parentDisplayPath = strings.TrimSpace(strings.TrimSuffix(parentDisplayPath, " "))
	if parentDisplayPath == "" || parentDisplayPath == "/" {
		return "/" + childName
	}
	return parentDisplayPath + " " + childName
}

func rewriteExampleLines(lines []string, canonicalPath, displayPath string) []string {
	if canonicalPath == "" || displayPath == "" || canonicalPath == displayPath {
		return lines
	}
	rewritten := make([]string, 0, len(lines))
	for _, line := range lines {
		rewritten = append(rewritten, strings.Replace(line, canonicalPath, displayPath, 1))
	}
	return rewritten
}

func rewriteExamples(examples []string, canonicalPath, displayPath string) []string {
	if canonicalPath == "" || displayPath == "" || canonicalPath == displayPath {
		return examples
	}
	rewritten := make([]string, 0, len(examples))
	for _, example := range examples {
		rewritten = append(rewritten, strings.Replace(example, canonicalPath, displayPath, 1))
	}
	return rewritten
}

func firstPathToken(path string) string {
	path = strings.TrimSpace(strings.TrimPrefix(path, "/"))
	if path == "" {
		return ""
	}
	parts := strings.Fields(path)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func renderArgLines(args []xcommand.CommandArg) []string {
	if len(args) == 0 {
		return nil
	}
	lines := []string{"Args:"}
	for _, arg := range args {
		line := "  " + arg.UsageToken()
		if arg.Description != "" {
			line += ": " + arg.Description
		}
		lines = append(lines, line)
	}
	return lines
}

func renderExampleLines(examples []string) []string {
	if len(examples) == 0 {
		return nil
	}
	lines := []string{"Examples:"}
	for _, example := range examples {
		lines = append(lines, "  "+example)
	}
	return lines
}

func renderHelpNotFound(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawPath string) helpView {
	path := strings.TrimSpace(strings.TrimPrefix(rawPath, "/"))
	if path == "" {
		return renderRootHelp(root)
	}
	rootView := renderRootHelp(root)
	return helpView{
		Title:    "命令帮助",
		Subtitle: "未找到命令: /" + path,
		Content:  rootView.Content,
	}
}

func buildHelpActionButtonFromState(state *helpCardState) map[string]any {
	if state == nil || state.ActionState == nil {
		return nil
	}
	label := "直接执行"
	if len(state.ActionState.ArgSpecs) != 0 {
		label = "填写参数并执行"
	}
	if state.UsedDefaultAction {
		if len(state.ActionState.ArgSpecs) != 0 {
			label = "填写默认子命令参数并执行"
		} else {
			label = "执行默认子命令"
		}
	}
	return buildCommandOpenFormButton(label, state.ActionCommand, "primary", CommandFormViewCompact)
}

func buildCommandOpenFormButton(label, commandText, buttonType string, viewMode CommandFormViewMode) map[string]any {
	commandText = strings.TrimSpace(commandText)
	if commandText == "" {
		return nil
	}
	payload := cardaction.New(cardaction.ActionCommandOpenForm).
		WithCommand(commandText)
	if viewMode != "" {
		payload = payload.WithValue(cardaction.ViewField, string(viewMode))
	}
	return larkmsg.Button(label, larkmsg.ButtonOptions{
		Type: buttonType,
		Payload: larkmsg.StringMapToAnyMap(
			payload.Payload(),
		),
	})
}

func buildCommandOpenHelpButton(label, commandText, buttonType string) map[string]any {
	return buildCommandOpenHelpButtonWithSize(label, commandText, buttonType, "")
}

func buildCompactCommandOpenHelpButton(label, commandText string) map[string]any {
	return buildCommandOpenHelpButtonWithSize(label, commandText, "text", "small")
}

func buildCommandOpenHelpButtonWithSize(label, commandText, buttonType, buttonSize string) map[string]any {
	commandText = strings.TrimSpace(commandText)
	if commandText == "" {
		return nil
	}
	return larkmsg.Button(label, larkmsg.ButtonOptions{
		Type: buttonType,
		Size: buttonSize,
		Payload: larkmsg.StringMapToAnyMap(
			cardaction.New(cardaction.ActionCommandOpenHelp).
				WithCommand(commandText).
				Payload(),
		),
		Fill: true,
	})
}

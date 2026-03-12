package command

import (
	"context"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	cardaction "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
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
	elements := []any{}
	if view.Subtitle != "" {
		elements = append(elements, larkmsg.HintMarkdown(view.Subtitle))
	}
	elements = append(elements, larkmsg.Markdown("```text\n"+view.Content+"\n```"))
	if action := buildHelpActionButton(root, rawPath); action != nil {
		elements = append(elements, larkmsg.Divider(), larkmsg.ButtonRow("none", action))
	}
	title := view.Title
	if title == "" {
		title = "命令帮助"
	}
	return larkmsg.NewCardV2(title, elements, larkmsg.StandardPanelCardV2Options())
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
	Title    string
	Subtitle string
	Content  string
}

func buildHelpView(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawPath string) helpView {
	target := lookupHelpTarget(root, rawPath)
	if target == nil {
		return renderHelpNotFound(root, rawPath)
	}
	if target.Path() == "/" {
		return renderRootHelp(root)
	}
	return renderCommandHelp(target)
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

func renderCommandHelp(cmd *xcommand.Command[*larkim.P2MessageReceiveV1]) helpView {
	lines := []string{renderUsageLine(cmd)}

	if argLines := renderArgLines(cmd.GetArgSpecs()); len(argLines) != 0 {
		lines = append(lines, "")
		lines = append(lines, argLines...)
	}

	if exampleLines := renderExampleLines(cmd.GetExamples()); len(exampleLines) != 0 {
		lines = append(lines, "")
		lines = append(lines, exampleLines...)
	}

	if len(cmd.SubCommands) != 0 {
		lines = append(lines, "", "SubCommands:")
		for _, name := range cmd.GetSubCommands() {
			child := cmd.SubCommands[name]
			if child == nil {
				continue
			}
			line := "  " + child.Path()
			if child.Description != "" {
				line += ": " + child.Description
			}
			lines = append(lines, line)
		}
	}

	return helpView{
		Title:    cmd.Path(),
		Subtitle: cmd.Description,
		Content:  strings.Join(lines, "\n"),
	}
}

func renderUsageLine(cmd *xcommand.Command[*larkim.P2MessageReceiveV1]) string {
	line := "Usage: " + cmd.Path()
	if args := cmd.GetSupportArgs(); len(args) != 0 {
		line += " " + strings.Join(args, " ")
	}
	if subs := cmd.GetSubCommands(); len(subs) != 0 {
		line += " [" + strings.Join(subs, ", ") + "]"
	}
	return line
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

func buildHelpActionButton(root *xcommand.Command[*larkim.P2MessageReceiveV1], rawPath string) map[string]any {
	target := lookupHelpTarget(root, rawPath)
	if target == nil || target.Func == nil {
		return nil
	}
	rawPath = strings.TrimSpace(strings.TrimPrefix(rawPath, "/"))
	commandText := target.Path()
	if rawPath != "" {
		commandText = "/" + rawPath
	}
	commandText = strings.TrimSpace(commandText)
	if commandText == "" || !CanBuildCommandForm(root, commandText) {
		return nil
	}

	label := "直接执行"
	if len(target.GetArgSpecs()) != 0 {
		label = "填写参数并执行"
	}
	return larkmsg.Button(label, larkmsg.ButtonOptions{
		Type: "primary",
		Payload: larkmsg.StringMapToAnyMap(
			cardaction.New(cardaction.ActionCommandOpenForm).
				WithCommand(commandText).
				Payload(),
		),
	})
}

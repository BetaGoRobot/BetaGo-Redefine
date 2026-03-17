package command

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestLookupHelpTarget(t *testing.T) {
	root := NewLarkRootCommand()

	target := lookupHelpTarget(root, "config set")
	if target == nil {
		t.Fatal("expected config set command")
	}
	if target.Path() != "/config set" {
		t.Fatalf("unexpected target path: %s", target.Path())
	}
}

func TestBuildHelpTextRoot(t *testing.T) {
	root := NewLarkRootCommand()

	text := buildHelpText(root, "")
	if !strings.Contains(text, "Usage: /help [command]") {
		t.Fatalf("expected root help usage, got: %s", text)
	}
	if !strings.Contains(text, "/config [delete, list, set]: 配置管理") {
		t.Fatalf("expected config command summary, got: %s", text)
	}
	if !strings.Contains(text, "/help: 查看命令帮助") {
		t.Fatalf("expected help command summary, got: %s", text)
	}
	if !strings.Contains(text, "/mute: 设置或解除禁言") {
		t.Fatalf("expected concise mute summary, got: %s", text)
	}
}

func TestBuildHelpTextLeaf(t *testing.T) {
	root := NewLarkRootCommand()

	text := buildHelpText(root, "config set")
	if !strings.Contains(text, "/config set") {
		t.Fatalf("expected command path, got: %s", text)
	}
	if !strings.Contains(text, "设置配置项") {
		t.Fatalf("expected command description, got: %s", text)
	}
	if !strings.Contains(text, "--key=<value>") || !strings.Contains(text, "--value=<value>") {
		t.Fatalf("expected arg usage, got: %s", text)
	}
	if !strings.Contains(text, "配置键名") || !strings.Contains(text, "配置值") {
		t.Fatalf("expected arg descriptions, got: %s", text)
	}
	if !strings.Contains(text, "Examples:") || !strings.Contains(text, "/config set --key=intent_recognition_enabled --value=true") {
		t.Fatalf("expected command examples, got: %s", text)
	}
}

func TestBuildHelpTextDebugGroup(t *testing.T) {
	root := NewLarkRootCommand()

	text := buildHelpText(root, "debug")
	if !strings.Contains(text, "/debug [card, chatid, conver, image, msgid, panic, repeat, revert, trace]") {
		t.Fatalf("expected debug usage, got: %s", text)
	}
	if !strings.Contains(text, "/debug msgid: 查看引用消息 ID") {
		t.Fatalf("expected debug msgid description, got: %s", text)
	}
	if !strings.Contains(text, "/debug revert: 撤回机器人消息") {
		t.Fatalf("expected debug revert description, got: %s", text)
	}
	if !strings.Contains(text, "/debug trace") {
		t.Fatalf("expected debug examples, got: %s", text)
	}
}

func TestBuildHelpTextScheduleGroup(t *testing.T) {
	root := NewLarkRootCommand()

	text := buildHelpText(root, "schedule")
	if !strings.Contains(text, "/schedule [create, delete, list, manage, pause, query, resume]") {
		t.Fatalf("expected schedule usage, got: %s", text)
	}
	if !strings.Contains(text, "/schedule manage: 打开当前群的 schedule 管理面板") {
		t.Fatalf("expected schedule manage description, got: %s", text)
	}
	if !strings.Contains(text, "/schedule create: 创建 schedule，并回显结果卡片") {
		t.Fatalf("expected schedule create description, got: %s", text)
	}
	if !strings.Contains(text, "/schedule pause: 暂停指定 schedule；仅操作当前群") {
		t.Fatalf("expected schedule pause description, got: %s", text)
	}
	if !strings.Contains(text, "/schedule manage") || !strings.Contains(text, "/schedule create --name=午休提醒") || !strings.Contains(text, "/schedule delete --id=task_id") {
		t.Fatalf("expected schedule examples, got: %s", text)
	}
}

func TestBuildHelpTextWcAliasGroup(t *testing.T) {
	root := NewLarkRootCommand()

	text := buildHelpText(root, "wc")
	if !strings.Contains(text, "/wc") {
		t.Fatalf("expected wc alias help to display raw alias path, got: %s", text)
	}
	if !strings.Contains(text, "Usage: /wc [chunk, chunks, cloud, summary, talkrate]") {
		t.Fatalf("expected wc alias usage to preserve raw alias path, got: %s", text)
	}
	if !strings.Contains(text, "Aliases: wc") {
		t.Fatalf("expected canonical help to show alias note, got: %s", text)
	}
	if !strings.Contains(text, "Canonical: /wordcount") {
		t.Fatalf("expected canonical path note in alias help, got: %s", text)
	}
	if !strings.Contains(text, "/wc summary") || !strings.Contains(text, "/wc chunks") {
		t.Fatalf("expected alias subcommand paths in alias help, got: %s", text)
	}
}

func TestBuildHelpTextRootShowsAliasWithoutDuplicateCommand(t *testing.T) {
	root := NewLarkRootCommand()

	text := buildHelpText(root, "")
	if !strings.Contains(text, "/wordcount (aliases: wc) [chunk, chunks, cloud, summary, talkrate]") {
		t.Fatalf("expected root help to show wordcount alias note, got: %s", text)
	}
	if strings.Contains(text, "\n  /wc:") || strings.Contains(text, "\n  /wc [") {
		t.Fatalf("did not expect alias to appear as duplicate top-level command, got: %s", text)
	}
}

func TestBuildHelpView(t *testing.T) {
	root := NewLarkRootCommand()

	view := buildHelpView(root, "config set")
	if view.Title != "/config set" {
		t.Fatalf("unexpected view title: %s", view.Title)
	}
	if view.Subtitle != "设置配置项" {
		t.Fatalf("unexpected view subtitle: %s", view.Subtitle)
	}
	if !strings.Contains(view.Content, "Usage: /config set") {
		t.Fatalf("expected usage in view content, got: %s", view.Content)
	}
	if !strings.Contains(view.Content, "Examples:") {
		t.Fatalf("expected examples in view content, got: %s", view.Content)
	}
}

func TestBuildHelpTextNotFound(t *testing.T) {
	root := NewLarkRootCommand()

	text := buildHelpText(root, "missing")
	if !strings.Contains(text, "未找到命令: /missing") {
		t.Fatalf("expected not found message, got: %s", text)
	}
	if !strings.Contains(text, "Usage: /help [command]") {
		t.Fatalf("expected root help fallback, got: %s", text)
	}
}

func TestBuildHelpCardIncludesSubCommandQuickEntriesForAliasGroup(t *testing.T) {
	root := NewLarkRootCommand()

	cardData := buildHelpCard(context.TODO(), root, "wc")
	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"子命令入口"`) {
		t.Fatalf("expected subcommand quick entry panel in help card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"action":"command.open_help"`) {
		t.Fatalf("expected subcommand quick entry to open child help: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"command":"/wc summary"`) || !strings.Contains(jsonStr, `"command":"/wc chunks"`) {
		t.Fatalf("expected alias-preserving subcommand help payloads in help card: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"command":"/wordcount summary"`) || strings.Contains(jsonStr, `"command":"/wordcount chunks"`) {
		t.Fatalf("did not expect canonical subcommand payloads when alias path is used: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"/wc"`) {
		t.Fatalf("expected alias title to be preserved in help card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `Usage: /wc [chunk, chunks, cloud, summary, talkrate]`) {
		t.Fatalf("expected alias usage path to be preserved in help card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `Canonical: /wordcount`) {
		t.Fatalf("expected canonical hint in alias help card: %s", jsonStr)
	}
}

func TestBuildHelpCardUsesCompactTextButtonsForSubCommandEntries(t *testing.T) {
	root := NewLarkRootCommand()

	cardData := buildHelpCard(context.TODO(), root, "wc")
	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"子命令入口"`) {
		t.Fatalf("expected subcommand entry panel in help card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"type":"text"`) || !strings.Contains(jsonStr, `"size":"small"`) {
		t.Fatalf("expected subcommand quick entries to use compact text buttons: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"/summary"`) || !strings.Contains(jsonStr, `"content":"/chunks"`) {
		t.Fatalf("expected slash-prefixed subcommand quick entry labels: %s", jsonStr)
	}
}

func TestBuildCommandPathNavigationElementsWrapsWithinSixColumns(t *testing.T) {
	elements := buildCommandPathNavigationElements([]string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"})
	if len(elements) != 2 {
		t.Fatalf("expected path navigation to wrap into multiple rows, got %d rows", len(elements))
	}
	for idx, element := range elements {
		row, ok := element.(map[string]any)
		if !ok {
			t.Fatalf("expected row %d to be a map, got %#v", idx, element)
		}
		columns, ok := row["columns"].([]any)
		if !ok {
			t.Fatalf("expected row %d to contain columns, got %#v", idx, row["columns"])
		}
		if len(columns) > 6 {
			t.Fatalf("expected row %d to contain at most 6 columns, got %d", idx, len(columns))
		}
	}
}

func TestBuildHelpCardRootIncludesCommandQuickEntries(t *testing.T) {
	root := NewLarkRootCommand()

	cardData := buildHelpCard(context.TODO(), root, "")
	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"action":"command.open_help"`) {
		t.Fatalf("expected root help card to contain open help quick entries: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"command":"/config"`) || !strings.Contains(jsonStr, `"command":"/wordcount"`) {
		t.Fatalf("expected root help quick entry payloads: %s", jsonStr)
	}
}

func TestBuildHelpCardTopLevelIncludesRootShortcut(t *testing.T) {
	root := NewLarkRootCommand()

	cardData := buildHelpCard(context.TODO(), root, "wc")
	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"command":"/"`) {
		t.Fatalf("expected top-level help card to retain root shortcut: %s", jsonStr)
	}
}

func TestBuildHelpCardIncludesParentHelpNavigation(t *testing.T) {
	root := NewLarkRootCommand()

	cardData := buildHelpCard(context.TODO(), root, "wc chunks")
	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"action":"command.open_help"`) {
		t.Fatalf("expected help navigation action in child help card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"command":"/wc"`) {
		t.Fatalf("expected parent help payload in child help card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"command":"/wc"`) ||
		!strings.Contains(jsonStr, `"command":"/wc chunks"`) {
		t.Fatalf("expected clickable path navigation payloads in child help card: %s", jsonStr)
	}
}

func TestBuildHelpCardUsesInlinePathNavigation(t *testing.T) {
	root := NewLarkRootCommand()

	cardData := buildHelpCard(context.TODO(), root, "wc chunks")
	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if strings.Contains(jsonStr, "层级跳转") {
		t.Fatalf("did not expect legacy path navigation hint in help card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"type":"text"`) || !strings.Contains(jsonStr, `"size":"small"`) {
		t.Fatalf("expected compact text-style path navigation buttons in help card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"content":"root"`) {
		t.Fatalf("expected root shortcut label in help card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"command":"/"`) ||
		!strings.Contains(jsonStr, `"command":"/wc"`) ||
		!strings.Contains(jsonStr, `"command":"/wc chunks"`) {
		t.Fatalf("expected path navigation callbacks in help card: %s", jsonStr)
	}
}

func TestBuildHelpCardCollapsesOptionalArgsWhenTooMany(t *testing.T) {
	root := xcommand.NewRootCommand[*larkim.P2MessageReceiveV1](nil)
	root.AddSubCommand(
		xcommand.NewCommand[*larkim.P2MessageReceiveV1]("complex", func(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, args ...string) error {
			return nil
		}).
			AddDescription("复杂命令").
			AddArgSpec(xcommand.CommandArg{Name: "required", Required: true, Description: "必填参数"}).
			AddArgSpec(xcommand.CommandArg{Name: "opt_a", Description: "可选参数 A"}).
			AddArgSpec(xcommand.CommandArg{Name: "opt_b", Description: "可选参数 B"}).
			AddArgSpec(xcommand.CommandArg{Name: "opt_c", Description: "可选参数 C"}).
			AddArgSpec(xcommand.CommandArg{Name: "opt_d", Description: "可选参数 D"}).
			AddArgSpec(xcommand.CommandArg{Name: "opt_e", Description: "可选参数 E"}),
	)
	root.BuildChain()

	cardData := buildHelpCard(context.TODO(), root, "complex")
	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"tag":"collapsible_panel"`) {
		t.Fatalf("expected collapsible panel for optional args in help card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `可选参数（5）`) {
		t.Fatalf("expected optional args panel title in help card: %s", jsonStr)
	}
}

package command

import (
	"strings"
	"testing"
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
	if !strings.Contains(text, "/debug [chatid, conver, image, msgid, panic, repeat, revert, trace]") {
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

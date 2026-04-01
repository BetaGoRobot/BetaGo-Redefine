package ops

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

func TestBuildIntentAnalyzeInputIncludesHistoryAndProfileContext(t *testing.T) {
	originalStatusFn := intentSearchStatusFn
	originalHistoryLoader := intentHistoryLoader
	originalProfileLoader := intentProfileLoader
	originalConfigAccessor := intentContextConfigAccessor
	t.Cleanup(func() {
		intentSearchStatusFn = originalStatusFn
		intentHistoryLoader = originalHistoryLoader
		intentProfileLoader = originalProfileLoader
		intentContextConfigAccessor = originalConfigAccessor
	})

	intentSearchStatusFn = func() (bool, string) { return true, "" }
	intentContextConfigAccessor = func(context.Context, string, string) intentContextConfig {
		return fakeIntentContextConfig{enabled: true, historyLimit: 1, profileLimit: 2}
	}
	intentHistoryLoader = func(_ context.Context, _ string, _ string, limit int) ([]string, error) {
		if limit != 1 {
			t.Fatalf("history limit = %d, want 1", limit)
		}
		return []string{"[10:00] <ou_a>: 先看下上周数据"}, nil
	}
	intentProfileLoader = func(_ context.Context, _ string, _ string, limit int) ([]string, error) {
		if limit != 2 {
			t.Fatalf("profile limit = %d, want 2", limit)
		}
		return []string{"画像线索: role=pm", "画像线索: style=concise"}, nil
	}

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}

	input := buildIntentAnalyzeInput(context.Background(), event, meta, "@bot 帮我总结一下")
	if !strings.Contains(input, "当前消息:\n@bot 帮我总结一下") {
		t.Fatalf("input = %q, want contain current message section", input)
	}
	if !strings.Contains(input, "最近上下文(新到旧):\n[10:00] <ou_a>: 先看下上周数据") {
		t.Fatalf("input = %q, want contain history section", input)
	}
	if !strings.Contains(input, "用户画像线索:\n画像线索: role=pm\n画像线索: style=concise") {
		t.Fatalf("input = %q, want contain profile section", input)
	}
}

func TestBuildIntentAnalyzeInputReturnsRawMessageWhenContextDisabled(t *testing.T) {
	originalConfigAccessor := intentContextConfigAccessor
	t.Cleanup(func() {
		intentContextConfigAccessor = originalConfigAccessor
	})

	intentContextConfigAccessor = func(context.Context, string, string) intentContextConfig {
		return fakeIntentContextConfig{enabled: false, historyLimit: 4, profileLimit: 2}
	}

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}
	if got := buildIntentAnalyzeInput(context.Background(), event, meta, "@bot 帮我总结一下"); got != "@bot 帮我总结一下" {
		t.Fatalf("buildIntentAnalyzeInput() = %q, want raw message", got)
	}
}

func TestBuildIntentAnalyzeInputPreviewSupportsIndependentOverrides(t *testing.T) {
	originalStatusFn := intentSearchStatusFn
	originalHistoryLoader := intentHistoryLoader
	originalProfileLoader := intentProfileLoader
	originalConfigAccessor := intentContextConfigAccessor
	t.Cleanup(func() {
		intentSearchStatusFn = originalStatusFn
		intentHistoryLoader = originalHistoryLoader
		intentProfileLoader = originalProfileLoader
		intentContextConfigAccessor = originalConfigAccessor
	})

	intentSearchStatusFn = func() (bool, string) { return true, "" }
	intentContextConfigAccessor = func(context.Context, string, string) intentContextConfig {
		return fakeIntentContextConfig{enabled: true, historyLimit: 4, profileLimit: 2}
	}
	intentHistoryLoader = func(_ context.Context, _ string, _ string, limit int) ([]string, error) {
		if limit != 0 {
			t.Fatalf("history limit = %d, want 0", limit)
		}
		return nil, nil
	}
	intentProfileLoader = func(_ context.Context, _ string, _ string, limit int) ([]string, error) {
		if limit != 1 {
			t.Fatalf("profile limit = %d, want 1", limit)
		}
		return []string{"画像线索: role=pm"}, nil
	}

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}
	preview := BuildIntentAnalyzeInputPreview(context.Background(), event, meta, "@bot 帮我总结一下", IntentAnalyzeInputBuildOptions{
		ContextEnabled: strPtr(true),
		HistoryLimit:   strPtr(0),
		ProfileLimit:   strPtr(1),
	})

	if !preview.ContextEnabled || preview.HistoryLimit != 0 || preview.ProfileLimit != 1 {
		t.Fatalf("preview limits = %+v, want enabled with 0/1", preview)
	}
	if len(preview.HistoryLines) != 0 {
		t.Fatalf("preview.HistoryLines = %v, want empty", preview.HistoryLines)
	}
	if strings.Join(preview.ProfileLines, ",") != "画像线索: role=pm" {
		t.Fatalf("preview.ProfileLines = %v", preview.ProfileLines)
	}
	if !strings.Contains(preview.Input, "用户画像线索:\n画像线索: role=pm") {
		t.Fatalf("preview.Input = %q, want contain profile section", preview.Input)
	}
}

func TestBuildIntentAnalyzeInputPreviewRespectsContextDisableOverride(t *testing.T) {
	originalStatusFn := intentSearchStatusFn
	originalHistoryLoader := intentHistoryLoader
	originalProfileLoader := intentProfileLoader
	originalConfigAccessor := intentContextConfigAccessor
	t.Cleanup(func() {
		intentSearchStatusFn = originalStatusFn
		intentHistoryLoader = originalHistoryLoader
		intentProfileLoader = originalProfileLoader
		intentContextConfigAccessor = originalConfigAccessor
	})

	intentSearchStatusFn = func() (bool, string) { return true, "" }
	intentContextConfigAccessor = func(context.Context, string, string) intentContextConfig {
		return fakeIntentContextConfig{enabled: true, historyLimit: 4, profileLimit: 2}
	}
	historyCalled := false
	profileCalled := false
	intentHistoryLoader = func(context.Context, string, string, int) ([]string, error) {
		historyCalled = true
		return []string{"[10:00] <ou_a>: 先看下上周数据"}, nil
	}
	intentProfileLoader = func(context.Context, string, string, int) ([]string, error) {
		profileCalled = true
		return []string{"画像线索: role=pm"}, nil
	}

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}
	preview := BuildIntentAnalyzeInputPreview(context.Background(), event, meta, "@bot 帮我总结一下", IntentAnalyzeInputBuildOptions{
		ContextEnabled: strPtr(false),
	})

	if preview.Input != "@bot 帮我总结一下" {
		t.Fatalf("preview.Input = %q, want raw message", preview.Input)
	}
	if historyCalled || profileCalled {
		t.Fatalf("loaders called when context disabled: history=%t profile=%t", historyCalled, profileCalled)
	}
}

type fakeIntentContextConfig struct {
	enabled      bool
	historyLimit int
	profileLimit int
}

func (f fakeIntentContextConfig) IntentContextReadEnabled() bool {
	return f.enabled
}

func (f fakeIntentContextConfig) IntentContextHistoryLimit() int {
	return f.historyLimit
}

func (f fakeIntentContextConfig) IntentContextProfileLimit() int {
	return f.profileLimit
}

var _ intentContextConfig = fakeIntentContextConfig{}

package agentruntime

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/bytedance/gg/gresult"
)

func TestNewToolCapabilityExecutesWrappedFunction(t *testing.T) {
	unit := arktools.NewUnit[string]().
		Name("search_history").
		Desc("search history").
		Func(func(ctx context.Context, args string, meta arktools.FCMeta[string]) gresult.R[string] {
			if meta.ChatID != "oc_chat" || meta.OpenID != "ou_actor" {
				return gresult.Err[string](context.Canceled)
			}
			return gresult.OK("search:" + args)
		})

	capability := NewToolCapability(unit, CapabilityMeta{
		Name:            "search_history",
		Kind:            CapabilityKindTool,
		SideEffectLevel: SideEffectLevelNone,
		AllowedScopes:   []CapabilityScope{CapabilityScopeGroup},
		DefaultTimeout:  5 * time.Second,
	}, (*string)(nil))

	result, err := capability.Execute(context.Background(), CapabilityRequest{
		Scope:       CapabilityScopeGroup,
		ChatID:      "oc_chat",
		ActorOpenID: "ou_actor",
		PayloadJSON: []byte(`{"query":"release"}`),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.OutputText != `search:{"query":"release"}` {
		t.Fatalf("unexpected output text: %q", result.OutputText)
	}
}

func TestCommandBridgeCapabilityBuildsRawChatCommandFromInput(t *testing.T) {
	var seen CommandInvocation
	capability := NewCommandBridgeCapability(
		"bb",
		CapabilityMeta{
			Name:            "bb",
			Kind:            CapabilityKindCommand,
			SideEffectLevel: SideEffectLevelChatWrite,
			AllowedScopes:   []CapabilityScope{CapabilityScopeGroup, CapabilityScopeP2P},
			DefaultTimeout:  time.Minute,
		},
		func(ctx context.Context, invocation CommandInvocation, req CapabilityRequest) (CapabilityResult, error) {
			seen = invocation
			return CapabilityResult{OutputText: invocation.RawCommand}, nil
		},
	)

	result, err := capability.Execute(context.Background(), CapabilityRequest{
		Scope:       CapabilityScopeGroup,
		InputText:   "帮我总结一下今天的讨论",
		ActorOpenID: "ou_actor",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.OutputText != "/bb 帮我总结一下今天的讨论" {
		t.Fatalf("unexpected output text: %q", result.OutputText)
	}
	if seen.CommandName != "bb" || seen.RawCommand != "/bb 帮我总结一下今天的讨论" {
		t.Fatalf("unexpected invocation: %#v", seen)
	}
	if len(seen.ParsedArgs) == 0 || seen.ParsedArgs[0] != "bb" {
		t.Fatalf("expected parsed args to start with bb, got %#v", seen.ParsedArgs)
	}
}

func TestCommandBridgeCapabilityHonorsRawCommandPayload(t *testing.T) {
	var seen CommandInvocation
	capability := NewCommandBridgeCapability(
		"bb",
		CapabilityMeta{
			Name:            "bb",
			Kind:            CapabilityKindCommand,
			SideEffectLevel: SideEffectLevelChatWrite,
			AllowedScopes:   []CapabilityScope{CapabilityScopeGroup},
			DefaultTimeout:  time.Minute,
		},
		func(ctx context.Context, invocation CommandInvocation, req CapabilityRequest) (CapabilityResult, error) {
			seen = invocation
			return CapabilityResult{OutputText: invocation.RawCommand}, nil
		},
	)

	_, err := capability.Execute(context.Background(), CapabilityRequest{
		Scope: CapabilityScopeGroup,
		PayloadJSON: []byte(`{
			"raw_command": "/bb --r 帮我总结一下今天的讨论"
		}`),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if seen.RawCommand != "/bb --r 帮我总结一下今天的讨论" {
		t.Fatalf("unexpected raw command: %#v", seen)
	}
	if !slices.Contains(seen.ParsedArgs, "--r") {
		t.Fatalf("expected parsed args to contain --r, got %#v", seen.ParsedArgs)
	}
}

func TestDefaultToolSideEffectLevelCoversDeferredApprovalTools(t *testing.T) {
	cases := []struct {
		name string
		want SideEffectLevel
	}{
		{name: "revert_message", want: SideEffectLevelChatWrite},
		{name: "oneword_get", want: SideEffectLevelChatWrite},
		{name: "music_search", want: SideEffectLevelChatWrite},
		{name: "create_todo", want: SideEffectLevelExternalWrite},
		{name: "update_todo", want: SideEffectLevelExternalWrite},
		{name: "delete_todo", want: SideEffectLevelExternalWrite},
	}

	for _, tc := range cases {
		if got := defaultToolSideEffectLevel(tc.name); got != tc.want {
			t.Fatalf("defaultToolSideEffectLevel(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestToolCapabilityCanAllowCompatibleOutputDuringRuntimeExecution(t *testing.T) {
	unit := arktools.NewUnit[string]().
		Name("permission_manage").
		Desc("permission manage").
		Func(func(ctx context.Context, args string, meta arktools.FCMeta[string]) gresult.R[string] {
			if runtimecontext.ShouldSuppressCompatibleOutput(ctx) {
				return gresult.Err[string](context.Canceled)
			}
			return gresult.OK("ok")
		})

	capability := NewToolCapability(unit, CapabilityMeta{
		Name:                  "permission_manage",
		Kind:                  CapabilityKindTool,
		SideEffectLevel:       SideEffectLevelAdminWrite,
		AllowCompatibleOutput: true,
		AllowedScopes:         []CapabilityScope{CapabilityScopeGroup},
		DefaultTimeout:        5 * time.Second,
	}, (*string)(nil))

	result, err := capability.Execute(context.Background(), CapabilityRequest{
		Scope:       CapabilityScopeGroup,
		ChatID:      "oc_chat",
		ActorOpenID: "ou_actor",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.OutputText != "ok" {
		t.Fatalf("unexpected output text: %q", result.OutputText)
	}
}

func TestDefaultToolAllowCompatibleOutputCoversMessageCardCapabilities(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{name: "gold_price_get", want: true},
		{name: "stock_zh_a_get", want: true},
		{name: "talkrate_get", want: true},
		{name: "word_cloud_get", want: true},
		{name: "word_get", want: true},
		{name: "reply_get", want: true},
		{name: "image_get", want: true},
		{name: "config_list", want: true},
		{name: "feature_list", want: true},
		{name: "ratelimit_stats_get", want: true},
		{name: "ratelimit_list", want: true},
		{name: "permission_manage", want: true},
		{name: "oneword_get", want: true},
		{name: "music_search", want: true},
		{name: "config_set", want: false},
	}

	for _, tc := range cases {
		if got := defaultToolAllowCompatibleOutput(tc.name); got != tc.want {
			t.Fatalf("defaultToolAllowCompatibleOutput(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDefaultToolRequiresApprovalCoversDeferredApprovalTools(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{name: "send_message", want: true},
		{name: "create_schedule", want: true},
		{name: "delete_todo", want: true},
		{name: "search_history", want: false},
	}

	for _, tc := range cases {
		if got := defaultToolRequiresApproval(tc.name); got != tc.want {
			t.Fatalf("defaultToolRequiresApproval(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

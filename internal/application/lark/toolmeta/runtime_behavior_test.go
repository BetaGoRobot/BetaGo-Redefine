package toolmeta

import "testing"

func TestLookupRuntimeBehaviorForAgenticTools(t *testing.T) {
	cases := []struct {
		name                  string
		wantSideEffectLevel   SideEffectLevel
		wantRequiresApproval  bool
		wantCompatibleOutput  bool
		wantResultKey         string
		wantPlaceholderOutput string
		wantApprovalTitle     string
	}{
		{
			name:                  "send_message",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  false,
			wantResultKey:         "send_message_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送消息。",
			wantApprovalTitle:     "审批发送消息",
		},
		{
			name:                  "permission_manage",
			wantSideEffectLevel:   SideEffectLevelAdminWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "permission_manage_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送权限管理卡片。",
			wantApprovalTitle:     "审批权限管理",
		},
		{
			name:                  "create_schedule",
			wantSideEffectLevel:   SideEffectLevelExternalWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  false,
			wantResultKey:         "schedule_tool_result",
			wantPlaceholderOutput: "已发起审批，等待确认后创建 schedule。",
			wantApprovalTitle:     "审批创建 schedule",
		},
		{
			name:                  "music_search",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "music_search_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送音乐卡片。",
			wantApprovalTitle:     "审批发送音乐卡片",
		},
		{
			name:                 "gold_price_get",
			wantSideEffectLevel:  SideEffectLevelNone,
			wantRequiresApproval: false,
			wantCompatibleOutput: true,
		},
		{
			name:                 "stock_zh_a_get",
			wantSideEffectLevel:  SideEffectLevelNone,
			wantRequiresApproval: false,
			wantCompatibleOutput: true,
		},
		{
			name:                 "config_list",
			wantSideEffectLevel:  SideEffectLevelNone,
			wantRequiresApproval: false,
			wantCompatibleOutput: true,
		},
		{
			name:                 "feature_list",
			wantSideEffectLevel:  SideEffectLevelNone,
			wantRequiresApproval: false,
			wantCompatibleOutput: true,
		},
		{
			name:                 "ratelimit_stats_get",
			wantSideEffectLevel:  SideEffectLevelNone,
			wantRequiresApproval: false,
			wantCompatibleOutput: true,
		},
		{
			name:                 "ratelimit_list",
			wantSideEffectLevel:  SideEffectLevelNone,
			wantRequiresApproval: false,
			wantCompatibleOutput: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			behavior, ok := LookupRuntimeBehavior(tc.name)
			if !ok {
				t.Fatalf("LookupRuntimeBehavior(%q) reported missing behavior", tc.name)
			}
			if behavior.SideEffectLevel != tc.wantSideEffectLevel {
				t.Fatalf("side effect level = %q, want %q", behavior.SideEffectLevel, tc.wantSideEffectLevel)
			}
			if behavior.RequiresApproval() != tc.wantRequiresApproval {
				t.Fatalf("requires approval = %v, want %v", behavior.RequiresApproval(), tc.wantRequiresApproval)
			}
			if behavior.AllowCompatibleOutput != tc.wantCompatibleOutput {
				t.Fatalf("allow compatible output = %v, want %v", behavior.AllowCompatibleOutput, tc.wantCompatibleOutput)
			}
			if tc.wantRequiresApproval {
				if behavior.Approval == nil {
					t.Fatal("expected approval behavior to be present")
				}
				if behavior.Approval.ResultKey != tc.wantResultKey {
					t.Fatalf("result key = %q, want %q", behavior.Approval.ResultKey, tc.wantResultKey)
				}
				if behavior.Approval.PlaceholderOutput != tc.wantPlaceholderOutput {
					t.Fatalf("placeholder output = %q, want %q", behavior.Approval.PlaceholderOutput, tc.wantPlaceholderOutput)
				}
				if behavior.Approval.ApprovalTitle != tc.wantApprovalTitle {
					t.Fatalf("approval title = %q, want %q", behavior.Approval.ApprovalTitle, tc.wantApprovalTitle)
				}
				return
			}
			if behavior.Approval != nil {
				t.Fatalf("expected no approval behavior, got %+v", behavior.Approval)
			}
		})
	}
}

func TestLookupRuntimeBehaviorForUnknownTool(t *testing.T) {
	behavior, ok := LookupRuntimeBehavior("unknown_tool")
	if ok {
		t.Fatalf("expected unknown tool to be missing, got %+v", behavior)
	}
	if behavior.RequiresApproval() {
		t.Fatal("unknown tool should not require approval")
	}
}

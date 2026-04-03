package toolmeta

import "testing"

func TestLookupRuntimeBehaviorForRegisteredTools(t *testing.T) {
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
			name:                  "gold_price_get",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "gold_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送金价走势卡。",
			wantApprovalTitle:     "审批发送金价走势卡",
		},
		{
			name:                  "stock_zh_a_get",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "stock_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送股票走势卡。",
			wantApprovalTitle:     "审批发送股票走势卡",
		},
		{
			name:                 "finance_tool_discover",
			wantSideEffectLevel:  SideEffectLevelNone,
			wantRequiresApproval: false,
			wantCompatibleOutput: false,
		},
		{
			name:                 "finance_market_data_get",
			wantSideEffectLevel:  SideEffectLevelNone,
			wantRequiresApproval: false,
			wantCompatibleOutput: false,
		},
		{
			name:                 "finance_news_get",
			wantSideEffectLevel:  SideEffectLevelNone,
			wantRequiresApproval: false,
			wantCompatibleOutput: false,
		},
		{
			name:                 "economy_indicator_get",
			wantSideEffectLevel:  SideEffectLevelNone,
			wantRequiresApproval: false,
			wantCompatibleOutput: false,
		},
		{
			name:                  "talkrate_get",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "talkrate_get_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送发言趋势图。",
			wantApprovalTitle:     "审批发送发言趋势图",
		},
		{
			name:                  "word_cloud_get",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "word_cloud_get_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送词云卡片。",
			wantApprovalTitle:     "审批发送词云卡片",
		},
		{
			name:                  "word_cloud_graph_get",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "word_cloud_graph_get_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送词云图。",
			wantApprovalTitle:     "审批发送词云图",
		},
		{
			name:                  "word_chunks_get",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "word_chunks_get_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送 chunk 列表卡片。",
			wantApprovalTitle:     "审批发送 chunk 列表卡片",
		},
		{
			name:                  "word_chunk_detail_get",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "word_chunk_detail_get_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送 chunk 详情卡片。",
			wantApprovalTitle:     "审批发送 chunk 详情卡片",
		},
		{
			name:                  "word_get",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "word_get_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送复读词条卡片。",
			wantApprovalTitle:     "审批发送复读词条卡片",
		},
		{
			name:                  "reply_get",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "reply_get_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送关键词回复卡片。",
			wantApprovalTitle:     "审批发送关键词回复卡片",
		},
		{
			name:                  "image_get",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "image_get_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送图片素材卡片。",
			wantApprovalTitle:     "审批发送图片素材卡片",
		},
		{
			name:                  "config_list",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "config_list_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送配置列表卡片。",
			wantApprovalTitle:     "审批发送配置列表卡片",
		},
		{
			name:                  "feature_list",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "feature_list_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送功能开关卡片。",
			wantApprovalTitle:     "审批发送功能开关卡片",
		},
		{
			name:                  "ratelimit_stats_get",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "ratelimit_stats_get_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送频控详情卡片。",
			wantApprovalTitle:     "审批发送频控详情卡片",
		},
		{
			name:                  "ratelimit_list",
			wantSideEffectLevel:   SideEffectLevelChatWrite,
			wantRequiresApproval:  true,
			wantCompatibleOutput:  true,
			wantResultKey:         "ratelimit_list_result",
			wantPlaceholderOutput: "已发起审批，等待确认后发送频控概览卡片。",
			wantApprovalTitle:     "审批发送频控概览卡片",
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

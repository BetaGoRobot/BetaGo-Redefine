package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestChatMembersHandlerUsesCurrentChatScope(t *testing.T) {
	old := chatMembersLoader
	defer func() {
		chatMembersLoader = old
	}()

	var capturedChatID string
	chatMembersLoader = func(ctx context.Context, chatID string) (map[string]*larkim.ListMember, error) {
		capturedChatID = chatID
		return map[string]*larkim.ListMember{
			"ou_a": {MemberId: memberToolsStrPtr("ou_a"), Name: memberToolsStrPtr("Alice")},
			"ou_b": {MemberId: memberToolsStrPtr("ou_b"), Name: memberToolsStrPtr("Bob")},
		}, nil
	}

	meta := &xhandler.BaseMetaData{ChatID: "oc_test_chat"}
	if err := ChatMembers.Handle(context.Background(), nil, meta, ChatMembersArgs{Limit: 1}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if capturedChatID != "oc_test_chat" {
		t.Fatalf("captured chat_id = %q, want %q", capturedChatID, "oc_test_chat")
	}
	if result, ok := meta.GetExtra("chat_members_result"); !ok || !strings.Contains(result, `"open_id":"ou_a"`) {
		t.Fatalf("chat_members_result extra missing expected payload: %q", result)
	}
}

func TestChatMembersHandlerRejectsEmptyChatID(t *testing.T) {
	old := chatMembersLoader
	defer func() {
		chatMembersLoader = old
	}()

	chatMembersLoader = func(ctx context.Context, chatID string) (map[string]*larkim.ListMember, error) {
		t.Fatal("chat members loader should not be called when chat scope is empty")
		return nil, nil
	}

	err := ChatMembers.Handle(context.Background(), nil, &xhandler.BaseMetaData{}, ChatMembersArgs{})
	if err == nil {
		t.Fatal("expected empty chat_id error")
	}
	if !strings.Contains(err.Error(), "chat_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecentActiveMembersHandlerUsesCurrentChatScopeAndDedupsByRecency(t *testing.T) {
	old := recentActiveMembersHistoryLoader
	defer func() {
		recentActiveMembersHistoryLoader = old
	}()

	var (
		capturedChatID string
		capturedSize   int
	)
	recentActiveMembersHistoryLoader = func(ctx context.Context, chatID string, size int) (history.OpensearchMsgLogList, error) {
		capturedChatID = chatID
		capturedSize = size
		return history.OpensearchMsgLogList{
			{CreateTime: "2026-03-26 10:05:00", OpenID: "ou_a", UserName: "Alice", MsgList: []string{"最新一条"}},
			{CreateTime: "2026-03-26 10:04:00", OpenID: "ou_b", UserName: "Bob", MsgList: []string{"第二条"}},
			{CreateTime: "2026-03-26 10:03:00", OpenID: "ou_a", UserName: "Alice", MsgList: []string{"更早一条"}},
			{CreateTime: "2026-03-26 10:02:00", OpenID: "ou_c", UserName: "Carol", MsgList: []string{"第三人"}},
		}, nil
	}

	meta := &xhandler.BaseMetaData{ChatID: "oc_test_chat"}
	if err := RecentActiveMembers.Handle(context.Background(), nil, meta, RecentActiveMembersArgs{TopK: 2, LookbackMessages: 30}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if capturedChatID != "oc_test_chat" {
		t.Fatalf("captured chat_id = %q, want %q", capturedChatID, "oc_test_chat")
	}
	if capturedSize != 30 {
		t.Fatalf("captured size = %d, want %d", capturedSize, 30)
	}
	result, ok := meta.GetExtra("recent_active_members_result")
	if !ok {
		t.Fatal("expected recent_active_members_result extra")
	}
	if !strings.Contains(result, `"open_id":"ou_a"`) || !strings.Contains(result, `"message_count_in_window":2`) {
		t.Fatalf("recent active result missing Alice payload: %q", result)
	}
	if !strings.Contains(result, `"open_id":"ou_b"`) {
		t.Fatalf("recent active result missing Bob payload: %q", result)
	}
	if strings.Contains(result, `"open_id":"ou_c"`) {
		t.Fatalf("recent active result should respect top_k: %q", result)
	}
	if strings.Index(result, `"open_id":"ou_a"`) > strings.Index(result, `"open_id":"ou_b"`) {
		t.Fatalf("recent active result should keep recency order: %q", result)
	}
}

func TestRecentActiveMembersHandlerRejectsEmptyChatID(t *testing.T) {
	old := recentActiveMembersHistoryLoader
	defer func() {
		recentActiveMembersHistoryLoader = old
	}()

	recentActiveMembersHistoryLoader = func(ctx context.Context, chatID string, size int) (history.OpensearchMsgLogList, error) {
		t.Fatal("recent active history loader should not be called when chat scope is empty")
		return nil, nil
	}

	err := RecentActiveMembers.Handle(context.Background(), nil, &xhandler.BaseMetaData{}, RecentActiveMembersArgs{})
	if err == nil {
		t.Fatal("expected empty chat_id error")
	}
	if !strings.Contains(err.Error(), "chat_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

var _ = larkuser.GetUserMapFromChatIDCache

func memberToolsStrPtr(v string) *string { return &v }

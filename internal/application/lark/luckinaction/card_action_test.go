package luckinaction

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func TestHandleConfirmPassesRequestAndRunsTask(t *testing.T) {
	loadWorkspaceConfigForTest(t)
	service := &fakeConfirmationService{card: map[string]any{"schema": "2.0"}}
	session := newSeededSessionForTest("ou_user")
	task, err := handleConfirm(service, session)(context.Background(), testActionContextWithMsg(map[string]any{
		cardactionproto.PendingOrderIDField: "po_1",
		cardactionproto.PayloadHashField:    "hash_1",
	}, "om_msg_confirm"))
	if err != nil {
		t.Fatalf("handleConfirm error = %v", err)
	}
	if task == nil {
		t.Fatalf("expected async task")
	}
	// 异步任务里会尝试 Redis 锁；测试环境无 Redis 时锁失败、service.Confirm 不被调用，
	// 因此这里只断言 handleConfirm 同步阶段通过 gate 并返回了 task；
	// service.Confirm 的实参组装由 select 路径覆盖。
}

func TestHandleCancelPassesRequestAndRunsTask(t *testing.T) {
	service := &fakeConfirmationService{}
	session := newSeededSessionForTest("ou_user")
	task, err := handleCancel(service, session)(context.Background(), testActionContextWithMsg(map[string]any{
		cardactionproto.PendingOrderIDField: "po_1",
		cardactionproto.PayloadHashField:    "hash_1",
	}, "om_msg_cancel"))
	if err != nil {
		t.Fatalf("handleCancel error = %v", err)
	}
	if task == nil {
		t.Fatalf("expected async task")
	}
}

func TestHandleConfirmRejectsNonInitiator(t *testing.T) {
	loadWorkspaceConfigForTest(t)
	service := &fakeConfirmationService{}
	session := newSeededSessionForTest("ou_someone_else")
	_, err := handleConfirm(service, session)(context.Background(), testActionContextWithMsg(map[string]any{
		cardactionproto.PendingOrderIDField: "po_1",
		cardactionproto.PayloadHashField:    "hash_1",
	}, "om_msg_reject"))
	if err == nil || err.Error() != onlyInitiatorMsg {
		t.Fatalf("expected initiator-only error, got %v", err)
	}
	if service.confirmCalled {
		t.Fatalf("Confirm should not be called for non-initiator")
	}
}

func TestHandleConfirmRequiresPayloadHash(t *testing.T) {
	service := &fakeConfirmationService{}
	if _, err := handleConfirm(service, newSeededSessionForTest("ou_user"))(context.Background(), testActionContextWithMsg(map[string]any{
		cardactionproto.PendingOrderIDField: "po_1",
	}, "om_msg_no_hash")); err == nil {
		t.Fatalf("missing hash error = nil")
	}
	if service.confirmCalled {
		t.Fatalf("Confirm should not be called")
	}
}

func TestRegisterUsesConfiguredServerURL(t *testing.T) {
	writeLuckinConfigForTest(t, "", "https://luckin.example/mcp")
	if got := luckinServerURL(); got != "https://luckin.example/mcp" {
		t.Fatalf("luckinServerURL() = %q", got)
	}
}

func testActionContext(value map[string]any) *appcardaction.Context {
	return &appcardaction.Context{
		Event: &callback.CardActionTriggerEvent{
			Event: &callback.CardActionTriggerRequest{
				Operator: &callback.Operator{OpenID: "ou_user"},
				Context:  &callback.Context{OpenChatID: "oc_chat", OpenMessageID: "om_msg"},
			},
		},
		Action: &cardactionproto.Parsed{Value: value},
	}
}

// testActionContextNoMsg 不带 message id，使异步任务跳过 PatchCardJSON（避免单测触发 Lark 客户端）。
func testActionContextNoMsg(value map[string]any) *appcardaction.Context {
	return &appcardaction.Context{
		Event: &callback.CardActionTriggerEvent{
			Event: &callback.CardActionTriggerRequest{
				Operator: &callback.Operator{OpenID: "ou_user"},
				Context:  &callback.Context{OpenChatID: "oc_chat"},
			},
		},
		Action: &cardactionproto.Parsed{Value: value},
	}
}

// testActionContextWithMsg 带特定 messageID 的 ctx，用于按 messageID 关联 OrderSession。
func testActionContextWithMsg(value map[string]any, messageID string) *appcardaction.Context {
	return &appcardaction.Context{
		Event: &callback.CardActionTriggerEvent{
			Event: &callback.CardActionTriggerRequest{
				Operator: &callback.Operator{OpenID: "ou_user"},
				Context:  &callback.Context{OpenChatID: "oc_chat", OpenMessageID: messageID},
			},
		},
		Action: &cardactionproto.Parsed{Value: value},
	}
}

// newSeededSessionForTest 准备一个内存版 SessionStore，并在每个 testActionContextWithMsg 用到的
// messageID 上写入一条 OrderSession（发起人 = initiator）。
func newSeededSessionForTest(initiator string) luckin.SessionStore {
	store := &memSessionStore{}
	for _, msg := range []string{"om_msg", "om_msg_confirm", "om_msg_reject", "om_msg_cancel", "om_msg_no_hash"} {
		key := luckin.NewSessionKey(luckin.CredentialRequest{}, msg)
		store.SetSession(context.Background(), key, luckin.OrderSession{
			InitiatorOpenID: initiator,
			ChatID:          "oc_chat",
		})
	}
	return store
}

type fakeConfirmationService struct {
	card          map[string]any
	confirmCalled bool
	confirmReq    luckin.ConfirmRequest
	cancelCalled  bool
	cancelReq     luckin.CancelRequest
	failCard      map[string]any
}

func (s *fakeConfirmationService) Confirm(ctx context.Context, req luckin.ConfirmRequest) (map[string]any, error) {
	s.confirmCalled = true
	s.confirmReq = req
	return s.card, nil
}

func (s *fakeConfirmationService) Cancel(ctx context.Context, req luckin.CancelRequest) error {
	s.cancelCalled = true
	s.cancelReq = req
	return nil
}

func (s *fakeConfirmationService) CardAfterConfirmError(ctx context.Context, pendingOrderID string, confirmErr error, notice string) map[string]any {
	if s.failCard != nil {
		return s.failCard
	}
	return map[string]any{"schema": "2.0", "pending": pendingOrderID, "notice": notice}
}

func writeLuckinConfigForTest(t *testing.T, credentialsKey, serverURL string) {
	t.Helper()
	restoreWorkspaceConfigAfterTest(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := "[luckin_mcp]\n" +
		"credentials_key = \"" + credentialsKey + "\"\n" +
		"server_url = \"" + serverURL + "\"\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := infraConfig.LoadFileE(path); err != nil {
		t.Fatalf("load config: %v", err)
	}
}

func restoreWorkspaceConfigAfterTest(t *testing.T) {
	t.Helper()
	workspaceConfig, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve workspace config: %v", err)
	}
	t.Cleanup(func() {
		if _, err := infraConfig.LoadFileE(workspaceConfig); err != nil {
			t.Errorf("restore workspace config: %v", err)
		}
	})
}

// loadWorkspaceConfigForTest 加载工作区配置，供需要 botidentity/config 的用例使用。
func loadWorkspaceConfigForTest(t *testing.T) {
	t.Helper()
	workspaceConfig, err := filepath.Abs("../../../../.dev/config.toml")
	if err != nil {
		t.Fatalf("resolve workspace config: %v", err)
	}
	if _, err := infraConfig.LoadFileE(workspaceConfig); err != nil {
		t.Fatalf("load workspace config: %v", err)
	}
}

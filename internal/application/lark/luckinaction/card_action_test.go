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

func TestHandleConfirmPassesRequestAndReturnsCard(t *testing.T) {
	service := &fakeConfirmationService{card: map[string]any{"schema": "2.0"}}
	resp, err := handleConfirm(service)(context.Background(), testActionContext(map[string]any{
		cardactionproto.PendingOrderIDField: "po_1",
		cardactionproto.PayloadHashField:    "hash_1",
	}))
	if err != nil {
		t.Fatalf("handleConfirm error = %v", err)
	}
	if !service.confirmCalled {
		t.Fatalf("Confirm was not called")
	}
	if service.confirmReq.PendingOrderID != "po_1" || service.confirmReq.PayloadHash != "hash_1" {
		t.Fatalf("confirm request id/hash mismatch")
	}
	if service.confirmReq.OperatorOpenID != "ou_user" || service.confirmReq.ChatID != "oc_chat" {
		t.Fatalf("confirm request operator/chat mismatch")
	}
	if service.confirmReq.Now.IsZero() {
		t.Fatalf("confirm request Now is zero")
	}
	if resp == nil || resp.Toast == nil || resp.Toast.Type != "info" || resp.Card == nil || resp.Card.Type != "raw" {
		t.Fatalf("unexpected confirm response: %+v", resp)
	}
}

func TestHandleCancelPassesRequest(t *testing.T) {
	service := &fakeConfirmationService{}
	resp, err := handleCancel(service)(context.Background(), testActionContext(map[string]any{
		cardactionproto.PendingOrderIDField: "po_1",
		cardactionproto.PayloadHashField:    "hash_1",
	}))
	if err != nil {
		t.Fatalf("handleCancel error = %v", err)
	}
	if !service.cancelCalled {
		t.Fatalf("Cancel was not called")
	}
	if service.cancelReq.PendingOrderID != "po_1" || service.cancelReq.PayloadHash != "hash_1" {
		t.Fatalf("cancel request id/hash mismatch")
	}
	if resp == nil || resp.Toast == nil || resp.Toast.Type != "info" {
		t.Fatalf("unexpected cancel response: %+v", resp)
	}
}

func TestHandleConfirmRequiresPayloadHash(t *testing.T) {
	service := &fakeConfirmationService{}
	if _, err := handleConfirm(service)(context.Background(), testActionContext(map[string]any{
		cardactionproto.PendingOrderIDField: "po_1",
	})); err == nil {
		t.Fatalf("missing hash error = nil")
	}
	if service.confirmCalled {
		t.Fatalf("Confirm should not be called")
	}
}

func TestCredentialStoreReturnsSystemToken(t *testing.T) {
	writeLuckinConfigForTest(t, "system-token", "", "")
	cred, err := credentialStore{}.FindToken(context.Background(), luckin.CredentialLookup{
		Provider: luckin.ProviderLuckin,
		Scope:    luckin.CredentialScope{Type: luckin.ScopeSystem},
	})
	if err != nil {
		t.Fatalf("FindToken error = %v", err)
	}
	if cred.Token != "system-token" || cred.TokenHint != "****oken" {
		t.Fatalf("system credential mismatch: hint=%q len=%d", cred.TokenHint, len(cred.Token))
	}
}

func TestRegisterUsesConfiguredServerURL(t *testing.T) {
	writeLuckinConfigForTest(t, "", "", "https://luckin.example/mcp")
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

type fakeConfirmationService struct {
	card          map[string]any
	confirmCalled bool
	confirmReq    luckin.ConfirmRequest
	cancelCalled  bool
	cancelReq     luckin.CancelRequest
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

func writeLuckinConfigForTest(t *testing.T, systemToken, credentialsKey, serverURL string) {
	t.Helper()
	restoreWorkspaceConfigAfterTest(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := "[luckin_mcp]\n" +
		"system_token = \"" + systemToken + "\"\n" +
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

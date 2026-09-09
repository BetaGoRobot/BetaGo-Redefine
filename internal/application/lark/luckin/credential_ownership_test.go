package luckin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
)

func TestConfirmRejectsCredentialOwnedBySomeoneElseBeforeSideEffects(t *testing.T) {
	for _, mode := range []CheckoutMode{CheckoutModeSelfService, CheckoutModeInitiatorUnified, ""} {
		t.Run(string(mode), func(t *testing.T) {
			now := time.Now()
			order := testConfirmableOrder(json.RawMessage(`{"deptId":1}`), now.Add(time.Minute))
			order.InitiatorOpenID = "ou_a"
			order.RequesterOpenID = "ou_b"
			order.CredentialScope = CredentialScope{Type: ScopePersonal, ID: "ou_a"}
			order.CheckoutMode = mode
			store := &fakePendingStore{order: order}
			tokens := &fakeCredentialLookup{credential: Credential{Token: "initiator-token"}}
			caller := &fakeToolCaller{}
			service := NewConfirmationService(store, tokens, caller, ServerURL)
			_, err := service.Confirm(context.Background(), ConfirmRequest{
				PendingOrderID: order.ID, PayloadHash: order.PayloadHash,
				OperatorOpenID: "ou_b", ChatID: order.ChatID, Now: now,
			})
			if !errors.Is(err, ErrPendingOrderCredentialMismatch) {
				t.Errorf("Confirm error = %v, want ErrPendingOrderCredentialMismatch", err)
			}
			if tokens.lookup.Provider != "" || caller.req.ToolName != "" || store.markConfirmedCalled {
				t.Error("credential mismatch must not read tokens, create orders, or mark confirmed")
			}
			card := string(mustJSON(service.CardAfterConfirmError(context.Background(), order.ID, err, "账号归属不匹配")))
			if !strings.Contains(card, "重新结算") || strings.Contains(card, "luckin_order_confirm") || strings.Contains(card, "luckin_coupon_apply") {
				t.Error("credential mismatch card must require new checkout without old confirm/coupon actions")
			}
			if err := service.Cancel(context.Background(), CancelRequest{
				PendingOrderID: order.ID, PayloadHash: order.PayloadHash,
				OperatorOpenID: "ou_b", ChatID: order.ChatID, Now: now,
			}); err != nil {
				t.Fatalf("old mismatched draft must remain cancellable: %v", err)
			}
			if !store.markCancelledCalled {
				t.Fatal("old mismatched draft cancellation was not persisted")
			}
		})
	}
}

func TestCheckoutKeepsPersonalCredentialThroughPreviewConfirmAndTracking(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      CheckoutMode
		requester string
	}{
		{"self_service_participant", CheckoutModeSelfService, "ou_b"},
		{"unified_initiator", CheckoutModeInitiatorUnified, "ou_a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			scope := CredentialScope{Type: ScopePersonal, ID: tc.requester}
			cred := Credential{Scope: scope, Token: "token-" + tc.requester}
			caller := &fakeToolCaller{result: mcpclient.CallResult{Content: json.RawMessage(`{"data":{"orderId":"remote-order"}}`)}}
			draft := NewDraftService(caller, ServerURL)
			order, _, err := draft.Draft(context.Background(), DraftRequest{
				AppID: "app", BotOpenID: "bot", ChatID: "chat", InitiatorOpenID: "ou_a",
				RequesterOpenID: tc.requester, CheckoutMode: tc.mode, Credential: cred,
				Shop: ShopSelection{DeptID: 1}, Items: []CartItem{{ProductID: 1, Amount: 1, AddedByOpenID: tc.requester}}, Now: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			if caller.req.ToolName != "previewOrder" || caller.req.Server.Headers["Authorization"] != "Bearer "+cred.Token {
				t.Fatal("preview must use the requester's personal credential")
			}
			if order.RequesterOpenID != tc.requester || order.InitiatorOpenID != "ou_a" || order.CredentialScope != scope {
				t.Fatal("draft lost actor or credential ownership")
			}
			// CheckoutMode is not available after loading legacy database rows.
			order.CheckoutMode = ""
			store := &fakePendingStore{order: order}
			tokens := &fakeCredentialLookup{credential: cred}
			tracker := &recordingOrderTracker{}
			service := NewConfirmationServiceWithTracking(store, tokens, caller, ServerURL, tracker, nil, DefaultOrderPollConfig())
			_, err = service.Confirm(context.Background(), ConfirmRequest{
				PendingOrderID: order.ID, PayloadHash: order.PayloadHash, OperatorOpenID: tc.requester, ChatID: "chat", Now: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			if tokens.lookup.Scope != scope || caller.req.ToolName != "createOrder" || caller.req.Server.Headers["Authorization"] != "Bearer "+cred.Token {
				t.Fatal("confirm must use the requester's personal credential")
			}
			if len(tracker.records) != 1 || tracker.records[0].CredentialScope != scope || tracker.records[0].RequesterOpenID != tc.requester || tracker.records[0].InitiatorOpenID != "ou_a" {
				t.Fatal("tracking must retain actual requester, initiator and credential owner")
			}
		})
	}
}

type recordingOrderTracker struct{ records []OrderRecord }

func (r *recordingOrderTracker) CreateOrder(_ context.Context, record OrderRecord) error {
	r.records = append(r.records, record)
	return nil
}

func TestSelfServiceCartDescribesPersonalAccountAndParticipantCheckout(t *testing.T) {
	card := BuildCartCard(ShopSelection{DeptName: "门店"}, Cart{Items: []CartItem{{Amount: 1}}}, CheckoutModeSelfService)
	text := string(mustJSON(card))
	if !strings.Contains(text, "使用各自的个人瑞幸账号") || strings.Contains(text, "仅发起人可点") {
		t.Fatal("self-service card must explain personal accounts and allow participants to checkout")
	}
}

func TestValidatePersonalCredentialOwner(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requester string
		scope     CredentialScope
		valid     bool
	}{
		{"own", "ou_b", CredentialScope{Type: ScopePersonal, ID: "ou_b"}, true},
		{"trimmed", " ou_b ", CredentialScope{Type: ScopePersonal, ID: " ou_b "}, true},
		{"other_person", "ou_b", CredentialScope{Type: ScopePersonal, ID: "ou_a"}, false},
		{"empty_requester", " ", CredentialScope{Type: ScopePersonal, ID: " "}, false},
		{"empty_scope_id", "ou_b", CredentialScope{Type: ScopePersonal}, false},
		{"chat_scope", "ou_b", CredentialScope{Type: ScopeChat, ID: "ou_b"}, false},
		{"system_scope", "ou_b", CredentialScope{Type: ScopeSystem, ID: "ou_b"}, false},
		{"missing_type", "ou_b", CredentialScope{ID: "ou_b"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePersonalCredentialOwner(tc.requester, tc.scope)
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && !errors.Is(err, ErrPendingOrderCredentialMismatch) {
				t.Fatalf("error = %v, want credential mismatch", err)
			}
		})
	}
}

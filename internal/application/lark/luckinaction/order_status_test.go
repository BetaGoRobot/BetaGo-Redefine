package luckinaction

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
)

type statusOrderFinderFake struct {
	record luckin.OrderRecord
	err    error
	keys   []string
}

func (f *statusOrderFinderFake) FindOrder(_ context.Context, appID, botID, orderID string) (luckin.OrderRecord, error) {
	f.keys = []string{appID, botID, orderID}
	return f.record, f.err
}

type statusTokenStoreFake struct {
	lookups []luckin.CredentialLookup
	token   string
	err     error
}

func (f *statusTokenStoreFake) FindToken(_ context.Context, lookup luckin.CredentialLookup) (luckin.Credential, error) {
	f.lookups = append(f.lookups, lookup)
	return luckin.Credential{Provider: luckin.ProviderLuckin, Scope: lookup.Scope, Token: f.token}, f.err
}

type statusToolCallerFake struct {
	calls []mcpclient.CallRequest
	err   error
}

func (f *statusToolCallerFake) CallTool(_ context.Context, req mcpclient.CallRequest) (mcpclient.CallResult, error) {
	f.calls = append(f.calls, req)
	return mcpclient.CallResult{Content: json.RawMessage(`{"data":{"orderId":"order-b","status":10}}`)}, f.err
}

func statusOrderFixture() (luckin.OrderRecord, luckin.CredentialRequest) {
	return luckin.OrderRecord{
			AppID: "app", BotOpenID: "bot", ChatID: "chat", OrderID: "order-b",
			RequesterOpenID: "buyer-b", InitiatorOpenID: "initiator-a",
			CredentialScope: luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "buyer-b"},
		}, luckin.CredentialRequest{
			AppID: "app", BotOpenID: "bot", ChatID: "chat", OpenID: "visitor-c", ChatType: luckin.ChatTypeGroup,
		}
}

func TestOrderStatusUsesStoredCredentialForVisitorAndHistoricalOrder(t *testing.T) {
	for _, owner := range []string{"buyer-b", "initiator-a"} {
		t.Run(owner, func(t *testing.T) {
			record, req := statusOrderFixture()
			record.CredentialScope.ID = owner
			orders := &statusOrderFinderFake{record: record}
			tokens := &statusTokenStoreFake{token: "fake-token-" + owner}
			caller := &statusToolCallerFake{}
			service := orderStatusService{orders: orders, tokens: tokens, draft: luckin.NewDraftService(caller, "https://example.invalid")}
			detail, err := service.detail(context.Background(), req, record.OrderID)
			if err != nil || detail.OrderID != record.OrderID {
				t.Fatalf("detail = %+v, error = %v", detail, err)
			}
			if !reflect.DeepEqual(orders.keys, []string{"app", "bot", "order-b"}) {
				t.Fatalf("order lookup keys = %v", orders.keys)
			}
			want := luckin.CredentialLookup{Provider: luckin.ProviderLuckin, AppID: "app", BotOpenID: "bot", Scope: record.CredentialScope}
			if !reflect.DeepEqual(tokens.lookups, []luckin.CredentialLookup{want}) {
				t.Fatalf("credential lookups = %+v, want stored owner %s only", tokens.lookups, owner)
			}
			if len(caller.calls) != 1 || caller.calls[0].Server.Headers["Authorization"] != "Bearer fake-token-"+owner {
				t.Fatalf("remote request did not use stored owner's credential: %+v", caller.calls)
			}
			if caller.calls[0].ToolName != "queryOrderDetailInfo" || string(caller.calls[0].Arguments) != `{"orderId":"order-b"}` {
				t.Fatalf("unexpected order detail request: %+v", caller.calls[0])
			}
		})
	}
}

func TestOrderStatusRejectsUntrustedRecordBeforeCredentialLookup(t *testing.T) {
	for _, field := range []string{"app", "bot", "chat", "order", "missing"} {
		t.Run(field, func(t *testing.T) {
			record, req := statusOrderFixture()
			switch field {
			case "app":
				record.AppID = "another-app"
			case "bot":
				record.BotOpenID = "another-bot"
			case "chat":
				record.ChatID = "another-chat"
			case "order":
				record.OrderID = "another-order"
			case "missing":
				record = luckin.OrderRecord{}
			}
			tokens, caller := &statusTokenStoreFake{token: "fake"}, &statusToolCallerFake{}
			service := orderStatusService{orders: &statusOrderFinderFake{record: record}, tokens: tokens, draft: luckin.NewDraftService(caller, "")}
			if _, err := service.detail(context.Background(), req, "order-b"); err == nil {
				t.Fatal("accepted order outside requested chat/tenant/order")
			}
			if len(tokens.lookups) != 0 || len(caller.calls) != 0 {
				t.Fatal("unauthorized lookup read credentials or called remote service")
			}
		})
	}
}

func TestOrderStatusDoesNotFallbackOnMissingScopeOrCredential(t *testing.T) {
	for _, failure := range []string{"scope-type", "scope-id", "missing-token", "empty-token", "missing-order"} {
		t.Run(failure, func(t *testing.T) {
			record, req := statusOrderFixture()
			tokens := &statusTokenStoreFake{token: "fake"}
			orders := &statusOrderFinderFake{record: record}
			wantLookups := 0
			switch failure {
			case "scope-type":
				orders.record.CredentialScope.Type = ""
			case "scope-id":
				orders.record.CredentialScope.ID = " "
			case "missing-token":
				tokens.err = luckin.ErrCredentialNotFound
				wantLookups = 1
			case "empty-token":
				tokens.token = " "
				wantLookups = 1
			case "missing-order":
				orders.err = errors.New("order not found")
			}
			caller := &statusToolCallerFake{}
			service := orderStatusService{orders: orders, tokens: tokens, draft: luckin.NewDraftService(caller, "")}
			_, err := service.detail(context.Background(), req, record.OrderID)
			if err == nil || len(tokens.lookups) != wantLookups || len(caller.calls) != 0 {
				t.Fatalf("error=%v, token lookups=%d, remote calls=%d", err, len(tokens.lookups), len(caller.calls))
			}
			if orders.err != nil && !errors.Is(err, orders.err) {
				t.Fatalf("lost order lookup error: %v", err)
			}
			if tokens.err != nil && !errors.Is(err, tokens.err) {
				t.Fatalf("lost credential error: %v", err)
			}
		})
	}
}

func TestLoadOrderCredentialUsesPersistedScopeOnly(t *testing.T) {
	record, _ := statusOrderFixture()
	record.CredentialScope.ID = "historical-owner-a"
	tokens := &statusTokenStoreFake{token: "fake-token"}
	cred, err := loadOrderCredential(context.Background(), tokens, record)
	if err != nil || cred.Scope != record.CredentialScope || len(tokens.lookups) != 1 {
		t.Fatalf("credential=%+v error=%v lookups=%+v", cred, err, tokens.lookups)
	}
	if tokens.lookups[0].AppID != record.AppID || tokens.lookups[0].BotOpenID != record.BotOpenID {
		t.Fatal("persisted credential tenant was not used")
	}
}

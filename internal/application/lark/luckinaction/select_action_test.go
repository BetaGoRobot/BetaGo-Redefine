package luckinaction

import (
	"context"
	"testing"

	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func TestHandleShopSelectStoresSession(t *testing.T) {
	session := &memSessionStore{}
	resp, err := handleShopSelect(session)(context.Background(), testActionContext(map[string]any{
		cardactionproto.LuckinDeptIDField:    "245062453",
		cardactionproto.LuckinDeptNameField:  "AI点单专用",
		cardactionproto.LuckinLongitudeField: "116.39",
		cardactionproto.LuckinLatitudeField:  "39.98",
	}))
	if err != nil {
		t.Fatalf("handleShopSelect error = %v", err)
	}
	if resp == nil || resp.Toast == nil {
		t.Fatalf("expected toast response")
	}
	shop, ok := session.shop, session.ok
	if !ok || shop.DeptID != 245062453 || shop.Longitude == 0 {
		t.Fatalf("session not stored: %+v ok=%v", shop, ok)
	}
}

func TestHandleProductSelectRequiresShop(t *testing.T) {
	session := &memSessionStore{}
	_, err := handleProductSelect(session, luckin.DraftService{}, nil, nil, nil, nil)(context.Background(), testActionContext(map[string]any{
		cardactionproto.LuckinProductIDField: "5293",
		cardactionproto.LuckinSkuCodeField:   "SP-1",
	}))
	if err == nil {
		t.Fatalf("expected error when shop missing")
	}
}

func TestHandleBindTokenStoresPersonalScope(t *testing.T) {
	writer := &memCredentialWriter{}
	resp, err := handleBindToken(writer)(context.Background(), testActionContextWithForm(
		map[string]any{cardactionproto.ActionField: cardactionproto.ActionLuckinBindToken},
		map[string]any{cardactionproto.LuckinTokenFormField: "token-xyz"},
	))
	if err != nil {
		t.Fatalf("handleBindToken error = %v", err)
	}
	if resp == nil || resp.Toast == nil {
		t.Fatalf("expected toast")
	}
	if !writer.upserted || writer.lookup.Scope.Type != luckin.ScopePersonal {
		t.Fatalf("personal scope not stored: %+v", writer.lookup)
	}
	if writer.token != "token-xyz" {
		t.Fatalf("token mismatch: %q", writer.token)
	}
}

func TestHandleBindTokenAlwaysPersonalScope(t *testing.T) {
	writer := &memCredentialWriter{}
	_, err := handleBindToken(writer)(context.Background(), testActionContextWithForm(
		map[string]any{cardactionproto.ActionField: cardactionproto.ActionLuckinBindToken},
		map[string]any{
			cardactionproto.LuckinTokenFormField: "token-grp",
			cardactionproto.LuckinScopeFormField: string(luckin.ScopeChat),
		},
	))
	if err != nil {
		t.Fatalf("handleBindToken error = %v", err)
	}
	if writer.lookup.Scope.Type != luckin.ScopePersonal {
		t.Fatalf("scope should be forced personal: %+v", writer.lookup)
	}
}

func TestHandleUnbindToken(t *testing.T) {
	writer := &memCredentialWriter{deleted: true}
	resp, err := handleUnbindToken(writer)(context.Background(), testActionContext(map[string]any{}))
	if err != nil {
		t.Fatalf("handleUnbindToken error = %v", err)
	}
	if !writer.deleteCalled {
		t.Fatalf("delete not called")
	}
	if resp == nil || resp.Toast == nil {
		t.Fatalf("expected toast")
	}
}

func TestHandleProductQueryValidatesAndReturnsTask(t *testing.T) {
	session := &memSessionStore{}

	// 无门店时返回错误，不产生异步任务。
	if _, err := handleProductQuery(session, luckin.DraftService{}, nil, nil)(context.Background(), testActionContextWithForm(
		map[string]any{cardactionproto.ActionField: cardactionproto.ActionLuckinProductQuery},
		map[string]any{cardactionproto.LuckinQueryFormField: "生椰拿铁"},
	)); err == nil {
		t.Fatalf("expected error when shop missing")
	}

	// 有门店但关键词为空时报错。
	session.SetShop(context.Background(), luckin.SessionKey{}, luckin.ShopSelection{DeptID: 1, DeptName: "门店A"})
	if _, err := handleProductQuery(session, luckin.DraftService{}, nil, nil)(context.Background(), testActionContextWithForm(
		map[string]any{cardactionproto.ActionField: cardactionproto.ActionLuckinProductQuery},
		map[string]any{cardactionproto.LuckinQueryFormField: ""},
	)); err == nil {
		t.Fatalf("expected error when query empty")
	}

	// 正常情况下返回一个非空异步任务。
	task, err := handleProductQuery(session, luckin.DraftService{}, nil, nil)(context.Background(), testActionContextWithForm(
		map[string]any{cardactionproto.ActionField: cardactionproto.ActionLuckinProductQuery},
		map[string]any{cardactionproto.LuckinQueryFormField: "生椰拿铁"},
	))
	if err != nil {
		t.Fatalf("handleProductQuery error = %v", err)
	}
	if task == nil {
		t.Fatalf("expected async task")
	}
}

func testActionContextWithForm(value, form map[string]any) *appcardaction.Context {
	ctx := testActionContext(value)
	ctx.Action.FormValue = form
	return ctx
}

type memSessionStore struct {
	shop luckin.ShopSelection
	ok   bool
}

func (s *memSessionStore) GetShop(ctx context.Context, key luckin.SessionKey) (luckin.ShopSelection, bool) {
	return s.shop, s.ok
}

func (s *memSessionStore) SetShop(ctx context.Context, key luckin.SessionKey, shop luckin.ShopSelection) {
	s.shop = shop
	s.ok = true
}

func (s *memSessionStore) ClearShop(ctx context.Context, key luckin.SessionKey) {
	s.ok = false
}

type memCredentialWriter struct {
	upserted     bool
	deleteCalled bool
	deleted      bool
	lookup       luckin.CredentialLookup
	token        string
}

func (w *memCredentialWriter) UpsertToken(ctx context.Context, lookup luckin.CredentialLookup, token, actorOpenID string) error {
	w.upserted = true
	w.lookup = lookup
	w.token = token
	return nil
}

func (w *memCredentialWriter) DeleteToken(ctx context.Context, lookup luckin.CredentialLookup, actorOpenID string) (bool, error) {
	w.deleteCalled = true
	w.lookup = lookup
	return w.deleted, nil
}

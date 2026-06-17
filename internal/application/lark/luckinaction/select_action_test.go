package luckinaction

import (
	"context"
	"encoding/json"
	"strings"
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
	if len(session.recent) != 1 || session.recent[0].DeptID != 245062453 {
		t.Fatalf("recent shop not stored: %+v", session.recent)
	}
}

func TestHandleProductSelectRequiresShop(t *testing.T) {
	session := &memSessionStore{}
	task, err := handleProductSelect(session, luckin.DraftService{}, nil, nil)(context.Background(), testActionContext(map[string]any{
		cardactionproto.LuckinProductIDField: "5293",
		cardactionproto.LuckinSkuCodeField:   "SP-1",
	}))
	if err != nil {
		t.Fatalf("handleProductSelect error = %v", err)
	}
	if task == nil {
		t.Fatalf("expected patch task when shop missing")
	}
}

func TestHandleBindTokenStoresPersonalScope(t *testing.T) {
	writer := &memCredentialWriter{}
	resp, err := handleBindToken(writer, nil)(context.Background(), testActionContextWithForm(
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
	_, err := handleBindToken(writer, nil)(context.Background(), testActionContextWithForm(
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

	// 无门店时返回过期/未选择卡片刷新任务。
	task, err := handleProductQuery(session, luckin.DraftService{}, nil, nil)(context.Background(), testActionContextWithForm(
		map[string]any{cardactionproto.ActionField: cardactionproto.ActionLuckinProductQuery},
		map[string]any{cardactionproto.LuckinQueryFormField: "生椰拿铁"},
	))
	if err != nil {
		t.Fatalf("handleProductQuery error = %v", err)
	}
	if task == nil {
		t.Fatalf("expected patch task when shop missing")
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
	task, err = handleProductQuery(session, luckin.DraftService{}, nil, nil)(context.Background(), testActionContextWithForm(
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

func TestShopStartCardUsesRecentShops(t *testing.T) {
	session := &memSessionStore{
		recent: []luckin.ShopSelection{{DeptID: 245062453, DeptName: "AI点单专用", Address: "北京安贞", Longitude: 116.39, Latitude: 39.98}},
	}
	card := sessionMissingCard(context.Background(), session, testActionContext(map[string]any{}))
	text := mustMarshalForTest(card)
	if !strings.Contains(text, "AI点单专用") || !strings.Contains(text, cardactionproto.LuckinLocationFormField) {
		t.Fatalf("start card missing recent shop or location input: %s", text)
	}
}

func mustMarshalForTest(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func testActionContextWithForm(value, form map[string]any) *appcardaction.Context {
	ctx := testActionContext(value)
	ctx.Action.FormValue = form
	return ctx
}

type memSessionStore struct {
	shop   luckin.ShopSelection
	ok     bool
	cart   luckin.Cart
	cok    bool
	seen   bool
	recent []luckin.ShopSelection
}

func (s *memSessionStore) GetShop(ctx context.Context, key luckin.SessionKey) (luckin.ShopSelection, bool) {
	return s.shop, s.ok
}

func (s *memSessionStore) SetShop(ctx context.Context, key luckin.SessionKey, shop luckin.ShopSelection) {
	s.shop = shop
	s.ok = true
	s.seen = true
	s.recent = append([]luckin.ShopSelection{shop}, s.recent...)
}

func (s *memSessionStore) ClearShop(ctx context.Context, key luckin.SessionKey) {
	s.ok = false
}

func (s *memSessionStore) GetRecentShops(ctx context.Context, key luckin.SessionKey, limit int) []luckin.ShopSelection {
	if limit > 0 && len(s.recent) > limit {
		return s.recent[:limit]
	}
	return s.recent
}

func (s *memSessionStore) GetCart(ctx context.Context, key luckin.SessionKey) (luckin.Cart, bool) {
	return s.cart, s.cok
}

func (s *memSessionStore) SetCart(ctx context.Context, key luckin.SessionKey, cart luckin.Cart) {
	s.cart = cart
	s.cok = true
	s.seen = true
}

func (s *memSessionStore) ClearCart(ctx context.Context, key luckin.SessionKey) {
	s.cok = false
}

func (s *memSessionStore) Seen(ctx context.Context, key luckin.SessionKey) bool {
	return s.seen
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

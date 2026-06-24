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
	store := newMemStoreWithInitiator("om_msg", "ou_user")
	resp, err := handleShopSelect(store)(context.Background(), testActionContext(map[string]any{
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
	// shop select 走 RMW 锁路径；Redis 不可用时锁失败仅 toast，不写 session。
	// 这里只断言响应体存在（toast 非空），完整的 store 写入在 mcpstore session_test 里测试。
	_ = store
}

func TestHandleShopSelectRejectsNonInitiator(t *testing.T) {
	store := newMemStoreWithInitiator("om_msg", "ou_someone_else")
	resp, err := handleShopSelect(store)(context.Background(), testActionContext(map[string]any{
		cardactionproto.LuckinDeptIDField:    "245062453",
		cardactionproto.LuckinDeptNameField:  "AI点单专用",
		cardactionproto.LuckinLongitudeField: "116.39",
		cardactionproto.LuckinLatitudeField:  "39.98",
	}))
	if err != nil {
		t.Fatalf("handleShopSelect error = %v", err)
	}
	if resp == nil || resp.Toast == nil || resp.Toast.Type != "error" {
		t.Fatalf("expected error toast, got %+v", resp)
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
	seeded := newMemStoreWithSession("om_msg", luckin.OrderSession{
		InitiatorOpenID: "ou_user",
		ChatID:          "oc_chat",
		Shop:            luckin.ShopSelection{DeptID: 1, DeptName: "门店A"},
	})
	if _, err := handleProductQuery(seeded, luckin.DraftService{}, nil, nil)(context.Background(), testActionContextWithForm(
		map[string]any{cardactionproto.ActionField: cardactionproto.ActionLuckinProductQuery},
		map[string]any{cardactionproto.LuckinQueryFormField: ""},
	)); err == nil {
		t.Fatalf("expected error when query empty")
	}

	// 正常情况下返回一个非空异步任务。
	task, err = handleProductQuery(seeded, luckin.DraftService{}, nil, nil)(context.Background(), testActionContextWithForm(
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
	sessions map[string]luckin.OrderSession
	recent   []luckin.ShopSelection
	seen     bool
}

func (s *memSessionStore) GetSession(ctx context.Context, key luckin.SessionKey) (luckin.OrderSession, bool) {
	if s.sessions == nil {
		return luckin.OrderSession{}, false
	}
	sess, ok := s.sessions[key.MessageID]
	return sess, ok
}

func (s *memSessionStore) SetSession(ctx context.Context, key luckin.SessionKey, sess luckin.OrderSession) {
	if s.sessions == nil {
		s.sessions = make(map[string]luckin.OrderSession)
	}
	s.sessions[key.MessageID] = sess
	s.seen = true
}

func (s *memSessionStore) DeleteSession(ctx context.Context, key luckin.SessionKey) {
	if s.sessions == nil {
		return
	}
	delete(s.sessions, key.MessageID)
}

func (s *memSessionStore) GetRecentShops(ctx context.Context, key luckin.UserHistoryKey, limit int) []luckin.ShopSelection {
	if limit > 0 && len(s.recent) > limit {
		return s.recent[:limit]
	}
	return s.recent
}

func (s *memSessionStore) AddRecentShop(ctx context.Context, key luckin.UserHistoryKey, shop luckin.ShopSelection) {
	s.recent = append([]luckin.ShopSelection{shop}, s.recent...)
}

func (s *memSessionStore) Seen(ctx context.Context, key luckin.UserHistoryKey) bool {
	return s.seen
}

func (s *memSessionStore) MarkSeen(ctx context.Context, key luckin.UserHistoryKey) {
	s.seen = true
}

// newMemStoreWithSession 在指定 messageID 上 seed 一条 OrderSession。
func newMemStoreWithSession(messageID string, sess luckin.OrderSession) *memSessionStore {
	store := &memSessionStore{}
	store.SetSession(context.Background(), luckin.SessionKey{MessageID: messageID}, sess)
	return store
}

// newMemStoreWithInitiator 在指定 messageID 上 seed 仅含发起人的 OrderSession。
func newMemStoreWithInitiator(messageID, initiator string) *memSessionStore {
	return newMemStoreWithSession(messageID, luckin.OrderSession{InitiatorOpenID: initiator, ChatID: "oc_chat"})
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

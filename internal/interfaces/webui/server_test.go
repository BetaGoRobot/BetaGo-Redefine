package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/bytedance/sonic"
)

// fakeConfigManager 是 ConfigManager 的内存替身，遵循显式依赖注入约束，
// 不依赖真实数据库或全局单例。
type fakeConfigManager struct {
	features []appconfig.Feature
	blocked  map[string]bool   // feature -> blocked
	values   map[string]string // key -> value
}

func newFakeConfigManager() *fakeConfigManager {
	return &fakeConfigManager{
		features: []appconfig.Feature{
			{Name: "repeat", Description: "复读", Category: "message", DefaultEnabled: true},
			{Name: "react", Description: "回应", Category: "message", DefaultEnabled: true},
		},
		blocked: map[string]bool{},
		values:  map[string]string{},
	}
}

func (f *fakeConfigManager) GetAllFeatures() []appconfig.Feature { return f.features }

func (f *fakeConfigManager) IsFeatureEnabled(_ context.Context, feature string, defaultEnabled bool, _, _ string) bool {
	if f.blocked[feature] {
		return false
	}
	return defaultEnabled
}

func (f *fakeConfigManager) BlockFeature(_ context.Context, feature string, _ appconfig.ConfigScope, _, _, _ string) error {
	f.blocked[feature] = true
	return nil
}

func (f *fakeConfigManager) UnblockFeature(_ context.Context, feature string, _ appconfig.ConfigScope, _, _ string) error {
	delete(f.blocked, feature)
	return nil
}

func (f *fakeConfigManager) GetString(_ context.Context, key appconfig.ConfigKey, _, _ string) string {
	return f.values[string(key)]
}

func (f *fakeConfigManager) GetInt(_ context.Context, key appconfig.ConfigKey, _, _ string) int {
	v := f.values[string(key)]
	switch v {
	case "":
		return 0
	default:
		n := 0
		for _, c := range v {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int(c-'0')
		}
		return n
	}
}

func (f *fakeConfigManager) GetBool(_ context.Context, key appconfig.ConfigKey, _, _ string) bool {
	return f.values[string(key)] == "true"
}

func (f *fakeConfigManager) SetString(_ context.Context, key appconfig.ConfigKey, _ appconfig.ConfigScope, _, _, value string) error {
	f.values[string(key)] = value
	return nil
}

func (f *fakeConfigManager) DeleteConfig(_ context.Context, key appconfig.ConfigKey, _ appconfig.ConfigScope, _, _ string) error {
	delete(f.values, string(key))
	return nil
}

// fakeChatService 是 ChatService 的内存替身。
type fakeChatService struct {
	chats []ChatSummary
}

type fakeEvaluationWorkbench struct {
	query           EvaluationListQuery
	page            EvaluationEpisodePage
	detail          *EvaluationEpisodeDetail
	err             error
	judgmentRequest HumanJudgmentRequest
}

func (f *fakeEvaluationWorkbench) AppendHumanJudgment(
	_ context.Context,
	_, _, _ string,
	request HumanJudgmentRequest,
) (*EvaluationJudgmentView, error) {
	f.judgmentRequest = request
	if f.err != nil {
		return nil, f.err
	}
	return &EvaluationJudgmentView{
		ID: "judgment-1", Source: "human", Version: 1,
		Winner: request.Winner,
	}, nil
}

func (f *fakeEvaluationWorkbench) ListEpisodes(
	_ context.Context,
	query EvaluationListQuery,
) (EvaluationEpisodePage, error) {
	f.query = query
	return f.page, f.err
}

func (f *fakeEvaluationWorkbench) GetEpisode(
	context.Context,
	string,
	string,
	string,
) (*EvaluationEpisodeDetail, error) {
	return f.detail, f.err
}

func (f *fakeChatService) ListChats(context.Context) ([]ChatSummary, error) { return f.chats, nil }

func (f *fakeChatService) GetChat(_ context.Context, chatID string) (*ChatDetail, error) {
	for _, c := range f.chats {
		if c.ChatID == chatID {
			return &ChatDetail{ChatSummary: c, MemberCount: 3}, nil
		}
	}
	return &ChatDetail{ChatSummary: ChatSummary{ChatID: chatID}}, nil
}

func newTestServer(t *testing.T, token string) (*Server, *fakeConfigManager, *fakeChatService) {
	t.Helper()
	cfg := newFakeConfigManager()
	chats := &fakeChatService{chats: []ChatSummary{
		{ChatID: "oc_1", Name: "群1", Avatar: "https://avatar/1"},
		{ChatID: "oc_2", Name: "群2", Avatar: "https://avatar/2"},
	}}
	srv := NewServer(Options{
		Config:        &infraConfig.WebUIConfig{AuthToken: token},
		ConfigManager: cfg,
		ChatService:   chats,
		MemberCount: func(_ context.Context, chatID string) (int, error) {
			return 3, nil
		},
		MemberList: func(_ context.Context, chatID string) ([]ChatMember, error) {
			return []ChatMember{{OpenID: "ou_a", Name: "Alice"}, {OpenID: "ou_b", Name: "Bob"}}, nil
		},
		MessageStats: func(context.Context, string, time.Time) (int, error) {
			return 42, nil
		},
		Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
	}, nil)
	return srv, cfg, chats
}

func TestListChats(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/chats", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Items []ChatSummary `json:"items"`
		Total int           `json:"total"`
	}
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 2 || resp.Items[0].Avatar != "https://avatar/1" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestEvaluationSensitiveReadsRequireConfiguredBearerAuth(t *testing.T) {
	for _, test := range []struct {
		name, token, authorization string
		want                       int
	}{
		{name: "auth not configured", want: http.StatusServiceUnavailable},
		{name: "missing bearer", token: "secret", want: http.StatusUnauthorized},
		{
			name: "valid bearer", token: "secret",
			authorization: "Bearer secret", want: http.StatusOK,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			workbench := &fakeEvaluationWorkbench{}
			srv := NewServer(Options{
				Config: &infraConfig.WebUIConfig{AuthToken: test.token},
				AppID:  "app-1", BotOpenID: "bot-1",
				EvaluationWorkbench: workbench,
				Now: func() time.Time {
					return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
				},
			}, nil)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/evaluations", nil)
			if test.authorization != "" {
				req.Header.Set("Authorization", test.authorization)
			}
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			if test.want == http.StatusOK &&
				(workbench.query.AppID != "app-1" ||
					workbench.query.BotOpenID != "bot-1" ||
					workbench.query.Limit != 50) {
				t.Fatalf("query = %#v", workbench.query)
			}
		})
	}
}

func TestEvaluationListRejectsInvalidBounds(t *testing.T) {
	srv := NewServer(Options{
		Config: &infraConfig.WebUIConfig{AuthToken: "secret"},
		AppID:  "app-1", BotOpenID: "bot-1",
		EvaluationWorkbench: &fakeEvaluationWorkbench{},
		Now: func() time.Time {
			return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
		},
	}, nil)
	for _, path := range []string{
		"/api/evaluations?limit=101",
		"/api/evaluations?from=bad-time",
		"/api/evaluations?cursor=bad-cursor",
		"/api/evaluations?needs_review=maybe",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestEvaluationDetailReturnsBotScopedBundle(t *testing.T) {
	workbench := &fakeEvaluationWorkbench{
		detail: &EvaluationEpisodeDetail{
			Episode: EvaluationEpisodeSummary{ID: "episode-1"},
		},
	}
	srv := NewServer(Options{
		Config: &infraConfig.WebUIConfig{AuthToken: "secret"},
		AppID:  "app-1", BotOpenID: "bot-1",
		EvaluationWorkbench: workbench,
	}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/evaluations/episode-1",
		nil,
	)
	req.Header.Set("Authorization", "Bearer secret")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"id":"episode-1"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	workbench.detail = nil
	workbench.err = ErrEvaluationNotFound
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not found status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEvaluationHumanJudgmentRequiresValidAuthenticatedDocument(t *testing.T) {
	workbench := &fakeEvaluationWorkbench{}
	srv := NewServer(Options{
		Config: &infraConfig.WebUIConfig{AuthToken: "secret"},
		AppID:  "app-1", BotOpenID: "bot-1",
		EvaluationWorkbench: workbench,
	}, nil)
	valid := `{
		"evaluator_id":"reviewer-1",
		"winner":"candidate",
		"scores":{"response_relevance":9},
		"problem_tags":["control_missed_context"],
		"rationale":"Candidate used the relevant constraint.",
		"confidence":90,
		"needs_review":false
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/evaluations/episode-1/judgments",
		strings.NewReader(valid),
	)
	req.Header.Set("Authorization", "Bearer secret")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated ||
		workbench.judgmentRequest.Winner != "candidate" ||
		workbench.judgmentRequest.EvaluatorID != "reviewer-1" {
		t.Fatalf(
			"status=%d body=%s request=%#v",
			rec.Code,
			rec.Body.String(),
			workbench.judgmentRequest,
		)
	}

	for _, invalid := range []string{
		`{"evaluator_id":"reviewer","winner":"bad","scores":{},"rationale":"x","confidence":50}`,
		`{"evaluator_id":"reviewer","winner":"tie","scores":[],"rationale":"x","confidence":50}`,
		`{"evaluator_id":"reviewer","winner":"tie","scores":{},"rationale":"","confidence":50}`,
		`{"evaluator_id":"reviewer","winner":"tie","scores":{},"rationale":"x","confidence":101}`,
		`{"evaluator_id":"reviewer","winner":"tie","scores":{},"rationale":"x","confidence":50,"unknown":true}`,
	} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(
			http.MethodPost,
			"/api/evaluations/episode-1/judgments",
			strings.NewReader(invalid),
		)
		req.Header.Set("Authorization", "Bearer secret")
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid=%s status=%d body=%s", invalid, rec.Code, rec.Body.String())
		}
	}
}

func TestListChatsWithMetrics(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/chats?metrics=1&window=7d", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Items []ChatSummary `json:"items"`
	}
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	m := resp.Items[0].Metrics
	if m == nil {
		t.Fatalf("expected metrics populated")
	}
	// DB 为 nil，token 总量为 0；成员/发言量来自注入的 fake。
	if m.MemberCount != 3 || m.RecentMessages != 42 || m.WindowDays != 7 {
		t.Fatalf("unexpected metrics: %+v", m)
	}
}

func TestListMembers(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/chats/oc_1/members", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Items []ChatMember `json:"items"`
		Total int          `json:"total"`
	}
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 2 || resp.Items[0].Name != "Alice" {
		t.Fatalf("unexpected members: %+v", resp)
	}
}

func TestAuthRequiredForWrites(t *testing.T) {
	srv, _, _ := newTestServer(t, "secret")
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"enabled":false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/chats/oc_1/features/repeat", body)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	// 带正确 token 应放行。
	rec = httptest.NewRecorder()
	body = strings.NewReader(`{"enabled":false}`)
	req = httptest.NewRequest(http.MethodPut, "/api/chats/oc_1/features/repeat", body)
	req.Header.Set("Authorization", "Bearer secret")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestSetAndListFeature(t *testing.T) {
	srv, cfg, _ := newTestServer(t, "")
	// 禁用 repeat。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/chats/oc_1/features/repeat", strings.NewReader(`{"enabled":false}`))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set feature failed: %d", rec.Code)
	}
	if !cfg.blocked["repeat"] {
		t.Fatalf("repeat should be blocked")
	}

	// 列表中 repeat.enabled 应为 false。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/chats/oc_1/features", nil)
	srv.Handler().ServeHTTP(rec, req)
	var resp struct {
		Items []FeatureView `json:"items"`
	}
	_ = sonic.Unmarshal(rec.Body.Bytes(), &resp)
	for _, f := range resp.Items {
		if f.Name == "repeat" && f.Enabled {
			t.Fatalf("repeat should be disabled in list")
		}
	}
}

func TestSetConfigValidation(t *testing.T) {
	srv, cfg, _ := newTestServer(t, "")

	// 合法 int 设置。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/chats/oc_1/configs/reaction_default_rate", strings.NewReader(`{"value":"30"}`))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if cfg.values["reaction_default_rate"] != "30" {
		t.Fatalf("value not stored: %v", cfg.values)
	}

	// 越界 int 应被拒。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/chats/oc_1/configs/reaction_default_rate", strings.NewReader(`{"value":"300"}`))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for out-of-range, got %d", rec.Code)
	}

	// 未知 key 应被拒。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/chats/oc_1/configs/not_a_key", strings.NewReader(`{"value":"x"}`))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown key, got %d", rec.Code)
	}
}

func TestStatsGracefulWithoutDB(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/chats/oc_1/stats?window=7d", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp StatsResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token.WindowDays != 7 {
		t.Fatalf("expected window 7, got %d", resp.Token.WindowDays)
	}
	if !resp.Messages.Available || resp.Messages.RecentCount != 42 {
		t.Fatalf("unexpected message stats: %+v", resp.Messages)
	}
}

func TestParseWindowDays(t *testing.T) {
	cases := map[string]int{
		"":     defaultStatsWindowDays,
		"7d":   7,
		"30d":  30,
		"48h":  2,
		"10":   10,
		"9999": maxStatsWindowDays,
		"0":    1,
	}
	for in, want := range cases {
		if got := parseWindowDays(in); got != want {
			t.Errorf("parseWindowDays(%q)=%d want %d", in, got, want)
		}
	}
}

func TestCORSPreflight(t *testing.T) {
	srv, _, _ := newTestServer(t, "secret")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/chats", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for preflight, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatalf("expected CORS header")
	}
}

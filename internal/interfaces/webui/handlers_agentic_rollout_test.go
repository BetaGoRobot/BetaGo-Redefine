package webui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/agenticrollout"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/bytedance/sonic"
)

type fakeAgenticRolloutService struct {
	resolveChatFn  func(context.Context, string) (agenticrollout.ChatState, error)
	resolveChatsFn func(context.Context, []string) ([]agenticrollout.ChatState, error)
	applyFn        func(context.Context, agenticrollout.BatchRequest) (agenticrollout.BatchResult, error)
}

func (f *fakeAgenticRolloutService) ResolveChat(
	ctx context.Context,
	chatID string,
) (agenticrollout.ChatState, error) {
	if f.resolveChatFn == nil {
		return agenticrollout.ChatState{}, errors.New("unexpected ResolveChat call")
	}
	return f.resolveChatFn(ctx, chatID)
}

func (f *fakeAgenticRolloutService) ResolveChats(
	ctx context.Context,
	chatIDs []string,
) ([]agenticrollout.ChatState, error) {
	if f.resolveChatsFn == nil {
		return nil, errors.New("unexpected ResolveChats call")
	}
	return f.resolveChatsFn(ctx, chatIDs)
}

func (f *fakeAgenticRolloutService) Apply(
	ctx context.Context,
	request agenticrollout.BatchRequest,
) (agenticrollout.BatchResult, error) {
	if f.applyFn == nil {
		return agenticrollout.BatchResult{}, errors.New("unexpected Apply call")
	}
	return f.applyFn(ctx, request)
}

func newAgenticRolloutTestServer(
	token string,
	service AgenticRolloutService,
) *Server {
	return NewServer(Options{
		Config:          &infraConfig.WebUIConfig{AuthToken: token},
		AgenticRollouts: service,
		RobotName:       "Test Bot",
		BotID:           "lark:cli_test",
		Instance:        "cli_test",
		Now:             testNow,
	}, nil)
}

func testNow() time.Time {
	return time.Unix(1_700_000_000, 0)
}

func rolloutState(chatID, revision string) agenticrollout.ChatState {
	return agenticrollout.ChatState{
		ChatID:   chatID,
		Revision: revision,
		Capabilities: []agenticrollout.CapabilityState{{
			Key:       agenticrollout.ConversationRuntime,
			Label:     "Conversation Runtime",
			Override:  agenticrollout.OverrideInherit,
			Baseline:  false,
			Effective: false,
			Source:    agenticrollout.SourceDefault,
			Available: true,
		}},
	}
}

func TestGetAgenticRolloutUsesServerBoundBotIdentity(t *testing.T) {
	service := &fakeAgenticRolloutService{
		resolveChatFn: func(_ context.Context, chatID string) (agenticrollout.ChatState, error) {
			if chatID != "oc_1" {
				t.Fatalf("unexpected chat id %q", chatID)
			}
			return rolloutState(chatID, "rev-1"), nil
		},
	}
	srv := newAgenticRolloutTestServer("", service)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/chats/oc_1/agentic-rollout", nil)

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Bot      AgenticBotView `json:"bot"`
		ChatID   string         `json:"chat_id"`
		Revision string         `json:"revision"`
	}
	if err := sonic.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Bot.ID != "lark:cli_test" || response.Bot.Name != "Test Bot" {
		t.Fatalf("unexpected bound bot: %+v", response.Bot)
	}
	if response.ChatID != "oc_1" || response.Revision != "rev-1" {
		t.Fatalf("unexpected state: %+v", response)
	}
}

func TestListAgenticRolloutsPreservesRequestedOrder(t *testing.T) {
	service := &fakeAgenticRolloutService{
		resolveChatsFn: func(_ context.Context, chatIDs []string) ([]agenticrollout.ChatState, error) {
			if fmt.Sprint(chatIDs) != "[oc_2 oc_1]" {
				t.Fatalf("unexpected chat ids: %v", chatIDs)
			}
			return []agenticrollout.ChatState{
				rolloutState("oc_2", "rev-2"),
				rolloutState("oc_1", "rev-1"),
			}, nil
		},
	}
	srv := newAgenticRolloutTestServer("", service)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agentic-rollouts?chat_ids=oc_2,oc_1", nil)

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Items []struct {
			Bot    AgenticBotView `json:"bot"`
			ChatID string         `json:"chat_id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := sonic.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Total != 2 ||
		response.Items[0].ChatID != "oc_2" ||
		response.Items[1].ChatID != "oc_1" {
		t.Fatalf("unexpected response order: %+v", response)
	}
	for _, item := range response.Items {
		if item.Bot.ID != "lark:cli_test" {
			t.Fatalf("unexpected bot identity: %+v", item.Bot)
		}
	}
}

func TestListAgenticRolloutsRejectsMoreThanReadLimit(t *testing.T) {
	ids := make([]string, 101)
	for index := range ids {
		ids[index] = fmt.Sprintf("oc_%03d", index)
	}
	srv := newAgenticRolloutTestServer("", &fakeAgenticRolloutService{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/agentic-rollouts?chat_ids="+strings.Join(ids, ","),
		nil,
	)

	srv.Handler().ServeHTTP(rec, req)

	assertRolloutError(t, rec, http.StatusBadRequest, "invalid_request")
}

func TestPutAgenticRolloutMapsSingleChatMutation(t *testing.T) {
	var received agenticrollout.BatchRequest
	service := &fakeAgenticRolloutService{
		applyFn: func(_ context.Context, request agenticrollout.BatchRequest) (agenticrollout.BatchResult, error) {
			received = request
			before := rolloutState("oc_1", "rev-1")
			after := rolloutState("oc_1", "rev-2")
			after.Capabilities[0].Override = agenticrollout.OverrideEnabled
			after.Capabilities[0].Effective = true
			return agenticrollout.BatchResult{Items: []agenticrollout.BatchItem{{
				ChatID: "oc_1",
				Before: before,
				After:  after,
			}}}, nil
		},
	}
	srv := newAgenticRolloutTestServer("secret", service)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/chats/oc_1/agentic-rollout",
		strings.NewReader(`{
			"expected_revision":"rev-1",
			"changes":{"conversation_runtime":"enabled"}
		}`),
	)
	req.Header.Set("Authorization", "Bearer secret")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if fmt.Sprint(received.ChatIDs) != "[oc_1]" ||
		received.ExpectedRevisions["oc_1"] != "rev-1" ||
		received.Changes[agenticrollout.ConversationRuntime] != agenticrollout.OverrideEnabled ||
		received.DryRun {
		t.Fatalf("unexpected mapped request: %+v", received)
	}
	var response struct {
		Bot   AgenticBotView `json:"bot"`
		Items []struct {
			Before AgenticRolloutView `json:"before"`
			After  AgenticRolloutView `json:"after"`
		} `json:"items"`
	}
	if err := sonic.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Bot.ID != "lark:cli_test" {
		t.Fatalf("unexpected bot: %+v", response.Bot)
	}
	if len(response.Items) != 1 ||
		response.Items[0].Before.Bot.ID != "lark:cli_test" ||
		response.Items[0].After.Bot.ID != "lark:cli_test" {
		t.Fatalf("batch item lost bot identity: %+v", response.Items)
	}
}

func TestAgenticRolloutWritesRequireAuthAndRejectTenantFields(t *testing.T) {
	calls := 0
	service := &fakeAgenticRolloutService{
		applyFn: func(context.Context, agenticrollout.BatchRequest) (agenticrollout.BatchResult, error) {
			calls++
			return agenticrollout.BatchResult{}, nil
		},
	}
	srv := newAgenticRolloutTestServer("secret", service)

	unauthorized := httptest.NewRecorder()
	unauthorizedReq := httptest.NewRequest(
		http.MethodPost,
		"/api/agentic-rollouts/batch",
		strings.NewReader(`{"chat_ids":["oc_1"],"expected_revisions":{"oc_1":"rev-1"},"changes":{"agent_card":"enabled"}}`),
	)
	srv.Handler().ServeHTTP(unauthorized, unauthorizedReq)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", unauthorized.Code, unauthorized.Body.String())
	}

	unknown := httptest.NewRecorder()
	unknownReq := httptest.NewRequest(
		http.MethodPost,
		"/api/agentic-rollouts/batch",
		strings.NewReader(`{
			"chat_ids":["oc_1"],
			"expected_revisions":{"oc_1":"rev-1"},
			"changes":{"agent_card":"enabled"},
			"bot_id":"another-bot"
		}`),
	)
	unknownReq.Header.Set("Authorization", "Bearer secret")
	srv.Handler().ServeHTTP(unknown, unknownReq)
	assertRolloutError(t, unknown, http.StatusBadRequest, "invalid_request")
	if calls != 0 {
		t.Fatalf("rollout service called %d times", calls)
	}
}

func TestBatchAgenticRolloutSupportsDryRunAndWriteLimit(t *testing.T) {
	var received agenticrollout.BatchRequest
	service := &fakeAgenticRolloutService{
		applyFn: func(_ context.Context, request agenticrollout.BatchRequest) (agenticrollout.BatchResult, error) {
			received = request
			return agenticrollout.BatchResult{DryRun: true}, nil
		},
	}
	srv := newAgenticRolloutTestServer("", service)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agentic-rollouts/batch",
		strings.NewReader(`{
			"chat_ids":["oc_1","oc_2"],
			"expected_revisions":{"oc_1":"rev-1","oc_2":"rev-2"},
			"changes":{"parallel_evaluation":"disabled"},
			"dry_run":true
		}`),
	)

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !received.DryRun || len(received.ChatIDs) != 2 {
		t.Fatalf("unexpected request: %+v", received)
	}

	tooManyIDs := make([]string, 201)
	revisions := make(map[string]string, len(tooManyIDs))
	for index := range tooManyIDs {
		tooManyIDs[index] = fmt.Sprintf("oc_%03d", index)
		revisions[tooManyIDs[index]] = "rev"
	}
	body, err := sonic.Marshal(agenticrollout.BatchRequest{
		ChatIDs:           tooManyIDs,
		ExpectedRevisions: revisions,
		Changes:           agenticrollout.ChangeSet{agenticrollout.AgentCard: agenticrollout.OverrideDisabled},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	limited := httptest.NewRecorder()
	limitedReq := httptest.NewRequest(http.MethodPost, "/api/agentic-rollouts/batch", strings.NewReader(string(body)))
	srv.Handler().ServeHTTP(limited, limitedReq)
	assertRolloutError(t, limited, http.StatusBadRequest, "invalid_request")
}

func TestAgenticRolloutMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid", err: agenticrollout.ErrInvalidRequest, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "stale", err: agenticrollout.ErrStaleRevision, status: http.StatusConflict, code: "stale_revision"},
		{name: "unavailable", err: agenticrollout.ErrUnavailable, status: http.StatusUnprocessableEntity, code: "capability_unavailable"},
		{name: "persistence", err: agenticrollout.ErrPersistence, status: http.StatusServiceUnavailable, code: "persistence_unavailable"},
		{name: "unexpected", err: errors.New("boom"), status: http.StatusInternalServerError, code: "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeAgenticRolloutService{
				applyFn: func(context.Context, agenticrollout.BatchRequest) (agenticrollout.BatchResult, error) {
					return agenticrollout.BatchResult{}, test.err
				},
			}
			srv := newAgenticRolloutTestServer("", service)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(
				http.MethodPost,
				"/api/agentic-rollouts/batch",
				strings.NewReader(`{
					"chat_ids":["oc_1"],
					"expected_revisions":{"oc_1":"rev-1"},
					"changes":{"agent_card":"disabled"}
				}`),
			)

			srv.Handler().ServeHTTP(rec, req)

			assertRolloutError(t, rec, test.status, test.code)
		})
	}
}

func assertRolloutError(
	t *testing.T,
	rec *httptest.ResponseRecorder,
	status int,
	code string,
) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("expected %d, got %d: %s", status, rec.Code, rec.Body.String())
	}
	var response struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := sonic.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Code != code || response.Error == "" {
		t.Fatalf("unexpected error response: %+v", response)
	}
}

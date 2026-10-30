package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type factsReader struct {
	calls int
	scope domain.Scope
	id    string
}

func (f *factsReader) TaskFacts(_ context.Context, s domain.Scope, id string) (json.RawMessage, error) {
	f.calls++
	f.scope = s
	f.id = id
	return json.RawMessage(`{"task":"synthetic"}`), nil
}
func TestTaskAPIAuthenticatesBeforeReadingAndFixesAppScope(t *testing.T) {
	f := &factsReader{}
	c := config.Config{App: config.App{APIKey: "read-secret", TenantID: "tenant", Visibility: "group", AllowedChatIDs: []string{"allowed"}}, Lark: config.Lark{AppID: "app"}}
	h := taskAPI(c, f)
	request := func(token, tenant, chat string) int {
		r := httptest.NewRequest("GET", "/api/tasks/task_123", nil)
		r.Header.Set("Authorization", token)
		r.Header.Set("X-Agent-Tenant-Key", tenant)
		r.Header.Set("X-Agent-Chat-ID", chat)
		r.Header.Set("X-Agent-App-ID", "spoofed")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if request("", "tenant", "allowed") != 401 || f.calls != 0 {
		t.Fatal("anonymous read reached storage")
	}
	if request("Bearer read-secret", "other", "allowed") != 403 || request("Bearer read-secret", "tenant", "other") != 403 || f.calls != 0 {
		t.Fatal("out-of-policy scope reached storage")
	}
	if request("Bearer read-secret", "tenant", "allowed") != 200 || f.calls != 1 || f.scope.AppID != "app" || f.scope.Visibility != "group" || f.id != "task_123" {
		t.Fatal("authenticated scoped task read failed")
	}
}

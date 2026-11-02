package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type scheduleTestPolicy struct{ denied bool }

func (p scheduleTestPolicy) CheckToolPolicy(context.Context, string, string, bool, ...bool) error {
	if p.denied {
		return domain.ErrUnauthorized
	}
	return nil
}

func TestScheduleRendererUsesConfiguredReadOnlyCapabilityAndTypedDisplay(t *testing.T) {
	called := 0
	r, err := atoms.New(atoms.Options{Extensions: []atoms.Definition{{Name: "gold_price_get", Run: func(_ context.Context, in atoms.Invocation, _ string) (string, error) {
		called++
		if in.Task.Scope.ChatID != "chat" {
			t.Fatal("lost server scope")
		}
		return `{"available":true,"provider":"SGE","records":[{"data":{"价格":650.25,"品种":"Au99.99"}}],"truncated":false}`, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	run := domain.ScheduleRun{Scope: domain.Scope{TenantID: "t", AppID: "a", ChatID: "chat", Visibility: "group"}, Query: &domain.ScheduleQuery{Name: "gold_price_get", Arguments: json.RawMessage(`{}`)}}
	typ, content, err := scheduleRenderer(r, scheduleTestPolicy{})(context.Background(), run)
	if err != nil || typ != "interactive" || !strings.Contains(content, "650.25") || !strings.Contains(content, "Au99.99") || called != 1 {
		t.Fatalf("query rendering failed: %s %s %v", typ, content, err)
	}
	run.Query.Name = "feishu_send_message"
	if _, _, err = scheduleRenderer(r, scheduleTestPolicy{})(context.Background(), run); err == nil {
		t.Fatal("scheduled arbitrary effect")
	}
	run.Query.Name = "stock_zh_a_get"
	if _, _, err = scheduleRenderer(r, scheduleTestPolicy{})(context.Background(), run); err == nil {
		t.Fatal("unconfigured capability scheduled")
	}
	run.Query.Name = "gold_price_get"
	if _, _, err = scheduleRenderer(r, scheduleTestPolicy{denied: true})(context.Background(), run); err == nil || called != 1 {
		t.Fatal("revoked selected query executed")
	}
}

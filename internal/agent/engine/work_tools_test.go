package engine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
)

type workModel struct {
	steps        []modelapi.ToolFunction
	observations []string
}

func (m *workModel) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.observations = nil
	for _, msg := range r.Messages {
		if msg.Role == "tool" {
			m.observations = append(m.observations, msg.Content.(string))
		}
	}
	if len(m.observations) < len(m.steps) {
		return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", ToolCalls: []modelapi.ToolCall{{ID: "work_call", Type: "function", Function: m.steps[len(m.observations)]}}}}, nil
	}
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: `{"choice":"reply","summary":"","messages":[{"text":"记下了，到点叫你。","reply_to_message_id":"request"}]}`}}, nil
}

func TestNativeWorkToolsCreateAndListWithoutApproval(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	registry, err := atoms.New(atoms.Options{Tasks: f.store})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, d := range registry.Definitions() {
		names[d.Name] = true
	}
	for _, name := range []string{"todo_create", "todo_list", "todo_update", "reminder_create", "reminder_list", "reminder_cancel"} {
		if !names[name] {
			t.Fatalf("missing work tool %s", name)
		}
	}
	reminder, _ := json.Marshal(domain.ReminderInput{Text: "休息一下", DueAt: time.Now().Add(time.Hour)})
	m := &workModel{steps: []modelapi.ToolFunction{{Name: "todo_create", Arguments: `{"title":"整理资料"}`}, {Name: "todo_list", Arguments: `{}`}, {Name: "reminder_create", Arguments: string(reminder)}, {Name: "reminder_list", Arguments: `{}`}}}
	f.engine, err = New(Options{Store: f.store, Model: m, Registry: registry, ConversationOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	approvals, err := f.store.Approvals(context.Background(), f.task.Scope, f.task.ID)
	if err != nil || len(approvals) != 0 {
		t.Fatalf("work tools requested approvals: %v %v", approvals, err)
	}
	todos, err := f.store.ListTodos(context.Background(), f.task.Scope, "", 20)
	if err != nil || len(todos) != 1 || todos[0].CreatedBy != "backend-actor" {
		t.Fatalf("todo result: %v %v", todos, err)
	}
	reminders, err := f.store.ListReminders(context.Background(), f.task.Scope, "", 20)
	if err != nil || len(reminders) != 1 || reminders[0].Text != "休息一下" {
		t.Fatalf("reminder result: %v %v", reminders, err)
	}
	if len(m.observations) != 4 {
		t.Fatalf("native loop stopped early: %v", m.observations)
	}
}

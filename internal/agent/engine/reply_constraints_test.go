package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// Capture at the native model boundary, including Eino's actual tool
// continuation; checking a prompt helper alone would miss unwired rules.
type replyConstraintCapture struct {
	requests     []modelapi.ChatRequest
	conversation bool
}

type resumeConstraintCapture struct{ requests []modelapi.ChatRequest }

func (m *resumeConstraintCapture) Generate(_ context.Context, request modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.requests = append(m.requests, request)
	for _, message := range request.Messages {
		if message.Role == "tool" {
			return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: `{"choice":"reply","summary":"","messages":[{"text":"处理好了。","reply_to_message_id":"request"}]}`}}, nil
		}
	}
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", ToolCalls: []modelapi.ToolCall{{ID: "legacy-action", Type: "function", Function: modelapi.ToolFunction{Name: "ordinary_action", Arguments: `{"amount":1}`}}}}}, nil
}

func legacyReplyConstraintCheckpoint(t *testing.T) (*autoEffectFixture, *resumeConstraintCapture, string, domain.Job, string) {
	t.Helper()
	f := newAutoEffectFixture(t, false, false)
	ctx := context.Background()
	model := &resumeConstraintCapture{}
	f.engine.c.Model, f.engine.c.ConversationOutput = model, true
	task, err := f.store.StartTurn(ctx, f.job, domain.TurnRequest{TaskID: f.task.ID, Revision: f.task.Revision})
	if err != nil {
		t.Fatal("start legacy turn failed")
	}
	prompt, refs, replyTargets, err := f.engine.prompt(ctx, task)
	if err != nil {
		t.Fatal("build legacy scene failed")
	}
	task, err = f.store.SetTurnContext(ctx, f.job, task, refs, replyTargets)
	if err != nil {
		t.Fatal("bind legacy scene failed")
	}
	// A genuine pre-release native checkpoint contains the old system message.
	// Reconstruct it through Eino rather than changing serialized checkpoint bytes.
	var tools []tool.BaseTool
	for _, definition := range f.engine.c.Registry.Definitions() {
		tools = append(tools, &boundTool{definition: definition, store: f.store, job: f.job, task: task, refresh: f.engine.RefreshTask, present: f.engine.presentTool})
	}
	legacyInstruction := instruction + "\n" + conversationOutputInstruction
	legacyAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "group_agent", Description: "Virtual AI group member", Instruction: legacyInstruction, Model: NewChatAdapter(model, f.engine.c.MaxContextBytes, true), ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools, ExecuteSequentially: true}}, MaxIterations: f.engine.c.MaxIterations})
	if err != nil {
		t.Fatal("create legacy native agent failed")
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: legacyAgent, CheckPointStore: f.store.Checkpoints(f.job, task)})
	iter := runner.Run(ctx, []*schema.Message{schema.UserMessage(prompt)}, adk.WithCheckPointID(fmt.Sprintf("%s:%d", task.ID, task.Revision)))
	interrupts := map[string]string{}
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal("legacy native run failed")
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			for _, interrupt := range event.Action.Interrupted.InterruptContexts {
				if id, ok := interrupt.Info.(string); interrupt.IsRootCause && ok {
					interrupts[id] = interrupt.ID
				}
			}
		}
	}
	if len(model.requests) != 1 || replyConstraintSystem(model.requests[0]) != legacyInstruction || len(interrupts) != 1 || f.calls.Load() != 0 {
		t.Fatal("legacy fixture must persist one real approval interrupt without executing")
	}
	var approvalID string
	for id, target := range interrupts {
		approvalID = id
		if err = f.store.BindInterrupt(ctx, f.job, task, id, target); err != nil {
			t.Fatal("bind legacy approval failed")
		}
	}
	if err = f.store.EndTurn(ctx, f.job, task, "waiting"); err != nil {
		t.Fatal("end legacy turn failed")
	}
	if err = f.store.CompleteJob(ctx, f.job); err != nil {
		t.Fatal("complete legacy job failed")
	}
	if _, err = f.store.ControlTask(ctx, task.Scope, task.OwnerID, domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: task.Revision, ApprovalID: approvalID}, domain.SourceRef{}, ""); err != nil {
		t.Fatal("approve exact legacy operation failed")
	}
	job, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "upgraded-worker", time.Minute)
	if err != nil {
		t.Fatal("claim upgraded resume failed")
	}
	f.task = task
	return f, model, prompt, job, approvalID
}

func TestNativeReplyConstraintsReachResumedLegacyApprovalCheckpoint(t *testing.T) {
	f, model, prompt, job, approvalID := legacyReplyConstraintCheckpoint(t)
	ctx, task := context.Background(), f.task
	var err error
	if err = f.engine.Execute(ctx, job, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision, ApprovalID: approvalID}); err != nil {
		t.Fatal("resume legacy native checkpoint failed")
	}
	if len(model.requests) != 2 {
		t.Fatalf("resume must continue the old tool call without replanning, got %d requests", len(model.requests))
	}
	resumed := model.requests[1]
	system := replyConstraintSystem(resumed)
	if !strings.Contains(system, replyConstraintsInstruction) || strings.Count(system, "阅读和回复约束：") != 1 {
		t.Error("resumed legacy checkpoint did not receive exactly one current reply constraint block")
	}
	if !strings.Contains(system, replyConstraintSystem(model.requests[0])) {
		t.Error("resume replaced existing social, permission or output instructions")
	}
	var sceneCount, assistantCalls, toolCount int
	for _, message := range resumed.Messages {
		switch message.Role {
		case "user":
			sceneCount++
			if message.Content != prompt {
				t.Error("resume changed the persisted user scene")
			}
		case "assistant":
			for _, call := range message.ToolCalls {
				assistantCalls++
				if call.ID != "legacy-action" || call.Function.Name != "ordinary_action" || call.Function.Arguments != `{"amount":1}` {
					t.Error("resume changed the frozen assistant tool call")
				}
			}
		case "tool":
			toolCount++
			if message.ToolCallID != "legacy-action" || message.Content != `{"receipt":"completed"}` {
				t.Error("resume changed the actual receipt or call binding")
			}
		}
	}
	if sceneCount != 1 || assistantCalls != 1 || toolCount != 1 || f.calls.Load() != 1 {
		t.Error("resume duplicated or dropped persisted history or operation")
	}
	ledger, err := f.store.ToolExecution(ctx, task.Scope, task.ID, domain.StableID(task.ID, "ordinary_action", `{"amount":1}`))
	if err != nil || ledger.Status != "completed" || ledger.Result != `{"receipt":"completed"}` || ledger.ApprovalID != approvalID || ledger.ActorID != task.OwnerID {
		t.Error("resume changed durable receipt or exact approval authority")
	}
}

func TestNativeReplyConstraintsCountTowardResumedContextBudget(t *testing.T) {
	f, model, _, job, approvalID := legacyReplyConstraintCheckpoint(t)
	// Fit exactly the request restored by the old adapter, including its actual
	// tool schema, output schema, frozen call and success receipt. Adding current
	// constraints must be charged before making the next provider call.
	oldRequest := model.requests[0]
	oldRequest.Messages = append(append([]modelapi.ChatMessage{}, oldRequest.Messages...),
		modelapi.ChatMessage{Role: "assistant", Content: "", ToolCalls: []modelapi.ToolCall{{ID: "legacy-action", Type: "function", Function: modelapi.ToolFunction{Name: "ordinary_action", Arguments: `{"amount":1}`}}}},
		modelapi.ChatMessage{Role: "tool", ToolCallID: "legacy-action", Content: `{"receipt":"completed"}`})
	encoded, err := json.Marshal(oldRequest)
	if err != nil {
		t.Fatal("encode legacy request budget failed")
	}
	f.engine.c.MaxContextBytes = len(encoded)
	err = f.engine.Execute(context.Background(), job, domain.TurnRequest{TaskID: f.task.ID, Revision: f.task.Revision, ApprovalID: approvalID})
	if !errors.Is(err, domain.ErrInvalid) || len(model.requests) != 1 {
		t.Error("resumed reply constraints were not counted before the provider request")
	}
	if f.calls.Load() != 1 {
		t.Error("context budget check changed the approved effect execution")
	}
	ledger, err := f.store.ToolExecution(context.Background(), f.task.Scope, f.task.ID, domain.StableID(f.task.ID, "ordinary_action", `{"amount":1}`))
	if err != nil || ledger.Status != "completed" || ledger.Result != `{"receipt":"completed"}` || ledger.ApprovalID != approvalID {
		t.Error("context budget rejection changed the confirmed operation receipt")
	}
}

func (m *replyConstraintCapture) Generate(_ context.Context, request modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.requests = append(m.requests, request)
	for _, message := range request.Messages {
		if message.Role == "tool" {
			answer := "结果还没确认。"
			if m.conversation {
				answer = `{"choice":"reply","summary":"","messages":[{"text":"结果还没确认。","reply_to_message_id":"constraint-current"}]}`
			}
			return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: answer}}, nil
		}
	}
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", ToolCalls: []modelapi.ToolCall{{ID: "uncertain-action", Type: "function", Function: modelapi.ToolFunction{Name: "ordinary_action", Arguments: `{"amount":1}`}}}}}, nil
}

func replyConstraintSystem(request modelapi.ChatRequest) string {
	var system strings.Builder
	for _, message := range request.Messages {
		if message.Role == "system" {
			if content, ok := message.Content.(string); ok && !strings.HasPrefix(content, "[当前时间] 北京时间 ") {
				system.WriteString(content)
			}
		}
	}
	return system.String()
}

func TestNativeReplyConstraintsPersistThroughUncertainToolContinuation(t *testing.T) {
	for _, conversation := range []bool{false, true} {
		name := "plain"
		if conversation {
			name = "conversation"
		}
		t.Run(name, func(t *testing.T) {
			f := newAutoEffectFixture(t, true, true)
			ctx := context.Background()
			at := time.Now().UTC().Add(-time.Minute)
			old := sceneAccept(t, f, "constraint-quote", "member-c", "@_user_1 很会讲课", "", at, domain.IngressMention{Key: "@_user_1", OpenID: "member-a", Name: "甲"})
			replied := sceneAccept(t, f, "constraint-replied", "member-b", "这是我的原话", "", at.Add(time.Millisecond))
			current := sceneAccept(t, f, "constraint-current", "member-a", "@_user_1 这次结果呢？", replied.Source.MessageID, at.Add(2*time.Millisecond), domain.IngressMention{Key: "@_user_1", OpenID: "member-b", Name: "乙"})
			task, err := f.store.CreateTask(ctx, current, time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal("create constraint task failed")
			}
			job, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "constraint-worker", time.Minute)
			if err != nil {
				t.Fatal("claim constraint turn failed")
			}
			model := &replyConstraintCapture{conversation: conversation}
			f.engine.c.Model, f.engine.c.ConversationOutput = model, conversation
			f.engine.c.GroupMembers = sceneMemberReader(func(context.Context, domain.Scope, int, string) (domain.GroupMembers, error) {
				return domain.GroupMembers{Members: []domain.GroupMember{{ID: "member-a", Name: "甲", IDType: "open_id"}, {ID: "member-b", Name: "乙", IDType: "open_id"}, {ID: "member-c", Name: "丙", IDType: "open_id"}}}, nil
			})
			if err = f.engine.Execute(ctx, job, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision}); err != nil {
				t.Fatal("native constraint turn failed")
			}
			if len(model.requests) != 2 {
				t.Fatalf("want initial request and tool continuation, got %d requests", len(model.requests))
			}
			for i, request := range model.requests {
				got, _ := sceneInput(t, request)
				if got.CurrentUtterance.SpeakerName != "甲" || got.CurrentUtterance.Text != "@乙 这次结果呢？" || got.CurrentUtterance.ReplyTo == nil || got.CurrentUtterance.ReplyTo.SpeakerName != "乙" {
					t.Errorf("request %d lost distinct current speaker, mention or reply author", i)
				}
				found := false
				for _, message := range append(got.RecentConversation, got.BoundMaterials...) {
					if message.MessageID == old.Source.MessageID {
						found = message.SpeakerName == "丙" && message.Text == "@甲 很会讲课"
					}
				}
				if !found {
					t.Errorf("request %d lost the quoted author's message-local mention binding", i)
				}
				system := replyConstraintSystem(request)
				for _, want := range []string{
					"当前作者、被提及者、被回复者和被引用者", "mention token 只在所属消息内绑定",
					"显示名和 self 身份 ID", "unknown_*", "别名归属或操作授权",
					"成员陈述", "DerivedText", "interpretation", "外部当前事实", "自己的历史话语", "已确认的操作回执",
					"第三方评价不能当作本人自述", "原话引用", "明确是转述",
					"available:false", "失败", "status:unknown", "replay_blocked", "不代表成功",
					"空搜索仅说明本次未找到", "不能推成从未发生", "Truncated", "Degraded", "partial", "不能当作完整材料",
					"工具名、状态字段和原因码只用于内部理解", "自然语言给出有用结论和必要限制", "只有对方确实要求调试细节时",
					"接着当前子话题", "不重讲背景", "不公开审计或纠错记账回执", "不强制调用工具、附引用或默认澄清",
				} {
					if !strings.Contains(system, want) {
						t.Errorf("native request %d missing reply grounding rule %q", i, want)
					}
				}
				if strings.Count(system, "阅读和回复约束：") != 1 || !strings.Contains(system, "保持稳定的 AI 身份") || !strings.Contains(system, "玩笑") || !strings.Contains(system, "关心") {
					t.Errorf("request %d must retain social voice with one grounding block", i)
				}
				if conversation && !strings.Contains(system, "如果提出一个真要对方回答的问题，就停下来等未来的人类消息") {
					t.Errorf("request %d lost conversation output discipline", i)
				}
			}
			if replyConstraintSystem(model.requests[0]) != replyConstraintSystem(model.requests[1]) {
				t.Error("tool continuation changed the instruction")
			}
			toolCount := 0
			for _, message := range model.requests[1].Messages {
				if message.Role == "tool" {
					toolCount++
					if message.ToolCallID != "uncertain-action" || message.Content != `{"status":"unknown","replay_blocked":true}` {
						t.Error("native continuation changed the uncertain result or call binding")
					}
				}
			}
			if toolCount != 1 || f.calls.Load() != 1 {
				t.Errorf("uncertain operation must execute once and yield one actual result: tools=%d calls=%d", toolCount, f.calls.Load())
			}
			ledger, err := f.store.ToolExecution(ctx, task.Scope, task.ID, domain.StableID(task.ID, "ordinary_action", `{"amount":1}`))
			if err != nil || ledger.Status != "unknown" || ledger.ApprovalID != "" {
				t.Error("uncertain receipt or authority was changed")
			}
			pinned, err := f.store.Task(ctx, task.Scope, task.ID)
			if err != nil {
				t.Fatal("read constraint task failed")
			}
			for _, want := range []domain.SourceRef{old.Source, replied.Source, current.Source} {
				found := false
				for _, source := range pinned.ContextSources {
					found = found || source == want
				}
				if !found {
					t.Error("native reading view dropped a backend source dependency")
				}
			}
		})
	}
}

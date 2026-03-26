package agentruntime_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	"gorm.io/gorm"
)

type plannerTestCapability struct {
	meta         agentruntime.CapabilityMeta
	result       agentruntime.CapabilityResult
	err          error
	executeCount int
}

type plannerTestReplyEmitter struct {
	requests []agentruntime.ReplyEmissionRequest
	result   agentruntime.ReplyEmissionResult
	err      error
}

type plannerTestCapabilityReplyPlanner struct {
	requests []agentruntime.CapabilityReplyPlanningRequest
	result   agentruntime.CapabilityReplyPlan
	err      error
}

type plannerTestCapabilityReplyTurnExecutor struct {
	requests []agentruntime.CapabilityReplyTurnRequest
	result   agentruntime.CapabilityReplyTurnResult
	err      error
	execute  func(context.Context, agentruntime.CapabilityReplyTurnRequest) (agentruntime.CapabilityReplyTurnResult, error)
}

type plannerTestContinuationReplyTurnExecutor struct {
	requests []agentruntime.ContinuationReplyTurnRequest
	result   agentruntime.ContinuationReplyTurnResult
	err      error
	execute  func(context.Context, agentruntime.ContinuationReplyTurnRequest) (agentruntime.ContinuationReplyTurnResult, error)
}

func (e *plannerTestReplyEmitter) EmitReply(_ context.Context, req agentruntime.ReplyEmissionRequest) (agentruntime.ReplyEmissionResult, error) {
	e.requests = append(e.requests, req)
	return e.result, e.err
}

func (p *plannerTestCapabilityReplyPlanner) PlanCapabilityReply(_ context.Context, req agentruntime.CapabilityReplyPlanningRequest) (agentruntime.CapabilityReplyPlan, error) {
	p.requests = append(p.requests, req)
	return p.result, p.err
}

func (e *plannerTestCapabilityReplyTurnExecutor) ExecuteCapabilityReplyTurn(ctx context.Context, req agentruntime.CapabilityReplyTurnRequest) (agentruntime.CapabilityReplyTurnResult, error) {
	e.requests = append(e.requests, req)
	if e.execute != nil {
		return e.execute(ctx, req)
	}
	return e.result, e.err
}

func (e *plannerTestContinuationReplyTurnExecutor) ExecuteContinuationReplyTurn(ctx context.Context, req agentruntime.ContinuationReplyTurnRequest) (agentruntime.ContinuationReplyTurnResult, error) {
	e.requests = append(e.requests, req)
	if e.execute != nil {
		return e.execute(ctx, req)
	}
	return e.result, e.err
}

func (c *plannerTestCapability) Meta() agentruntime.CapabilityMeta {
	return c.meta
}

func (c *plannerTestCapability) Execute(context.Context, agentruntime.CapabilityRequest) (agentruntime.CapabilityResult, error) {
	c.executeCount++
	return c.result, c.err
}

func TestContinuationProcessorExecutesQueuedCapabilityCall(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createQueuedCapabilityRun(t, db, coordinator, "echo_cap", agentruntime.CapabilityCallInput{
		Request: agentruntime.CapabilityRequest{
			Scope:       agentruntime.CapabilityScopeGroup,
			ChatID:      "oc_chat",
			ActorOpenID: "ou_actor",
			InputText:   "hello",
			PayloadJSON: []byte(`{"text":"hello"}`),
		},
	})

	registry := agentruntime.NewCapabilityRegistry()
	capability := &plannerTestCapability{
		meta: agentruntime.CapabilityMeta{
			Name:            "echo_cap",
			Kind:            agentruntime.CapabilityKindTool,
			SideEffectLevel: agentruntime.SideEffectLevelNone,
			AllowedScopes:   []agentruntime.CapabilityScope{agentruntime.CapabilityScopeGroup},
			DefaultTimeout:  5 * time.Second,
		},
		result: agentruntime.CapabilityResult{
			OutputText:  "echo:hello",
			OutputJSON:  []byte(`{"echo":"hello"}`),
			ExternalRef: "echo_ref",
		},
	}
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithCapabilityRegistry(registry))
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      run.ID,
		Revision:   run.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 18, 19, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if capability.executeCount != 1 {
		t.Fatalf("execute count = %d, want 1", capability.executeCount)
	}

	updatedRun, err := agentstore.NewRunRepository(db).GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updatedRun.Status != agentruntime.RunStatusCompleted {
		t.Fatalf("run status = %q, want %q", updatedRun.Status, agentruntime.RunStatusCompleted)
	}
	if updatedRun.CurrentStepIndex != 4 {
		t.Fatalf("current step index = %d, want 4", updatedRun.CurrentStepIndex)
	}
	if updatedRun.ResultSummary != "echo:hello" {
		t.Fatalf("result summary = %q, want echo:hello", updatedRun.ResultSummary)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 5 {
		t.Fatalf("step count = %d, want 5", len(steps))
	}
	if steps[1].Kind != agentruntime.StepKindCapabilityCall || steps[1].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected capability step: %+v", steps[1])
	}
	if steps[2].Kind != agentruntime.StepKindObserve || steps[2].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected observe step: %+v", steps[2])
	}
	if steps[3].Kind != agentruntime.StepKindPlan || steps[3].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected plan step: %+v", steps[3])
	}
	if steps[4].Kind != agentruntime.StepKindReply || steps[4].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected reply step: %+v", steps[4])
	}
}

func TestContinuationProcessorEmitsCapabilityReplyAndPersistsReplyRefs(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createQueuedCapabilityRun(t, db, coordinator, "echo_cap", agentruntime.CapabilityCallInput{
		Request: agentruntime.CapabilityRequest{
			Scope:       agentruntime.CapabilityScopeGroup,
			ChatID:      "oc_chat",
			ActorOpenID: "ou_actor",
			InputText:   "hello",
			PayloadJSON: []byte(`{"text":"hello"}`),
		},
	})

	registry := agentruntime.NewCapabilityRegistry()
	capability := &plannerTestCapability{
		meta: agentruntime.CapabilityMeta{
			Name:            "echo_cap",
			Kind:            agentruntime.CapabilityKindTool,
			SideEffectLevel: agentruntime.SideEffectLevelNone,
			AllowedScopes:   []agentruntime.CapabilityScope{agentruntime.CapabilityScopeGroup},
			DefaultTimeout:  5 * time.Second,
		},
		result: agentruntime.CapabilityResult{
			OutputText:  "echo:hello",
			OutputJSON:  []byte(`{"echo":"hello"}`),
			ExternalRef: "echo_ref",
		},
	}
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID: "om_cont_reply",
			CardID:    "card_cont_reply",
		},
	}

	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(registry),
		agentruntime.WithReplyEmitter(replyEmitter),
	)
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      run.ID,
		Revision:   run.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 18, 19, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	req := replyEmitter.requests[0]
	if req.Run == nil || req.Run.ID != run.ID {
		t.Fatalf("unexpected run in reply emitter request: %+v", req.Run)
	}
	if req.Session == nil || req.Session.ChatID != "oc_chat" {
		t.Fatalf("unexpected session in reply emitter request: %+v", req.Session)
	}
	if req.ReplyText != "echo:hello" {
		t.Fatalf("reply text = %q, want echo:hello", req.ReplyText)
	}

	updatedRun, err := agentstore.NewRunRepository(db).GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updatedRun.LastResponseID != "om_cont_reply" {
		t.Fatalf("last response id = %q, want om_cont_reply", updatedRun.LastResponseID)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if steps[4].Kind != agentruntime.StepKindReply {
		t.Fatalf("unexpected reply step: %+v", steps[4])
	}
	if steps[4].ExternalRef != "card_cont_reply" {
		t.Fatalf("reply step external ref = %q, want card_cont_reply", steps[4].ExternalRef)
	}
	replyOutput := map[string]any{}
	if err := json.Unmarshal(steps[4].OutputJSON, &replyOutput); err != nil {
		t.Fatalf("json.Unmarshal(reply output) error = %v", err)
	}
	if replyOutput["response_message_id"] != "om_cont_reply" {
		t.Fatalf("response_message_id = %#v, want om_cont_reply", replyOutput["response_message_id"])
	}
	if replyOutput["response_card_id"] != "card_cont_reply" {
		t.Fatalf("response_card_id = %#v, want card_cont_reply", replyOutput["response_card_id"])
	}
}

func TestContinuationProcessorUsesCapabilityReplyPlannerWhenPresent(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createQueuedCapabilityRun(t, db, coordinator, "echo_cap", agentruntime.CapabilityCallInput{
		Request: agentruntime.CapabilityRequest{
			Scope:       agentruntime.CapabilityScopeGroup,
			ChatID:      "oc_chat",
			ActorOpenID: "ou_actor",
			InputText:   "帮我处理下结果",
			PayloadJSON: []byte(`{"text":"hello"}`),
		},
	})

	registry := agentruntime.NewCapabilityRegistry()
	capability := &plannerTestCapability{
		meta: agentruntime.CapabilityMeta{
			Name:            "echo_cap",
			Kind:            agentruntime.CapabilityKindTool,
			SideEffectLevel: agentruntime.SideEffectLevelNone,
			AllowedScopes:   []agentruntime.CapabilityScope{agentruntime.CapabilityScopeGroup},
			DefaultTimeout:  5 * time.Second,
		},
		result: agentruntime.CapabilityResult{
			OutputText:  "echo:hello",
			OutputJSON:  []byte(`{"echo":"hello"}`),
			ExternalRef: "echo_ref",
		},
	}
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID: "om_cont_reply",
			CardID:    "card_cont_reply",
		},
	}
	replyPlanner := &plannerTestCapabilityReplyPlanner{
		result: agentruntime.CapabilityReplyPlan{
			ThoughtText: "先消化工具输出，再组织回复",
			ReplyText:   "我已经根据 echo 结果整理好了。",
		},
	}

	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(registry),
		agentruntime.WithReplyEmitter(replyEmitter),
		agentruntime.WithCapabilityReplyPlanner(replyPlanner),
	)
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      run.ID,
		Revision:   run.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 20, 11, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyPlanner.requests) != 1 {
		t.Fatalf("reply planner request count = %d, want 1", len(replyPlanner.requests))
	}
	if replyPlanner.requests[0].ChatID != "oc_chat" {
		t.Fatalf("planner chat id = %q, want %q", replyPlanner.requests[0].ChatID, "oc_chat")
	}
	if replyPlanner.requests[0].OpenID != run.ActorOpenID {
		t.Fatalf("planner open id = %q, want %q", replyPlanner.requests[0].OpenID, run.ActorOpenID)
	}
	if replyPlanner.requests[0].InputText != run.InputText {
		t.Fatalf("planner input text = %q, want %q", replyPlanner.requests[0].InputText, run.InputText)
	}
	if replyPlanner.requests[0].CapabilityName != "echo_cap" {
		t.Fatalf("capability name = %q, want %q", replyPlanner.requests[0].CapabilityName, "echo_cap")
	}
	if replyPlanner.requests[0].Result.OutputText != "echo:hello" {
		t.Fatalf("planner result output = %q, want %q", replyPlanner.requests[0].Result.OutputText, "echo:hello")
	}

	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	if replyEmitter.requests[0].ThoughtText != "先消化工具输出，再组织回复" {
		t.Fatalf("thought text = %q, want %q", replyEmitter.requests[0].ThoughtText, "先消化工具输出，再组织回复")
	}
	if replyEmitter.requests[0].ReplyText != "我已经根据 echo 结果整理好了。" {
		t.Fatalf("reply text = %q, want %q", replyEmitter.requests[0].ReplyText, "我已经根据 echo 结果整理好了。")
	}

	updatedRun, err := agentstore.NewRunRepository(db).GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updatedRun.ResultSummary != "我已经根据 echo 结果整理好了。" {
		t.Fatalf("result summary = %q, want %q", updatedRun.ResultSummary, "我已经根据 echo 结果整理好了。")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	replyOutput := map[string]any{}
	if err := json.Unmarshal(steps[4].OutputJSON, &replyOutput); err != nil {
		t.Fatalf("json.Unmarshal(reply output) error = %v", err)
	}
	if replyOutput["thought_text"] != "先消化工具输出，再组织回复" {
		t.Fatalf("reply thought_text = %#v, want %q", replyOutput["thought_text"], "先消化工具输出，再组织回复")
	}
	if replyOutput["reply_text"] != "我已经根据 echo 结果整理好了。" {
		t.Fatalf("reply text = %#v, want %q", replyOutput["reply_text"], "我已经根据 echo 结果整理好了。")
	}
}

func TestContinuationProcessorUsesCapabilityReplyTurnExecutorWhenContinuationStatePresent(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createQueuedCapabilityRun(t, db, coordinator, "echo_cap", agentruntime.CapabilityCallInput{
		Request: agentruntime.CapabilityRequest{
			Scope:       agentruntime.CapabilityScopeGroup,
			ChatID:      "oc_chat",
			ActorOpenID: "ou_actor",
			InputText:   "帮我处理下结果",
			PayloadJSON: []byte(`{"text":"hello"}`),
		},
		Continuation: &agentruntime.CapabilityContinuationInput{
			PreviousResponseID: "resp_tool_1",
		},
	})

	registry := agentruntime.NewCapabilityRegistry()
	capability := &plannerTestCapability{
		meta: agentruntime.CapabilityMeta{
			Name:            "echo_cap",
			Kind:            agentruntime.CapabilityKindTool,
			SideEffectLevel: agentruntime.SideEffectLevelNone,
			AllowedScopes:   []agentruntime.CapabilityScope{agentruntime.CapabilityScopeGroup},
			DefaultTimeout:  5 * time.Second,
		},
		result: agentruntime.CapabilityResult{
			OutputText:  "echo:hello",
			OutputJSON:  []byte(`{"echo":"hello"}`),
			ExternalRef: "echo_ref",
		},
	}
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID: "om_cont_reply",
			CardID:    "card_cont_reply",
		},
	}
	replyTurnExecutor := &plannerTestCapabilityReplyTurnExecutor{
		result: agentruntime.CapabilityReplyTurnResult{
			Executed: true,
			Plan: agentruntime.CapabilityReplyPlan{
				ThoughtText: "把真实工具结果回填到原对话链路",
				ReplyText:   "现在我已经根据真实 echo 结果完成回复。",
			},
		},
	}
	replyPlanner := &plannerTestCapabilityReplyPlanner{
		result: agentruntime.CapabilityReplyPlan{
			ThoughtText: "planner fallback",
			ReplyText:   "planner fallback",
		},
	}

	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(registry),
		agentruntime.WithReplyEmitter(replyEmitter),
		agentruntime.WithCapabilityReplyTurnExecutor(replyTurnExecutor),
		agentruntime.WithCapabilityReplyPlanner(replyPlanner),
	)
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      run.ID,
		Revision:   run.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 20, 11, 30, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyTurnExecutor.requests) != 1 {
		t.Fatalf("reply turn executor request count = %d, want 1", len(replyTurnExecutor.requests))
	}
	if replyTurnExecutor.requests[0].Input.Continuation == nil || replyTurnExecutor.requests[0].Input.Continuation.PreviousResponseID != "resp_tool_1" {
		t.Fatalf("unexpected continuation input: %+v", replyTurnExecutor.requests[0].Input.Continuation)
	}
	if len(replyPlanner.requests) != 0 {
		t.Fatalf("reply planner request count = %d, want 0", len(replyPlanner.requests))
	}
	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	if replyEmitter.requests[0].ThoughtText != "把真实工具结果回填到原对话链路" {
		t.Fatalf("thought text = %q, want %q", replyEmitter.requests[0].ThoughtText, "把真实工具结果回填到原对话链路")
	}
	if replyEmitter.requests[0].ReplyText != "现在我已经根据真实 echo 结果完成回复。" {
		t.Fatalf("reply text = %q, want %q", replyEmitter.requests[0].ReplyText, "现在我已经根据真实 echo 结果完成回复。")
	}
}

func TestContinuationProcessorQueuesFollowUpPendingCapabilityFromReplyTurnExecutor(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createQueuedCapabilityRun(t, db, coordinator, "echo_cap", agentruntime.CapabilityCallInput{
		Request: agentruntime.CapabilityRequest{
			Scope:       agentruntime.CapabilityScopeGroup,
			ChatID:      "oc_chat",
			ActorOpenID: "ou_actor",
			InputText:   "帮我处理下结果",
			PayloadJSON: []byte(`{"text":"hello"}`),
		},
		Continuation: &agentruntime.CapabilityContinuationInput{
			PreviousResponseID: "resp_tool_1",
		},
	})

	registry := agentruntime.NewCapabilityRegistry()
	capability := &plannerTestCapability{
		meta: agentruntime.CapabilityMeta{
			Name:            "echo_cap",
			Kind:            agentruntime.CapabilityKindTool,
			SideEffectLevel: agentruntime.SideEffectLevelNone,
			AllowedScopes:   []agentruntime.CapabilityScope{agentruntime.CapabilityScopeGroup},
			DefaultTimeout:  5 * time.Second,
		},
		result: agentruntime.CapabilityResult{
			OutputText:  "echo:hello",
			OutputJSON:  []byte(`{"echo":"hello"}`),
			ExternalRef: "echo_ref",
		},
	}
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID: "om_cont_reply",
			CardID:    "card_cont_reply",
		},
	}
	replyTurnExecutor := &plannerTestCapabilityReplyTurnExecutor{
		result: agentruntime.CapabilityReplyTurnResult{
			Executed: true,
			Plan: agentruntime.CapabilityReplyPlan{
				ThoughtText: "先执行附带查询，再等待发送审批",
				ReplyText:   "我已经补充查询，并发起新的发送审批。",
			},
			CapabilityCalls: []agentruntime.CompletedCapabilityCall{
				{
					CallID:         "call_nested_1",
					CapabilityName: "search_history",
					Arguments:      `{"q":"agentic runtime"}`,
					Output:         "搜索结果",
				},
			},
			PendingCapability: &agentruntime.QueuedCapabilityCall{
				CallID:         "call_pending_2",
				CapabilityName: "send_message",
				Input: agentruntime.CapabilityCallInput{
					Request: agentruntime.CapabilityRequest{
						Scope:       agentruntime.CapabilityScopeGroup,
						ChatID:      "oc_chat",
						ActorOpenID: "ou_actor",
						InputText:   "帮我处理下结果",
						PayloadJSON: []byte(`{"content":"hello"}`),
					},
					Approval: &agentruntime.CapabilityApprovalSpec{
						Type:    "capability",
						Title:   "审批发送消息",
						Summary: "等待发送审批",
					},
				},
			},
		},
	}

	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(registry),
		agentruntime.WithReplyEmitter(replyEmitter),
		agentruntime.WithCapabilityReplyTurnExecutor(replyTurnExecutor),
	)
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      run.ID,
		Revision:   run.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	updatedRun, err := agentstore.NewRunRepository(db).GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updatedRun.Status != agentruntime.RunStatusQueued {
		t.Fatalf("run status = %q, want %q", updatedRun.Status, agentruntime.RunStatusQueued)
	}
	if updatedRun.ResultSummary != "我已经补充查询，并发起新的发送审批。" {
		t.Fatalf("result summary = %q, want %q", updatedRun.ResultSummary, "我已经补充查询，并发起新的发送审批。")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 8 {
		t.Fatalf("step count = %d, want 8", len(steps))
	}
	if steps[3].Kind != agentruntime.StepKindCapabilityCall || steps[3].CapabilityName != "search_history" {
		t.Fatalf("unexpected nested capability step: %+v", steps[3])
	}
	if steps[4].Kind != agentruntime.StepKindObserve {
		t.Fatalf("unexpected nested observe step: %+v", steps[4])
	}
	if steps[5].Kind != agentruntime.StepKindPlan || steps[5].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected plan step: %+v", steps[5])
	}
	if steps[6].Kind != agentruntime.StepKindReply {
		t.Fatalf("unexpected reply step: %+v", steps[6])
	}
	if steps[7].Kind != agentruntime.StepKindCapabilityCall || steps[7].Status != agentruntime.StepStatusQueued || steps[7].CapabilityName != "send_message" {
		t.Fatalf("unexpected queued capability step: %+v", steps[7])
	}
}

func TestContinuationProcessorRequestsApprovalForProtectedCapability(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createQueuedCapabilityRun(t, db, coordinator, "danger_cap", agentruntime.CapabilityCallInput{
		Request: agentruntime.CapabilityRequest{
			Scope:       agentruntime.CapabilityScopeGroup,
			ChatID:      "oc_chat",
			ActorOpenID: "ou_actor",
			InputText:   "ship it",
		},
		Approval: &agentruntime.CapabilityApprovalSpec{
			Type:      "side_effect",
			Title:     "审批执行危险动作",
			Summary:   "需要审批后才可执行危险动作",
			ExpiresAt: time.Date(2026, 3, 18, 19, 30, 0, 0, time.UTC),
		},
	})

	registry := agentruntime.NewCapabilityRegistry()
	capability := &plannerTestCapability{
		meta: agentruntime.CapabilityMeta{
			Name:             "danger_cap",
			Kind:             agentruntime.CapabilityKindTool,
			SideEffectLevel:  agentruntime.SideEffectLevelExternalWrite,
			AllowedScopes:    []agentruntime.CapabilityScope{agentruntime.CapabilityScopeGroup},
			DefaultTimeout:   5 * time.Second,
			RequiresApproval: true,
		},
	}
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithCapabilityRegistry(registry))
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      run.ID,
		Revision:   run.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 18, 19, 5, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if capability.executeCount != 0 {
		t.Fatalf("execute count = %d, want 0 before approval", capability.executeCount)
	}

	updatedRun, err := agentstore.NewRunRepository(db).GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updatedRun.Status != agentruntime.RunStatusWaitingApproval {
		t.Fatalf("run status = %q, want %q", updatedRun.Status, agentruntime.RunStatusWaitingApproval)
	}

	approval, err := coordinator.LoadApprovalRequest(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("LoadApprovalRequest() error = %v", err)
	}
	if approval.Title != "审批执行危险动作" {
		t.Fatalf("approval title = %q, want 审批执行危险动作", approval.Title)
	}
}

func TestContinuationProcessorReplaysProtectedCapabilityAfterApproval(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createQueuedCapabilityRun(t, db, coordinator, "danger_cap", agentruntime.CapabilityCallInput{
		Request: agentruntime.CapabilityRequest{
			Scope:       agentruntime.CapabilityScopeGroup,
			ChatID:      "oc_chat",
			ActorOpenID: "ou_actor",
			InputText:   "ship it",
		},
		Approval: &agentruntime.CapabilityApprovalSpec{
			Type:      "side_effect",
			Title:     "审批执行危险动作",
			Summary:   "需要审批后才可执行危险动作",
			ExpiresAt: time.Date(2026, 3, 18, 19, 30, 0, 0, time.UTC),
		},
	})

	registry := agentruntime.NewCapabilityRegistry()
	capability := &plannerTestCapability{
		meta: agentruntime.CapabilityMeta{
			Name:             "danger_cap",
			Kind:             agentruntime.CapabilityKindTool,
			SideEffectLevel:  agentruntime.SideEffectLevelExternalWrite,
			AllowedScopes:    []agentruntime.CapabilityScope{agentruntime.CapabilityScopeGroup},
			DefaultTimeout:   5 * time.Second,
			RequiresApproval: true,
		},
		result: agentruntime.CapabilityResult{
			OutputText: "danger:done",
			OutputJSON: []byte(`{"ok":true}`),
		},
	}
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithCapabilityRegistry(registry))
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      run.ID,
		Revision:   run.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 18, 19, 5, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() approval request error = %v", err)
	}

	approval, err := coordinator.LoadApprovalRequest(context.Background(), run.ID, "")
	if err != nil {
		t.Fatalf("LoadApprovalRequest() error = %v", err)
	}

	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:       run.ID,
		StepID:      approval.StepID,
		Revision:    approval.Revision,
		Source:      agentruntime.ResumeSourceApproval,
		Token:       approval.Token,
		ActorOpenID: "ou_actor",
		OccurredAt:  time.Date(2026, 3, 18, 19, 10, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() approval replay error = %v", err)
	}

	if capability.executeCount != 1 {
		t.Fatalf("execute count = %d, want 1 after approval replay", capability.executeCount)
	}

	updatedRun, err := agentstore.NewRunRepository(db).GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updatedRun.Status != agentruntime.RunStatusCompleted {
		t.Fatalf("run status = %q, want %q", updatedRun.Status, agentruntime.RunStatusCompleted)
	}
	if updatedRun.CurrentStepIndex != 6 {
		t.Fatalf("current step index = %d, want 6", updatedRun.CurrentStepIndex)
	}
	if updatedRun.ResultSummary != "danger:done" {
		t.Fatalf("result summary = %q, want danger:done", updatedRun.ResultSummary)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 7 {
		t.Fatalf("step count = %d, want 7", len(steps))
	}
	if steps[1].Kind != agentruntime.StepKindCapabilityCall || steps[1].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected capability step: %+v", steps[1])
	}
	if steps[2].Kind != agentruntime.StepKindApprovalRequest || steps[2].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected approval step: %+v", steps[2])
	}
	if steps[3].Kind != agentruntime.StepKindResume || steps[3].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected resume step: %+v", steps[3])
	}
	if steps[4].Kind != agentruntime.StepKindObserve || steps[4].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected observe step: %+v", steps[4])
	}
	if steps[5].Kind != agentruntime.StepKindPlan || steps[5].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected plan step: %+v", steps[5])
	}
	if steps[6].Kind != agentruntime.StepKindReply || steps[6].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected reply step: %+v", steps[6])
	}
}

func createQueuedCapabilityRun(
	t *testing.T,
	db *gorm.DB,
	coordinator *agentruntime.RunCoordinator,
	capabilityName string,
	input agentruntime.CapabilityCallInput,
) *agentruntime.AgentRun {
	t.Helper()

	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeMention,
		TriggerMessageID: "om_capability_" + capabilityName,
		InputText:        "@bot 执行一个能力",
		Now:              time.Date(2026, 3, 18, 18, 50, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	stepRepo := agentstore.NewStepRepository(db)
	steps, err := stepRepo.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("expected initial decide step")
	}
	if _, err := stepRepo.UpdateStatus(context.Background(), steps[0].ID, agentruntime.StepStatusQueued, func(current *agentruntime.AgentStep) error {
		current.Status = agentruntime.StepStatusSkipped
		current.FinishedAt = ptrTime(time.Date(2026, 3, 18, 18, 51, 0, 0, time.UTC))
		return nil
	}); err != nil {
		t.Fatalf("UpdateStatus() initial step error = %v", err)
	}

	rawInput, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Marshal() capability input error = %v", err)
	}
	if err := stepRepo.Append(context.Background(), &agentruntime.AgentStep{
		ID:             "step_capability_" + capabilityName,
		RunID:          run.ID,
		Index:          1,
		Kind:           agentruntime.StepKindCapabilityCall,
		Status:         agentruntime.StepStatusQueued,
		CapabilityName: capabilityName,
		InputJSON:      rawInput,
		CreatedAt:      time.Date(2026, 3, 18, 18, 52, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Append() capability step error = %v", err)
	}

	updatedRun, err := agentstore.NewRunRepository(db).UpdateStatus(context.Background(), run.ID, run.Revision, func(current *agentruntime.AgentRun) error {
		current.CurrentStepIndex = 1
		current.UpdatedAt = time.Date(2026, 3, 18, 18, 52, 0, 0, time.UTC)
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateStatus() run current step error = %v", err)
	}
	return updatedRun
}

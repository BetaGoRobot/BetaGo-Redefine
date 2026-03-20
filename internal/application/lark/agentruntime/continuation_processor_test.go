package agentruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	"gorm.io/gorm"
)

func TestContinuationProcessorProcessesQueuedCallbackRun(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitingRun := createWaitingRun(t, db, coordinator, agentruntime.RunStatusWaitingCallback, agentruntime.WaitingReasonCallback, "cb_token")
	event := agentruntime.ResumeEvent{
		RunID:       waitingRun.ID,
		Revision:    waitingRun.Revision,
		Source:      agentruntime.ResumeSourceCallback,
		Token:       "cb_token",
		ActorOpenID: "ou_actor",
		OccurredAt:  time.Date(2026, 3, 18, 14, 0, 0, 0, time.UTC),
	}
	if _, err := coordinator.ResumeRun(context.Background(), event); err != nil {
		t.Fatalf("ResumeRun() error = %v", err)
	}

	processor := agentruntime.NewContinuationProcessor(coordinator)
	if err := processor.ProcessResume(context.Background(), event); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	assertRunCompletedAfterContinuation(t, db, store, waitingRun.ID, "oc_chat", agentruntime.ResumeSourceCallback)
}

func TestContinuationProcessorResumesAndProcessesWaitingScheduleRun(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitingRun := createWaitingRun(t, db, coordinator, agentruntime.RunStatusWaitingSchedule, agentruntime.WaitingReasonSchedule, "")
	event := agentruntime.ResumeEvent{
		RunID:      waitingRun.ID,
		Revision:   waitingRun.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 18, 14, 5, 0, 0, time.UTC),
	}

	processor := agentruntime.NewContinuationProcessor(coordinator)
	if err := processor.ProcessResume(context.Background(), event); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	assertRunCompletedAfterContinuation(t, db, store, waitingRun.ID, "oc_chat", agentruntime.ResumeSourceSchedule)
}

func TestContinuationProcessorEmitsGenericContinuationReplyAndPersistsReplyRefs(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitingRun := createWaitingRun(t, db, coordinator, agentruntime.RunStatusWaitingCallback, agentruntime.WaitingReasonCallback, "cb_token")
	event := agentruntime.ResumeEvent{
		RunID:       waitingRun.ID,
		Revision:    waitingRun.Revision,
		Source:      agentruntime.ResumeSourceCallback,
		Token:       "cb_token",
		ActorOpenID: "ou_actor",
		OccurredAt:  time.Date(2026, 3, 18, 14, 10, 0, 0, time.UTC),
	}
	if _, err := coordinator.ResumeRun(context.Background(), event); err != nil {
		t.Fatalf("ResumeRun() error = %v", err)
	}

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID: "om_generic_reply",
			CardID:    "card_generic_reply",
		},
	}
	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithReplyEmitter(replyEmitter))
	if err := processor.ProcessResume(context.Background(), event); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	if replyEmitter.requests[0].Run == nil || replyEmitter.requests[0].Run.ID != waitingRun.ID {
		t.Fatalf("unexpected run in reply request: %+v", replyEmitter.requests[0].Run)
	}
	if replyEmitter.requests[0].ReplyText != "收到回调了，我已经继续处理好了。" {
		t.Fatalf("reply text = %q, want %q", replyEmitter.requests[0].ReplyText, "收到回调了，我已经继续处理好了。")
	}

	assertRunCompletedAfterContinuation(t, db, store, waitingRun.ID, "oc_chat", agentruntime.ResumeSourceCallback)

	run, err := agentstore.NewRunRepository(db).GetByID(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if run.LastResponseID != "om_generic_reply" {
		t.Fatalf("last response id = %q, want om_generic_reply", run.LastResponseID)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if steps[4].Kind != agentruntime.StepKindReply || steps[4].ExternalRef != "card_generic_reply" {
		t.Fatalf("unexpected reply step: %+v", steps[4])
	}
}

func TestContinuationProcessorUsesUserFacingScheduleReplyText(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitingRun := createWaitingRun(t, db, coordinator, agentruntime.RunStatusWaitingSchedule, agentruntime.WaitingReasonSchedule, "")
	replyEmitter := &plannerTestReplyEmitter{}
	replyEmitter.result = agentruntime.ReplyEmissionResult{
		MessageID:       "om_existing_reply",
		CardID:          "card_existing_reply",
		DeliveryMode:    agentruntime.ReplyDeliveryModePatch,
		TargetMessageID: "om_existing_reply",
		TargetCardID:    "card_existing_reply",
	}
	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithReplyEmitter(replyEmitter))
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      waitingRun.ID,
		Revision:   waitingRun.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 18, 14, 12, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	if replyEmitter.requests[0].ReplyText != "定时任务跑完了，我已经继续处理好了。" {
		t.Fatalf("reply text = %q, want %q", replyEmitter.requests[0].ReplyText, "定时任务跑完了，我已经继续处理好了。")
	}
}

func TestContinuationProcessorUsesContinuationReplyTurnExecutorForScheduleWait(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitOutput, err := json.Marshal(map[string]any{
		"title": "每日摘要任务",
	})
	if err != nil {
		t.Fatalf("Marshal() wait output error = %v", err)
	}

	waitingRun := createWaitingRunWithPreviousStep(t, db, coordinator, agentruntime.RunStatusWaitingSchedule, agentruntime.WaitingReasonSchedule, "", &agentruntime.AgentStep{
		ID:          "step_wait_schedule_turn",
		Index:       1,
		Kind:        agentruntime.StepKindWait,
		Status:      agentruntime.StepStatusCompleted,
		OutputJSON:  waitOutput,
		ExternalRef: "schedule_job_daily",
		CreatedAt:   time.Date(2026, 3, 18, 13, 30, 0, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 30, 0, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 30, 0, 0, time.UTC)),
	})

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID:       "om_existing_reply",
			CardID:          "card_existing_reply",
			DeliveryMode:    agentruntime.ReplyDeliveryModePatch,
			TargetMessageID: "om_existing_reply",
			TargetCardID:    "card_existing_reply",
		},
	}
	replyTurnExecutor := &plannerTestContinuationReplyTurnExecutor{
		result: agentruntime.ContinuationReplyTurnResult{
			Executed: true,
			Plan: agentruntime.CapabilityReplyPlan{
				ThoughtText: "继续沿着定时任务结果处理原请求",
				ReplyText:   "我已经根据定时任务结果继续处理完成。",
			},
		},
	}

	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithReplyEmitter(replyEmitter),
		agentruntime.WithContinuationReplyTurnExecutor(replyTurnExecutor),
	)
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      waitingRun.ID,
		StepID:     "step_wait_schedule_turn",
		Revision:   waitingRun.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 20, 13, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyTurnExecutor.requests) != 1 {
		t.Fatalf("reply turn executor request count = %d, want 1", len(replyTurnExecutor.requests))
	}
	if replyTurnExecutor.requests[0].Source != agentruntime.ResumeSourceSchedule {
		t.Fatalf("source = %q, want %q", replyTurnExecutor.requests[0].Source, agentruntime.ResumeSourceSchedule)
	}
	if replyTurnExecutor.requests[0].PreviousStepKind != agentruntime.StepKindWait {
		t.Fatalf("previous step kind = %q, want %q", replyTurnExecutor.requests[0].PreviousStepKind, agentruntime.StepKindWait)
	}
	if replyTurnExecutor.requests[0].PreviousStepTitle != "每日摘要任务" {
		t.Fatalf("previous step title = %q, want %q", replyTurnExecutor.requests[0].PreviousStepTitle, "每日摘要任务")
	}
	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	if replyEmitter.requests[0].ThoughtText != "继续沿着定时任务结果处理原请求" {
		t.Fatalf("thought text = %q, want %q", replyEmitter.requests[0].ThoughtText, "继续沿着定时任务结果处理原请求")
	}
	if replyEmitter.requests[0].ReplyText != "我已经根据定时任务结果继续处理完成。" {
		t.Fatalf("reply text = %q, want %q", replyEmitter.requests[0].ReplyText, "我已经根据定时任务结果继续处理完成。")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 6 {
		t.Fatalf("step count = %d, want 6", len(steps))
	}
	if steps[4].Kind != agentruntime.StepKindPlan || steps[4].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected plan step: %+v", steps[4])
	}
	replyOutput := map[string]any{}
	if err := json.Unmarshal(steps[5].OutputJSON, &replyOutput); err != nil {
		t.Fatalf("json.Unmarshal(reply output) error = %v", err)
	}
	if replyOutput["thought_text"] != "继续沿着定时任务结果处理原请求" {
		t.Fatalf("thought_text = %#v, want %q", replyOutput["thought_text"], "继续沿着定时任务结果处理原请求")
	}
	if replyOutput["reply_text"] != "我已经根据定时任务结果继续处理完成。" {
		t.Fatalf("reply_text = %#v, want %q", replyOutput["reply_text"], "我已经根据定时任务结果继续处理完成。")
	}
}

func TestContinuationProcessorQueuesFollowUpPendingCapabilityFromContinuationReplyTurnExecutor(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitOutput, err := json.Marshal(map[string]any{
		"title": "日报推送任务",
	})
	if err != nil {
		t.Fatalf("Marshal() wait output error = %v", err)
	}

	waitingRun := createWaitingRunWithPreviousStep(t, db, coordinator, agentruntime.RunStatusWaitingSchedule, agentruntime.WaitingReasonSchedule, "", &agentruntime.AgentStep{
		ID:          "step_wait_schedule_follow",
		Index:       1,
		Kind:        agentruntime.StepKindWait,
		Status:      agentruntime.StepStatusCompleted,
		OutputJSON:  waitOutput,
		ExternalRef: "schedule_job_daily_follow",
		CreatedAt:   time.Date(2026, 3, 18, 13, 35, 0, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 35, 0, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 35, 0, 0, time.UTC)),
	})

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID: "om_cont_reply",
			CardID:    "card_cont_reply",
		},
	}
	replyTurnExecutor := &plannerTestContinuationReplyTurnExecutor{
		result: agentruntime.ContinuationReplyTurnResult{
			Executed: true,
			Plan: agentruntime.CapabilityReplyPlan{
				ThoughtText: "先补充查询，再发起新的消息审批",
				ReplyText:   "我已经补充查询，并发起新的发送审批。",
			},
			CapabilityCalls: []agentruntime.CompletedCapabilityCall{
				{
					CallID:             "call_nested_1",
					CapabilityName:     "search_history",
					Arguments:          `{"q":"日报"}`,
					Output:             "搜索结果",
					PreviousResponseID: "resp_nested_1",
				},
			},
			PendingCapability: &agentruntime.QueuedCapabilityCall{
				CallID:         "call_pending_2",
				CapabilityName: "send_message",
				Input: agentruntime.CapabilityCallInput{
					Request: agentruntime.CapabilityRequest{
						PayloadJSON: []byte(`{"content":"日报已更新"}`),
					},
					Approval: &agentruntime.CapabilityApprovalSpec{
						Type:    "capability",
						Title:   "审批发送日报消息",
						Summary: "等待发送审批",
					},
				},
			},
		},
	}

	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithReplyEmitter(replyEmitter),
		agentruntime.WithContinuationReplyTurnExecutor(replyTurnExecutor),
	)
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      waitingRun.ID,
		StepID:     "step_wait_schedule_follow",
		Revision:   waitingRun.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 20, 13, 5, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	updatedRun, err := agentstore.NewRunRepository(db).GetByID(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updatedRun.Status != agentruntime.RunStatusQueued {
		t.Fatalf("run status = %q, want %q", updatedRun.Status, agentruntime.RunStatusQueued)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 9 {
		t.Fatalf("step count = %d, want 9", len(steps))
	}
	if steps[4].Kind != agentruntime.StepKindCapabilityCall || steps[4].CapabilityName != "search_history" {
		t.Fatalf("unexpected nested capability step: %+v", steps[4])
	}
	capabilityInput := struct {
		Continuation *struct {
			PreviousResponseID string `json:"previous_response_id"`
		} `json:"continuation"`
	}{}
	if err := json.Unmarshal(steps[4].InputJSON, &capabilityInput); err != nil {
		t.Fatalf("json.Unmarshal(nested capability input) error = %v", err)
	}
	if capabilityInput.Continuation == nil || capabilityInput.Continuation.PreviousResponseID != "resp_nested_1" {
		t.Fatalf("nested capability continuation = %+v, want previous_response_id resp_nested_1", capabilityInput.Continuation)
	}
	if steps[5].Kind != agentruntime.StepKindObserve {
		t.Fatalf("unexpected nested observe step: %+v", steps[5])
	}
	if steps[6].Kind != agentruntime.StepKindPlan || steps[6].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected plan step: %+v", steps[6])
	}
	if steps[7].Kind != agentruntime.StepKindReply {
		t.Fatalf("unexpected reply step: %+v", steps[7])
	}
	if steps[8].Kind != agentruntime.StepKindCapabilityCall || steps[8].Status != agentruntime.StepStatusQueued || steps[8].CapabilityName != "send_message" {
		t.Fatalf("unexpected queued capability step: %+v", steps[8])
	}
}

func TestContinuationProcessorPassesResumeSummaryAndPayloadToContinuationReplyTurnExecutor(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitOutput, err := json.Marshal(map[string]any{
		"title": "日报推送任务",
	})
	if err != nil {
		t.Fatalf("Marshal() wait output error = %v", err)
	}

	waitingRun := createWaitingRunWithPreviousStep(t, db, coordinator, agentruntime.RunStatusWaitingSchedule, agentruntime.WaitingReasonSchedule, "", &agentruntime.AgentStep{
		ID:          "step_wait_schedule_payload",
		Index:       1,
		Kind:        agentruntime.StepKindWait,
		Status:      agentruntime.StepStatusCompleted,
		OutputJSON:  waitOutput,
		ExternalRef: "schedule_job_daily_payload",
		CreatedAt:   time.Date(2026, 3, 18, 13, 36, 0, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 36, 0, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 36, 0, 0, time.UTC)),
	})

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID: "om_existing_reply",
			CardID:    "card_existing_reply",
		},
	}
	replyTurnExecutor := &plannerTestContinuationReplyTurnExecutor{
		result: agentruntime.ContinuationReplyTurnResult{
			Executed: true,
			Plan: agentruntime.CapabilityReplyPlan{
				ThoughtText: "基于定时触发 payload 继续处理",
				ReplyText:   "我已经根据定时触发 payload 继续处理完成。",
			},
		},
	}

	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithReplyEmitter(replyEmitter),
		agentruntime.WithContinuationReplyTurnExecutor(replyTurnExecutor),
	)
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:       waitingRun.ID,
		StepID:      "step_wait_schedule_payload",
		Revision:    waitingRun.Revision,
		Source:      agentruntime.ResumeSourceSchedule,
		Summary:     "定时任务触发：日报窗口已到。",
		PayloadJSON: []byte(`{"task_id":"task_daily","trigger":"cron"}`),
		OccurredAt:  time.Date(2026, 3, 20, 13, 6, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyTurnExecutor.requests) != 1 {
		t.Fatalf("reply turn executor request count = %d, want 1", len(replyTurnExecutor.requests))
	}
	if replyTurnExecutor.requests[0].ResumeSummary != "定时任务触发：日报窗口已到。" {
		t.Fatalf("resume summary = %q, want %q", replyTurnExecutor.requests[0].ResumeSummary, "定时任务触发：日报窗口已到。")
	}
	if string(replyTurnExecutor.requests[0].ResumePayloadJSON) != `{"task_id":"task_daily","trigger":"cron"}` {
		t.Fatalf("resume payload = %s, want %s", string(replyTurnExecutor.requests[0].ResumePayloadJSON), `{"task_id":"task_daily","trigger":"cron"}`)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	observe := struct {
		Summary     string          `json:"summary"`
		PayloadJSON json.RawMessage `json:"payload_json"`
	}{}
	if err := json.Unmarshal(steps[3].OutputJSON, &observe); err != nil {
		t.Fatalf("json.Unmarshal(observe output) error = %v", err)
	}
	if observe.Summary != "定时任务触发：日报窗口已到。" {
		t.Fatalf("observe summary = %q, want %q", observe.Summary, "定时任务触发：日报窗口已到。")
	}
	var gotPayload map[string]any
	if err := json.Unmarshal(observe.PayloadJSON, &gotPayload); err != nil {
		t.Fatalf("json.Unmarshal(observe payload) error = %v", err)
	}
	if gotPayload["task_id"] != "task_daily" || gotPayload["trigger"] != "cron" {
		t.Fatalf("observe payload = %+v, want task_id=task_daily trigger=cron", gotPayload)
	}
}

func TestContinuationProcessorEmitsPayloadAwareContinuationThoughtAndObservation(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitingRun := createWaitingRun(t, db, coordinator, agentruntime.RunStatusWaitingCallback, agentruntime.WaitingReasonCallback, "cb_token")
	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID:       "om_existing_reply",
			CardID:          "card_existing_reply",
			DeliveryMode:    agentruntime.ReplyDeliveryModePatch,
			TargetMessageID: "om_existing_reply",
			TargetCardID:    "card_existing_reply",
		},
	}
	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithReplyEmitter(replyEmitter))
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:       waitingRun.ID,
		Revision:    waitingRun.Revision,
		Source:      agentruntime.ResumeSourceCallback,
		Token:       "cb_token",
		ActorOpenID: "ou_actor",
		OccurredAt:  time.Date(2026, 3, 18, 14, 13, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	thought := replyEmitter.requests[0].ThoughtText
	if !strings.Contains(thought, "恢复来源：回调") {
		t.Fatalf("thought text = %q, want contain %q", thought, "恢复来源：回调")
	}
	if !strings.Contains(thought, "请求上下文：@bot 帮我总结一下") {
		t.Fatalf("thought text = %q, want contain %q", thought, "请求上下文：@bot 帮我总结一下")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	observe := struct {
		Source                agentruntime.ResumeSource  `json:"source"`
		WaitingReason         agentruntime.WaitingReason `json:"waiting_reason"`
		TriggerType           agentruntime.TriggerType   `json:"trigger_type"`
		ResumeStepID          string                     `json:"resume_step_id"`
		ResumeStepExternalRef string                     `json:"resume_step_external_ref"`
		PreviousStepKind      agentruntime.StepKind      `json:"previous_step_kind"`
	}{}
	if err := json.Unmarshal(steps[2].OutputJSON, &observe); err != nil {
		t.Fatalf("json.Unmarshal(observe output) error = %v", err)
	}
	if observe.Source != agentruntime.ResumeSourceCallback {
		t.Fatalf("observe source = %q, want %q", observe.Source, agentruntime.ResumeSourceCallback)
	}
	if observe.WaitingReason != agentruntime.WaitingReasonCallback {
		t.Fatalf("observe waiting reason = %q, want %q", observe.WaitingReason, agentruntime.WaitingReasonCallback)
	}
	if observe.TriggerType != agentruntime.TriggerTypeMention {
		t.Fatalf("observe trigger type = %q, want %q", observe.TriggerType, agentruntime.TriggerTypeMention)
	}
	if observe.ResumeStepID != steps[1].ID {
		t.Fatalf("observe resume step id = %q, want %q", observe.ResumeStepID, steps[1].ID)
	}
	if observe.ResumeStepExternalRef != steps[1].ExternalRef {
		t.Fatalf("observe resume external ref = %q, want %q", observe.ResumeStepExternalRef, steps[1].ExternalRef)
	}
	if observe.PreviousStepKind != agentruntime.StepKindDecide {
		t.Fatalf("observe previous step kind = %q, want %q", observe.PreviousStepKind, agentruntime.StepKindDecide)
	}
}

func TestContinuationProcessorIncludesPreviousStepContextInThought(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitingRun := createWaitingRunWithPreviousStep(t, db, coordinator, agentruntime.RunStatusWaitingSchedule, agentruntime.WaitingReasonSchedule, "", &agentruntime.AgentStep{
		ID:          "step_wait_schedule",
		Index:       1,
		Kind:        agentruntime.StepKindWait,
		Status:      agentruntime.StepStatusCompleted,
		ExternalRef: "schedule_job_01",
		CreatedAt:   time.Date(2026, 3, 18, 13, 16, 30, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 16, 30, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 16, 30, 0, time.UTC)),
	})

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID:       "om_existing_reply",
			CardID:          "card_existing_reply",
			DeliveryMode:    agentruntime.ReplyDeliveryModePatch,
			TargetMessageID: "om_existing_reply",
			TargetCardID:    "card_existing_reply",
		},
	}
	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithReplyEmitter(replyEmitter))
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      waitingRun.ID,
		StepID:     "step_wait_schedule",
		Revision:   waitingRun.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 18, 14, 14, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	thought := replyEmitter.requests[0].ThoughtText
	if !strings.Contains(thought, "恢复来源：定时任务") {
		t.Fatalf("thought text = %q, want contain %q", thought, "恢复来源：定时任务")
	}
	if !strings.Contains(thought, "前置步骤：等待") {
		t.Fatalf("thought text = %q, want contain %q", thought, "前置步骤：等待")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	observe := struct {
		PreviousStepKind        agentruntime.StepKind `json:"previous_step_kind"`
		PreviousStepExternalRef string                `json:"previous_step_external_ref"`
	}{}
	if err := json.Unmarshal(steps[3].OutputJSON, &observe); err != nil {
		t.Fatalf("json.Unmarshal(observe output) error = %v", err)
	}
	if observe.PreviousStepKind != agentruntime.StepKindWait {
		t.Fatalf("observe previous step kind = %q, want %q", observe.PreviousStepKind, agentruntime.StepKindWait)
	}
	if observe.PreviousStepExternalRef != "schedule_job_01" {
		t.Fatalf("observe previous step external ref = %q, want %q", observe.PreviousStepExternalRef, "schedule_job_01")
	}
}

func TestContinuationProcessorTargetsLatestReplyRefsForAgenticPatch(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	replyStepOutput, err := json.Marshal(map[string]any{
		"thought_text":        "先读上下文",
		"reply_text":          "这是旧回复",
		"response_message_id": "om_existing_reply",
		"response_card_id":    "card_existing_reply",
		"delivery_mode":       string(agentruntime.ReplyDeliveryModeCreate),
		"lifecycle_state":     string(agentruntime.ReplyLifecycleStateActive),
	})
	if err != nil {
		t.Fatalf("Marshal() reply output error = %v", err)
	}

	waitingRun := createWaitingRunWithPreviousStep(t, db, coordinator, agentruntime.RunStatusWaitingCallback, agentruntime.WaitingReasonCallback, "cb_token", &agentruntime.AgentStep{
		ID:          "step_prior_reply",
		Index:       1,
		Kind:        agentruntime.StepKindReply,
		Status:      agentruntime.StepStatusCompleted,
		OutputJSON:  replyStepOutput,
		ExternalRef: "card_existing_reply",
		CreatedAt:   time.Date(2026, 3, 18, 13, 16, 30, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 16, 30, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 16, 30, 0, time.UTC)),
	})

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID:       "om_existing_reply",
			CardID:          "card_existing_reply",
			DeliveryMode:    agentruntime.ReplyDeliveryModePatch,
			TargetMessageID: "om_existing_reply",
			TargetCardID:    "card_existing_reply",
		},
	}
	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithReplyEmitter(replyEmitter))
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:       waitingRun.ID,
		Revision:    waitingRun.Revision,
		Source:      agentruntime.ResumeSourceCallback,
		Token:       "cb_token",
		ActorOpenID: "ou_actor",
		OccurredAt:  time.Date(2026, 3, 18, 14, 15, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	if replyEmitter.requests[0].TargetMessageID != "om_existing_reply" {
		t.Fatalf("target message id = %q, want %q", replyEmitter.requests[0].TargetMessageID, "om_existing_reply")
	}
	if replyEmitter.requests[0].TargetCardID != "card_existing_reply" {
		t.Fatalf("target card id = %q, want %q", replyEmitter.requests[0].TargetCardID, "card_existing_reply")
	}
	if replyEmitter.requests[0].ReplyText != "收到回调了，我已经把原消息更新好了。" {
		t.Fatalf("reply text = %q, want %q", replyEmitter.requests[0].ReplyText, "收到回调了，我已经把原消息更新好了。")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) < 2 {
		t.Fatalf("step count = %d, want at least 2", len(steps))
	}
	if steps[len(steps)-2].Kind != agentruntime.StepKindPlan {
		t.Fatalf("unexpected plan step: %+v", steps[len(steps)-2])
	}
	if steps[len(steps)-1].Kind != agentruntime.StepKindReply {
		t.Fatalf("unexpected reply step: %+v", steps[len(steps)-1])
	}
	replyOutput := map[string]any{}
	if err := json.Unmarshal(steps[len(steps)-1].OutputJSON, &replyOutput); err != nil {
		t.Fatalf("json.Unmarshal(reply output) error = %v", err)
	}
	if replyOutput["delivery_mode"] != string(agentruntime.ReplyDeliveryModePatch) {
		t.Fatalf("delivery_mode = %#v, want %q", replyOutput["delivery_mode"], agentruntime.ReplyDeliveryModePatch)
	}
	if replyOutput["target_message_id"] != "om_existing_reply" {
		t.Fatalf("target_message_id = %#v, want %q", replyOutput["target_message_id"], "om_existing_reply")
	}
	if replyOutput["target_card_id"] != "card_existing_reply" {
		t.Fatalf("target_card_id = %#v, want %q", replyOutput["target_card_id"], "card_existing_reply")
	}
	if replyOutput["target_step_id"] != "step_prior_reply" {
		t.Fatalf("target_step_id = %#v, want %q", replyOutput["target_step_id"], "step_prior_reply")
	}
	if replyOutput["lifecycle_state"] != string(agentruntime.ReplyLifecycleStateActive) {
		t.Fatalf("lifecycle_state = %#v, want %q", replyOutput["lifecycle_state"], agentruntime.ReplyLifecycleStateActive)
	}

	priorReplyOutput := map[string]any{}
	if err := json.Unmarshal(steps[1].OutputJSON, &priorReplyOutput); err != nil {
		t.Fatalf("json.Unmarshal(prior reply output) error = %v", err)
	}
	if priorReplyOutput["patched_by_step_id"] != steps[len(steps)-1].ID {
		t.Fatalf("patched_by_step_id = %#v, want %q", priorReplyOutput["patched_by_step_id"], steps[len(steps)-1].ID)
	}
	if priorReplyOutput["lifecycle_state"] != string(agentruntime.ReplyLifecycleStateSuperseded) {
		t.Fatalf("lifecycle_state = %#v, want %q", priorReplyOutput["lifecycle_state"], agentruntime.ReplyLifecycleStateSuperseded)
	}
	if priorReplyOutput["thought_text"] != "先读上下文" {
		t.Fatalf("thought_text = %#v, want %q", priorReplyOutput["thought_text"], "先读上下文")
	}
	if priorReplyOutput["reply_text"] != "这是旧回复" {
		t.Fatalf("reply_text = %#v, want %q", priorReplyOutput["reply_text"], "这是旧回复")
	}
}

func TestContinuationProcessorSupersedesPriorReplyStepWhenContinuationCreatesNewReply(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	replyStepOutput, err := json.Marshal(map[string]any{
		"thought_text":        "旧 thought",
		"reply_text":          "旧 reply",
		"response_message_id": "om_prior_reply",
		"delivery_mode":       string(agentruntime.ReplyDeliveryModeReply),
		"lifecycle_state":     string(agentruntime.ReplyLifecycleStateActive),
	})
	if err != nil {
		t.Fatalf("Marshal() reply output error = %v", err)
	}

	waitingRun := createWaitingRunWithPreviousStep(t, db, coordinator, agentruntime.RunStatusWaitingCallback, agentruntime.WaitingReasonCallback, "cb_token", &agentruntime.AgentStep{
		ID:          "step_prior_reply_create",
		Index:       1,
		Kind:        agentruntime.StepKindReply,
		Status:      agentruntime.StepStatusCompleted,
		OutputJSON:  replyStepOutput,
		ExternalRef: "om_prior_reply",
		CreatedAt:   time.Date(2026, 3, 18, 13, 17, 30, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 17, 30, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 17, 30, 0, time.UTC)),
	})

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID:    "om_new_reply",
			DeliveryMode: agentruntime.ReplyDeliveryModeReply,
		},
	}
	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithReplyEmitter(replyEmitter))
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:       waitingRun.ID,
		Revision:    waitingRun.Revision,
		Source:      agentruntime.ResumeSourceCallback,
		Token:       "cb_token",
		ActorOpenID: "ou_actor",
		OccurredAt:  time.Date(2026, 3, 18, 14, 16, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	if replyEmitter.requests[0].TargetMessageID != "om_prior_reply" {
		t.Fatalf("target message id = %q, want %q", replyEmitter.requests[0].TargetMessageID, "om_prior_reply")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) < 2 {
		t.Fatalf("step count = %d, want at least 2", len(steps))
	}
	if steps[len(steps)-2].Kind != agentruntime.StepKindPlan {
		t.Fatalf("unexpected plan step: %+v", steps[len(steps)-2])
	}
	if steps[len(steps)-1].Kind != agentruntime.StepKindReply {
		t.Fatalf("unexpected reply step: %+v", steps[len(steps)-1])
	}
	replyOutput := map[string]any{}
	if err := json.Unmarshal(steps[len(steps)-1].OutputJSON, &replyOutput); err != nil {
		t.Fatalf("json.Unmarshal(reply output) error = %v", err)
	}
	if replyOutput["response_message_id"] != "om_new_reply" {
		t.Fatalf("response_message_id = %#v, want %q", replyOutput["response_message_id"], "om_new_reply")
	}
	if replyOutput["delivery_mode"] != string(agentruntime.ReplyDeliveryModeReply) {
		t.Fatalf("delivery_mode = %#v, want %q", replyOutput["delivery_mode"], agentruntime.ReplyDeliveryModeReply)
	}
	if replyOutput["target_step_id"] != "step_prior_reply_create" {
		t.Fatalf("target_step_id = %#v, want %q", replyOutput["target_step_id"], "step_prior_reply_create")
	}
	if replyOutput["lifecycle_state"] != string(agentruntime.ReplyLifecycleStateActive) {
		t.Fatalf("lifecycle_state = %#v, want %q", replyOutput["lifecycle_state"], agentruntime.ReplyLifecycleStateActive)
	}

	priorReplyOutput := map[string]any{}
	if err := json.Unmarshal(steps[1].OutputJSON, &priorReplyOutput); err != nil {
		t.Fatalf("json.Unmarshal(prior reply output) error = %v", err)
	}
	if priorReplyOutput["superseded_by_step_id"] != steps[len(steps)-1].ID {
		t.Fatalf("superseded_by_step_id = %#v, want %q", priorReplyOutput["superseded_by_step_id"], steps[len(steps)-1].ID)
	}
	if priorReplyOutput["lifecycle_state"] != string(agentruntime.ReplyLifecycleStateSuperseded) {
		t.Fatalf("lifecycle_state = %#v, want %q", priorReplyOutput["lifecycle_state"], agentruntime.ReplyLifecycleStateSuperseded)
	}
	if priorReplyOutput["thought_text"] != "旧 thought" {
		t.Fatalf("thought_text = %#v, want %q", priorReplyOutput["thought_text"], "旧 thought")
	}
	if priorReplyOutput["reply_text"] != "旧 reply" {
		t.Fatalf("reply_text = %#v, want %q", priorReplyOutput["reply_text"], "旧 reply")
	}
	if _, exists := priorReplyOutput["patched_by_step_id"]; exists {
		t.Fatalf("patched_by_step_id should be absent for create/reply supersede, got %#v", priorReplyOutput["patched_by_step_id"])
	}
}

func TestContinuationProcessorPrefersLatestActiveReplyTarget(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitingRun := createWaitingRun(t, db, coordinator, agentruntime.RunStatusWaitingCallback, agentruntime.WaitingReasonCallback, "cb_token")
	stepRepo := agentstore.NewStepRepository(db)

	activeReplyOutput, err := json.Marshal(map[string]any{
		"response_message_id": "om_active_reply",
		"lifecycle_state":     string(agentruntime.ReplyLifecycleStateActive),
	})
	if err != nil {
		t.Fatalf("Marshal() active reply output error = %v", err)
	}
	if err := stepRepo.Append(context.Background(), &agentruntime.AgentStep{
		ID:          "step_active_reply",
		RunID:       waitingRun.ID,
		Index:       1,
		Kind:        agentruntime.StepKindReply,
		Status:      agentruntime.StepStatusCompleted,
		OutputJSON:  activeReplyOutput,
		ExternalRef: "om_active_reply",
		CreatedAt:   time.Date(2026, 3, 18, 13, 18, 0, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 18, 0, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 18, 0, 0, time.UTC)),
	}); err != nil {
		t.Fatalf("Append() active reply step error = %v", err)
	}

	supersededReplyOutput, err := json.Marshal(map[string]any{
		"response_message_id":   "om_superseded_reply",
		"lifecycle_state":       string(agentruntime.ReplyLifecycleStateSuperseded),
		"superseded_by_step_id": "step_future_reply",
	})
	if err != nil {
		t.Fatalf("Marshal() superseded reply output error = %v", err)
	}
	if err := stepRepo.Append(context.Background(), &agentruntime.AgentStep{
		ID:          "step_superseded_reply",
		RunID:       waitingRun.ID,
		Index:       2,
		Kind:        agentruntime.StepKindReply,
		Status:      agentruntime.StepStatusCompleted,
		OutputJSON:  supersededReplyOutput,
		ExternalRef: "om_superseded_reply",
		CreatedAt:   time.Date(2026, 3, 18, 13, 18, 30, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 18, 30, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 18, 30, 0, time.UTC)),
	}); err != nil {
		t.Fatalf("Append() superseded reply step error = %v", err)
	}

	waitingRun, err = agentstore.NewRunRepository(db).UpdateStatus(context.Background(), waitingRun.ID, waitingRun.Revision, func(current *agentruntime.AgentRun) error {
		current.CurrentStepIndex = 2
		current.UpdatedAt = time.Date(2026, 3, 18, 13, 18, 45, 0, time.UTC)
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateStatus() run current step error = %v", err)
	}

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID:    "om_newest_reply",
			DeliveryMode: agentruntime.ReplyDeliveryModeReply,
		},
	}
	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithReplyEmitter(replyEmitter))
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:       waitingRun.ID,
		Revision:    waitingRun.Revision,
		Source:      agentruntime.ResumeSourceCallback,
		Token:       "cb_token",
		ActorOpenID: "ou_actor",
		OccurredAt:  time.Date(2026, 3, 18, 14, 17, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	if replyEmitter.requests[0].TargetMessageID != "om_active_reply" {
		t.Fatalf("target message id = %q, want %q", replyEmitter.requests[0].TargetMessageID, "om_active_reply")
	}
}

func TestContinuationProcessorUsesApprovalTitleInGenericReplyText(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	approvalOutput, err := json.Marshal(map[string]any{
		"title": "发送群消息",
	})
	if err != nil {
		t.Fatalf("Marshal() approval output error = %v", err)
	}

	waitingRun := createWaitingRunWithPreviousStep(t, db, coordinator, agentruntime.RunStatusWaitingApproval, agentruntime.WaitingReasonApproval, "approval_token", &agentruntime.AgentStep{
		ID:          "step_prior_approval",
		Index:       1,
		Kind:        agentruntime.StepKindApprovalRequest,
		Status:      agentruntime.StepStatusCompleted,
		OutputJSON:  approvalOutput,
		ExternalRef: "approval_req_01",
		CreatedAt:   time.Date(2026, 3, 18, 13, 19, 0, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 19, 0, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 19, 0, 0, time.UTC)),
	})

	replyEmitter := &plannerTestReplyEmitter{}
	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithReplyEmitter(replyEmitter))
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:       waitingRun.ID,
		Revision:    waitingRun.Revision,
		Source:      agentruntime.ResumeSourceApproval,
		Token:       "approval_token",
		ActorOpenID: "ou_actor",
		OccurredAt:  time.Date(2026, 3, 18, 14, 18, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	if replyEmitter.requests[0].ReplyText != "审批「发送群消息」通过了，我已经继续处理好了。" {
		t.Fatalf("reply text = %q, want %q", replyEmitter.requests[0].ReplyText, "审批「发送群消息」通过了，我已经继续处理好了。")
	}
	if !strings.Contains(replyEmitter.requests[0].ThoughtText, "审批事项：发送群消息") {
		t.Fatalf("thought text = %q, want contain %q", replyEmitter.requests[0].ThoughtText, "审批事项：发送群消息")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	observe := struct {
		PreviousStepTitle string `json:"previous_step_title"`
	}{}
	if err := json.Unmarshal(steps[3].OutputJSON, &observe); err != nil {
		t.Fatalf("json.Unmarshal(observe output) error = %v", err)
	}
	if observe.PreviousStepTitle != "发送群消息" {
		t.Fatalf("observe previous_step_title = %q, want %q", observe.PreviousStepTitle, "发送群消息")
	}
}

func TestContinuationProcessorRequestsApprovalAndSendsApprovalCardForQueuedCapability(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	run := createQueuedCapabilityRun(t, db, coordinator, "send_message", agentruntime.CapabilityCallInput{
		Request: agentruntime.CapabilityRequest{
			Scope:       agentruntime.CapabilityScopeGroup,
			ChatID:      "oc_chat",
			PayloadJSON: []byte(`{"content":"hello"}`),
		},
		Approval: &agentruntime.CapabilityApprovalSpec{
			Type:      "capability",
			Title:     "审批发送消息",
			Summary:   "将向群里发送一条消息",
			ExpiresAt: time.Date(2026, 3, 18, 14, 45, 0, 0, time.UTC),
		},
	})

	capability := &plannerTestCapability{
		meta: agentruntime.CapabilityMeta{
			Name:            "send_message",
			Kind:            agentruntime.CapabilityKindTool,
			SideEffectLevel: agentruntime.SideEffectLevelChatWrite,
			AllowedScopes:   []agentruntime.CapabilityScope{agentruntime.CapabilityScopeGroup},
			DefaultTimeout:  5 * time.Second,
		},
		result: agentruntime.CapabilityResult{OutputText: "sent"},
	}
	registry := agentruntime.NewCapabilityRegistry()
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	approvalSender := &plannerTestApprovalSender{}
	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(registry),
		agentruntime.WithApprovalSender(approvalSender),
	)
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:       run.ID,
		Revision:    run.Revision,
		Source:      agentruntime.ResumeSourceSchedule,
		ActorOpenID: "ou_actor",
		OccurredAt:  time.Date(2026, 3, 18, 14, 30, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if capability.executeCount != 0 {
		t.Fatalf("execute count = %d, want 0 before approval", capability.executeCount)
	}
	if len(approvalSender.requests) != 1 {
		t.Fatalf("approval sender request count = %d, want 1", len(approvalSender.requests))
	}
	req := approvalSender.requests[0]
	if req.Target.ChatID != "oc_chat" {
		t.Fatalf("approval target chat id = %q, want %q", req.Target.ChatID, "oc_chat")
	}
	if req.Target.ReplyToMessageID != "om_capability_send_message" {
		t.Fatalf("approval reply target = %q, want %q", req.Target.ReplyToMessageID, "om_capability_send_message")
	}
	if req.Target.VisibleOpenID != "ou_actor" {
		t.Fatalf("approval visible open id = %q, want %q", req.Target.VisibleOpenID, "ou_actor")
	}
	if req.Request.Title != "审批发送消息" {
		t.Fatalf("approval title = %q, want %q", req.Request.Title, "审批发送消息")
	}
	if req.Request.CapabilityName != "send_message" {
		t.Fatalf("approval capability = %q, want %q", req.Request.CapabilityName, "send_message")
	}

	updatedRun, err := agentstore.NewRunRepository(db).GetByID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updatedRun.Status != agentruntime.RunStatusWaitingApproval {
		t.Fatalf("run status = %q, want %q", updatedRun.Status, agentruntime.RunStatusWaitingApproval)
	}
}

func TestContinuationProcessorUsesWaitTitleInCallbackGenericReplyText(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitOutput, err := json.Marshal(map[string]any{
		"title": "支付结果通知",
	})
	if err != nil {
		t.Fatalf("Marshal() wait output error = %v", err)
	}

	waitingRun := createWaitingRunWithPreviousStep(t, db, coordinator, agentruntime.RunStatusWaitingCallback, agentruntime.WaitingReasonCallback, "cb_token", &agentruntime.AgentStep{
		ID:          "step_wait_callback_title",
		Index:       1,
		Kind:        agentruntime.StepKindWait,
		Status:      agentruntime.StepStatusCompleted,
		OutputJSON:  waitOutput,
		ExternalRef: "callback_wait_01",
		CreatedAt:   time.Date(2026, 3, 18, 13, 20, 0, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 20, 0, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 20, 0, 0, time.UTC)),
	})

	replyEmitter := &plannerTestReplyEmitter{}
	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithReplyEmitter(replyEmitter))
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:       waitingRun.ID,
		StepID:      "step_wait_callback_title",
		Revision:    waitingRun.Revision,
		Source:      agentruntime.ResumeSourceCallback,
		Token:       "cb_token",
		ActorOpenID: "ou_actor",
		OccurredAt:  time.Date(2026, 3, 18, 14, 19, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	if replyEmitter.requests[0].ReplyText != "收到回调「支付结果通知」了，我已经继续处理好了。" {
		t.Fatalf("reply text = %q, want %q", replyEmitter.requests[0].ReplyText, "收到回调「支付结果通知」了，我已经继续处理好了。")
	}
	if !strings.Contains(replyEmitter.requests[0].ThoughtText, "等待事项：支付结果通知") {
		t.Fatalf("thought text = %q, want contain %q", replyEmitter.requests[0].ThoughtText, "等待事项：支付结果通知")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	observe := struct {
		PreviousStepTitle string `json:"previous_step_title"`
	}{}
	if err := json.Unmarshal(steps[3].OutputJSON, &observe); err != nil {
		t.Fatalf("json.Unmarshal(observe output) error = %v", err)
	}
	if observe.PreviousStepTitle != "支付结果通知" {
		t.Fatalf("observe previous_step_title = %q, want %q", observe.PreviousStepTitle, "支付结果通知")
	}
}

func TestContinuationProcessorUsesWaitTitleInScheduleGenericReplyText(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitOutput, err := json.Marshal(map[string]any{
		"title": "日报推送",
	})
	if err != nil {
		t.Fatalf("Marshal() wait output error = %v", err)
	}

	waitingRun := createWaitingRunWithPreviousStep(t, db, coordinator, agentruntime.RunStatusWaitingSchedule, agentruntime.WaitingReasonSchedule, "", &agentruntime.AgentStep{
		ID:          "step_wait_schedule_title",
		Index:       1,
		Kind:        agentruntime.StepKindWait,
		Status:      agentruntime.StepStatusCompleted,
		OutputJSON:  waitOutput,
		ExternalRef: "schedule_job_daily",
		CreatedAt:   time.Date(2026, 3, 18, 13, 21, 0, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 21, 0, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 21, 0, 0, time.UTC)),
	})

	replyEmitter := &plannerTestReplyEmitter{}
	processor := agentruntime.NewContinuationProcessor(coordinator, agentruntime.WithReplyEmitter(replyEmitter))
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      waitingRun.ID,
		StepID:     "step_wait_schedule_title",
		Revision:   waitingRun.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 18, 14, 20, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	if len(replyEmitter.requests) != 1 {
		t.Fatalf("reply emitter request count = %d, want 1", len(replyEmitter.requests))
	}
	if replyEmitter.requests[0].ReplyText != "定时任务「日报推送」跑完了，我已经继续处理好了。" {
		t.Fatalf("reply text = %q, want %q", replyEmitter.requests[0].ReplyText, "定时任务「日报推送」跑完了，我已经继续处理好了。")
	}
	if !strings.Contains(replyEmitter.requests[0].ThoughtText, "等待事项：日报推送") {
		t.Fatalf("thought text = %q, want contain %q", replyEmitter.requests[0].ThoughtText, "等待事项：日报推送")
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	observe := struct {
		PreviousStepTitle string `json:"previous_step_title"`
	}{}
	if err := json.Unmarshal(steps[3].OutputJSON, &observe); err != nil {
		t.Fatalf("json.Unmarshal(observe output) error = %v", err)
	}
	if observe.PreviousStepTitle != "日报推送" {
		t.Fatalf("observe previous_step_title = %q, want %q", observe.PreviousStepTitle, "日报推送")
	}
}

func TestContinuationProcessorCompletesCapabilityReplyTurnAfterDurablyRecordingNestedCapabilityTrace(t *testing.T) {
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
			InputText:   "@bot 执行一个能力",
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
			ExternalRef: "echo_ref",
		},
	}
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID: "om_capability_reply",
			CardID:    "card_capability_reply",
		},
	}
	replyTurnExecutor := &plannerTestCapabilityReplyTurnExecutor{
		execute: func(ctx context.Context, req agentruntime.CapabilityReplyTurnRequest) (agentruntime.CapabilityReplyTurnResult, error) {
			if req.Recorder == nil {
				t.Fatal("expected capability reply turn recorder")
			}
			if req.PlanRecorder == nil {
				t.Fatal("expected capability reply turn plan recorder")
			}
			if err := req.Recorder.RecordCompletedCapabilityCall(ctx, agentruntime.CompletedCapabilityCall{
				CallID:             "call_nested_capability",
				CapabilityName:     "search_history",
				Arguments:          `{"q":"日报"}`,
				Output:             "搜索结果",
				PreviousResponseID: "resp_nested_capability",
			}); err != nil {
				return agentruntime.CapabilityReplyTurnResult{}, err
			}
			return agentruntime.CapabilityReplyTurnResult{
				Executed: true,
				Plan: agentruntime.CapabilityReplyPlan{
					ThoughtText: "先补查询，再给结论",
					ReplyText:   "我已经补充查询并完成处理。",
				},
			}, nil
		},
	}

	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(registry),
		agentruntime.WithReplyEmitter(replyEmitter),
		agentruntime.WithCapabilityReplyTurnExecutor(replyTurnExecutor),
	)
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:       run.ID,
		Revision:    run.Revision,
		Source:      agentruntime.ResumeSourceSchedule,
		ActorOpenID: "ou_actor",
		OccurredAt:  time.Date(2026, 3, 20, 16, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 7 {
		t.Fatalf("step count = %d, want 7", len(steps))
	}
	if steps[2].Kind != agentruntime.StepKindObserve {
		t.Fatalf("unexpected capability observe step: %+v", steps[2])
	}
	if steps[3].Kind != agentruntime.StepKindCapabilityCall || steps[3].CapabilityName != "search_history" {
		t.Fatalf("unexpected nested capability step: %+v", steps[3])
	}
	if steps[4].Kind != agentruntime.StepKindObserve {
		t.Fatalf("unexpected nested observe step: %+v", steps[4])
	}
	if steps[5].Kind != agentruntime.StepKindPlan {
		t.Fatalf("unexpected plan step: %+v", steps[5])
	}
	if steps[6].Kind != agentruntime.StepKindReply {
		t.Fatalf("unexpected reply step: %+v", steps[6])
	}

	capabilityInput := struct {
		Continuation *struct {
			PreviousResponseID string `json:"previous_response_id"`
		} `json:"continuation"`
	}{}
	if err := json.Unmarshal(steps[3].InputJSON, &capabilityInput); err != nil {
		t.Fatalf("json.Unmarshal(nested capability input) error = %v", err)
	}
	if capabilityInput.Continuation == nil || capabilityInput.Continuation.PreviousResponseID != "resp_nested_capability" {
		t.Fatalf("nested capability continuation = %+v, want previous_response_id resp_nested_capability", capabilityInput.Continuation)
	}
}

func TestContinuationProcessorQueuesPendingCapabilityAfterDurablyRecordingContinuationNestedCapabilityTrace(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitOutput, err := json.Marshal(map[string]any{
		"title": "日报推送任务",
	})
	if err != nil {
		t.Fatalf("Marshal() wait output error = %v", err)
	}

	waitingRun := createWaitingRunWithPreviousStep(t, db, coordinator, agentruntime.RunStatusWaitingSchedule, agentruntime.WaitingReasonSchedule, "", &agentruntime.AgentStep{
		ID:          "step_wait_schedule_durable",
		Index:       1,
		Kind:        agentruntime.StepKindWait,
		Status:      agentruntime.StepStatusCompleted,
		OutputJSON:  waitOutput,
		ExternalRef: "schedule_job_daily_durable",
		CreatedAt:   time.Date(2026, 3, 18, 13, 35, 0, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 35, 0, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 35, 0, 0, time.UTC)),
	})

	replyEmitter := &plannerTestReplyEmitter{
		result: agentruntime.ReplyEmissionResult{
			MessageID: "om_cont_reply",
			CardID:    "card_cont_reply",
		},
	}
	replyTurnExecutor := &plannerTestContinuationReplyTurnExecutor{
		execute: func(ctx context.Context, req agentruntime.ContinuationReplyTurnRequest) (agentruntime.ContinuationReplyTurnResult, error) {
			if req.Recorder == nil {
				t.Fatal("expected continuation reply turn recorder")
			}
			if req.PlanRecorder == nil {
				t.Fatal("expected continuation reply turn plan recorder")
			}
			if err := req.Recorder.RecordCompletedCapabilityCall(ctx, agentruntime.CompletedCapabilityCall{
				CallID:             "call_nested_1",
				CapabilityName:     "search_history",
				Arguments:          `{"q":"日报"}`,
				Output:             "搜索结果",
				PreviousResponseID: "resp_nested_1",
			}); err != nil {
				return agentruntime.ContinuationReplyTurnResult{}, err
			}
			return agentruntime.ContinuationReplyTurnResult{
				Executed: true,
				Plan: agentruntime.CapabilityReplyPlan{
					ThoughtText: "先补充查询，再发起新的消息审批",
					ReplyText:   "我已经补充查询，并发起新的发送审批。",
				},
				PendingCapability: &agentruntime.QueuedCapabilityCall{
					CallID:         "call_pending_2",
					CapabilityName: "send_message",
					Input: agentruntime.CapabilityCallInput{
						Request: agentruntime.CapabilityRequest{
							PayloadJSON: []byte(`{"content":"日报已更新"}`),
						},
						Approval: &agentruntime.CapabilityApprovalSpec{
							Type:    "capability",
							Title:   "审批发送日报消息",
							Summary: "等待发送审批",
						},
					},
				},
			}, nil
		},
	}

	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithReplyEmitter(replyEmitter),
		agentruntime.WithContinuationReplyTurnExecutor(replyTurnExecutor),
	)
	if err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      waitingRun.ID,
		StepID:     "step_wait_schedule_durable",
		Revision:   waitingRun.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 20, 16, 5, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ProcessResume() error = %v", err)
	}

	updatedRun, err := agentstore.NewRunRepository(db).GetByID(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if updatedRun.Status != agentruntime.RunStatusQueued {
		t.Fatalf("run status = %q, want %q", updatedRun.Status, agentruntime.RunStatusQueued)
	}

	steps, err := agentstore.NewStepRepository(db).ListByRun(context.Background(), waitingRun.ID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 9 {
		t.Fatalf("step count = %d, want 9", len(steps))
	}
	if steps[4].Kind != agentruntime.StepKindCapabilityCall || steps[4].CapabilityName != "search_history" {
		t.Fatalf("unexpected nested capability step: %+v", steps[4])
	}
	if steps[5].Kind != agentruntime.StepKindObserve {
		t.Fatalf("unexpected nested observe step: %+v", steps[5])
	}
	if steps[6].Kind != agentruntime.StepKindPlan || steps[6].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected plan step: %+v", steps[6])
	}
	if steps[7].Kind != agentruntime.StepKindReply {
		t.Fatalf("unexpected reply step: %+v", steps[7])
	}
	if steps[8].Kind != agentruntime.StepKindCapabilityCall || steps[8].Status != agentruntime.StepStatusQueued || steps[8].CapabilityName != "send_message" {
		t.Fatalf("unexpected queued capability step: %+v", steps[8])
	}
}

func TestContinuationProcessorFailsRunWhenCapabilityReplyTurnReturnsError(t *testing.T) {
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
			InputText:   "@bot 执行一个能力",
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
			ExternalRef: "echo_ref",
		},
	}
	if err := registry.Register(capability); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(registry),
		agentruntime.WithCapabilityReplyTurnExecutor(&plannerTestCapabilityReplyTurnExecutor{
			err: errors.New("capability reply turn failed"),
		}),
	)
	err := processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:       run.ID,
		Revision:    run.Revision,
		Source:      agentruntime.ResumeSourceSchedule,
		ActorOpenID: "ou_actor",
		OccurredAt:  time.Date(2026, 3, 20, 16, 10, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected ProcessResume() error")
	}

	updatedRun, getErr := agentstore.NewRunRepository(db).GetByID(context.Background(), run.ID)
	if getErr != nil {
		t.Fatalf("GetByID() error = %v", getErr)
	}
	if updatedRun.Status != agentruntime.RunStatusFailed {
		t.Fatalf("run status = %q, want %q", updatedRun.Status, agentruntime.RunStatusFailed)
	}
	if !strings.Contains(updatedRun.ErrorText, "capability reply turn failed") {
		t.Fatalf("run error text = %q, want contain %q", updatedRun.ErrorText, "capability reply turn failed")
	}
}

func TestContinuationProcessorFailsRunWhenContinuationReplyTurnReturnsError(t *testing.T) {
	db := openCoordinatorTestDB(t)
	store := openCoordinatorRedisStore(t)
	coordinator := agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		store,
		botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"},
	)

	waitOutput, err := json.Marshal(map[string]any{
		"title": "日报推送任务",
	})
	if err != nil {
		t.Fatalf("Marshal() wait output error = %v", err)
	}

	waitingRun := createWaitingRunWithPreviousStep(t, db, coordinator, agentruntime.RunStatusWaitingSchedule, agentruntime.WaitingReasonSchedule, "", &agentruntime.AgentStep{
		ID:          "step_wait_schedule_fail",
		Index:       1,
		Kind:        agentruntime.StepKindWait,
		Status:      agentruntime.StepStatusCompleted,
		OutputJSON:  waitOutput,
		ExternalRef: "schedule_job_daily_fail",
		CreatedAt:   time.Date(2026, 3, 18, 13, 35, 0, 0, time.UTC),
		StartedAt:   ptrTime(time.Date(2026, 3, 18, 13, 35, 0, 0, time.UTC)),
		FinishedAt:  ptrTime(time.Date(2026, 3, 18, 13, 35, 0, 0, time.UTC)),
	})

	processor := agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithContinuationReplyTurnExecutor(&plannerTestContinuationReplyTurnExecutor{
			err: errors.New("continuation reply turn failed"),
		}),
	)
	err = processor.ProcessResume(context.Background(), agentruntime.ResumeEvent{
		RunID:      waitingRun.ID,
		StepID:     "step_wait_schedule_fail",
		Revision:   waitingRun.Revision,
		Source:     agentruntime.ResumeSourceSchedule,
		OccurredAt: time.Date(2026, 3, 20, 16, 15, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected ProcessResume() error")
	}

	updatedRun, getErr := agentstore.NewRunRepository(db).GetByID(context.Background(), waitingRun.ID)
	if getErr != nil {
		t.Fatalf("GetByID() error = %v", getErr)
	}
	if updatedRun.Status != agentruntime.RunStatusFailed {
		t.Fatalf("run status = %q, want %q", updatedRun.Status, agentruntime.RunStatusFailed)
	}
	if !strings.Contains(updatedRun.ErrorText, "continuation reply turn failed") {
		t.Fatalf("run error text = %q, want contain %q", updatedRun.ErrorText, "continuation reply turn failed")
	}
}

type plannerTestApprovalSender struct {
	requests []plannerApprovalSendRequest
}

type plannerApprovalSendRequest struct {
	Target  agentruntime.ApprovalCardTarget
	Request agentruntime.ApprovalRequest
}

func (s *plannerTestApprovalSender) SendApprovalCard(ctx context.Context, target agentruntime.ApprovalCardTarget, request agentruntime.ApprovalRequest) error {
	s.requests = append(s.requests, plannerApprovalSendRequest{
		Target:  target,
		Request: request,
	})
	return nil
}

func assertRunCompletedAfterContinuation(
	t *testing.T,
	db *gorm.DB,
	store activeChatRunStore,
	runID, chatID string,
	source agentruntime.ResumeSource,
) {
	t.Helper()

	runRepo := agentstore.NewRunRepository(db)
	stepRepo := agentstore.NewStepRepository(db)
	sessionRepo := agentstore.NewSessionRepository(db)

	run, err := runRepo.GetByID(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if run.Status != agentruntime.RunStatusCompleted {
		t.Fatalf("run status = %q, want %q", run.Status, agentruntime.RunStatusCompleted)
	}
	if run.CurrentStepIndex != 4 {
		t.Fatalf("current step index = %d, want 4", run.CurrentStepIndex)
	}
	if run.FinishedAt == nil {
		t.Fatalf("finished_at is nil: %+v", run)
	}
	if !strings.Contains(run.ResultSummary, string(source)) {
		t.Fatalf("result summary = %q, want contain %q", run.ResultSummary, source)
	}

	steps, err := stepRepo.ListByRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	if len(steps) != 5 {
		t.Fatalf("step count = %d, want 5", len(steps))
	}
	if steps[1].Kind != agentruntime.StepKindResume || steps[1].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected resume step: %+v", steps[1])
	}
	if steps[2].Kind != agentruntime.StepKindObserve || steps[2].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected observe step: %+v", steps[2])
	}
	if !strings.Contains(steps[2].ExternalRef, string(source)) {
		t.Fatalf("observe step external ref = %q, want contain %q", steps[2].ExternalRef, source)
	}
	if steps[3].Kind != agentruntime.StepKindPlan || steps[3].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected plan step: %+v", steps[3])
	}
	if steps[4].Kind != agentruntime.StepKindReply || steps[4].Status != agentruntime.StepStatusCompleted {
		t.Fatalf("unexpected reply step: %+v", steps[4])
	}

	session, err := sessionRepo.GetByID(context.Background(), run.SessionID)
	if err != nil {
		t.Fatalf("GetByID() session error = %v", err)
	}
	if session.ActiveRunID != "" {
		t.Fatalf("session active run = %q, want empty", session.ActiveRunID)
	}

	activeRunID, err := store.ActiveChatRun(context.Background(), chatID)
	if err != nil {
		t.Fatalf("ActiveChatRun() error = %v", err)
	}
	if activeRunID != "" {
		t.Fatalf("ActiveChatRun() = %q, want empty", activeRunID)
	}
}

type activeChatRunStore interface {
	ActiveChatRun(context.Context, string) (string, error)
}

func createWaitingRunWithPreviousStep(
	t *testing.T,
	db *gorm.DB,
	coordinator *agentruntime.RunCoordinator,
	status agentruntime.RunStatus,
	waitingReason agentruntime.WaitingReason,
	waitingToken string,
	step *agentruntime.AgentStep,
) *agentruntime.AgentRun {
	t.Helper()

	waitingRun := createWaitingRun(t, db, coordinator, status, waitingReason, waitingToken)
	if step == nil {
		t.Fatal("step is required")
	}
	step.RunID = waitingRun.ID

	stepRepo := agentstore.NewStepRepository(db)
	if err := stepRepo.Append(context.Background(), step); err != nil {
		t.Fatalf("Append() step error = %v", err)
	}

	updatedRun, err := agentstore.NewRunRepository(db).UpdateStatus(context.Background(), waitingRun.ID, waitingRun.Revision, func(current *agentruntime.AgentRun) error {
		current.CurrentStepIndex = step.Index
		current.UpdatedAt = time.Date(2026, 3, 18, 13, 16, 45, 0, time.UTC)
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateStatus() run current step error = %v", err)
	}
	return updatedRun
}

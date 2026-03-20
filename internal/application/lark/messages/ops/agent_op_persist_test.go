package ops

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type fakeShadowCoordinator struct {
	seen   agentruntime.StartShadowRunRequest
	result *agentruntime.AgentRun
	active *agentruntime.ActiveRunSnapshot
	err    error
}

func (f *fakeShadowCoordinator) StartShadowRun(ctx context.Context, req agentruntime.StartShadowRunRequest) (*agentruntime.AgentRun, error) {
	f.seen = req
	return f.result, f.err
}

func (f *fakeShadowCoordinator) ActiveRunSnapshot(context.Context, string) (*agentruntime.ActiveRunSnapshot, error) {
	return f.active, f.err
}

func TestAgentShadowOperatorPersistsRunIDsInMetaWhenCoordinatorIsPresent(t *testing.T) {
	fixedNow := time.Date(2026, 3, 18, 14, 0, 0, 0, time.UTC)
	observer := &fakeShadowObserver{
		result: agentruntime.ShadowObservation{
			PolicyDecision: agentruntime.PolicyDecision{
				EnterRuntime: true,
				TriggerType:  agentruntime.TriggerTypeMention,
				Reason:       "explicit_mention",
			},
			Scope:                 agentruntime.CapabilityScopeGroup,
			CandidateCapabilities: []string{"bb"},
		},
	}
	coordinator := &fakeShadowCoordinator{
		result: &agentruntime.AgentRun{
			ID:               "run_100",
			SessionID:        "session_100",
			TriggerType:      agentruntime.TriggerTypeMention,
			TriggerMessageID: "om_message",
		},
	}
	op := &AgentShadowOperator{
		now: func() time.Time { return fixedNow },
		configAccessor: func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) agentRuntimeShadowConfig {
			return fakeAgentRuntimeAccessor{shadowOnly: true}
		},
		observer:        observer,
		coordinator:     coordinator,
		mentionDetector: func(*larkim.P2MessageReceiveV1) bool { return true },
		replyToBotDetector: func(context.Context, *larkim.P2MessageReceiveV1) bool {
			return false
		},
		commandDetector: func(context.Context, *larkim.P2MessageReceiveV1) (bool, string) {
			return false, ""
		},
	}

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	msgID := "om_message"
	event.Event.Message.MessageId = &msgID
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}

	if err := op.PreRun(context.Background(), event, meta); err != nil {
		t.Fatalf("PreRun() error = %v", err)
	}
	if err := op.Run(context.Background(), event, meta); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if coordinator.seen.TriggerMessageID != msgID || coordinator.seen.ChatID != "oc_chat" {
		t.Fatalf("unexpected StartShadowRunRequest: %+v", coordinator.seen)
	}
	assertMetaExtra(t, meta, agentRuntimeShadowRunIDKey, "run_100")
	assertMetaExtra(t, meta, agentRuntimeShadowSessionIDKey, "session_100")
}

func TestAgentShadowOperatorPersistsRunIDsViaCoordinatorLoader(t *testing.T) {
	fixedNow := time.Date(2026, 3, 18, 14, 30, 0, 0, time.UTC)
	observer := &fakeShadowObserver{
		result: agentruntime.ShadowObservation{
			PolicyDecision: agentruntime.PolicyDecision{
				EnterRuntime: true,
				TriggerType:  agentruntime.TriggerTypeMention,
				Reason:       "explicit_mention",
			},
			Scope:                 agentruntime.CapabilityScopeGroup,
			CandidateCapabilities: []string{"bb"},
		},
	}
	coordinator := &fakeShadowCoordinator{
		result: &agentruntime.AgentRun{
			ID:               "run_101",
			SessionID:        "session_101",
			TriggerType:      agentruntime.TriggerTypeMention,
			TriggerMessageID: "om_message_loader",
		},
	}
	loadCalls := 0
	op := &AgentShadowOperator{
		now: func() time.Time { return fixedNow },
		configAccessor: func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) agentRuntimeShadowConfig {
			return fakeAgentRuntimeAccessor{shadowOnly: true}
		},
		observer: observer,
		coordinatorLoader: func(context.Context) agentruntime.ShadowRunStarter {
			loadCalls++
			return coordinator
		},
		mentionDetector: func(*larkim.P2MessageReceiveV1) bool { return true },
		replyToBotDetector: func(context.Context, *larkim.P2MessageReceiveV1) bool {
			return false
		},
		commandDetector: func(context.Context, *larkim.P2MessageReceiveV1) (bool, string) {
			return false, ""
		},
	}

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	msgID := "om_message_loader"
	event.Event.Message.MessageId = &msgID
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}

	if err := op.PreRun(context.Background(), event, meta); err != nil {
		t.Fatalf("PreRun() error = %v", err)
	}
	if err := op.Run(context.Background(), event, meta); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if loadCalls != 1 {
		t.Fatalf("coordinator loader called %d times, want 1", loadCalls)
	}
	assertMetaExtra(t, meta, agentRuntimeShadowRunIDKey, "run_101")
	assertMetaExtra(t, meta, agentRuntimeShadowSessionIDKey, "session_101")
}

func TestNewAgentShadowOperatorProvidesDefaultCoordinatorLoader(t *testing.T) {
	op := NewAgentShadowOperator()
	if op.coordinatorLoader == nil {
		t.Fatal("NewAgentShadowOperator() coordinatorLoader is nil")
	}
}

func TestNewAgentShadowOperatorAttachesFollowUpToActiveRun(t *testing.T) {
	fixedNow := time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)
	coordinator := &fakeShadowCoordinator{
		active: &agentruntime.ActiveRunSnapshot{
			ID:           "run_active",
			ActorOpenID:  "ou_actor",
			Status:       agentruntime.RunStatusWaitingApproval,
			LastActiveAt: fixedNow.Add(-30 * time.Second),
		},
		result: &agentruntime.AgentRun{
			ID:        "run_active",
			SessionID: "session_active",
		},
	}
	op := NewAgentShadowOperator()
	op.now = func() time.Time { return fixedNow }
	op.configAccessor = func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) agentRuntimeShadowConfig {
		return fakeAgentRuntimeAccessor{shadowOnly: true}
	}
	op.coordinator = coordinator
	op.mentionDetector = func(*larkim.P2MessageReceiveV1) bool { return false }
	op.replyToBotDetector = func(context.Context, *larkim.P2MessageReceiveV1) bool { return false }
	op.commandDetector = func(context.Context, *larkim.P2MessageReceiveV1) (bool, string) { return false, "" }

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	msgID := "om_follow_up"
	event.Event.Message.MessageId = &msgID
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}

	if err := op.PreRun(context.Background(), event, meta); err != nil {
		t.Fatalf("PreRun() error = %v", err)
	}
	if err := op.Run(context.Background(), event, meta); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if coordinator.seen.AttachToRunID != "run_active" {
		t.Fatalf("attach_to_run_id = %q, want %q", coordinator.seen.AttachToRunID, "run_active")
	}
	if coordinator.seen.SupersedeRunID != "" {
		t.Fatalf("supersede_run_id = %q, want empty", coordinator.seen.SupersedeRunID)
	}
	if coordinator.seen.TriggerType != agentruntime.TriggerTypeFollowUp {
		t.Fatalf("trigger type = %q, want %q", coordinator.seen.TriggerType, agentruntime.TriggerTypeFollowUp)
	}
	assertMetaExtra(t, meta, agentRuntimeShadowTriggerKey, string(agentruntime.TriggerTypeFollowUp))
	assertMetaExtra(t, meta, agentRuntimeShadowReasonKey, "attach_follow_up")
}

func TestNewAgentShadowOperatorSupersedesActiveRunOnMention(t *testing.T) {
	fixedNow := time.Date(2026, 3, 19, 10, 1, 0, 0, time.UTC)
	coordinator := &fakeShadowCoordinator{
		active: &agentruntime.ActiveRunSnapshot{
			ID:           "run_active",
			ActorOpenID:  "ou_other",
			Status:       agentruntime.RunStatusRunning,
			LastActiveAt: fixedNow.Add(-20 * time.Second),
		},
		result: &agentruntime.AgentRun{
			ID:        "run_new",
			SessionID: "session_new",
		},
	}
	op := NewAgentShadowOperator()
	op.now = func() time.Time { return fixedNow }
	op.configAccessor = func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) agentRuntimeShadowConfig {
		return fakeAgentRuntimeAccessor{shadowOnly: true}
	}
	op.coordinator = coordinator
	op.mentionDetector = func(*larkim.P2MessageReceiveV1) bool { return true }
	op.replyToBotDetector = func(context.Context, *larkim.P2MessageReceiveV1) bool { return false }
	op.commandDetector = func(context.Context, *larkim.P2MessageReceiveV1) (bool, string) { return false, "" }

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	msgID := "om_mention"
	event.Event.Message.MessageId = &msgID
	meta := &xhandler.BaseMetaData{ChatID: "oc_chat", OpenID: "ou_actor"}

	if err := op.PreRun(context.Background(), event, meta); err != nil {
		t.Fatalf("PreRun() error = %v", err)
	}
	if err := op.Run(context.Background(), event, meta); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if coordinator.seen.SupersedeRunID != "run_active" {
		t.Fatalf("supersede_run_id = %q, want %q", coordinator.seen.SupersedeRunID, "run_active")
	}
	if coordinator.seen.AttachToRunID != "" {
		t.Fatalf("attach_to_run_id = %q, want empty", coordinator.seen.AttachToRunID)
	}
	if coordinator.seen.TriggerType != agentruntime.TriggerTypeMention {
		t.Fatalf("trigger type = %q, want %q", coordinator.seen.TriggerType, agentruntime.TriggerTypeMention)
	}
	assertMetaExtra(t, meta, agentRuntimeShadowTriggerKey, string(agentruntime.TriggerTypeMention))
	assertMetaExtra(t, meta, agentRuntimeShadowReasonKey, "supersede_active_run")
}

package ops

import (
	"context"
	"errors"
	"testing"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type fakeAgentRuntimeAccessor struct {
	shadowOnly bool
}

func (f fakeAgentRuntimeAccessor) ChatMode() appconfig.ChatMode { return appconfig.ChatModeStandard }
func (f fakeAgentRuntimeAccessor) AgentRuntimeShadowOnly() bool { return f.shadowOnly }

type fakeShadowObserver struct {
	seen   agentruntime.ShadowObserveInput
	result agentruntime.ShadowObservation
}

func (f *fakeShadowObserver) Observe(ctx context.Context, input agentruntime.ShadowObserveInput) agentruntime.ShadowObservation {
	f.seen = input
	return f.result
}

func TestAgentShadowOperatorPreRunSkipsWithoutShadowFlag(t *testing.T) {
	op := &AgentShadowOperator{
		configAccessor: func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) agentRuntimeShadowConfig {
			return fakeAgentRuntimeAccessor{shadowOnly: false}
		},
	}

	err := op.PreRun(context.Background(), nil, &xhandler.BaseMetaData{})
	if !errors.Is(err, xerror.ErrStageSkip) {
		t.Fatalf("PreRun() error = %v, want stage skip", err)
	}
}

func TestAgentShadowOperatorRunStoresShadowDecisionInMeta(t *testing.T) {
	fixedNow := time.Date(2026, 3, 18, 10, 30, 0, 0, time.UTC)
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
	op := &AgentShadowOperator{
		now: func() time.Time { return fixedNow },
		configAccessor: func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) agentRuntimeShadowConfig {
			return fakeAgentRuntimeAccessor{shadowOnly: true}
		},
		observer:        observer,
		mentionDetector: func(*larkim.P2MessageReceiveV1) bool { return true },
		replyToBotDetector: func(context.Context, *larkim.P2MessageReceiveV1) bool {
			return false
		},
		commandDetector: func(context.Context, *larkim.P2MessageReceiveV1) (bool, string) {
			return false, ""
		},
	}

	event := testMessageEvent("group", "oc_chat", "ou_actor")
	meta := &xhandler.BaseMetaData{
		ChatID: "oc_chat",
		OpenID: "ou_actor",
	}

	if err := op.PreRun(context.Background(), event, meta); err != nil {
		t.Fatalf("PreRun() error = %v", err)
	}
	if err := op.Run(context.Background(), event, meta); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if observer.seen.Now != fixedNow {
		t.Fatalf("observer saw time %v, want %v", observer.seen.Now, fixedNow)
	}
	if observer.seen.ChatType != "group" || !observer.seen.Mentioned || observer.seen.ActorOpenID != "ou_actor" {
		t.Fatalf("unexpected observer input: %#v", observer.seen)
	}
	assertMetaExtra(t, meta, agentRuntimeShadowEnterKey, "true")
	assertMetaExtra(t, meta, agentRuntimeShadowTriggerKey, string(agentruntime.TriggerTypeMention))
	assertMetaExtra(t, meta, agentRuntimeShadowReasonKey, "explicit_mention")
	assertMetaExtra(t, meta, agentRuntimeShadowScopeKey, string(agentruntime.CapabilityScopeGroup))
	assertMetaExtra(t, meta, agentRuntimeShadowCandidatesKey, "bb")
}

func testMessageEvent(chatType, chatID, openID string) *larkim.P2MessageReceiveV1 {
	text := `{"text":"@bot 帮我总结一下"}`
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatType:    &chatType,
				ChatId:      &chatID,
				MessageType: strPtr(larkim.MsgTypeText),
				Content:     &text,
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: &openID,
				},
			},
		},
	}
}

func strPtr[T any](v T) *T {
	return &v
}

func assertMetaExtra(t *testing.T, meta *xhandler.BaseMetaData, key, want string) {
	t.Helper()
	got, ok := meta.GetExtra(key)
	if !ok {
		t.Fatalf("meta extra %q missing", key)
	}
	if got != want {
		t.Fatalf("meta extra %q = %q, want %q", key, got, want)
	}
}

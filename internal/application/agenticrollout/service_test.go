package agenticrollout

import (
	"context"
	"errors"
	"strconv"
	"testing"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
)

type rolloutStoreFake struct {
	values   map[string]bool
	applyErr error
}

func newRolloutStoreFake() *rolloutStoreFake {
	return &rolloutStoreFake{values: make(map[string]bool)}
}

func rolloutStoreKey(
	scope appconfig.ConfigScope,
	chatID string,
	key appconfig.ConfigKey,
) string {
	return string(scope) + ":" + chatID + ":" + string(key)
}

func (f *rolloutStoreFake) put(
	scope appconfig.ConfigScope,
	chatID string,
	key appconfig.ConfigKey,
	value bool,
) {
	f.values[rolloutStoreKey(scope, chatID, key)] = value
}

func (f *rolloutStoreFake) GetScopedBoolOverride(
	_ context.Context,
	key appconfig.ConfigKey,
	scope appconfig.ConfigScope,
	chatID string,
	_ string,
) (bool, bool) {
	value, ok := f.values[rolloutStoreKey(scope, chatID, key)]
	return value, ok
}

func (f *rolloutStoreFake) ApplyConfigMutations(
	_ context.Context,
	mutations []appconfig.ConfigMutation,
	guard func() error,
) error {
	if guard != nil {
		if err := guard(); err != nil {
			return err
		}
	}
	if f.applyErr != nil {
		return f.applyErr
	}
	next := make(map[string]bool, len(f.values))
	for key, value := range f.values {
		next[key] = value
	}
	for _, mutation := range mutations {
		key := rolloutStoreKey(mutation.Scope, mutation.ChatID, mutation.Key)
		if mutation.Value == nil {
			delete(next, key)
			continue
		}
		value, err := strconv.ParseBool(*mutation.Value)
		if err != nil {
			return err
		}
		next[key] = value
	}
	f.values = next
	return nil
}

func newAvailableService(store ConfigStore) *Service {
	service, err := NewService(ServiceOptions{
		Store: store,
		Static: StaticPolicies{
			EvaluationAvailable: true,
			EvaluationAllows:    func(string) bool { return false },
			AgentCardAvailable:  true,
			AgentCardAllows:     func(string) bool { return false },
		},
	})
	if err != nil {
		panic(err)
	}
	return service
}

func TestResolveChatUsesChatOverrideBeforeGlobalAndStatic(t *testing.T) {
	store := newRolloutStoreFake()
	store.put(
		appconfig.ScopeGlobal,
		"",
		appconfig.KeyConversationParallelEvaluationEnabled,
		true,
	)
	store.put(
		appconfig.ScopeChat,
		"oc_priority",
		appconfig.KeyConversationParallelEvaluationEnabled,
		false,
	)
	service := newAvailableService(store)

	state, err := service.ResolveChat(context.Background(), "oc_priority")
	if err != nil {
		t.Fatal(err)
	}
	got := state.Capability(ParallelEvaluation)
	if got.Override != OverrideDisabled ||
		got.Effective ||
		got.Baseline != true ||
		got.Source != SourceChatOverride {
		t.Fatalf("unexpected state: %#v", got)
	}
}

func TestResolveChatDefaultsRuntimeAndCallbackToInheritedOff(t *testing.T) {
	service := newAvailableService(newRolloutStoreFake())

	state, err := service.ResolveChat(context.Background(), "oc_default")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []Capability{
		ConversationRuntime,
		CallbackContinuation,
	} {
		got := state.Capability(key)
		if got.Override != OverrideInherit ||
			got.Baseline ||
			got.Effective ||
			got.Source != SourceDefault ||
			!got.Available {
			t.Fatalf("%s state = %#v", key, got)
		}
	}
}

func TestResolveChatUsesStaticPoliciesAndStableRevision(t *testing.T) {
	store := newRolloutStoreFake()
	service, err := NewService(ServiceOptions{
		Store: store,
		Static: StaticPolicies{
			EvaluationAvailable: true,
			EvaluationAllows: func(chatID string) bool {
				return chatID == "oc_static"
			},
			AgentCardAvailable: true,
			AgentCardAllows: func(chatID string) bool {
				return chatID == "oc_static"
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := service.ResolveChat(context.Background(), "oc_static")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ResolveChat(context.Background(), "oc_static")
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision == "" || first.Revision != second.Revision {
		t.Fatalf("revisions = %q and %q", first.Revision, second.Revision)
	}
	for _, key := range []Capability{ParallelEvaluation, AgentCard} {
		got := first.Capability(key)
		if !got.Baseline || !got.Effective || got.Source != SourceTOML {
			t.Fatalf("%s state = %#v", key, got)
		}
	}
}

func TestResolveChatFailsClosedWhenAgentCardIsUnavailable(t *testing.T) {
	store := newRolloutStoreFake()
	store.put(
		appconfig.ScopeChat,
		"oc_shadow",
		appconfig.ConfigKey("agent_card_enabled"),
		true,
	)
	service, err := NewService(ServiceOptions{
		Store: store,
		Static: StaticPolicies{
			EvaluationAvailable:        true,
			EvaluationAllows:           func(string) bool { return false },
			AgentCardAvailable:         false,
			AgentCardAllows:            func(string) bool { return false },
			AgentCardUnavailableReason: "agent_card_shadow_mode",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	state, err := service.ResolveChat(context.Background(), "oc_shadow")
	if err != nil {
		t.Fatal(err)
	}
	got := state.Capability(AgentCard)
	if got.Available || got.Effective ||
		got.Override != OverrideEnabled ||
		got.Reason != "agent_card_shadow_mode" {
		t.Fatalf("state = %#v", got)
	}
}

func TestApplyDeletesOverrideWhenRestoringInherit(t *testing.T) {
	store := newRolloutStoreFake()
	store.put(
		appconfig.ScopeChat,
		"oc_inherit",
		appconfig.KeyConversationRuntimeEnabled,
		true,
	)
	service := newAvailableService(store)
	before, err := service.ResolveChat(context.Background(), "oc_inherit")
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Apply(context.Background(), BatchRequest{
		ChatIDs: []string{"oc_inherit"},
		ExpectedRevisions: map[string]string{
			"oc_inherit": before.Revision,
		},
		Changes: ChangeSet{
			ConversationRuntime: OverrideInherit,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %d", len(result.Items))
	}
	after := result.Items[0].After.Capability(ConversationRuntime)
	if after.Override != OverrideInherit || after.Effective {
		t.Fatalf("after = %#v", after)
	}
	if _, configured := store.GetScopedBoolOverride(
		context.Background(),
		appconfig.KeyConversationRuntimeEnabled,
		appconfig.ScopeChat,
		"oc_inherit",
		"",
	); configured {
		t.Fatal("inherit left a persisted override")
	}
}

func TestApplyRejectsStaleRevisionWithoutMutation(t *testing.T) {
	store := newRolloutStoreFake()
	service := newAvailableService(store)

	_, err := service.Apply(context.Background(), BatchRequest{
		ChatIDs: []string{"oc_stale"},
		ExpectedRevisions: map[string]string{
			"oc_stale": "sha256:stale",
		},
		Changes: ChangeSet{
			ConversationRuntime: OverrideEnabled,
		},
	})
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("error = %v, want stale revision", err)
	}
	if _, configured := store.GetScopedBoolOverride(
		context.Background(),
		appconfig.KeyConversationRuntimeEnabled,
		appconfig.ScopeChat,
		"oc_stale",
		"",
	); configured {
		t.Fatal("stale request mutated the store")
	}
}

func TestApplyRejectsUnavailableEnableWithoutMutation(t *testing.T) {
	store := newRolloutStoreFake()
	service, err := NewService(ServiceOptions{
		Store: store,
		Static: StaticPolicies{
			EvaluationAllows:           func(string) bool { return false },
			AgentCardAllows:            func(string) bool { return false },
			AgentCardUnavailableReason: "agent_card_off",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := service.ResolveChat(context.Background(), "oc_unavailable")
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Apply(context.Background(), BatchRequest{
		ChatIDs: []string{"oc_unavailable"},
		ExpectedRevisions: map[string]string{
			"oc_unavailable": before.Revision,
		},
		Changes: ChangeSet{AgentCard: OverrideEnabled},
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want unavailable", err)
	}
}

func TestApplyIsAtomicAcrossChatsWhenPersistenceFails(t *testing.T) {
	store := newRolloutStoreFake()
	service := newAvailableService(store)
	first, _ := service.ResolveChat(context.Background(), "oc_first")
	second, _ := service.ResolveChat(context.Background(), "oc_second")
	store.applyErr = errors.New("database unavailable")

	_, err := service.Apply(context.Background(), BatchRequest{
		ChatIDs: []string{"oc_first", "oc_second"},
		ExpectedRevisions: map[string]string{
			"oc_first":  first.Revision,
			"oc_second": second.Revision,
		},
		Changes: ChangeSet{
			ConversationRuntime: OverrideEnabled,
		},
	})
	if err == nil {
		t.Fatal("expected persistence error")
	}
	for _, chatID := range []string{"oc_first", "oc_second"} {
		if _, configured := store.GetScopedBoolOverride(
			context.Background(),
			appconfig.KeyConversationRuntimeEnabled,
			appconfig.ScopeChat,
			chatID,
			"",
		); configured {
			t.Fatalf("%s mutated after failed batch", chatID)
		}
	}
}

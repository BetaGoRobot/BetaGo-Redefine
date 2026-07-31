package config

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
)

type mutationStoreFake struct {
	err     error
	applied [][]persistedConfigMutation
	mu      sync.Mutex
	onApply func()
}

func (f *mutationStoreFake) Apply(
	_ context.Context,
	mutations []persistedConfigMutation,
) error {
	copied := append([]persistedConfigMutation(nil), mutations...)
	f.mu.Lock()
	f.applied = append(f.applied, copied)
	f.mu.Unlock()
	if f.onApply != nil {
		f.onApply()
	}
	return f.err
}

func TestBuildConfigKeyUsesBotNamespace(t *testing.T) {
	oldIdentity := currentBotIdentity
	currentBotIdentity = func() botidentity.Identity {
		return botidentity.Identity{
			AppID:     "cli_test_app",
			BotOpenID: "ou_test_bot",
		}
	}
	defer func() { currentBotIdentity = oldIdentity }()

	got := buildConfigKey(ScopeUser, "oc_test_chat", "ou_test_user", KeyRepeatDefaultRate)
	want := "bot:cli_test_app:ou_test_bot:user:oc_test_chat:ou_test_user:repeat_default_rate"
	if got != want {
		t.Fatalf("buildConfigKey() = %q, want %q", got, want)
	}
}

func TestBuildConfigKeyKeepsLegacyFormatWithoutBotIdentity(t *testing.T) {
	oldIdentity := currentBotIdentity
	currentBotIdentity = func() botidentity.Identity { return botidentity.Identity{} }
	defer func() { currentBotIdentity = oldIdentity }()

	got := buildConfigKey(ScopeChat, "oc_test_chat", "", KeyIntentRecognitionEnabled)
	want := "chat:oc_test_chat:intent_recognition_enabled"
	if got != want {
		t.Fatalf("buildConfigKey() = %q, want %q", got, want)
	}
}

func TestBuildFeatureBlockKeyUsesBotNamespace(t *testing.T) {
	oldIdentity := currentBotIdentity
	currentBotIdentity = func() botidentity.Identity {
		return botidentity.Identity{
			AppID:     "cli_test_app",
			BotOpenID: "ou_test_bot",
		}
	}
	defer func() { currentBotIdentity = oldIdentity }()

	got := buildFeatureBlockKey(ScopeChat, "oc_test_chat", "", "send_message")
	want := "bot:cli_test_app:ou_test_bot:feature_block:chat:oc_test_chat:send_message"
	if got != want {
		t.Fatalf("buildFeatureBlockKey() = %q, want %q", got, want)
	}
}

func TestGetStringFallsBackToToml(t *testing.T) {
	oldIdentity := currentBotIdentity
	oldConfig := currentBaseConfig
	currentBotIdentity = func() botidentity.Identity { return botidentity.Identity{} }
	currentBaseConfig = func() *infraConfig.BaseConfig {
		return &infraConfig.BaseConfig{
			ArkConfig: &infraConfig.ArkConfig{
				ReasoningModel: "deep-reasoner",
				NormalModel:    "fast-chat",
				LiteModel:      "intent-lite",
			},
			OpensearchConfig: &infraConfig.OpensearchConfig{
				LarkCardActionIndex: "card-action-index",
				LarkMsgIndex:        "msg-index",
				LarkChunkIndex:      "chunk-index",
			},
		}
	}
	defer func() {
		currentBotIdentity = oldIdentity
		currentBaseConfig = oldConfig
	}()

	manager := NewManager()

	if got := manager.GetString(context.Background(), KeyChatReasoningModel, "", ""); got != "deep-reasoner" {
		t.Fatalf("GetString(reasoning) = %q, want %q", got, "deep-reasoner")
	}
	if got := manager.GetString(context.Background(), KeyChatNormalModel, "", ""); got != "fast-chat" {
		t.Fatalf("GetString(normal) = %q, want %q", got, "fast-chat")
	}
	if got := manager.GetString(context.Background(), KeyIntentLiteModel, "", ""); got != "intent-lite" {
		t.Fatalf("GetString(intent) = %q, want %q", got, "intent-lite")
	}
	if got := manager.GetString(context.Background(), KeyLarkMsgIndex, "", ""); got != "msg-index" {
		t.Fatalf("GetString(msg index) = %q, want %q", got, "msg-index")
	}
	if got := manager.GetString(context.Background(), KeyLarkChunkIndex, "", ""); got != "chunk-index" {
		t.Fatalf("GetString(chunk index) = %q, want %q", got, "chunk-index")
	}
	if got := manager.GetString(context.Background(), ConfigKey("lark_card_action_index"), "", ""); got != "card-action-index" {
		t.Fatalf("GetString(card action index) = %q, want %q", got, "card-action-index")
	}
}

func TestGetBoolFallsBackToTomlForBusinessFlags(t *testing.T) {
	oldIdentity := currentBotIdentity
	oldConfig := currentBaseConfig
	currentBotIdentity = func() botidentity.Identity { return botidentity.Identity{} }
	currentBaseConfig = func() *infraConfig.BaseConfig {
		return &infraConfig.BaseConfig{
			NeteaseMusicConfig: &infraConfig.NeteaseMusicConfig{
				MusicCardInThread: true,
			},
			LarkConfig: &infraConfig.LarkConfig{
				WithDrawReplace: true,
			},
		}
	}
	defer func() {
		currentBotIdentity = oldIdentity
		currentBaseConfig = oldConfig
	}()

	manager := NewManager()

	if !manager.GetBool(context.Background(), KeyMusicCardInThread, "", "") {
		t.Fatal("expected music_card_in_thread TOML fallback to be true")
	}
	if !manager.GetBool(context.Background(), KeyWithDrawReplace, "", "") {
		t.Fatal("expected with_draw_replace TOML fallback to be true")
	}
}

func TestParseConfigKeySupportsBotNamespace(t *testing.T) {
	entry, ok := parseConfigKey(
		"bot:cli_test_app:ou_test_bot:user:oc_test_chat:ou_test_user:repeat_default_rate",
		"42",
	)
	if !ok {
		t.Fatal("expected parseConfigKey to succeed")
	}
	if entry.Scope != ScopeUser || entry.ChatID != "oc_test_chat" || entry.OpenID != "ou_test_user" {
		t.Fatalf("unexpected entry scope/chat/user: %+v", entry)
	}
	if entry.Key != KeyRepeatDefaultRate || entry.Value != "42" {
		t.Fatalf("unexpected entry key/value: %+v", entry)
	}
}

func TestGetAllConfigKeysIncludesAccessorBackedKeys(t *testing.T) {
	keys := GetAllConfigKeys()
	set := make(map[ConfigKey]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}

	expected := []ConfigKey{
		KeyChunkEnabled,
		KeyMusicCardInThread,
		KeyWithDrawReplace,
		KeyChatMode,
		KeyChatReasoningModel,
		KeyChatNormalModel,
		KeyIntentLiteModel,
		KeyLarkMsgIndex,
		KeyLarkChunkIndex,
	}
	for _, key := range expected {
		if _, ok := set[key]; !ok {
			t.Fatalf("expected config key %q in GetAllConfigKeys()", key)
		}
	}
}

func TestGetAllConfigKeysIncludesStartupOnlyKeys(t *testing.T) {
	keys := GetAllConfigKeys()
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[string(key)] = struct{}{}
	}

	expected := []string{
		"lark_card_action_index",
	}
	for _, key := range expected {
		if _, ok := set[key]; !ok {
			t.Fatalf("expected startup-only config key %q in GetAllConfigKeys()", key)
		}
	}
}

func TestChunkEnabledDefaultsToTrue(t *testing.T) {
	manager := NewManager()
	if !manager.GetBool(context.Background(), KeyChunkEnabled, "", "") {
		t.Fatal("expected chunk_enabled default to be true")
	}
}

func TestConversationRuntimeFlagsDefaultOffAndHonorChatScope(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()
	accessor := NewAccessorWithManager(ctx, "chat-enabled", "", manager)

	if accessor.ConversationRuntimeEnabled() ||
		accessor.ConversationCallbackContinuationEnabled() ||
		accessor.ConversationParallelEvaluationEnabled() {
		t.Fatal("conversation runtime flags must default off")
	}
	manager.cache[buildConfigKey(ScopeChat, "chat-enabled", "", KeyConversationRuntimeEnabled)] = "true"
	manager.cache[buildConfigKey(ScopeChat, "chat-enabled", "", KeyConversationCallbackContinuationEnabled)] = "true"
	manager.cache[buildConfigKey(ScopeChat, "chat-enabled", "", KeyConversationParallelEvaluationEnabled)] = "true"
	if !accessor.ConversationRuntimeEnabled() ||
		!accessor.ConversationCallbackContinuationEnabled() ||
		!accessor.ConversationParallelEvaluationEnabled() {
		t.Fatal("conversation runtime flags did not honor chat scope")
	}
	for _, key := range []ConfigKey{
		KeyConversationRuntimeEnabled,
		KeyConversationCallbackContinuationEnabled,
		KeyConversationParallelEvaluationEnabled,
	} {
		def, ok := GetConfigDefinition(key)
		if !ok || def.ValueType != "bool" {
			t.Fatalf("definition %q = %#v, %v", key, def, ok)
		}
	}
}

func TestAgenticRolloutDefinitionsUseDedicatedManagementSurface(t *testing.T) {
	for _, key := range []ConfigKey{
		KeyConversationRuntimeEnabled,
		KeyConversationCallbackContinuationEnabled,
		KeyConversationParallelEvaluationEnabled,
		KeyAgentCardEnabled,
	} {
		def, ok := GetConfigDefinition(key)
		if !ok || def.ValueType != "bool" ||
			def.ManagementSurface != ManagementSurfaceAgenticRollout {
			t.Fatalf("definition %q = %#v, %v", key, def, ok)
		}
	}
}

func TestGetBoolOverrideDistinguishesExplicitFalseFromMissing(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()
	if value, configured := manager.GetBoolOverride(
		ctx,
		KeyConversationParallelEvaluationEnabled,
		"chat-a",
		"",
	); configured || value {
		t.Fatalf("missing override = %v, %v", value, configured)
	}
	manager.cache[buildConfigKey(
		ScopeChat,
		"chat-a",
		"",
		KeyConversationParallelEvaluationEnabled,
	)] = "false"
	if value, configured := manager.GetBoolOverride(
		ctx,
		KeyConversationParallelEvaluationEnabled,
		"chat-a",
		"",
	); !configured || value {
		t.Fatalf("explicit false override = %v, %v", value, configured)
	}
	manager.cache[buildConfigKey(
		ScopeGlobal,
		"",
		"",
		KeyConversationParallelEvaluationEnabled,
	)] = "true"
	if value, configured := manager.GetBoolOverride(
		ctx,
		KeyConversationParallelEvaluationEnabled,
		"chat-b",
		"",
	); !configured || !value {
		t.Fatalf("global true override = %v, %v", value, configured)
	}
}

func TestConversationRuntimeGlobalHelpersDefaultOff(t *testing.T) {
	if IsConversationRuntimeEnabled(context.Background(), "chat-default", "") ||
		IsConversationCallbackContinuationEnabled(context.Background(), "chat-default", "") ||
		IsConversationParallelEvaluationEnabled(context.Background(), "chat-default", "") {
		t.Fatal("global conversation flags must default off")
	}
}

func TestConversationRuntimeChatActivationIgnoresUserOnlyOverride(t *testing.T) {
	manager := NewManager()
	manager.cache[buildConfigKey(
		ScopeUser,
		"chat-user-only",
		"user-enabled",
		KeyConversationRuntimeEnabled,
	)] = "true"

	if manager.GetBool(
		context.Background(),
		KeyConversationRuntimeEnabled,
		"chat-user-only",
		"",
	) {
		t.Fatal("user-only runtime override enabled a chat-level activation lookup")
	}
}

func TestGetScopedBoolOverrideDistinguishesAbsentAndFalse(t *testing.T) {
	manager := newManagerWithMutationStore(&mutationStoreFake{})
	manager.cache[buildConfigKey(
		ScopeChat,
		"oc_scoped_false",
		"",
		KeyConversationRuntimeEnabled,
	)] = "false"

	got, configured := manager.GetScopedBoolOverride(
		context.Background(),
		KeyConversationRuntimeEnabled,
		ScopeChat,
		"oc_scoped_false",
		"",
	)
	if !configured || got {
		t.Fatalf("got (%v, %v), want (false, true)", got, configured)
	}
	if got, configured = manager.GetScopedBoolOverride(
		context.Background(),
		KeyConversationRuntimeEnabled,
		ScopeChat,
		"oc_missing",
		"",
	); configured || got {
		t.Fatalf("missing got (%v, %v), want (false, false)", got, configured)
	}
}

func TestApplyConfigMutationsKeepsCacheUnchangedOnRollback(t *testing.T) {
	store := &mutationStoreFake{err: errors.New("write failed")}
	manager := newManagerWithMutationStore(store)
	value := "true"

	err := manager.ApplyConfigMutations(
		context.Background(),
		[]ConfigMutation{{
			Key:    KeyConversationRuntimeEnabled,
			Scope:  ScopeChat,
			ChatID: "oc_rollback",
			Value:  &value,
		}},
		nil,
	)
	if err == nil {
		t.Fatal("expected persistence error")
	}
	if _, ok := manager.cache[buildConfigKey(
		ScopeChat,
		"oc_rollback",
		"",
		KeyConversationRuntimeEnabled,
	)]; ok {
		t.Fatal("cache changed before transaction commit")
	}
	if len(store.applied) != 1 {
		t.Fatalf("apply calls = %d, want 1", len(store.applied))
	}
}

func TestApplyConfigMutationsDeletesCacheOnlyAfterCommit(t *testing.T) {
	manager := newManagerWithMutationStore(&mutationStoreFake{})
	fullKey := buildConfigKey(
		ScopeChat,
		"oc_inherit",
		"",
		KeyConversationRuntimeEnabled,
	)
	manager.cache[fullKey] = "true"

	err := manager.ApplyConfigMutations(
		context.Background(),
		[]ConfigMutation{{
			Key:    KeyConversationRuntimeEnabled,
			Scope:  ScopeChat,
			ChatID: "oc_inherit",
			Value:  nil,
		}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.cache[fullKey]; ok {
		t.Fatal("delete mutation must restore inherit")
	}
}

func TestApplyConfigMutationsStopsBeforeStoreWhenGuardFails(t *testing.T) {
	store := &mutationStoreFake{}
	manager := newManagerWithMutationStore(store)
	value := "true"
	guardErr := errors.New("stale revision")

	err := manager.ApplyConfigMutations(
		context.Background(),
		[]ConfigMutation{{
			Key: KeyConversationRuntimeEnabled, Scope: ScopeChat,
			ChatID: "oc_guard", Value: &value,
		}},
		func() error { return guardErr },
	)
	if !errors.Is(err, guardErr) {
		t.Fatalf("error = %v, want guard error", err)
	}
	if len(store.applied) != 0 {
		t.Fatal("store called after guard failure")
	}
}

func TestApplyConfigMutationsRejectsDuplicateKeys(t *testing.T) {
	store := &mutationStoreFake{}
	manager := newManagerWithMutationStore(store)
	value := "true"
	mutation := ConfigMutation{
		Key: KeyConversationRuntimeEnabled, Scope: ScopeChat,
		ChatID: "oc_duplicate", Value: &value,
	}

	err := manager.ApplyConfigMutations(
		context.Background(),
		[]ConfigMutation{mutation, mutation},
		nil,
	)
	if err == nil {
		t.Fatal("expected duplicate mutation error")
	}
	if len(store.applied) != 0 {
		t.Fatal("duplicate mutations reached persistence")
	}
}

func TestApplyConfigMutationsSerializesWriters(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	store := &mutationStoreFake{
		onApply: func() {
			current := active.Add(1)
			for {
				previous := maximum.Load()
				if current <= previous || maximum.CompareAndSwap(previous, current) {
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
			active.Add(-1)
		},
	}
	manager := newManagerWithMutationStore(store)
	var wg sync.WaitGroup
	for index := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value := "true"
			if err := manager.ApplyConfigMutations(
				context.Background(),
				[]ConfigMutation{{
					Key: KeyConversationRuntimeEnabled, Scope: ScopeChat,
					ChatID: "oc_serial_" + strconv.Itoa(index), Value: &value,
				}},
				nil,
			); err != nil {
				t.Errorf("ApplyConfigMutations: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent writers = %d, want 1", got)
	}
}

func TestConfigDefaultDisplayValueSupportsStringDefaults(t *testing.T) {
	oldConfig := currentBaseConfig
	currentBaseConfig = func() *infraConfig.BaseConfig {
		return &infraConfig.BaseConfig{
			ArkConfig: &infraConfig.ArkConfig{
				ReasoningModel: "deep-reasoner",
			},
		}
	}
	defer func() {
		currentBaseConfig = oldConfig
	}()

	manager := NewManager()
	if got := configDefaultDisplayValue(manager, KeyChatReasoningModel); got != "deep-reasoner" {
		t.Fatalf("configDefaultDisplayValue() = %q, want %q", got, "deep-reasoner")
	}
}

func TestGetConfigEnumOptionsBuildsCandidatesFromBaseConfig(t *testing.T) {
	oldConfig := currentBaseConfig
	currentBaseConfig = func() *infraConfig.BaseConfig {
		return &infraConfig.BaseConfig{
			ArkConfig: &infraConfig.ArkConfig{
				ReasoningModel: "deep-reasoner",
				NormalModel:    "fast-chat",
				LiteModel:      "intent-lite",
			},
			OpensearchConfig: &infraConfig.OpensearchConfig{
				LarkMsgIndex:   "lark_msg_index_jieba",
				LarkChunkIndex: "lark_chunk_index_jieba",
			},
		}
	}
	defer func() {
		currentBaseConfig = oldConfig
	}()

	modelOptions := GetConfigEnumOptions(KeyChatReasoningModel, "")
	if len(modelOptions) != 3 {
		t.Fatalf("expected 3 model options, got %+v", modelOptions)
	}

	indexOptions := GetConfigEnumOptions(KeyLarkMsgIndex, "")
	if len(indexOptions) != 2 {
		t.Fatalf("expected 2 index options, got %+v", indexOptions)
	}

	modeOptions := GetConfigEnumOptions(KeyChatMode, "")
	if len(modeOptions) != 1 {
		t.Fatalf("expected 1 chat mode option, got %+v", modeOptions)
	}
	if modeOptions[0].Value != string(ChatModeStandard) {
		t.Fatalf("unexpected chat mode options: %+v", modeOptions)
	}
}

func TestGetStringFallsBackToDefaultForChatMode(t *testing.T) {
	manager := NewManager()
	if got := manager.GetString(context.Background(), KeyChatMode, "", ""); got != string(ChatModeStandard) {
		t.Fatalf("GetString(chat mode) = %q, want %q", got, ChatModeStandard)
	}
}

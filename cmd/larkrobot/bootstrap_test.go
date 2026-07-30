package main

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
)

func TestAddInfrastructureModulesRegistersAKShareAPIModule(t *testing.T) {
	app := appruntime.NewApp()

	addInfrastructureModules(app, &infraConfig.BaseConfig{})

	snapshot := app.Registry().Snapshot()
	if hasComponent(snapshot.Components, "aktool") {
		t.Fatalf("unexpected aktool module in registry: %+v", snapshot.Components)
	}
	if !hasComponent(snapshot.Components, "akshareapi") {
		t.Fatalf("expected akshareapi module in registry: %+v", snapshot.Components)
	}
}

func TestNewAppComponentsBuildsConversationRuntimeBeforeLateBinding(t *testing.T) {
	cfg := testConversationRuntimeConfig()
	components, err := newAppComponents(cfg)
	if err != nil {
		t.Fatalf("newAppComponents() error = %v", err)
	}
	if components.conversationExecutor == nil ||
		components.projectionExecutor == nil ||
		components.conversationRuntime == nil ||
		components.conversationWorker == nil ||
		components.conversationProjectionWorker == nil ||
		components.continuationDispatcher == nil ||
		components.feedbackRouter == nil {
		t.Fatalf("conversation components are incomplete: %#v", components)
	}
	if components.conversationWorker.Critical() ||
		components.conversationProjectionWorker.Critical() {
		t.Fatal("dynamic conversation and OpenSearch projection workers must be non-critical")
	}
	if _, err := components.conversationRuntime.StartScheduleEdit(context.Background(), agentruntime.StartScheduleEditRequest{}); err == nil {
		t.Fatal("conversation runtime should remain unbound before application_services starts")
	}
}

func TestNewAppComponentsRejectsUnsafeConversationBudgets(t *testing.T) {
	cfg := testConversationRuntimeConfig()
	cfg.RuntimeConfig = &infraConfig.RuntimeConfig{
		ConversationTimeoutSeconds:           180,
		ConversationProjectionTimeoutSeconds: 120,
	}
	if _, err := newAppComponents(cfg); err == nil {
		t.Fatal("newAppComponents() accepted executor timeouts that reach their durable leases")
	}
}

func TestBuildAppAllowsMissingArkConfigWhenRuntimeIsDisabledByDefault(t *testing.T) {
	cfg := testConversationRuntimeConfig()
	cfg.ArkConfig = nil
	if _, err := buildApp(cfg); err != nil {
		t.Fatalf("buildApp() with disabled-by-default runtime and nil Ark config error = %v", err)
	}
}

func TestConversationModulesAreRegisteredAfterExecutors(t *testing.T) {
	cfg := testConversationRuntimeConfig()
	app, err := buildApp(cfg)
	if err != nil {
		t.Fatalf("buildApp() error = %v", err)
	}
	snapshot := app.Registry().Snapshot()
	for _, name := range []string{
		"conversation_executor",
		"conversation_projection_executor",
		"conversation_runtime_worker",
		"conversation_projection_worker",
	} {
		if !hasComponent(snapshot.Components, name) {
			t.Fatalf("missing runtime component %q: %+v", name, snapshot.Components)
		}
	}
	names := app.ModuleNames()
	assertModuleBefore(t, names, "conversation_executor", "application_services")
	assertModuleBefore(t, names, "conversation_projection_executor", "application_services")
	assertModuleBefore(t, names, "application_services", "conversation_runtime_worker")
	assertModuleBefore(t, names, "application_services", "conversation_projection_worker")
	assertModuleBefore(t, names, "conversation_projection_worker", "conversation_evaluation")
	assertModuleBefore(t, names, "conversation_evaluation", "lark_ws")
	assertModuleBefore(t, names, "conversation_runtime_worker", "lark_ws")
	assertModuleBefore(t, names, "conversation_projection_worker", "lark_ws")
}

func testConversationRuntimeConfig() *infraConfig.BaseConfig {
	return &infraConfig.BaseConfig{
		LarkConfig: &infraConfig.LarkConfig{
			AppID: "app-test", AppSecret: "secret-test", BotOpenID: "bot-test",
		},
		ArkConfig: &infraConfig.ArkConfig{
			NormalModel: "normal-test", ReasoningModel: "reasoning-test",
		},
		RuntimeConfig: &infraConfig.RuntimeConfig{
			ConversationTimeoutSeconds:           int((2 * time.Minute) / time.Second),
			ConversationProjectionTimeoutSeconds: int(time.Minute / time.Second),
		},
	}
}

func hasComponent(items []appruntime.ComponentStatus, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}

func assertModuleBefore(t *testing.T, names []string, first, second string) {
	t.Helper()
	firstIndex, secondIndex := -1, -1
	for index, name := range names {
		switch name {
		case first:
			firstIndex = index
		case second:
			secondIndex = index
		}
	}
	if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
		t.Fatalf("module order %q before %q not satisfied: %v", first, second, names)
	}
}

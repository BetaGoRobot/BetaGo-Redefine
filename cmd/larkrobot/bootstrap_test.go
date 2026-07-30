package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
)

func TestAddInfrastructureModulesRegistersAKShareAPIModule(t *testing.T) {
	app := appruntime.NewApp()

	addInfrastructureModules(app, &infraConfig.BaseConfig{}, nil)

	snapshot := app.Registry().Snapshot()
	if hasComponent(snapshot.Components, "aktool") {
		t.Fatalf("unexpected aktool module in registry: %+v", snapshot.Components)
	}
	if !hasComponent(snapshot.Components, "akshareapi") {
		t.Fatalf("expected akshareapi module in registry: %+v", snapshot.Components)
	}
}

func TestRuntimeSchemaIsRegisteredAfterDBBeforeRepositories(t *testing.T) {
	cfg := testConversationRuntimeConfig()
	app, err := buildApp(cfg)
	if err != nil {
		t.Fatalf("buildApp() error = %v", err)
	}
	names := app.ModuleNames()
	assertModuleBefore(t, names, "db", "runtime_schema")
	assertModuleBefore(t, names, "opensearch", "tenant_search_schema")
	assertModuleBefore(t, names, "tenant_search_schema", "application_services")
	assertModuleBefore(t, names, "runtime_schema", "application_services")
	assertModuleBefore(t, names, "runtime_schema", "conversation_evaluation")
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
	if components.tenant.ID == "" ||
		components.conversationIndexAlias == "" ||
		components.evaluationIndexAlias == "" {
		t.Fatalf("tenant search resources are incomplete: %#v", components)
	}
	suffix := "-" + components.tenant.ID
	if !strings.HasSuffix(components.conversationIndexAlias, suffix) ||
		!strings.HasSuffix(components.evaluationIndexAlias, suffix) {
		t.Fatalf(
			"search aliases are not tenant scoped: conversation=%q evaluation=%q",
			components.conversationIndexAlias,
			components.evaluationIndexAlias,
		)
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

func TestSearchSchemaModuleSkipsEvaluationIndexWhenEvaluationModeIsOff(t *testing.T) {
	cfg := testConversationRuntimeConfig()
	cfg.OpensearchConfig = &infraConfig.OpensearchConfig{Domain: "search.internal"}
	components, err := newAppComponents(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fake := &tenantIndexProvisionerFake{}
	original := newTenantIndexProvisioner
	newTenantIndexProvisioner = func() (tenantIndexProvisioner, error) {
		return fake, nil
	}
	t.Cleanup(func() {
		newTenantIndexProvisioner = original
	})
	module := newSearchSchemaModule(cfg, components)
	if err := module.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.baseNames) != 1 ||
		fake.baseNames[0] != appruntime.ConversationEventIndex(cfg) {
		t.Fatalf("provisioned bases = %#v", fake.baseNames)
	}
	stats := module.(appruntime.StatsProvider).Stats()
	if stats["succeeded"] != true ||
		stats["tenant_id"] != components.tenant.ID ||
		stats["conversation_physical"] == "" {
		t.Fatalf("search bootstrap stats = %#v", stats)
	}
	if _, exposed := stats["app_id"]; exposed {
		t.Fatalf("search bootstrap exposed full app identity: %#v", stats)
	}
	if _, exposed := stats["bot_open_id"]; exposed {
		t.Fatalf("search bootstrap exposed full bot identity: %#v", stats)
	}
	if err := ensureEvaluationSearchIndex(
		context.Background(),
		cfg,
		components,
	); err != nil {
		t.Fatalf("lazy evaluation bootstrap: %v", err)
	}
	if err := ensureEvaluationSearchIndex(
		context.Background(),
		cfg,
		components,
	); err != nil {
		t.Fatalf("lazy evaluation bootstrap restart: %v", err)
	}
	if len(fake.baseNames) != 2 {
		t.Fatalf(
			"lazy evaluation bootstrap calls = %#v, want one additional idempotent create",
			fake.baseNames,
		)
	}
	stats = module.(appruntime.StatsProvider).Stats()
	if stats["evaluation_physical"] == "" || stats["succeeded"] != true {
		t.Fatalf("lazy evaluation bootstrap stats = %#v", stats)
	}
}

func TestSearchSchemaModuleCreatesEvaluationIndexWhenEnabled(t *testing.T) {
	cfg := testConversationRuntimeConfig()
	cfg.OpensearchConfig = &infraConfig.OpensearchConfig{Domain: "search.internal"}
	cfg.RuntimeConfig.EvaluationMode = "on"
	components, err := newAppComponents(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fake := &tenantIndexProvisionerFake{}
	original := newTenantIndexProvisioner
	newTenantIndexProvisioner = func() (tenantIndexProvisioner, error) {
		return fake, nil
	}
	t.Cleanup(func() {
		newTenantIndexProvisioner = original
	})
	module := newSearchSchemaModule(cfg, components)
	if err := module.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.baseNames) != 2 {
		t.Fatalf("provisioned bases = %#v, want conversation and evaluation", fake.baseNames)
	}
	stats := module.(appruntime.StatsProvider).Stats()
	if stats["evaluation_physical"] == "" ||
		stats["evaluation_mode"] != "on" {
		t.Fatalf("evaluation bootstrap stats = %#v", stats)
	}
}

func TestSearchSchemaPermissionFailureFailsReadinessClosed(t *testing.T) {
	cfg := testConversationRuntimeConfig()
	cfg.OpensearchConfig = &infraConfig.OpensearchConfig{Domain: "search.internal"}
	components, err := newAppComponents(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fake := &tenantIndexProvisionerFake{err: errors.New("HTTP 403")}
	original := newTenantIndexProvisioner
	newTenantIndexProvisioner = func() (tenantIndexProvisioner, error) {
		return fake, nil
	}
	t.Cleanup(func() {
		newTenantIndexProvisioner = original
	})
	app := appruntime.NewApp(newSearchSchemaModule(cfg, components))
	if err := app.Start(context.Background()); err == nil {
		t.Fatal("App.Start() accepted a tenant search permission failure")
	}
	snapshot := app.Registry().Snapshot()
	if snapshot.Ready || len(snapshot.Components) != 1 ||
		snapshot.Components[0].State != appruntime.StateFailed {
		t.Fatalf("permission failure health snapshot = %#v", snapshot)
	}
	stats := snapshot.Components[0].Stats
	if stats["succeeded"] != false ||
		!strings.Contains(stats["last_error"].(string), "HTTP 403") {
		t.Fatalf("permission failure stats = %#v", stats)
	}
}

type tenantIndexProvisionerFake struct {
	baseNames []string
	err       error
}

func (f *tenantIndexProvisionerFake) EnsureTenantIndex(
	_ context.Context,
	_ tenant.Tenant,
	baseName string,
	_ string,
	_ []byte,
) (opensearch.TenantIndex, error) {
	f.baseNames = append(f.baseNames, baseName)
	if f.err != nil {
		return opensearch.TenantIndex{}, f.err
	}
	return opensearch.TenantIndex{Alias: baseName + "-tenant", PhysicalIndex: baseName + "-tenant-v1"}, nil
}

func TestNewAppComponentsRejectsInvalidOrUnsecuredAgentCardRollout(t *testing.T) {
	cfg := testConversationRuntimeConfig()
	cfg.AgentCardConfig = &infraConfig.AgentCardConfig{
		Enabled: true, Mode: "invalid",
	}
	if _, err := newAppComponents(cfg); err == nil {
		t.Fatal("newAppComponents() accepted an invalid agent card mode")
	}

	cfg = testConversationRuntimeConfig()
	cfg.LarkConfig.AppSecret = ""
	cfg.AgentCardConfig = &infraConfig.AgentCardConfig{
		Enabled: true, Mode: "on",
	}
	if _, err := newAppComponents(cfg); err == nil {
		t.Fatal("newAppComponents() accepted delivery without a binding secret")
	}

	cfg.AgentCardConfig.Mode = "shadow"
	if _, err := newAppComponents(cfg); err != nil {
		t.Fatalf("shadow rollout should not require delivery credentials: %v", err)
	}
}

func TestBuildAppAllowsMissingArkConfigWhenRuntimeIsDisabledByDefault(t *testing.T) {
	cfg := testConversationRuntimeConfig()
	cfg.ArkConfig = nil
	if _, err := buildApp(cfg); err != nil {
		t.Fatalf("buildApp() with disabled-by-default runtime and nil Ark config error = %v", err)
	}
}

func TestEvaluationJudgeModelPrefersExplicitThenReasoningFallback(t *testing.T) {
	cfg := testConversationRuntimeConfig()
	runtimeConfig := cfg.RuntimeConfig
	runtimeConfig.EvaluationJudgeModel = " explicit-judge "
	if got := evaluationJudgeModelID(cfg, runtimeConfig); got != "explicit-judge" {
		t.Fatalf("explicit judge model = %q", got)
	}
	runtimeConfig.EvaluationJudgeModel = ""
	if got := evaluationJudgeModelID(cfg, runtimeConfig); got != "reasoning-test" {
		t.Fatalf("reasoning judge model = %q", got)
	}
	cfg.ArkConfig.ReasoningModel = ""
	if got := evaluationJudgeModelID(cfg, runtimeConfig); got != "normal-test" {
		t.Fatalf("normal judge model = %q", got)
	}
	if got := evaluationJudgeModelID(&infraConfig.BaseConfig{}, nil); got != "" {
		t.Fatalf("missing Ark config judge model = %q", got)
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
	assertModuleBefore(t, names, "application_services", "agent_card_patch_reconciler")
	assertModuleBefore(t, names, "agent_card_patch_reconciler", "lark_ws")
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

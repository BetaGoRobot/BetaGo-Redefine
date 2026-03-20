package runtimewire

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/testsupport/pgtest"
)

func TestBuildDefaultCapabilityRegistryUsesInjectedProvider(t *testing.T) {
	SetDefaultCapabilityProvider(func() []agentruntime.Capability {
		return []agentruntime.Capability{
			fakeCapability{name: "cap_a"},
			fakeCapability{name: "cap_b"},
		}
	})
	defer SetDefaultCapabilityProvider(nil)

	registry := buildDefaultCapabilityRegistry()
	if _, ok := registry.Get("cap_a"); !ok {
		t.Fatal("expected cap_a to be registered")
	}
	if _, ok := registry.Get("cap_b"); !ok {
		t.Fatal("expected cap_b to be registered")
	}
}

func TestBuildDefaultCapabilityRegistryAllowsEmptyProvider(t *testing.T) {
	SetDefaultCapabilityProvider(nil)

	registry := buildDefaultCapabilityRegistry()
	if got := len(registry.List()); got != 0 {
		t.Fatalf("registry size = %d, want 0", got)
	}
}

func TestNewCoordinatorPersistsSessionWithoutRedis(t *testing.T) {
	db := pgtest.OpenTempSchema(t)
	if err := agentstore.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	identity := botidentity.Identity{AppID: "cli_app", BotOpenID: "ou_bot"}
	coordinator := NewCoordinator(db, nil, identity)
	if coordinator == nil {
		t.Fatal("expected coordinator when db and identity are available")
	}

	run, err := coordinator.StartShadowRun(context.Background(), agentruntime.StartShadowRunRequest{
		ChatID:           "oc_chat",
		ActorOpenID:      "ou_actor",
		TriggerType:      agentruntime.TriggerTypeCommandBridge,
		TriggerMessageID: "om_runtime_persist",
		InputText:        "/bb 帮我总结",
	})
	if err != nil {
		t.Fatalf("StartShadowRun() error = %v", err)
	}

	session, err := agentstore.NewSessionRepository(db).GetByID(context.Background(), run.SessionID)
	if err != nil {
		t.Fatalf("GetByID() session error = %v", err)
	}
	if session.ChatID != "oc_chat" {
		t.Fatalf("session chat id = %q, want %q", session.ChatID, "oc_chat")
	}
	if session.ActiveRunID != run.ID {
		t.Fatalf("session active run = %q, want %q", session.ActiveRunID, run.ID)
	}
}

type fakeCapability struct {
	name string
}

func (f fakeCapability) Meta() agentruntime.CapabilityMeta {
	return agentruntime.CapabilityMeta{Name: f.name}
}

func (f fakeCapability) Execute(_ context.Context, _ agentruntime.CapabilityRequest) (agentruntime.CapabilityResult, error) {
	return agentruntime.CapabilityResult{}, nil
}

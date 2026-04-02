package main

import (
	"testing"

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

func hasComponent(items []appruntime.ComponentStatus, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}


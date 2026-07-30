package main

import (
	"context"
	"errors"

	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
)

type agentCardPatchModule struct {
	components *appComponents
}

func (m *agentCardPatchModule) Name() string {
	return "agent_card_patch_reconciler"
}

func (*agentCardPatchModule) Critical() bool { return false }

func (*agentCardPatchModule) Init(context.Context) error { return nil }

func (m *agentCardPatchModule) Start(ctx context.Context) error {
	if m == nil || m.components == nil ||
		m.components.agentCardPatchReconciler == nil {
		return errors.New("agent card patch reconciler unavailable")
	}
	return m.components.agentCardPatchReconciler.Start(ctx)
}

func (m *agentCardPatchModule) Ready(context.Context) error {
	if m == nil || m.components == nil ||
		m.components.agentCardPatchReconciler == nil {
		return errors.New("agent card patch reconciler unavailable")
	}
	return nil
}

func (m *agentCardPatchModule) Stop(ctx context.Context) error {
	if m == nil || m.components == nil ||
		m.components.agentCardPatchReconciler == nil {
		return nil
	}
	return m.components.agentCardPatchReconciler.Stop(ctx)
}

func (m *agentCardPatchModule) Stats() map[string]any {
	if m == nil || m.components == nil ||
		m.components.agentCardPatchReconciler == nil {
		return map[string]any{"running": false}
	}
	return m.components.agentCardPatchReconciler.Stats()
}

func (m *agentCardPatchModule) DynamicHealth() (appruntime.State, string) {
	if m == nil || m.components == nil ||
		m.components.agentCardPatchReconciler == nil {
		return appruntime.StateDegraded, "agent card patch reconciler unavailable"
	}
	healthy, message := m.components.agentCardPatchReconciler.Health()
	if !healthy {
		return appruntime.StateDegraded, message
	}
	return appruntime.StateReady, ""
}

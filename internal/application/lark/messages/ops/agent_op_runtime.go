package ops

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/runtimewire"
)

func buildDefaultShadowRunCoordinator(ctx context.Context) agentruntime.ShadowRunStarter {
	return runtimewire.BuildCoordinator(ctx)
}

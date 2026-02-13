package larkchunking

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo/utility/chunking"
	"github.com/BetaGoRobot/go_utils/reflecting"
)

var M *chunking.Management

func init() {
	M = chunking.NewManagement()
	ctx, span := otel.T().Start(context.Background(), reflecting.GetCurrentFunc())
	defer span.End()
	M.StartBackgroundCleaner(ctx)
}

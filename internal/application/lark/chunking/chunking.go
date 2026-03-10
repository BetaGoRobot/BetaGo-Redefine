package larkchunking

import (
	"context"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xchunk"
)

var M *xchunk.Management
var (
	backgroundCancel context.CancelFunc
	backgroundMu     sync.Mutex
)

func Init() {
	Start(context.Background())
}

func Start(ctx context.Context) {
	backgroundMu.Lock()
	defer backgroundMu.Unlock()
	if M == nil {
		M = xchunk.NewManagement()
	}
	if backgroundCancel != nil {
		return
	}
	cleanerCtx, cancel := context.WithCancel(ctx)
	backgroundCancel = cancel
	traceCtx, span := otel.Start(cleanerCtx)
	defer span.End()
	M.StartBackgroundCleaner(traceCtx)
}

func Stop() {
	backgroundMu.Lock()
	defer backgroundMu.Unlock()
	if backgroundCancel != nil {
		backgroundCancel()
		backgroundCancel = nil
	}
}

func SetExecutor(executor interface {
	Submit(context.Context, string, func(context.Context) error) error
}) {
	if M == nil {
		M = xchunk.NewManagement()
	}
	M.SetExecutor(executor)
}

func Enabled() bool {
	return M != nil && M.Enabled()
}

func DisableReason() string {
	if M == nil {
		return ""
	}
	return M.DisableReason()
}

package larkchunking

import (
	"context"
	"strings"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xchunk"
	"go.uber.org/zap"
)

var M *xchunk.Management
var (
	backgroundCancel context.CancelFunc
	backgroundMu     sync.Mutex
	enabledForChat   = func(context.Context, string) bool { return true }
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

func SetEnabledForChat(fn func(context.Context, string) bool) {
	if fn == nil {
		enabledForChat = func(context.Context, string) bool { return true }
		return
	}
	enabledForChat = fn
}

func SubmitMessage(ctx context.Context, msg xchunk.GenericMsg) error {
	if msg == nil {
		return nil
	}
	chatID := strings.TrimSpace(msg.GroupID())
	if chatID != "" && !enabledForChat(ctx, chatID) {
		logs.L().Ctx(ctx).Debug("Chunk submission skipped by config", zap.String("chat_id", chatID))
		return nil
	}
	if M == nil {
		M = xchunk.NewManagement()
	}
	return M.SubmitMessage(ctx, msg)
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

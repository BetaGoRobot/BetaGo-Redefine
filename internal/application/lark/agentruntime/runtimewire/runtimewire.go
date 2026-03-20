package runtimewire

import (
	"context"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var defaultCapabilityProvider = func() []agentruntime.Capability { return nil }

func SetDefaultCapabilityProvider(provider func() []agentruntime.Capability) {
	if provider == nil {
		defaultCapabilityProvider = func() []agentruntime.Capability { return nil }
		return
	}
	defaultCapabilityProvider = provider
}

func BuildCoordinator(ctx context.Context) *agentruntime.RunCoordinator {
	identity := botidentity.Current()
	if !identity.Valid() {
		logs.L().Ctx(ctx).Debug("agent runtime persistence disabled: bot identity missing")
		return nil
	}

	db := infraDB.DBWithoutQueryCache()
	if db == nil {
		logs.L().Ctx(ctx).Debug("agent runtime persistence disabled: db unavailable")
		return nil
	}

	redisClient := redis_dal.GetRedisClient()
	if redisClient == nil {
		logs.L().Ctx(ctx).Debug("agent runtime persistence degraded: redis unavailable; using db-only coordinator")
	}

	return NewCoordinator(db, redisClient, identity)
}

func BuildResumeDispatcher(ctx context.Context) *agentruntime.ResumeDispatcher {
	identity := botidentity.Current()
	if !identity.Valid() {
		logs.L().Ctx(ctx).Debug("agent runtime resume disabled: bot identity missing")
		return nil
	}

	db := infraDB.DBWithoutQueryCache()
	if db == nil {
		logs.L().Ctx(ctx).Debug("agent runtime resume disabled: db unavailable")
		return nil
	}

	redisClient := redis_dal.GetRedisClient()
	if redisClient == nil {
		logs.L().Ctx(ctx).Debug("agent runtime resume disabled: redis unavailable")
		return nil
	}

	coordinator := NewCoordinator(db, redisClient, identity)
	if coordinator == nil {
		return nil
	}
	return agentruntime.NewResumeDispatcher(coordinator, &resumeQueueAdapter{
		store: redis_dal.NewAgentRuntimeStore(redisClient, identity),
	})
}

func BuildResumeWorker(ctx context.Context) *agentruntime.ResumeWorker {
	identity := botidentity.Current()
	if !identity.Valid() {
		logs.L().Ctx(ctx).Debug("agent runtime resume worker disabled: bot identity missing")
		return nil
	}

	db := infraDB.DBWithoutQueryCache()
	if db == nil {
		logs.L().Ctx(ctx).Debug("agent runtime resume worker disabled: db unavailable")
		return nil
	}

	redisClient := redis_dal.GetRedisClient()
	if redisClient == nil {
		logs.L().Ctx(ctx).Debug("agent runtime resume worker disabled: redis unavailable")
		return nil
	}

	coordinator := NewCoordinator(db, redisClient, identity)
	if coordinator == nil {
		return nil
	}

	return agentruntime.NewResumeWorker(
		&resumeWorkerQueueAdapter{store: redis_dal.NewAgentRuntimeStore(redisClient, identity)},
		BuildRunProcessor(ctx, nil),
	)
}

func BuildContinuationProcessor(ctx context.Context) *agentruntime.ContinuationProcessor {
	return BuildRunProcessor(ctx, nil)
}

func BuildRunProcessor(ctx context.Context, initialReplyEmitter agentruntime.InitialReplyEmitter) *agentruntime.ContinuationProcessor {
	coordinator := BuildCoordinator(ctx)
	if coordinator == nil {
		return nil
	}
	return agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(buildDefaultCapabilityRegistry()),
		agentruntime.WithCapabilityReplyTurnExecutor(agentruntime.NewDefaultCapabilityReplyTurnExecutor()),
		agentruntime.WithContinuationReplyTurnExecutor(agentruntime.NewDefaultContinuationReplyTurnExecutor()),
		agentruntime.WithCapabilityReplyPlanner(agentruntime.NewDefaultCapabilityReplyPlanner()),
		agentruntime.WithInitialReplyEmitter(initialReplyEmitter),
		agentruntime.WithReplyEmitter(agentruntime.NewLarkReplyEmitter()),
		agentruntime.WithApprovalSender(agentruntime.NewLarkApprovalSender()),
	)
}

func NewCoordinator(db *gorm.DB, redisClient *redis.Client, identity botidentity.Identity) *agentruntime.RunCoordinator {
	if db == nil || !identity.Valid() {
		logs.L().Debug("agent runtime persistence dependencies incomplete",
			zap.Bool("db_ready", db != nil),
			zap.Bool("identity_ready", identity.Valid()),
		)
		return nil
	}

	if redisClient == nil {
		return agentruntime.NewRunCoordinator(
			agentstore.NewSessionRepository(db),
			agentstore.NewRunRepository(db),
			agentstore.NewStepRepository(db),
			nil,
			identity,
		)
	}

	return agentruntime.NewRunCoordinator(
		agentstore.NewSessionRepository(db),
		agentstore.NewRunRepository(db),
		agentstore.NewStepRepository(db),
		redis_dal.NewAgentRuntimeStore(redisClient, identity),
		identity,
	)
}

func buildDefaultCapabilityRegistry() *agentruntime.CapabilityRegistry {
	registry := agentruntime.NewCapabilityRegistry()
	for _, capability := range defaultCapabilityProvider() {
		if capability == nil {
			continue
		}
		if err := registry.Register(capability); err != nil {
			logs.L().Warn("register default agent runtime capability failed", zap.Error(err))
		}
	}
	return registry
}

type resumeQueueAdapter struct {
	store *redis_dal.AgentRuntimeStore
}

func (a *resumeQueueAdapter) EnqueueResumeEvent(ctx context.Context, event agentruntime.ResumeEvent) error {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.EnqueueResumeEvent(ctx, redis_dal.ResumeEvent{
		RunID:       event.RunID,
		StepID:      event.StepID,
		Revision:    event.Revision,
		Source:      string(event.Source),
		Token:       event.Token,
		Summary:     event.Summary,
		PayloadJSON: append([]byte(nil), event.PayloadJSON...),
		ActorOpenID: event.ActorOpenID,
		OccurredAt:  event.OccurredAt,
	})
}

type resumeWorkerQueueAdapter struct {
	store *redis_dal.AgentRuntimeStore
}

func (a *resumeWorkerQueueAdapter) DequeueResumeEvent(ctx context.Context, timeout time.Duration) (*agentruntime.ResumeEvent, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	event, err := a.store.DequeueResumeEvent(ctx, timeout)
	if err != nil || event == nil {
		return nil, err
	}
	return &agentruntime.ResumeEvent{
		RunID:       event.RunID,
		StepID:      event.StepID,
		Revision:    event.Revision,
		Source:      agentruntime.ResumeSource(event.Source),
		Token:       event.Token,
		Summary:     event.Summary,
		PayloadJSON: append([]byte(nil), event.PayloadJSON...),
		ActorOpenID: event.ActorOpenID,
		OccurredAt:  event.OccurredAt,
	}, nil
}

func (a *resumeWorkerQueueAdapter) AcquireRunLock(ctx context.Context, runID, owner string, ttl time.Duration) (bool, error) {
	if a == nil || a.store == nil {
		return false, nil
	}
	return a.store.AcquireRunLock(ctx, runID, owner, ttl)
}

func (a *resumeWorkerQueueAdapter) ReleaseRunLock(ctx context.Context, runID, owner string) (bool, error) {
	if a == nil || a.store == nil {
		return false, nil
	}
	return a.store.ReleaseRunLock(ctx, runID, owner)
}

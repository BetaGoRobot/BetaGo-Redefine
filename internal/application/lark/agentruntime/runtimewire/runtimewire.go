package runtimewire

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	initialcore "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/initial"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var defaultCapabilityProvider = func() []agentruntime.Capability { return nil }

var (
	pendingInitialMetricsOnce    sync.Once
	pendingInitialMetricsRef     *initialcore.PendingInitialMetrics
	agentRuntimeWorkerSettingsMu sync.RWMutex
	agentRuntimeWorkerSettings   = appruntime.AgentRuntimeWorkerSettings{
		ResumeWorkers:            1,
		PendingInitialRunWorkers: 1,
	}
	agentRuntimeTimingSettings = appruntime.AgentRuntimeTimingSettings{
		ExecutionLeaseTTL:          3 * time.Minute,
		ExecutionHeartbeatInterval: 15 * time.Second,
		LegacyRunStaleTimeout:      30 * time.Minute,
		StaleRunSweepInterval:      5 * time.Second,
	}
)

// SetDefaultCapabilityProvider overrides the capability provider used when
// building the default capability registry for production wiring.
func SetDefaultCapabilityProvider(provider func() []agentruntime.Capability) {
	if provider == nil {
		defaultCapabilityProvider = func() []agentruntime.Capability { return nil }
		return
	}
	defaultCapabilityProvider = provider
}

func SetAgentRuntimeWorkerSettings(settings appruntime.AgentRuntimeWorkerSettings) {
	agentRuntimeWorkerSettingsMu.Lock()
	defer agentRuntimeWorkerSettingsMu.Unlock()
	if settings.ResumeWorkers <= 0 {
		settings.ResumeWorkers = 1
	}
	if settings.PendingInitialRunWorkers <= 0 {
		settings.PendingInitialRunWorkers = 1
	}
	agentRuntimeWorkerSettings = settings
}

func currentAgentRuntimeWorkerSettings() appruntime.AgentRuntimeWorkerSettings {
	agentRuntimeWorkerSettingsMu.RLock()
	defer agentRuntimeWorkerSettingsMu.RUnlock()
	return agentRuntimeWorkerSettings
}

func SetAgentRuntimeTimingSettings(settings appruntime.AgentRuntimeTimingSettings) {
	agentRuntimeWorkerSettingsMu.Lock()
	defer agentRuntimeWorkerSettingsMu.Unlock()
	if settings.ExecutionLeaseTTL <= 0 {
		settings.ExecutionLeaseTTL = 3 * time.Minute
	}
	if settings.ExecutionHeartbeatInterval <= 0 {
		settings.ExecutionHeartbeatInterval = 15 * time.Second
	}
	if settings.LegacyRunStaleTimeout <= 0 {
		settings.LegacyRunStaleTimeout = 30 * time.Minute
	}
	if settings.StaleRunSweepInterval <= 0 {
		settings.StaleRunSweepInterval = 5 * time.Second
	}
	agentRuntimeTimingSettings = settings
}

func currentAgentRuntimeTimingSettings() appruntime.AgentRuntimeTimingSettings {
	agentRuntimeWorkerSettingsMu.RLock()
	defer agentRuntimeWorkerSettingsMu.RUnlock()
	return agentRuntimeTimingSettings
}

// BuildCoordinator constructs the default runtime coordinator from the current request context and infrastructure singletons.
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

// BuildResumeDispatcher constructs the production resume dispatcher when the
// required DB, Redis, and bot identity dependencies are available.
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

// BuildResumeWorker constructs the background worker that drains queued resume
// events under production infrastructure dependencies.
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
	).WithWorkers(currentAgentRuntimeWorkerSettings().ResumeWorkers)
}

// BuildPendingInitialRunEnqueuer constructs the queue writer used to park
// initial runtime work when no execution slot is available yet.
func BuildPendingInitialRunEnqueuer(ctx context.Context) initialcore.RunEnqueuer {
	identity := botidentity.Current()
	if !identity.Valid() {
		logs.L().Ctx(ctx).Debug("agent runtime pending initial queue disabled: bot identity missing")
		return nil
	}

	redisClient := redis_dal.GetRedisClient()
	if redisClient == nil {
		logs.L().Ctx(ctx).Debug("agent runtime pending initial queue disabled: redis unavailable")
		return nil
	}

	return &pendingInitialRunQueueAdapter{
		store:      redis_dal.NewAgentRuntimeStore(redisClient, identity),
		maxPending: agentruntime.DefaultMaxPendingInitialRunsPerActorChat,
		metrics:    pendingInitialMetrics(),
	}
}

// BuildPendingInitialRunWorker constructs the worker that drains queued initial
// runs once capacity becomes available.
func BuildPendingInitialRunWorker(ctx context.Context) *initialcore.RunWorker {
	identity := botidentity.Current()
	if !identity.Valid() {
		logs.L().Ctx(ctx).Debug("agent runtime pending initial worker disabled: bot identity missing")
		return nil
	}

	redisClient := redis_dal.GetRedisClient()
	if redisClient == nil {
		logs.L().Ctx(ctx).Debug("agent runtime pending initial worker disabled: redis unavailable")
		return nil
	}

	return initialcore.NewRunWorkerWithMetricsAndStatusUpdater(
		redis_dal.NewAgentRuntimeStore(redisClient, identity),
		BuildRunProcessor(ctx, initialcore.NewLarkInitialReplyEmitter()),
		pendingInitialMetrics(),
		initialcore.NewLarkRunStatusUpdater(),
	).WithWorkers(currentAgentRuntimeWorkerSettings().PendingInitialRunWorkers)
}

// BuildOutstandingTaskCounter builds the counter used to estimate active and
// pending work for one actor/chat pair.
func BuildOutstandingTaskCounter(ctx context.Context) func(context.Context, string, string) (int64, int64, error) {
	identity := botidentity.Current()
	if !identity.Valid() {
		return nil
	}

	db := infraDB.DBWithoutQueryCache()
	if db == nil {
		return nil
	}

	redisClient := redis_dal.GetRedisClient()
	var store *redis_dal.AgentRuntimeStore
	if redisClient != nil {
		store = redis_dal.NewAgentRuntimeStore(redisClient, identity)
	}

	adapter := &outstandingTaskCounterAdapter{
		sessionRepo: agentstore.NewSessionRepository(db),
		runRepo:     agentstore.NewRunRepository(db),
		store:       store,
		identity:    identity,
	}
	return adapter.Count
}

// BuildPendingScopeSweeper constructs the sweeper that wakes pending initial
// scopes after runs finish or capacity changes.
func BuildPendingScopeSweeper(ctx context.Context) *initialcore.Sweeper {
	identity := botidentity.Current()
	if !identity.Valid() {
		logs.L().Ctx(ctx).Debug("agent runtime pending scope sweeper disabled: bot identity missing")
		return nil
	}

	redisClient := redis_dal.GetRedisClient()
	if redisClient == nil {
		logs.L().Ctx(ctx).Debug("agent runtime pending scope sweeper disabled: redis unavailable")
		return nil
	}

	sweeper := initialcore.NewPendingScopeSweeperWithMetrics(&pendingScopeSweepAdapter{
		store: redis_dal.NewAgentRuntimeStore(redisClient, identity),
	}, pendingInitialMetrics())
	agentruntime.RegisterPendingScopeSweepTrigger(sweeper.Trigger)
	return sweeper
}

// BuildStaleRunSweeper constructs the sweeper that repairs stale queued/running
// runs using run heartbeat/lease state plus a legacy updated_at fallback.
func BuildStaleRunSweeper(ctx context.Context) *agentruntime.StaleRunSweeper {
	identity := botidentity.Current()
	if !identity.Valid() {
		logs.L().Ctx(ctx).Debug("agent runtime stale run sweeper disabled: bot identity missing")
		return nil
	}

	db := infraDB.DBWithoutQueryCache()
	if db == nil {
		logs.L().Ctx(ctx).Debug("agent runtime stale run sweeper disabled: db unavailable")
		return nil
	}

	redisClient := redis_dal.GetRedisClient()
	if redisClient == nil {
		logs.L().Ctx(ctx).Debug("agent runtime stale run sweeper disabled: redis unavailable")
		return nil
	}

	runRepo := agentstore.NewRunRepository(db)
	coordinator := NewCoordinator(db, redisClient, identity)
	if coordinator == nil {
		return nil
	}
	settings := currentAgentRuntimeTimingSettings()
	return agentruntime.NewStaleRunSweeper(runRepo, coordinator).
		WithSweepInterval(settings.StaleRunSweepInterval).
		WithLegacyStaleAfter(settings.LegacyRunStaleTimeout)
}

// PendingInitialMetricsProvider builds the metrics provider exposed to the
// application runtime for pending initial-run visibility.
func PendingInitialMetricsProvider(ctx context.Context) appruntime.MetricsProvider {
	identity := botidentity.Current()
	redisClient := redis_dal.GetRedisClient()
	var snapshotter initialcore.PendingInitialBacklogSnapshotter
	if identity.Valid() && redisClient != nil {
		snapshotter = &pendingInitialBacklogSnapshotter{
			store: redis_dal.NewAgentRuntimeStore(redisClient, identity),
		}
	}
	return initialcore.NewPendingInitialMetricsProvider(pendingInitialMetrics(), snapshotter)
}

// BuildContinuationProcessor constructs the default continuation processor with
// production dependencies and the default initial-reply emitter.
func BuildContinuationProcessor(ctx context.Context) *agentruntime.ContinuationProcessor {
	return BuildRunProcessor(ctx, nil)
}

// BuildRunProcessor wires the default continuation processor together with runtime dependencies and the supplied initial reply emitter.
func BuildRunProcessor(ctx context.Context, initialReplyEmitter agentruntime.InitialReplyEmitter) *agentruntime.ContinuationProcessor {
	coordinator := BuildCoordinator(ctx)
	if coordinator == nil {
		return nil
	}
	settings := currentAgentRuntimeTimingSettings()
	return agentruntime.NewContinuationProcessor(
		coordinator,
		agentruntime.WithCapabilityRegistry(buildDefaultCapabilityRegistry()),
		agentruntime.WithCapabilityReplyTurnExecutor(agentruntime.NewDefaultCapabilityReplyTurnExecutor()),
		agentruntime.WithContinuationReplyTurnExecutor(agentruntime.NewDefaultContinuationReplyTurnExecutor()),
		agentruntime.WithCapabilityReplyPlanner(agentruntime.NewDefaultCapabilityReplyPlanner()),
		agentruntime.WithInitialReplyEmitter(initialReplyEmitter),
		agentruntime.WithReplyEmitter(agentruntime.NewLarkReplyEmitter()),
		agentruntime.WithApprovalSender(agentruntime.NewLarkApprovalSender()),
		agentruntime.WithRunLeasePolicy(agentruntime.RunLeasePolicy{
			TTL:               settings.ExecutionLeaseTTL,
			HeartbeatInterval: settings.ExecutionHeartbeatInterval,
		}),
	)
}

// NewCoordinator constructs a coordinator directly from explicit infrastructure
// dependencies instead of the ambient request context.
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
		&coordinationStoreAdapter{
			AgentRuntimeStore: redis_dal.NewAgentRuntimeStore(redisClient, identity),
			metrics:           pendingInitialMetrics(),
		},
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

// EnqueueResumeEvent serializes and enqueues a resume event into the production resume queue.
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

type pendingInitialRunQueueAdapter struct {
	store      *redis_dal.AgentRuntimeStore
	maxPending int64
	metrics    *initialcore.PendingInitialMetrics
}

type outstandingTaskCounterAdapter struct {
	sessionRepo *agentstore.SessionRepository
	runRepo     *agentstore.RunRepository
	store       *redis_dal.AgentRuntimeStore
	identity    botidentity.Identity
}

type pendingScopeSweepAdapter struct {
	store *redis_dal.AgentRuntimeStore
}

type coordinationStoreAdapter struct {
	*redis_dal.AgentRuntimeStore
	metrics *initialcore.PendingInitialMetrics
}

type pendingInitialBacklogSnapshotter struct {
	store *redis_dal.AgentRuntimeStore
}

// EnqueuePendingInitialRun serializes and enqueues a pending initial run while updating metrics.
func (a *pendingInitialRunQueueAdapter) EnqueuePendingInitialRun(ctx context.Context, item initialcore.PendingRun) (int64, error) {
	if a == nil || a.store == nil {
		return 0, nil
	}
	raw, err := initialcore.MarshalPendingRun(item)
	if err != nil {
		return 0, err
	}
	position, err := a.store.EnqueuePendingInitialRun(ctx, item.ChatID(), item.ActorOpenID(), raw, a.maxPending)
	if errors.Is(err, redis_dal.ErrPendingInitialRunQueueFull) {
		if a.metrics != nil {
			a.metrics.IncEnqueueRejected()
		}
		return 0, agentruntime.ErrPendingInitialRunQueueFull
	}
	if err == nil {
		if a.metrics != nil {
			a.metrics.IncEnqueued()
		}
		if notifyErr := a.store.NotifyPendingInitialRun(ctx, item.ChatID(), item.ActorOpenID()); notifyErr == nil && a.metrics != nil {
			a.metrics.IncWakeupEmitted()
		}
	}
	return position, err
}

// ListPendingInitialScopes lists pending initial scopes from the coordination store for sweeping.
func (a *pendingScopeSweepAdapter) ListPendingInitialScopes(ctx context.Context, cursor uint64, count int64) ([]initialcore.PendingScope, uint64, error) {
	if a == nil || a.store == nil {
		return nil, 0, nil
	}
	scopes, nextCursor, err := a.store.ListPendingInitialScopes(ctx, cursor, count)
	if err != nil {
		return nil, 0, err
	}
	result := make([]initialcore.PendingScope, 0, len(scopes))
	for _, scope := range scopes {
		result = append(result, initialcore.PendingScope{
			ChatID:      scope.ChatID,
			ActorOpenID: scope.ActorOpenID,
		})
	}
	return result, nextCursor, nil
}

// PendingInitialRunCount reports the number of queued initial runs for one scope.
func (a *pendingScopeSweepAdapter) PendingInitialRunCount(ctx context.Context, chatID, actorOpenID string) (int64, error) {
	if a == nil || a.store == nil {
		return 0, nil
	}
	return a.store.PendingInitialRunCount(ctx, chatID, actorOpenID)
}

// ActiveExecutionLeaseCount reports how many execution leases are currently held for one scope.
func (a *pendingScopeSweepAdapter) ActiveExecutionLeaseCount(ctx context.Context, chatID, actorOpenID string) (int64, error) {
	if a == nil || a.store == nil {
		return 0, nil
	}
	return a.store.ExecutionLeaseCount(ctx, chatID, actorOpenID)
}

// NotifyPendingInitialRun emits a wakeup signal for a pending initial scope.
func (a *pendingScopeSweepAdapter) NotifyPendingInitialRun(ctx context.Context, chatID, actorOpenID string) error {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.NotifyPendingInitialRun(ctx, chatID, actorOpenID)
}

// ClearPendingInitialScopeIfEmpty removes an empty pending scope after sweeping.
func (a *pendingScopeSweepAdapter) ClearPendingInitialScopeIfEmpty(ctx context.Context, chatID, actorOpenID string) error {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.ClearPendingInitialScopeIfEmpty(ctx, chatID, actorOpenID)
}

// NotifyPendingInitialRun forwards a wakeup notification through the coordination store.
func (a *coordinationStoreAdapter) NotifyPendingInitialRun(ctx context.Context, chatID, actorOpenID string) error {
	if a == nil || a.AgentRuntimeStore == nil {
		return nil
	}
	err := a.AgentRuntimeStore.NotifyPendingInitialRun(ctx, chatID, actorOpenID)
	if err == nil && a.metrics != nil {
		a.metrics.IncWakeupEmitted()
	}
	return err
}

// Count reports active-run and queued-run totals for one actor/chat pair.
func (a *outstandingTaskCounterAdapter) Count(ctx context.Context, chatID, actorOpenID string) (int64, int64, error) {
	if a == nil || a.sessionRepo == nil || a.runRepo == nil || !a.identity.Valid() {
		return 0, 0, nil
	}
	session, err := a.sessionRepo.FindOrCreateChatSession(ctx, a.identity.AppID, a.identity.BotOpenID, chatID)
	if err != nil {
		return 0, 0, err
	}
	activeCount, err := a.runRepo.CountActiveBySessionActor(ctx, session.ID, actorOpenID)
	if err != nil {
		return 0, 0, err
	}
	if a.store == nil {
		return activeCount, 0, nil
	}
	pendingCount, err := a.store.PendingInitialRunCount(ctx, chatID, actorOpenID)
	if err != nil {
		return 0, 0, err
	}
	return activeCount, pendingCount, nil
}

// SnapshotPendingInitialBacklog collects a backlog snapshot for pending initial-run metrics.
func (s *pendingInitialBacklogSnapshotter) SnapshotPendingInitialBacklog(ctx context.Context) (initialcore.PendingInitialBacklog, error) {
	if s == nil || s.store == nil {
		return initialcore.PendingInitialBacklog{}, nil
	}
	scopeCount, err := s.store.PendingInitialScopeCount(ctx)
	if err != nil {
		return initialcore.PendingInitialBacklog{}, err
	}
	if scopeCount == 0 {
		return initialcore.PendingInitialBacklog{}, nil
	}

	backlog := initialcore.PendingInitialBacklog{PendingScopes: scopeCount}
	var cursor uint64
	for {
		scopes, nextCursor, err := s.store.ListPendingInitialScopes(ctx, cursor, 100)
		if err != nil {
			return initialcore.PendingInitialBacklog{}, err
		}
		for _, scope := range scopes {
			count, err := s.store.PendingInitialRunCount(ctx, scope.ChatID, scope.ActorOpenID)
			if err != nil {
				return initialcore.PendingInitialBacklog{}, err
			}
			backlog.PendingRuns += count
		}
		if nextCursor == 0 {
			return backlog, nil
		}
		cursor = nextCursor
	}
}

// EnqueueResumeEvent serializes and pushes a resume event for the resume worker queue.
func (a *resumeWorkerQueueAdapter) EnqueueResumeEvent(ctx context.Context, event agentruntime.ResumeEvent) error {
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

func pendingInitialMetrics() *initialcore.PendingInitialMetrics {
	pendingInitialMetricsOnce.Do(func() {
		pendingInitialMetricsRef = initialcore.NewPendingInitialMetrics()
	})
	return pendingInitialMetricsRef
}

// DequeueResumeEvent blocks until a resume event is available or the timeout expires.
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

// AcquireRunLock acquires the per-run lock used by the resume worker.
func (a *resumeWorkerQueueAdapter) AcquireRunLock(ctx context.Context, runID, owner string, ttl time.Duration) (bool, error) {
	if a == nil || a.store == nil {
		return false, nil
	}
	return a.store.AcquireRunLock(ctx, runID, owner, ttl)
}

// ReleaseRunLock releases the per-run lock held by the resume worker.
func (a *resumeWorkerQueueAdapter) ReleaseRunLock(ctx context.Context, runID, owner string) (bool, error) {
	if a == nil || a.store == nil {
		return false, nil
	}
	return a.store.ReleaseRunLock(ctx, runID, owner)
}

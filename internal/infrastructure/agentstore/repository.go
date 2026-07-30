package agentstore

import (
	"context"
	"errors"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type Repository struct {
	db     *gorm.DB
	q      *query.Query
	tenant tenant.Tenant
}

var _ agentruntime.Store = (*Repository)(nil)

func NewRepository(db *gorm.DB, owner tenant.Tenant) (*Repository, error) {
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	db = infraDB.WithoutQueryCache(db)
	if db != nil {
		db = db.Session(&gorm.Session{Logger: logger.Discard})
		db = db.Scopes(scopeTenant(owner.ID))
	}
	return &Repository{db: db, q: query.Use(db), tenant: owner}, nil
}

func scopeTenant(tenantID string) func(*gorm.DB) *gorm.DB {
	return func(database *gorm.DB) *gorm.DB {
		table := database.Statement.Table
		modelValue := database.Statement.Model
		if modelValue == nil {
			modelValue = database.Statement.Dest
		}
		if table == "" && modelValue != nil {
			_ = database.Statement.Parse(modelValue)
			table = database.Statement.Table
		}
		switch table {
		case "agent_sessions", "sessions",
			"agent_runs", "runs",
			"agent_steps", "steps",
			"agent_capability_executions",
			"agent_projection_outbox":
		default:
			return database
		}
		return database.Where(clause.Eq{
			Column: clause.Column{Table: clause.CurrentTable, Name: "tenant_id"},
			Value:  tenantID,
		})
	}
}

func (r *Repository) GetOrCreateSession(ctx context.Context, session *agentruntime.AgentSession) (*agentruntime.AgentSession, error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	if session != nil {
		span.SetAttributes(attribute.String("agent.session.id", session.ID), attribute.String("chat.id", session.ChatID))
	}
	dbSession := toDBSession(session)
	if dbSession == nil {
		return nil, errors.New("agent session is nil")
	}
	if session.AppID != r.tenant.AppID || session.BotOpenID != r.tenant.BotOpenID {
		return nil, errors.New("agent session belongs to another tenant")
	}
	dbSession.TenantID = r.tenant.ID
	err := r.q.AgentSession.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(dbSession)
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}
	ins := r.q.AgentSession
	stored, err := ins.WithContext(ctx).Where(ins.ID.Eq(session.ID)).Limit(1).Find()
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}
	if len(stored) == 0 {
		return nil, agentruntime.ErrNotFound
	}
	return toRuntimeSession(stored[0]), nil
}

func (r *Repository) FindRunBySessionAndTriggerMessage(ctx context.Context, sessionID, messageID string) (*agentruntime.AgentRun, error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("agent.session.id", sessionID), attribute.String("message.id", messageID))
	defer span.End()

	ins := r.q.AgentRun
	runs, err := ins.WithContext(ctx).
		Where(ins.SessionID.Eq(sessionID)).
		Where(ins.TriggerMessageID.Eq(messageID)).
		Order(ins.CreatedAt.Desc()).
		Limit(1).
		Find()
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}
	if len(runs) == 0 {
		return nil, agentruntime.ErrNotFound
	}
	return toRuntimeRun(runs[0]), nil
}

func (r *Repository) CreateRun(ctx context.Context, run *agentruntime.AgentRun) error {
	ctx, span := otel.Start(ctx)
	defer span.End()
	if run != nil {
		span.SetAttributes(attribute.String("agent.run.id", run.ID), attribute.String("agent.session.id", run.SessionID))
	}
	dbRun := toDBRun(run)
	if dbRun == nil {
		return errors.New("agent run is nil")
	}
	dbRun.TenantID = r.tenant.ID
	err := r.q.AgentRun.WithContext(ctx).Create(dbRun)
	otel.RecordError(span, err)
	return err
}

func (r *Repository) UpdateSessionActiveRun(ctx context.Context, sessionID, runID, lastMessageID, lastActorOpenID string) (*agentruntime.AgentSession, error) {
	ctx, span := otel.Start(ctx)
	span.SetAttributes(attribute.String("agent.session.id", sessionID), attribute.String("agent.run.id", runID))
	defer span.End()

	ins := r.q.AgentSession
	_, err := ins.WithContext(ctx).
		Where(ins.ID.Eq(sessionID)).
		Updates(map[string]any{
			"active_run_id":      runID,
			"last_message_id":    lastMessageID,
			"last_actor_open_id": lastActorOpenID,
			"updated_at":         time.Now(),
		})
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}
	sessions, err := ins.WithContext(ctx).Where(ins.ID.Eq(sessionID)).Limit(1).Find()
	if err != nil {
		otel.RecordError(span, err)
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, agentruntime.ErrNotFound
	}
	return toRuntimeSession(sessions[0]), nil
}

func (r *Repository) CreateStep(ctx context.Context, step *agentruntime.AgentStep) error {
	ctx, span := otel.Start(ctx)
	defer span.End()
	if step != nil {
		span.SetAttributes(attribute.String("agent.run.id", step.RunID), attribute.String("agent.step.kind", string(step.Kind)))
	}
	dbStep := toDBStep(step)
	if dbStep == nil {
		return errors.New("agent step is nil")
	}
	dbStep.TenantID = r.tenant.ID
	err := r.q.AgentStep.WithContext(ctx).Create(dbStep)
	otel.RecordError(span, err)
	return err
}

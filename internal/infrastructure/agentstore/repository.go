package agentstore

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	uuid "github.com/satori/go.uuid"
	"gorm.io/gorm"
)

var (
	ErrRevisionConflict   = errors.New("agent runtime revision conflict")
	ErrStepStatusConflict = errors.New("agent runtime step status conflict")
)

func AutoMigrate(db *gorm.DB) error {
	if db == nil {
		return errors.New("agentstore db is nil")
	}
	if strings.EqualFold(db.Dialector.Name(), "sqlite") {
		return autoMigrateSQLite(db)
	}
	if strings.EqualFold(db.Dialector.Name(), "postgres") {
		return autoMigratePostgres(db)
	}
	if err := db.AutoMigrate(&model.AgentSession{}, &model.AgentRun{}, &model.AgentStep{}); err != nil {
		return err
	}

	indexes := []string{
		"create unique index if not exists idx_agent_sessions_scope_unique on agent_sessions (app_id, bot_open_id, scope_type, scope_id)",
		"create index if not exists idx_agent_sessions_status_updated on agent_sessions (status, updated_at desc)",
		"create unique index if not exists idx_agent_runs_session_trigger_unique on agent_runs (session_id, trigger_message_id)",
		"create index if not exists idx_agent_runs_status_updated on agent_runs (status, updated_at desc)",
		"create index if not exists idx_agent_runs_session_updated on agent_runs (session_id, updated_at desc)",
		`create unique index if not exists idx_agent_steps_run_index_unique on agent_steps (run_id, "index")`,
		"create index if not exists idx_agent_steps_run_created on agent_steps (run_id, created_at asc)",
	}
	for _, statement := range indexes {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func autoMigratePostgres(db *gorm.DB) error {
	statements := []string{
		`create table if not exists agent_sessions (
			id text primary key,
			app_id text not null,
			bot_open_id text not null,
			chat_id text not null,
			scope_type text not null,
			scope_id text not null,
			status text not null,
			active_run_id text not null default '',
			last_message_id text not null default '',
			last_actor_open_id text not null default '',
			memory_version bigint not null default 0,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		)`,
		`create unique index if not exists idx_agent_sessions_scope_unique on agent_sessions (app_id, bot_open_id, scope_type, scope_id)`,
		`create index if not exists idx_agent_sessions_status_updated on agent_sessions (status, updated_at desc)`,
		`create table if not exists agent_runs (
			id text primary key,
			session_id text not null references agent_sessions(id) on delete cascade,
			trigger_type text not null,
			trigger_message_id text not null default '',
			trigger_event_id text not null default '',
			actor_open_id text not null default '',
			parent_run_id text not null default '',
			status text not null,
			goal text not null default '',
			input_text text not null default '',
			current_step_index integer not null default 0,
			waiting_reason text not null default '',
			waiting_token text not null default '',
			last_response_id text not null default '',
			result_summary text not null default '',
			error_text text not null default '',
			revision bigint not null default 0,
			started_at timestamptz null,
			finished_at timestamptz null,
			created_at timestamptz not null default now(),
			updated_at timestamptz not null default now()
		)`,
		`create unique index if not exists idx_agent_runs_session_trigger_unique on agent_runs (session_id, trigger_message_id)`,
		`create index if not exists idx_agent_runs_status_updated on agent_runs (status, updated_at desc)`,
		`create index if not exists idx_agent_runs_session_updated on agent_runs (session_id, updated_at desc)`,
		`create table if not exists agent_steps (
			id text primary key,
			run_id text not null references agent_runs(id) on delete cascade,
			"index" integer not null,
			kind text not null,
			status text not null,
			capability_name text not null default '',
			input_json jsonb not null default '{}'::jsonb,
			output_json jsonb not null default '{}'::jsonb,
			error_text text not null default '',
			external_ref text not null default '',
			started_at timestamptz null,
			finished_at timestamptz null,
			created_at timestamptz not null default now()
		)`,
		`create unique index if not exists idx_agent_steps_run_index_unique on agent_steps (run_id, "index")`,
		`create index if not exists idx_agent_steps_run_created on agent_steps (run_id, created_at asc)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func autoMigrateSQLite(db *gorm.DB) error {
	statements := []string{
		`create table if not exists agent_sessions (
			id text primary key,
			app_id text not null,
			bot_open_id text not null,
			chat_id text not null,
			scope_type text not null,
			scope_id text not null,
			status text not null,
			active_run_id text not null default '',
			last_message_id text not null default '',
			last_actor_open_id text not null default '',
			memory_version integer not null default 0,
			created_at datetime not null,
			updated_at datetime not null
		)`,
		`create unique index if not exists idx_agent_sessions_scope_unique on agent_sessions (app_id, bot_open_id, scope_type, scope_id)`,
		`create index if not exists idx_agent_sessions_status_updated on agent_sessions (status, updated_at desc)`,
		`create table if not exists agent_runs (
			id text primary key,
			session_id text not null,
			trigger_type text not null,
			trigger_message_id text not null default '',
			trigger_event_id text not null default '',
			actor_open_id text not null default '',
			parent_run_id text not null default '',
			status text not null,
			goal text not null default '',
			input_text text not null default '',
			current_step_index integer not null default 0,
			waiting_reason text not null default '',
			waiting_token text not null default '',
			last_response_id text not null default '',
			result_summary text not null default '',
			error_text text not null default '',
			revision integer not null default 0,
			started_at datetime null,
			finished_at datetime null,
			created_at datetime not null,
			updated_at datetime not null
		)`,
		`create unique index if not exists idx_agent_runs_session_trigger_unique on agent_runs (session_id, trigger_message_id)`,
		`create index if not exists idx_agent_runs_status_updated on agent_runs (status, updated_at desc)`,
		`create index if not exists idx_agent_runs_session_updated on agent_runs (session_id, updated_at desc)`,
		`create table if not exists agent_steps (
			id text primary key,
			run_id text not null,
			"index" integer not null,
			kind text not null,
			status text not null,
			capability_name text not null default '',
			input_json text not null default '{}',
			output_json text not null default '{}',
			error_text text not null default '',
			external_ref text not null default '',
			started_at datetime null,
			finished_at datetime null,
			created_at datetime not null
		)`,
		`create unique index if not exists idx_agent_steps_run_index_unique on agent_steps (run_id, "index")`,
		`create index if not exists idx_agent_steps_run_created on agent_steps (run_id, created_at asc)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

type SessionRepository struct {
	db *gorm.DB
	q  *query.Query
}

func NewSessionRepository(db *gorm.DB) *SessionRepository {
	db = infraDB.WithoutQueryCache(db)
	return &SessionRepository{
		db: db,
		q:  repositoryQuery(db),
	}
}

func (r *SessionRepository) FindOrCreateChatSession(ctx context.Context, appID, botOpenID, chatID string) (*agentruntime.AgentSession, error) {
	ins := r.q.AgentSession
	scopeType := string(agentruntime.ScopeTypeChat)
	scopeID := strings.TrimSpace(chatID)

	current, err := ins.WithContext(ctx).
		Where(
			ins.AppID.Eq(strings.TrimSpace(appID)),
			ins.BotOpenID.Eq(strings.TrimSpace(botOpenID)),
			ins.ScopeType.Eq(scopeType),
			ins.ScopeID.Eq(scopeID),
		).
		Take()
	switch {
	case err == nil:
		return sessionFromModel(current), nil
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, err
	}

	now := time.Now().UTC()
	entity := &model.AgentSession{
		ID:              newAgentID("session"),
		AppID:           strings.TrimSpace(appID),
		BotOpenID:       strings.TrimSpace(botOpenID),
		ChatID:          scopeID,
		ScopeType:       scopeType,
		ScopeID:         scopeID,
		Status:          string(agentruntime.SessionStatusIdle),
		ActiveRunID:     "",
		LastMessageID:   "",
		LastActorOpenID: "",
		MemoryVersion:   0,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := ins.WithContext(ctx).Create(entity); err != nil {
		current, findErr := ins.WithContext(ctx).
			Where(
				ins.AppID.Eq(entity.AppID),
				ins.BotOpenID.Eq(entity.BotOpenID),
				ins.ScopeType.Eq(entity.ScopeType),
				ins.ScopeID.Eq(entity.ScopeID),
			).
			Take()
		if findErr == nil {
			return sessionFromModel(current), nil
		}
		return nil, err
	}
	return sessionFromModel(entity), nil
}

func (r *SessionRepository) GetByID(ctx context.Context, id string) (*agentruntime.AgentSession, error) {
	ins := r.q.AgentSession
	entity, err := ins.WithContext(ctx).Where(ins.ID.Eq(strings.TrimSpace(id))).Take()
	if err != nil {
		return nil, err
	}
	return sessionFromModel(entity), nil
}

func (r *SessionRepository) SetActiveRun(ctx context.Context, sessionID, runID, lastMessageID, lastActorOpenID string, updatedAt time.Time) error {
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	updates := map[string]any{
		"active_run_id": strings.TrimSpace(runID),
		"status":        string(sessionStatusForRun(runID)),
		"updated_at":    updatedAt,
	}
	if trimmed := strings.TrimSpace(lastMessageID); trimmed != "" {
		updates["last_message_id"] = trimmed
	}
	if trimmed := strings.TrimSpace(lastActorOpenID); trimmed != "" {
		updates["last_actor_open_id"] = trimmed
	}

	ins := r.q.AgentSession
	_, err := ins.WithContext(ctx).
		Where(ins.ID.Eq(strings.TrimSpace(sessionID))).
		Updates(updates)
	return err
}

type RunRepository struct {
	db *gorm.DB
	q  *query.Query
}

func NewRunRepository(db *gorm.DB) *RunRepository {
	db = infraDB.WithoutQueryCache(db)
	return &RunRepository{
		db: db,
		q:  repositoryQuery(db),
	}
}

func (r *RunRepository) Create(ctx context.Context, run *agentruntime.AgentRun) error {
	if run == nil {
		return errors.New("agent run is nil")
	}
	if strings.TrimSpace(run.ID) == "" {
		run.ID = newAgentID("run")
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = run.CreatedAt
	}

	entity := runToModel(run)
	create := r.q.AgentRun.WithContext(ctx)
	if run.StartedAt == nil {
		create = create.Omit(r.q.AgentRun.StartedAt)
	}
	if run.FinishedAt == nil {
		create = create.Omit(r.q.AgentRun.FinishedAt)
	}
	if err := create.Create(entity); err != nil {
		return err
	}

	current, err := r.GetByID(ctx, run.ID)
	if err != nil {
		return err
	}
	*run = *current
	return nil
}

func (r *RunRepository) GetByID(ctx context.Context, id string) (*agentruntime.AgentRun, error) {
	ins := r.q.AgentRun
	entity, err := ins.WithContext(ctx).Where(ins.ID.Eq(strings.TrimSpace(id))).Take()
	if err != nil {
		return nil, err
	}
	return runFromModel(entity), nil
}

func (r *RunRepository) FindByTriggerMessage(ctx context.Context, sessionID, triggerMessageID string) (*agentruntime.AgentRun, error) {
	if strings.TrimSpace(triggerMessageID) == "" {
		return nil, nil
	}

	ins := r.q.AgentRun
	entity, err := ins.WithContext(ctx).
		Where(
			ins.SessionID.Eq(strings.TrimSpace(sessionID)),
			ins.TriggerMessageID.Eq(strings.TrimSpace(triggerMessageID)),
		).
		Take()
	switch {
	case err == nil:
		return runFromModel(entity), nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, nil
	default:
		return nil, err
	}
}

func (r *RunRepository) UpdateStatus(ctx context.Context, runID string, fromRevision int64, mutate func(*agentruntime.AgentRun) error) (*agentruntime.AgentRun, error) {
	var updated *agentruntime.AgentRun

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx = infraDB.WithoutQueryCache(tx)
		qtx := query.Use(tx)
		ins := qtx.AgentRun

		entity, err := ins.WithContext(ctx).Where(ins.ID.Eq(strings.TrimSpace(runID))).Take()
		if err != nil {
			return err
		}
		if entity.Revision != fromRevision {
			return ErrRevisionConflict
		}

		current := runFromModel(entity)
		if mutate != nil {
			if err := mutate(current); err != nil {
				return err
			}
		}
		if err := agentruntime.ValidateRunStatusTransition(agentruntime.RunStatus(entity.Status), current.Status); err != nil {
			return err
		}

		current.ID = entity.ID
		current.SessionID = entity.SessionID
		current.TriggerType = agentruntime.TriggerType(entity.TriggerType)
		current.TriggerMessageID = entity.TriggerMessageID
		current.TriggerEventID = entity.TriggerEventID
		current.ActorOpenID = entity.ActorOpenID
		current.ParentRunID = entity.ParentRunID
		current.Revision = entity.Revision + 1
		if current.CreatedAt.IsZero() {
			current.CreatedAt = entity.CreatedAt
		}
		if current.UpdatedAt.IsZero() {
			current.UpdatedAt = time.Now().UTC()
		}

		result, err := ins.WithContext(ctx).
			Where(ins.ID.Eq(entity.ID), ins.Revision.Eq(entity.Revision)).
			Updates(runUpdateMap(current))
		if err != nil {
			return err
		}
		if result.RowsAffected != 1 {
			return ErrRevisionConflict
		}

		updated = current
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

type StepRepository struct {
	db *gorm.DB
	q  *query.Query
}

func NewStepRepository(db *gorm.DB) *StepRepository {
	db = infraDB.WithoutQueryCache(db)
	return &StepRepository{
		db: db,
		q:  repositoryQuery(db),
	}
}

func (r *StepRepository) Append(ctx context.Context, step *agentruntime.AgentStep) error {
	if step == nil {
		return errors.New("agent step is nil")
	}
	if strings.TrimSpace(step.ID) == "" {
		step.ID = newAgentID("step")
	}
	if step.CreatedAt.IsZero() {
		step.CreatedAt = time.Now().UTC()
	}

	entity := stepToModel(step)
	create := r.q.AgentStep.WithContext(ctx)
	if step.StartedAt == nil {
		create = create.Omit(r.q.AgentStep.StartedAt)
	}
	if step.FinishedAt == nil {
		create = create.Omit(r.q.AgentStep.FinishedAt)
	}
	if err := create.Create(entity); err != nil {
		return err
	}
	return nil
}

func (r *StepRepository) ListByRun(ctx context.Context, runID string) ([]*agentruntime.AgentStep, error) {
	ins := r.q.AgentStep
	entities, err := ins.WithContext(ctx).
		Where(ins.RunID.Eq(strings.TrimSpace(runID))).
		Order(ins.Index.Asc()).
		Order(ins.CreatedAt.Asc()).
		Find()
	if err != nil {
		return nil, err
	}

	steps := make([]*agentruntime.AgentStep, 0, len(entities))
	for _, entity := range entities {
		steps = append(steps, stepFromModel(entity))
	}
	return steps, nil
}

func (r *StepRepository) UpdateStatus(ctx context.Context, stepID string, fromStatus agentruntime.StepStatus, mutate func(*agentruntime.AgentStep) error) (*agentruntime.AgentStep, error) {
	var updated *agentruntime.AgentStep

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx = infraDB.WithoutQueryCache(tx)
		qtx := query.Use(tx)
		ins := qtx.AgentStep

		entity, err := ins.WithContext(ctx).Where(ins.ID.Eq(strings.TrimSpace(stepID))).Take()
		if err != nil {
			return err
		}
		if agentruntime.StepStatus(entity.Status) != fromStatus {
			return ErrStepStatusConflict
		}

		current := stepFromModel(entity)
		if mutate != nil {
			if err := mutate(current); err != nil {
				return err
			}
		}
		if err := agentruntime.ValidateStepStatusTransition(agentruntime.StepStatus(entity.Status), current.Status); err != nil {
			return err
		}

		current.ID = entity.ID
		current.RunID = entity.RunID
		current.Index = int(entity.Index)
		current.Kind = agentruntime.StepKind(entity.Kind)
		if current.CreatedAt.IsZero() {
			current.CreatedAt = entity.CreatedAt
		}

		result, err := ins.WithContext(ctx).
			Where(ins.ID.Eq(entity.ID), ins.Status.Eq(entity.Status)).
			Updates(stepUpdateMap(current))
		if err != nil {
			return err
		}
		if result.RowsAffected != 1 {
			return ErrStepStatusConflict
		}

		updated = current
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func repositoryQuery(db *gorm.DB) *query.Query {
	if db == nil {
		return nil
	}
	return query.Use(db)
}

func sessionStatusForRun(runID string) agentruntime.SessionStatus {
	if strings.TrimSpace(runID) == "" {
		return agentruntime.SessionStatusIdle
	}
	return agentruntime.SessionStatusActive
}

func sessionFromModel(entity *model.AgentSession) *agentruntime.AgentSession {
	if entity == nil {
		return nil
	}
	return &agentruntime.AgentSession{
		ID:              entity.ID,
		AppID:           entity.AppID,
		BotOpenID:       entity.BotOpenID,
		ChatID:          entity.ChatID,
		ScopeType:       agentruntime.ScopeType(entity.ScopeType),
		ScopeID:         entity.ScopeID,
		Status:          agentruntime.SessionStatus(entity.Status),
		ActiveRunID:     entity.ActiveRunID,
		LastMessageID:   entity.LastMessageID,
		LastActorOpenID: entity.LastActorOpenID,
		MemoryVersion:   entity.MemoryVersion,
		CreatedAt:       entity.CreatedAt,
		UpdatedAt:       entity.UpdatedAt,
	}
}

func runFromModel(entity *model.AgentRun) *agentruntime.AgentRun {
	if entity == nil {
		return nil
	}
	return &agentruntime.AgentRun{
		ID:               entity.ID,
		SessionID:        entity.SessionID,
		TriggerType:      agentruntime.TriggerType(entity.TriggerType),
		TriggerMessageID: entity.TriggerMessageID,
		TriggerEventID:   entity.TriggerEventID,
		ActorOpenID:      entity.ActorOpenID,
		ParentRunID:      entity.ParentRunID,
		Status:           agentruntime.RunStatus(entity.Status),
		Goal:             entity.Goal,
		InputText:        entity.InputText,
		CurrentStepIndex: int(entity.CurrentStepIndex),
		WaitingReason:    agentruntime.WaitingReason(entity.WaitingReason),
		WaitingToken:     entity.WaitingToken,
		LastResponseID:   entity.LastResponseID,
		ResultSummary:    entity.ResultSummary,
		ErrorText:        entity.ErrorText,
		Revision:         entity.Revision,
		StartedAt:        timePtrFromValue(entity.StartedAt),
		FinishedAt:       timePtrFromValue(entity.FinishedAt),
		CreatedAt:        entity.CreatedAt,
		UpdatedAt:        entity.UpdatedAt,
	}
}

func stepFromModel(entity *model.AgentStep) *agentruntime.AgentStep {
	if entity == nil {
		return nil
	}
	return &agentruntime.AgentStep{
		ID:             entity.ID,
		RunID:          entity.RunID,
		Index:          int(entity.Index),
		Kind:           agentruntime.StepKind(entity.Kind),
		Status:         agentruntime.StepStatus(entity.Status),
		CapabilityName: entity.CapabilityName,
		InputJSON:      jsonStringToBytes(entity.InputJSON),
		OutputJSON:     jsonStringToBytes(entity.OutputJSON),
		ErrorText:      entity.ErrorText,
		ExternalRef:    entity.ExternalRef,
		StartedAt:      timePtrFromValue(entity.StartedAt),
		FinishedAt:     timePtrFromValue(entity.FinishedAt),
		CreatedAt:      entity.CreatedAt,
	}
}

func runToModel(run *agentruntime.AgentRun) *model.AgentRun {
	if run == nil {
		return nil
	}

	entity := &model.AgentRun{
		ID:               strings.TrimSpace(run.ID),
		SessionID:        strings.TrimSpace(run.SessionID),
		TriggerType:      string(run.TriggerType),
		TriggerMessageID: strings.TrimSpace(run.TriggerMessageID),
		TriggerEventID:   strings.TrimSpace(run.TriggerEventID),
		ActorOpenID:      strings.TrimSpace(run.ActorOpenID),
		ParentRunID:      strings.TrimSpace(run.ParentRunID),
		Status:           string(run.Status),
		Goal:             run.Goal,
		InputText:        run.InputText,
		CurrentStepIndex: int32(run.CurrentStepIndex),
		WaitingReason:    string(run.WaitingReason),
		WaitingToken:     run.WaitingToken,
		LastResponseID:   run.LastResponseID,
		ResultSummary:    run.ResultSummary,
		ErrorText:        run.ErrorText,
		Revision:         run.Revision,
		CreatedAt:        run.CreatedAt,
		UpdatedAt:        run.UpdatedAt,
	}
	if run.StartedAt != nil {
		entity.StartedAt = *run.StartedAt
	}
	if run.FinishedAt != nil {
		entity.FinishedAt = *run.FinishedAt
	}
	return entity
}

func runUpdateMap(run *agentruntime.AgentRun) map[string]any {
	values := map[string]any{
		"status":             string(run.Status),
		"goal":               run.Goal,
		"input_text":         run.InputText,
		"current_step_index": int32(run.CurrentStepIndex),
		"waiting_reason":     string(run.WaitingReason),
		"waiting_token":      run.WaitingToken,
		"last_response_id":   run.LastResponseID,
		"result_summary":     run.ResultSummary,
		"error_text":         run.ErrorText,
		"revision":           run.Revision,
		"updated_at":         run.UpdatedAt,
	}
	if run.StartedAt != nil {
		values["started_at"] = *run.StartedAt
	}
	if run.FinishedAt != nil {
		values["finished_at"] = *run.FinishedAt
	}
	return values
}

func stepToModel(step *agentruntime.AgentStep) *model.AgentStep {
	if step == nil {
		return nil
	}

	entity := &model.AgentStep{
		ID:             strings.TrimSpace(step.ID),
		RunID:          strings.TrimSpace(step.RunID),
		Index:          int32(step.Index),
		Kind:           string(step.Kind),
		Status:         string(step.Status),
		CapabilityName: strings.TrimSpace(step.CapabilityName),
		InputJSON:      normalizeJSONText(step.InputJSON),
		OutputJSON:     normalizeJSONText(step.OutputJSON),
		ErrorText:      step.ErrorText,
		ExternalRef:    step.ExternalRef,
		CreatedAt:      step.CreatedAt,
	}
	if step.StartedAt != nil {
		entity.StartedAt = *step.StartedAt
	}
	if step.FinishedAt != nil {
		entity.FinishedAt = *step.FinishedAt
	}
	return entity
}

func stepUpdateMap(step *agentruntime.AgentStep) map[string]any {
	values := map[string]any{
		"status":          string(step.Status),
		"capability_name": strings.TrimSpace(step.CapabilityName),
		"input_json":      normalizeJSONText(step.InputJSON),
		"output_json":     normalizeJSONText(step.OutputJSON),
		"error_text":      step.ErrorText,
		"external_ref":    step.ExternalRef,
	}
	if step.StartedAt != nil {
		values["started_at"] = *step.StartedAt
	}
	if step.FinishedAt != nil {
		values["finished_at"] = *step.FinishedAt
	}
	return values
}

func timePtrFromValue(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copied := value
	return &copied
}

func normalizeJSONText(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "{}"
	}
	return text
}

func jsonStringToBytes(raw string) []byte {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return []byte(raw)
}

func newAgentID(prefix string) string {
	return prefix + "_" + uuid.NewV4().String()
}

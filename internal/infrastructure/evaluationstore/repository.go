package evaluationstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type Repository struct {
	db     *gorm.DB
	tenant tenant.Tenant
}

var _ conversationeval.Store = (*Repository)(nil)

func NewRepository(db *gorm.DB, owner tenant.Tenant) (*Repository, error) {
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	db = infraDB.WithoutQueryCache(db)
	if db != nil {
		db = db.Session(&gorm.Session{Logger: logger.Discard})
		db = db.Scopes(scopeTenant(owner.ID))
	}
	return &Repository{db: db, tenant: owner}, nil
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
		case "evaluation_cohorts", "cohorts",
			"evaluation_episodes", "episodes",
			"evaluation_episode_messages",
			"evaluation_lane_outputs", "outputs",
			"evaluation_feedback", "feedback",
			"evaluation_judgments", "judgments",
			"evaluation_candidate_tasks":
		default:
			return database
		}
		return database.Where(clause.Eq{
			Column: clause.Column{Table: clause.CurrentTable, Name: "tenant_id"},
			Value:  tenantID,
		})
	}
}

func (r *Repository) CreateCohort(ctx context.Context, cohort conversationeval.Cohort) error {
	if err := cohort.Validate(); err != nil {
		return err
	}
	if cohort.Status != conversationeval.CohortStatusCollecting {
		return fmt.Errorf(
			"%w: new cohort must start in %q",
			conversationeval.ErrInvalidTransition, conversationeval.CohortStatusCollecting,
		)
	}
	if cohort.AppID != r.tenant.AppID || cohort.BotOpenID != r.tenant.BotOpenID {
		return fmt.Errorf("%w: cohort belongs to another tenant", conversationeval.ErrInvalidContract)
	}
	cohort.TenantID = r.tenant.ID
	db, err := r.database()
	if err != nil {
		return err
	}
	chatIDs, err := json.Marshal(cohort.ChatIDs)
	if err != nil {
		return fmt.Errorf("marshal cohort chat ids: %w", err)
	}
	return db.WithContext(ctx).Exec(`
		INSERT INTO evaluation_cohorts (
			id, tenant_id, app_id, bot_open_id, chat_ids, start_at, end_at, status,
			serving_lane, control_version, candidate_version, judge_config_json,
			sampling_policy_json, result_version
		) VALUES (?, ?, ?, ?, ?::jsonb, ?, ?, ?, ?, ?, ?, ?::jsonb, ?::jsonb, ?)`,
		cohort.ID, cohort.TenantID, cohort.AppID, cohort.BotOpenID,
		string(chatIDs), cohort.StartAt, cohort.EndAt,
		string(cohort.Status), string(cohort.ServingLane), cohort.ControlVersion,
		cohort.CandidateVersion, string(cohort.JudgeConfigJSON),
		string(cohort.SamplingPolicyJSON), cohort.ResultVersion,
	).Error
}

func (r *Repository) ActiveCohorts(
	ctx context.Context,
	chatID string,
	at time.Time,
) ([]conversationeval.Cohort, error) {
	if err := validateQueryID("chat_id", chatID); err != nil {
		return nil, err
	}
	if at.IsZero() {
		return nil, fmt.Errorf("%w: active cohort timestamp must not be zero", conversationeval.ErrInvalidContract)
	}
	db, err := r.database()
	if err != nil {
		return nil, err
	}
	chatJSON, err := json.Marshal([]string{chatID})
	if err != nil {
		return nil, fmt.Errorf("marshal active cohort chat id: %w", err)
	}
	var rows []cohortRow
	if err := db.WithContext(ctx).Raw(`
		SELECT id, tenant_id, app_id, bot_open_id, chat_ids::text AS chat_ids, start_at, end_at,
		       status, serving_lane, control_version, candidate_version,
		       judge_config_json::text AS judge_config_json,
		       sampling_policy_json::text AS sampling_policy_json,
		       result_version, created_at, updated_at
		FROM evaluation_cohorts
		WHERE tenant_id = ?
		  AND status = ?
		  AND start_at <= ?
		  AND end_at > ?
		  AND chat_ids @> ?::jsonb
		ORDER BY start_at, id`,
		r.tenant.ID, string(conversationeval.CohortStatusCollecting),
		at, at, string(chatJSON),
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	cohorts := make([]conversationeval.Cohort, 0, len(rows))
	for _, row := range rows {
		cohort, err := row.domain()
		if err != nil {
			return nil, err
		}
		cohorts = append(cohorts, cohort)
	}
	return cohorts, nil
}

func (r *Repository) GetOrCreateEpisode(
	ctx context.Context,
	episode conversationeval.Episode,
) (*conversationeval.Episode, error) {
	if err := episode.Validate(); err != nil {
		return nil, err
	}
	episode.TenantID = r.tenant.ID
	db, err := r.database()
	if err != nil {
		return nil, err
	}
	var stored *conversationeval.Episode
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cohort, err := loadCohort(tx, r.tenant.ID, episode.CohortID, true)
		if err != nil {
			return err
		}
		if err := validateEpisodeCohort(episode, cohort); err != nil {
			return err
		}
		canonical, found, err := loadEpisodeByNaturalKey(
			tx,
			r.tenant.ID,
			episode.CohortID,
			episode.AnchorEventID,
			true,
		)
		if err != nil {
			return err
		}
		if found {
			if err := validateEpisodeCohort(canonical, cohort); err != nil {
				return err
			}
			if err := validateEpisodeReplay(episode, canonical); err != nil {
				return err
			}
			stored = &canonical
			return nil
		}
		if cohort.Status != conversationeval.CohortStatusCollecting {
			return fmt.Errorf(
				"%w: cohort %q is %q and cannot accept a new episode",
				conversationeval.ErrInvalidTransition, cohort.ID, cohort.Status,
			)
		}
		if err := tx.Exec(`
			INSERT INTO evaluation_episodes (
				id, tenant_id, cohort_id, chat_id, run_id, anchor_event_id, anchor_message_id,
				topic_id, serving_lane, status, pre_window_start, anchor_at,
				post_window_end, late_feedback_until
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (cohort_id, anchor_event_id) DO NOTHING`,
			episode.ID, episode.TenantID, episode.CohortID, episode.ChatID, episode.RunID,
			episode.AnchorEventID, episode.AnchorMessageID, episode.TopicID,
			string(episode.ServingLane), string(episode.Status), episode.PreWindowStart,
			episode.AnchorAt, episode.PostWindowEnd, episode.LateFeedbackUntil,
		).Error; err != nil {
			return err
		}
		canonical, found, err = loadEpisodeByNaturalKey(
			tx,
			r.tenant.ID,
			episode.CohortID, episode.AnchorEventID,
			true,
		)
		if err != nil {
			return err
		}
		if !found {
			return gorm.ErrRecordNotFound
		}
		if err := validateEpisodeCohort(canonical, cohort); err != nil {
			return err
		}
		if err := validateEpisodeReplay(episode, canonical); err != nil {
			return err
		}
		stored = &canonical
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stored, nil
}

func (r *Repository) UpsertLaneOutput(ctx context.Context, output conversationeval.LaneOutput) error {
	if err := output.Validate(); err != nil {
		return err
	}
	output.TenantID = r.tenant.ID
	db, err := r.database()
	if err != nil {
		return err
	}
	contextJSON, err := json.Marshal(output.ContextSnapshot)
	if err != nil {
		return fmt.Errorf("marshal context snapshot: %w", err)
	}
	excluded := output.ExcludedContext
	if excluded == nil {
		excluded = []conversationeval.ExcludedContextItem{}
	}
	excludedJSON, err := json.Marshal(excluded)
	if err != nil {
		return fmt.Errorf("marshal excluded context: %w", err)
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		episode, err := loadEpisode(tx, r.tenant.ID, output.EpisodeID, true)
		if err != nil {
			return err
		}
		if output.ContextSnapshot.AnchorEventID != episode.AnchorEventID ||
			output.ContextSnapshot.AnchorAt.UnixMicro() != episode.AnchorAt.UnixMicro() {
			return fmt.Errorf(
				"%w: lane output snapshot anchor does not match episode %q",
				conversationeval.ErrInvalidContract, episode.ID,
			)
		}
		expectedMode := conversationeval.OutputModeShadow
		if output.Lane == episode.ServingLane {
			expectedMode = conversationeval.OutputModeActual
		}
		if output.OutputMode != expectedMode {
			return fmt.Errorf(
				"%w: lane %q for episode %q must use output mode %q",
				conversationeval.ErrInvalidContract, output.Lane, episode.ID, expectedMode,
			)
		}
		return tx.Exec(`
			INSERT INTO evaluation_lane_outputs (
				id, tenant_id, episode_id, lane, output_mode, activation_json, relevance_json,
				join_decision, topic_relation, context_snapshot_json, excluded_context_json,
				tool_plan_json, reply_text, latency_ms, token_usage_json, error_json
			) VALUES (?, ?, ?, ?, ?, ?::jsonb, ?::jsonb, ?, ?, ?::jsonb, ?::jsonb,
			          ?::jsonb, ?, ?, ?::jsonb, ?::jsonb)
			ON CONFLICT (episode_id, lane) DO UPDATE SET
				output_mode = EXCLUDED.output_mode,
				activation_json = EXCLUDED.activation_json,
				relevance_json = EXCLUDED.relevance_json,
				join_decision = EXCLUDED.join_decision,
				topic_relation = EXCLUDED.topic_relation,
				context_snapshot_json = EXCLUDED.context_snapshot_json,
				excluded_context_json = EXCLUDED.excluded_context_json,
				tool_plan_json = EXCLUDED.tool_plan_json,
				reply_text = EXCLUDED.reply_text,
				latency_ms = EXCLUDED.latency_ms,
				token_usage_json = EXCLUDED.token_usage_json,
				error_json = EXCLUDED.error_json,
				updated_at = now()`,
			output.ID, output.TenantID, output.EpisodeID,
			string(output.Lane), string(output.OutputMode),
			string(output.ActivationJSON), string(output.RelevanceJSON),
			string(output.JoinDecision), string(output.TopicRelation), string(contextJSON),
			string(excludedJSON), string(output.ToolPlanJSON), output.ReplyText,
			output.Latency.Milliseconds(), string(output.TokenUsageJSON), string(output.ErrorJSON),
		).Error
	})
}

func (r *Repository) AppendFeedback(ctx context.Context, feedback conversationeval.Feedback) error {
	if err := feedback.Validate(); err != nil {
		return err
	}
	feedback.TenantID = r.tenant.ID
	db, err := r.database()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		episode, err := loadEpisode(tx, r.tenant.ID, feedback.EpisodeID, false)
		if err != nil {
			return err
		}
		var cohortState struct {
			Status string
		}
		cohortResult := tx.Raw(`
			SELECT status
			FROM evaluation_cohorts
			WHERE id = ? AND tenant_id = ?
			LIMIT 1
			FOR UPDATE`,
			episode.CohortID, r.tenant.ID,
		).Scan(&cohortState)
		if cohortResult.Error != nil {
			return cohortResult.Error
		}
		if cohortResult.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		episode, err = loadEpisode(tx, r.tenant.ID, feedback.EpisodeID, true)
		if err != nil {
			return err
		}
		decision := conversationeval.DecideFeedbackWindow(
			episode,
			feedback.Explicitness,
			feedback.OccurredAt,
			cohortState.Status == string(conversationeval.CohortStatusFinalized),
		)
		if !decision.Attach {
			return fmt.Errorf(
				"%w: feedback is outside its attribution window: %s",
				conversationeval.ErrInvalidContract,
				decision.Reason,
			)
		}
		if feedback.TargetLane != "" {
			if feedback.TargetLane != episode.ServingLane {
				return fmt.Errorf(
					"%w: feedback target lane %q is not episode serving lane %q",
					conversationeval.ErrInvalidContract, feedback.TargetLane, episode.ServingLane,
				)
			}
			var actualCount int64
			if err := tx.Raw(`
				SELECT count(*)
				FROM evaluation_lane_outputs
				WHERE episode_id = ? AND tenant_id = ? AND lane = ? AND output_mode = ?
				  AND tool_plan_json->>'delivery_message_id' = ?`,
				episode.ID, r.tenant.ID,
				string(feedback.TargetLane), string(conversationeval.OutputModeActual),
				feedback.TargetMessageID,
			).Scan(&actualCount).Error; err != nil {
				return err
			}
			if actualCount != 1 {
				return fmt.Errorf(
					"%w: feedback target requires one corresponding actual lane output",
					conversationeval.ErrInvalidContract,
				)
			}
		}
		insert := tx.Exec(`
			INSERT INTO evaluation_feedback (
				id, tenant_id, episode_id, target_lane, target_message_id, feedback_event_id,
				feedback_type, explicitness, content_json, attribution_confidence, occurred_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?, ?)
			ON CONFLICT (episode_id, feedback_event_id) DO NOTHING`,
			feedback.ID, feedback.TenantID, feedback.EpisodeID,
			string(feedback.TargetLane), feedback.TargetMessageID,
			feedback.FeedbackEventID, string(feedback.FeedbackType), string(feedback.Explicitness),
			string(feedback.ContentJSON), feedback.AttributionConfidence, feedback.OccurredAt,
		)
		if insert.Error != nil {
			return insert.Error
		}
		if insert.RowsAffected == 1 {
			if err := tx.Exec(`
				UPDATE evaluation_episodes
				SET updated_at = now()
				WHERE id = ? AND tenant_id = ?`,
				episode.ID, r.tenant.ID,
			).Error; err != nil {
				return err
			}
		}
		if insert.RowsAffected == 1 && decision.IncrementResultVersion {
			return tx.Exec(`
				UPDATE evaluation_cohorts
				SET result_version = result_version + 1, updated_at = now()
				WHERE id = ? AND tenant_id = ?`,
				episode.CohortID, r.tenant.ID,
			).Error
		}
		return nil
	})
}

func (r *Repository) FeedbackCandidates(
	ctx context.Context,
	chatID string,
	occurredAt time.Time,
) ([]conversationeval.FeedbackCandidate, error) {
	if err := validateQueryID("chat_id", chatID); err != nil {
		return nil, err
	}
	if occurredAt.IsZero() {
		return nil, fmt.Errorf(
			"%w: feedback candidate timestamp must not be zero",
			conversationeval.ErrInvalidContract,
		)
	}
	db, err := r.database()
	if err != nil {
		return nil, err
	}
	var rows []feedbackCandidateRow
	if err := db.WithContext(ctx).Raw(`
		SELECT e.id, e.tenant_id, e.cohort_id, e.chat_id, e.run_id, e.anchor_event_id,
		       e.anchor_message_id, e.topic_id, e.serving_lane, e.status,
		       e.pre_window_start, e.anchor_at, e.post_window_end,
		       e.late_feedback_until, e.created_at, e.updated_at,
		       COALESCE(output.tool_plan_json->>'delivery_message_id', '') AS delivery_message_id
		FROM evaluation_episodes AS e
		LEFT JOIN evaluation_lane_outputs AS output
		  ON output.episode_id = e.id
		 AND output.lane = e.serving_lane
		 AND output.output_mode = ?
		WHERE e.chat_id = ?
		  AND e.tenant_id = ?
		  AND e.anchor_at <= ?
		  AND e.late_feedback_until >= ?
		ORDER BY e.anchor_at DESC, e.id
		LIMIT 256`,
		string(conversationeval.OutputModeActual),
		chatID,
		r.tenant.ID,
		occurredAt,
		occurredAt,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	candidates := make([]conversationeval.FeedbackCandidate, 0, len(rows))
	for _, row := range rows {
		episode, err := row.episode().domain()
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, conversationeval.FeedbackCandidate{
			Episode: episode, DeliveryMessageID: row.DeliveryMessageID,
		})
	}
	return candidates, nil
}

func (r *Repository) AppendJudgment(ctx context.Context, judgment conversationeval.Judgment) error {
	if err := judgment.Validate(); err != nil {
		return err
	}
	judgment.TenantID = r.tenant.ID
	db, err := r.database()
	if err != nil {
		return err
	}
	problemTags := judgment.ProblemTags
	if problemTags == nil {
		problemTags = []string{}
	}
	problemTagsJSON, err := json.Marshal(problemTags)
	if err != nil {
		return fmt.Errorf("marshal judgment problem tags: %w", err)
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if judgment.Version > 1 {
			var previousID string
			previous := tx.Raw(`
				SELECT id
				FROM evaluation_judgments
				WHERE episode_id = ? AND tenant_id = ? AND source = ? AND version = ?
				FOR SHARE`,
				judgment.EpisodeID, r.tenant.ID,
				string(judgment.Source), judgment.Version-1,
			).Scan(&previousID)
			if previous.Error != nil {
				return previous.Error
			}
			if previous.RowsAffected != 1 {
				return fmt.Errorf(
					"%w: judgment version %d has no version %d in the same episode/source",
					conversationeval.ErrInvalidContract, judgment.Version, judgment.Version-1,
				)
			}
			if previousID != judgment.SupersedesID {
				return fmt.Errorf(
					"%w: supersedes_id %q does not match prior judgment %q",
					conversationeval.ErrInvalidContract, judgment.SupersedesID, previousID,
				)
			}
		}
		if err := tx.Exec(`
			INSERT INTO evaluation_judgments (
				id, tenant_id, episode_id, version, source, evaluator_id, winner, scores_json,
				problem_tags_json, rationale, confidence, needs_review, supersedes_id
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?::jsonb, ?, ?, ?, ?)`,
			judgment.ID, judgment.TenantID, judgment.EpisodeID,
			judgment.Version, string(judgment.Source),
			judgment.EvaluatorID, string(judgment.Winner), string(judgment.ScoresJSON),
			string(problemTagsJSON), judgment.Rationale, judgment.Confidence,
			judgment.NeedsReview, judgment.SupersedesID,
		).Error; err != nil {
			return err
		}
		if judgment.Source == conversationeval.JudgmentSourceConversationJudge {
			return tx.Exec(`
				UPDATE evaluation_episodes
				SET status = CASE WHEN status = ? THEN ? ELSE status END,
				    updated_at = now()
				WHERE id = ? AND tenant_id = ?`,
				string(conversationeval.EpisodeStatusReadyForJudge),
				string(conversationeval.EpisodeStatusJudged),
				judgment.EpisodeID, r.tenant.ID,
			).Error
		}
		return nil
	})
}

func (r *Repository) EpisodesReadyForJudge(
	ctx context.Context,
	at time.Time,
	limit int,
) ([]conversationeval.Episode, error) {
	if at.IsZero() {
		return nil, fmt.Errorf("%w: judge timestamp must not be zero", conversationeval.ErrInvalidContract)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("%w: judge query limit must be positive", conversationeval.ErrInvalidContract)
	}
	db, err := r.database()
	if err != nil {
		return nil, err
	}
	var rows []episodeRow
	if err := db.WithContext(ctx).Raw(`
		SELECT e.id, e.tenant_id, e.cohort_id, e.chat_id, e.run_id, e.anchor_event_id,
		       e.anchor_message_id, e.topic_id, e.serving_lane, e.status,
		       e.pre_window_start, e.anchor_at, e.post_window_end,
		       e.late_feedback_until, e.created_at, e.updated_at
		FROM evaluation_episodes AS e
		WHERE e.status = ?
		  AND e.tenant_id = ?
		  AND e.post_window_end IS NOT NULL
		  AND e.post_window_end <= ?
		  AND (
		      SELECT count(DISTINCT lane)
		      FROM evaluation_lane_outputs AS output
		      WHERE output.episode_id = e.id
		        AND output.lane IN (?, ?)
		  ) = 2
		ORDER BY e.post_window_end, e.id
		LIMIT ?`,
		string(conversationeval.EpisodeStatusReadyForJudge), r.tenant.ID, at,
		string(conversationeval.LaneControl), string(conversationeval.LaneCandidate), limit,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	episodes := make([]conversationeval.Episode, 0, len(rows))
	for _, row := range rows {
		episode, err := row.domain()
		if err != nil {
			return nil, err
		}
		episodes = append(episodes, episode)
	}
	return episodes, nil
}

func (r *Repository) TransitionCohorts(ctx context.Context, at time.Time) (int64, error) {
	if at.IsZero() {
		return 0, fmt.Errorf("%w: cohort transition timestamp must not be zero", conversationeval.ErrInvalidContract)
	}
	db, err := r.database()
	if err != nil {
		return 0, err
	}
	var transitioned int64
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		collecting := tx.Exec(`
			UPDATE evaluation_cohorts
			SET status = ?, updated_at = ?
			WHERE status = ?
			  AND tenant_id = ?
			  AND end_at <= ?`,
			string(conversationeval.CohortStatusWaitingLateFeedback), at,
			string(conversationeval.CohortStatusCollecting), r.tenant.ID, at,
		)
		if collecting.Error != nil {
			return collecting.Error
		}
		finalized := tx.Exec(`
			UPDATE evaluation_cohorts
			SET status = ?, updated_at = ?
			WHERE status = ?
			  AND tenant_id = ?
			  AND end_at + (? * interval '1 second') <= ?`,
			string(conversationeval.CohortStatusFinalized), at,
			string(conversationeval.CohortStatusWaitingLateFeedback),
			r.tenant.ID,
			int64(conversationeval.LateFeedbackGracePeriod/time.Second), at,
		)
		if finalized.Error != nil {
			return finalized.Error
		}
		transitioned = finalized.RowsAffected + collecting.RowsAffected
		return nil
	})
	return transitioned, err
}

func (r *Repository) database() (*gorm.DB, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("conversation evaluation repository is not configured")
	}
	return r.db, nil
}

type cohortRow struct {
	ID                 string
	TenantID           string
	AppID              string
	BotOpenID          string
	ChatIDs            string
	StartAt            time.Time
	EndAt              time.Time
	Status             string
	ServingLane        string
	ControlVersion     string
	CandidateVersion   string
	JudgeConfigJSON    string
	SamplingPolicyJSON string
	ResultVersion      int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (r cohortRow) domain() (conversationeval.Cohort, error) {
	var chatIDs []string
	if err := json.Unmarshal([]byte(r.ChatIDs), &chatIDs); err != nil {
		return conversationeval.Cohort{}, fmt.Errorf("decode cohort %q chat ids: %w", r.ID, err)
	}
	value := conversationeval.Cohort{
		ID: r.ID, TenantID: r.TenantID,
		AppID: r.AppID, BotOpenID: r.BotOpenID, ChatIDs: chatIDs,
		StartAt: r.StartAt, EndAt: r.EndAt, Status: conversationeval.CohortStatus(r.Status),
		ServingLane: conversationeval.Lane(r.ServingLane), ControlVersion: r.ControlVersion,
		CandidateVersion: r.CandidateVersion, JudgeConfigJSON: json.RawMessage(r.JudgeConfigJSON),
		SamplingPolicyJSON: json.RawMessage(r.SamplingPolicyJSON), ResultVersion: r.ResultVersion,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if err := value.Validate(); err != nil {
		return conversationeval.Cohort{}, fmt.Errorf("stored cohort %q: %w", r.ID, err)
	}
	return value, nil
}

type episodeRow struct {
	ID                string
	TenantID          string
	CohortID          string
	ChatID            string
	RunID             string
	AnchorEventID     string
	AnchorMessageID   string
	TopicID           string
	ServingLane       string
	Status            string
	PreWindowStart    time.Time
	AnchorAt          time.Time
	PostWindowEnd     sql.NullTime
	LateFeedbackUntil time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type feedbackCandidateRow struct {
	ID                string
	TenantID          string
	CohortID          string
	ChatID            string
	RunID             string
	AnchorEventID     string
	AnchorMessageID   string
	TopicID           string
	ServingLane       string
	Status            string
	PreWindowStart    time.Time
	AnchorAt          time.Time
	PostWindowEnd     sql.NullTime
	LateFeedbackUntil time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeliveryMessageID string
}

func (r feedbackCandidateRow) episode() episodeRow {
	return episodeRow{
		ID: r.ID, TenantID: r.TenantID,
		CohortID: r.CohortID, ChatID: r.ChatID, RunID: r.RunID,
		AnchorEventID: r.AnchorEventID, AnchorMessageID: r.AnchorMessageID,
		TopicID: r.TopicID, ServingLane: r.ServingLane, Status: r.Status,
		PreWindowStart: r.PreWindowStart, AnchorAt: r.AnchorAt,
		PostWindowEnd: r.PostWindowEnd, LateFeedbackUntil: r.LateFeedbackUntil,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func (r episodeRow) domain() (conversationeval.Episode, error) {
	var postWindowEnd *time.Time
	if r.PostWindowEnd.Valid {
		value := r.PostWindowEnd.Time
		postWindowEnd = &value
	}
	episode := conversationeval.Episode{
		ID: r.ID, TenantID: r.TenantID,
		CohortID: r.CohortID, ChatID: r.ChatID, RunID: r.RunID,
		AnchorEventID: r.AnchorEventID, AnchorMessageID: r.AnchorMessageID,
		TopicID: r.TopicID, ServingLane: conversationeval.Lane(r.ServingLane),
		Status: conversationeval.EpisodeStatus(r.Status), PreWindowStart: r.PreWindowStart,
		AnchorAt: r.AnchorAt, PostWindowEnd: postWindowEnd,
		LateFeedbackUntil: r.LateFeedbackUntil, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if err := episode.Validate(); err != nil {
		return conversationeval.Episode{}, fmt.Errorf("stored episode %q: %w", r.ID, err)
	}
	return episode, nil
}

func validateQueryID(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf(
			"%w: %s must be non-empty without surrounding whitespace",
			conversationeval.ErrInvalidContract, name,
		)
	}
	return nil
}

func loadCohort(
	db *gorm.DB,
	tenantID string,
	cohortID string,
	lock bool,
) (conversationeval.Cohort, error) {
	query := `
		SELECT id, tenant_id, app_id, bot_open_id, chat_ids::text AS chat_ids, start_at, end_at,
		       status, serving_lane, control_version, candidate_version,
		       judge_config_json::text AS judge_config_json,
		       sampling_policy_json::text AS sampling_policy_json,
		       result_version, created_at, updated_at
		FROM evaluation_cohorts
		WHERE id = ? AND tenant_id = ?
		LIMIT 1`
	if lock {
		query += " FOR SHARE"
	}
	var row cohortRow
	result := db.Raw(query, cohortID, tenantID).Scan(&row)
	if result.Error != nil {
		return conversationeval.Cohort{}, result.Error
	}
	if result.RowsAffected != 1 {
		return conversationeval.Cohort{}, gorm.ErrRecordNotFound
	}
	return row.domain()
}

func loadEpisode(
	db *gorm.DB,
	tenantID string,
	episodeID string,
	lock bool,
) (conversationeval.Episode, error) {
	query := `
		SELECT id, tenant_id, cohort_id, chat_id, run_id, anchor_event_id, anchor_message_id,
		       topic_id, serving_lane, status, pre_window_start, anchor_at,
		       post_window_end, late_feedback_until, created_at, updated_at
		FROM evaluation_episodes
		WHERE id = ? AND tenant_id = ?
		LIMIT 1`
	if lock {
		query += " FOR SHARE"
	}
	var row episodeRow
	result := db.Raw(query, episodeID, tenantID).Scan(&row)
	if result.Error != nil {
		return conversationeval.Episode{}, result.Error
	}
	if result.RowsAffected != 1 {
		return conversationeval.Episode{}, gorm.ErrRecordNotFound
	}
	return row.domain()
}

func loadEpisodeByNaturalKey(
	db *gorm.DB,
	tenantID string,
	cohortID string,
	anchorEventID string,
	lock bool,
) (conversationeval.Episode, bool, error) {
	query := `
		SELECT id, tenant_id, cohort_id, chat_id, run_id, anchor_event_id, anchor_message_id,
		       topic_id, serving_lane, status, pre_window_start, anchor_at,
		       post_window_end, late_feedback_until, created_at, updated_at
		FROM evaluation_episodes
		WHERE cohort_id = ? AND anchor_event_id = ? AND tenant_id = ?
		LIMIT 1`
	if lock {
		query += " FOR SHARE"
	}
	var row episodeRow
	result := db.Raw(query, cohortID, anchorEventID, tenantID).Scan(&row)
	if result.Error != nil {
		return conversationeval.Episode{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		return conversationeval.Episode{}, false, nil
	}
	episode, err := row.domain()
	if err != nil {
		return conversationeval.Episode{}, false, err
	}
	return episode, true, nil
}

func validateEpisodeCohort(
	episode conversationeval.Episode,
	cohort conversationeval.Cohort,
) error {
	chatAllowed := false
	for _, chatID := range cohort.ChatIDs {
		if chatID == episode.ChatID {
			chatAllowed = true
			break
		}
	}
	if !chatAllowed {
		return fmt.Errorf(
			"%w: episode chat %q does not belong to cohort %q",
			conversationeval.ErrInvalidContract, episode.ChatID, cohort.ID,
		)
	}
	if episode.AnchorAt.Before(cohort.StartAt) || !episode.AnchorAt.Before(cohort.EndAt) {
		return fmt.Errorf(
			"%w: episode anchor_at must fall within cohort [start_at, end_at)",
			conversationeval.ErrInvalidContract,
		)
	}
	if episode.ServingLane != cohort.ServingLane {
		return fmt.Errorf(
			"%w: episode serving lane %q does not match cohort lane %q",
			conversationeval.ErrInvalidContract, episode.ServingLane, cohort.ServingLane,
		)
	}
	return nil
}

func validateEpisodeReplay(
	incoming conversationeval.Episode,
	canonical conversationeval.Episode,
) error {
	fields := []struct {
		name      string
		incoming  string
		canonical string
	}{
		{"cohort_id", incoming.CohortID, canonical.CohortID},
		{"anchor_event_id", incoming.AnchorEventID, canonical.AnchorEventID},
		{"chat_id", incoming.ChatID, canonical.ChatID},
		{"anchor_message_id", incoming.AnchorMessageID, canonical.AnchorMessageID},
		{"run_id", incoming.RunID, canonical.RunID},
		{"topic_id", incoming.TopicID, canonical.TopicID},
		{"serving_lane", string(incoming.ServingLane), string(canonical.ServingLane)},
	}
	for _, field := range fields {
		if field.incoming != field.canonical {
			return fmt.Errorf(
				"%w: episode replay field %s conflicts with canonical episode %q",
				conversationeval.ErrInvalidContract, field.name, canonical.ID,
			)
		}
	}
	times := []struct {
		name      string
		incoming  time.Time
		canonical time.Time
	}{
		{"pre_window_start", incoming.PreWindowStart, canonical.PreWindowStart},
		{"anchor_at", incoming.AnchorAt, canonical.AnchorAt},
		{"late_feedback_until", incoming.LateFeedbackUntil, canonical.LateFeedbackUntil},
	}
	for _, field := range times {
		if field.incoming.UnixMicro() != field.canonical.UnixMicro() {
			return fmt.Errorf(
				"%w: episode replay field %s conflicts with canonical episode %q",
				conversationeval.ErrInvalidContract, field.name, canonical.ID,
			)
		}
	}
	return nil
}

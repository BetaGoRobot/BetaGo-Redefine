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
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Repository struct {
	db *gorm.DB
}

var _ conversationeval.Store = (*Repository)(nil)

func NewRepository(db *gorm.DB) *Repository {
	db = infraDB.WithoutQueryCache(db)
	if db != nil {
		db = db.Session(&gorm.Session{Logger: logger.Discard})
	}
	return &Repository{db: db}
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
			id, app_id, bot_open_id, chat_ids, start_at, end_at, status,
			serving_lane, control_version, candidate_version, judge_config_json,
			sampling_policy_json, result_version
		) VALUES (?, ?, ?, ?::jsonb, ?, ?, ?, ?, ?, ?, ?::jsonb, ?::jsonb, ?)`,
		cohort.ID, cohort.AppID, cohort.BotOpenID, string(chatIDs), cohort.StartAt, cohort.EndAt,
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
		SELECT id, app_id, bot_open_id, chat_ids::text AS chat_ids, start_at, end_at,
		       status, serving_lane, control_version, candidate_version,
		       judge_config_json::text AS judge_config_json,
		       sampling_policy_json::text AS sampling_policy_json,
		       result_version, created_at, updated_at
		FROM evaluation_cohorts
		WHERE status = ?
		  AND start_at <= ?
		  AND end_at > ?
		  AND chat_ids @> ?::jsonb
		ORDER BY start_at, id`,
		string(conversationeval.CohortStatusCollecting), at, at, string(chatJSON),
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
	db, err := r.database()
	if err != nil {
		return nil, err
	}
	var stored *conversationeval.Episode
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			INSERT INTO evaluation_episodes (
				id, cohort_id, chat_id, run_id, anchor_event_id, anchor_message_id,
				topic_id, serving_lane, status, pre_window_start, anchor_at,
				post_window_end, late_feedback_until
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (cohort_id, anchor_event_id) DO NOTHING`,
			episode.ID, episode.CohortID, episode.ChatID, episode.RunID,
			episode.AnchorEventID, episode.AnchorMessageID, episode.TopicID,
			string(episode.ServingLane), string(episode.Status), episode.PreWindowStart,
			episode.AnchorAt, episode.PostWindowEnd, episode.LateFeedbackUntil,
		).Error; err != nil {
			return err
		}
		var row episodeRow
		result := tx.Raw(`
			SELECT id, cohort_id, chat_id, run_id, anchor_event_id, anchor_message_id,
			       topic_id, serving_lane, status, pre_window_start, anchor_at,
			       post_window_end, late_feedback_until, created_at, updated_at
			FROM evaluation_episodes
			WHERE cohort_id = ? AND anchor_event_id = ?
			LIMIT 1`,
			episode.CohortID, episode.AnchorEventID,
		).Scan(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		value := row.domain()
		stored = &value
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
	return db.WithContext(ctx).Exec(`
		INSERT INTO evaluation_lane_outputs (
			id, episode_id, lane, output_mode, activation_json, relevance_json,
			join_decision, topic_relation, context_snapshot_json, excluded_context_json,
			tool_plan_json, reply_text, latency_ms, token_usage_json, error_json
		) VALUES (?, ?, ?, ?, ?::jsonb, ?::jsonb, ?, ?, ?::jsonb, ?::jsonb,
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
		output.ID, output.EpisodeID, string(output.Lane), string(output.OutputMode),
		string(output.ActivationJSON), string(output.RelevanceJSON),
		string(output.JoinDecision), string(output.TopicRelation), string(contextJSON),
		string(excludedJSON), string(output.ToolPlanJSON), output.ReplyText,
		output.Latency.Milliseconds(), string(output.TokenUsageJSON), string(output.ErrorJSON),
	).Error
}

func (r *Repository) AppendFeedback(ctx context.Context, feedback conversationeval.Feedback) error {
	if err := feedback.Validate(); err != nil {
		return err
	}
	db, err := r.database()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Exec(`
		INSERT INTO evaluation_feedback (
			id, episode_id, target_lane, target_message_id, feedback_event_id,
			feedback_type, explicitness, content_json, attribution_confidence, occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?, ?)
		ON CONFLICT (episode_id, feedback_event_id) DO NOTHING`,
		feedback.ID, feedback.EpisodeID, string(feedback.TargetLane), feedback.TargetMessageID,
		feedback.FeedbackEventID, string(feedback.FeedbackType), string(feedback.Explicitness),
		string(feedback.ContentJSON), feedback.AttributionConfidence, feedback.OccurredAt,
	).Error
}

func (r *Repository) AppendJudgment(ctx context.Context, judgment conversationeval.Judgment) error {
	if err := judgment.Validate(); err != nil {
		return err
	}
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
				WHERE episode_id = ? AND source = ? AND version = ?
				FOR SHARE`,
				judgment.EpisodeID, string(judgment.Source), judgment.Version-1,
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
		return tx.Exec(`
			INSERT INTO evaluation_judgments (
				id, episode_id, version, source, evaluator_id, winner, scores_json,
				problem_tags_json, rationale, confidence, needs_review, supersedes_id
			) VALUES (?, ?, ?, ?, ?, ?, ?::jsonb, ?::jsonb, ?, ?, ?, ?)`,
			judgment.ID, judgment.EpisodeID, judgment.Version, string(judgment.Source),
			judgment.EvaluatorID, string(judgment.Winner), string(judgment.ScoresJSON),
			string(problemTagsJSON), judgment.Rationale, judgment.Confidence,
			judgment.NeedsReview, judgment.SupersedesID,
		).Error
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
		SELECT e.id, e.cohort_id, e.chat_id, e.run_id, e.anchor_event_id,
		       e.anchor_message_id, e.topic_id, e.serving_lane, e.status,
		       e.pre_window_start, e.anchor_at, e.post_window_end,
		       e.late_feedback_until, e.created_at, e.updated_at
		FROM evaluation_episodes AS e
		WHERE e.status = ?
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
		string(conversationeval.EpisodeStatusReadyForJudge), at,
		string(conversationeval.LaneControl), string(conversationeval.LaneCandidate), limit,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	episodes := make([]conversationeval.Episode, 0, len(rows))
	for _, row := range rows {
		episodes = append(episodes, row.domain())
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
		finalized := tx.Exec(`
			UPDATE evaluation_cohorts
			SET status = ?, updated_at = ?
			WHERE status = ?
			  AND end_at + (? * interval '1 second') <= ?`,
			string(conversationeval.CohortStatusFinalized), at,
			string(conversationeval.CohortStatusWaitingLateFeedback),
			int64(conversationeval.LateFeedbackGracePeriod/time.Second), at,
		)
		if finalized.Error != nil {
			return finalized.Error
		}
		collecting := tx.Exec(`
			UPDATE evaluation_cohorts
			SET status = ?, updated_at = ?
			WHERE status = ?
			  AND end_at <= ?`,
			string(conversationeval.CohortStatusWaitingLateFeedback), at,
			string(conversationeval.CohortStatusCollecting), at,
		)
		if collecting.Error != nil {
			return collecting.Error
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
		ID: r.ID, AppID: r.AppID, BotOpenID: r.BotOpenID, ChatIDs: chatIDs,
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

func (r episodeRow) domain() conversationeval.Episode {
	var postWindowEnd *time.Time
	if r.PostWindowEnd.Valid {
		value := r.PostWindowEnd.Time
		postWindowEnd = &value
	}
	return conversationeval.Episode{
		ID: r.ID, CohortID: r.CohortID, ChatID: r.ChatID, RunID: r.RunID,
		AnchorEventID: r.AnchorEventID, AnchorMessageID: r.AnchorMessageID,
		TopicID: r.TopicID, ServingLane: conversationeval.Lane(r.ServingLane),
		Status: conversationeval.EpisodeStatus(r.Status), PreWindowStart: r.PreWindowStart,
		AnchorAt: r.AnchorAt, PostWindowEnd: postWindowEnd,
		LateFeedbackUntil: r.LateFeedbackUntil, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
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

package evaluationstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"gorm.io/gorm"
)

var _ conversationeval.JudgeInputSource = (*Repository)(nil)

func (r *Repository) NextJudgeInput(
	ctx context.Context,
	at time.Time,
) (*conversationeval.JudgeInput, error) {
	if at.IsZero() {
		return nil, fmt.Errorf(
			"%w: judge timestamp must not be zero",
			conversationeval.ErrInvalidContract,
		)
	}
	db, err := r.database()
	if err != nil {
		return nil, err
	}
	var selected struct {
		ID string
	}
	result := db.WithContext(ctx).Raw(`
		SELECT e.id
		FROM evaluation_episodes AS e
		LEFT JOIN LATERAL (
			SELECT created_at
			FROM evaluation_judgments
			WHERE episode_id = e.id AND tenant_id = ? AND source = ?
			ORDER BY version DESC
			LIMIT 1
		) AS latest ON true
		WHERE e.status IN (?, ?)
		  AND e.tenant_id = ?
		  AND e.post_window_end IS NOT NULL
		  AND e.post_window_end <= ?
		  AND (
		      SELECT count(DISTINCT lane)
		      FROM evaluation_lane_outputs AS output
		      WHERE output.episode_id = e.id
		        AND output.tenant_id = ?
		        AND output.lane IN (?, ?)
		  ) = 2
		  AND (
		      e.status = ?
		      OR EXISTS (
		          SELECT 1
		          FROM evaluation_feedback AS feedback
		          WHERE feedback.episode_id = e.id
		            AND feedback.tenant_id = ?
		            AND feedback.created_at > COALESCE(latest.created_at, to_timestamp(0))
		      )
		  )
		ORDER BY e.post_window_end, e.id
		LIMIT 1`,
		r.tenant.ID, string(conversationeval.JudgmentSourceConversationJudge),
		string(conversationeval.EpisodeStatusReadyForJudge),
		string(conversationeval.EpisodeStatusJudged),
		r.tenant.ID,
		at,
		r.tenant.ID,
		string(conversationeval.LaneControl),
		string(conversationeval.LaneCandidate),
		string(conversationeval.EpisodeStatusReadyForJudge),
		r.tenant.ID,
	).Scan(&selected)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, conversationeval.ErrJudgeInputNotFound
	}
	return loadJudgeInput(db.WithContext(ctx), r.tenant.ID, selected.ID)
}

func loadJudgeInput(
	db *gorm.DB,
	tenantID string,
	episodeID string,
) (*conversationeval.JudgeInput, error) {
	episode, err := loadEpisode(db, tenantID, episodeID, false)
	if err != nil {
		return nil, err
	}

	var outputRows []judgeLaneOutputRow
	if err := db.Raw(`
		SELECT id, tenant_id, episode_id, lane, output_mode,
		       activation_json::text AS activation_json,
		       relevance_json::text AS relevance_json,
		       join_decision, topic_relation,
		       context_snapshot_json::text AS context_snapshot_json,
		       excluded_context_json::text AS excluded_context_json,
		       tool_plan_json::text AS tool_plan_json,
		       reply_text, latency_ms,
		       token_usage_json::text AS token_usage_json,
		       error_json::text AS error_json,
		       created_at, updated_at
		FROM evaluation_lane_outputs
		WHERE episode_id = ? AND tenant_id = ? AND lane IN (?, ?)
		ORDER BY lane`,
		episodeID, tenantID,
		string(conversationeval.LaneControl),
		string(conversationeval.LaneCandidate),
	).Scan(&outputRows).Error; err != nil {
		return nil, err
	}
	outputs := make(map[conversationeval.Lane]conversationeval.LaneOutput, len(outputRows))
	for _, row := range outputRows {
		output, decodeErr := row.domain()
		if decodeErr != nil {
			return nil, decodeErr
		}
		outputs[output.Lane] = output
	}

	var messageRows []struct {
		PayloadJSON string
	}
	if err := db.Raw(`
		SELECT payload_json::text AS payload_json
		FROM evaluation_episode_messages
		WHERE episode_id = ? AND tenant_id = ?
		ORDER BY
			CASE position WHEN 'pre' THEN 0 WHEN 'anchor' THEN 1 ELSE 2 END,
			sequence, occurred_at, event_id`,
		episodeID, tenantID,
	).Scan(&messageRows).Error; err != nil {
		return nil, err
	}
	messages := make([]conversationeval.WindowMessage, 0, len(messageRows))
	for _, row := range messageRows {
		var message conversationeval.WindowMessage
		if err := json.Unmarshal([]byte(row.PayloadJSON), &message); err != nil {
			return nil, fmt.Errorf("decode judge window message: %w", err)
		}
		messages = append(messages, message)
	}

	var feedbackRows []judgeFeedbackRow
	if err := db.Raw(`
		SELECT id, tenant_id, episode_id, target_lane, target_message_id, feedback_event_id,
		       feedback_type, explicitness, content_json::text AS content_json,
		       attribution_confidence, occurred_at, created_at
		FROM evaluation_feedback
		WHERE episode_id = ? AND tenant_id = ?
		ORDER BY occurred_at, id`,
		episodeID, tenantID,
	).Scan(&feedbackRows).Error; err != nil {
		return nil, err
	}
	feedback := make([]conversationeval.Feedback, 0, len(feedbackRows))
	for _, row := range feedbackRows {
		item, decodeErr := row.domain()
		if decodeErr != nil {
			return nil, decodeErr
		}
		feedback = append(feedback, item)
	}

	var latest struct {
		ID      string
		Version int64
	}
	latestResult := db.Raw(`
		SELECT id, version
		FROM evaluation_judgments
		WHERE episode_id = ? AND tenant_id = ? AND source = ?
		ORDER BY version DESC
		LIMIT 1`,
		episodeID, tenantID,
		string(conversationeval.JudgmentSourceConversationJudge),
	).Scan(&latest)
	if latestResult.Error != nil {
		return nil, latestResult.Error
	}
	version := int64(1)
	previousID := ""
	if latestResult.RowsAffected == 1 {
		version = latest.Version + 1
		previousID = latest.ID
	}
	input := &conversationeval.JudgeInput{
		Episode: episode, Version: version, PreviousJudgmentID: previousID,
		Messages: messages, ControlOutput: outputs[conversationeval.LaneControl],
		CandidateOutput: outputs[conversationeval.LaneCandidate], Feedback: feedback,
	}
	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("stored judge input %q: %w", episodeID, err)
	}
	return input, nil
}

type judgeLaneOutputRow struct {
	ID                  string
	TenantID            string
	EpisodeID           string
	Lane                string
	OutputMode          string
	ActivationJSON      string
	RelevanceJSON       string
	JoinDecision        string
	TopicRelation       string
	ContextSnapshotJSON string
	ExcludedContextJSON string
	ToolPlanJSON        string
	ReplyText           string
	LatencyMS           int64
	TokenUsageJSON      string
	ErrorJSON           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (r judgeLaneOutputRow) domain() (conversationeval.LaneOutput, error) {
	var snapshot conversationeval.ContextSnapshot
	if err := json.Unmarshal([]byte(r.ContextSnapshotJSON), &snapshot); err != nil {
		return conversationeval.LaneOutput{}, fmt.Errorf(
			"decode lane output %q context: %w",
			r.ID,
			err,
		)
	}
	var excluded []conversationeval.ExcludedContextItem
	if err := json.Unmarshal([]byte(r.ExcludedContextJSON), &excluded); err != nil {
		return conversationeval.LaneOutput{}, fmt.Errorf(
			"decode lane output %q excluded context: %w",
			r.ID,
			err,
		)
	}
	output := conversationeval.LaneOutput{
		ID: r.ID, TenantID: r.TenantID,
		EpisodeID: r.EpisodeID, Lane: conversationeval.Lane(r.Lane),
		OutputMode:      conversationeval.OutputMode(r.OutputMode),
		ActivationJSON:  json.RawMessage(r.ActivationJSON),
		RelevanceJSON:   json.RawMessage(r.RelevanceJSON),
		JoinDecision:    conversationeval.JoinDecision(r.JoinDecision),
		TopicRelation:   conversationeval.TopicRelation(r.TopicRelation),
		ContextSnapshot: snapshot, ExcludedContext: excluded,
		ToolPlanJSON: json.RawMessage(r.ToolPlanJSON), ReplyText: r.ReplyText,
		Latency:        time.Duration(r.LatencyMS) * time.Millisecond,
		TokenUsageJSON: json.RawMessage(r.TokenUsageJSON),
		ErrorJSON:      json.RawMessage(r.ErrorJSON),
		CreatedAt:      r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if err := output.Validate(); err != nil {
		return conversationeval.LaneOutput{}, fmt.Errorf(
			"stored lane output %q: %w",
			r.ID,
			err,
		)
	}
	return output, nil
}

type judgeFeedbackRow struct {
	ID                    string
	TenantID              string
	EpisodeID             string
	TargetLane            string
	TargetMessageID       string
	FeedbackEventID       string
	FeedbackType          string
	Explicitness          string
	ContentJSON           string
	AttributionConfidence int
	OccurredAt            time.Time
	CreatedAt             time.Time
}

func (r judgeFeedbackRow) domain() (conversationeval.Feedback, error) {
	feedback := conversationeval.Feedback{
		ID: r.ID, TenantID: r.TenantID,
		EpisodeID: r.EpisodeID, TargetLane: conversationeval.Lane(r.TargetLane),
		TargetMessageID: r.TargetMessageID, FeedbackEventID: r.FeedbackEventID,
		FeedbackType:          conversationeval.FeedbackType(r.FeedbackType),
		Explicitness:          conversationeval.FeedbackExplicitness(r.Explicitness),
		ContentJSON:           json.RawMessage(r.ContentJSON),
		AttributionConfidence: r.AttributionConfidence,
		OccurredAt:            r.OccurredAt, CreatedAt: r.CreatedAt,
	}
	if err := feedback.Validate(); err != nil {
		return conversationeval.Feedback{}, fmt.Errorf(
			"stored feedback %q: %w",
			r.ID,
			err,
		)
	}
	return feedback, nil
}

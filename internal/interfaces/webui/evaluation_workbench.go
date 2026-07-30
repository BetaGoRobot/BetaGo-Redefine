package webui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	uuid "github.com/satori/go.uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalidEvaluationQuery = errors.New("invalid evaluation query")
	ErrEvaluationUnavailable  = errors.New("evaluation workbench unavailable")
	ErrEvaluationNotFound     = errors.New("evaluation episode not found")
)

const maxEvaluationQueryWindow = 31 * 24 * time.Hour

type EvaluationListQuery struct {
	AppID, BotOpenID                 string
	ChatID, CohortID, Status, Winner string
	NeedsReview                      *bool
	From, To                         time.Time
	CursorAnchorAt                   time.Time
	CursorID                         string
	Limit                            int
}

type EvaluationCursor struct {
	AnchorAt  time.Time `json:"anchor_at"`
	EpisodeID string    `json:"episode_id"`
}

func (c EvaluationCursor) Encode() string {
	encoded, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func DecodeEvaluationCursor(raw string) (EvaluationCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return EvaluationCursor{}, ErrInvalidEvaluationQuery
	}
	var cursor EvaluationCursor
	if json.Unmarshal(decoded, &cursor) != nil || cursor.AnchorAt.IsZero() ||
		strings.TrimSpace(cursor.EpisodeID) == "" ||
		cursor.EpisodeID != strings.TrimSpace(cursor.EpisodeID) {
		return EvaluationCursor{}, ErrInvalidEvaluationQuery
	}
	return cursor, nil
}

type EvaluationEpisodeSummary struct {
	ID               string     `json:"id"`
	CohortID         string     `json:"cohort_id"`
	ChatID           string     `json:"chat_id"`
	AnchorMessageID  string     `json:"anchor_message_id"`
	TopicID          string     `json:"topic_id,omitempty"`
	Status           string     `json:"status"`
	ServingLane      string     `json:"serving_lane"`
	AnchorAt         time.Time  `json:"anchor_at"`
	PostWindowEnd    *time.Time `json:"post_window_end,omitempty"`
	PostWindowReason string     `json:"post_window_reason,omitempty"`
	ControlJoin      string     `json:"control_join,omitempty"`
	CandidateJoin    string     `json:"candidate_join,omitempty"`
	ControlTopic     string     `json:"control_topic,omitempty"`
	CandidateTopic   string     `json:"candidate_topic,omitempty"`
	JudgeWinner      string     `json:"judge_winner,omitempty"`
	HumanWinner      string     `json:"human_winner,omitempty"`
	Confidence       int        `json:"confidence,omitempty"`
	NeedsReview      bool       `json:"needs_review"`
	FeedbackCount    int64      `json:"feedback_count"`
}

type EvaluationEpisodePage struct {
	Items      []EvaluationEpisodeSummary `json:"items"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

type EvaluationMessageView struct {
	ID          string          `json:"id"`
	Position    string          `json:"position"`
	EventID     string          `json:"event_id"`
	MessageID   string          `json:"message_id"`
	Sequence    int32           `json:"sequence"`
	OccurredAt  time.Time       `json:"occurred_at"`
	PayloadJSON json.RawMessage `json:"payload"`
}

type EvaluationLaneOutputView struct {
	ID                  string          `json:"id"`
	Lane                string          `json:"lane"`
	OutputMode          string          `json:"output_mode"`
	JoinDecision        string          `json:"join_decision"`
	TopicRelation       string          `json:"topic_relation"`
	ActivationJSON      json.RawMessage `json:"activation"`
	RelevanceJSON       json.RawMessage `json:"relevance"`
	ContextSnapshotJSON json.RawMessage `json:"context_snapshot"`
	ExcludedContextJSON json.RawMessage `json:"excluded_context"`
	ToolPlanJSON        json.RawMessage `json:"tool_plan"`
	TokenUsageJSON      json.RawMessage `json:"token_usage"`
	ErrorJSON           json.RawMessage `json:"error"`
	ReplyText           string          `json:"reply_text"`
	LatencyMs           int64           `json:"latency_ms"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type EvaluationFeedbackView struct {
	ID                    string          `json:"id"`
	TargetLane            string          `json:"target_lane,omitempty"`
	TargetMessageID       string          `json:"target_message_id,omitempty"`
	FeedbackEventID       string          `json:"feedback_event_id"`
	FeedbackType          string          `json:"feedback_type"`
	Explicitness          string          `json:"explicitness"`
	ContentJSON           json.RawMessage `json:"content"`
	AttributionConfidence int             `json:"attribution_confidence"`
	OccurredAt            time.Time       `json:"occurred_at"`
	CreatedAt             time.Time       `json:"created_at"`
}

type EvaluationJudgmentView struct {
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	EvaluatorID     string          `json:"evaluator_id"`
	Winner          string          `json:"winner"`
	Rationale       string          `json:"rationale"`
	SupersedesID    string          `json:"supersedes_id,omitempty"`
	Version         int64           `json:"version"`
	Confidence      int64           `json:"confidence"`
	ScoresJSON      json.RawMessage `json:"scores"`
	ProblemTagsJSON json.RawMessage `json:"problem_tags"`
	NeedsReview     bool            `json:"needs_review"`
	CreatedAt       time.Time       `json:"created_at"`
}

type EvaluationEpisodeDetail struct {
	Episode   EvaluationEpisodeSummary   `json:"episode"`
	Messages  []EvaluationMessageView    `json:"messages"`
	Outputs   []EvaluationLaneOutputView `json:"outputs"`
	Feedback  []EvaluationFeedbackView   `json:"feedback"`
	Judgments []EvaluationJudgmentView   `json:"judgments"`
}

type EvaluationWorkbench interface {
	ListEpisodes(context.Context, EvaluationListQuery) (EvaluationEpisodePage, error)
	GetEpisode(context.Context, string, string, string) (*EvaluationEpisodeDetail, error)
	AppendHumanJudgment(
		context.Context,
		string,
		string,
		string,
		HumanJudgmentRequest,
	) (*EvaluationJudgmentView, error)
}

type HumanJudgmentRequest struct {
	EvaluatorID string          `json:"evaluator_id"`
	Winner      string          `json:"winner"`
	ScoresJSON  json.RawMessage `json:"scores"`
	ProblemTags []string        `json:"problem_tags"`
	Rationale   string          `json:"rationale"`
	Confidence  int             `json:"confidence"`
	NeedsReview bool            `json:"needs_review"`
}

func (r HumanJudgmentRequest) validate() error {
	if strings.TrimSpace(r.EvaluatorID) == "" ||
		r.EvaluatorID != strings.TrimSpace(r.EvaluatorID) ||
		len(r.EvaluatorID) > 255 {
		return ErrInvalidEvaluationQuery
	}
	switch r.Winner {
	case "control", "candidate", "tie":
	default:
		return ErrInvalidEvaluationQuery
	}
	var scores map[string]json.RawMessage
	if len(r.ScoresJSON) == 0 || json.Unmarshal(r.ScoresJSON, &scores) != nil ||
		scores == nil {
		return ErrInvalidEvaluationQuery
	}
	if strings.TrimSpace(r.Rationale) == "" ||
		utf8.RuneCountInString(r.Rationale) > 4000 ||
		r.Confidence < 0 || r.Confidence > 100 ||
		len(r.ProblemTags) > 20 {
		return ErrInvalidEvaluationQuery
	}
	seen := make(map[string]struct{}, len(r.ProblemTags))
	for _, tag := range r.ProblemTags {
		if strings.TrimSpace(tag) == "" || tag != strings.TrimSpace(tag) ||
			len(tag) > 64 {
			return ErrInvalidEvaluationQuery
		}
		if _, exists := seen[tag]; exists {
			return ErrInvalidEvaluationQuery
		}
		seen[tag] = struct{}{}
	}
	return nil
}

type evaluationWorkbenchStore struct {
	db *gorm.DB
}

func newEvaluationWorkbenchStore(db *gorm.DB) *evaluationWorkbenchStore {
	return &evaluationWorkbenchStore{db: db}
}

func (s *evaluationWorkbenchStore) ListEpisodes(
	ctx context.Context,
	query EvaluationListQuery,
) (EvaluationEpisodePage, error) {
	if err := query.validate(); err != nil {
		return EvaluationEpisodePage{}, err
	}
	if s == nil || s.db == nil {
		return EvaluationEpisodePage{}, ErrEvaluationUnavailable
	}
	args := []any{query.AppID, query.BotOpenID, query.From.UTC(), query.To.UTC()}
	where := []string{
		"c.app_id = ?", "c.bot_open_id = ?",
		"e.anchor_at >= ?", "e.anchor_at < ?",
	}
	add := func(clause string, value any) {
		where = append(where, clause)
		args = append(args, value)
	}
	if query.ChatID != "" {
		add("e.chat_id = ?", query.ChatID)
	}
	if query.CohortID != "" {
		add("e.cohort_id = ?", query.CohortID)
	}
	if query.Status != "" {
		add("e.status = ?", query.Status)
	}
	if query.Winner != "" {
		add("COALESCE(h.winner, j.winner, '') = ?", query.Winner)
	}
	if query.NeedsReview != nil {
		add("COALESCE(h.needs_review, j.needs_review, false) = ?", *query.NeedsReview)
	}
	if !query.CursorAnchorAt.IsZero() {
		where = append(where, "(e.anchor_at < ? OR (e.anchor_at = ? AND e.id < ?))")
		args = append(args, query.CursorAnchorAt.UTC(), query.CursorAnchorAt.UTC(), query.CursorID)
	}
	args = append(args, query.Limit+1)
	var rows []evaluationSummaryRow
	statement := fmt.Sprintf(`
		SELECT e.id, e.cohort_id, e.chat_id, e.anchor_message_id, e.topic_id,
		       e.status, e.serving_lane, e.anchor_at, e.post_window_end,
		       e.post_window_reason,
		       control.join_decision AS control_join,
		       candidate.join_decision AS candidate_join,
		       control.topic_relation AS control_topic,
		       candidate.topic_relation AS candidate_topic,
		       j.winner AS judge_winner, h.winner AS human_winner,
		       COALESCE(h.confidence, j.confidence, 0) AS confidence,
		       COALESCE(h.needs_review, j.needs_review, false) AS needs_review,
		       (SELECT count(*) FROM evaluation_feedback f
		        WHERE f.episode_id = e.id) AS feedback_count
		FROM evaluation_episodes e
		JOIN evaluation_cohorts c ON c.id = e.cohort_id
		LEFT JOIN evaluation_lane_outputs control
		  ON control.episode_id = e.id AND control.lane = 'control'
		LEFT JOIN evaluation_lane_outputs candidate
		  ON candidate.episode_id = e.id AND candidate.lane = 'candidate'
		LEFT JOIN evaluation_judgments j ON j.id = (
		  SELECT id FROM evaluation_judgments
		  WHERE episode_id = e.id AND source = 'conversation_evaluation_judge'
		  ORDER BY version DESC LIMIT 1)
		LEFT JOIN evaluation_judgments h ON h.id = (
		  SELECT id FROM evaluation_judgments
		  WHERE episode_id = e.id AND source = 'human'
		  ORDER BY version DESC LIMIT 1)
		WHERE %s
		ORDER BY e.anchor_at DESC, e.id DESC
		LIMIT ?`, strings.Join(where, " AND "))
	if err := s.db.WithContext(ctx).Raw(statement, args...).Scan(&rows).Error; err != nil {
		return EvaluationEpisodePage{}, err
	}
	page := EvaluationEpisodePage{
		Items: make([]EvaluationEpisodeSummary, 0, min(len(rows), query.Limit)),
	}
	for index, row := range rows {
		if index == query.Limit {
			last := page.Items[len(page.Items)-1]
			page.NextCursor = (EvaluationCursor{
				AnchorAt: last.AnchorAt, EpisodeID: last.ID,
			}).Encode()
			break
		}
		page.Items = append(page.Items, row.view())
	}
	return page, nil
}

func (q EvaluationListQuery) validate() error {
	if strings.TrimSpace(q.AppID) == "" || strings.TrimSpace(q.BotOpenID) == "" ||
		q.AppID != strings.TrimSpace(q.AppID) ||
		q.BotOpenID != strings.TrimSpace(q.BotOpenID) ||
		q.From.IsZero() || q.To.IsZero() || !q.To.After(q.From) ||
		q.To.Sub(q.From) > maxEvaluationQueryWindow ||
		q.Limit <= 0 || q.Limit > 100 {
		return ErrInvalidEvaluationQuery
	}
	if q.CursorAnchorAt.IsZero() != (q.CursorID == "") {
		return ErrInvalidEvaluationQuery
	}
	return nil
}

type evaluationSummaryRow struct {
	ID, CohortID, ChatID, AnchorMessageID, TopicID string
	Status, ServingLane, PostWindowReason          string
	ControlJoin, CandidateJoin                     string
	ControlTopic, CandidateTopic                   string
	JudgeWinner, HumanWinner                       string
	AnchorAt                                       time.Time
	PostWindowEnd                                  *time.Time
	Confidence                                     int
	NeedsReview                                    bool
	FeedbackCount                                  int64
}

func (r evaluationSummaryRow) view() EvaluationEpisodeSummary {
	return EvaluationEpisodeSummary{
		ID: r.ID, CohortID: r.CohortID, ChatID: r.ChatID,
		AnchorMessageID: r.AnchorMessageID, TopicID: r.TopicID,
		Status: r.Status, ServingLane: r.ServingLane, AnchorAt: r.AnchorAt,
		PostWindowEnd: r.PostWindowEnd, PostWindowReason: r.PostWindowReason,
		ControlJoin: r.ControlJoin, CandidateJoin: r.CandidateJoin,
		ControlTopic: r.ControlTopic, CandidateTopic: r.CandidateTopic,
		JudgeWinner: r.JudgeWinner, HumanWinner: r.HumanWinner,
		Confidence: r.Confidence, NeedsReview: r.NeedsReview,
		FeedbackCount: r.FeedbackCount,
	}
}

func (s *evaluationWorkbenchStore) GetEpisode(
	ctx context.Context,
	appID, botOpenID, episodeID string,
) (*EvaluationEpisodeDetail, error) {
	for _, value := range []string{appID, botOpenID, episodeID} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return nil, ErrInvalidEvaluationQuery
		}
	}
	if s == nil || s.db == nil {
		return nil, ErrEvaluationUnavailable
	}
	var summary evaluationSummaryRow
	result := s.db.WithContext(ctx).Raw(`
		SELECT e.id, e.cohort_id, e.chat_id, e.anchor_message_id, e.topic_id,
		       e.status, e.serving_lane, e.anchor_at, e.post_window_end,
		       e.post_window_reason
		FROM evaluation_episodes e
		JOIN evaluation_cohorts c ON c.id = e.cohort_id
		WHERE e.id = ? AND c.app_id = ? AND c.bot_open_id = ?`,
		episodeID, appID, botOpenID,
	).Scan(&summary)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrEvaluationNotFound
	}
	detail := &EvaluationEpisodeDetail{Episode: summary.view()}
	if err := s.loadEpisodeChildren(ctx, episodeID, detail); err != nil {
		return nil, err
	}
	return detail, nil
}

func (s *evaluationWorkbenchStore) loadEpisodeChildren(
	ctx context.Context,
	episodeID string,
	detail *EvaluationEpisodeDetail,
) error {
	type messageRow struct {
		ID, Position, EventID, MessageID, PayloadJSON string
		Sequence                                      int32
		OccurredAt                                    time.Time
	}
	var messages []messageRow
	if err := s.db.WithContext(ctx).Raw(`
		SELECT id, position, event_id, message_id, sequence, occurred_at,
		       payload_json::text AS payload_json
		FROM evaluation_episode_messages WHERE episode_id = ?
		ORDER BY occurred_at, sequence, event_id`, episodeID).Scan(&messages).Error; err != nil {
		return err
	}
	detail.Messages = make([]EvaluationMessageView, 0, len(messages))
	for _, row := range messages {
		detail.Messages = append(detail.Messages, EvaluationMessageView{
			ID: row.ID, Position: row.Position, EventID: row.EventID,
			MessageID: row.MessageID, Sequence: row.Sequence,
			OccurredAt: row.OccurredAt, PayloadJSON: rawJSON(row.PayloadJSON, `{}`),
		})
	}
	type outputRow struct {
		ID, Lane, OutputMode, JoinDecision, TopicRelation  string
		ActivationJSON, RelevanceJSON, ContextSnapshotJSON string
		ExcludedContextJSON, ToolPlanJSON                  string
		ReplyText, TokenUsageJSON, ErrorJSON               string
		LatencyMs                                          int64
		CreatedAt, UpdatedAt                               time.Time
	}
	var outputs []outputRow
	if err := s.db.WithContext(ctx).Raw(`
		SELECT id, lane, output_mode, join_decision, topic_relation,
		       activation_json::text AS activation_json,
		       relevance_json::text AS relevance_json,
		       context_snapshot_json::text AS context_snapshot_json,
		       excluded_context_json::text AS excluded_context_json,
		       tool_plan_json::text AS tool_plan_json, reply_text, latency_ms,
		       token_usage_json::text AS token_usage_json,
		       error_json::text AS error_json, created_at, updated_at
		FROM evaluation_lane_outputs WHERE episode_id = ?
		ORDER BY lane`, episodeID).Scan(&outputs).Error; err != nil {
		return err
	}
	detail.Outputs = make([]EvaluationLaneOutputView, 0, len(outputs))
	for _, row := range outputs {
		detail.Outputs = append(detail.Outputs, EvaluationLaneOutputView{
			ID: row.ID, Lane: row.Lane, OutputMode: row.OutputMode,
			JoinDecision: row.JoinDecision, TopicRelation: row.TopicRelation,
			ActivationJSON:      rawJSON(row.ActivationJSON, `{}`),
			RelevanceJSON:       rawJSON(row.RelevanceJSON, `{}`),
			ContextSnapshotJSON: rawJSON(row.ContextSnapshotJSON, `{}`),
			ExcludedContextJSON: rawJSON(row.ExcludedContextJSON, `[]`),
			ToolPlanJSON:        rawJSON(row.ToolPlanJSON, `{}`),
			ReplyText:           row.ReplyText, LatencyMs: row.LatencyMs,
			TokenUsageJSON: rawJSON(row.TokenUsageJSON, `{}`),
			ErrorJSON:      rawJSON(row.ErrorJSON, `{}`),
			CreatedAt:      row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
	}
	type feedbackRow struct {
		ID, TargetLane, TargetMessageID, FeedbackEventID string
		FeedbackType, Explicitness, ContentJSON          string
		AttributionConfidence                            int
		OccurredAt, CreatedAt                            time.Time
	}
	var feedback []feedbackRow
	if err := s.db.WithContext(ctx).Raw(`
		SELECT id, target_lane, target_message_id, feedback_event_id,
		       feedback_type, explicitness, content_json::text AS content_json,
		       attribution_confidence, occurred_at, created_at
		FROM evaluation_feedback WHERE episode_id = ?
		ORDER BY occurred_at, id`, episodeID).Scan(&feedback).Error; err != nil {
		return err
	}
	detail.Feedback = make([]EvaluationFeedbackView, 0, len(feedback))
	for _, row := range feedback {
		detail.Feedback = append(detail.Feedback, EvaluationFeedbackView{
			ID: row.ID, TargetLane: row.TargetLane,
			TargetMessageID: row.TargetMessageID,
			FeedbackEventID: row.FeedbackEventID,
			FeedbackType:    row.FeedbackType, Explicitness: row.Explicitness,
			ContentJSON:           rawJSON(row.ContentJSON, `{}`),
			AttributionConfidence: row.AttributionConfidence,
			OccurredAt:            row.OccurredAt, CreatedAt: row.CreatedAt,
		})
	}
	type judgmentRow struct {
		ID, Source, EvaluatorID, Winner, ScoresJSON string
		ProblemTagsJSON, Rationale, SupersedesID    string
		Version, Confidence                         int64
		NeedsReview                                 bool
		CreatedAt                                   time.Time
	}
	var judgments []judgmentRow
	if err := s.db.WithContext(ctx).Raw(`
		SELECT id, source, evaluator_id, winner,
		       scores_json::text AS scores_json,
		       problem_tags_json::text AS problem_tags_json,
		       rationale, confidence, needs_review, supersedes_id, version,
		       created_at
		FROM evaluation_judgments WHERE episode_id = ?
		ORDER BY source, version`, episodeID).Scan(&judgments).Error; err != nil {
		return err
	}
	detail.Judgments = make([]EvaluationJudgmentView, 0, len(judgments))
	for _, row := range judgments {
		detail.Judgments = append(detail.Judgments, EvaluationJudgmentView{
			ID: row.ID, Source: row.Source, EvaluatorID: row.EvaluatorID,
			Winner: row.Winner, Version: row.Version,
			ScoresJSON:      rawJSON(row.ScoresJSON, `{}`),
			ProblemTagsJSON: rawJSON(row.ProblemTagsJSON, `[]`),
			Rationale:       row.Rationale, Confidence: row.Confidence,
			NeedsReview: row.NeedsReview, SupersedesID: row.SupersedesID,
			CreatedAt: row.CreatedAt,
		})
	}
	return nil
}

func rawJSON(value, fallback string) json.RawMessage {
	if json.Valid([]byte(value)) {
		return json.RawMessage(value)
	}
	return json.RawMessage(fallback)
}

func (s *evaluationWorkbenchStore) AppendHumanJudgment(
	ctx context.Context,
	appID, botOpenID, episodeID string,
	request HumanJudgmentRequest,
) (*EvaluationJudgmentView, error) {
	for _, value := range []string{appID, botOpenID, episodeID} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return nil, ErrInvalidEvaluationQuery
		}
	}
	if err := request.validate(); err != nil {
		return nil, err
	}
	if s == nil || s.db == nil {
		return nil, ErrEvaluationUnavailable
	}
	tagsJSON, _ := json.Marshal(request.ProblemTags)
	var created EvaluationJudgmentView
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owned struct{ ID string }
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Raw(`
			SELECT e.id
			FROM evaluation_episodes e
			JOIN evaluation_cohorts c ON c.id = e.cohort_id
			WHERE e.id = ? AND c.app_id = ? AND c.bot_open_id = ?
			FOR UPDATE`, episodeID, appID, botOpenID).Scan(&owned)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrEvaluationNotFound
		}
		var previous struct {
			ID      string
			Version int64
		}
		previousResult := tx.Raw(`
			SELECT id, version FROM evaluation_judgments
			WHERE episode_id = ? AND source = 'human'
			ORDER BY version DESC LIMIT 1
			FOR UPDATE`, episodeID).Scan(&previous)
		if previousResult.Error != nil {
			return previousResult.Error
		}
		version := previous.Version + 1
		created = EvaluationJudgmentView{
			ID:     "judgment_human_" + strings.ReplaceAll(uuid.NewV4().String(), "-", ""),
			Source: "human", EvaluatorID: request.EvaluatorID,
			Winner: request.Winner, Rationale: request.Rationale,
			SupersedesID: previous.ID, Version: version,
			Confidence:      int64(request.Confidence),
			ScoresJSON:      append(json.RawMessage(nil), request.ScoresJSON...),
			ProblemTagsJSON: append(json.RawMessage(nil), tagsJSON...),
			NeedsReview:     request.NeedsReview, CreatedAt: time.Now().UTC(),
		}
		return tx.Exec(`
			INSERT INTO evaluation_judgments (
				id, episode_id, version, source, evaluator_id, winner,
				scores_json, problem_tags_json, rationale, confidence,
				needs_review, supersedes_id, created_at
			) VALUES (?, ?, ?, 'human', ?, ?, ?::jsonb, ?::jsonb, ?, ?, ?, ?, ?)`,
			created.ID, episodeID, created.Version, created.EvaluatorID,
			created.Winner, string(created.ScoresJSON),
			string(created.ProblemTagsJSON), created.Rationale,
			created.Confidence, created.NeedsReview, created.SupersedesID,
			created.CreatedAt,
		).Error
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

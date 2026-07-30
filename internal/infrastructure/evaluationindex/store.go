package evaluationindex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
)

const (
	DefaultIndexAlias = "agent_conversation_evaluations"
	DefaultSearchSize = 100
	schemaVersion     = "conversation_evaluation.v1"
)

type MessageSnapshot struct {
	MessageID        string    `json:"message_id"`
	SenderOpenID     string    `json:"sender_open_id,omitempty"`
	ReplyToMessageID string    `json:"reply_to_message_id,omitempty"`
	Content          string    `json:"content"`
	OccurredAt       time.Time `json:"occurred_at"`
}

type LaneSnapshot struct {
	JoinDecision  string   `json:"join_decision"`
	TopicRelation string   `json:"topic_relation"`
	ReplyText     string   `json:"reply_text"`
	ContextText   []string `json:"context_text"`
	ExcludedText  []string `json:"excluded_text"`
	HasError      bool     `json:"has_error"`
}

type JudgmentSnapshot struct {
	Source      string    `json:"source"`
	Version     int64     `json:"version"`
	Winner      string    `json:"winner"`
	Rationale   string    `json:"rationale"`
	Confidence  int       `json:"confidence"`
	NeedsReview bool      `json:"needs_review"`
	ProblemTags []string  `json:"problem_tags"`
	CreatedAt   time.Time `json:"created_at"`
}

type EvaluationSnapshot struct {
	SchemaVersion     string             `json:"schema_version"`
	TenantID          string             `json:"tenant_id"`
	AppID             string             `json:"app_id"`
	BotOpenID         string             `json:"bot_open_id"`
	EpisodeID         string             `json:"episode_id"`
	CohortID          string             `json:"cohort_id"`
	ChatID            string             `json:"chat_id"`
	RunID             string             `json:"run_id,omitempty"`
	AnchorEventID     string             `json:"anchor_event_id"`
	AnchorMessageID   string             `json:"anchor_message_id"`
	TopicID           string             `json:"topic_id,omitempty"`
	Status            string             `json:"status"`
	ServingLane       string             `json:"serving_lane"`
	AnchorAt          time.Time          `json:"anchor_at"`
	PostWindowEnd     *time.Time         `json:"post_window_end,omitempty"`
	LateFeedbackUntil time.Time          `json:"late_feedback_until"`
	Disagreements     []string           `json:"disagreements"`
	FeedbackTypes     []string           `json:"feedback_types"`
	NeedsReview       bool               `json:"needs_review"`
	PreMessages       []MessageSnapshot  `json:"pre_messages"`
	AnchorMessage     MessageSnapshot    `json:"anchor_message"`
	PostMessages      []MessageSnapshot  `json:"post_messages"`
	Control           LaneSnapshot       `json:"control"`
	Candidate         LaneSnapshot       `json:"candidate"`
	LatestJudgments   []JudgmentSnapshot `json:"latest_judgments"`
	FullSnapshot      json.RawMessage    `json:"full_snapshot"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

func (s *EvaluationSnapshot) normalize() {
	if s.SchemaVersion == "" {
		s.SchemaVersion = schemaVersion
	}
	if s.Disagreements == nil {
		s.Disagreements = []string{}
	}
	if s.FeedbackTypes == nil {
		s.FeedbackTypes = []string{}
	}
	if s.PreMessages == nil {
		s.PreMessages = []MessageSnapshot{}
	}
	if s.PostMessages == nil {
		s.PostMessages = []MessageSnapshot{}
	}
	if s.LatestJudgments == nil {
		s.LatestJudgments = []JudgmentSnapshot{}
	}
	if s.Control.ContextText == nil {
		s.Control.ContextText = []string{}
	}
	if s.Control.ExcludedText == nil {
		s.Control.ExcludedText = []string{}
	}
	if s.Candidate.ContextText == nil {
		s.Candidate.ContextText = []string{}
	}
	if s.Candidate.ExcludedText == nil {
		s.Candidate.ExcludedText = []string{}
	}
}

func (s EvaluationSnapshot) Validate() error {
	for name, value := range map[string]string{
		"schema_version":    s.SchemaVersion,
		"tenant_id":         s.TenantID,
		"app_id":            s.AppID,
		"bot_open_id":       s.BotOpenID,
		"episode_id":        s.EpisodeID,
		"cohort_id":         s.CohortID,
		"chat_id":           s.ChatID,
		"anchor_event_id":   s.AnchorEventID,
		"anchor_message_id": s.AnchorMessageID,
		"status":            s.Status,
		"serving_lane":      s.ServingLane,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("%s must be non-empty without surrounding whitespace", name)
		}
	}
	if s.AnchorAt.IsZero() || s.LateFeedbackUntil.IsZero() ||
		s.UpdatedAt.IsZero() || s.LateFeedbackUntil.Before(s.AnchorAt) {
		return fmt.Errorf("evaluation snapshot timestamps are invalid")
	}
	if s.PostWindowEnd != nil &&
		(s.PostWindowEnd.IsZero() || s.PostWindowEnd.Before(s.AnchorAt)) {
		return fmt.Errorf("post_window_end is invalid")
	}
	if strings.TrimSpace(s.AnchorMessage.MessageID) == "" ||
		s.AnchorMessage.OccurredAt.IsZero() {
		return fmt.Errorf("anchor message is invalid")
	}
	if len(s.FullSnapshot) == 0 || !json.Valid(s.FullSnapshot) {
		return fmt.Errorf("full_snapshot must contain valid JSON")
	}
	return nil
}

type EpisodeFilter struct {
	CohortID     string
	ChatID       string
	From         time.Time
	To           time.Time
	Disagreement string
	FeedbackType string
	NeedsReview  *bool
}

func (f EpisodeFilter) Validate() error {
	if !f.From.IsZero() && !f.To.IsZero() && !f.To.After(f.From) {
		return fmt.Errorf("episode filter time range must be increasing")
	}
	for name, value := range map[string]string{
		"cohort_id": f.CohortID, "chat_id": f.ChatID,
		"disagreement": f.Disagreement, "feedback_type": f.FeedbackType,
	} {
		if value != "" && strings.TrimSpace(value) != value {
			return fmt.Errorf("%s filter has surrounding whitespace", name)
		}
	}
	return nil
}

type Backend interface {
	Upsert(context.Context, string, string, any) error
	Search(context.Context, string, map[string]any) ([]json.RawMessage, error)
}

func NewOpenSearchBackend() Backend {
	return openSearchBackend{}
}

type Store struct {
	tenant  tenant.Tenant
	index   string
	backend Backend
}

func NewStoreWithBackend(
	owner tenant.Tenant,
	index string,
	backend Backend,
) (*Store, error) {
	if err := owner.Validate(); err != nil {
		return nil, fmt.Errorf("evaluation index tenant: %w", err)
	}
	if backend == nil {
		return nil, fmt.Errorf("evaluation index backend is required")
	}
	index = strings.TrimSpace(index)
	if index == "" || !strings.HasSuffix(index, "-"+owner.ID) {
		return nil, fmt.Errorf("evaluation index alias is not tenant scoped")
	}
	return &Store{tenant: owner, index: index, backend: backend}, nil
}

func (s *Store) Upsert(ctx context.Context, snapshot EvaluationSnapshot) error {
	if s == nil || s.backend == nil {
		return fmt.Errorf("evaluation index store is not configured")
	}
	snapshot.normalize()
	if err := snapshot.Validate(); err != nil {
		return err
	}
	if snapshot.TenantID != s.tenant.ID ||
		snapshot.AppID != s.tenant.AppID ||
		snapshot.BotOpenID != s.tenant.BotOpenID {
		return fmt.Errorf("evaluation snapshot tenant does not match index tenant")
	}
	documentID, err := s.tenant.DocumentID(snapshot.EpisodeID)
	if err != nil {
		return err
	}
	if err := s.backend.Upsert(ctx, s.index, documentID, snapshot); err != nil {
		return fmt.Errorf("upsert evaluation snapshot: %w", err)
	}
	return nil
}

func (s *Store) Search(
	ctx context.Context,
	filter EpisodeFilter,
) ([]EvaluationSnapshot, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("evaluation index store is not configured")
	}
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	query := episodeSearchQuery(s.tenant.ID, filter)
	documents, err := s.backend.Search(ctx, s.index, query)
	if err != nil {
		return nil, fmt.Errorf("search evaluation snapshots: %w", err)
	}
	snapshots := make([]EvaluationSnapshot, 0, len(documents))
	for _, document := range documents {
		var snapshot EvaluationSnapshot
		if err := json.Unmarshal(document, &snapshot); err != nil {
			return nil, fmt.Errorf("decode evaluation snapshot: %w", err)
		}
		snapshot.normalize()
		if err := snapshot.Validate(); err != nil {
			return nil, fmt.Errorf("invalid indexed evaluation snapshot %q: %w", snapshot.EpisodeID, err)
		}
		if snapshot.TenantID != s.tenant.ID ||
			snapshot.AppID != s.tenant.AppID ||
			snapshot.BotOpenID != s.tenant.BotOpenID {
			return nil, fmt.Errorf(
				"indexed evaluation snapshot %q belongs to another tenant",
				snapshot.EpisodeID,
			)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func episodeSearchQuery(tenantID string, filter EpisodeFilter) map[string]any {
	filters := make([]any, 0, 7)
	addTerm := func(field string, value any, present bool) {
		if present {
			filters = append(filters, map[string]any{
				"term": map[string]any{field: value},
			})
		}
	}
	addTerm("tenant_id", tenantID, true)
	addTerm("cohort_id", filter.CohortID, filter.CohortID != "")
	addTerm("chat_id", filter.ChatID, filter.ChatID != "")
	addTerm("disagreements", filter.Disagreement, filter.Disagreement != "")
	addTerm("feedback_types", filter.FeedbackType, filter.FeedbackType != "")
	if filter.NeedsReview != nil {
		addTerm("needs_review", *filter.NeedsReview, true)
	}
	if !filter.From.IsZero() || !filter.To.IsZero() {
		bounds := map[string]any{}
		if !filter.From.IsZero() {
			bounds["gte"] = filter.From.Format(time.RFC3339Nano)
		}
		if !filter.To.IsZero() {
			bounds["lt"] = filter.To.Format(time.RFC3339Nano)
		}
		filters = append(filters, map[string]any{
			"range": map[string]any{"anchor_at": bounds},
		})
	}
	return map[string]any{
		"size": DefaultSearchSize,
		"sort": []any{
			map[string]any{"anchor_at": map[string]any{"order": "desc"}},
			map[string]any{"episode_id": map[string]any{"order": "asc"}},
		},
		"query": map[string]any{
			"bool": map[string]any{"filter": filters},
		},
	}
}

type openSearchBackend struct{}

func (openSearchBackend) Upsert(
	ctx context.Context,
	index string,
	id string,
	data any,
) error {
	return opensearch.UpsertData(ctx, index, id, data)
}

func (openSearchBackend) Search(
	ctx context.Context,
	index string,
	query map[string]any,
) ([]json.RawMessage, error) {
	response, err := opensearch.SearchData(ctx, index, query)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("opensearch returned an empty response")
	}
	documents := make([]json.RawMessage, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		documents = append(documents, append(json.RawMessage(nil), hit.Source...))
	}
	return documents, nil
}

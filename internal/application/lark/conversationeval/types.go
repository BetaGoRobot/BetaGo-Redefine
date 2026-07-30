package conversationeval

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const SchemaVersion = "conversation.v1"

const LateFeedbackGracePeriod = 24 * time.Hour

var (
	ErrInvalidContract   = errors.New("invalid conversation evaluation contract")
	ErrInvalidTransition = errors.New("invalid evaluation cohort transition")
)

type Lane string

const (
	LaneControl   Lane = "control"
	LaneCandidate Lane = "candidate"
)

func (l Lane) Valid() bool {
	return l == LaneControl || l == LaneCandidate
}

type CohortStatus string

const (
	CohortStatusCollecting          CohortStatus = "collecting"
	CohortStatusWaitingLateFeedback CohortStatus = "waiting_late_feedback"
	CohortStatusFinalized           CohortStatus = "finalized"
)

func (s CohortStatus) Valid() bool {
	switch s {
	case CohortStatusCollecting, CohortStatusWaitingLateFeedback, CohortStatusFinalized:
		return true
	default:
		return false
	}
}

type Cohort struct {
	ID                 string          `json:"id"`
	TenantID           string          `json:"tenant_id"`
	AppID              string          `json:"app_id"`
	BotOpenID          string          `json:"bot_open_id"`
	ChatIDs            []string        `json:"chat_ids"`
	StartAt            time.Time       `json:"start_at"`
	EndAt              time.Time       `json:"end_at"`
	Status             CohortStatus    `json:"status"`
	ServingLane        Lane            `json:"serving_lane"`
	ControlVersion     string          `json:"control_version"`
	CandidateVersion   string          `json:"candidate_version"`
	JudgeConfigJSON    json.RawMessage `json:"judge_config_json"`
	SamplingPolicyJSON json.RawMessage `json:"sampling_policy_json"`
	ResultVersion      int64           `json:"result_version"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

func (c *Cohort) TransitionTo(next CohortStatus, at time.Time) error {
	if c == nil || at.IsZero() {
		return contractError("cohort transition requires a cohort and timestamp")
	}
	valid := (c.Status == CohortStatusCollecting && next == CohortStatusWaitingLateFeedback) ||
		(c.Status == CohortStatusWaitingLateFeedback && next == CohortStatusFinalized)
	if !valid {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, c.Status, next)
	}
	c.Status = next
	c.UpdatedAt = at
	return nil
}

func (c Cohort) Validate() error {
	for name, value := range map[string]string{
		"id": c.ID, "app_id": c.AppID, "bot_open_id": c.BotOpenID,
		"control_version": c.ControlVersion, "candidate_version": c.CandidateVersion,
	} {
		if err := validateID(name, value); err != nil {
			return err
		}
	}
	if len(c.ChatIDs) == 0 {
		return contractError("chat_ids must not be empty")
	}
	seen := make(map[string]struct{}, len(c.ChatIDs))
	for _, chatID := range c.ChatIDs {
		if err := validateID("chat_id", chatID); err != nil {
			return err
		}
		if _, exists := seen[chatID]; exists {
			return contractError("chat_id %q is duplicated", chatID)
		}
		seen[chatID] = struct{}{}
	}
	if c.StartAt.IsZero() || c.EndAt.IsZero() || !c.EndAt.After(c.StartAt) {
		return contractError("cohort time range must be non-zero and increasing")
	}
	if !c.Status.Valid() {
		return contractError("invalid cohort status %q", c.Status)
	}
	if !c.ServingLane.Valid() {
		return contractError("invalid serving lane %q", c.ServingLane)
	}
	if err := validateJSONObject("judge_config_json", c.JudgeConfigJSON); err != nil {
		return err
	}
	if err := validateJSONObject("sampling_policy_json", c.SamplingPolicyJSON); err != nil {
		return err
	}
	if c.ResultVersion < 0 {
		return contractError("result_version must not be negative")
	}
	return nil
}

type EpisodeStatus string

const (
	EpisodeStatusCollecting    EpisodeStatus = "collecting"
	EpisodeStatusReadyForJudge EpisodeStatus = "ready_for_judge"
	EpisodeStatusJudged        EpisodeStatus = "judged"
)

func (s EpisodeStatus) Valid() bool {
	switch s {
	case EpisodeStatusCollecting, EpisodeStatusReadyForJudge, EpisodeStatusJudged:
		return true
	default:
		return false
	}
}

type Episode struct {
	ID                string        `json:"id"`
	TenantID          string        `json:"tenant_id"`
	CohortID          string        `json:"cohort_id"`
	ChatID            string        `json:"chat_id"`
	RunID             string        `json:"run_id,omitempty"`
	AnchorEventID     string        `json:"anchor_event_id"`
	AnchorMessageID   string        `json:"anchor_message_id"`
	TopicID           string        `json:"topic_id,omitempty"`
	ServingLane       Lane          `json:"serving_lane"`
	Status            EpisodeStatus `json:"status"`
	PreWindowStart    time.Time     `json:"pre_window_start"`
	AnchorAt          time.Time     `json:"anchor_at"`
	PostWindowEnd     *time.Time    `json:"post_window_end,omitempty"`
	LateFeedbackUntil time.Time     `json:"late_feedback_until"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

func (e Episode) Validate() error {
	for name, value := range map[string]string{
		"id": e.ID, "cohort_id": e.CohortID, "chat_id": e.ChatID,
		"anchor_event_id": e.AnchorEventID, "anchor_message_id": e.AnchorMessageID,
	} {
		if err := validateID(name, value); err != nil {
			return err
		}
	}
	for name, value := range map[string]string{"run_id": e.RunID, "topic_id": e.TopicID} {
		if value != "" {
			if err := validateID(name, value); err != nil {
				return err
			}
		}
	}
	if !e.ServingLane.Valid() {
		return contractError("invalid episode serving lane %q", e.ServingLane)
	}
	if !e.Status.Valid() {
		return contractError("invalid episode status %q", e.Status)
	}
	if e.PreWindowStart.IsZero() || e.AnchorAt.IsZero() || e.LateFeedbackUntil.IsZero() {
		return contractError("episode window timestamps must not be zero")
	}
	if e.PreWindowStart.After(e.AnchorAt) {
		return contractError("pre_window_start must precede anchor_at")
	}
	if e.LateFeedbackUntil.Before(e.AnchorAt) {
		return contractError("late_feedback_until must not precede anchor_at")
	}
	if e.PostWindowEnd != nil {
		if e.PostWindowEnd.IsZero() || e.PostWindowEnd.Before(e.AnchorAt) ||
			e.PostWindowEnd.After(e.LateFeedbackUntil) {
			return contractError("post_window_end must fall between anchor_at and late_feedback_until")
		}
	} else if e.Status != EpisodeStatusCollecting {
		return contractError("episode status %q requires post_window_end", e.Status)
	}
	return nil
}

type OutputMode string

const (
	OutputModeActual OutputMode = "actual"
	OutputModeShadow OutputMode = "shadow"
)

func (m OutputMode) Valid() bool {
	return m == OutputModeActual || m == OutputModeShadow
}

type JoinDecision string

const (
	JoinDecisionJoin JoinDecision = "join"
	JoinDecisionSkip JoinDecision = "skip"
)

func (d JoinDecision) Valid() bool {
	return d == JoinDecisionJoin || d == JoinDecisionSkip
}

type TopicRelation string

const (
	TopicRelationRelated   TopicRelation = "related"
	TopicRelationNewTopic  TopicRelation = "new_topic"
	TopicRelationUnrelated TopicRelation = "unrelated"
)

func (r TopicRelation) Valid() bool {
	switch r {
	case TopicRelationRelated, TopicRelationNewTopic, TopicRelationUnrelated:
		return true
	default:
		return false
	}
}

type ContextItem struct {
	ID            string          `json:"id"`
	Source        string          `json:"source"`
	SourceID      string          `json:"source_id"`
	Kind          string          `json:"kind"`
	Content       string          `json:"content"`
	ContentHash   string          `json:"content_hash"`
	Score         float64         `json:"score,omitempty"`
	Rank          int             `json:"rank"`
	TokenCount    int             `json:"token_count"`
	Selected      bool            `json:"selected"`
	ExcludeReason string          `json:"exclude_reason,omitempty"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
}

type ContextBucket string

const (
	ContextBucketMessages  ContextBucket = "messages"
	ContextBucketRetrieved ContextBucket = "retrieved"
	ContextBucketEvents    ContextBucket = "events"
)

func (b ContextBucket) Valid() bool {
	switch b {
	case ContextBucketMessages, ContextBucketRetrieved, ContextBucketEvents:
		return true
	default:
		return false
	}
}

type ExcludedContextItem struct {
	ContextItem
	OriginalBucket ContextBucket `json:"original_bucket,omitempty"`
}

type ContextSnapshot struct {
	SchemaVersion   string        `json:"schema_version"`
	AnchorEventID   string        `json:"anchor_event_id"`
	AnchorAt        time.Time     `json:"anchor_at"`
	Messages        []ContextItem `json:"messages"`
	Retrieved       []ContextItem `json:"retrieved"`
	Events          []ContextItem `json:"events"`
	SystemPrompt    string        `json:"system_prompt"`
	UserPrompt      string        `json:"user_prompt"`
	CurrentInput    string        `json:"current_input,omitempty"`
	TokenEstimate   int           `json:"token_estimate"`
	TokenBudget     int           `json:"token_budget"`
	Truncated       bool          `json:"truncated"`
	DegradedSources []string      `json:"degraded_sources,omitempty"`
}

func (s ContextSnapshot) Validate() error {
	if s.SchemaVersion != SchemaVersion {
		return contractError("schema_version must be %q", SchemaVersion)
	}
	if err := validateID("anchor_event_id", s.AnchorEventID); err != nil {
		return err
	}
	if s.AnchorAt.IsZero() {
		return contractError("anchor_at must not be zero")
	}
	if s.TokenEstimate < 0 || s.TokenBudget < 0 || s.TokenEstimate > s.TokenBudget {
		return contractError("token estimates and budget are out of range")
	}
	seen := make(map[string]struct{})
	selectedTokens := 0
	collections := [][]ContextItem{s.Messages, s.Retrieved, s.Events}
	for _, items := range collections {
		for _, item := range items {
			if err := item.validate(s.AnchorAt); err != nil {
				return err
			}
			key := item.Source + "\x00" + item.SourceID
			if _, exists := seen[key]; exists {
				return contractError("context identity %q/%q is duplicated", item.Source, item.SourceID)
			}
			seen[key] = struct{}{}
			if item.Selected {
				selectedTokens += item.TokenCount
			}
		}
	}
	if selectedTokens > s.TokenBudget {
		return contractError("selected context tokens %d exceed budget %d", selectedTokens, s.TokenBudget)
	}
	degraded := make(map[string]struct{}, len(s.DegradedSources))
	for _, source := range s.DegradedSources {
		if err := validateID("degraded_source", source); err != nil {
			return err
		}
		if _, exists := degraded[source]; exists {
			return contractError("degraded source %q is duplicated", source)
		}
		degraded[source] = struct{}{}
	}
	return nil
}

func (i ContextItem) validate(anchorAt time.Time) error {
	for name, value := range map[string]string{
		"context id": i.ID, "context source": i.Source, "context source_id": i.SourceID,
		"context kind": i.Kind, "context content_hash": i.ContentHash,
	} {
		if err := validateID(name, value); err != nil {
			return err
		}
	}
	if i.Rank < 0 || i.TokenCount < 0 || math.IsNaN(i.Score) || math.IsInf(i.Score, 0) {
		return contractError("context item %q contains an invalid range", i.ID)
	}
	if !i.Selected && strings.TrimSpace(i.ExcludeReason) == "" {
		return contractError("excluded context item %q requires exclude_reason", i.ID)
	}
	if i.Selected && strings.TrimSpace(i.ExcludeReason) != "" {
		return contractError("selected context item %q must not carry exclude_reason", i.ID)
	}
	if i.OccurredAt.IsZero() || i.OccurredAt.After(anchorAt) {
		return contractError("context item %q occurred after the anchor or has no timestamp", i.ID)
	}
	if len(i.Metadata) > 0 {
		if err := validateJSONObject("context metadata", i.Metadata); err != nil {
			return err
		}
	}
	return nil
}

type LaneOutput struct {
	ID              string                `json:"id"`
	TenantID        string                `json:"tenant_id"`
	EpisodeID       string                `json:"episode_id"`
	Lane            Lane                  `json:"lane"`
	OutputMode      OutputMode            `json:"output_mode"`
	ActivationJSON  json.RawMessage       `json:"activation_json"`
	RelevanceJSON   json.RawMessage       `json:"relevance_json"`
	JoinDecision    JoinDecision          `json:"join_decision"`
	TopicRelation   TopicRelation         `json:"topic_relation"`
	ContextSnapshot ContextSnapshot       `json:"context_snapshot"`
	ExcludedContext []ExcludedContextItem `json:"excluded_context"`
	ToolPlanJSON    json.RawMessage       `json:"tool_plan_json"`
	ReplyText       string                `json:"reply_text"`
	Latency         time.Duration         `json:"latency"`
	TokenUsageJSON  json.RawMessage       `json:"token_usage_json"`
	ErrorJSON       json.RawMessage       `json:"error_json"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

func (o LaneOutput) Validate() error {
	if err := validateID("lane_output id", o.ID); err != nil {
		return err
	}
	if err := validateID("episode_id", o.EpisodeID); err != nil {
		return err
	}
	if !o.Lane.Valid() || !o.OutputMode.Valid() || !o.JoinDecision.Valid() || !o.TopicRelation.Valid() {
		return contractError("lane output contains an invalid enum")
	}
	if o.Latency < 0 {
		return contractError("lane output latency must not be negative")
	}
	for name, value := range map[string]json.RawMessage{
		"activation_json": o.ActivationJSON, "relevance_json": o.RelevanceJSON,
		"tool_plan_json": o.ToolPlanJSON, "token_usage_json": o.TokenUsageJSON,
		"error_json": o.ErrorJSON,
	} {
		if err := validateJSONObject(name, value); err != nil {
			return err
		}
	}
	if err := o.ContextSnapshot.Validate(); err != nil {
		return fmt.Errorf("context_snapshot: %w", err)
	}
	seen := make(map[string]struct{})
	for _, items := range [][]ContextItem{
		o.ContextSnapshot.Messages,
		o.ContextSnapshot.Retrieved,
		o.ContextSnapshot.Events,
	} {
		for _, item := range items {
			seen[item.Source+"\x00"+item.SourceID] = struct{}{}
		}
	}
	for _, excluded := range o.ExcludedContext {
		if excluded.Selected {
			return contractError("excluded context item %q must not be selected", excluded.ID)
		}
		if excluded.OriginalBucket != "" && !excluded.OriginalBucket.Valid() {
			return contractError(
				"excluded context item %q has invalid original_bucket %q",
				excluded.ID,
				excluded.OriginalBucket,
			)
		}
		if err := excluded.ContextItem.validate(o.ContextSnapshot.AnchorAt); err != nil {
			return err
		}
		key := excluded.Source + "\x00" + excluded.SourceID
		if _, exists := seen[key]; exists {
			return contractError(
				"context identity %q/%q is duplicated across selected and excluded context",
				excluded.Source, excluded.SourceID,
			)
		}
		seen[key] = struct{}{}
	}
	return nil
}

type FeedbackType string

const (
	FeedbackTypeDirectReply       FeedbackType = "direct_reply"
	FeedbackTypeReaction          FeedbackType = "reaction"
	FeedbackTypeCorrection        FeedbackType = "correction"
	FeedbackTypeCardAction        FeedbackType = "card_action"
	FeedbackTypeSemanticInference FeedbackType = "semantic_inference"
)

func (t FeedbackType) Valid() bool {
	switch t {
	case FeedbackTypeDirectReply, FeedbackTypeReaction, FeedbackTypeCorrection,
		FeedbackTypeCardAction, FeedbackTypeSemanticInference:
		return true
	default:
		return false
	}
}

type FeedbackExplicitness string

const (
	FeedbackExplicit FeedbackExplicitness = "explicit"
	FeedbackInferred FeedbackExplicitness = "inferred"
)

func (e FeedbackExplicitness) Valid() bool {
	return e == FeedbackExplicit || e == FeedbackInferred
}

type Feedback struct {
	ID                    string               `json:"id"`
	TenantID              string               `json:"tenant_id"`
	EpisodeID             string               `json:"episode_id"`
	TargetLane            Lane                 `json:"target_lane,omitempty"`
	TargetMessageID       string               `json:"target_message_id,omitempty"`
	FeedbackEventID       string               `json:"feedback_event_id"`
	FeedbackType          FeedbackType         `json:"feedback_type"`
	Explicitness          FeedbackExplicitness `json:"explicitness"`
	ContentJSON           json.RawMessage      `json:"content_json"`
	AttributionConfidence int                  `json:"attribution_confidence"`
	OccurredAt            time.Time            `json:"occurred_at"`
	CreatedAt             time.Time            `json:"created_at"`
}

func (f Feedback) Validate() error {
	for name, value := range map[string]string{
		"feedback id": f.ID, "episode_id": f.EpisodeID, "feedback_event_id": f.FeedbackEventID,
	} {
		if err := validateID(name, value); err != nil {
			return err
		}
	}
	if f.TargetLane != "" && !f.TargetLane.Valid() {
		return contractError("invalid feedback target lane %q", f.TargetLane)
	}
	if (f.TargetLane == "") != (f.TargetMessageID == "") {
		return contractError("target_lane and target_message_id must both be present or both be empty")
	}
	if f.TargetMessageID != "" {
		if err := validateID("target_message_id", f.TargetMessageID); err != nil {
			return err
		}
	}
	if !f.FeedbackType.Valid() || !f.Explicitness.Valid() {
		return contractError("feedback contains an invalid enum")
	}
	if err := validateJSONObject("content_json", f.ContentJSON); err != nil {
		return err
	}
	if f.AttributionConfidence < 0 || f.AttributionConfidence > 100 {
		return contractError("attribution_confidence must be between 0 and 100")
	}
	if f.OccurredAt.IsZero() {
		return contractError("feedback occurred_at must not be zero")
	}
	return nil
}

type JudgmentSource string

const (
	JudgmentSourceConversationJudge JudgmentSource = "conversation_evaluation_judge"
	JudgmentSourceHuman             JudgmentSource = "human"
)

func (s JudgmentSource) Valid() bool {
	return s == JudgmentSourceConversationJudge || s == JudgmentSourceHuman
}

type JudgmentWinner string

const (
	JudgmentWinnerControl   JudgmentWinner = "control"
	JudgmentWinnerCandidate JudgmentWinner = "candidate"
	JudgmentWinnerTie       JudgmentWinner = "tie"
)

func (w JudgmentWinner) Valid() bool {
	return w == JudgmentWinnerControl || w == JudgmentWinnerCandidate || w == JudgmentWinnerTie
}

type Judgment struct {
	ID           string          `json:"id"`
	TenantID     string          `json:"tenant_id"`
	EpisodeID    string          `json:"episode_id"`
	Version      int64           `json:"version"`
	Source       JudgmentSource  `json:"source"`
	EvaluatorID  string          `json:"evaluator_id"`
	Winner       JudgmentWinner  `json:"winner"`
	ScoresJSON   json.RawMessage `json:"scores_json"`
	ProblemTags  []string        `json:"problem_tags"`
	Rationale    string          `json:"rationale"`
	Confidence   int             `json:"confidence"`
	NeedsReview  bool            `json:"needs_review"`
	SupersedesID string          `json:"supersedes_id,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

func (j Judgment) Validate() error {
	for name, value := range map[string]string{
		"judgment id": j.ID, "episode_id": j.EpisodeID, "evaluator_id": j.EvaluatorID,
	} {
		if err := validateID(name, value); err != nil {
			return err
		}
	}
	if j.Version <= 0 {
		return contractError("judgment version must be positive")
	}
	if !j.Source.Valid() || !j.Winner.Valid() {
		return contractError("judgment contains an invalid enum")
	}
	if err := validateJSONObject("scores_json", j.ScoresJSON); err != nil {
		return err
	}
	if j.Confidence < 0 || j.Confidence > 100 {
		return contractError("judgment confidence must be between 0 and 100")
	}
	seen := make(map[string]struct{}, len(j.ProblemTags))
	for _, tag := range j.ProblemTags {
		if err := validateID("problem_tag", tag); err != nil {
			return err
		}
		if _, exists := seen[tag]; exists {
			return contractError("problem tag %q is duplicated", tag)
		}
		seen[tag] = struct{}{}
	}
	if j.Version == 1 && j.SupersedesID != "" {
		return contractError("first judgment must not supersede another judgment")
	}
	if j.Version > 1 {
		if err := validateID("supersedes_id", j.SupersedesID); err != nil {
			return err
		}
	}
	return nil
}

func validateID(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return contractError("%s must be non-empty without surrounding whitespace", name)
	}
	return nil
}

func validateJSONObject(name string, value json.RawMessage) error {
	if len(value) == 0 || !json.Valid(value) {
		return contractError("%s must contain valid JSON", name)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		return contractError("%s must contain a JSON object", name)
	}
	return nil
}

func contractError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidContract, fmt.Sprintf(format, args...))
}

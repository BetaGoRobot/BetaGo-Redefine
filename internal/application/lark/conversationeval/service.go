package conversationeval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrEvaluationUnavailable = errors.New("conversation evaluation unavailable")

type MessageInput struct {
	AppID            string
	BotOpenID        string
	ChatID           string
	RunID            string
	EventID          string
	MessageID        string
	TopicID          string
	SenderOpenID     string
	ReplyToMessageID string
	Content          string
	OccurredAt       time.Time
}

func (m MessageInput) Validate() error {
	for name, value := range map[string]string{
		"message app_id": m.AppID, "message bot_open_id": m.BotOpenID,
		"message chat_id": m.ChatID, "message event_id": m.EventID,
		"message message_id": m.MessageID,
	} {
		if err := validateID(name, value); err != nil {
			return err
		}
	}
	if m.OccurredAt.IsZero() {
		return contractError("message occurred_at must not be zero")
	}
	return nil
}

func (m MessageInput) WindowMessage(position WindowPosition) WindowMessage {
	return WindowMessage{
		EventID: m.EventID, MessageID: m.MessageID, ChatID: m.ChatID,
		TopicID: m.TopicID, SenderOpenID: m.SenderOpenID,
		ReplyToMessageID: m.ReplyToMessageID, Content: m.Content,
		OccurredAt: m.OccurredAt, Position: position,
	}
}

type EpisodeArtifacts interface {
	SaveWindowMessages(context.Context, string, []WindowMessage) error
	OpenEpisodesForMessage(context.Context, string, time.Time) ([]Episode, error)
	ApplyPostWindowObservation(
		context.Context,
		string,
		WindowMessage,
		bool,
	) (PostWindowMutation, error)
	CloseExpiredPostWindows(context.Context, string, time.Time) (int, error)
	CloseExpiredPostWindowsAll(context.Context, time.Time, int) (int, error)
	MarkReadyIfComplete(context.Context, string, time.Time) (bool, error)
}

type PostWindowMutation struct {
	Added       bool
	Closed      bool
	ClosedAt    *time.Time
	CloseReason PostWindowCloseReason
	Ready       bool
}

type EvaluationRepository interface {
	Store
	EpisodeArtifacts
	FeedbackAttributionStore
}

type PreWindowSource interface {
	MessagesBefore(context.Context, string, time.Time, int) ([]WindowMessage, error)
}

type TopicBoundaryDetector interface {
	IsTopicBoundary(context.Context, Episode, WindowMessage) (bool, error)
}

type CandidateTask struct {
	ID              string
	Cohort          Cohort
	Episode         Episode
	Message         MessageInput
	OutputID        string
	ContextSnapshot ContextSnapshot
	ExcludedContext []ExcludedContextItem
	ControlCapture  CaptureSnapshot
	CreatedAt       time.Time
}

type CandidateSubmitter interface {
	SubmitCandidate(context.Context, CandidateTask) error
}

type RollingCohortStore interface {
	EnsureRollingCohort(
		context.Context,
		MessageInput,
		time.Duration,
	) (Cohort, error)
}

type ServiceOptions struct {
	Repository          EvaluationRepository
	PreWindowSource     PreWindowSource
	BoundaryDetector    TopicBoundaryDetector
	CandidateSubmitter  CandidateSubmitter
	EnsureCohortForChat func(string) bool
	CohortDuration      time.Duration
	Now                 func() time.Time
}

type Service struct {
	repository         EvaluationRepository
	preWindowSource    PreWindowSource
	boundaryDetector   TopicBoundaryDetector
	candidateSubmitter CandidateSubmitter
	rollingCohorts     RollingCohortStore
	ensureCohort       func(string) bool
	cohortDuration     time.Duration
	feedback           *FeedbackAttributor
	now                func() time.Time
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Repository == nil {
		return nil, fmt.Errorf("%w: evaluation repository is nil", ErrEvaluationUnavailable)
	}
	if options.PreWindowSource == nil {
		return nil, fmt.Errorf("%w: pre-window source is nil", ErrEvaluationUnavailable)
	}
	if options.CandidateSubmitter == nil {
		return nil, fmt.Errorf("%w: candidate submitter is nil", ErrEvaluationUnavailable)
	}
	var rollingCohorts RollingCohortStore
	if options.EnsureCohortForChat != nil {
		var ok bool
		rollingCohorts, ok = options.Repository.(RollingCohortStore)
		if !ok {
			return nil, fmt.Errorf(
				"%w: repository cannot create rolling cohorts",
				ErrEvaluationUnavailable,
			)
		}
		if options.CohortDuration <= 0 || options.CohortDuration > 7*24*time.Hour {
			return nil, fmt.Errorf(
				"%w: rolling cohort duration must be within (0, 168h]",
				ErrEvaluationUnavailable,
			)
		}
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	feedback, err := NewFeedbackAttributor(options.Repository)
	if err != nil {
		return nil, err
	}
	return &Service{
		repository: options.Repository, preWindowSource: options.PreWindowSource,
		boundaryDetector:   options.BoundaryDetector,
		candidateSubmitter: options.CandidateSubmitter,
		rollingCohorts:     rollingCohorts,
		ensureCohort:       options.EnsureCohortForChat,
		cohortDuration:     options.CohortDuration,
		feedback:           feedback,
		now:                options.Now,
	}, nil
}

type MessageSession struct {
	input    MessageInput
	cohorts  []Cohort
	pre      []WindowMessage
	recorder *CaptureRecorder
}

func (s *MessageSession) Capture() Capture {
	if s == nil || s.recorder == nil {
		return noopCapture{}
	}
	return s.recorder
}

func (s *MessageSession) Enabled() bool {
	return s != nil && s.recorder != nil && len(s.cohorts) > 0
}

// BeginMessage first advances all already-open post windows, then performs the
// cheap active-cohort lookup. Control capture is allocated only when at least
// one cohort matches.
func (s *Service) BeginMessage(
	ctx context.Context,
	input MessageInput,
) (*MessageSession, error) {
	if s == nil || s.repository == nil {
		return nil, ErrEvaluationUnavailable
	}
	if err := input.Validate(); err != nil {
		return nil, err
	}
	if err := s.ObserveWindowMessage(ctx, input); err != nil {
		return nil, err
	}
	if s.ensureCohort != nil && s.ensureCohort(input.ChatID) {
		if _, err := s.rollingCohorts.EnsureRollingCohort(
			ctx,
			input,
			s.cohortDuration,
		); err != nil {
			return nil, fmt.Errorf("ensure rolling evaluation cohort: %w", err)
		}
	}
	cohorts, err := s.repository.ActiveCohorts(ctx, input.ChatID, input.OccurredAt)
	if err != nil {
		return nil, fmt.Errorf("load active evaluation cohorts: %w", err)
	}
	matched := make([]Cohort, 0, len(cohorts))
	for _, cohort := range cohorts {
		if cohort.AppID == input.AppID && cohort.BotOpenID == input.BotOpenID {
			matched = append(matched, cohort)
		}
	}
	if len(matched) == 0 {
		return &MessageSession{input: input}, nil
	}
	rawPre, err := s.preWindowSource.MessagesBefore(
		ctx,
		input.ChatID,
		input.OccurredAt,
		PreWindowMessageLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("load evaluation pre-window: %w", err)
	}
	pre, err := SelectPreWindow(rawPre, input.OccurredAt)
	if err != nil {
		return nil, err
	}
	return &MessageSession{
		input: input, cohorts: matched, pre: pre, recorder: NewCaptureRecorder(),
	}, nil
}

func (s *Service) ObserveWindowMessage(ctx context.Context, input MessageInput) error {
	episodes, err := s.repository.OpenEpisodesForMessage(ctx, input.ChatID, input.OccurredAt)
	if err != nil {
		return fmt.Errorf("load open evaluation windows: %w", err)
	}
	message := input.WindowMessage(WindowPositionPost)
	for _, episode := range episodes {
		boundary := defaultTopicBoundary(episode, message)
		if s.boundaryDetector != nil {
			boundary, err = s.boundaryDetector.IsTopicBoundary(
				ctx,
				episode,
				message,
			)
			if err != nil {
				return fmt.Errorf("detect evaluation topic boundary: %w", err)
			}
		}
		if _, applyErr := s.repository.ApplyPostWindowObservation(
			ctx,
			episode.ID,
			message,
			boundary,
		); applyErr != nil {
			if errors.Is(applyErr, ErrInvalidTransition) {
				continue
			}
			return fmt.Errorf("apply evaluation post-window observation: %w", applyErr)
		}
	}
	return nil
}

func (s *Service) ObserveMessage(ctx context.Context, event MessageFeedback) error {
	if s == nil || s.feedback == nil {
		return ErrEvaluationUnavailable
	}
	return s.feedback.ObserveMessage(ctx, event)
}

func (s *Service) ObserveReaction(ctx context.Context, event ReactionFeedback) error {
	if s == nil || s.feedback == nil {
		return ErrEvaluationUnavailable
	}
	return s.feedback.ObserveReaction(ctx, event)
}

func (s *Service) ObserveCardAction(ctx context.Context, event CardFeedback) error {
	if s == nil || s.feedback == nil {
		return ErrEvaluationUnavailable
	}
	return s.feedback.ObserveCardAction(ctx, event)
}

func (s *Service) AdvanceOpenWindows(
	ctx context.Context,
	chatID string,
	now time.Time,
) (int, error) {
	return s.repository.CloseExpiredPostWindows(ctx, chatID, now)
}

func (s *Service) AdvanceAllOpenWindows(
	ctx context.Context,
	now time.Time,
	limit int,
) (int, error) {
	return s.repository.CloseExpiredPostWindowsAll(ctx, now, limit)
}

func (s *Service) CompleteMessage(ctx context.Context, session *MessageSession) error {
	if session == nil || !session.Enabled() {
		return nil
	}
	completedAt := s.now()
	capture := session.recorder.Snapshot()
	for _, cohort := range session.cohorts {
		episode := newEpisode(cohort, session.input, session.pre, completedAt)
		stored, err := s.repository.GetOrCreateEpisode(ctx, episode)
		if err != nil {
			return fmt.Errorf("get or create evaluation episode: %w", err)
		}
		windowMessages := append(
			cloneCaptureValue(session.pre),
			session.input.WindowMessage(WindowPositionAnchor),
		)
		windowMessages[len(windowMessages)-1].Sequence = 0
		if err := s.repository.SaveWindowMessages(ctx, stored.ID, windowMessages); err != nil {
			return fmt.Errorf("save evaluation pre-window: %w", err)
		}
		control, err := BuildControlLaneOutput(*stored, session.input, capture, completedAt)
		if err != nil {
			return err
		}
		if err := s.repository.UpsertLaneOutput(ctx, control); err != nil {
			return fmt.Errorf("store evaluation control output: %w", err)
		}
		task := CandidateTask{
			ID:     evaluationID("candidate_task", cohort.ID, session.input.EventID),
			Cohort: cohort, Episode: *stored, Message: session.input,
			OutputID:        evaluationID("lane_candidate", stored.ID),
			ContextSnapshot: cloneCaptureValue(control.ContextSnapshot),
			ExcludedContext: cloneCaptureValue(control.ExcludedContext),
			ControlCapture:  cloneCaptureValue(capture),
			CreatedAt:       completedAt,
		}
		if err := s.candidateSubmitter.SubmitCandidate(ctx, task); err != nil {
			return fmt.Errorf("submit evaluation candidate: %w", err)
		}
	}
	return nil
}

func (s *Service) CompleteCandidate(
	ctx context.Context,
	episodeID string,
	output LaneOutput,
) error {
	if output.EpisodeID != episodeID || output.Lane != LaneCandidate {
		return contractError("candidate output does not match episode")
	}
	if err := s.repository.UpsertLaneOutput(ctx, output); err != nil {
		return err
	}
	_, err := s.repository.MarkReadyIfComplete(ctx, episodeID, s.now())
	return err
}

func BuildControlLaneOutput(
	episode Episode,
	input MessageInput,
	capture CaptureSnapshot,
	completedAt time.Time,
) (LaneOutput, error) {
	if completedAt.IsZero() {
		return LaneOutput{}, contractError("control completion timestamp must not be zero")
	}
	snapshot := fallbackControlContext(input)
	excluded := []ExcludedContextItem{}
	if capture.Context != nil {
		snapshot = cloneCaptureValue(*capture.Context)
		excluded = cloneCaptureValue(capture.ExcludedContext)
	}
	if err := snapshot.Validate(); err != nil {
		return LaneOutput{}, fmt.Errorf("control context snapshot: %w", err)
	}
	activation := objectOrEmpty(capture.IntentJSON)
	decision := JoinDecisionSkip
	relation := TopicRelationUnrelated
	reply := ""
	latency := time.Duration(0)
	tokenUsage := json.RawMessage(`{}`)
	if capture.Output != nil {
		reply = capture.Output.Reply
		latency = capture.Output.Latency
		if capture.Output.Decision == OutputDecisionReply {
			decision = JoinDecisionJoin
			relation = TopicRelationRelated
		}
		if capture.Output.TokenUsage != nil {
			tokenUsage = mustObjectJSON(capture.Output.TokenUsage)
		}
	}
	relevance := mustObjectJSON(map[string]any{
		"join_decision": decision, "topic_relation": relation, "source": "control",
	})
	toolPlan := mustObjectJSON(map[string]any{
		"plans":               capture.ToolPlans,
		"delivery_message_id": capture.DeliveryMessageID,
		"capability_calls": func() []ToolTrace {
			if capture.Output == nil {
				return []ToolTrace{}
			}
			return capture.Output.CapabilityCalls
		}(),
	})
	mode := OutputModeShadow
	if episode.ServingLane == LaneControl {
		mode = OutputModeActual
	}
	output := LaneOutput{
		ID: evaluationID("lane_control", episode.ID), EpisodeID: episode.ID,
		Lane: LaneControl, OutputMode: mode,
		ActivationJSON: activation, RelevanceJSON: relevance,
		JoinDecision: decision, TopicRelation: relation,
		ContextSnapshot: snapshot, ExcludedContext: excluded,
		ToolPlanJSON: toolPlan, ReplyText: reply, Latency: latency,
		TokenUsageJSON: tokenUsage, ErrorJSON: json.RawMessage(`{}`),
		CreatedAt: completedAt, UpdatedAt: completedAt,
	}
	if err := output.Validate(); err != nil {
		return LaneOutput{}, err
	}
	return output, nil
}

func newEpisode(
	cohort Cohort,
	input MessageInput,
	pre []WindowMessage,
	createdAt time.Time,
) Episode {
	preStart := input.OccurredAt
	if len(pre) > 0 {
		preStart = pre[0].OccurredAt
	}
	return Episode{
		ID:       evaluationID("episode", cohort.ID, input.EventID),
		CohortID: cohort.ID, ChatID: input.ChatID, RunID: input.RunID,
		AnchorEventID: input.EventID, AnchorMessageID: input.MessageID,
		TopicID: input.TopicID, ServingLane: cohort.ServingLane,
		Status: EpisodeStatusCollecting, PreWindowStart: preStart,
		AnchorAt:          input.OccurredAt,
		LateFeedbackUntil: input.OccurredAt.Add(LateFeedbackGracePeriod),
		CreatedAt:         createdAt, UpdatedAt: createdAt,
	}
}

func fallbackControlContext(input MessageInput) ContextSnapshot {
	return ContextSnapshot{
		SchemaVersion: SchemaVersion, AnchorEventID: input.EventID,
		AnchorAt: input.OccurredAt, Messages: []ContextItem{},
		Retrieved: []ContextItem{}, Events: []ContextItem{},
		CurrentInput: input.Content, TokenBudget: 0, TokenEstimate: 0,
	}
}

func defaultTopicBoundary(episode Episode, message WindowMessage) bool {
	return episode.TopicID != "" && message.TopicID != "" && episode.TopicID != message.TopicID
}

func objectOrEmpty(raw json.RawMessage) json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil && object != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return json.RawMessage(`{}`)
}

func mustObjectJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(encoded)
}

func evaluationID(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:16])
}

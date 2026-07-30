package conversationeval

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestServiceNoActiveCohortAvoidsCaptureAndPreWindowLoad(t *testing.T) {
	repository := &serviceRepositoryFake{}
	pre := &preWindowSourceFake{}
	service := newServiceForTest(t, repository, pre, &candidateSubmitterFake{})

	session, err := service.BeginMessage(context.Background(), serviceMessageInput())
	if err != nil {
		t.Fatalf("BeginMessage() error = %v", err)
	}
	if session.Enabled() {
		t.Fatal("session.Enabled() = true, want false")
	}
	if pre.calls != 0 {
		t.Fatalf("pre-window calls = %d, want 0", pre.calls)
	}
	if err := service.CompleteMessage(context.Background(), session); err != nil {
		t.Fatalf("CompleteMessage(disabled) error = %v", err)
	}
}

func TestServiceCollectsEpisodeAndSubmitsCandidate(t *testing.T) {
	input := serviceMessageInput()
	cohort := serviceCohort(input)
	repository := &serviceRepositoryFake{cohorts: []Cohort{cohort}}
	preMessages := make([]WindowMessage, 25)
	for index := range preMessages {
		preMessages[index] = windowTestMessage(
			index,
			input.OccurredAt.Add(time.Duration(index-25)*time.Minute),
		)
		preMessages[index].ChatID = input.ChatID
	}
	pre := &preWindowSourceFake{messages: preMessages}
	submitter := &candidateSubmitterFake{}
	service := newServiceForTest(t, repository, pre, submitter)

	session, err := service.BeginMessage(context.Background(), input)
	if err != nil {
		t.Fatalf("BeginMessage() error = %v", err)
	}
	if !session.Enabled() {
		t.Fatal("session.Enabled() = false, want true")
	}
	contextSnapshot := fallbackControlContext(input)
	session.recorder.RecordIntent(context.Background(), map[string]any{"mode": "silent"})
	session.recorder.RecordContext(context.Background(), contextSnapshot, nil)
	session.recorder.RecordOutput(context.Background(), Output{
		Decision: OutputDecisionReply, Reply: "control reply", Latency: 90 * time.Millisecond,
		TokenUsage: &TokenUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13, Records: 1},
	})
	session.recorder.RecordDelivery(context.Background(), "delivered-message")

	if err := service.CompleteMessage(context.Background(), session); err != nil {
		t.Fatalf("CompleteMessage() error = %v", err)
	}
	if len(repository.episodes) != 1 {
		t.Fatalf("episodes = %d, want 1", len(repository.episodes))
	}
	episode := repository.episodes[0]
	if episode.AnchorEventID != input.EventID ||
		episode.PreWindowStart != preMessages[5].OccurredAt {
		t.Fatalf("episode = %#v", episode)
	}
	if len(repository.savedWindows) != 1 ||
		len(repository.savedWindows[0]) != PreWindowMessageLimit+1 {
		t.Fatalf("saved windows = %#v, want 20 pre + anchor", repository.savedWindows)
	}
	if got := repository.savedWindows[0][0].MessageID; got != "message-05" {
		t.Fatalf("first pre-window message = %q, want message-05", got)
	}
	anchor := repository.savedWindows[0][PreWindowMessageLimit]
	if anchor.Position != WindowPositionAnchor || anchor.MessageID != input.MessageID {
		t.Fatalf("anchor window message = %#v", anchor)
	}
	if len(repository.outputs) != 1 {
		t.Fatalf("lane outputs = %d, want 1", len(repository.outputs))
	}
	control := repository.outputs[0]
	if control.Lane != LaneControl || control.OutputMode != OutputModeActual ||
		control.JoinDecision != JoinDecisionJoin || control.ReplyText != "control reply" {
		t.Fatalf("control output = %#v", control)
	}
	var toolPlan struct {
		DeliveryMessageID string `json:"delivery_message_id"`
	}
	if err := json.Unmarshal(control.ToolPlanJSON, &toolPlan); err != nil {
		t.Fatalf("decode control tool plan: %v", err)
	}
	if toolPlan.DeliveryMessageID != "delivered-message" {
		t.Fatalf("control delivery message ID = %q", toolPlan.DeliveryMessageID)
	}
	if len(submitter.tasks) != 1 {
		t.Fatalf("candidate tasks = %d, want 1", len(submitter.tasks))
	}
	task := submitter.tasks[0]
	if task.Episode.ID != episode.ID || task.OutputID == "" ||
		task.ContextSnapshot.AnchorEventID != input.EventID ||
		task.ControlCapture.DeliveryMessageID != "delivered-message" {
		t.Fatalf("candidate task = %#v", task)
	}
}

func TestServiceObserveMessageClosesAtTopicBoundary(t *testing.T) {
	input := serviceMessageInput()
	episode := newEpisode(serviceCohort(input), input, nil, input.OccurredAt)
	window, _ := NewPostWindow(input.OccurredAt, "topic-old")
	repository := &serviceRepositoryFake{
		openEpisodes: []Episode{episode},
		postWindows:  map[string]PostWindow{episode.ID: *window},
	}
	service := newServiceForTest(
		t,
		repository,
		&preWindowSourceFake{},
		&candidateSubmitterFake{},
	)
	next := input
	next.EventID = "event-next"
	next.MessageID = "message-next"
	next.TopicID = "topic-new"
	next.OccurredAt = input.OccurredAt.Add(time.Minute)

	if err := service.ObserveWindowMessage(context.Background(), next); err != nil {
		t.Fatalf("ObserveWindowMessage() error = %v", err)
	}
	if len(repository.postMessages) != 0 {
		t.Fatalf("boundary message was appended: %#v", repository.postMessages)
	}
	if len(repository.closed) != 1 ||
		repository.closed[0].reason != PostWindowCloseTopicBoundary ||
		!repository.closed[0].at.Equal(next.OccurredAt) {
		t.Fatalf("closed windows = %#v", repository.closed)
	}
	if repository.readyCalls != 1 {
		t.Fatalf("ready calls = %d, want 1", repository.readyCalls)
	}
}

func TestBuildControlLaneOutputFallsBackForEarlySkip(t *testing.T) {
	input := serviceMessageInput()
	episode := newEpisode(serviceCohort(input), input, nil, input.OccurredAt)
	output, err := BuildControlLaneOutput(
		episode,
		input,
		CaptureSnapshot{
			IntentJSON: json.RawMessage(`{"decision":"skip"}`),
			Output:     &Output{Decision: OutputDecisionSkip, Thought: "quiet"},
		},
		input.OccurredAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("BuildControlLaneOutput() error = %v", err)
	}
	if output.JoinDecision != JoinDecisionSkip || output.ReplyText != "" ||
		output.ContextSnapshot.CurrentInput != input.Content ||
		output.ContextSnapshot.TokenBudget != 0 {
		t.Fatalf("fallback control output = %#v", output)
	}
}

func TestServiceCompleteCandidateMarksReady(t *testing.T) {
	input := serviceMessageInput()
	repository := &serviceRepositoryFake{}
	service := newServiceForTest(
		t,
		repository,
		&preWindowSourceFake{},
		&candidateSubmitterFake{},
	)
	episode := newEpisode(serviceCohort(input), input, nil, input.OccurredAt)
	output := serviceLaneOutput(episode, LaneCandidate)
	if err := service.CompleteCandidate(context.Background(), episode.ID, output); err != nil {
		t.Fatalf("CompleteCandidate() error = %v", err)
	}
	if len(repository.outputs) != 1 || repository.readyCalls != 1 {
		t.Fatalf("outputs/ready calls = %d/%d, want 1/1", len(repository.outputs), repository.readyCalls)
	}
}

type serviceRepositoryFake struct {
	cohorts      []Cohort
	episodes     []Episode
	outputs      []LaneOutput
	savedWindows [][]WindowMessage
	openEpisodes []Episode
	postWindows  map[string]PostWindow
	postMessages []WindowMessage
	closed       []struct {
		id     string
		at     time.Time
		reason PostWindowCloseReason
	}
	readyCalls int
}

func (r *serviceRepositoryFake) CreateCohort(context.Context, Cohort) error { return nil }
func (r *serviceRepositoryFake) ActiveCohorts(context.Context, string, time.Time) ([]Cohort, error) {
	return cloneCaptureValue(r.cohorts), nil
}
func (r *serviceRepositoryFake) GetOrCreateEpisode(_ context.Context, episode Episode) (*Episode, error) {
	for index := range r.episodes {
		if r.episodes[index].CohortID == episode.CohortID &&
			r.episodes[index].AnchorEventID == episode.AnchorEventID {
			stored := r.episodes[index]
			return &stored, nil
		}
	}
	r.episodes = append(r.episodes, episode)
	stored := episode
	return &stored, nil
}
func (r *serviceRepositoryFake) UpsertLaneOutput(_ context.Context, output LaneOutput) error {
	r.outputs = append(r.outputs, cloneCaptureValue(output))
	return nil
}
func (r *serviceRepositoryFake) AppendFeedback(context.Context, Feedback) error { return nil }
func (r *serviceRepositoryFake) FeedbackCandidates(
	context.Context,
	string,
	time.Time,
) ([]FeedbackCandidate, error) {
	return nil, nil
}
func (r *serviceRepositoryFake) AppendJudgment(context.Context, Judgment) error { return nil }
func (r *serviceRepositoryFake) EpisodesReadyForJudge(context.Context, time.Time, int) ([]Episode, error) {
	return nil, nil
}
func (r *serviceRepositoryFake) TransitionCohorts(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (r *serviceRepositoryFake) SaveWindowMessages(_ context.Context, _ string, messages []WindowMessage) error {
	r.savedWindows = append(r.savedWindows, cloneCaptureValue(messages))
	return nil
}
func (r *serviceRepositoryFake) OpenEpisodesForMessage(context.Context, string, time.Time) ([]Episode, error) {
	return cloneCaptureValue(r.openEpisodes), nil
}
func (r *serviceRepositoryFake) ApplyPostWindowObservation(
	_ context.Context,
	episodeID string,
	message WindowMessage,
	boundary bool,
) (PostWindowMutation, error) {
	window := r.postWindows[episodeID]
	added, err := window.Append(message, boundary)
	if err != nil {
		return PostWindowMutation{}, err
	}
	r.postWindows[episodeID] = window
	if added {
		r.postMessages = append(r.postMessages, window.Messages[len(window.Messages)-1])
	}
	mutation := PostWindowMutation{Added: added}
	if window.ClosedAt != nil {
		mutation.Closed = true
		mutation.ClosedAt = window.ClosedAt
		mutation.CloseReason = window.CloseReason
		mutation.Ready = true
		r.closed = append(r.closed, struct {
			id     string
			at     time.Time
			reason PostWindowCloseReason
		}{id: episodeID, at: *window.ClosedAt, reason: window.CloseReason})
		r.readyCalls++
	}
	return mutation, nil
}
func (r *serviceRepositoryFake) CloseExpiredPostWindows(
	_ context.Context,
	_ string,
	now time.Time,
) (int, error) {
	closed := 0
	for episodeID, window := range r.postWindows {
		advanced, err := window.Advance(now)
		if err != nil {
			return closed, err
		}
		if advanced {
			r.postWindows[episodeID] = window
			closed++
		}
	}
	return closed, nil
}
func (r *serviceRepositoryFake) CloseExpiredPostWindowsAll(
	ctx context.Context,
	now time.Time,
	_ int,
) (int, error) {
	return r.CloseExpiredPostWindows(ctx, "", now)
}
func (r *serviceRepositoryFake) MarkReadyIfComplete(context.Context, string, time.Time) (bool, error) {
	r.readyCalls++
	return true, nil
}

type preWindowSourceFake struct {
	messages []WindowMessage
	calls    int
}

func (s *preWindowSourceFake) MessagesBefore(
	context.Context,
	string,
	time.Time,
	int,
) ([]WindowMessage, error) {
	s.calls++
	return cloneCaptureValue(s.messages), nil
}

type candidateSubmitterFake struct {
	tasks []CandidateTask
}

func (s *candidateSubmitterFake) SubmitCandidate(_ context.Context, task CandidateTask) error {
	s.tasks = append(s.tasks, cloneCaptureValue(task))
	return nil
}

func newServiceForTest(
	t *testing.T,
	repository EvaluationRepository,
	pre PreWindowSource,
	submitter CandidateSubmitter,
) *Service {
	t.Helper()
	service, err := NewService(ServiceOptions{
		Repository: repository, PreWindowSource: pre, CandidateSubmitter: submitter,
		Now: func() time.Time {
			return serviceMessageInput().OccurredAt.Add(2 * time.Second)
		},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func serviceMessageInput() MessageInput {
	return MessageInput{
		AppID: "app-1", BotOpenID: "bot-1", ChatID: "chat-1",
		EventID: "event-anchor", MessageID: "message-anchor",
		TopicID: "topic-1", SenderOpenID: "user-1", Content: "what happened?",
		OccurredAt: time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC),
	}
}

func serviceCohort(input MessageInput) Cohort {
	return Cohort{
		ID: "cohort-1", AppID: input.AppID, BotOpenID: input.BotOpenID,
		ChatIDs: []string{input.ChatID},
		StartAt: input.OccurredAt.Add(-time.Hour), EndAt: input.OccurredAt.Add(time.Hour),
		Status: CohortStatusCollecting, ServingLane: LaneControl,
		ControlVersion: "control-v1", CandidateVersion: "candidate-v1",
		JudgeConfigJSON: json.RawMessage(`{}`), SamplingPolicyJSON: json.RawMessage(`{}`),
	}
}

func serviceLaneOutput(episode Episode, lane Lane) LaneOutput {
	mode := OutputModeShadow
	if episode.ServingLane == lane {
		mode = OutputModeActual
	}
	input := serviceMessageInput()
	return LaneOutput{
		ID: evaluationID("lane", episode.ID, string(lane)), EpisodeID: episode.ID,
		Lane: lane, OutputMode: mode, ActivationJSON: json.RawMessage(`{}`),
		RelevanceJSON: json.RawMessage(`{}`), JoinDecision: JoinDecisionSkip,
		TopicRelation: TopicRelationUnrelated, ContextSnapshot: fallbackControlContext(input),
		ExcludedContext: []ExcludedContextItem{}, ToolPlanJSON: json.RawMessage(`{}`),
		TokenUsageJSON: json.RawMessage(`{}`), ErrorJSON: json.RawMessage(`{}`),
		CreatedAt: input.OccurredAt, UpdatedAt: input.OccurredAt,
	}
}

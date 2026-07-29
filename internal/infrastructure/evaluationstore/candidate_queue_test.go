package evaluationstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
)

func TestCandidateQueueLeaseRetryAndCompletion(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	task := fixture.candidateTask(t, "lease")
	if err := fixture.repo.SubmitCandidate(ctx, task); err != nil {
		t.Fatalf("SubmitCandidate() error = %v", err)
	}
	if err := fixture.repo.SubmitCandidate(ctx, task); err != nil {
		t.Fatalf("SubmitCandidate(replay) error = %v", err)
	}
	claim := conversationeval.CandidateTaskClaim{
		WorkerID: "worker-1", LeaseTTL: time.Minute, Now: fixture.now,
	}
	lease, err := fixture.repo.ClaimCandidate(ctx, claim)
	if err != nil {
		t.Fatalf("ClaimCandidate() error = %v", err)
	}
	if lease.Task.ID != task.ID ||
		lease.Status != conversationeval.CandidateTaskRunning ||
		lease.AttemptCount != 1 {
		t.Fatalf("first lease = %#v", lease)
	}
	if err := fixture.repo.RetryCandidateTask(ctx, conversationeval.RetryCandidateTaskRequest{
		TaskID: task.ID, WorkerID: claim.WorkerID, AttemptCount: lease.AttemptCount,
		ErrorText: "temporary", FailedAt: fixture.now.Add(time.Second),
		RetryAt: fixture.now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("RetryCandidateTask() error = %v", err)
	}
	if _, err := fixture.repo.ClaimCandidate(ctx, conversationeval.CandidateTaskClaim{
		WorkerID: "too-early", LeaseTTL: time.Minute, Now: fixture.now.Add(time.Second),
	}); !errors.Is(err, conversationeval.ErrCandidateTaskNotFound) {
		t.Fatalf("early ClaimCandidate() error = %v", err)
	}
	second, err := fixture.repo.ClaimCandidate(ctx, conversationeval.CandidateTaskClaim{
		WorkerID: "worker-2", LeaseTTL: time.Minute, Now: fixture.now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("second ClaimCandidate() error = %v", err)
	}
	if second.AttemptCount != 2 {
		t.Fatalf("second attempt = %d, want 2", second.AttemptCount)
	}
	finishedAt := fixture.now.Add(3 * time.Second)
	if err := fixture.repo.CompleteCandidateTask(ctx, conversationeval.CompleteCandidateTaskRequest{
		TaskID: task.ID, WorkerID: "worker-2",
		AttemptCount: second.AttemptCount, FinishedAt: finishedAt,
	}); err != nil {
		t.Fatalf("CompleteCandidateTask() error = %v", err)
	}
	if err := fixture.repo.CompleteCandidateTask(ctx, conversationeval.CompleteCandidateTaskRequest{
		TaskID: task.ID, WorkerID: "worker-2",
		AttemptCount: second.AttemptCount, FinishedAt: finishedAt,
	}); !errors.Is(err, conversationeval.ErrCandidateTaskLeaseLost) {
		t.Fatalf("duplicate CompleteCandidateTask() error = %v", err)
	}
}

func TestCandidateQueueRejectsConflictingReplay(t *testing.T) {
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	task := fixture.candidateTask(t, "conflict")
	if err := fixture.repo.SubmitCandidate(ctx, task); err != nil {
		t.Fatalf("SubmitCandidate() error = %v", err)
	}
	conflict := task
	conflict.OutputID += "_forged"
	if err := fixture.repo.SubmitCandidate(ctx, conflict); !errors.Is(
		err,
		conversationeval.ErrInvalidContract,
	) {
		t.Fatalf("SubmitCandidate(conflict) error = %v", err)
	}
}

func (f *repositoryFixture) candidateTask(
	t *testing.T,
	name string,
) conversationeval.CandidateTask {
	t.Helper()
	ctx := context.Background()
	cohort := f.cohort("candidate_"+name, f.now.Add(-time.Hour), f.now.Add(time.Hour))
	if err := f.repo.CreateCohort(ctx, cohort); err != nil {
		t.Fatalf("CreateCohort() error = %v", err)
	}
	episode, err := f.repo.GetOrCreateEpisode(
		ctx,
		f.episode(cohort.ID, "episode_candidate_"+name+"_"+f.suffix, "anchor_candidate_"+name),
	)
	if err != nil {
		t.Fatalf("GetOrCreateEpisode() error = %v", err)
	}
	return conversationeval.CandidateTask{
		ID:     "task_" + name + "_" + f.suffix,
		Cohort: cohort, Episode: *episode,
		Message: conversationeval.MessageInput{
			AppID: cohort.AppID, BotOpenID: cohort.BotOpenID, ChatID: episode.ChatID,
			EventID: episode.AnchorEventID, MessageID: episode.AnchorMessageID,
			TopicID: episode.TopicID, SenderOpenID: "user_" + f.suffix,
			Content: "candidate input", OccurredAt: episode.AnchorAt,
		},
		OutputID: "output_" + name + "_" + f.suffix,
		ContextSnapshot: conversationeval.ContextSnapshot{
			SchemaVersion: conversationeval.SchemaVersion,
			AnchorEventID: episode.AnchorEventID, AnchorAt: episode.AnchorAt,
			Messages: []conversationeval.ContextItem{}, Retrieved: []conversationeval.ContextItem{},
			Events: []conversationeval.ContextItem{}, CurrentInput: "candidate input",
		},
		ExcludedContext: []conversationeval.ExcludedContextItem{},
		CreatedAt:       f.now,
	}
}

package agentstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	uuid "github.com/satori/go.uuid"
)

func TestStartInteractionIsIdempotentAndDoesNotPersistPlaintextToken(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	now := time.Now().UTC().Truncate(time.Microsecond)
	req := startInteractionRequest(f.runID, "secret-token", now)
	run, wait, err := f.repo.StartInteraction(context.Background(), req)
	if err != nil {
		t.Fatalf("StartInteraction(first): %v", err)
	}
	againRun, againWait, err := f.repo.StartInteraction(context.Background(), req)
	if err != nil {
		t.Fatalf("StartInteraction(duplicate): %v", err)
	}
	if againRun.ID != run.ID || againWait.ID != wait.ID {
		t.Fatalf("duplicate returned run=%q step=%q, want run=%q step=%q", againRun.ID, againWait.ID, run.ID, wait.ID)
	}
	if strings.Contains(wait.InputJSON, "secret-token") {
		t.Fatal("wait input persisted plaintext token")
	}
	var envelope interactionWaitPayload
	if err := json.Unmarshal([]byte(wait.InputJSON), &envelope); err != nil {
		t.Fatalf("decode wait input: %v", err)
	}
	if envelope.Version != 1 || envelope.InteractionID != req.InteractionID ||
		envelope.Kind != req.InteractionKind || envelope.Revision != req.Revision ||
		!envelope.ExpiresAt.Equal(req.ExpiresAt) || envelope.TokenHash != req.TokenHash {
		t.Fatalf("wait input = %#v", envelope)
	}
	var outboxes int64
	if err := f.db.Model(&model.AgentProjectionOutbox{}).Where("step_id = ?", wait.ID).Count(&outboxes).Error; err != nil {
		t.Fatal(err)
	}
	if outboxes != 1 {
		t.Fatalf("wait outboxes = %d, want 1", outboxes)
	}
}

func TestFindActiveRunUsesSessionPointerAndRejectsTerminalRun(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	if err := f.db.Model(&model.AgentSession{}).Where("id = ?", f.sessionID).
		Update("active_run_id", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	run, err := f.repo.FindActiveRun(context.Background(), f.sessionID)
	if err != nil {
		t.Fatalf("FindActiveRun(): %v", err)
	}
	if run.ID != f.runID {
		t.Fatalf("active run = %q, want %q", run.ID, f.runID)
	}
	if err := f.db.Model(&model.AgentRun{}).Where("id = ?", f.runID).
		Update("status", string(agentruntime.RunStatusCompleted)).Error; err != nil {
		t.Fatal(err)
	}
	_, err = f.repo.FindActiveRun(context.Background(), f.sessionID)
	if !errors.Is(err, agentruntime.ErrNotFound) {
		t.Fatalf("FindActiveRun(terminal) error = %v, want ErrNotFound", err)
	}
}

func TestConcurrentStartInteractionAllowsOneActiveRunPerSession(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	now := time.Now().UTC().Truncate(time.Microsecond)
	secondRunID := "run_test_" + uuid.NewV4().String()
	if err := f.db.Create(&model.AgentRun{
		ID: secondRunID, SessionID: f.sessionID, TriggerType: string(agentruntime.TriggerTypeMention),
		TriggerMessageID: "message_" + uuid.NewV4().String(), Status: string(agentruntime.RunStatusRunning),
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create second run: %v", err)
	}

	requests := []agentruntime.StartInteractionRequest{
		startInteractionRequest(f.runID, "token-one", now),
		startInteractionRequest(secondRunID, "token-two", now),
	}
	type result struct {
		run *agentruntime.AgentRun
		err error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, req := range requests {
		req := req
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, _, err := NewRepository(f.db).StartInteraction(context.Background(), req)
			results <- result{run: run, err: err}
		}()
	}
	wg.Wait()
	close(results)
	var winner string
	var successes, conflicts int
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winner = result.run.ID
		case errors.Is(result.err, ErrActiveRunConflict):
			conflicts++
		default:
			t.Fatalf("StartInteraction() error = %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}
	var session model.AgentSession
	if err := f.db.First(&session, "id = ?", f.sessionID).Error; err != nil {
		t.Fatal(err)
	}
	if session.ActiveRunID != winner {
		t.Fatalf("active run = %q, want winner %q", session.ActiveRunID, winner)
	}
}

func TestStartInteractionRejectsASecondDifferentInteractionOnWaitingRun(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first := startInteractionRequest(f.runID, "token-one", now)
	if _, _, err := f.repo.StartInteraction(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := startInteractionRequest(f.runID, "token-two", now)
	_, _, err := f.repo.StartInteraction(context.Background(), second)
	if !errors.Is(err, ErrInteractionConflict) {
		t.Fatalf("StartInteraction(second interaction) error = %v, want ErrInteractionConflict", err)
	}
}

func TestStartInteractionRequiresNextRevisionForNewWait(t *testing.T) {
	tests := []struct {
		name     string
		revision int64
		wantErr  bool
	}{
		{name: "equal current is stale", revision: 1, wantErr: true},
		{name: "exactly next", revision: 2},
		{name: "future jump", revision: 3, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
			req := startInteractionRequest(f.runID, "revision-token", time.Now().UTC())
			req.Revision = tt.revision
			_, _, err := f.repo.StartInteraction(context.Background(), req)
			if tt.wantErr {
				if !errors.Is(err, ErrInteractionConflict) {
					t.Fatalf("StartInteraction(revision=%d) error = %v, want ErrInteractionConflict", tt.revision, err)
				}
				var count int64
				if err := f.db.Model(&model.AgentStep{}).Where("run_id = ?", f.runID).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("persisted steps = %d, want 0", count)
				}
				return
			}
			if err != nil {
				t.Fatalf("StartInteraction(revision=%d): %v", tt.revision, err)
			}
		})
	}
}

func TestResolveInteractionRejectsWrongTokenAndExpiry(t *testing.T) {
	t.Run("wrong token", func(t *testing.T) {
		f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
		now := time.Now().UTC().Truncate(time.Microsecond)
		start := startInteractionRequest(f.runID, "correct-token", now)
		if _, _, err := f.repo.StartInteraction(context.Background(), start); err != nil {
			t.Fatal(err)
		}
		resolve := resolveInteractionRequest(start, "wrong-token", now.Add(time.Minute))
		_, _, err := f.repo.ResolveInteraction(context.Background(), resolve)
		if !errors.Is(err, ErrInteractionTokenMismatch) {
			t.Fatalf("ResolveInteraction() error = %v, want ErrInteractionTokenMismatch", err)
		}
		if strings.Contains(err.Error(), resolve.PresentedToken) {
			t.Fatal("token mismatch error leaked token")
		}
	})

	t.Run("expired", func(t *testing.T) {
		f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
		now := time.Now().UTC().Truncate(time.Microsecond)
		start := startInteractionRequest(f.runID, "correct-token", now)
		if _, _, err := f.repo.StartInteraction(context.Background(), start); err != nil {
			t.Fatal(err)
		}
		resolve := resolveInteractionRequest(start, "correct-token", start.ExpiresAt.Add(time.Microsecond))
		_, _, err := f.repo.ResolveInteraction(context.Background(), resolve)
		if !errors.Is(err, ErrInteractionExpired) {
			t.Fatalf("ResolveInteraction() error = %v, want ErrInteractionExpired", err)
		}
	})
}

func TestResolveInteractionResumesOnceAndIsIdempotent(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	now := time.Now().UTC().Truncate(time.Microsecond)
	start := startInteractionRequest(f.runID, "correct-token", now)
	if _, _, err := f.repo.StartInteraction(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	resolve := resolveInteractionRequest(start, "correct-token", now.Add(time.Minute))
	resolve.SourceRef = "idempotent-source-ref"
	run, resume, err := f.repo.ResolveInteraction(context.Background(), resolve)
	if err != nil {
		t.Fatalf("ResolveInteraction(first): %v", err)
	}
	if run.Status != agentruntime.RunStatusQueued || run.Revision != start.Revision+1 ||
		run.WaitingReason != "" || run.WaitingToken != "" {
		t.Fatalf("resumed run = %#v", run)
	}
	if resume.Kind != agentruntime.StepKindResume || resume.Status != agentruntime.StepStatusCompleted ||
		resume.OutputJSON != string(resolve.Outcome) {
		t.Fatalf("resume step = %#v", resume)
	}
	retry := resolve
	retry.EventID = "retry-event-with-different-id"
	retry.Outcome = json.RawMessage(`{"approved":true,"retry_payload_changed":true}`)
	againRun, againResume, err := f.repo.ResolveInteraction(context.Background(), retry)
	if err != nil {
		t.Fatalf("ResolveInteraction(duplicate): %v", err)
	}
	if againRun.ID != run.ID || againResume.ID != resume.ID {
		t.Fatalf("duplicate returned run=%q step=%q, want run=%q step=%q", againRun.ID, againResume.ID, run.ID, resume.ID)
	}
	wrongToken := retry
	wrongToken.PresentedToken = "wrong-token"
	_, _, err = f.repo.ResolveInteraction(context.Background(), wrongToken)
	if !errors.Is(err, ErrInteractionTokenMismatch) {
		t.Fatalf("ResolveInteraction(duplicate wrong token) error = %v, want ErrInteractionTokenMismatch", err)
	}
	var outboxes int64
	if err := f.db.Model(&model.AgentProjectionOutbox{}).
		Joins("JOIN agent_steps ON agent_steps.id = agent_projection_outbox.step_id").
		Where("agent_steps.run_id = ?", f.runID).Count(&outboxes).Error; err != nil {
		t.Fatal(err)
	}
	if outboxes != 2 {
		t.Fatalf("interaction outboxes = %d, want wait+resume = 2", outboxes)
	}
}

func TestStableResumeStepIDScopesSameSourceDedupeByRun(t *testing.T) {
	first := stableResumeStepID("run-one", "source:shared-message")
	second := stableResumeStepID("run-two", "source:shared-message")
	if first == second {
		t.Fatalf("stable resume IDs collide across runs: %q", first)
	}
	if first != stableResumeStepID("run-one", "source:shared-message") {
		t.Fatal("stable resume ID is not deterministic within a run")
	}
}

func TestResolveInteractionScopesSharedSourceRefByRun(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	now := time.Now().UTC().Truncate(time.Microsecond)
	suffix := uuid.NewV4().String()
	secondSessionID := "session_test_" + suffix
	secondRunID := "run_test_" + suffix
	if err := f.db.Create(&model.AgentSession{
		ID: secondSessionID, AppID: "app_" + suffix, BotOpenID: "bot_" + suffix,
		ChatID: "chat_" + suffix, ScopeType: "chat", ScopeID: "scope_" + suffix,
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Exec("DELETE FROM agent_sessions WHERE id = ?", secondSessionID).Error })
	if err := f.db.Create(&model.AgentRun{
		ID: secondRunID, SessionID: secondSessionID, TriggerType: string(agentruntime.TriggerTypeMention),
		TriggerMessageID: "message_" + suffix, Status: string(agentruntime.RunStatusRunning),
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	firstStart := startInteractionRequest(f.runID, "token-one", now)
	secondStart := startInteractionRequest(secondRunID, "token-two", now)
	if _, _, err := f.repo.StartInteraction(context.Background(), firstStart); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.repo.StartInteraction(context.Background(), secondStart); err != nil {
		t.Fatal(err)
	}
	firstResolve := resolveInteractionRequest(firstStart, "token-one", now.Add(time.Minute))
	firstResolve.EventID = ""
	firstResolve.SourceRef = "shared-source-ref"
	secondResolve := resolveInteractionRequest(secondStart, "token-two", now.Add(time.Minute))
	secondResolve.EventID = ""
	secondResolve.SourceRef = "shared-source-ref"
	_, firstResume, err := f.repo.ResolveInteraction(context.Background(), firstResolve)
	if err != nil {
		t.Fatal(err)
	}
	_, secondResume, err := f.repo.ResolveInteraction(context.Background(), secondResolve)
	if err != nil {
		t.Fatal(err)
	}
	if firstResume.ID == secondResume.ID {
		t.Fatalf("shared source ref produced colliding step ID %q", firstResume.ID)
	}
}

func TestResolveInteractionRejectsOldSourceRefFromAnotherInteractionOnSameRun(t *testing.T) {
	f := newRepositoryFixture(t, agentruntime.RunStatusRunning)
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstStart := startInteractionRequest(f.runID, "token-one", now)
	if _, _, err := f.repo.StartInteraction(context.Background(), firstStart); err != nil {
		t.Fatal(err)
	}
	firstResolve := resolveInteractionRequest(firstStart, "token-one", now.Add(time.Minute))
	firstResolve.EventID = ""
	firstResolve.SourceRef = "shared-source-ref"
	if _, _, err := f.repo.ResolveInteraction(context.Background(), firstResolve); err != nil {
		t.Fatal(err)
	}

	secondStart := startInteractionRequest(f.runID, "token-two", now.Add(2*time.Minute))
	secondStart.Revision = 4
	if _, _, err := f.repo.StartInteraction(context.Background(), secondStart); err != nil {
		t.Fatal(err)
	}
	secondResolve := resolveInteractionRequest(secondStart, "token-two", now.Add(3*time.Minute))
	secondResolve.EventID = ""
	secondResolve.SourceRef = firstResolve.SourceRef
	_, _, err := f.repo.ResolveInteraction(context.Background(), secondResolve)
	if !errors.Is(err, ErrInteractionConflict) {
		t.Fatalf("ResolveInteraction(reused source) error = %v, want ErrInteractionConflict", err)
	}
	var run model.AgentRun
	if err := f.db.First(&run, "id = ?", f.runID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != string(agentruntime.RunStatusWaitingApproval) ||
		run.Revision != secondStart.Revision || run.WaitingToken == "" {
		t.Fatalf("second interaction run changed: status=%q revision=%d token_empty=%v",
			run.Status, run.Revision, run.WaitingToken == "")
	}
	var resumes int64
	if err := f.db.Model(&model.AgentStep{}).
		Where("run_id = ? AND kind = ?", f.runID, string(agentruntime.StepKindResume)).
		Count(&resumes).Error; err != nil {
		t.Fatal(err)
	}
	if resumes != 1 {
		t.Fatalf("resume steps = %d, want 1", resumes)
	}
}

func startInteractionRequest(runID, token string, now time.Time) agentruntime.StartInteractionRequest {
	return agentruntime.StartInteractionRequest{
		RunID:           runID,
		StepID:          "step_wait_" + uuid.NewV4().String(),
		InteractionID:   "interaction_" + uuid.NewV4().String(),
		Revision:        2,
		TokenHash:       agentruntime.HashInteractionToken(token),
		InteractionKind: "approval",
		ExpiresAt:       now.Add(time.Hour),
		Projection:      testProjection(runID),
	}
}

func resolveInteractionRequest(start agentruntime.StartInteractionRequest, token string, resolvedAt time.Time) agentruntime.ResolveInteractionRequest {
	return agentruntime.ResolveInteractionRequest{
		RunID:          start.RunID,
		StepID:         start.StepID,
		InteractionID:  start.InteractionID,
		Revision:       start.Revision,
		PresentedToken: token,
		Action:         "approve",
		Outcome:        json.RawMessage(`{"approved":true}`),
		EventID:        "event_" + uuid.NewV4().String(),
		ResolvedAt:     resolvedAt,
		Projection:     testProjection(start.RunID),
	}
}

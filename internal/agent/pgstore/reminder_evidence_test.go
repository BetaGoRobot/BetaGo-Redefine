package pgstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func TestReminderContextEvidenceMutation(t *testing.T) {
	for _, mutation := range []string{"edit", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			f, w := workFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			input := domain.ReminderInput{Text: "quote-derived reminder", DueAt: time.Now().UTC().Add(time.Second)}
			j, task, x := workTurn(t, f, "quality-root", "alice", "reminder_create", input)
			material := event("quality-context", 1)
			material.Message.Source.MessageID = "quality-context"
			material.Message.Text = "quoted scheduling material"
			accept(t, f, material)
			if err := f.AddTurnSources(ctx, j, task, []domain.SourceRef{material.Message.Source}); err != nil {
				t.Fatal(err)
			}
			r, err := w.CreateReminder(ctx, j, task, x.Key, "work-policy", input)
			if err != nil {
				t.Fatal(err)
			}
			persistedTask, err := f.Task(ctx, task.Scope, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("task context sources=%d; frozen reminder sources=%d", len(persistedTask.ContextSources), len(r.Sources))
			changed := material
			changed.ID = "quality-context-mutated"
			changed.Message = material.Message.Clone()
			changed.Message.EventID = changed.ID
			changed.Message.Source.Version++
			changed.Message.UpdatedAt = time.Now().UTC()
			changed.OccurredAt = changed.Message.UpdatedAt
			if mutation == "delete" {
				changed.Type = domain.EventDelete
				changed.Message.Deleted = true
			} else {
				changed.Message.Text = "scheduling material revoked"
			}
			accept(t, f, changed)
			timer := time.NewTimer(time.Until(r.DueAt) + 10*time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-timer.C:
			}
			notify, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobReminder}, "quality-notifier", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := w.PrepareReminder(ctx, notify)
			if !errors.Is(err, domain.ErrStaleSource) {
				t.Fatalf("stale context evidence still authorizes reminder: status=%q origin=%s err=%v", prepared.Status, prepared.Origin.MessageID, err)
			}
		})
	}
}

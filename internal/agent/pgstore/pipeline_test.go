package pgstore_test

import (
	"context"
	"errors"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"testing"
	"time"
)

func TestPipelineArtifactsAreVersionedAndClaimGuarded(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	e := event("image", 1)
	e.Message.Media = []domain.Media{{Type: domain.MediaImage, ResourceID: "img", MIME: "image/png"}}
	accept(t, f, e)
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobMedia}, "media", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	artifact := domain.MediaArtifact{Source: e.Message.Source, Parts: []domain.MediaPart{{Text: "OCR", Page: 1}}}
	if err = f.PutMedia(ctx, j, artifact); err != nil {
		t.Fatal(err)
	}
	stored, err := f.Media(ctx, e.Message.Source)
	if err != nil || stored.Parts[0].Text != "OCR" {
		t.Fatalf("artifact missing %v", err)
	}
	e.ID = "edit"
	e.Message.EventID = e.ID
	e.Message.Source.Version = 2
	e.Message.UpdatedAt = e.Message.UpdatedAt.Add(time.Second)
	accept(t, f, e)
	if err = f.PutMedia(ctx, j, artifact); !errors.Is(err, domain.ErrStaleSource) {
		t.Fatalf("stale overwrite %v", err)
	}
}
func TestPublicationLockDoesNotBlockIngress(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e := event("one", 1)
	accept(t, f, e)
	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- f.Publication(ctx, testScope(), "message", func(context.Context) error { close(locked); <-release; return nil })
	}()
	<-locked
	e.ID = "edit"
	e.Message.EventID = e.ID
	e.Message.Source.Version = 2
	e.Message.UpdatedAt = e.Message.UpdatedAt.Add(time.Second)
	ingress, stop := context.WithTimeout(ctx, 300*time.Millisecond)
	defer stop()
	_, err := f.Accept(ingress, e)
	close(release)
	if err != nil {
		t.Fatalf("publication blocked ingress %v", err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPipelineRejectsExpiryDuringSourceLockWait(t *testing.T) {
	for _, kind := range []domain.JobKind{domain.JobMedia, domain.JobContexts} {
		for _, expiry := range []string{"lease_until", "expires_at"} {
			t.Run(string(kind)+"/"+expiry, func(t *testing.T) {
				f := setup(t)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				e := event("review-source", 1)
				e.Message.Media = []domain.Media{{Type: domain.MediaImage, ResourceID: "review"}}
				accept(t, f, e)
				j, err := f.ClaimJob(ctx, []domain.JobKind{kind}, "review", time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.db.ExecContext(ctx, `UPDATE "`+f.schema+`".jobs SET `+expiry+`=clock_timestamp()+interval '150 milliseconds' WHERE id=$1`, j.ID); err != nil {
					t.Fatal(err)
				}
				tx, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				var pid int
				if err = tx.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM pg_advisory_xact_lock(hashtextextended($1,0))`, j.Scope.Key()+":"+j.Source.MessageID).Scan(&pid); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					if kind == domain.JobMedia {
						done <- f.PutMedia(ctx, j, domain.MediaArtifact{Source: j.Source, Parts: []domain.MediaPart{{Text: "review"}}})
					} else {
						done <- f.PutStage(ctx, j, domain.PipelineStage{Source: j.Source, Name: "contexts", ContextID: "review", ContextVersion: 1}, []domain.SourceRef{j.Source})
					}
				}()
				waitForPGCondition(t, ctx, f.db, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid)
				waitForPGCondition(t, ctx, f.db, `SELECT clock_timestamp()>=`+expiry+` FROM "`+f.schema+`".jobs WHERE id=$1`, j.ID)
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if err = <-done; !errors.Is(err, domain.ErrStaleClaim) {
					t.Fatalf("artifact committed after %s expired while waiting source lock: got %v", expiry, err)
				}
			})
		}
	}
}

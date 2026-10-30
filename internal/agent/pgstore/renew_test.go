package pgstore_test

import (
	"context"
	"errors"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"testing"
	"time"
)

func TestRenewJobFencesAndRejectsExpiredLockWait(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	id, err := f.Enqueue(ctx, domain.JobSpec{Kind: domain.JobMedia, Scope: testScope()})
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobMedia}, "renew", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := f.RenewJob(ctx, j, 2*time.Minute)
	if err != nil || !renewed.LeaseUntil.After(j.LeaseUntil) {
		t.Fatalf("renew %v", err)
	}
	bad := j
	bad.Fence++
	if _, err = f.RenewJob(ctx, bad, time.Minute); !errors.Is(err, domain.ErrStaleClaim) {
		t.Fatalf("wrong fence %v", err)
	}
	if _, err = f.db.ExecContext(ctx, `UPDATE "`+f.schema+`".jobs SET lease_until=clock_timestamp()+interval '500 milliseconds' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err = tx.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM "`+f.schema+`".jobs WHERE id=$1 FOR UPDATE`, id).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := f.RenewJob(ctx, j, time.Minute); done <- e }()
	waitForPGCondition(t, ctx, f.db, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid)
	waitForPGCondition(t, ctx, f.db, `SELECT clock_timestamp()>=lease_until FROM "`+f.schema+`".jobs WHERE id=$1`, id)
	tx.Commit()
	if err = <-done; !errors.Is(err, domain.ErrStaleClaim) {
		t.Fatalf("renew resurrected expired claim %v", err)
	}
}
func TestRenewJobRejectsGlobalExpiryDuringRowLockWait(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	id, err := f.Enqueue(ctx, domain.JobSpec{Kind: domain.JobMedia, Scope: testScope()})
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobMedia}, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(ctx, `UPDATE "`+f.schema+`".jobs SET expires_at=clock_timestamp()+interval '500 milliseconds' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var pid int
	if err = tx.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM "`+f.schema+`".jobs WHERE id=$1 FOR UPDATE`, id).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := f.RenewJob(ctx, j, time.Minute); done <- err }()
	waitForPGCondition(t, ctx, f.db, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid)
	waitForPGCondition(t, ctx, f.db, `SELECT clock_timestamp()>=expires_at FROM "`+f.schema+`".jobs WHERE id=$1`, id)
	tx.Commit()
	if err = <-done; !errors.Is(err, domain.ErrStaleClaim) {
		t.Fatalf("global expired claim resurrected: %v", err)
	}
}

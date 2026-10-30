package domain

import (
	"errors"
	"testing"
	"time"
)

func TestSchedulePlanHasBoundedExplicitRecurrence(t *testing.T) {
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	good := ScheduleInput{Text: "喝水", StartAt: now.Add(time.Minute), IntervalSeconds: 60, Runs: 3}
	if err := ValidateScheduleInput(good, now); err != nil {
		t.Fatal(err)
	}
	for _, in := range []ScheduleInput{{Text: "", StartAt: good.StartAt, IntervalSeconds: 60, Runs: 1}, {Text: "x", StartAt: now, IntervalSeconds: 60, Runs: 1}, {Text: "x", StartAt: good.StartAt, IntervalSeconds: 59, Runs: 1}, {Text: "x", StartAt: good.StartAt, IntervalSeconds: 60, Runs: 367}, {Text: "x", StartAt: good.StartAt, IntervalSeconds: 366 * 86400, Runs: 2}} {
		if !errors.Is(ValidateScheduleInput(in, now), ErrInvalid) {
			t.Errorf("accepted %+v", in)
		}
	}
	if got := NextScheduleIndex(good, now.Add(150*time.Second), 0); got != 2 {
		t.Fatalf("catchup index %d", got)
	}
	if got := NextScheduleIndex(good, now.Add(5*time.Minute), 0); got != 3 {
		t.Fatalf("exhausted index %d", got)
	}
}
func TestScheduleQueryIsAnExplicitReadOnlyUnion(t *testing.T) {
	now := time.Now().UTC()
	in := ScheduleInput{StartAt: now.Add(time.Minute), IntervalSeconds: 60, Runs: 2, Query: &ScheduleQuery{Name: "gold_price_get", Arguments: []byte(`{"days":1}`)}}
	if err := ValidateScheduleInput(in, now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"feishu_send_message", "music_send_audio", "schedule_create", "catalog_put", "unknown"} {
		in.Query.Name = name
		if err := ValidateScheduleInput(in, now); err == nil {
			t.Fatalf("unsafe future effect %s", name)
		}
	}
	in.Query.Name = "gold_price_get"
	in.Text = "mixing text and query"
	if err := ValidateScheduleInput(in, now); err == nil {
		t.Fatal("ambiguous action union")
	}
}
func TestScheduleHorizonArithmeticCannotOverflow(t *testing.T) {
	now := time.Now().UTC()
	in := ScheduleInput{Text: "too far", StartAt: now.Add(time.Minute), IntervalSeconds: 366 * 86400, Runs: 366}
	if err := ValidateScheduleInput(in, now); err == nil {
		t.Fatal("overflow bypassed bounded scheduling horizon")
	}
}

package service

import (
	"context"
	"errors"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"time"
)

func (s *Service) schedule(ctx context.Context, j domain.Job) (retErr error) {
	ctx, span := telemetry.Start(ctx, "agent.schedule")
	defer func() { telemetry.End(ctx, span, retErr) }()
	run, err := s.c.Store.PrepareScheduleRun(ctx, j)
	if err != nil {
		return runtimeFailure("schedule.prepare", err)
	}
	if run.Status == "rendering" {
		if s.c.ScheduleRender == nil {
			return s.scheduleRenderFailure(ctx, j, run, "schedule.render", domain.ErrInvalid)
		}
		kind, content, renderErr := s.c.ScheduleRender(ctx, run)
		if renderErr != nil {
			return s.scheduleRenderFailure(ctx, j, run, "schedule.render", renderErr)
		}
		frozen, freezeErr := s.c.Store.FreezeScheduleRun(ctx, j, run, kind, content)
		err = freezeErr
		if err != nil {
			return s.scheduleRenderFailure(ctx, j, run, "schedule.freeze", err)
		}
		run = frozen
	}
	if run.Status != "sending" {
		return nil
	}
	if err = s.c.Store.CheckScheduleRun(ctx, j, run); err != nil {
		return runtimeFailure("schedule.guard", err)
	}
	id, err := s.c.Messenger.Send(ctx, run.Scope, run.Origin.MessageID, run.MessageType, run.Content, run.UUID)
	if err != nil {
		return telemetry.Wrap("schedule.send", "schedule_transport_failed", err)
	}
	receiptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return runtimeFailure("schedule.receipt.persist", s.c.Store.FinishScheduleRun(receiptCtx, j, run, id))
}

func (s *Service) scheduleRenderFailure(ctx context.Context, j domain.Job, run domain.ScheduleRun, stage string, cause error) error {
	if !permanent(cause) && (j.MaxAttempts == 0 || j.Attempt < j.MaxAttempts) {
		return runtimeFailure(stage, cause)
	}
	reason := "render_attempts_exhausted"
	switch {
	case errors.Is(cause, domain.ErrNotFound):
		reason = "render_capability_missing"
	case errors.Is(cause, domain.ErrUnauthorized):
		reason = "render_not_authorized"
	case errors.Is(cause, domain.ErrInvalid):
		reason = "render_invalid"
	case errors.Is(cause, domain.ErrStaleSource):
		reason = "render_source_invalid"
	case permanent(cause):
		reason = "render_rejected"
	}
	failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.c.Store.FailScheduleRun(failureCtx, j, run, reason); err != nil {
		return runtimeFailure("schedule.failure.persist", err)
	}
	return runtimeFailure(stage, cause)
}

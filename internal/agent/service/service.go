// Package service consumes durable ingress, native agent turns and frozen
// delivery jobs. HTTP ingress remains a separate persistence-only boundary.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/engine"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Options struct {
	Presentation                    func(context.Context, domain.Job) error
	ScheduleRender                  func(context.Context, domain.ScheduleRun) (string, string, error)
	Store                           *pgstore.Store
	Engine                          *engine.Engine
	Messenger                       atoms.Messenger
	Owner                           string
	Lease, Poll, TaskTimeout        time.Duration
	TurnTimeout                     time.Duration
	Concurrency, MaxConcurrentTasks int
	EligibleForConversation         func(context.Context, domain.Message) (bool, error)
}
type Service struct{ c Options }

func New(o Options) (*Service, error) {
	if o.Store == nil || o.Engine == nil || o.Messenger == nil || strings.TrimSpace(o.Owner) == "" {
		return nil, domain.ErrInvalid
	}
	if o.Lease == 0 {
		o.Lease = time.Minute
	}
	if o.Poll == 0 {
		o.Poll = 250 * time.Millisecond
	}
	if o.TaskTimeout == 0 {
		o.TaskTimeout = 30 * time.Minute
	}
	if o.TurnTimeout == 0 {
		o.TurnTimeout = 2 * time.Minute
	}
	if o.Concurrency == 0 {
		o.Concurrency = 4
	}
	if o.MaxConcurrentTasks == 0 {
		o.MaxConcurrentTasks = 4
	}
	if o.Lease < 30*time.Millisecond || o.Lease > time.Hour || o.Poll <= 0 || o.TaskTimeout < time.Second || o.TaskTimeout > 7*24*time.Hour || o.TurnTimeout < time.Millisecond || o.TurnTimeout > time.Hour || o.Concurrency < 1 || o.Concurrency > 64 || o.MaxConcurrentTasks < 1 || o.MaxConcurrentTasks > 64 {
		return nil, domain.ErrInvalid
	}
	o.Concurrency = min(o.Concurrency, o.MaxConcurrentTasks)
	return &Service{c: o}, nil
}

var runtimeKinds = []domain.JobKind{domain.JobRoute, domain.JobAgentTurn, domain.JobDelivery, domain.JobReminder, domain.JobSchedule, domain.JobPresentation}

func (s *Service) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	spawn := func(kinds []domain.JobKind) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				if err := s.Step(ctx, kinds...); err == nil {
					continue
				}
				timer := time.NewTimer(s.c.Poll)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	}
	ordinary := runtimeKinds
	if s.c.Presentation != nil {
		ordinary = make([]domain.JobKind, 0, len(runtimeKinds)-1)
		for _, kind := range runtimeKinds {
			if kind != domain.JobPresentation {
				ordinary = append(ordinary, kind)
			}
		}
	}
	for range s.c.Concurrency {
		spawn(ordinary)
	}
	if s.c.Presentation != nil {
		// The same queue and lifecycle reserve two I/O workers for card assets;
		// a slow audio conversion never occupies a native conversation worker.
		for range 2 {
			spawn([]domain.JobKind{domain.JobPresentation})
		}
	}
	wg.Wait()
	return ctx.Err()
}
func (s *Service) Step(ctx context.Context, kinds ...domain.JobKind) (retErr error) {
	if len(kinds) == 0 {
		kinds = runtimeKinds
	}
	for _, kind := range kinds {
		if kind != domain.JobRoute && kind != domain.JobAgentTurn && kind != domain.JobDelivery && kind != domain.JobReminder && kind != domain.JobSchedule && kind != domain.JobPresentation {
			return domain.ErrInvalid
		}
	}
	claimStarted := time.Now()
	j, err := s.c.Store.ClaimJob(ctx, kinds, s.c.Owner, s.c.Lease)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			claimMS := time.Since(claimStarted).Milliseconds()
			failedCtx := telemetry.Context(telemetry.Quiet(ctx), "claim_ms", claimMS)
			failedCtx, failedSpan := telemetry.Start(failedCtx, "agent.queue.claim", attribute.Int64("claim_ms", claimMS))
			telemetry.End(failedCtx, failedSpan, telemetry.Wrap("queue.claim", "database_claim_failed", err))
		}
		return err
	}
	jobCtx, cancel := context.WithCancel(telemetry.JobContext(ctx, j))
	defer cancel()
	lifecycleCtx := jobCtx
	if j.Kind == domain.JobAgentTurn || j.Kind == domain.JobDelivery {
		lifecycleCtx = telemetry.Quiet(lifecycleCtx)
	}
	lifecycleCtx, span := telemetry.Start(lifecycleCtx, "agent.job."+string(j.Kind), attribute.String("kind", string(j.Kind)), attribute.Int64("job_id", j.ID), attribute.Int("attempt", j.Attempt))
	// Only lifecycle logs are quiet. Keep business events on the same span at
	// their normal levels when a dependency becomes ready.
	jobCtx = trace.ContextWithSpan(jobCtx, span)
	var operationErr error
	defer func() {
		// Acknowledgement errors are the actual returned failure. A successful
		// acknowledgement still retains an earlier permanent rejection.
		if retErr != nil {
			operationErr = retErr
		}
		telemetry.End(lifecycleCtx, span, operationErr)
	}()
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(s.c.Lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				if _, e := s.c.Store.RenewJob(jobCtx, j, s.c.Lease); e != nil {
					telemetry.Error(jobCtx, "agent.job.lease_lost", runtimeFailure("job.lease.renew", e))
					cancel()
					return
				}
			}
		}
	}()
	err = s.handle(jobCtx, j)
	operationErr = err
	if recovered, recoveryErr := s.recoverContext(jobCtx, j, err); recoveryErr != nil {
		err = runtimeFailure("job.context.recover", recoveryErr)
		operationErr = err
	} else if recovered {
		telemetry.Info(jobCtx, "agent.job.context_replanned", "reason", rejectionReason(err))
		err = nil
	}
	close(stop)
	<-stopped
	if errors.Is(err, domain.ErrTaskBusy) || errors.Is(err, domain.ErrDeliveryPending) || errors.Is(err, engine.ErrNotReady) {
		stage, code := telemetry.Classify(err)
		telemetry.Info(telemetry.Quiet(jobCtx), "agent.job.deferred", "reason", deferredReason(err), "error_stage", stage, "error_code", code, "delay_ms", s.c.Poll.Milliseconds())
		return runtimeFailure("job.defer", s.c.Store.DeferJob(ctx, j, s.c.Poll))
	}
	if err == nil || permanent(err) {
		if err != nil {
			telemetry.Error(jobCtx, "agent.job.rejected", err, "reason", rejectionReason(err))
		}
		return runtimeFailure("job.complete", s.c.Store.CompleteJob(ctx, j))
	}
	if errors.Is(err, domain.ErrStaleClaim) {
		telemetry.Error(jobCtx, "agent.job.claim_lost", err)
		return err
	}
	telemetry.Error(jobCtx, "agent.job.retrying", err, "reason", rejectionReason(err))
	if retryErr := s.c.Store.RetryJob(ctx, j, errors.New("runtime operation failed: "+rejectionReason(err))); retryErr != nil {
		return runtimeFailure("job.retry", retryErr)
	}
	return err
}

func runtimeFailure(stage string, err error) error {
	_, code := telemetry.Classify(err)
	return telemetry.Wrap(stage, code, err)
}

func deferredReason(err error) string {
	switch {
	case errors.Is(err, domain.ErrTaskBusy):
		return "task_busy"
	case errors.Is(err, domain.ErrDeliveryPending):
		return "delivery_pending"
	default:
		return "media_not_ready"
	}
}
func permanent(e error) bool {
	return errors.Is(e, domain.ErrNotFound) || errors.Is(e, domain.ErrUnauthorized) || errors.Is(e, domain.ErrInvalid) || errors.Is(e, domain.ErrConflict) || errors.Is(e, domain.ErrStaleSource) || errors.Is(e, domain.ErrEffectUnknown)
}
func (s *Service) handle(ctx context.Context, j domain.Job) error {
	switch j.Kind {
	case domain.JobRoute:
		return s.route(ctx, j)
	case domain.JobAgentTurn:
		var r domain.TurnRequest
		if json.Unmarshal(j.Payload, &r) != nil || r.TaskID == "" || r.Revision <= 0 {
			return telemetry.Wrap("job.payload.validate", "turn_payload_invalid", domain.ErrInvalid)
		}
		// Bound this attempt, not the durable task or lease acknowledgement.
		turnCtx, cancel := context.WithTimeout(ctx, s.c.TurnTimeout)
		defer cancel()
		return s.c.Engine.Execute(turnCtx, j, r)
	case domain.JobDelivery:
		return s.deliver(ctx, j)
	case domain.JobReminder:
		return s.remind(ctx, j)
	case domain.JobSchedule:
		return s.schedule(ctx, j)
	case domain.JobPresentation:
		if s.c.Presentation == nil {
			return telemetry.Wrap("presentation.handler", "presentation_unconfigured", domain.ErrUnauthorized)
		}
		return s.c.Presentation(ctx, j)
	}
	return telemetry.Wrap("job.kind.validate", "job_kind_invalid", domain.ErrInvalid)
}
func (s *Service) route(ctx context.Context, j domain.Job) (retErr error) {
	ctx, span := telemetry.Start(ctx, "agent.route")
	decision, origin, addressed := "failed", "unknown", false
	defer func() {
		retErr = runtimeFailure("route", retErr)
		telemetry.Info(ctx, "agent.route.decision", "decision", decision, "addressed", addressed, "invocation_origin", origin)
		telemetry.End(ctx, span, retErr)
	}()
	if err := s.c.Store.CheckClaim(ctx, j); err != nil {
		return runtimeFailure("route.claim.guard", err)
	}
	m, err := s.c.Store.Message(ctx, j.Scope, j.Source.MessageID)
	if err != nil {
		return runtimeFailure("route.message.read", err)
	}
	ctx = telemetry.Context(ctx, "event_id", m.EventID)
	if m.Source.Version != j.Source.Version || m.Deleted {
		return runtimeFailure("route.source.guard", domain.ErrStaleSource)
	}
	var meta domain.IngressMetadata
	if json.Unmarshal(m.Raw, &meta) != nil {
		return telemetry.Wrap("route.metadata.validate", "ingress_metadata_invalid", domain.ErrInvalid)
	}
	addressed = meta.Addressed
	origin = domain.InvocationAmbient
	if addressed || command(strings.TrimSpace(m.Text)) {
		origin = domain.InvocationDirect
	}
	createTask := func(expires time.Time) error {
		created, err := s.c.Store.CreateTask(ctx, m, expires)
		if err != nil {
			return runtimeFailure("route.task.create", err)
		}
		ctx = telemetry.Context(ctx, "task_id", created.ID, "revision", created.Revision)
		decision, origin = "created_task", created.InvocationOrigin
		telemetry.Info(ctx, "agent.task.created", "invocation_origin", created.InvocationOrigin, "source_count", len(created.Sources))
		return nil
	}
	if meta.SenderType != "user" || meta.Kind == "recall" {
		decision = "ignored_sender_or_recall"
		return nil
	}
	if meta.Kind == "refresh" {
		existing, _, e := s.c.Store.TaskForMessage(ctx, j.Scope, m.Source.MessageID)
		if errors.Is(e, domain.ErrNotFound) {
			decision = "refresh_unbound"
			return nil
		}
		if e != nil {
			return runtimeFailure("route.refresh.lookup", e)
		}
		if existing.Root.MessageID == m.Source.MessageID && m.Source.Version > existing.Root.Version {
			return createTask(existing.ExpiresAt)
		}
		decision = "refresh_unchanged"
		return nil
	}
	if meta.Kind == "control" {
		if meta.Control == nil {
			return telemetry.Wrap("route.control.validate", "control_missing", domain.ErrInvalid)
		}
		decision = "control"
		_, err = s.c.Store.ControlTask(ctx, j.Scope, m.Source.AuthorID, *meta.Control, m.Source, "")
		return runtimeFailure("route.control", err)
	}
	if meta.Kind != "message" {
		decision = "non_message"
		return nil
	}
	groupState, err := s.c.Store.CatalogAccess(ctx, j.Scope)
	if err != nil {
		return runtimeFailure("route.group.settings", err)
	}
	// Explicit requests remain available to query or change group settings.
	// The conversation switch controls ambient participation; effect admission
	// independently enforces feature/mute policy before any business operation.
	if !meta.Addressed && !command(strings.TrimSpace(m.Text)) && (groupState.Muted(time.Now().UTC()) || !groupState.FeatureEnabled("conversation")) {
		decision = "group_participation_disabled"
		return nil
	}
	// Only concrete PG provenance can bind a group reply to a task. Topic IDs
	// (ThreadID) never participate in message lookup or task selection.
	var t domain.Task
	var d *domain.Delivery
	bound := false
	for _, id := range []string{m.ParentID, m.RootID} {
		if id == "" || id == m.Source.MessageID {
			continue
		}
		t, d, err = s.c.Store.TaskForMessage(ctx, j.Scope, id)
		if err == nil {
			bound = true
			break
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return runtimeFailure("route.reply.lookup", err)
		}
	}
	text := strings.TrimSpace(m.Text)
	if bound {
		ctx = telemetry.Context(ctx, "task_id", t.ID, "revision", t.Revision)
		if d != nil && d.Revision != t.Revision {
			// A confirmed ordinary utterance remains chat history after its
			// task is revised. The new speaker starts their own conversation;
			// old approvals, tool receipts and unknown sends stay revision-bound.
			if _, ordinary := domain.ConversationDeliveryText(*d); !ordinary || d.Scope != j.Scope || d.TaskID != t.ID || d.Status != "delivered" || d.MessageID == "" {
				return runtimeFailure("route.reply.guard", domain.ErrConflict)
			}
			switch text {
			case "确认", "确认执行", "approve", "取消", "取消任务", "cancel":
				decision = "stale_reply_control_ignored"
				return nil
			}
			if strings.HasPrefix(text, "/delegate ") || strings.HasPrefix(text, "委托 ") {
				return runtimeFailure("route.reply.guard", domain.ErrConflict)
			}
			return createTask(time.Now().UTC().Add(s.c.TaskTimeout))
		}
		// A reply to the exact delivered proposal carries all three identifiers.
		// Bare confirmations elsewhere remain ordinary material and cannot execute.
		if d != nil && d.Kind == "approval" {
			action := ""
			switch text {
			case "确认", "确认执行", "approve":
				action = "approve"
			case "取消", "取消任务", "cancel":
				action = "cancel"
			}
			if action != "" {
				decision = "bound_confirmation"
				_, err = s.c.Store.ControlTask(ctx, j.Scope, m.Source.AuthorID, domain.ControlAction{Action: action, TaskID: d.TaskID, Revision: d.Revision, ApprovalID: d.ApprovalID}, m.Source, "")
				return runtimeFailure("route.control", err)
			}
		}
		if text == "确认" || text == "取消" || text == "approve" || text == "cancel" {
			decision = "bare_confirmation_ignored"
			return nil
		}
		if t.Status == "completed" || t.Status == "canceled" {
			if d != nil || meta.Addressed || command(text) {
				return createTask(time.Now().UTC().Add(s.c.TaskTimeout))
			}
			decision = "completed_task_unaddressed"
			return nil
		}
		if strings.HasPrefix(text, "/delegate ") || strings.HasPrefix(text, "委托 ") {
			if len(meta.Mentions) != 1 {
				return telemetry.Wrap("route.delegate.validate", "delegate_mentions_invalid", domain.ErrInvalid)
			}
			decision = "delegate"
			_, err = s.c.Store.ControlTask(ctx, j.Scope, m.Source.AuthorID, domain.ControlAction{Action: "delegate", TaskID: t.ID, Revision: t.Revision}, m.Source, meta.Mentions[0].OpenID)
			return runtimeFailure("route.control", err)
		}
		if t.Authorized(m.Source.AuthorID) {
			decision = "modify"
			_, err = s.c.Store.ControlTask(ctx, j.Scope, m.Source.AuthorID, domain.ControlAction{Action: "modify", TaskID: t.ID, Revision: t.Revision}, m.Source, text)
			return runtimeFailure("route.control", err)
		}
		decision = "contribute"
		_, err = s.c.Store.Contribute(ctx, t, m)
		return runtimeFailure("route.contribute", err)
	}
	if !meta.Addressed && !command(text) {
		if s.c.EligibleForConversation == nil {
			decision = "no_eligibility"
			return nil
		}
		join, err := s.c.EligibleForConversation(ctx, m)
		if err != nil || !join {
			decision = "not_eligible"
			if err != nil {
				decision = "eligibility_failed"
			}
			return runtimeFailure("route.eligibility", err)
		}
	}
	return createTask(time.Now().UTC().Add(s.c.TaskTimeout))
}
func command(text string) bool { return domain.IsAgentCommand(text) }

func (s *Service) deliver(ctx context.Context, j domain.Job) (retErr error) {
	lifecycleCtx, span := telemetry.Start(telemetry.Quiet(ctx), "agent.delivery")
	ctx = trace.ContextWithSpan(ctx, span)
	defer func() { telemetry.End(lifecycleCtx, span, retErr) }()
	var p struct {
		ID string `json:"delivery_id"`
	}
	if json.Unmarshal(j.Payload, &p) != nil || p.ID == "" {
		return telemetry.Wrap("delivery.payload.validate", "delivery_payload_invalid", domain.ErrInvalid)
	}
	stored, err := s.c.Store.Delivery(ctx, j.Scope, p.ID)
	if err != nil {
		return runtimeFailure("delivery.read", err)
	}
	ctx = telemetry.Context(ctx, "task_id", stored.TaskID, "revision", stored.Revision)
	lifecycleCtx = telemetry.Context(lifecycleCtx, "task_id", stored.TaskID, "revision", stored.Revision)
	if err = s.c.Engine.RefreshTask(ctx, j.Scope, stored.TaskID); err != nil {
		telemetry.Error(ctx, "agent.delivery.skipped", runtimeFailure("delivery.refresh", err), "reason", "refresh_failed")
		return runtimeFailure("delivery.refresh", err)
	}
	d, err := s.c.Store.CheckDelivery(ctx, j, p.ID)
	if err != nil {
		if errors.Is(err, domain.ErrDeliveryPending) || errors.Is(err, domain.ErrTaskBusy) {
			telemetry.Info(telemetry.Quiet(ctx), "agent.delivery.skipped", "reason", deferredReason(err))
		} else {
			telemetry.Error(ctx, "agent.delivery.skipped", runtimeFailure("delivery.guard", err), "reason", "guard_failed")
		}
		return runtimeFailure("delivery.guard", err)
	}
	if d.Status == "delivered" {
		telemetry.Info(ctx, "agent.delivery.skipped", "reason", "already_delivered", "provider_message_id", d.MessageID)
		return nil
	}
	id, callErr := s.sendFrozenDelivery(ctx, d)
	if callErr != nil {
		stage, code := telemetry.Classify(callErr)
		return telemetry.Wrap(stage, code, callErr)
	}
	receiptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err = s.c.Store.FinishDelivery(receiptCtx, j, d, id); err != nil {
		return runtimeFailure("delivery.receipt.persist", err)
	}
	telemetry.Info(ctx, "agent.delivery.persisted", "provider_message_id", id, "delivery_kind", d.Kind, "message_type", d.MessageType)
	return nil
}

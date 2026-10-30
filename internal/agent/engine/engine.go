// Package engine connects durable task facts to native Eino ADK. Eino owns the
// tool loop and checkpoint protocol; PostgreSQL owns authority and side effects.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memereference"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	einoschema "github.com/eino-contrib/jsonschema"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Options struct {
	MemeReferences                 memereference.Provider
	MemeRevalidate                 memereference.Revalidator
	Store                          *pgstore.Store
	Model                          Chat
	Registry                       *atoms.Registry
	MaxIterations, MaxContextBytes int
	ConversationOutput             bool
	RefreshSource                  func(context.Context, domain.SourceRef) error
	ResolveMessage                 func(context.Context, domain.Scope, string) error
	GroupMembers                   GroupMembersReader
	BotOpenID                      string
}
type Engine struct {
	c       Options
	members memberCache
	memes   *memes.Library
}

func New(o Options) (*Engine, error) {
	if o.Store == nil || o.Model == nil || o.Registry == nil {
		return nil, domain.ErrInvalid
	}
	if o.MaxIterations == 0 {
		o.MaxIterations = 12
	}
	if o.MaxContextBytes == 0 {
		o.MaxContextBytes = 131072
	}
	if o.MaxIterations < 1 || o.MaxIterations > 100 || o.MaxContextBytes < 8192 || o.MaxContextBytes > 1<<20 {
		return nil, domain.ErrInvalid
	}
	library, err := memes.Default()
	if err != nil {
		return nil, telemetry.Wrap("engine.memes.load", "meme_corpus_invalid", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := o.Store.SyncToolPolicies(ctx, o.Registry.Policies()); e != nil {
		return nil, e
	}
	meta := library.Metadata()
	telemetry.Info(ctx, "agent.memes.loaded", "corpus_count", meta.Count, "corpus_sha256", meta.SHA256)
	return &Engine{c: o, memes: library}, nil
}
func (e *Engine) Execute(ctx context.Context, j domain.Job, r domain.TurnRequest) (retErr error) {
	ctx = telemetry.Context(ctx, "scope_hash", j.Scope.Key(), "message_id", j.Source.MessageID, "job_id", j.ID, "kind", string(j.Kind), "attempt", j.Attempt, "task_id", r.TaskID, "revision", r.Revision)
	lifecycleCtx, span := telemetry.Start(telemetry.Quiet(ctx), "agent.turn", attribute.Bool("resume", r.ApprovalID != ""))
	ctx = trace.ContextWithSpan(ctx, span)
	defer func() { telemetry.End(lifecycleCtx, span, retErr) }()
	if err := engineStage(ctx, "agent.turn.refresh", "turn.refresh", func(stageCtx context.Context) error { return e.RefreshTask(stageCtx, j.Scope, r.TaskID) }); err != nil {
		return err
	}
	var t domain.Task
	err := engineStage(ctx, "agent.turn.start", "turn.start", func(stageCtx context.Context) error {
		var err error
		t, err = e.c.Store.StartTurn(stageCtx, j, r)
		return err
	})
	if err != nil {
		return err
	}
	ctx = telemetry.Context(ctx, "task_id", t.ID, "revision", t.Revision)
	lifecycleCtx = telemetry.Context(lifecycleCtx, "task_id", t.ID, "revision", t.Revision)
	span.SetAttributes(attribute.String("task_id", t.ID), attribute.Int64("revision", t.Revision), attribute.String("task_status", t.Status), attribute.Int("source_count", len(t.Sources)), attribute.Int("context_source_count", len(t.ContextSources)))
	telemetry.Info(telemetry.Quiet(ctx), "agent.turn.ready", "task_status", t.Status, "invocation_origin", t.InvocationOrigin, "source_count", len(t.Sources), "context_source_count", len(t.ContextSources))
	if t.Status == "ready_delivery" {
		telemetry.Info(telemetry.Quiet(ctx), "agent.turn.skipped", "reason", "ready_delivery")
		return nil
	}
	model := e.c.Model
	var references *memeReferenceChat
	if e.c.MemeReferences != nil {
		currentID := t.Root.MessageID
		if len(t.Sources) > 0 {
			currentID = t.Sources[0].MessageID
		}
		references = &memeReferenceChat{client: model, currentMessageID: currentID, currentChatID: t.Scope.ChatID, revalidate: e.c.MemeRevalidate}
		model = references
	}
	var tools []tool.BaseTool
	definitions, accessErr := e.c.Registry.AvailableDefinitions(ctx, atoms.Invocation{Task: t, Job: j, ActorID: t.LastActorID})
	if accessErr != nil {
		return stageFailure("turn.tools.group.guard", accessErr)
	}
	for _, d := range definitions {
		tools = append(tools, &boundTool{definition: d, store: e.c.Store, job: j, task: t, refresh: e.RefreshTask, present: e.presentTool})
	}
	agentInstruction := instruction + "\n" + replyConstraintsInstruction + "\n" + chatVoiceInstruction
	if e.c.MemeReferences != nil {
		agentInstruction += "\n" + memereference.Instruction
	}
	if e.c.ConversationOutput {
		agentInstruction += "\n" + conversationOutputInstruction
	}
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "group_agent", Description: "Virtual AI group member", Instruction: agentInstruction, Model: newNativeChatAdapter(model, e.c.MaxContextBytes, e.c.ConversationOutput), ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools, ExecuteSequentially: true}}, MaxIterations: e.c.MaxIterations})
	if err != nil {
		return stageFailure("turn.agent.create", err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, CheckPointStore: e.c.Store.Checkpoints(j, t)})
	checkpointID := fmt.Sprintf("%s:%d", t.ID, t.Revision)
	nativeLifecycleCtx, nativeSpan := telemetry.Start(telemetry.Quiet(ctx), "agent.turn.native", attribute.Bool("resume", r.ApprovalID != ""), attribute.Int("max_iterations", e.c.MaxIterations))
	nativeCtx := trace.ContextWithSpan(ctx, nativeSpan)
	nativeEnded := false
	defer func() {
		if !nativeEnded {
			telemetry.End(nativeLifecycleCtx, nativeSpan, retErr)
		}
	}()
	var iter *adk.AsyncIterator[*adk.AgentEvent]
	if r.ApprovalID != "" {
		as, er := e.c.Store.Approvals(ctx, t.Scope, t.ID)
		if er != nil {
			return stageFailure("turn.resume.approvals", er)
		}
		var target string
		for _, a := range as {
			if a.ID == r.ApprovalID && (a.Status == "approved" || a.Status == "executed") && a.Revision == t.Revision {
				target = a.InterruptID
			}
		}
		if target == "" {
			return telemetry.Wrap("turn.resume.validate", "approval_target_missing", domain.ErrConflict)
		}
		iter, err = runner.ResumeWithParams(nativeCtx, checkpointID, &adk.ResumeParams{Targets: map[string]any{target: r.ApprovalID}})
		if err != nil {
			return stageFailure("turn.resume", err)
		}
	} else {
		var prompt string
		var refs []domain.SourceRef
		var replyTargets []string
		er := engineStage(ctx, "agent.turn.prompt", "turn.prompt", func(stageCtx context.Context) error {
			var err error
			prompt, refs, replyTargets, err = e.prompt(stageCtx, t)
			if err == nil {
				telemetry.Info(stageCtx, "agent.turn.prompt.built", "estimated_scene_bytes", len(prompt), "limit_bytes", e.c.MaxContextBytes/2, "source_count", len(refs), "reply_target_count", len(replyTargets))
			}
			return err
		})
		if er != nil {
			if errors.Is(er, ErrMediaFailed) {
				if qerr := e.c.Store.QueueFinalDelivery(ctx, j, t, "任务所需的媒体解析失败，暂时无法处理此请求。请重新发送媒体或提供文字内容。"); qerr != nil {
					return stageFailure("turn.delivery.queue", qerr)
				}
				telemetry.Info(ctx, "agent.delivery.queued", "delivery_kind", "media_failed", "source_count", len(t.ContextSources))
				return stageFailure("turn.end", e.c.Store.EndTurn(ctx, j, t, "media_failed"))
			}
			return er
		}
		er = engineStage(ctx, "agent.turn.set_context", "turn.set_context", func(stageCtx context.Context) error {
			var err error
			t, err = e.c.Store.SetTurnContext(stageCtx, j, t, refs, replyTargets)
			return err
		})
		if er != nil {
			return er
		}
		iter = runner.Run(nativeCtx, []*schema.Message{schema.UserMessage(prompt)}, adk.WithCheckPointID(checkpointID))
	}
	final := ""
	interrupts := map[string]string{}
	var runErr error
	eventCount := 0
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		eventCount++
		if event.Err != nil {
			runErr = stageFailure("turn.runner", event.Err)
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			for _, ic := range event.Action.Interrupted.InterruptContexts {
				if ic.IsRootCause {
					if id, ok := ic.Info.(string); ok && strings.HasPrefix(id, "ap_") {
						interrupts[id] = ic.ID
					}
				}
			}
		}
		if event.Output != nil && event.Output.MessageOutput != nil {
			m, er := event.Output.MessageOutput.GetMessage()
			if er != nil {
				runErr = stageFailure("turn.runner.output", er)
				continue
			}
			if m != nil && m.Role == schema.Assistant && len(m.ToolCalls) == 0 {
				final = m.Content
			}
		}
	}
	nativeSpan.SetAttributes(attribute.Int("event_count", eventCount), attribute.Int("interrupt_count", len(interrupts)), attribute.Int("terminal_bytes", len(final)))
	telemetry.Info(nativeCtx, "agent.turn.native.result", "event_count", eventCount, "interrupt_count", len(interrupts), "terminal_bytes", len(final))
	telemetry.End(nativeLifecycleCtx, nativeSpan, runErr)
	nativeEnded = true
	if runErr != nil {
		return runErr
	}
	if len(interrupts) > 0 {
		// Draining the iterator above waits for native checkpoint persistence before
		// making any confirmation visible or eligible for user approval.
		for id, target := range interrupts {
			if err = e.c.Store.BindInterrupt(ctx, j, t, id, target); err != nil {
				return stageFailure("turn.interrupt.bind", err)
			}
		}
		as, er := e.c.Store.Approvals(ctx, t.Scope, t.ID)
		if er != nil {
			return stageFailure("turn.interrupt.approvals", er)
		}
		for _, a := range as {
			if _, ok := interrupts[a.ID]; ok && a.Status == "pending" {
				card, er := ApprovalCard(t, a)
				if er != nil {
					return stageFailure("turn.approval.render", er)
				}
				if er = e.c.Store.QueueApprovalDelivery(ctx, j, t, a, card); er != nil {
					return stageFailure("turn.delivery.queue", er)
				}
				telemetry.Info(ctx, "agent.delivery.queued", "delivery_kind", "approval", "content_bytes", len(card))
			}
		}
		return stageFailure("turn.end", e.c.Store.EndTurn(ctx, j, t, "waiting"))
	}
	if strings.TrimSpace(final) == "" {
		return telemetry.Wrap("turn.terminal.validate", "terminal_missing", fmt.Errorf("agent produced no final response"))
	}
	if err = e.RefreshTask(ctx, t.Scope, t.ID); err != nil {
		return stageFailure("turn.terminal.refresh", err)
	}
	if e.c.ConversationOutput {
		plan, parseErr := domain.ParseConversationPlan(final)
		if parseErr != nil {
			return telemetry.Wrap("turn.terminal.parse", "terminal_envelope_invalid", fmt.Errorf("%w: invalid terminal envelope", ErrConversationOutput))
		}
		if e.c.MemeReferences != nil {
			if !safeMemeReferenceVisiblePlan(plan, references.cards) || !references.safeKnowledgePlan(plan) {
				return telemetry.Wrap("turn.terminal.validate", "meme_reference_disclosure", fmt.Errorf("%w: internal reference material in visible output", ErrConversationOutput))
			}
			logMemeAttribution(ctx, plan, references.suppliedIDs())
		}
		plan.MemeUseIDs, plan.MemeUseIDsMalformed = nil, false
		telemetry.Info(ctx, "agent.turn.decision", "choice", plan.Choice, "bubble_count", len(plan.Messages), "source_count", len(t.ContextSources), "reply_target_count", len(t.ReplyTargets), "terminal_bytes", len(final))
		if queueErr := e.c.Store.QueueConversationPlan(ctx, j, t, plan); queueErr != nil {
			// A model-proposed unknown target is a malformed output, not a
			// permanent task failure or permission grant. Retry with a fresh scene.
			if errors.Is(queueErr, domain.ErrUnauthorized) || errors.Is(queueErr, domain.ErrInvalid) {
				return telemetry.Wrap("turn.delivery.queue", "conversation_target_invalid", fmt.Errorf("%w: invalid conversation presentation or target", ErrConversationOutput))
			}
			return stageFailure("turn.delivery.queue", queueErr)
		}
		telemetry.Info(ctx, "agent.delivery.queued", "delivery_kind", "conversation", "choice", plan.Choice, "bubble_count", len(plan.Messages), "source_count", len(t.ContextSources))
		return nil
	}
	if e.c.MemeReferences != nil && (!safeMemeReferenceVisibleText(final, references.cards) || memereference.ContainsSuppliedIdentifier(final, references.knowledgeIDs)) {
		return telemetry.Wrap("turn.terminal.validate", "meme_reference_disclosure", fmt.Errorf("%w: internal reference material in visible output", ErrConversationOutput))
	}
	if err = e.c.Store.QueueFinalDelivery(ctx, j, t, final); err != nil {
		return stageFailure("turn.delivery.queue", err)
	}
	telemetry.Info(ctx, "agent.delivery.queued", "delivery_kind", "final", "content_bytes", len(final), "source_count", len(t.ContextSources))
	return stageFailure("turn.end", e.c.Store.EndTurn(ctx, j, t, "completed"))
}

func stageFailure(stage string, err error) error {
	_, code := telemetry.Classify(err)
	switch {
	case errors.Is(err, ErrNotReady):
		code = "media_not_ready"
	case errors.Is(err, ErrMediaFailed):
		code = "media_failed"
	case errors.Is(err, ErrConversationOutput):
		code = "conversation_output_invalid"
	}
	return telemetry.Wrap(stage, code, err)
}

func engineStage(ctx context.Context, name, stage string, fn func(context.Context) error) error {
	lifecycleCtx, span := telemetry.Start(telemetry.Quiet(ctx), name)
	err := stageFailure(stage, fn(trace.ContextWithSpan(ctx, span)))
	telemetry.End(lifecycleCtx, span, err)
	return err
}

type boundTool struct {
	definition atoms.Definition
	store      *pgstore.Store
	job        domain.Job
	task       domain.Task
	refresh    func(context.Context, domain.Scope, string) error
	present    func(context.Context, domain.Scope, string, string) (string, error)
}

func (b *boundTool) Info(context.Context) (*schema.ToolInfo, error) {
	var sc einoschema.Schema
	if err := json.Unmarshal(b.definition.Parameters, &sc); err != nil {
		return nil, telemetry.Wrap("tool.schema.validate", "tool_schema_invalid", err)
	}
	return &schema.ToolInfo{Name: b.definition.Name, Desc: b.definition.Description, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&sc)}, nil
}
func (b *boundTool) InvokableRun(ctx context.Context, raw string, _ ...tool.Option) (result string, retErr error) {
	permissionMode := "read"
	if b.definition.SideEffect {
		permissionMode = "approval"
		if b.definition.AutoExecute {
			permissionMode = "auto"
		}
	}
	ctx = telemetry.Context(ctx, "task_id", b.task.ID, "revision", b.task.Revision, "job_id", b.job.ID)
	ctx, span := telemetry.Start(ctx, "agent.tool.invoke", attribute.String("tool", b.definition.Name), attribute.Int("argument_bytes", len(raw)), attribute.String("permission_mode", permissionMode), attribute.Bool("side_effect", b.definition.SideEffect), attribute.Bool("cache_hit", false))
	telemetry.Payload(ctx, "agent.tool.arguments", "arguments", raw)
	interrupted := false
	outputBytes := 0
	defer func() {
		telemetry.Payload(ctx, "agent.tool.output", "tool_result", result)
		outputBytes = max(outputBytes, len(result))
		span.SetAttributes(attribute.Int("output_bytes", outputBytes))
		telemetry.Info(ctx, "agent.tool.result", "tool", b.definition.Name, "output_bytes", outputBytes, "interrupted", interrupted)
		if interrupted {
			telemetry.End(ctx, span, nil)
		} else {
			telemetry.End(ctx, span, retErr)
		}
	}()
	var args string
	err := engineStage(ctx, "agent.tool.validate", "tool.validate", func(context.Context) error {
		var err error
		args, err = b.definition.Validate(raw)
		return telemetry.Wrap("tool.validate", "tool_arguments_invalid", err)
	})
	if err != nil {
		return "", err
	}
	if err = b.definition.Authorize(ctx, atoms.Invocation{Task: b.task, Job: b.job, ActorID: b.task.LastActorID}); err != nil {
		return "", stageFailure("tool.group.guard", err)
	}
	if err = b.store.CheckTask(ctx, b.job, b.task); err != nil {
		return "", stageFailure("tool.task.guard", err)
	}
	if err = b.store.CheckToolPolicy(ctx, b.definition.Name, b.definition.Digest, b.definition.SideEffect, b.definition.AutoExecute); err != nil {
		return "", stageFailure("tool.policy.guard", err)
	}
	// Only immutable effect receipts are reusable. Dynamic reads must observe
	// current PG progress/evidence even within the same turn or native resume.
	if b.definition.SideEffect && !b.definition.AutoExecute && !b.definition.PerActorTurn {
		key := domain.StableID(b.task.ID, b.definition.Name, args)
		existing, lookupErr := b.store.ToolExecution(ctx, b.task.Scope, b.task.ID, key)
		if lookupErr == nil && existing.Status == "completed" {
			span.SetAttributes(attribute.Bool("cache_hit", true))
			telemetry.Info(ctx, "agent.tool.cache_hit", "tool", b.definition.Name, "output_bytes", len(existing.Result), "permission_mode", permissionMode)
			return existing.Result, nil
		}
		if lookupErr != nil && !errors.Is(lookupErr, domain.ErrNotFound) {
			return "", stageFailure("tool.cache.lookup", lookupErr)
		}
	}
	approvalID := ""
	if b.definition.SideEffect && !b.definition.AutoExecute {
		a, er := b.store.FreezeApproval(ctx, b.job, b.task, b.definition.Name, args, b.definition.Digest)
		if er != nil {
			return "", stageFailure("tool.approval.freeze", er)
		}
		was, has, state := tool.GetInterruptState[string](ctx)
		target, dataOK, data := tool.GetResumeContext[string](ctx)
		if !was || !has || state != a.ID || !target || !dataOK || data != a.ID {
			interrupted = true
			telemetry.Info(ctx, "agent.tool.interrupted", "tool", b.definition.Name, "permission_mode", permissionMode)
			return "", tool.StatefulInterrupt(ctx, a.ID, a.ID)
		}
		approvalID = a.ID
	}
	if b.definition.SideEffect && b.refresh != nil {
		if err = engineStage(ctx, "agent.tool.refresh", "tool.refresh", func(stageCtx context.Context) error { return b.refresh(stageCtx, b.task.Scope, b.task.ID) }); err != nil {
			return "", err
		}
	}
	var ledger domain.ToolExecution
	var reused bool
	err = engineStage(ctx, "agent.tool.begin_ledger", "tool.ledger.begin", func(stageCtx context.Context) error {
		var err error
		begin := b.store.BeginTool
		if b.definition.PerActorTurn {
			begin = b.store.BeginToolForActorTurn
		}
		ledger, reused, err = begin(stageCtx, b.job, b.task, b.definition.Name, args, approvalID, b.definition.SideEffect, b.definition.Digest)
		return err
	})
	if err != nil {
		if b.definition.AutoExecute && errors.Is(err, domain.ErrEffectUnknown) {
			// An uncertain receipt is observable, never authority to retry. Check
			// guards again before turning it into a model-visible observation.
			if guardErr := b.store.CheckTask(ctx, b.job, b.task); guardErr != nil {
				return "", stageFailure("tool.task.guard", guardErr)
			}
			if policyErr := b.store.CheckToolPolicy(ctx, b.definition.Name, b.definition.Digest, b.definition.SideEffect, b.definition.AutoExecute); policyErr != nil {
				return "", stageFailure("tool.policy.guard", policyErr)
			}
			telemetry.Warn(ctx, "agent.tool.unknown", "tool", b.definition.Name, "reason", "effect_unknown", "replay_blocked", true)
			return unknownEffectObservation, nil
		}
		return "", err
	}
	if reused {
		span.SetAttributes(attribute.Bool("cache_hit", true))
		telemetry.Info(ctx, "agent.tool.cache_hit", "tool", b.definition.Name, "output_bytes", len(ledger.Result), "permission_mode", permissionMode)
		return ledger.Result, nil
	}
	if err = b.store.CheckToolPolicy(ctx, b.definition.Name, b.definition.Digest, b.definition.SideEffect, b.definition.AutoExecute); err != nil {
		return "", stageFailure("tool.policy.guard", err)
	}
	if err = b.store.CheckTask(ctx, b.job, b.task); err != nil {
		return "", stageFailure("tool.task.guard", err)
	}
	var callResult string
	callErr := engineStage(ctx, "agent.tool.execute", "tool.execute", func(stageCtx context.Context) error {
		var err error
		callResult, err = b.definition.Run(stageCtx, atoms.Invocation{Task: b.task, Job: b.job, PolicyDigest: b.definition.Digest, ActorID: ledger.ActorID, ExecutionKey: ledger.Key}, args)
		return err
	})
	result = callResult
	outputBytes = len(callResult)
	if callErr == nil && !b.definition.SideEffect {
		refs, sourceErr := b.definition.Sources(result)
		sourceErr = stageFailure("tool.sources.parse", sourceErr)
		span.SetAttributes(attribute.Int("source_count", len(refs)))
		if sourceErr == nil && len(refs) > 0 {
			sourceErr = engineStage(ctx, "agent.tool.bind_sources", "tool.sources.bind", func(stageCtx context.Context) error { return b.store.AddTurnSources(stageCtx, b.job, b.task, refs) })
		}
		if sourceErr != nil {
			callErr = sourceErr
		}
		// Native tools retain the turn-start Task value. The durable ledger can
		// already contain dependencies from earlier reads in this same turn.
		// A diagnostic read must not change the call result or its authority.
		current, readErr := b.store.Task(ctx, b.task.Scope, b.task.ID)
		if readErr == nil {
			span.SetAttributes(attribute.Int("context_source_count", len(current.ContextSources)))
			telemetry.Info(ctx, "agent.tool.sources", "tool", b.definition.Name, "source_count", len(refs), "context_source_count", len(current.ContextSources), "source_limit", 256, "output_bytes", len(result))
		} else {
			telemetry.Error(ctx, "agent.tool.sources.observation_failed", stageFailure("tool.sources.observe", readErr), "tool", b.definition.Name, "source_count", len(refs), "output_bytes", len(result))
		}
	}
	receiptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err = engineStage(receiptCtx, "agent.tool.finish_ledger", "tool.ledger.finish", func(stageCtx context.Context) error {
		return b.store.FinishTool(stageCtx, b.job, b.task, ledger, result, callErr)
	}); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", stageFailure("tool.context", err)
	}
	if b.definition.AutoExecute && callErr != nil && !toolGuardError(callErr) {
		if err = b.store.CheckToolPolicy(ctx, b.definition.Name, b.definition.Digest, b.definition.SideEffect, b.definition.AutoExecute); err != nil {
			return "", stageFailure("tool.policy.guard", err)
		}
		telemetry.Warn(ctx, "agent.tool.unknown", "tool", b.definition.Name, "reason", "effect_unknown", "replay_blocked", true)
		return unknownEffectObservation, nil
	}
	if callErr == nil && b.present != nil {
		view, presentErr := b.present(ctx, b.task.Scope, b.definition.Name, result)
		if presentErr != nil {
			return "", stageFailure("tool.presentation", presentErr)
		}
		if err = b.store.CheckTask(ctx, b.job, b.task); err != nil {
			return "", stageFailure("tool.task.guard", err)
		}
		return view, nil
	}
	return result, callErr
}

const unknownEffectObservation = `{"status":"unknown","replay_blocked":true}`

func toolGuardError(err error) bool {
	for _, guard := range []error{context.Canceled, context.DeadlineExceeded, domain.ErrInvalid, domain.ErrUnauthorized, domain.ErrNotFound, domain.ErrConflict, domain.ErrStaleClaim, domain.ErrStaleSource, domain.ErrTaskBusy, domain.ErrDeliveryPending} {
		if errors.Is(err, guard) {
			return true
		}
	}
	return false
}

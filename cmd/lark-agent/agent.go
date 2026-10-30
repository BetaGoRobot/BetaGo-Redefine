package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/accounts"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/engine"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/lark"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musiccards"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/participation"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/web"
)

type retrievalModel struct {
	*modelapi.Embedder
	*modelapi.Reranker
}

type groupRuntime struct {
	ingress   *lark.Ingress
	service   *service.Service
	handler   http.Handler
	websocket bool
}

func participating(ctx context.Context, c config.Config, listen string) error {
	parts, err := initialize(ctx, c)
	if err != nil {
		return err
	}
	defer parts.close()
	rt, err := assembleAgent(c, parts)
	if err != nil {
		return err
	}
	return rt.run(ctx, listen)
}

func assembleAgent(c config.Config, p *components) (*groupRuntime, error) {
	if p == nil || p.db == nil || p.store == nil || p.lark == nil {
		return nil, errors.New("agent components missing")
	}
	r, err := retrieval.New(retrieval.Options{Sources: p.store, Search: p.index, Memory: p.memory, Model: retrievalModel{p.embed, p.rank}})
	if err != nil {
		return nil, errors.New("retrieval initialization failed")
	}
	// Conversation replies are the final output of the model and are delivered
	// once by the durable delivery service. Exposing a separate send tool makes
	// a normal greeting look like an approval-gated business action and produces
	// a second, receipt-shaped answer after it runs.
	opts := atoms.Options{Sources: p.store, Retrieval: r, Tasks: p.store, Group: p.lark, Governance: p.store, WebPages: web.NewReader()}
	opts.Extensions, err = capabilityDefinitions(c, p)
	if err != nil {
		return nil, errors.New("business capabilities initialization failed")
	}
	opts.Extensions = append(opts.Extensions, atoms.PresentationDefinitions(atoms.PresentationOptions{Messenger: p.lark, Retractor: p.lark, Sources: p.store, Tasks: p.store})...)
	opts.Extensions = append(opts.Extensions, atoms.ResearchDefinitions(opts)...)
	if c.Models.WebSearch.Model != "" {
		search, err := modelapi.NewWebSearch(endpoint(c.Models.WebSearch))
		if err != nil {
			return nil, errors.New("public web search initialization failed")
		}
		opts.WebSearch = search
	}
	for _, v := range c.APIs {
		opts.APIs = append(opts.APIs, atoms.APIConfig{Name: v.Name, Description: v.Description, URL: v.URL, Method: v.Method, Headers: v.Headers, Parameters: json.RawMessage(v.Parameters), ReadOnly: v.ReadOnly, SideEffect: v.SideEffect, AutoExecute: v.AutoExecute, Timeout: time.Duration(v.TimeoutSeconds) * time.Second})
	}
	for _, v := range c.MCPs {
		opts.MCPs = append(opts.MCPs, atoms.MCPConfig{Name: v.Name, Description: v.Description, ServerURL: v.ServerURL, ToolName: v.ToolName, Headers: v.Headers, Parameters: json.RawMessage(v.Parameters), ReadOnly: v.ReadOnly, SideEffect: v.SideEffect, AutoExecute: v.AutoExecute, Timeout: time.Duration(v.TimeoutSeconds) * time.Second})
	}
	registry, err := atoms.New(opts)
	if err != nil {
		return nil, errors.New("atomic tools initialization failed")
	}
	chat, err := modelapi.NewChat(endpoint(c.Models.Answer))
	if err != nil {
		return nil, errors.New("answer model initialization failed")
	}
	e, err := engine.New(engine.Options{Store: p.store, Model: chat, Registry: registry, MemeReferences: p.memeReferences, MemeRevalidate: p.memeRevalidate, GroupMembers: p.lark, BotOpenID: c.Lark.BotOpenID, ConversationOutput: true, MaxIterations: c.Runtime.MaxTurns, MaxContextBytes: c.Runtime.MaxContextBytes, ResolveMessage: sourceResolver(p)})
	if err != nil {
		return nil, errors.New("native agent initialization failed")
	}
	runID, err := owner()
	if err != nil {
		return nil, err
	}
	var presentation func(context.Context, domain.Job) error
	if p.musicService != nil {
		presentation = p.musicService.Process
	}
	svc, err := service.New(service.Options{Presentation: presentation, Store: p.store, Engine: e, Messenger: p.lark, ScheduleRender: scheduleRenderer(registry, p.store), Owner: runID, Lease: time.Duration(c.Runtime.TurnLeaseSeconds) * time.Second, Poll: time.Duration(c.Worker.PollMilliseconds) * time.Millisecond, TaskTimeout: time.Duration(c.Runtime.TaskTimeoutSeconds) * time.Second, Concurrency: c.Runtime.MaxConcurrentTasks, MaxConcurrentTasks: c.Runtime.MaxConcurrentTasks, EligibleForConversation: participation.EligibleForConversation})
	if err != nil {
		return nil, errors.New("group task service initialization failed")
	}
	ingress, err := lark.NewIngress(lark.IngressConfig{Normalize: lark.NormalizeConfig{AppID: c.Lark.AppID, BotOpenID: c.Lark.BotOpenID, TenantID: c.App.TenantID, AllowedChatIDs: c.App.AllowedChatIDs}, AppSecret: c.Lark.AppSecret, VerificationToken: c.Lark.VerificationToken, EncryptionKey: c.Lark.EncryptionKey, MusicCardAction: func(ctx context.Context, a lark.MusicCardActionSubmission) error {
		if p.musicService == nil {
			return domain.ErrUnauthorized
		}
		return p.musicService.HandleAction(ctx, musiccards.ActionInvocation{Scope: a.Scope, ActorID: a.ActorID, EventID: a.EventID, MessageID: a.MessageID, Token: a.Token})
	}, LuckinBinding: func(ctx context.Context, submission lark.CredentialBindingSubmission) error {
		if p.accounts == nil {
			return domain.ErrUnauthorized
		}
		return p.accounts.CompleteBinding(ctx, accounts.Principal{Scope: submission.Scope, ActorID: submission.ActorID}, submission.BindingID, submission.MessageID, submission.Token, submission.Revoke)
	}, Persist: func(ctx context.Context, e domain.Event) error {
		accepted, err := p.store.Accept(ctx, e)
		if err != nil {
			return telemetry.Wrap("ingress.persist", "database_persistence_failed", err)
		}
		telemetry.Info(ctx, "agent.ingress.persisted", "duplicate", accepted.Duplicate, "current", accepted.Current, "queued_jobs", len(accepted.JobIDs))
		return nil
	}})
	if err != nil {
		return nil, errors.New("Lark ingress initialization failed")
	}
	rt := &groupRuntime{ingress: ingress, service: svc, websocket: c.Lark.Transport == "websocket"}
	mux := http.NewServeMux()
	if p.musicMedia != nil {
		mux.Handle("/music/assets/", p.musicMedia.Handler())
	}
	mux.Handle("/api/", taskAPI(c, p.store))
	var ready func() bool
	if rt.websocket {
		ready = ingress.Ready
	} else {
		mux.Handle("/lark/events", ingress.Handler())
	}
	mux.Handle("/", readyHandler(p.db, ready))
	rt.handler = mux
	telemetry.Info(context.Background(), "agent.business.ready", "transport", c.Lark.Transport, "tools", len(registry.Definitions()))
	return rt, nil
}

// Reading a provider message never creates a fresh participation event. Only a
// strictly newer provider version is accepted into the durable refresh path.
func sourceRefresher(p *components) func(context.Context, domain.SourceRef) error {
	resolve := sourceResolver(p)
	return func(ctx context.Context, ref domain.SourceRef) error {
		return resolve(ctx, ref.Scope, ref.MessageID)
	}
}
func sourceResolver(p *components) func(context.Context, domain.Scope, string) error {
	return func(ctx context.Context, scope domain.Scope, id string) error {
		if strings.HasPrefix(id, "card:") {
			return nil
		}
		m, err := p.lark.ReadMessage(ctx, scope, id)
		if err != nil {
			return err
		}
		current, err := p.store.Message(ctx, scope, id)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if err == nil && m.Source.Version <= current.Source.Version {
			return nil
		}
		typ := domain.EventMessage
		if m.Deleted {
			typ = domain.EventDelete
		}
		_, err = p.store.Accept(ctx, domain.Event{ID: m.EventID, Type: typ, Message: m, OccurredAt: m.UpdatedAt})
		return err
	}
}

func (r *groupRuntime) run(ctx context.Context, listen string) error {
	runners := []func(context.Context) error{r.service.Run, func(ctx context.Context) error { return serve(ctx, listen, r.handler) }}
	if r.websocket {
		runners = append(runners, r.ingress.Start)
	}
	if err := runServices(ctx, runners...); err != nil {
		return errors.New("group agent service stopped")
	}
	return nil
}

// All children share cancellation, and every started child is joined before
// returning so database handles cannot close underneath a running worker.
func runServices(ctx context.Context, runners ...func(context.Context) error) error {
	if len(runners) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, len(runners))
	for _, run := range runners {
		go func() { done <- run(ctx) }()
	}
	first := <-done
	cancel()
	for range len(runners) - 1 {
		<-done
	}
	if errors.Is(first, context.Canceled) {
		return nil
	}
	return first
}

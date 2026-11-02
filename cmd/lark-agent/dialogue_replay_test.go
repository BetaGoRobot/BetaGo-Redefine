package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/accounts"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/engine"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
)

// TestNextDialogueModelReplay is deliberately opt-in. Only answer-model HTTP
// leaves this process. Every source, delivery, reaction and business adapter is
// test-owned; the real native Eino loop and PostgreSQL effect ledger are used.
// NEXT_REPLAY_CONFIG loads models from an existing private config, or callers
// may inject NEXT_REPLAY_BASE_URL/API_KEY/MODEL/PROTOCOL/SERVICE_TIER directly.
// NEXT_REPLAY_TEST_DSN_FILE names the isolated fixture DSN file (preferred);
// AGENT_TEST_POSTGRES_DSN is also accepted. Only loopback PostgreSQL is allowed.
// NEXT_DIALOGUE_REPLAY_CONSTRUCT_ONLY=1 verifies native registry/PG assembly
// without calling the provider or running the dialogue scenarios.
func TestNextDialogueModelReplay(t *testing.T) {
	if os.Getenv("NEXT_DIALOGUE_MODEL_REPLAY") != "1" {
		t.Skip("set NEXT_DIALOGUE_MODEL_REPLAY=1 for isolated native real-model replay")
	}
	output := os.Getenv("NEXT_DIALOGUE_REPLAY_OUTPUT")
	if output == "" {
		t.Fatal("NEXT_DIALOGUE_REPLAY_OUTPUT is required")
	}
	ep := replayEndpoint(t)
	dsn := replayDSN(t)
	report := replayReport{Version: 1, Model: ep.Model, StartedAt: time.Now().UTC(), ToolCount: 74, ConstructionOnly: os.Getenv("NEXT_DIALOGUE_REPLAY_CONSTRUCT_ONLY") == "1", Scenarios: []*replayScenario{}}
	t.Cleanup(func() {
		report.FinishedAt = time.Now().UTC()
		report.Passed = !t.Failed()
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil || os.WriteFile(output, append(b, '\n'), 0600) != nil {
			t.Error("cannot write replay report")
		}
	})
	tests := []struct {
		name string
		run  func(*testing.T, *replayFixture)
	}{
		{"tcc_question", func(t *testing.T, f *replayFixture) {
			f.human(t, "member", "现在 Devflow 发布个 TCC 也太久了，TCC 也单机房", false)
			turn := f.turn(t, "你知道什么是 TCC 吗？", true)
			f.wantReply(t, turn)
			text := strings.ToLower(replayText(turn.Plan))
			relevant := false
			for _, term := range []string{"配置", "字节", "基建", "发布", "机房", "不确定", "不清楚", "哪种", "具体指", "不敢确定"} {
				relevant = relevant || strings.Contains(text, term)
			}
			if !relevant {
				t.Error("TCC answer neither explains the term nor honestly clarifies uncertainty")
			}
			f.wantReply(t, f.turn(t, "你理解错了，TCC 是字节跳动的基建", true))
		}},
		{"three_known_routes", func(t *testing.T, f *replayFixture) {
			f.human(t, "alice", "这个上下文方案就三路：关系链、情感链，加关键元素。", false)
			f.human(t, "member", "对，就按刚说的这三路整理。", false)
			turn := f.turn(t, "哪三路？", true)
			f.wantReply(t, turn)
			text := replayText(turn.Plan)
			for _, term := range []string{"关系链", "情感链", "关键元素"} {
				if !strings.Contains(text, term) {
					t.Errorf("answer omitted visible route %q", term)
				}
			}
			if len(turn.Tools) != 0 {
				t.Error("visible antecedent unnecessarily triggered tools")
			}
		}},
		{"ambient_reply_frequency", func(t *testing.T, f *replayFixture) {
			for i, text := range []string{"这锅今天又轮到你背了", "你这进度条比下载器还会装忙", "行，先让它跑一会儿", "刚说完它就报错，挺会挑时候"} {
				f.seedReply(t, fmt.Sprintf("看看刚才这个情况 %d", i), text)
			}
			turn := f.turn(t, "他回复频率怎么这么高", false)
			f.wantObserve(t, turn)
		}},
		{"ambient_repeated_touch", func(t *testing.T, f *replayFixture) {
			f.seedReply(t, "摸摸摸", "再摸就包浆了")
			for _, author := range []string{"alice", "member", "alice", "member"} {
				f.human(t, author, "摸摸摸", false)
			}
			f.wantObserve(t, f.turn(t, "摸摸摸", false))
		}},
		{"luckin_setup_and_explanation", func(t *testing.T, f *replayFixture) {
			first := f.turn(t, "我要喝瑞幸", true)
			f.wantReply(t, first)
			if f.bindingCalls != 1 || len(f.private.bindings) != 1 {
				t.Error("ordering intent did not ensure one actual private binding form")
			}
			f.wantBindingReceipt(t, first, "sent")
			originalMessage := ""
			if len(f.private.bindings) > 0 {
				originalMessage = f.private.bindings[0].MessageID
			}
			why := f.turn(t, "我需要知道原因", true)
			f.wantReply(t, why)
			if err := replayBindingFollowupError(why, originalMessage, "reused"); err != nil {
				t.Errorf("explanation request repeated binding delivery: %v", err)
			}
			f.completeBinding(t)
			again := f.turn(t, "为什么又发了一次？我已绑定", true)
			f.wantReply(t, again)
			if err := replayBindingFollowupError(again, originalMessage, "already_bound"); err != nil {
				t.Errorf("duplicate-send feedback repeated binding delivery: %v", err)
			}
		}},
		{"luckin_already_bound", func(t *testing.T, f *replayFixture) {
			f.bindAccount(t)
			turn := f.turn(t, "我要喝瑞幸", true)
			f.wantReply(t, turn)
			if f.bindingCalls != 1 || len(f.private.bindings) != 0 {
				t.Error("bound actor should be checked without sending a new form")
			}
			f.wantBindingReceipt(t, turn, "already_bound")
		}},
		{"direct_greeting", func(t *testing.T, f *replayFixture) {
			f.wantReply(t, f.turn(t, "早啊", true))
		}},
		{"grounded_explicit_roast", func(t *testing.T, f *replayFixture) {
			f.human(t, "alice", "我昨天说五分钟上线，结果修到凌晨三点才发现改错分支了", false)
			f.wantReply(t, f.turn(t, "吐槽一下他这五分钟上线", true))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			scenario := &replayScenario{Name: test.name, Turns: []*replayTurn{}}
			report.Scenarios = append(report.Scenarios, scenario)
			t.Cleanup(func() { scenario.Passed = !t.Failed() })
			f := newReplayFixture(t, ctx, dsn, ep, scenario)
			t.Cleanup(func() {
				scenario.ModelCalls = f.model.calls
				scenario.BindingCalls = f.bindingCalls
				scenario.PrivateForms = len(f.private.bindings)
				scenario.Reactions = f.group.reactions
			})
			if report.SchemaDigest == "" {
				report.SchemaDigest = f.model.schemaDigest
			}
			if report.ConstructionOnly {
				return
			}
			test.run(t, f)
		})
	}
}

type replayReport struct {
	Version                  int
	Model, SchemaDigest      string
	StartedAt, FinishedAt    time.Time
	ToolCount                int
	Passed, ConstructionOnly bool
	Scenarios                []*replayScenario
}

type replayScenario struct {
	Name                                              string
	Passed                                            bool
	ModelCalls, BindingCalls, PrivateForms, Reactions int
	Turns                                             []*replayTurn
}

type replayTurn struct {
	MessageID, Input, Error                           string
	ParentID                                          string `json:",omitempty"`
	Addressed                                         bool
	Plan                                              *domain.ConversationPlan
	ModelReplies                                      []modelapi.ChatMessage
	Tools                                             []string
	ActionsBefore                                     []pgstore.ActionReceipt
	ActionsAfter                                      []pgstore.ActionReceipt
	BindingCalls, PrivateForms, Reactions, Deliveries int
	RuntimeRetries                                    int
}

func replayEndpoint(t *testing.T) modelapi.Endpoint {
	t.Helper()
	var e modelapi.Endpoint
	if path := os.Getenv("NEXT_REPLAY_CONFIG"); path != "" {
		c, err := config.Load(path, "models")
		if err != nil {
			t.Fatal("private replay model config invalid")
		}
		e = endpoint(c.Models.Answer)
	} else {
		e = modelapi.Endpoint{BaseURL: os.Getenv("NEXT_REPLAY_BASE_URL"), APIKey: os.Getenv("NEXT_REPLAY_API_KEY"), Model: os.Getenv("NEXT_REPLAY_MODEL"), Protocol: os.Getenv("NEXT_REPLAY_PROTOCOL"), ServiceTier: os.Getenv("NEXT_REPLAY_SERVICE_TIER")}
	}
	if e.Protocol == "" {
		e.Protocol = "openai"
	}
	u, err := url.Parse(e.BaseURL)
	if err != nil || u.Host == "" || e.APIKey == "" || e.Model == "" {
		t.Fatal("inject a complete private answer endpoint")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	e.Timeout = 45 * time.Second
	e.HTTPClient = &http.Client{Transport: replayModelTransport{origin: u.Scheme + "://" + u.Host, path: strings.TrimRight(u.Path, "/") + "/chat/completions", next: transport}, Timeout: 45 * time.Second}
	return e
}

type replayModelTransport struct {
	origin, path string
	next         http.RoundTripper
}

func (r replayModelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.URL.Scheme+"://"+req.URL.Host != r.origin || req.URL.Path != r.path || req.URL.RawQuery != "" {
		return nil, errors.New("replay blocked non-model HTTP")
	}
	return r.next.RoundTrip(req)
}

func replayDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if path := os.Getenv("NEXT_REPLAY_TEST_DSN_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("cannot read isolated replay PostgreSQL DSN")
		}
		dsn = strings.TrimSpace(string(b))
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		t.Fatal("replay requires an explicit loopback isolated PostgreSQL DSN")
	}
	return dsn
}

type replayModel struct {
	chat         *modelapi.Chat
	seed         *domain.ConversationPlan
	active       *replayTurn
	calls        int
	schemaDigest string
}

func (m *replayModel) Generate(ctx context.Context, input modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	if m.seed != nil {
		b, _ := json.Marshal(m.seed)
		return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: string(b)}}, nil
	}
	if m.calls >= 8 {
		return modelapi.ChatResponse{}, errors.New("replay scenario model ceiling")
	}
	if len(input.Tools) != 74 {
		return modelapi.ChatResponse{}, errors.New("replay lost full tool schema set")
	}
	m.calls++
	input.MaxTokens = 512
	response, err := m.chat.Generate(ctx, input)
	if err != nil {
		return modelapi.ChatResponse{}, errors.New("replay answer model call failed")
	}
	if m.active != nil {
		m.active.ModelReplies = append(m.active.ModelReplies, response.Message)
		for _, call := range response.Message.ToolCalls {
			m.active.Tools = append(m.active.Tools, call.Function.Name)
		}
	}
	return response, nil
}

type replayFixture struct {
	ctx          context.Context
	db           *sql.DB
	schema       string
	store        *pgstore.Store
	accounts     *accounts.Store
	scope        domain.Scope
	svc          *service.Service
	model        *replayModel
	sender       *replaySender
	private      *replayPrivateSender
	group        *replayGroup
	scenario     *replayScenario
	currentID    string
	replyParent  string
	messageCount int
	bindingCalls int
}

func newReplayFixture(t *testing.T, ctx context.Context, dsn string, ep modelapi.Endpoint, scenario *replayScenario) *replayFixture {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal("cannot open isolated replay PostgreSQL")
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	schema := "dialogue_replay_" + hex.EncodeToString(nonce[:])
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanup, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Error("cannot remove owned replay schema")
		}
		db.Close()
	})
	store, err := pgstore.New(db, schema)
	if err != nil || store.Migrate(ctx) != nil {
		t.Fatal("cannot migrate isolated replay PostgreSQL")
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	accountStore, err := accounts.New(db, schema, key[:])
	if err != nil || accountStore.Migrate(ctx) != nil {
		t.Fatal("cannot migrate isolated replay accounts")
	}
	chat, err := modelapi.NewChat(ep)
	if err != nil {
		t.Fatal("invalid replay answer model")
	}
	f := &replayFixture{ctx: ctx, db: db, schema: schema, store: store, accounts: accountStore, scope: domain.Scope{TenantID: "replay-tenant", AppID: "replay-app", ChatID: scenario.Name, Visibility: "group"}, model: &replayModel{chat: chat}, sender: &replaySender{}, private: &replayPrivateSender{}, scenario: scenario}
	f.group = &replayGroup{store: store}
	registry := f.registry(t)
	tools := []modelapi.ToolDefinition{}
	for _, d := range registry.Definitions() {
		tools = append(tools, modelapi.ToolDefinition{Type: "function", Function: modelapi.FunctionDefinition{Name: d.Name, Description: d.Description, Parameters: d.Parameters}})
	}
	b, _ := json.Marshal(tools)
	f.model.schemaDigest = domain.StableID(string(b))
	native, err := engine.New(engine.Options{Store: store, Model: f.model, Registry: registry, GroupMembers: f.group, BotOpenID: "replay-bot", ConversationOutput: true, MaxIterations: 8, MaxContextBytes: 131072})
	if err != nil {
		t.Fatal("cannot initialize native replay engine")
	}
	f.svc, err = service.New(service.Options{Store: store, Engine: native, Messenger: f.sender, Owner: "dialogue-replay", Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: 5 * time.Minute, EligibleForConversation: func(_ context.Context, m domain.Message) (bool, error) { return m.Source.MessageID == f.currentID, nil }})
	if err != nil {
		t.Fatal("cannot initialize native replay service")
	}
	return f
}

func (f *replayFixture) registry(t *testing.T) *atoms.Registry {
	t.Helper()
	opts := atoms.Options{Sources: f.store, Tasks: f.store, Group: f.group, Governance: f.store, Retrieval: replayRetriever{}}
	base, err := atoms.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]atoms.Definition{}
	for _, d := range base.Definitions() {
		known[d.Name] = d
	}
	manager := &business.ManagedLuckinAccounts{Store: f.accounts, Sender: f.private}
	luckin, err := business.NewLuckinClient(business.LuckinConfig{ServerURL: "https://replay.invalid/luckin", BindingURL: "https://replay.invalid/bind", SigningKey: make([]byte, 32), Credentials: manager})
	if err != nil {
		t.Fatal(err)
	}
	businessDefs, err := atoms.BusinessDefinitions(atoms.BusinessOptions{Luckin: luckin, LuckinBinding: replayBindingManager{f: f, manager: manager}})
	if err != nil {
		t.Fatal(err)
	}
	var binding atoms.Definition
	for _, d := range businessDefs {
		if d.Name == "luckin_account_bind" {
			binding = d
		}
	}
	full := inventoryRegistry(t, true).Definitions()
	if len(full) != 74 {
		t.Fatal("full production tool inventory changed")
	}
	for _, original := range full {
		if _, ok := known[original.Name]; ok {
			continue
		}
		// Copy only exported schema/policy fields: inventory closures, source
		// resolvers and authorization hooks may never enter this runtime.
		d := atoms.Definition{Name: original.Name, Description: original.Description, Parameters: original.Parameters, SideEffect: original.SideEffect, AutoExecute: original.AutoExecute, PerActorTurn: original.PerActorTurn, Run: func(context.Context, atoms.Invocation, string) (string, error) {
			return `{"available":false,"reason":"isolated_replay_has_no_external_data","data":[]}`, nil
		}}
		if d.Name == "luckin_account_bind" {
			d.Run = binding.Run
		}
		opts.Extensions = append(opts.Extensions, d)
	}
	registry, err := atoms.New(opts)
	if err != nil {
		t.Fatal("cannot assemble full local replay registry")
	}
	actual := registry.Definitions()
	if len(actual) != len(full) {
		t.Fatal("replay registry lost tools")
	}
	for i, d := range actual {
		want := full[i]
		if d.Name != want.Name || d.Description != want.Description || replaySchemaCanonical(t, d.Parameters) != replaySchemaCanonical(t, want.Parameters) || d.SideEffect != want.SideEffect || d.AutoExecute != want.AutoExecute || d.PerActorTurn != want.PerActorTurn {
			t.Fatalf("replay schema/policy diverged for %s", d.Name)
		}
	}
	return registry
}

// schedule_create/update compose query alternatives in extension registration
// order. A oneOf permutation preserves validation, including duplicate-branch
// rejection, while sorting extensions changes that order. Normalize only these
// unordered schema alternatives; retain every branch and every other field.
// atoms.New independently resolves and validates both schemas before comparison.
func replaySchemaCanonical(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal("invalid replay tool schema JSON")
	}
	var normalize func(any)
	normalize = func(node any) {
		switch node := node.(type) {
		case map[string]any:
			for key, child := range node {
				normalize(child)
				if key == "oneOf" {
					if branches, ok := child.([]any); ok {
						sort.SliceStable(branches, func(i, j int) bool {
							a, _ := json.Marshal(branches[i])
							b, _ := json.Marshal(branches[j])
							return string(a) < string(b)
						})
					}
				}
			}
		case []any:
			for _, child := range node {
				normalize(child)
			}
		}
	}
	normalize(value)
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal("cannot canonicalize replay tool schema")
	}
	return string(b)
}

type replayBindingManager struct {
	f       *replayFixture
	manager *business.ManagedLuckinAccounts
}

func (m replayBindingManager) RequestBinding(ctx context.Context, identity business.Identity, manage bool) (business.BindingReceipt, error) {
	m.f.bindingCalls++
	return m.manager.RequestBinding(ctx, identity, manage)
}

func (f *replayFixture) human(t *testing.T, author, text string, addressed bool) domain.Message {
	t.Helper()
	f.messageCount++
	id := fmt.Sprintf("message-%02d", f.messageCount)
	now := time.Now().UTC().Add(-time.Millisecond)
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: addressed, MessageType: "text"})
	m := domain.Message{Source: domain.SourceRef{Scope: f.scope, MessageID: id, AuthorID: author, Version: 1, OccurredAt: now}, EventID: id, ParentID: f.replyParent, Text: text, UpdatedAt: now, Raw: raw}
	if _, err := f.store.Accept(f.ctx, domain.Event{ID: id, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
		t.Fatal("cannot accept replay input")
	}
	if err := f.svc.Step(f.ctx, domain.JobRoute); err != nil {
		t.Fatal("cannot route replay input")
	}
	return m
}

func (f *replayFixture) seedReply(t *testing.T, input, answer string) {
	t.Helper()
	id := fmt.Sprintf("message-%02d", f.messageCount+1)
	f.model.seed = &domain.ConversationPlan{Choice: "reply", Messages: []domain.ConversationMessage{{Text: answer, ReplyToMessageID: id}}}
	f.human(t, "member", input, true)
	if err := f.svc.Step(f.ctx, domain.JobAgentTurn); err != nil {
		t.Fatal("cannot create owned seed reply")
	}
	f.drainDeliveries(t)
	f.model.seed = nil
}

func (f *replayFixture) turn(t *testing.T, text string, addressed bool) *replayTurn {
	t.Helper()
	f.currentID = fmt.Sprintf("message-%02d", f.messageCount+1)
	turn := &replayTurn{MessageID: f.currentID, Input: text, ParentID: f.replyParent, Addressed: addressed, Tools: []string{}, ModelReplies: []modelapi.ChatMessage{}}
	f.scenario.Turns = append(f.scenario.Turns, turn)
	f.model.active = turn
	turn.ActionsBefore, _ = f.store.RecentActionReceipts(f.ctx, f.scope, "member", time.Now().UTC(), 8)
	sends, private, reactions, binds := len(f.sender.sent), len(f.private.bindings), f.group.reactions, f.bindingCalls
	m := f.human(t, "member", text, addressed)
	for {
		err := f.svc.Step(f.ctx, domain.JobAgentTurn)
		if err == nil {
			break
		}
		if errors.Is(err, engine.ErrConversationOutput) && turn.RuntimeRetries < 2 {
			// Step has already persisted RetryJob's native backoff. Do not
			// replace the job, edit available_at, or accept malformed output.
			turn.RuntimeRetries++
		} else if !(turn.RuntimeRetries > 0 && errors.Is(err, domain.ErrNotFound)) {
			turn.Error = "native_turn_failed"
			t.Fatal("native replay turn failed; inspect captured model replies in report")
		}
		// No claim while the durable retry is pending is expected. Keep the
		// same scenario deadline and provider ceiling across every attempt.
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-f.ctx.Done():
			timer.Stop()
			turn.Error = "native_retry_deadline"
			t.Fatal("native replay retry exceeded scenario deadline")
		case <-timer.C:
		}
	}
	f.drainDeliveries(t)
	task, _, err := f.store.TaskForMessage(f.ctx, f.scope, m.Source.MessageID)
	if err != nil || task.FinalConversation == nil {
		turn.Error = "no_durable_conversation_plan"
		t.Fatal("native replay did not persist a conversation plan")
	}
	turn.Plan = task.FinalConversation
	turn.ActionsAfter, err = f.store.RecentActionReceipts(f.ctx, f.scope, "member", time.Now().UTC(), 8)
	if err != nil {
		t.Fatal("cannot read native action receipts")
	}
	turn.BindingCalls = f.bindingCalls - binds
	turn.PrivateForms = len(f.private.bindings) - private
	turn.Reactions = f.group.reactions - reactions
	turn.Deliveries = len(f.sender.sent) - sends
	if task.Status != "completed" {
		t.Errorf("native task ended in %s", task.Status)
	}
	f.model.active = nil
	return turn
}

func (f *replayFixture) drainDeliveries(t *testing.T) {
	t.Helper()
	for i := 0; i < domain.MaxConversationMessages+2; i++ {
		err := f.svc.Step(f.ctx, domain.JobDelivery)
		if errors.Is(err, domain.ErrNotFound) {
			return
		}
		if err != nil {
			t.Fatal("cannot deliver local replay reply")
		}
	}
	t.Fatal("replay delivery ceiling exceeded")
}

func (f *replayFixture) wantReply(t *testing.T, turn *replayTurn) {
	t.Helper()
	if turn.Plan == nil || turn.Plan.Choice == "observe" || strings.TrimSpace(replayText(turn.Plan)) == "" || turn.Deliveries != 1 {
		t.Error("direct request needs one durable relevant reply")
	}
}

func (f *replayFixture) wantObserve(t *testing.T, turn *replayTurn) {
	t.Helper()
	if turn.Plan == nil || turn.Plan.Choice != "observe" || turn.Deliveries != 0 || turn.Reactions != 0 || len(turn.Tools) != 0 {
		t.Error("ambient exhausted interaction should observe without reaction or tools")
	}
}

func (f *replayFixture) wantBindingReceipt(t *testing.T, turn *replayTurn, outcome string) {
	t.Helper()
	for _, action := range turn.ActionsAfter {
		if action.Tool == "luckin_account_bind" && action.Status == "completed" && action.OriginMessageID == turn.MessageID && action.Result["outcome"] == outcome {
			return
		}
	}
	t.Errorf("native effect ledger lacks completed binding outcome %s", outcome)
}

// The default bind tool also verifies current status. A fresh status read is
// safe only when its actual receipt proves no new form was sent; conversation
// history alone cannot establish the actor's current account status.
func replayBindingFollowupError(turn *replayTurn, originalMessage, outcome string) error {
	if turn.PrivateForms != 0 || turn.BindingCalls < 0 || turn.BindingCalls > 1 {
		return errors.New("new form or more than one account check")
	}
	for _, reply := range turn.ModelReplies {
		for _, call := range reply.ToolCalls {
			if call.Function.Name != "luckin_account_bind" {
				continue
			}
			var arguments struct {
				Manage bool `json:"manage"`
			}
			if json.Unmarshal([]byte(call.Function.Arguments), &arguments) != nil || arguments.Manage {
				return errors.New("followup requested account management")
			}
		}
	}
	if turn.BindingCalls == 0 {
		return nil
	}
	if outcome != "reused" && outcome != "already_bound" {
		return errors.New("unsupported no-send account outcome")
	}
	for _, action := range turn.ActionsAfter {
		if action.Tool != "luckin_account_bind" || action.OriginMessageID != turn.MessageID {
			continue
		}
		if action.Status != "completed" || action.Result["outcome"] != outcome {
			return errors.New("account check lacks completed no-send receipt")
		}
		message, _ := action.Result["message_id"].(string)
		if (outcome == "reused" && (originalMessage == "" || message != originalMessage)) || (outcome == "already_bound" && message != "") {
			return errors.New("account check returned a new message receipt")
		}
		return nil
	}
	return errors.New("account check has no current-turn receipt")
}

func TestNextDialogueReplayBindingFollowupAcceptsOnlyNoSendReceipts(t *testing.T) {
	for _, test := range []struct {
		name, outcome, message, expected, arguments string
		calls, forms                                int
		missing                                     bool
		wantError                                   bool
	}{
		{name: "explain_without_tool"},
		{name: "reuse_original", outcome: "reused", message: "original", expected: "reused", arguments: `{}`, calls: 1},
		{name: "check_bound", outcome: "already_bound", expected: "already_bound", arguments: `{"manage":false}`, calls: 1},
		{name: "fresh_form", outcome: "sent", message: "new", expected: "reused", arguments: `{}`, calls: 1, forms: 1, wantError: true},
		{name: "sent_receipt_without_form", outcome: "sent", message: "new", expected: "reused", arguments: `{}`, calls: 1, wantError: true},
		{name: "reused_other_form", outcome: "reused", message: "new", expected: "reused", arguments: `{}`, calls: 1, wantError: true},
		{name: "bound_with_message", outcome: "already_bound", message: "original", expected: "already_bound", arguments: `{}`, calls: 1, wantError: true},
		{name: "management_request", outcome: "reused", message: "original", expected: "reused", arguments: `{"manage":true}`, calls: 1, wantError: true},
		{name: "repeated_check", outcome: "reused", message: "original", expected: "reused", arguments: `{}`, calls: 2, wantError: true},
		{name: "missing_receipt", expected: "reused", arguments: `{}`, calls: 1, missing: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			turn := &replayTurn{MessageID: "current", BindingCalls: test.calls, PrivateForms: test.forms}
			if test.arguments != "" {
				turn.ModelReplies = []modelapi.ChatMessage{{ToolCalls: []modelapi.ToolCall{{Function: modelapi.ToolFunction{Name: "luckin_account_bind", Arguments: test.arguments}}}}}
			}
			if !test.missing {
				result := map[string]any{"outcome": test.outcome}
				if test.message != "" {
					result["message_id"] = test.message
				}
				turn.ActionsAfter = []pgstore.ActionReceipt{{Tool: "luckin_account_bind", Status: "completed", OriginMessageID: turn.MessageID, Result: result}}
			}
			if err := replayBindingFollowupError(turn, "original", test.expected); (err != nil) != test.wantError {
				t.Fatalf("no-send acceptance error=%v, wantError=%v", err, test.wantError)
			}
		})
	}
}

func replayText(plan *domain.ConversationPlan) string {
	if plan == nil {
		return ""
	}
	text := plan.Summary
	for _, message := range plan.Messages {
		text += "\n" + message.Text
	}
	return text
}

func (f *replayFixture) bindAccount(t *testing.T) {
	t.Helper()
	p := accounts.Principal{Scope: f.scope, ActorID: "member"}
	b, err := f.accounts.BeginBinding(f.ctx, p, "test-owned-initial-account")
	if err != nil || f.accounts.SetBindingMessage(f.ctx, p, b.ID, "fixture-initial-binding") != nil || f.accounts.CompleteBinding(f.ctx, p, b.ID, "fixture-initial-binding", "replay-fixture-credential", false) != nil {
		t.Fatal("cannot seed test-owned bound account")
	}
}

func (f *replayFixture) completeBinding(t *testing.T) {
	t.Helper()
	if len(f.private.bindings) == 0 {
		t.Fatal("no actual private binding form to complete")
	}
	b := f.private.bindings[len(f.private.bindings)-1]
	if err := f.accounts.CompleteBinding(f.ctx, accounts.Principal{Scope: f.scope, ActorID: "member"}, b.ID, b.MessageID, "replay-fixture-credential", false); err != nil {
		t.Fatal("cannot complete test-owned binding form")
	}
}

type replayPrivateSender struct{ bindings []accounts.Binding }

func (s *replayPrivateSender) SendLuckinBinding(_ context.Context, _ domain.Scope, _, id string) (string, error) {
	message := fmt.Sprintf("replay-private-%d", len(s.bindings)+1)
	s.bindings = append(s.bindings, accounts.Binding{ID: id, MessageID: message})
	return message, nil
}

type replaySender struct{ sent, updated []domain.Delivery }

func (s *replaySender) Send(_ context.Context, scope domain.Scope, parent, kind, content, uuid string) (string, error) {
	id := "replay-bot-" + uuid
	s.sent = append(s.sent, domain.Delivery{Scope: scope, ParentID: parent, MessageType: kind, Content: content, UUID: uuid, MessageID: id})
	return id, nil
}

func (s *replaySender) UpdateCard(_ context.Context, scope domain.Scope, id, content, uuid string, sequence int) error {
	s.updated = append(s.updated, domain.Delivery{Scope: scope, MessageID: id, Content: content, UUID: uuid, Sequence: sequence})
	return nil
}

type replayRetriever struct{}

func (replayRetriever) Retrieve(context.Context, domain.Query) (retrieval.Result, error) {
	return retrieval.Result{}, nil
}

type replayGroup struct {
	store     *pgstore.Store
	reactions int
}

func (g *replayGroup) ReadMessage(ctx context.Context, scope domain.Scope, id string) (domain.Message, error) {
	return g.store.Message(ctx, scope, id)
}
func (*replayGroup) GroupInfo(context.Context, domain.Scope) (domain.GroupInfo, error) {
	return domain.GroupInfo{Name: "isolated dialogue replay", Description: "test-owned local adapters"}, nil
}
func (*replayGroup) GroupMembers(context.Context, domain.Scope, int, string) (domain.GroupMembers, error) {
	return domain.GroupMembers{Total: 4, Members: []domain.GroupMember{{ID: "member", Name: "小白", IDType: "open_id"}, {ID: "alice", Name: "阿明", IDType: "open_id"}, {ID: "replay-bot", Name: "BetaGo", IDType: "open_id"}}}, nil
}
func (*replayGroup) ListReactions(context.Context, domain.Scope, string, int, string) (domain.ReactionPage, error) {
	return domain.ReactionPage{Reactions: []domain.Reaction{}}, nil
}
func (g *replayGroup) AddReaction(_ context.Context, _ domain.Scope, _, emoji, _ string) (domain.Reaction, error) {
	g.reactions++
	return domain.Reaction{ID: fmt.Sprintf("replay-reaction-%d", g.reactions), Emoji: emoji, OperatorID: "replay-bot", OperatorType: "app", Own: true}, nil
}
func (*replayGroup) RemoveReaction(context.Context, domain.Scope, string, string, string) error {
	return nil
}
func (*replayGroup) SendImage(context.Context, domain.Scope, domain.Message, domain.Media, string, string) (string, error) {
	return "replay-local-image", nil
}

package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/engine"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/service"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mememilvus"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memeruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

// This opt-in probe runs the real query adapter, PG/Milvus retriever and native
// Eino final-reply engine. Only the final delivery/business adapters and fixture
// schemas are test-owned. The immutable public snapshot replaces the unpublished
// production sidecar; current visibility is deliberately NOT claimed as tested.
func TestMemeKnowledgeLiveNativeProbe(t *testing.T) {
	if os.Getenv("MEME_KNOWLEDGE_LIVE_PROBE") != "1" {
		t.Skip("explicit paid native validation only")
	}
	var input struct {
		Samples []knowledgeLiveCase `json:"samples"`
	}
	raw, err := os.ReadFile(os.Getenv("MEME_KNOWLEDGE_CASES"))
	if err != nil || json.Unmarshal(raw, &input) != nil || len(input.Samples) == 0 || len(input.Samples) > 8 {
		t.Fatal("invalid bounded probe cases")
	}
	c, err := config.Load("/etc/agent/agent.toml", "worker")
	if err != nil {
		t.Fatal("runtime config invalid: ", err)
	}
	settings, err := c.MemeSettings()
	if err != nil {
		t.Fatal("meme settings invalid")
	}
	var snapshot memes.KnowledgeSnapshot
	raw, err = os.ReadFile(os.Getenv("MEME_KNOWLEDGE_SNAPSHOT"))
	if err != nil || json.Unmarshal(raw, &snapshot) != nil {
		t.Fatal("snapshot input invalid")
	}
	prepared, err := memes.PrepareKnowledge(snapshot)
	if err != nil {
		t.Fatal("snapshot verification failed")
	}
	db, err := sql.Open("postgres", settings.DatabaseURL)
	if err != nil {
		t.Fatal("catalog unavailable")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	catalog, err := memestore.New(db, settings.Schema)
	if err != nil {
		t.Fatal("catalog configuration invalid")
	}
	scoped := catalog.ForScope(settings.Scope)
	manifest, err := scoped.Active(ctx)
	if err != nil || memestore.KnowledgeBinding(settings.Scope, manifest) != snapshot.Binding {
		t.Fatal("snapshot is not the current verified generation")
	}
	gate, err := catalog.Gate("ark:meme-reference", time.Duration(settings.StartIntervalMillis)*time.Millisecond, settings.MaxConcurrency)
	if err != nil {
		t.Fatal("query admission unavailable")
	}
	queryEP := endpoint(c.Models.Answer)
	queryEP.Model = settings.QueryModel
	queryEP.Timeout = time.Duration(settings.QueryTimeoutSeconds) * time.Second
	queryChat, err := modelapi.NewChat(queryEP)
	if err != nil {
		t.Fatal("query endpoint invalid")
	}
	embedder, err := newMemeReferenceEmbedder(c, settings)
	if err != nil {
		t.Fatal("embedding endpoint invalid")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if settings.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	index, err := mememilvus.New(mememilvus.Options{Endpoint: settings.SearchEndpoint, Username: settings.SearchUsername, Password: settings.SearchPassword, IndexPrefix: settings.IndexPrefix, Scope: settings.Scope, EmbeddingModel: settings.EmbeddingModel, Dimensions: settings.Dimensions, Embedder: memeReferenceEmbedder{embed: embedder, gate: gate, model: settings.EmbeddingModel}, HTTPClient: &http.Client{Transport: transport, Timeout: time.Duration(settings.QueryTimeoutSeconds) * time.Second}, Ledger: scoped, Records: scoped})
	if err != nil {
		t.Fatal("index initialization failed")
	}
	retriever, err := index.ForGeneration(manifest)
	if err != nil {
		t.Fatal("current index unavailable")
	}
	finalEP := endpoint(c.Models.Answer)
	dsn := replayDSN(t)
	report := knowledgeLiveReport{Version: 1, Model: finalEP.Model, QueryModel: settings.QueryModel, Binding: snapshot.Binding, KnowledgeFingerprint: snapshot.Fingerprint, KnowledgeEntries: len(snapshot.Entries), NativeToolCount: 74, PolicyExercised: false, HumanAcceptance: "pending independent review of first outputs; no model self-score", Rows: []*knowledgeLiveRow{}}
	save := func() {
		b, e := json.MarshalIndent(report, "", "  ")
		if e != nil || os.WriteFile(os.Getenv("MEME_KNOWLEDGE_OUTPUT"), append(b, '\n'), 0600) != nil {
			t.Error("cannot retain complete probe evidence")
		}
	}
	t.Cleanup(save)
	t.Logf("query_model=%s final_model=%s query_timeout_ms=%d final_timeout_ms=%d generation=%s entries=%d fingerprint=%s", settings.QueryModel, finalEP.Model, queryEP.Timeout.Milliseconds(), finalEP.Timeout.Milliseconds(), manifest.Generation, len(snapshot.Entries), snapshot.Fingerprint)
	if os.Getenv("MEME_KNOWLEDGE_PREFLIGHT") == "1" {
		_ = newReplayFixture(t, ctx, dsn, finalEP, &replayScenario{Name: "knowledge_preflight"})
		return
	}
	for _, sample := range input.Samples {
		t.Run(sample.ID, func(t *testing.T) {
			start := time.Now()
			row := &knowledgeLiveRow{ID: sample.ID, Origin: sample.Origin, Input: sample, Planner: []knowledgePlannerCall{}, Final: []knowledgeFinalCall{}}
			report.Rows = append(report.Rows, row)
			caseCtx, caseCancel := context.WithTimeout(ctx, 150*time.Second)
			defer caseCancel()
			scenario := &replayScenario{Name: "knowledge_" + sample.ID, Turns: []*replayTurn{}}
			f := newReplayFixture(t, caseCtx, dsn, finalEP, scenario)
			t.Cleanup(func() {
				row.ElapsedMS = time.Since(start).Milliseconds()
				row.Scenario = scenario
				scenario.Passed = !t.Failed()
				save()
			})
			// Historical bot text is persisted as a confirmed, test-owned delivery rather
			// than being misrepresented as a fresh human utterance.
			for i := 0; i < len(sample.Turns)-1; i++ {
				turn := sample.Turns[i]
				if i+1 < len(sample.Turns)-1 && sample.Turns[i+1].IsBot {
					f.seedReply(t, turn.Text, sample.Turns[i+1].Text)
					i++
					continue
				}
				if turn.IsBot {
					f.seedReply(t, "[历史窗口未含触发原话]", turn.Text)
					continue
				}
				f.human(t, "alice", turn.Text, false)
			}
			completion := &knowledgeRecordingCompletion{next: memeQueryCompletion{chat: queryChat, model: settings.QueryModel, gate: gate}, row: row}
			references, err := memes.NewGroundedReferenceServiceWithKnowledge(memes.ReferenceDependencies{Model: completion, Retriever: retriever}, memes.ReferenceOptions{RequestTimeout: time.Duration(settings.RequestTimeoutSeconds) * time.Second, StageTimeout: time.Duration(settings.QueryTimeoutSeconds) * time.Second}, prepared)
			if err != nil {
				t.Fatal("knowledge service invalid")
			}
			finalChat, err := modelapi.NewChat(finalEP)
			if err != nil {
				t.Fatal("final endpoint invalid")
			}
			finalModel := &knowledgeRecordingChat{next: finalChat, row: row}
			native, err := engine.New(engine.Options{Store: f.store, Model: finalModel, Registry: f.registry(t), GroupMembers: f.group, BotOpenID: "replay-bot", ConversationOutput: true, MaxIterations: 8, MaxContextBytes: 131072, MemeReferences: func(parent context.Context, chatID, messageID string, dialogue []memes.Turn) memes.ReferenceResult {
				result := references.Retrieve(parent, memes.Request{RequestID: "knowledge-live-" + sample.ID, Dialogue: dialogue, MaxChars: memes.DefaultMaxChars})
				row.Reference = &result
				return result
			}})
			if err != nil {
				t.Fatal("native engine invalid")
			}
			f.svc, err = service.New(service.Options{Store: f.store, Engine: native, Messenger: f.sender, Owner: "knowledge-native-probe", Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: 3 * time.Minute, EligibleForConversation: func(_ context.Context, m domain.Message) (bool, error) { return m.Source.MessageID == f.currentID, nil }})
			if err != nil {
				t.Fatal("native service invalid")
			}
			f.currentID = fmt.Sprintf("message-%02d", f.messageCount+1)
			last := sample.Turns[len(sample.Turns)-1]
			turn := &replayTurn{MessageID: f.currentID, Input: last.Text, Addressed: sample.Addressed}
			scenario.Turns = append(scenario.Turns, turn)
			message := f.human(t, "member", last.Text, sample.Addressed)
			// Exactly one native Step: retain its first failure; never run the runtime's
			// durable retries or ask the paid provider to reselect a better response.
			if err = f.svc.Step(f.ctx, domain.JobAgentTurn); err != nil {
				turn.Error = "native_turn_failed"
				row.Error = "native_turn_failed"
				t.Errorf("case=%s first native turn failed", sample.ID)
				return
			}
			f.drainDeliveries(t)
			task, _, err := f.store.TaskForMessage(f.ctx, f.scope, message.Source.MessageID)
			if err != nil || task.FinalConversation == nil {
				row.Error = "no_durable_conversation_plan"
				t.Error(row.Error)
				return
			}
			turn.Plan = task.FinalConversation
			turn.Deliveries = len(f.sender.sent)
			row.VisibleReply = replayText(task.FinalConversation)
			row.FinalChoice = task.FinalConversation.Choice
			row.FinalStatus = task.Status
			t.Logf("case=%s reference=%s priors=%d final=%s calls=%d", sample.ID, row.Reference.Status, len(row.Reference.ContextV2.PriorKnowledge), row.FinalChoice, len(row.Final))
		})
	}
}

type knowledgeLiveCase struct {
	ID        string `json:"id"`
	Origin    string `json:"origin"`
	ChatID    string `json:"chat_id"`
	Addressed bool   `json:"addressed"`
	Turns     []struct {
		TurnID  string `json:"turn_id"`
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
		IsBot   bool   `json:"is_bot"`
	} `json:"turns"`
}
type knowledgeLiveReport struct {
	Version                                                                              int
	Model, QueryModel                                                                    string
	Binding                                                                              memes.KnowledgeBinding
	KnowledgeFingerprint                                                                 string
	KnowledgeEntries, NativeToolCount                                                    int
	PolicyExercised                                                                      bool
	RealLarkSends, ProductionSourceWrites, ProductionIndexWrites, ProductionActiveWrites int
	HumanAcceptance                                                                      string
	Rows                                                                                 []*knowledgeLiveRow
}
type knowledgeLiveRow struct {
	ID, Origin                                    string
	Input                                         knowledgeLiveCase
	Planner                                       []knowledgePlannerCall
	Reference                                     *memes.ReferenceResult
	Final                                         []knowledgeFinalCall
	Scenario                                      *replayScenario
	SeededFinal                                   *knowledgeFinalCall `json:",omitempty"`
	VisibleReply, FinalChoice, FinalStatus, Error string
	ElapsedMS                                     int64
}
type knowledgePlannerCall struct {
	Input       memes.CompletionRequest
	InputSHA256 string
	InputBytes  int
	Response    memes.CompletionResponse
	Error       string
	ElapsedMS   int64
}
type knowledgeFinalCall struct {
	Input       modelapi.ChatRequest
	InputSHA256 string
	InputBytes  int
	Response    modelapi.ChatResponse
	Error       string
	ElapsedMS   int64
}

func knowledgeDigest(v any) (string, int) {
	b, _ := json.Marshal(v)
	digest := sha256.Sum256(b)
	return hex.EncodeToString(digest[:]), len(b)
}

type knowledgeRecordingCompletion struct {
	next memes.Completion
	row  *knowledgeLiveRow
}

func (m *knowledgeRecordingCompletion) Complete(ctx context.Context, input memes.CompletionRequest) (memes.CompletionResponse, error) {
	if len(m.row.Planner) > 0 {
		return memes.CompletionResponse{}, errors.New("probe query paid call ceiling")
	}
	started := time.Now()
	hash, n := knowledgeDigest(input)
	response, err := m.next.Complete(ctx, input)
	entry := knowledgePlannerCall{Input: input, InputSHA256: hash, InputBytes: n, Response: response, ElapsedMS: time.Since(started).Milliseconds()}
	if err != nil {
		entry.Error = err.Error()
	}
	m.row.Planner = append(m.row.Planner, entry)
	return response, err
}

type knowledgeRecordingChat struct {
	next  *modelapi.Chat
	row   *knowledgeLiveRow
	limit int
}

func (m *knowledgeRecordingChat) Generate(ctx context.Context, input modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	limit := m.limit
	if limit == 0 {
		limit = 1
	}
	if len(m.row.Final) >= limit {
		return modelapi.ChatResponse{}, errors.New("probe final paid call ceiling")
	}
	if len(input.Tools) != 74 {
		return modelapi.ChatResponse{}, errors.New("native tool inventory changed: " + strconv.Itoa(len(input.Tools)))
	}
	input.MaxTokens = 4096
	started := time.Now()
	hash, n := knowledgeDigest(input)
	response, err := m.next.Generate(ctx, input)
	entry := knowledgeFinalCall{Input: input, InputSHA256: hash, InputBytes: n, Response: response, ElapsedMS: time.Since(started).Milliseconds()}
	if err != nil {
		entry.Error = err.Error()
	}
	m.row.Final = append(m.row.Final, entry)
	return response, err
}

// TestMemeKnowledgeNativeToolContinuation never regenerates the initial model
// decision or planner. It replays that frozen response through the actual native
// tool loop, then allows at most four paid continuation calls. The original
// failed run remains a separate immutable artifact.
func TestMemeKnowledgeNativeToolContinuation(t *testing.T) {
	if os.Getenv("MEME_KNOWLEDGE_CONTINUATION") != "1" {
		t.Skip("explicit separately authorized native tool continuation")
	}
	var original knowledgeLiveReport
	raw, err := os.ReadFile(os.Getenv("MEME_KNOWLEDGE_CONTINUATION_SOURCE"))
	if err != nil || json.Unmarshal(raw, &original) != nil {
		t.Fatal("continuation source invalid")
	}
	var source *knowledgeLiveRow
	for _, row := range original.Rows {
		if row.ID == "H05" {
			source = row
			break
		}
	}
	if source == nil || len(source.Final) != 1 || len(source.Final[0].Response.Message.ToolCalls) == 0 || source.Reference == nil {
		t.Fatal("first tool response unavailable")
	}
	c, err := config.Load("/etc/agent/agent.toml", "worker")
	if err != nil {
		t.Fatal("runtime config invalid: ", err)
	}
	ep := endpoint(c.Models.Answer)
	if ep.Model != original.Model {
		t.Fatal("continuation model changed")
	}
	row := &knowledgeLiveRow{ID: "H05_tool_continuation", Origin: "frozen_first_response_then_native_tool_continuation", Input: source.Input, Reference: source.Reference, Planner: []knowledgePlannerCall{}, Final: []knowledgeFinalCall{}}
	report := knowledgeLiveReport{Version: 1, Model: original.Model, QueryModel: original.QueryModel, Binding: original.Binding, KnowledgeFingerprint: original.KnowledgeFingerprint, KnowledgeEntries: original.KnowledgeEntries, NativeToolCount: 74, PolicyExercised: false, HumanAcceptance: "separate native tool continuation, initial choice and references frozen; no source facts available from stub tools", Rows: []*knowledgeLiveRow{row}}
	start := time.Now()
	t.Cleanup(func() {
		row.ElapsedMS = time.Since(start).Milliseconds()
		b, e := json.MarshalIndent(report, "", "  ")
		if e != nil || os.WriteFile(os.Getenv("MEME_KNOWLEDGE_OUTPUT"), append(b, '\n'), 0600) != nil {
			t.Error("cannot save continuation evidence")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	scenario := &replayScenario{Name: "knowledge_H05", Turns: []*replayTurn{}}
	row.Scenario = scenario
	f := newReplayFixture(t, ctx, replayDSN(t), ep, scenario)
	chat, err := modelapi.NewChat(ep)
	if err != nil {
		t.Fatal("continuation endpoint invalid")
	}
	model := &knowledgeSeedThenChat{next: &knowledgeRecordingChat{next: chat, row: row, limit: 4}, first: source.Final[0].Response, row: row}
	native, err := engine.New(engine.Options{Store: f.store, Model: model, Registry: f.registry(t), GroupMembers: f.group, BotOpenID: "replay-bot", ConversationOutput: true, MaxIterations: 8, MaxContextBytes: 131072, MemeReferences: func(context.Context, string, string, []memes.Turn) memes.ReferenceResult { return *source.Reference }})
	if err != nil {
		t.Fatal("continuation native engine invalid")
	}
	f.svc, err = service.New(service.Options{Store: f.store, Engine: native, Messenger: f.sender, Owner: "knowledge-native-continuation", Lease: time.Minute, Poll: time.Millisecond, TaskTimeout: 6 * time.Minute, EligibleForConversation: func(_ context.Context, m domain.Message) (bool, error) { return m.Source.MessageID == f.currentID, nil }})
	if err != nil {
		t.Fatal("continuation native service invalid")
	}
	f.currentID = "message-01"
	input := source.Input.Turns[len(source.Input.Turns)-1].Text
	turn := &replayTurn{MessageID: f.currentID, Input: input, Addressed: source.Input.Addressed}
	scenario.Turns = append(scenario.Turns, turn)
	message := f.human(t, "member", input, source.Input.Addressed)
	if err = f.svc.Step(f.ctx, domain.JobAgentTurn); err != nil {
		row.Error = "native_tool_continuation_failed"
		turn.Error = row.Error
		t.Error(row.Error)
		return
	}
	f.drainDeliveries(t)
	task, _, err := f.store.TaskForMessage(f.ctx, f.scope, message.Source.MessageID)
	if err != nil || task.FinalConversation == nil {
		row.Error = "no_durable_conversation_plan"
		t.Error(row.Error)
		return
	}
	turn.Plan = task.FinalConversation
	turn.Deliveries = len(f.sender.sent)
	row.VisibleReply = replayText(task.FinalConversation)
	row.FinalChoice = task.FinalConversation.Choice
	row.FinalStatus = task.Status
	scenario.Passed = true
	t.Logf("case=%s frozen_first_responses=1 paid_continuation_calls=%d choice=%s", row.ID, len(row.Final), row.FinalChoice)
}

type knowledgeSeedThenChat struct {
	next   *knowledgeRecordingChat
	first  modelapi.ChatResponse
	row    *knowledgeLiveRow
	seeded bool
}

func (m *knowledgeSeedThenChat) Generate(ctx context.Context, input modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	if !m.seeded {
		m.seeded = true
		hash, n := knowledgeDigest(input)
		m.row.SeededFinal = &knowledgeFinalCall{Input: input, InputSHA256: hash, InputBytes: n, Response: m.first}
		return m.first, nil
	}
	return m.next.Generate(ctx, input)
}

// Published readback is separately gated: the two new catalog tables and the
// expected current sidecar must already exist before native initialization can
// reach its idempotent migration call. No fixture publication or withdrawal is
// performed here. The optional actual query is separately paid and opt-in.
func TestMemeKnowledgePublishedReadback(t *testing.T) {
	if os.Getenv("MEME_KNOWLEDGE_PUBLISHED_READBACK") != "1" {
		t.Skip("wait for explicit published-candidate readback")
	}
	report := map[string]any{"real_lark_sends": 0, "source_writes": 0, "index_writes": 0, "active_writes": 0, "withdrawal_writes": 0, "query_calls": 0, "final_calls": 0, "native_initialize_called": false, "schema_guard_passed": false}
	t.Cleanup(func() {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil || os.WriteFile(os.Getenv("MEME_KNOWLEDGE_OUTPUT"), append(raw, '\n'), 0600) != nil {
			t.Error("cannot retain readback evidence")
		}
	})
	c, err := config.Load("/etc/agent/agent.toml", "worker")
	if err != nil {
		t.Fatal("runtime config invalid: ", err)
	}
	settings, err := c.MemeSettings()
	if err != nil {
		t.Fatal("meme settings invalid")
	}
	report["runtime_enabled"] = settings.Enabled
	report["query_model"] = settings.QueryModel
	report["embedding_model"] = settings.EmbeddingModel
	report["runtime_knowledge_first"] = settings.KnowledgeFirst
	if !settings.Enabled || !settings.KnowledgeFirst {
		t.Fatal("actual runtime knowledge-first flag is not enabled")
	}
	expected := os.Getenv("MEME_KNOWLEDGE_EXPECTED_FINGERPRINT")
	if expected == "" {
		t.Fatal("expected published snapshot fingerprint required")
	}
	db, err := sql.Open("postgres", settings.DatabaseURL)
	if err != nil {
		t.Fatal("catalog connection unavailable")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, table := range []memestore.Kind{memestore.Knowledge, memestore.Withdrawal} {
		var exists bool
		if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname=$1 AND tablename=$2)`, settings.Schema, string(table)).Scan(&exists); err != nil || !exists {
			t.Fatal("published catalog schema is not ready; initialization not attempted")
		}
	}
	report["schema_guard_passed"] = true
	catalog, err := memestore.New(db, settings.Schema)
	if err != nil {
		t.Fatal("catalog schema invalid")
	}
	scoped := catalog.ForScope(settings.Scope)
	before, err := scoped.Active(ctx)
	if err != nil {
		t.Fatal("active catalog unavailable")
	}
	snapshot, err := scoped.Knowledge(ctx, before)
	if err != nil || snapshot.Fingerprint != expected {
		t.Fatal("published sidecar unavailable or unexpected; initialization not attempted")
	}
	report["binding"] = snapshot.Binding
	report["knowledge_fingerprint"] = snapshot.Fingerprint
	report["knowledge_entries"] = len(snapshot.Entries)
	factories := 0
	runtime, err := memeruntime.New(memeruntime.Options{Catalog: scoped, Knowledge: scoped, KnowledgeFirst: true, Scope: settings.Scope, Index: knowledgeReadbackBlockedIndex{}, Model: func(context.Context, string) (memes.Completion, error) {
		factories++
		return nil, errors.New("zero-paid warmup probe")
	}, BotID: c.Lark.BotOpenID})
	if err != nil {
		t.Fatal("runtime composition failed")
	}
	started := time.Now()
	if err = runtime.Warm(ctx); err != nil {
		t.Fatal("published runtime warmup failed")
	}
	report["warm_ms"] = time.Since(started).Milliseconds()
	report["warm_model_factory_calls"] = factories
	if factories != 0 {
		t.Fatal("warmup reached model factory")
	}
	parts := components{db: db}
	defer parts.close()
	report["native_initialize_called"] = true
	if err = initializeMemeReferences(ctx, c, &parts); err != nil || parts.memeReferences == nil || parts.memeRevalidate == nil {
		t.Fatal("actual native meme initialization unavailable")
	}
	selector, err := memes.NewKnowledgeSelector(snapshot)
	if err != nil {
		t.Fatal("published selector invalid")
	}
	dialogue := []memes.Turn{{TurnID: "published-readback-current", Speaker: "readback-member", Text: "三年，从钢铁雄心玩成海岛奇兵了。机器人，接一句。"}}
	selection, err := selector.Select(memes.Request{RequestID: "published-readback", Dialogue: dialogue, MaxChars: memes.DefaultMaxChars})
	if err != nil || len(selection.Entries) == 0 {
		t.Fatal("readback synthetic anchor not found")
	}
	view := memes.GroundedKnowledgeContext(selection)
	view.BindKnowledgeSnapshot(snapshot.Fingerprint)
	probeChat := "knowledge-published-readback"
	if len(settings.AllowedChatIDs) > 0 {
		probeChat = settings.AllowedChatIDs[0]
	}
	current, cards, err := parts.memeRevalidate(ctx, probeChat, view, nil)
	if err != nil || current == nil || len(current.PriorKnowledge) == 0 || len(cards) != 0 {
		t.Fatal("native current source policy rejected verified visible prior")
	}
	report["native_revalidate_prior_titles"] = func() []string {
		titles := []string{}
		for _, p := range current.PriorKnowledge {
			titles = append(titles, p.Title)
		}
		return titles
	}()
	report["observation_method"] = current.ObservationMethod
	report["normalization_version"] = current.NormalizationVersion
	report["dictionary_fingerprint"] = current.DictionaryFingerprint
	raw, _ := json.Marshal(view)
	var tampered memes.GroundedContext
	_ = json.Unmarshal(raw, &tampered)
	tampered.PriorKnowledge[0].EntryFingerprint = "unpublished-entry-fingerprint"
	_, _, tamperErr := parts.memeRevalidate(ctx, probeChat, &tampered, nil)
	if tamperErr == nil {
		t.Fatal("actual revalidator accepted tampered prior fingerprint")
	}
	report["tampered_prior_rejected"] = true
	_ = json.Unmarshal(raw, &tampered)
	tampered.Binding.Scope = "readback-other-scope"
	_, _, scopeErr := parts.memeRevalidate(ctx, probeChat, &tampered, nil)
	if scopeErr == nil {
		t.Fatal("actual revalidator accepted wrong scope")
	}
	report["wrong_scope_rejected"] = true
	if len(settings.AllowedChatIDs) > 0 {
		blockedChat := "knowledge-denied-readback"
		for _, allowed := range settings.AllowedChatIDs {
			if allowed == blockedChat {
				blockedChat += "-outside"
			}
		}
		_, _, chatErr := parts.memeRevalidate(ctx, blockedChat, view, nil)
		if chatErr == nil {
			t.Fatal("actual revalidator accepted a chat outside the current allowlist")
		}
		report["disabled_chat_rejected"] = true
	}
	if os.Getenv("MEME_KNOWLEDGE_PUBLISHED_QUERY") == "1" {
		chatID := "knowledge-published-readback"
		if len(settings.AllowedChatIDs) > 0 {
			chatID = settings.AllowedChatIDs[0]
		}
		result := parts.memeReferences(ctx, chatID, dialogue[0].TurnID, dialogue)
		report["query_result"] = result
		report["query_calls"] = result.Metadata.SemanticCalls + result.Metadata.RepairCalls
		if result.ContextV2 == nil || result.ContextV2.KnowledgeFingerprint != expected || result.Metadata.SemanticCalls != 1 || result.Metadata.RepairCalls != 0 {
			t.Fatal("actual native query did not prove enabled knowledge-first path")
		}
	}
	after, err := scoped.Active(ctx)
	if err != nil || memestore.KnowledgeBinding(settings.Scope, before) != memestore.KnowledgeBinding(settings.Scope, after) {
		t.Fatal("active generation changed during readback")
	}
	report["active_binding_unchanged"] = true
	report["live_source_policy_exercised"] = true
	t.Logf("published knowledge readback ready generation=%s entries=%d native_revalidation=true query_enabled=%t", before.Generation, len(snapshot.Entries), os.Getenv("MEME_KNOWLEDGE_PUBLISHED_QUERY") == "1")
}

type knowledgeReadbackBlockedIndex struct{}

func (knowledgeReadbackBlockedIndex) ForGeneration(memestore.GenerationManifest) (memes.Retriever, error) {
	return nil, errors.New("zero-paid warmup probe cannot search")
}

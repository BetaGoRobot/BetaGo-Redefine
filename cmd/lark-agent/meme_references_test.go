package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mememilvus"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memeruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

func TestMemeReferencesNativeCompositionRetrievesPublishedMilvusGeneration(t *testing.T) {
	dsn, searchURL := os.Getenv("AGENT_TEST_POSTGRES_DSN"), os.Getenv("MEME_TEST_MILVUS_URL")
	if dsn == "" || searchURL == "" {
		t.Skip("isolated PostgreSQL and Milvus required")
	}
	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(oldLogger)
	queries, embeddings := 0, 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/chat/completions":
			queries++
			if r.Header.Get("Authorization") != "Bearer private-query" {
				t.Error("query role credential changed")
			}
			var body struct{ Messages []modelapi.ChatMessage }
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("bad native query request")
			}
			encoded, _ := json.Marshal(body.Messages)
			if !strings.Contains(string(encoded), "raw-query-private-canary") {
				t.Error("raw source never reached native query model")
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": `{"relations":[{"kind":"benefit_cost","pattern":"同一变化使A获得收益并承受损失","evidence":[{"source_id":0,"state":"asserted"}]}],"emotion_chain":[],"keys":[]}`}}}})
		case "/embeddings":
			embeddings++
			if r.Header.Get("Authorization") != "Bearer private-embedding" {
				t.Error("embedding role credential changed")
			}
			var body struct {
				Input      []string
				Dimensions int
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Dimensions != 3 {
				t.Error("meme embedding used memory deployment dimensions")
			}
			data := []any{}
			for i := range body.Input {
				data = append(data, map[string]any{"index": i, "embedding": []float32{1, 0, 0}})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
		default:
			http.NotFound(w, r)
		}
	}))
	defer model.Close()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	schema, prefix := "next_meme_"+suffix, "next_meme_"+suffix
	defer db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
	username, password := os.Getenv("MEME_TEST_MILVUS_USERNAME"), os.Getenv("MEME_TEST_MILVUS_PASSWORD")
	c := config.Config{Database: config.Database{URL: dsn, MaxConnections: 4}, Lark: config.Lark{BotOpenID: "native-bot"}, Models: config.Models{Answer: config.Endpoint{BaseURL: model.URL, APIKey: "private-query", Model: "query-model", Protocol: "openai", TimeoutSeconds: 8}, Embedding: config.Embedding{Endpoint: config.Endpoint{BaseURL: model.URL, APIKey: "private-embedding", Model: "embedding-model", Protocol: "openai", TimeoutSeconds: 8}, Dimensions: 1024, Concurrency: 1, MaxInputs: 32}}, Memes: memeruntime.Settings{Enabled: true, Schema: schema, IndexPrefix: prefix, Scope: "wiki", SearchEndpoint: searchURL, SearchUsername: username, SearchPassword: password, Dimensions: 3}}
	p := &components{db: db}
	if err = initializeMemeReferences(context.Background(), c, p); err != nil {
		t.Fatal(err)
	}
	store, _ := memestore.New(db, schema)
	embed, err := modelapi.NewEmbedder(modelapi.EmbeddingConfig{Endpoint: endpoint(c.Models.Embedding.Endpoint), Dimensions: 3, MaxInputs: 32, Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	scoped := store.ForScope("wiki")
	index, err := mememilvus.New(mememilvus.Options{Endpoint: searchURL, Username: username, Password: password, IndexPrefix: prefix, Scope: "wiki", EmbeddingModel: "embedding-model", Dimensions: 3, Embedder: embed, Ledger: scoped, Records: scoped})
	if err != nil {
		t.Fatal(err)
	}
	card := memes.UseCard{UseID: "native-reference", FamilyID: "two_sides", Name: "好坏消息", ReviewStatus: "pending", InputRelation: "同一变化带来收益和损失", Requirements: []memes.Requirement{{ID: "benefit", AnyOf: []string{"实际收益"}}}, ResponseAct: "指出好坏两面", Roles: map[string]memes.Role{"change": {Kind: "event", Meaning: "同一变化"}}, Stance: memes.Stance{Voice: "bot_comment", ReplyTarget: "conversation", EvaluationTarget: "change"}, Template: "还有这种操作？", Anchors: []string{"这种操作"}, Slots: map[string]memes.Slot{}, Sources: []memes.SourceMetadata{{Kind: "constructed", Scope: "结构示例"}}}
	card.Description = "同一个变化同时带来好处和代价的原始Wiki解释。"
	fingerprint, _ := memes.Fingerprint(card)
	record, err := memes.CompileCard(card, memes.Projection{CardFingerprint: fingerprint, Relations: []memes.Relation{{Kind: "benefit_cost", Pattern: "同一变化使A收益并失去B"}}, Keys: memes.ProjectionKeys{Related: []string{"宽带"}}})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := index.Publish(context.Background(), "g1", []memes.IndexRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, collection := range manifest.Indexes {
			body, _ := json.Marshal(map[string]string{"collectionName": collection})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(searchURL, "/")+"/v2/vectordb/collections/drop", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if username != "" || password != "" {
				req.Header.Set("Authorization", "Bearer "+username+":"+password)
			}
			res, e := http.DefaultClient.Do(req)
			if e == nil {
				res.Body.Close()
			}
			cancel()
		}
	}()
	if manifest.Backend != "milvus" {
		t.Fatal("native publication did not bind Milvus backend")
	}
	if _, err = scoped.Put(context.Background(), memestore.Generation, "g1", manifest); err != nil {
		t.Fatal(err)
	}
	if err = scoped.Activate(context.Background(), "g1"); err != nil {
		t.Fatal(err)
	}
	beforeEmbedding := embeddings
	result := p.memeReferences(context.Background(), "chat", "message", []memes.Turn{{TurnID: "message", Speaker: "raw-human", Text: "raw-query-private-canary 升级宽带后更快但更贵"}})
	if result.Status != memes.ReferenceStatusReady || len(result.Cards) != 1 || result.Cards[0].UseID != "native-reference" || queries != 1 || embeddings != beforeEmbedding+1 || result.Metadata.SemanticCalls != 1 {
		t.Fatalf("native composition did not retrieve published references: status=%s reason=%s cards=%d query=%d embedding=%d", result.Status, result.Reason, len(result.Cards), queries, embeddings-beforeEmbedding)
	}
	if len(result.Retrieval.Queries) != 2 || len(result.Retrieval.Queries[0].Channels) != 2 || result.Retrieval.Queries[1].Query.Field != memes.FieldDescription || len(result.Retrieval.Queries[1].Channels) != 1 || result.Retrieval.Queries[1].Channels[0].Channel != "vector" {
		t.Fatal("native path did not preserve semantic/BM25 and original-description vector lanes")
	}
	if strings.Contains(logs.String(), "raw-query-private-canary") || strings.Contains(logs.String(), "private-query") || strings.Contains(logs.String(), "private-embedding") {
		t.Fatal("meme query/embedding logs exposed raw dialogue or credentials")
	}
}

func TestMemeQueryCompletionUsesNativeRoleSchemaAndNoAutomaticRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer private-native-key" {
			t.Error("query adapter lost independent endpoint/credential")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if string(body["model"]) != `"query-endpoint"` || !strings.Contains(string(body["response_format"]), "json_schema") || strings.Contains(string(body["messages"]), "private-native-key") || string(body["thinking"]) != `{"type":"disabled"}` {
			t.Error("query model request lost native bounded schema contract")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 2 {
			http.Error(w, "provider failure", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"relations\":[],\"emotion_chain\":[],\"keys\":[]}"}}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`))
	}))
	defer server.Close()
	chat, err := modelapi.NewChat(modelapi.Endpoint{BaseURL: server.URL, APIKey: "private-native-key", Model: "query-endpoint", Protocol: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	completion := memeQueryCompletion{chat: chat, model: "query-endpoint"}
	req := memes.CompletionRequest{Stage: memes.StageQuery, SystemPrompt: "reference query system", InputJSON: json.RawMessage(`{"raw":"source"}`), Schema: memes.StageSchema(memes.StageQuery)}
	res, err := completion.Complete(context.Background(), req)
	if err != nil || !json.Valid(res.JSON) || res.Metadata.Model != "query-endpoint" || res.Metadata.InputTokens != 11 || res.Metadata.OutputTokens != 7 {
		t.Fatalf("native query completion failed: %v", err)
	}
	if _, err = completion.Complete(context.Background(), req); err == nil || calls != 2 {
		t.Fatalf("query HTTP failure retried or disappeared: calls=%d err=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = completion.Complete(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatal("native query cancellation lost its timeout classification")
	}
}

func TestMemeReferencesCompositionUsesCorrectCatalogDatabaseAndClosesOwnedConnection(t *testing.T) {
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("next_meme_%d", time.Now().UnixNano())
	defer db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
	c := config.Config{Database: config.Database{URL: "postgres://unused/next", MaxConnections: 3}, Lark: config.Lark{BotOpenID: "stable-bot"}, Models: config.Models{Answer: config.Endpoint{BaseURL: "http://query.example.test", APIKey: "private-query", Model: "query-model", Protocol: "openai", TimeoutSeconds: 8}, Embedding: config.Embedding{Endpoint: config.Endpoint{BaseURL: "http://embedding.example.test", APIKey: "private-embedding", Model: "embedding-model", Protocol: "openai", TimeoutSeconds: 8}, Dimensions: 1024, Concurrency: 1, MaxInputs: 32}}, Memes: memeruntime.Settings{Enabled: true, DatabaseURL: dsn, Schema: schema, SearchEndpoint: "http://milvus.example.test:19530", AllowedChatIDs: []string{"allowed-chat"}}}
	nextDB, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	p := &components{db: nextDB}
	if err = initializeMemeReferences(context.Background(), c, p); err != nil {
		t.Fatal(err)
	}
	if p.memeReferences == nil || p.memeDB == nil || p.memeDB == nextDB {
		t.Fatal("separate catalog connection or registered provider absent")
	}
	var exists bool
	if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=$1 AND table_name='meme_index_generations')`, schema).Scan(&exists); err != nil || !exists {
		t.Fatalf("catalog migrated in wrong database: %v", err)
	}
	result := p.memeReferences(context.Background(), "allowed-chat", "message", []memes.Turn{{TurnID: "message", Speaker: "human", Text: "hello"}})
	if result.Status != memes.ReferenceStatusError || result.Reason != "CORPUS_UNAVAILABLE" {
		t.Fatal("missing published generation did not fail open to host")
	}
	result = p.memeReferences(context.Background(), "other-chat", "message", []memes.Turn{{TurnID: "message", Speaker: "human", Text: "hello"}})
	if result.Reason != "CHAT_NOT_ENABLED" {
		t.Fatal("reference rollout allowlist ignored")
	}
	owned := p.memeDB
	p.close()
	if err = owned.Ping(); err == nil {
		t.Fatal("owned catalog connection leaked")
	}
	if err = initializeMemeReferences(context.Background(), config.Config{}, &components{}); err != nil {
		t.Fatal("disabled references needlessly initialize dependencies")
	}
}

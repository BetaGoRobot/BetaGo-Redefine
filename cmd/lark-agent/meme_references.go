package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mememilvus"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memeruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

// No legacy Ark global configuration participates in either native model role.
// ReferenceService owns its one query plus optional structural repair budget;
// this adapter performs exactly one HTTP call for each requested completion.
type memeQueryCompletion struct {
	chat  *modelapi.Chat
	model string
	gate  *memestore.QuotaGate
}

func (m memeQueryCompletion) Complete(ctx context.Context, input memes.CompletionRequest) (memes.CompletionResponse, error) {
	ctx = telemetry.WithoutPayload(ctx)
	if m.gate != nil {
		release, err := m.gate.Acquire(ctx, "model")
		if err != nil {
			if ctx.Err() != nil {
				return memes.CompletionResponse{}, ctx.Err()
			}
			return memes.CompletionResponse{}, errors.New("meme query admission unavailable")
		}
		defer release()
	}
	format, err := json.Marshal(map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "meme_query", "strict": true, "schema": input.Schema}})
	if err != nil {
		return memes.CompletionResponse{}, errors.New("meme query schema invalid")
	}
	response, err := m.chat.Generate(ctx, modelapi.ChatRequest{Messages: []modelapi.ChatMessage{{Role: "system", Content: input.SystemPrompt}, {Role: "user", Content: string(input.InputJSON)}}, ResponseFormat: format, MaxTokens: 2048})
	if err != nil {
		if ctx.Err() != nil {
			return memes.CompletionResponse{}, ctx.Err()
		}
		return memes.CompletionResponse{}, errors.New("meme query model unavailable")
	}
	text, ok := response.Message.Content.(string)
	if !ok {
		return memes.CompletionResponse{}, errors.New("meme query response invalid")
	}
	return memes.CompletionResponse{JSON: json.RawMessage(text), Metadata: memes.CallMetadata{Model: m.model, InputTokens: response.PromptTokens, OutputTokens: response.CompletionTokens}}, nil
}

type memeReferenceEmbedder struct {
	embed *modelapi.Embedder
	gate  *memestore.QuotaGate
	model string
}

func (m memeReferenceEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	ctx = telemetry.WithoutPayload(ctx)
	release, err := m.gate.Acquire(ctx, "model")
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("meme embedding admission unavailable")
	}
	defer release()
	return m.embed.Embed(ctx, texts)
}

func initializeMemeReferences(ctx context.Context, c config.Config, p *components) error {
	s, err := c.MemeSettings()
	if err != nil {
		return err
	}
	if !s.Enabled {
		return nil
	}
	db := p.db
	if s.DatabaseURL != c.Database.URL {
		db, err = sql.Open("postgres", s.DatabaseURL)
		if err != nil {
			return errors.New("meme catalog database configuration invalid")
		}
		p.memeDB = db // owned by components on every startup and shutdown path
		limit := c.Database.MaxConnections
		if limit < 1 {
			limit = 4
		}
		db.SetMaxOpenConns(limit)
		db.SetMaxIdleConns(limit)
		db.SetConnMaxIdleTime(5 * time.Minute)
	}
	if db == nil || db.PingContext(ctx) != nil {
		return errors.New("meme catalog database unavailable")
	}
	catalog, err := memestore.New(db, s.Schema)
	if err != nil {
		return errors.New("meme catalog schema invalid")
	}
	if err = catalog.Migrate(ctx); err != nil {
		return errors.New("meme catalog migration failed")
	}
	gate, err := catalog.Gate("ark:meme-reference", time.Duration(s.StartIntervalMillis)*time.Millisecond, s.MaxConcurrency)
	if err != nil {
		return errors.New("meme model quota configuration invalid")
	}
	queryEndpoint := endpoint(c.Models.Answer)
	queryEndpoint.Model = s.QueryModel
	queryEndpoint.Timeout = time.Duration(s.QueryTimeoutSeconds) * time.Second
	chat, err := modelapi.NewChat(queryEndpoint)
	if err != nil {
		return errors.New("meme query model initialization failed")
	}
	embed, err := newMemeReferenceEmbedder(c, s)
	if err != nil {
		return errors.New("meme embedding initialization failed")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if s.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	scoped := catalog.ForScope(s.Scope)
	index, err := mememilvus.New(mememilvus.Options{Endpoint: s.SearchEndpoint, Username: s.SearchUsername, Password: s.SearchPassword, IndexPrefix: s.IndexPrefix, Scope: s.Scope, EmbeddingModel: s.EmbeddingModel, Dimensions: s.Dimensions, Embedder: memeReferenceEmbedder{embed: embed, gate: gate, model: s.EmbeddingModel}, HTTPClient: &http.Client{Timeout: time.Duration(s.QueryTimeoutSeconds) * time.Second, Transport: transport}, Ledger: scoped, Records: scoped})
	if err != nil {
		return errors.New("meme reference index initialization failed")
	}
	completion := memeQueryCompletion{chat: chat, model: s.QueryModel, gate: gate}
	service, err := memeruntime.New(memeruntime.Options{Catalog: scoped, Knowledge: scoped, KnowledgeFirst: s.KnowledgeFirst, Scope: s.Scope, Index: index, Model: func(context.Context, string) (memes.Completion, error) { return completion, nil }, BotID: c.Lark.BotOpenID, AllowedChats: s.AllowedChatIDs, Reference: memes.ReferenceOptions{RequestTimeout: time.Duration(s.RequestTimeoutSeconds) * time.Second, StageTimeout: time.Duration(s.QueryTimeoutSeconds) * time.Second}})
	if err != nil {
		return errors.New("meme reference runtime initialization failed")
	}
	if err = service.Warm(ctx); err != nil {
		telemetry.Info(ctx, "agent.memes.knowledge.warmup", "status", "unavailable", "fallback", "compatible_retrieval")
	}
	p.memeRevalidate = service.Revalidate
	p.memeReferences = func(parent context.Context, chatID, messageID string, dialogue []memes.Turn) memes.ReferenceResult {
		request, cancel := context.WithTimeout(parent, time.Duration(s.RequestTimeoutSeconds)*time.Second)
		defer cancel()
		return service.References(request, chatID, messageID, dialogue)
	}
	return nil
}

func newMemeReferenceEmbedder(c config.Config, s memeruntime.Settings) (*modelapi.Embedder, error) {
	configured := endpoint(c.Models.Embedding.Endpoint)
	configured.Model = s.EmbeddingModel
	configured.Timeout = time.Duration(s.QueryTimeoutSeconds) * time.Second
	return modelapi.NewEmbedder(modelapi.EmbeddingConfig{Endpoint: configured, Dimensions: s.Dimensions, Concurrency: c.Models.Embedding.Concurrency, MaxInputs: c.Models.Embedding.MaxInputs})
}

func (p *components) close() {
	if p.memeDB != nil {
		p.memeDB.Close()
	}
	if p.db != nil {
		p.db.Close()
	}
}

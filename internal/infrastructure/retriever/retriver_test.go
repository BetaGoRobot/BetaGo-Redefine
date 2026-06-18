package retriever

import (
	"context"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/tmc/langchaingo/llms"
)

type fakeTextGenerator struct {
	resp *llms.ContentResponse
	err  error
}

func (f fakeTextGenerator) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	return f.resp, f.err
}

func TestIndexNameUsesV2Prefix(t *testing.T) {
	got := indexNameForSuffix("chat_123")
	want := "langchaingo_v2_chat_123"
	if got != want {
		t.Fatalf("indexNameForSuffix() = %q, want %q", got, want)
	}
}

func TestMutateIndexMapUsesV2VectorDimension(t *testing.T) {
	indexMap := map[string]any{
		"mappings": map[string]any{
			"properties": map[string]any{
				"contentVector": map[string]any{
					"dimension": 1536,
				},
			},
		},
	}

	mutateIndexMap(&indexMap)

	got := indexMap["mappings"].(map[string]any)["properties"].(map[string]any)["contentVector"].(map[string]any)["dimension"]
	if got != vectorDimension {
		t.Fatalf("contentVector.dimension = %v, want %d", got, vectorDimension)
	}
}

func TestEmbeddingUsageScopeFromContextFallback(t *testing.T) {
	scope := embeddingUsageScopeFromContext(context.Background())
	normalized := llmusage.NormalizeScope(scope)
	if normalized.ChatName != "system:retriever_embedding" {
		t.Fatalf("ChatName = %q, want source fallback", normalized.ChatName)
	}
}

func TestEmbeddingUsageScopeFromContextUsesContext(t *testing.T) {
	want := llmusage.Scope{
		ChatID:     "oc_chat",
		ChatName:   "Test Chat",
		SourceType: llmusage.SourceTypeSystem,
		Source:     "retriever_embedding",
	}
	ctx := context.WithValue(context.Background(), embeddingUsageScopeKey{}, want)

	got := embeddingUsageScopeFromContext(ctx)
	if got != want {
		t.Fatalf("scope = %+v, want %+v", got, want)
	}
}

func TestRecordLangchainUsageWritesTokenUsage(t *testing.T) {
	store := &fakeRetrieverUsageStore{}
	llmusage.SetDefaultRecorder(llmusage.NewRecorderWithStore(store))
	t.Cleanup(func() {
		llmusage.SetDefaultRecorder(nil)
	})

	recordLangchainUsage(context.Background(), llmusage.Scope{
		ChatID:     "oc_chat",
		SourceType: llmusage.SourceTypeSystem,
		Source:     "retriever_answer",
	}, "model", &llms.ContentResponse{Choices: []*llms.ContentChoice{{
		Content: "answer",
		GenerationInfo: map[string]any{
			"PromptTokens":     11,
			"CompletionTokens": 7,
			"TotalTokens":      18,
		},
	}}}, nil)

	if len(store.rows) != 1 {
		t.Fatalf("usage rows = %d, want 1", len(store.rows))
	}
	row := store.rows[0]
	if row.Status != string(llmusage.StatusSuccess) || row.PromptTokens != 11 || row.CompletionTokens != 7 || row.TotalTokens != 18 {
		t.Fatalf("usage row = %+v", row)
	}
}

func TestRecordLangchainUsageRecordsErrors(t *testing.T) {
	store := &fakeRetrieverUsageStore{}
	llmusage.SetDefaultRecorder(llmusage.NewRecorderWithStore(store))
	t.Cleanup(func() {
		llmusage.SetDefaultRecorder(nil)
	})

	recordLangchainUsage(context.Background(), llmusage.Scope{SourceType: llmusage.SourceTypeSystem, Source: "retriever_answer"}, "model", nil, errors.New("call failed"))

	if len(store.rows) != 1 {
		t.Fatalf("usage rows = %d, want 1", len(store.rows))
	}
	if store.rows[0].Status != string(llmusage.StatusError) || store.rows[0].Error != "call failed" {
		t.Fatalf("usage row = %+v", store.rows[0])
	}
}

type fakeRetrieverUsageStore struct {
	rows []llmusage.UsageRecordRow
}

func (s *fakeRetrieverUsageStore) CreateUsageRecord(_ context.Context, row *llmusage.UsageRecordRow) error {
	s.rows = append(s.rows, *row)
	return nil
}

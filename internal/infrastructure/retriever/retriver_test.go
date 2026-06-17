package retriever

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
)

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

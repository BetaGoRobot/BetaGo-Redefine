package retriever

import (
	"testing"
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


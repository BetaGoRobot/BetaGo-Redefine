package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memeruntime"
)

func TestMemeReferenceEmbedderPreservesArkProtocolAndIndependentDimensions(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/embeddings/multimodal" || r.Header.Get("Authorization") != "Bearer embedding-fixture" {
			t.Errorf("embedding role protocol or credentials changed: path=%s", r.URL.Path)
			http.Error(w, "wrong protocol", http.StatusBadRequest)
			return
		}
		var input struct {
			Model      string `json:"model"`
			Dimensions int    `json:"dimensions"`
			Input      []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Model != "meme-embedding" || input.Dimensions != 2048 || len(input.Input) != 1 || input.Input[0].Type != "text" || input.Input[0].Text != "raw projection" {
			t.Error("Ark multimodal embedding request lost the independent meme settings")
			http.Error(w, "wrong request", http.StatusBadRequest)
			return
		}
		vector := make([]float32, 2048)
		vector[0], vector[2047] = 0.5, 1.5
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"embedding": vector}})
	}))
	defer server.Close()
	c := config.Config{Models: config.Models{Embedding: config.Embedding{Endpoint: config.Endpoint{BaseURL: server.URL, APIKey: "embedding-fixture", Model: "memory-embedding", Protocol: "ark_multimodal"}, Dimensions: 1024, Concurrency: 1, MaxInputs: 8}}}
	embedder, err := newMemeReferenceEmbedder(c, memeruntime.Settings{EmbeddingModel: "meme-embedding", Dimensions: 2048, QueryTimeoutSeconds: 8})
	if err != nil {
		t.Fatal(err)
	}
	result, err := embedder.Embed(context.Background(), []string{"raw projection"})
	if err != nil || len(result) != 1 || len(result[0]) != 2048 || result[0][0] != 0.5 || result[0][2047] != 1.5 || calls.Load() != 1 {
		t.Fatalf("native meme embedding failed: err=%v calls=%d", err, calls.Load())
	}
	if c.Models.Embedding.Dimensions != 1024 || c.Models.Embedding.Model != "memory-embedding" || c.Models.Embedding.Protocol != "ark_multimodal" {
		t.Fatal("meme embedding changed existing memory role")
	}
}

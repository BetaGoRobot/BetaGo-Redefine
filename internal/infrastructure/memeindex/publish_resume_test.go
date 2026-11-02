package memeindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func TestOpenSearch215PublicationResumesCommittedEmbeddingBatches(t *testing.T) {
	endpoint := os.Getenv("MEME_TEST_OPENSEARCH_URL")
	if endpoint == "" {
		t.Skip("set MEME_TEST_OPENSEARCH_URL for isolated OpenSearch 2.15")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prefix := fmt.Sprintf("meme_resume_%d", time.Now().UnixNano())
	var mutationRequests atomic.Int32
	var hideFrozenDocument atomic.Bool
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			mutationRequests.Add(1)
		}
		if hideFrozenDocument.Load() && r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/_doc/") {
			return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"found":false}`)), Request: r}, nil
		}
		return transport.RoundTrip(r)
	})}
	var batches []int
	failSecondBatch := true
	embedder := embedFunc(func(_ context.Context, texts []string) ([][]float32, error) {
		batches = append(batches, len(texts))
		if failSecondBatch && len(batches) == 2 {
			return nil, errors.New("temporary embedding failure")
		}
		return fixedEmbedder().Embed(ctx, texts)
	})
	l := &ledger{}
	c, err := New(Options{Endpoint: endpoint, HTTPClient: httpClient, IndexPrefix: prefix, Dimensions: 3, EmbeddingModel: "resume-fixture", Embedder: embedder, Ledger: l})
	if err != nil {
		t.Fatal(err)
	}
	indexes := c.indexes("g1")
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, index := range indexes {
			c.request(cleanup, http.MethodDelete, "/"+index, nil, "")
		}
	})
	records := make([]memes.IndexRecord, 70)
	for i := range records {
		records[i] = fixture(t, fmt.Sprintf("resume_%03d", i))
	}
	failed, err := c.Publish(ctx, "g1", records)
	if !errors.Is(err, ErrEmbedding) || failed.Verified {
		t.Fatalf("failed embedding batch published a generation: %v", err)
	}
	committed := 0
	for _, record := range records {
		if _, found, err := c.getDocument(ctx, indexes[0], record.UseID); err != nil {
			t.Fatal(err)
		} else if found {
			committed++
		}
	}
	if committed != 64 || !slices.Equal(batches, []int{64, 64}) {
		t.Fatalf("first completed batch was not durably checkpointed: docs=%d batches=%v", committed, batches)
	}
	for _, index := range indexes {
		if frozen, err := c.readWriteBlock(ctx, index); err != nil || frozen {
			t.Fatalf("failed publication froze an unfinished index: frozen=%t err=%v", frozen, err)
		}
	}
	documentReceipts := 0
	for id, raw := range l.values {
		var event publicationEvent
		if json.Unmarshal([]byte(raw), &event) == nil {
			if event.Operation == "freeze_index" {
				t.Fatal("failed publication attempted to freeze an unfinished index")
			}
			if strings.HasSuffix(id, ":receipt") {
				if event.Operation == "publish_generation" {
					t.Fatal("failed publication certified a verified manifest")
				}
				if event.Operation == "create_document" {
					documentReceipts++
				}
			}
		}
	}
	if documentReceipts != 64 {
		t.Fatalf("completed batch lost its durable write receipts: %d", documentReceipts)
	}
	// An unexpected change in a committed, still inactive document is a
	// conflict; resuming must neither overwrite it nor pay for new embeddings.
	original, found, err := c.getDocument(ctx, indexes[0], records[0].UseID)
	if err != nil || !found {
		t.Fatal("committed fixture unavailable")
	}
	tampered := original
	tampered.Full.Name = "changed unpublished record"
	raw, _ := json.Marshal(tampered)
	if status, _, err := c.request(ctx, http.MethodPut, "/"+indexes[0]+"/_doc/"+documentID(original.UseID), raw, ""); err != nil || !successful(status) {
		t.Fatal("could not prepare isolated conflicting document")
	}
	mutationsBeforeConflict := mutationRequests.Load()
	if _, err := c.Publish(ctx, "g1", records); !errors.Is(err, ErrConflict) || len(batches) != 2 || mutationRequests.Load() != mutationsBeforeConflict {
		t.Fatalf("resume ignored existing document validation: %v", err)
	}
	raw, _ = json.Marshal(original)
	if status, _, err := c.request(ctx, http.MethodPut, "/"+indexes[0]+"/_doc/"+documentID(original.UseID), raw, ""); err != nil || !successful(status) {
		t.Fatal("could not restore isolated fixture")
	}
	failSecondBatch = false
	manifest, err := c.Publish(ctx, "g1", records)
	if err != nil || !manifest.Verified || manifest.RecordCount != 70 {
		t.Fatalf("publication did not resume: verified=%t err=%v", manifest.Verified, err)
	}
	if !slices.Equal(batches, []int{64, 64, 64, 12}) {
		t.Fatalf("retry recomputed committed vectors: batches=%v", batches)
	}
	for _, index := range indexes {
		if frozen, err := c.readWriteBlock(ctx, index); err != nil || !frozen {
			t.Fatalf("completed publication was not frozen: frozen=%t err=%v", frozen, err)
		}
	}
	mutations := mutationRequests.Load()
	again, err := c.Publish(ctx, "g1", records)
	if err != nil || !again.Verified || mutationRequests.Load() != mutations || len(batches) != 4 {
		t.Fatalf("frozen publication was modified or re-embedded: %v", err)
	}
	hideFrozenDocument.Store(true)
	if _, err := c.Publish(ctx, "g1", records); !errors.Is(err, ErrConflict) || mutationRequests.Load() != mutations || len(batches) != 4 {
		t.Fatalf("missing frozen document triggered writes or embedding: %v", err)
	}
}

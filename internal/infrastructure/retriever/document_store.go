package retriever

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	opensearchgo "github.com/opensearch-project/opensearch-go"
	"github.com/opensearch-project/opensearch-go/opensearchapi"
	"github.com/tmc/langchaingo/embeddings"
	"github.com/tmc/langchaingo/schema"
	"github.com/tmc/langchaingo/vectorstores"
	vectoropensearch "github.com/tmc/langchaingo/vectorstores/opensearch"
)

// documentStore owns the legacy SDK response bodies. The upstream vector store
// discards indexing responses without reading or closing them.
type documentStore struct {
	indexStore *vectoropensearch.Store
	client     *opensearchgo.Client
	embedder   embeddings.Embedder
}

func newDocumentStore(client *opensearchgo.Client, embedder embeddings.Embedder) (*documentStore, error) {
	store, err := vectoropensearch.New(client, vectoropensearch.WithEmbedder(embedder))
	if err != nil {
		return nil, err
	}
	return &documentStore{indexStore: &store, client: client, embedder: embedder}, nil
}

func (s *documentStore) CreateIndex(ctx context.Context, index string, opts ...vectoropensearch.IndexOption) (*opensearchapi.Response, error) {
	response, err := s.indexStore.CreateIndex(ctx, index, opts...)
	return response, consumeDocumentResponse(response, err, true)
}

func (s *documentStore) AddDocuments(ctx context.Context, docs []schema.Document, options ...vectorstores.Option) ([]string, error) {
	var opts vectorstores.Options
	for _, option := range options {
		option(&opts)
	}
	texts := make([]string, len(docs))
	for i := range docs {
		texts[i] = docs[i].PageContent
	}
	vectors, err := s.embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(docs) {
		return nil, vectoropensearch.ErrNumberOfVectorDoesNotMatch
	}
	ids := make([]string, 0, len(docs))
	for i, doc := range docs {
		// Preserve the upstream document schema and generated document IDs.
		payload, err := json.Marshal(struct {
			Content string         `json:"content"`
			Vector  []float32      `json:"contentVector"`
			Meta    map[string]any `json:"metadata"`
		}{Content: doc.PageContent, Vector: vectors[i], Meta: doc.Metadata})
		if err != nil {
			return ids, fmt.Errorf("encode vector document: %w", err)
		}
		id := uuid.NewString()
		request := opensearchapi.IndexRequest{
			Index: opts.NameSpace, DocumentID: id, Body: strings.NewReader(string(payload)),
		}
		response, err := request.Do(ctx, s.client)
		if err := consumeDocumentResponse(response, err, false); err != nil {
			return ids, fmt.Errorf("index vector document: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Only index creation may ignore the specific "already exists" response.
// Consume the body even on HTTP errors so the connection can be reused.
func consumeDocumentResponse(response *opensearchapi.Response, requestErr error, allowExistingIndex bool) error {
	if response == nil {
		if requestErr != nil {
			return requestErr
		}
		return fmt.Errorf("OpenSearch returned no response")
	}
	var body []byte
	var readErr error
	if response.Body != nil {
		defer response.Body.Close()
		body, readErr = io.ReadAll(response.Body)
	}
	if requestErr != nil {
		return requestErr
	}
	if readErr != nil {
		return fmt.Errorf("read OpenSearch response: %w", readErr)
	}
	if response.IsError() {
		if allowExistingIndex && response.StatusCode == http.StatusBadRequest {
			var failure struct {
				Error struct {
					Type string `json:"type"`
				} `json:"error"`
			}
			if json.Unmarshal(body, &failure) == nil && failure.Error.Type == "resource_already_exists_exception" {
				return nil
			}
		}
		return fmt.Errorf("OpenSearch HTTP %d: %s", response.StatusCode, body)
	}
	return nil
}

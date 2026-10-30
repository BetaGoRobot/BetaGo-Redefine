package memeindex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"sort"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

var ErrLedger = errors.New("meme publication ledger failed")

type indexMeta struct {
	DescriptionEmbeddingPolicy string      `json:"description_embedding_policy,omitempty"`
	VectorEngine               string      `json:"vector_engine"`
	Schema                     string      `json:"schema"`
	Generation                 string      `json:"generation"`
	Field                      memes.Field `json:"field"`
	CorpusHash                 string      `json:"corpus_hash"`
	ProjectionHash             string      `json:"projection_hash"`
	EmbeddingModel             string      `json:"embedding_model"`
	Dimension                  int         `json:"dimension"`
	RecordCount                int         `json:"record_count"`
}
type publicationEvent struct {
	TextHash        string                        `json:"text_hash,omitempty"`
	ChunkCount      int                           `json:"chunk_count,omitempty"`
	EmbeddingPolicy string                        `json:"embedding_policy,omitempty"`
	Generation      string                        `json:"generation"`
	Operation       string                        `json:"operation"`
	Index           string                        `json:"index,omitempty"`
	DocumentID      string                        `json:"document_id,omitempty"`
	Fingerprint     string                        `json:"fingerprint"`
	Manifest        *memestore.GenerationManifest `json:"manifest,omitempty"`
}

func (c *Client) record(ctx context.Context, e publicationEvent, phase string) error {
	if c.options.Ledger == nil {
		return nil
	}
	id, _ := memes.Fingerprint(e)
	_, err := c.options.Ledger.Put(ctx, memestore.Publication, e.Generation+":"+id+":"+phase, e)
	if err != nil {
		return ErrLedger
	}
	return nil
}
func (c *Client) mapping(meta indexMeta) map[string]any {
	return map[string]any{"dynamic": "strict", "_meta": meta, "properties": map[string]any{
		"use_id":         map[string]any{"type": "keyword"},
		"text":           map[string]any{"type": "text", "analyzer": "cjk"},
		"full":           map[string]any{"type": "object", "enabled": false},
		"projection":     map[string]any{"type": "object", "enabled": false},
		"fingerprint":    map[string]any{"type": "keyword", "index": false, "doc_values": false},
		"relation_kinds": map[string]any{"type": "keyword"},
		"vector":         map[string]any{"type": "knn_vector", "dimension": c.options.Dimensions, "method": map[string]any{"name": "hnsw", "engine": c.vectorEngine(), "space_type": "l2", "parameters": map[string]any{"ef_construction": 128, "m": 16}}},
	}}
}
func expectedMeta(m memestore.GenerationManifest, field memes.Field) indexMeta {
	meta := indexMeta{Schema: schemaVersion, Generation: m.Generation, Field: field, CorpusHash: m.CorpusHash, ProjectionHash: m.ProjectionHash, EmbeddingModel: m.EmbeddingModel, Dimension: m.Dimension, RecordCount: m.RecordCount, VectorEngine: engineForDimensions(m.Dimension)}
	if field == memes.FieldDescription {
		meta.DescriptionEmbeddingPolicy = descriptionEmbeddingPolicy
	}
	return meta
}
func (c *Client) checkMapping(ctx context.Context, index string, meta indexMeta) (bool, error) {
	status, raw, err := c.request(ctx, http.MethodGet, "/"+index+"/_mapping", nil, "")
	if err != nil {
		return false, err
	}
	if status == http.StatusNotFound {
		return false, nil
	}
	if !successful(status) {
		return false, &HTTPError{Status: status}
	}
	var response map[string]struct {
		Mappings json.RawMessage `json:"mappings"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return false, ErrResponse
	}
	body, ok := response[index]
	if !ok {
		return false, ErrResponse
	}
	var actual, expected any
	if json.Unmarshal(body.Mappings, &actual) != nil {
		return false, ErrResponse
	}
	b, _ := json.Marshal(c.mapping(meta))
	json.Unmarshal(b, &expected)
	if !reflect.DeepEqual(actual, expected) {
		return false, ErrConflict
	}
	return true, nil
}
func (c *Client) ensureIndex(ctx context.Context, index string, meta indexMeta) error {
	mapping := c.mapping(meta)
	body, _ := json.Marshal(map[string]any{"settings": map[string]any{"index": map[string]any{"knn": true, "number_of_shards": 1, "number_of_replicas": 0}}, "mappings": mapping})
	fp, _ := memes.Fingerprint(json.RawMessage(body))
	event := publicationEvent{Generation: meta.Generation, Operation: "create_index", Index: index, Fingerprint: fp}
	if err := c.record(ctx, event, "intent"); err != nil {
		return err
	}
	exists, err := c.checkMapping(ctx, index, meta)
	if err != nil {
		return err
	}
	if !exists {
		status, raw, writeErr := c.request(ctx, http.MethodPut, "/"+index, body, "")
		// A transport failure can follow a committed write. Read remote state before any retry.
		if writeErr != nil || !successful(status) {
			found, readErr := c.checkMapping(ctx, index, meta)
			if readErr != nil {
				return readErr
			}
			if !found {
				if writeErr != nil {
					return writeErr
				}
				return &HTTPError{Status: status}
			}
		} else {
			var ack struct {
				Acknowledged bool `json:"acknowledged"`
			}
			if json.Unmarshal(raw, &ack) != nil || !ack.Acknowledged {
				return ErrResponse
			}
		}
	}
	if _, err := c.checkMapping(ctx, index, meta); err != nil {
		return err
	}
	return c.record(ctx, event, "receipt")
}
func (c *Client) getDocument(ctx context.Context, index, id string) (document, bool, error) {
	status, raw, err := c.request(ctx, http.MethodGet, "/"+index+"/_doc/"+documentID(id), nil, "")
	if err != nil {
		return document{}, false, err
	}
	if status == http.StatusNotFound {
		return document{}, false, nil
	}
	if !successful(status) {
		return document{}, false, &HTTPError{Status: status}
	}
	var response struct {
		Found  bool     `json:"found"`
		ID     string   `json:"_id"`
		Source document `json:"_source"`
	}
	if json.Unmarshal(raw, &response) != nil || !response.Found || response.ID != documentID(id) {
		return document{}, false, ErrResponse
	}
	return response.Source, true, nil
}
func relationKinds(record memes.IndexRecord) []string {
	out := make([]string, 0, len(record.Projection.Relations))
	for _, r := range record.Projection.Relations {
		out = append(out, r.Kind)
	}
	sort.Strings(out)
	return slices.Compact(out)
}
func sameDocument(actual, expected document, dimensions int) bool {
	if !validVector(actual.Vector, dimensions) {
		return false
	}
	actualVector := actual.Vector
	expected.Vector = actualVector
	a, _ := memes.Fingerprint(actual)
	b, _ := memes.Fingerprint(expected)
	return a == b
}
func (c *Client) writeDocument(ctx context.Context, generation, index string, expected document) error {
	raw, _ := json.Marshal(expected)
	fp, _ := memes.Fingerprint(json.RawMessage(raw))
	event := publicationEvent{Generation: generation, Operation: "create_document", Index: index, DocumentID: documentID(expected.UseID), Fingerprint: fp}
	if err := c.record(ctx, event, "intent"); err != nil {
		return err
	}
	actual, found, err := c.getDocument(ctx, index, expected.UseID)
	if err != nil {
		return err
	}
	if found {
		if !sameDocument(actual, expected, c.options.Dimensions) {
			return ErrConflict
		}
	} else {
		status, body, writeErr := c.request(ctx, http.MethodPut, "/"+index+"/_create/"+documentID(expected.UseID), raw, "")
		if writeErr == nil && successful(status) {
			var receipt struct {
				Result string `json:"result"`
				ID     string `json:"_id"`
				Shards struct {
					Failed int `json:"failed"`
				} `json:"_shards"`
			}
			if json.Unmarshal(body, &receipt) != nil || receipt.Result != "created" || receipt.ID != documentID(expected.UseID) || receipt.Shards.Failed != 0 {
				return ErrResponse
			}
		}
		// GET is also the recovery boundary for a 409 or unknown write outcome.
		actual, found, err = c.getDocument(ctx, index, expected.UseID)
		if err != nil {
			return err
		}
		if !found {
			if writeErr != nil {
				return writeErr
			}
			if !successful(status) {
				return &HTTPError{Status: status}
			}
			return ErrResponse
		}
		if !sameDocument(actual, expected, c.options.Dimensions) {
			return ErrConflict
		}
	}
	// Compare vectors too, not merely their shape, before certifying a write receipt.
	if !slices.Equal(actual.Vector, expected.Vector) {
		return ErrConflict
	}
	return c.record(ctx, event, "receipt")
}

// Publish creates or resumes an isolated generation and verifies every frozen source.
// Existing documents remain immutable; only a fully verified generation is frozen.
// The caller retains responsibility for activating the verified manifest.
func (c *Client) Publish(ctx context.Context, generation string, records []memes.IndexRecord) (memestore.GenerationManifest, error) {
	var empty memestore.GenerationManifest
	if c == nil || !safeName.MatchString(generation) || len(records) == 0 || len(records) > 100000 {
		return empty, ErrInvalid
	}
	frozen := make([]memes.IndexRecord, len(records))
	seen := map[string]bool{}
	for i, r := range records {
		compiled, err := memes.CompileCard(r.Full, r.Projection)
		if err != nil || seen[r.UseID] {
			return empty, ErrInvalid
		}
		a, _ := memes.Fingerprint(r)
		b, _ := memes.Fingerprint(compiled)
		if a != b {
			return empty, ErrInvalid
		}
		seen[r.UseID] = true
		frozen[i] = compiled
	}
	sort.Slice(frozen, func(i, j int) bool { return frozen[i].UseID < frozen[j].UseID })
	cards := make([]memes.UseCard, len(frozen))
	for i, r := range frozen {
		cards[i] = r.Full
	}
	corpusHash, _ := memes.Fingerprint(cards)
	projectionHash, _ := memes.Fingerprint(frozen)
	manifest := memestore.GenerationManifest{Generation: generation, CorpusHash: corpusHash, ProjectionHash: projectionHash, EmbeddingModel: c.options.EmbeddingModel, Dimension: c.options.Dimensions, RecordCount: len(frozen), Indexes: c.indexes(generation)}
	fingerprint, _ := memes.Fingerprint(manifest)
	planEvent := publicationEvent{Generation: generation, Operation: "publish_generation", Fingerprint: fingerprint, Manifest: &manifest}
	if err := c.record(ctx, planEvent, "intent"); err != nil {
		return empty, err
	}
	exists := make([]bool, len(fields))
	writeBlocked := make([]bool, len(fields))
	for i, field := range fields {
		found, err := c.checkMapping(ctx, manifest.Indexes[i], expectedMeta(manifest, field))
		if err != nil {
			return empty, err
		}
		exists[i] = found
		if found {
			writeBlocked[i], err = c.readWriteBlock(ctx, manifest.Indexes[i])
			if err != nil {
				return empty, err
			}
		}
	}
	type pendingDocument struct {
		index int
		body  document
	}
	var docs []pendingDocument
	var missing []int
	descriptionVectors := map[[32]byte][]float32{}
	for i, field := range fields {
		for _, r := range frozen {
			text := projectionText(r, field)
			if text == "" {
				continue
			}
			expected := document{UseID: r.UseID, Text: text, Full: r.Full, Projection: r.Projection, Fingerprint: r.Fingerprint, RelationKinds: relationKinds(r)}
			var actual document
			found := false
			var err error
			if exists[i] {
				actual, found, err = c.getDocument(ctx, manifest.Indexes[i], r.UseID)
				if err != nil {
					return empty, err
				}
			}
			if found {
				if !sameDocument(actual, expected, c.options.Dimensions) {
					return empty, ErrConflict
				}
				expected.Vector = actual.Vector
				if field == memes.FieldDescription {
					descriptionVectors[sha256.Sum256([]byte(text))] = actual.Vector
				}
			} else {
				if writeBlocked[i] {
					return empty, ErrConflict
				}
				missing = append(missing, len(docs))
			}
			docs = append(docs, pendingDocument{index: i, body: expected})
		}
	}
	for i, field := range fields {
		if err := c.ensureIndex(ctx, manifest.Indexes[i], expectedMeta(manifest, field)); err != nil {
			return empty, err
		}
	}
	counts := make([]int, len(fields))
	for _, doc := range docs {
		counts[doc.index]++
		if len(doc.body.Vector) > 0 {
			// Recover a missing receipt without rewriting a committed document.
			if err := c.writeDocument(ctx, generation, manifest.Indexes[doc.index], doc.body); err != nil {
				return empty, err
			}
		}
	}
	for start := 0; start < len(missing); start += 64 {
		end := start + 64
		if end > len(missing) {
			end = len(missing)
		}
		texts := make([]publicationText, end-start)
		for i, pos := range missing[start:end] {
			texts[i] = publicationText{text: docs[pos].body.Text, description: fields[docs[pos].index] == memes.FieldDescription}
		}
		vectors, err := c.embedPublicationBatch(ctx, generation, texts, descriptionVectors)
		if err != nil {
			return empty, err
		}
		for i, pos := range missing[start:end] {
			docs[pos].body.Vector = slices.Clone(vectors[i])
			// Checkpoint each completed batch before asking the provider for more.
			// Retries read these immutable vectors and only embed missing documents.
			if err := c.writeDocument(ctx, generation, manifest.Indexes[docs[pos].index], docs[pos].body); err != nil {
				return empty, err
			}
		}
	}
	for i, index := range manifest.Indexes {
		status, raw, err := c.request(ctx, http.MethodPost, "/"+index+"/_refresh", nil, "")
		if err != nil {
			return empty, err
		}
		if !successful(status) {
			return empty, &HTTPError{Status: status}
		}
		var refreshed struct {
			Shards struct {
				Failed int `json:"failed"`
			} `json:"_shards"`
		}
		if json.Unmarshal(raw, &refreshed) != nil || refreshed.Shards.Failed > 0 {
			return empty, ErrResponse
		}
		status, raw, err = c.request(ctx, http.MethodGet, "/"+index+"/_count", nil, "")
		if err != nil {
			return empty, err
		}
		if !successful(status) {
			return empty, &HTTPError{Status: status}
		}
		var result struct {
			Count  *int `json:"count"`
			Shards struct {
				Failed int `json:"failed"`
			} `json:"_shards"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Count == nil || result.Shards.Failed > 0 {
			return empty, ErrResponse
		}
		if *result.Count != counts[i] {
			return empty, ErrConflict
		}
	}
	for _, index := range manifest.Indexes {
		if err := c.freezeIndex(ctx, generation, index); err != nil {
			return empty, err
		}
	}
	manifest.Verified = true
	receipt := publicationEvent{Generation: generation, Operation: "publish_generation", Fingerprint: fingerprint, Manifest: &manifest}
	if err := c.record(ctx, receipt, "receipt"); err != nil {
		return empty, err
	}
	return manifest, nil
}

func (c *Client) readWriteBlock(ctx context.Context, index string) (bool, error) {
	status, raw, err := c.request(ctx, http.MethodGet, "/"+index+"/_settings", nil, "")
	if err != nil {
		return false, err
	}
	if !successful(status) {
		return false, &HTTPError{Status: status}
	}
	var response map[string]struct {
		Settings struct {
			Index struct {
				Blocks map[string]string `json:"blocks"`
			} `json:"index"`
		} `json:"settings"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return false, ErrResponse
	}
	settings, ok := response[index]
	if !ok {
		return false, ErrResponse
	}
	return settings.Settings.Index.Blocks["write"] == "true", nil
}
func (c *Client) freezeIndex(ctx context.Context, generation, index string) error {
	raw := []byte(`{"index":{"blocks.write":true}}`)
	fp, _ := memes.Fingerprint(json.RawMessage(raw))
	event := publicationEvent{Generation: generation, Operation: "freeze_index", Index: index, Fingerprint: fp}
	if err := c.record(ctx, event, "intent"); err != nil {
		return err
	}
	frozen, err := c.readWriteBlock(ctx, index)
	if err != nil {
		return err
	}
	if !frozen {
		status, body, writeErr := c.request(ctx, http.MethodPut, "/"+index+"/_settings", raw, "")
		frozen, err = c.readWriteBlock(ctx, index)
		if err != nil {
			return err
		}
		if !frozen {
			if writeErr != nil {
				return writeErr
			}
			if !successful(status) {
				return &HTTPError{Status: status}
			}
			return ErrResponse
		}
		if writeErr == nil && successful(status) {
			var ack struct {
				Acknowledged bool `json:"acknowledged"`
			}
			if json.Unmarshal(body, &ack) != nil || !ack.Acknowledged {
				return ErrResponse
			}
		}
	}
	return c.record(ctx, event, "receipt")
}

func engineForDimensions(dimensions int) string {
	if dimensions > 1024 {
		return "faiss"
	}
	return "lucene"
}
func (c *Client) vectorEngine() string { return engineForDimensions(c.options.Dimensions) }

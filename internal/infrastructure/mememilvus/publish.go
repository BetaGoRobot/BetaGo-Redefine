package mememilvus

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

// Publish does not activate a generation. The scoped PostgreSQL advisory
// transaction lock serializes publishers, including retries after restart.
func (c *Client) Publish(ctx context.Context, generation string, records []memes.IndexRecord) (memestore.GenerationManifest, error) {
	var manifest memestore.GenerationManifest
	if c == nil || c.options.Records == nil || !safeName.MatchString(generation) || len(records) == 0 || len(records) > 100000 {
		return manifest, ErrInvalid
	}
	locker, ok := c.options.Records.(publicationLocker)
	if !ok {
		return manifest, ErrInvalid
	}
	frozen := make([]memes.IndexRecord, len(records))
	seen := map[string]bool{}
	for i, r := range records {
		compiled, e := memes.CompileCard(r.Full, r.Projection)
		if e != nil || seen[r.UseID] {
			return manifest, ErrInvalid
		}
		a, _ := memes.Fingerprint(r)
		b, _ := memes.Fingerprint(compiled)
		if a != b {
			return manifest, ErrInvalid
		}
		for _, f := range fields {
			if f != memes.FieldDescription && len(textFor(r, f)) > 65535 {
				return manifest, ErrInvalid
			}
		}
		if len(relationKinds(r)) > 128 || len(r.UseID) > 1024 {
			return manifest, ErrInvalid
		}
		seen[r.UseID] = true
		frozen[i] = compiled
	}
	sort.Slice(frozen, func(i, j int) bool { return frozen[i].UseID < frozen[j].UseID })
	cards := make([]memes.UseCard, len(frozen))
	for i, r := range frozen {
		cards[i] = r.Full
	}
	corpus, _ := memes.Fingerprint(cards)
	projection, _ := memes.Fingerprint(frozen)
	manifest = memestore.GenerationManifest{Backend: "milvus", Generation: generation, CorpusHash: corpus, ProjectionHash: projection, EmbeddingModel: c.options.EmbeddingModel, Dimension: c.options.Dimensions, RecordCount: len(frozen), Indexes: c.indexes(generation)}
	err := locker.WithPublicationLock(ctx, "milvus:"+manifest.Indexes[0], func(locked context.Context) error { return c.publishLocked(locked, &manifest, frozen) })
	if err != nil {
		return memestore.GenerationManifest{}, err
	}
	return manifest, nil
}

func (c *Client) publishLocked(ctx context.Context, m *memestore.GenerationManifest, records []memes.IndexRecord) error {
	planID := m.Generation + ":milvus:plan"
	sealID := m.Generation + ":milvus:verified"
	var seal memestore.GenerationManifest
	sealed, e := c.get(ctx, sealID, &seal)
	if e != nil {
		return e
	}
	if sealed {
		expected := *m
		expected.Verified = true
		a, _ := memes.Fingerprint(seal)
		b, _ := memes.Fingerprint(expected)
		if a != b {
			return ErrConflict
		}
	} else {
		if e = c.put(ctx, planID, struct {
			Schema, Policy string
			Manifest       memestore.GenerationManifest
		}{schemaVersion, descriptionEmbeddingPolicy, *m}); e != nil {
			return e
		}
	}
	for _, r := range records {
		if sealed {
			stored, e := c.options.Records.Get(ctx, memestore.IndexCard, r.Fingerprint)
			if e != nil {
				return ErrConflict
			}
			var actual memes.IndexRecord
			if json.Unmarshal(stored.Body, &actual) != nil {
				return ErrConflict
			}
			a, _ := memes.Fingerprint(actual)
			b, _ := memes.Fingerprint(r)
			if a != b {
				return ErrConflict
			}
		} else {
			if _, e := c.options.Records.Put(ctx, memestore.IndexCard, r.Fingerprint, r); e != nil {
				return ErrLedger
			}
		}
	}
	cache := map[string][]float32{}
	for fi, field := range fields {
		name := m.Indexes[fi]
		if e = c.ensureCollection(ctx, m.Generation, name, field, sealed); e != nil {
			return e
		}
		var relevant []memes.IndexRecord
		for _, r := range records {
			if textFor(r, field) != "" {
				relevant = append(relevant, r)
			}
		}
		for start := 0; start < len(relevant); start += 64 {
			batch := relevant[start:min(start+64, len(relevant))]
			texts := make([]string, len(batch))
			ids := make([]string, len(batch))
			for i, r := range batch {
				texts[i] = textFor(r, field)
				ids[i] = r.UseID
			}
			vectors, e := c.checkpointVectors(ctx, texts, field == memes.FieldDescription, cache, sealed)
			if e != nil {
				return e
			}
			expected := make([]document, len(batch))
			for i, r := range batch {
				expected[i] = documentFor(r, field)
				expected[i].Vector = vectors[i]
			}
			if e = c.publishBatch(ctx, m.Generation, name, ids, expected, sealed); e != nil {
				return e
			}
		}
		if !sealed {
			if e = c.request(ctx, "collections/flush", map[string]any{"collectionName": name}, nil); e != nil {
				return e
			}
		}
		n, e := c.count(ctx, name)
		if e != nil {
			return e
		}
		if n != len(relevant) {
			return ErrConflict
		}
	}
	m.Verified = true
	if sealed {
		return nil
	}
	if e = c.put(ctx, sealID, *m); e != nil {
		return e
	}
	if c.options.Ledger != nil {
		if _, e = c.options.Ledger.Put(ctx, memestore.Publication, sealID, *m); e != nil {
			return ErrLedger
		}
	}
	return nil
}

func (c *Client) publishBatch(ctx context.Context, generation, name string, ids []string, expected []document, sealed bool) error {
	actual, e := c.readDocuments(ctx, name, ids)
	if e != nil {
		return e
	}
	var missing []document
	for _, d := range expected {
		a, found := actual[d.UseID]
		if found {
			if !equalDocument(a, d) {
				return ErrConflict
			}
		} else {
			missing = append(missing, d)
		}
	}
	key := batchKey(generation, name, expected)
	var prior json.RawMessage
	pending, e := c.get(ctx, key+":intent", &prior)
	if e != nil {
		return e
	}
	var receipt json.RawMessage
	done, e := c.get(ctx, key+":receipt", &receipt)
	if e != nil {
		return e
	}
	if len(missing) > 0 {
		if sealed || done {
			return ErrConflict
		}
		if pending {
			return ErrUnknownOutcome
		}
		if e = c.put(ctx, key+":intent", missing); e != nil {
			return e
		}
		writeErr := c.request(ctx, "entities/insert", map[string]any{"collectionName": name, "data": missing}, nil)
		actual, e = c.readDocuments(ctx, name, ids)
		if e != nil {
			return errors.Join(ErrUnknownOutcome, e)
		}
		for _, d := range expected {
			a, found := actual[d.UseID]
			if !found {
				return errors.Join(ErrUnknownOutcome, writeErr)
			}
			if !equalDocument(a, d) {
				return ErrConflict
			}
		}
	}
	if sealed {
		return nil
	}
	return c.put(ctx, key+":receipt", expected)
}

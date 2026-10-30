package memestore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

const knowledgeChunkBytes = 512 * 1024
const maxKnowledgeSnapshotBytes = 64 * 1024 * 1024

type knowledgeChunkRef struct{ ID, Fingerprint string }
type knowledgeManifest struct {
	EntryFingerprints map[string]string      `json:"entry_fingerprints"`
	SchemaVersion     int                    `json:"schema_version"`
	Binding           memes.KnowledgeBinding `json:"binding"`
	Fingerprint       string                 `json:"fingerprint"`
	EntryCount        int                    `json:"entry_count"`
	Chunks            []knowledgeChunkRef    `json:"chunks"`
}

func KnowledgeBinding(scope string, m GenerationManifest) memes.KnowledgeBinding {
	return memes.KnowledgeBinding{Scope: scope, Generation: m.Generation, CorpusHash: m.CorpusHash, ProjectionHash: m.ProjectionHash}
}
func knowledgeID(binding memes.KnowledgeBinding) string {
	fp, _ := memes.Fingerprint(binding)
	return "knowledge-v1:" + fp
}

// GenerationRecords reconstructs exact membership from a sealed Milvus
// generation's relation receipts. Every compiled record has a relation.
// Historical projections in the scope are deliberately excluded. Corpus and
// projection hashes independently verify the complete, sorted result.
func (s *Scoped) GenerationRecords(ctx context.Context, m GenerationManifest) ([]memes.IndexRecord, error) {
	if !s.valid() || !m.Verified || m.Backend != "milvus" || len(m.Indexes) != 4 || m.RecordCount < 1 || m.RecordCount > 100000 {
		return nil, ErrInvalid
	}
	seal, err := s.Get(ctx, Publication, m.Generation+":milvus:verified")
	if err != nil {
		return nil, err
	}
	a, _, err := canonical(m)
	if err != nil || string(a) != string(seal.Body) {
		return nil, ErrConflict
	}
	prefix := m.Generation + ":milvus:insert:" + m.Indexes[0] + ":"
	// JSON is reduced inside PG: vectors never cross the operator connection.
	rows, err := s.store.db.QueryContext(ctx, `WITH membership AS (
	 SELECT DISTINCT d->>'fingerprint' AS fingerprint
	 FROM `+s.store.table(string(Publication))+` p
	 CROSS JOIN LATERAL jsonb_array_elements(convert_from(p.body,'UTF8')::jsonb) d
	 WHERE p.scope_key=$1 AND left(p.id,length($2))=$2 AND right(p.id,8)=':receipt'
	) SELECT r.id,r.fingerprint,r.body,r.created_at FROM membership m
	 JOIN `+s.store.table(string(IndexCard))+` r ON r.scope_key=$1 AND r.id=m.fingerprint`, s.scope, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]memes.IndexRecord, 0, m.RecordCount)
	seen := map[string]bool{}
	for rows.Next() {
		var row Record
		if err = rows.Scan(&row.ID, &row.Fingerprint, &row.Body, &row.CreatedAt); err != nil {
			return nil, err
		}
		_, fp, e := canonical(row.Body)
		var record memes.IndexRecord
		if e != nil || fp != row.Fingerprint || json.Unmarshal(row.Body, &record) != nil || record.Fingerprint != row.ID || seen[record.UseID] {
			return nil, ErrConflict
		}
		compiled, e := memes.CompileCard(record.Full, record.Projection)
		x, _ := memes.Fingerprint(record)
		y, _ := memes.Fingerprint(compiled)
		if e != nil || x != y {
			return nil, ErrConflict
		}
		seen[record.UseID] = true
		records = append(records, record)
		if len(records) > m.RecordCount {
			return nil, ErrConflict
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(records) != m.RecordCount {
		return nil, ErrConflict
	}
	sort.Slice(records, func(i, j int) bool { return records[i].UseID < records[j].UseID })
	cards := make([]memes.UseCard, len(records))
	for i, r := range records {
		cards[i] = r.Full
	}
	corpus, _ := memes.Fingerprint(cards)
	projection, _ := memes.Fingerprint(records)
	if corpus != m.CorpusHash || projection != m.ProjectionHash {
		return nil, ErrConflict
	}
	return records, nil
}

// PublishKnowledge adds an immutable sidecar without changing the active corpus
// pointer, card records, embeddings or search collections. The manifest seals
// chunks last, so readers cannot observe a partially published snapshot.
func (s *Scoped) PublishKnowledge(ctx context.Context, m GenerationManifest, snapshot memes.KnowledgeSnapshot) error {
	if !s.valid() || !m.Verified {
		return ErrInvalid
	}
	binding := KnowledgeBinding(s.scope, m)
	if err := memes.ValidateKnowledgeSnapshot(snapshot, binding); err != nil {
		return ErrInvalid
	}
	if err := knowledgeEncodedSize(snapshot.Entries, maxKnowledgeSnapshotBytes); err != nil {
		return err
	}
	seal, err := s.Get(ctx, Generation, m.Generation)
	raw, _, e := canonical(m)
	if err != nil {
		return err
	}
	if e != nil || string(seal.Body) != string(raw) {
		return ErrConflict
	}
	// A caller-provided binding does not establish membership. Reconstruct the
	// sealed set and compare its deterministic compilation before sealing.
	records, err := s.GenerationRecords(ctx, m)
	if err != nil {
		return err
	}
	expected, err := memes.CompileKnowledgeSnapshot(binding, records)
	if err != nil || expected.Fingerprint != snapshot.Fingerprint {
		return ErrConflict
	}
	base := knowledgeID(binding)
	manifest := knowledgeManifest{SchemaVersion: snapshot.SchemaVersion, Binding: binding, Fingerprint: snapshot.Fingerprint, EntryCount: len(snapshot.Entries)}
	manifest.EntryFingerprints = map[string]string{}
	for _, entry := range snapshot.Entries {
		fp, err := memes.Fingerprint(entry)
		if err != nil {
			return ErrInvalid
		}
		manifest.EntryFingerprints[entry.ID] = fp
	}
	metadata, _, err := canonical(manifest)
	if err != nil || len(metadata) > (8<<20)-(64<<10) {
		return ErrInvalid
	}
	return s.WithPublicationLock(ctx, base, func(locked context.Context) error {
		var batch []memes.KnowledgeEntry
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			id := fmt.Sprintf("%s:chunk:%06d", base, len(manifest.Chunks))
			row, err := s.Put(locked, Knowledge, id, batch)
			if err != nil {
				return err
			}
			manifest.Chunks = append(manifest.Chunks, knowledgeChunkRef{ID: id, Fingerprint: row.Fingerprint})
			batch = nil
			return nil
		}
		size := 2
		for _, entry := range snapshot.Entries {
			encoded, err := json.Marshal(entry)
			if err != nil || len(encoded) > knowledgeChunkBytes {
				return ErrInvalid
			}
			if size+len(encoded)+1 > knowledgeChunkBytes {
				if err = flush(); err != nil {
					return err
				}
				size = 2
			}
			batch = append(batch, entry)
			size += len(encoded) + 1
		}
		if err := flush(); err != nil {
			return err
		}
		_, err := s.Put(locked, Knowledge, base, manifest)
		return err
	})
}

// Match the writer's chunk grouping and the reader's cumulative byte limit
// before the first immutable Put, so publication cannot seal an unreadable set.
func knowledgeEncodedSize(entries []memes.KnowledgeEntry, totalLimit int) error {
	if totalLimit < 1 {
		return ErrInvalid
	}
	size, total, count := 2, 0, 0
	for _, entry := range entries {
		raw, err := json.Marshal(entry)
		if err != nil || len(raw)+2 > knowledgeChunkBytes {
			return ErrInvalid
		}
		if size+len(raw)+1 > knowledgeChunkBytes {
			if count > 0 {
				total += size - 1
			}
			size = 2
			count = 0
		}
		size += len(raw) + 1
		count++
		if total+size-1 > totalLimit {
			return ErrInvalid
		}
	}
	return nil
}

func (s *Scoped) Knowledge(ctx context.Context, m GenerationManifest) (memes.KnowledgeSnapshot, error) {
	var snapshot memes.KnowledgeSnapshot
	if !s.valid() || !m.Verified {
		return snapshot, ErrInvalid
	}
	binding := KnowledgeBinding(s.scope, m)
	base := knowledgeID(binding)
	row, err := s.Get(ctx, Knowledge, base)
	if err != nil {
		return snapshot, err
	}
	var manifest knowledgeManifest
	if json.Unmarshal(row.Body, &manifest) != nil || manifest.Binding != binding || manifest.EntryCount < 1 || manifest.EntryCount > 100000 || len(manifest.Chunks) < 1 || len(manifest.Chunks) > 10000 {
		return snapshot, ErrConflict
	}
	snapshot = memes.KnowledgeSnapshot{SchemaVersion: manifest.SchemaVersion, Binding: binding, Fingerprint: manifest.Fingerprint, Entries: make([]memes.KnowledgeEntry, 0, manifest.EntryCount)}
	totalBytes := 0
	for i, ref := range manifest.Chunks {
		if ref.ID != fmt.Sprintf("%s:chunk:%06d", base, i) || !strings.HasPrefix(ref.ID, base+":chunk:") {
			return memes.KnowledgeSnapshot{}, ErrConflict
		}
		row, err = s.Get(ctx, Knowledge, ref.ID)
		if err != nil {
			return memes.KnowledgeSnapshot{}, err
		}
		var entries []memes.KnowledgeEntry
		totalBytes += len(row.Body)
		if totalBytes > maxKnowledgeSnapshotBytes {
			return memes.KnowledgeSnapshot{}, ErrConflict
		}
		if row.Fingerprint != ref.Fingerprint || len(row.Body) > knowledgeChunkBytes || json.Unmarshal(row.Body, &entries) != nil {
			return memes.KnowledgeSnapshot{}, ErrConflict
		}
		snapshot.Entries = append(snapshot.Entries, entries...)
		if len(snapshot.Entries) > manifest.EntryCount {
			return memes.KnowledgeSnapshot{}, ErrConflict
		}
	}
	if len(snapshot.Entries) != manifest.EntryCount || len(manifest.EntryFingerprints) != manifest.EntryCount || memes.ValidateKnowledgeSnapshot(snapshot, binding) != nil {
		return memes.KnowledgeSnapshot{}, ErrConflict
	}
	for _, entry := range snapshot.Entries {
		fp, err := memes.Fingerprint(entry)
		if err != nil || manifest.EntryFingerprints[entry.ID] != fp {
			return memes.KnowledgeSnapshot{}, ErrConflict
		}
	}
	return snapshot, nil
}

// ValidateKnowledgeEnvironment binds a restored view to the immutable sidecar
// without loading the entire corpus. Current withdrawal checks remain separate.
func (s *Scoped) ValidateKnowledgeEnvironment(ctx context.Context, c *memes.GroundedContext) error {
	if c == nil {
		return nil
	}
	if !s.valid() || c.Binding.Scope != s.scope {
		return ErrInvalid
	}
	row, err := s.Get(ctx, Generation, c.Binding.Generation)
	if err != nil {
		return err
	}
	var generation GenerationManifest
	if json.Unmarshal(row.Body, &generation) != nil || !generation.Verified || KnowledgeBinding(s.scope, generation) != c.Binding {
		return ErrConflict
	}
	row, err = s.Get(ctx, Knowledge, knowledgeID(c.Binding))
	if err != nil {
		return err
	}
	var manifest knowledgeManifest
	if json.Unmarshal(row.Body, &manifest) != nil || manifest.Binding != c.Binding || manifest.Fingerprint != c.KnowledgeFingerprint {
		return ErrConflict
	}
	for _, entry := range c.PriorKnowledge {
		if entry.Binding != c.Binding || entry.EntryFingerprint == "" || manifest.EntryFingerprints[entry.ID] != entry.EntryFingerprint {
			return ErrConflict
		}
	}
	return nil
}

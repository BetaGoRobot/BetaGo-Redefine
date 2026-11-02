package memestore

import (
	"context"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/lib/pq"
)

// WithdrawSource is an immutable scope-wide withdrawal across corpus versions.
// It is an operator API, never exposed as a chat capability. A corrected source
// can be published with a new identity; old withdrawn versions stay withdrawn.
func (s *Scoped) WithdrawSource(ctx context.Context, source memes.SourceMetadata) error {
	if source.Kind == "" || source.Title == "" && source.URL == "" {
		return ErrInvalid
	}
	key := memes.KnowledgeSourceKey(source)
	_, err := s.Put(ctx, Withdrawal, key, struct {
		Key string `json:"source_key"`
	}{key})
	return err
}

func (s *Scoped) withdrawn(ctx context.Context, keys []string) (map[string]bool, error) {
	denied := map[string]bool{}
	if !s.valid() {
		return nil, ErrInvalid
	}
	if len(keys) == 0 {
		return denied, nil
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT id FROM `+s.store.table(string(Withdrawal))+` WHERE scope_key=$1 AND id=ANY($2)`, s.scope, pq.Array(keys))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		denied[key] = true
	}
	return denied, rows.Err()
}

func (s *Scoped) CheckKnowledge(ctx context.Context, selection memes.KnowledgeSelection) (memes.KnowledgeSelection, error) {
	if !s.valid() || selection.Binding.Scope != s.scope {
		return memes.KnowledgeSelection{}, ErrInvalid
	}
	keys, recordIDs := []string{}, []string{}
	for _, entry := range selection.Entries {
		if entry.Binding != selection.Binding {
			return memes.KnowledgeSelection{}, ErrInvalid
		}
		for _, source := range entry.Sources {
			keys = append(keys, memes.KnowledgeSourceKey(source))
		}
		recordIDs = append(recordIDs, entry.RecordFingerprints...)
	}
	denied, err := s.withdrawn(ctx, keys)
	if err != nil {
		return memes.KnowledgeSelection{}, err
	}
	present := map[string]bool{}
	if len(recordIDs) > 0 {
		rows, err := s.store.db.QueryContext(ctx, `SELECT id FROM `+s.store.table(string(IndexCard))+` WHERE scope_key=$1 AND id=ANY($2)`, s.scope, pq.Array(recordIDs))
		if err != nil {
			return memes.KnowledgeSelection{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return memes.KnowledgeSelection{}, err
			}
			present[id] = true
		}
		if err = rows.Err(); err != nil {
			return memes.KnowledgeSelection{}, err
		}
	}
	allowed := map[string]bool{}
	for _, entry := range selection.Entries {
		visible := len(entry.RecordFingerprints) > 0
		for _, fp := range entry.RecordFingerprints {
			visible = visible && present[fp]
		}
		for _, source := range entry.Sources {
			visible = visible && !denied[memes.KnowledgeSourceKey(source)]
		}
		allowed[entry.ID] = visible
	}
	return memes.FilterKnowledgeSelection(selection, allowed), nil
}

func (s *Scoped) FilterUseCards(ctx context.Context, cards []memes.UseCard) ([]memes.UseCard, error) {
	keys := []string{}
	for _, card := range cards {
		for _, source := range card.Sources {
			keys = append(keys, memes.KnowledgeSourceKey(source))
		}
	}
	denied, err := s.withdrawn(ctx, keys)
	if err != nil {
		return nil, err
	}
	filtered := []memes.UseCard{}
	for _, card := range cards {
		visible := true
		for _, source := range card.Sources {
			visible = visible && !denied[memes.KnowledgeSourceKey(source)]
		}
		if visible {
			filtered = append(filtered, card)
		}
	}
	return filtered, nil
}

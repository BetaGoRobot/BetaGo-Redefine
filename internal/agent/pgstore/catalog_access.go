package pgstore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// CatalogAccess is the cheap, current per-call admission snapshot. It deliberately
// excludes source-bound prompt preferences; actual invocations must fetch it
// afresh rather than reusing the tool-advertisement snapshot.
func (s *Store) CatalogAccess(ctx context.Context, scope domain.Scope) (domain.CatalogRuntime, error) {
	out := domain.CatalogRuntime{Features: map[string]bool{}, Rules: []domain.CatalogReplyRule{}, Phrases: []domain.CatalogPhrase{}, Corrections: []domain.CatalogCorrection{}, Sources: []domain.SourceRef{}, Inactive: []domain.CatalogInactive{}}
	if scope.Validate() != nil {
		return out, domain.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM `+s.table("catalog_entries")+` WHERE scope_key=$1 AND NOT deleted AND (kind='feature' OR (kind='group_config' AND name IN ('mute_until','history_since'))) ORDER BY kind,name`, scope.Key())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var body []byte
		var entry domain.CatalogEntry
		if err = rows.Scan(&body); err != nil {
			return out, err
		}
		if err = json.Unmarshal(body, &entry); err != nil {
			return out, err
		}
		if !catalogValueValid(domain.CatalogPutInput{Kind: entry.Kind, Name: entry.Name, CatalogValue: entry.CatalogValue}) {
			return out, domain.ErrInvalid
		}
		switch entry.Kind {
		case domain.CatalogFeature:
			out.Features[entry.Name] = entry.Feature.Enabled
		case domain.CatalogGroupConfig:
			at, e := time.Parse(time.RFC3339Nano, entry.Config.Value)
			if e != nil {
				return out, e
			}
			if entry.Name == "mute_until" {
				out.MuteUntil = at
			} else {
				out.HistorySince = at
			}
		}
	}
	return out, rows.Err()
}

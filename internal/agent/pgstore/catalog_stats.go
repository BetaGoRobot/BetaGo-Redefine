package pgstore

import (
	"context"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type CatalogCapacity struct {
	Kind     domain.CatalogKind `json:"kind"`
	Names    int                `json:"names"`
	Active   int                `json:"active"`
	MaxNames int                `json:"max_names"`
	Enforced bool               `json:"enforced"`
}
type CatalogCapacityReport struct {
	Kinds                []CatalogCapacity `json:"kinds"`
	ImportedLegacyLimits bool              `json:"imported_legacy_limits"`
}

func (s *Store) CatalogCapacityStats(ctx context.Context, scope domain.Scope) (CatalogCapacityReport, error) {
	out := CatalogCapacityReport{Kinds: []CatalogCapacity{}}
	if scope.Validate() != nil {
		return out, domain.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT kind,count(*),count(*) FILTER(WHERE NOT deleted) FROM `+s.table("catalog_entries")+` WHERE scope_key=$1 GROUP BY kind`, scope.Key())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	byKind := map[domain.CatalogKind]CatalogCapacity{}
	for rows.Next() {
		var c CatalogCapacity
		if err = rows.Scan(&c.Kind, &c.Names, &c.Active); err != nil {
			return out, err
		}
		byKind[c.Kind] = c
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	for _, kind := range []domain.CatalogKind{domain.CatalogAsset, domain.CatalogPhraseKind, domain.CatalogReplyRuleKind, domain.CatalogGroupConfig, domain.CatalogFeature, domain.CatalogPermission, domain.CatalogCorrectionKind} {
		c := byKind[kind]
		c.Kind = kind
		c.MaxNames = 100
		c.Enforced = true
		out.Kinds = append(out.Kinds, c)
	}
	return out, nil
}

type CatalogExecutionCount struct {
	Tool   string `json:"tool"`
	Status string `json:"status"`
	Count  int64  `json:"count"`
}
type CatalogExecutionReport struct {
	Since                time.Time               `json:"since"`
	Until                time.Time               `json:"until"`
	Rows                 []CatalogExecutionCount `json:"rows"`
	ImportedLegacyLimits bool                    `json:"imported_legacy_limits"`
	Measured             string                  `json:"measured"`
}

func (s *Store) CatalogExecutionStats(ctx context.Context, scope domain.Scope) (CatalogExecutionReport, error) {
	now := time.Now().UTC()
	out := CatalogExecutionReport{Since: now.Add(-time.Hour), Until: now, Rows: []CatalogExecutionCount{}, Measured: "tool ledger entries created in the last hour, grouped by tool and current status"}
	if scope.Validate() != nil {
		return out, domain.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT convert_from(body,'UTF8')::jsonb->>'Tool',status,count(*) FROM `+s.table("tool_executions")+` WHERE scope_key=$1 AND (convert_from(body,'UTF8')::jsonb->>'CreatedAt')::timestamptz >= $2 AND (convert_from(body,'UTF8')::jsonb->>'CreatedAt')::timestamptz <= $3 GROUP BY 1,2 ORDER BY 1,2`, scope.Key(), out.Since, out.Until)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var c CatalogExecutionCount
		if err = rows.Scan(&c.Tool, &c.Status, &c.Count); err != nil {
			return out, err
		}
		out.Rows = append(out.Rows, c)
	}
	return out, rows.Err()
}

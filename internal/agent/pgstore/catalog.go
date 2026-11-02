package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// CatalogAdmin is a server-owned bootstrap authority. A nil resolver denies
// administrative changes. Persisted catalog grants never acquire this power.
type CatalogAdmin func(context.Context, domain.Scope, string) (bool, error)

func (s *Store) MigrateCatalog(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS ` + s.table("catalog_entries") + ` (scope_key text NOT NULL,kind text NOT NULL,name text NOT NULL,revision bigint NOT NULL CHECK(revision>0),deleted boolean NOT NULL DEFAULT false,creator text NOT NULL,body bytea NOT NULL,updated_at timestamptz NOT NULL,PRIMARY KEY(scope_key,kind,name))`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("catalog_mutations") + ` (scope_key text NOT NULL,task_id text NOT NULL,execution_key text NOT NULL,body bytea NOT NULL,PRIMARY KEY(scope_key,task_id,execution_key))`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func catalogValueValid(in domain.CatalogPutInput) bool {
	if !in.Kind.Valid() || !domain.CatalogNameValid(in.Name) || in.ExpectedRevision < 0 || in.ExpectedRevision == int64(^uint64(0)>>1) {
		return false
	}
	n := 0
	for _, v := range []bool{in.Asset != nil, in.Phrase != nil, in.Rule != nil, in.Config != nil, in.Feature != nil, in.Permission != nil, in.Correction != nil} {
		if v {
			n++
		}
	}
	if n != 1 {
		return false
	}
	switch in.Kind {
	case domain.CatalogAsset:
		return in.Asset != nil && workText(in.Asset.SourceMessageID, 1024, false) && workText(in.Asset.ResourceID, 1024, false) && (in.Asset.Type == "image" || in.Asset.Type == "sticker") && workText(in.Asset.Description, 1024, true)
	case domain.CatalogPhraseKind:
		return in.Phrase != nil && (in.Phrase.Name == "" || in.Phrase.Name == in.Name) && workText(in.Phrase.Text, 512, false) && in.Phrase.Weight >= 1 && in.Phrase.Weight <= 100
	case domain.CatalogReplyRuleKind:
		r := in.Rule
		if r != nil && len(r.Pattern) > 512 {
			return false
		}
		if r != nil && r.Match == "regex" {
			if _, err := regexp.Compile(r.Pattern); err != nil {
				return false
			}
		}
		return r != nil && (r.Name == "" || r.Name == in.Name) && workText(r.Pattern, 512, false) && (workText(r.Reply, 4096, false) || domain.CatalogNameValid(r.AssetName)) && workText(r.Reply, 4096, true) && (r.AssetName == "" || domain.CatalogNameValid(r.AssetName)) && (r.Match == "exact" || r.Match == "contains" || r.Match == "prefix" || r.Match == "regex") && r.Probability >= 0 && r.Probability <= 1 && r.Weight >= 1 && r.Weight <= 100
	case domain.CatalogGroupConfig:
		if in.Config == nil {
			return false
		}
		if in.Name == "reply_instruction" {
			return workText(in.Config.Value, 4096, false)
		}
		if in.Name == "history_since" || in.Name == "mute_until" {
			_, err := time.Parse(time.RFC3339Nano, in.Config.Value)
			return err == nil && len(in.Config.Value) <= 64
		}
	case domain.CatalogFeature:
		return in.Feature != nil && domain.CatalogFeatureValid(in.Name)
	case domain.CatalogPermission:
		return in.Permission != nil && workText(in.Permission.SubjectID, 1024, false) && strings.TrimSpace(in.Permission.SubjectID) == in.Permission.SubjectID && domain.CatalogPermissionValid(in.Permission.Point) && in.Name == CatalogPermissionName(in.Permission.SubjectID, in.Permission.Point)
	case domain.CatalogCorrectionKind:
		return in.Correction != nil && (in.Correction.Name == "" || in.Correction.Name == in.Name) && workText(in.Correction.Text, 2048, false)
	}
	return false
}

// CatalogPermissionName produces a deterministic grant key; callers cannot hide
// multiple grants for one subject/point behind unrelated names.
func CatalogPermissionName(subject, point string) string {
	return "grant_" + domain.StableID(subject, point)[:32]
}

func (s *Store) catalogGuardTx(ctx context.Context, tx *sql.Tx, j domain.Job, t domain.Task, key, digest, name string, expected any) (domain.Task, string, error) {
	current, err := s.guardTaskTx(ctx, tx, j, t)
	if err != nil {
		return current, "", err
	}
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT enabled AND side_effect AND NOT auto_execute AND policy_digest=$2 FROM `+s.table("tool_policies")+` WHERE tool_name=$1 FOR SHARE`, name, digest).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !allowed {
		return current, "", domain.ErrUnauthorized
	}
	if err != nil {
		return current, "", err
	}
	x, err := s.toolTx(ctx, tx, t.Scope, t.ID, key)
	if errors.Is(err, domain.ErrNotFound) {
		return current, "", domain.ErrUnauthorized
	}
	if err != nil {
		return current, "", err
	}
	if x.Scope != current.Scope || x.TaskID != current.ID || x.Tool != name || x.Status != "running_effect" || x.JobID != j.ID || x.Fence != j.Fence || x.Revision != current.Revision || x.ActorID == "" || x.ApprovalID == "" {
		return current, "", domain.ErrUnauthorized
	}
	actual := reflect.New(reflect.TypeOf(expected))
	actualErr := json.Unmarshal([]byte(x.Arguments), actual.Interface())
	a, _ := json.Marshal(actual.Elem().Interface())
	b, _ := json.Marshal(expected)
	if actualErr != nil || string(a) != string(b) {
		return current, "", domain.ErrUnauthorized
	}
	approval, err := s.approvalTx(ctx, tx, t.Scope, x.ApprovalID)
	if err != nil {
		return current, "", err
	}
	if approval.Status != "approved" || approval.ActorID != x.ActorID || !current.Authorized(x.ActorID) || approval.PolicyDigest != digest || approval.Tool != name || approval.TaskID != t.ID || approval.Revision != current.Revision || approval.Arguments != x.Arguments {
		return current, "", domain.ErrUnauthorized
	}
	if approval.Source.MessageID != "" {
		if err = s.lockSourcesTx(ctx, tx, []domain.SourceRef{approval.Source}); err != nil {
			return current, "", err
		}
		if err = s.checkSourcesTx(ctx, tx, []domain.SourceRef{approval.Source}); err != nil {
			return current, "", err
		}
	}
	// Serialize catalog grants and their protected writes in the same group.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, t.Scope.Key()+":catalog"); err != nil {
		return current, "", err
	}
	return current, x.ActorID, s.checkRuntimeClaimTx(ctx, tx, j, current)
}
func (s *Store) catalogAdminTx(ctx context.Context, tx *sql.Tx, scope domain.Scope, actor string, kind domain.CatalogKind, admin CatalogAdmin) (bool, bool, error) {
	bootstrap := false
	if admin != nil {
		var err error
		bootstrap, err = admin(ctx, scope, actor)
		if err != nil {
			return false, false, domain.ErrUnauthorized
		}
	}
	if bootstrap {
		return true, true, nil
	}
	if kind == domain.CatalogPermission {
		return false, false, nil
	}
	name := CatalogPermissionName(actor, string(kind)+".write")
	var body []byte
	err := tx.QueryRowContext(ctx, `SELECT body FROM `+s.table("catalog_entries")+` WHERE scope_key=$1 AND kind='permission' AND name=$2 AND NOT deleted FOR SHARE`, scope.Key(), name).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	var entry domain.CatalogEntry
	if err = json.Unmarshal(body, &entry); err != nil {
		return false, false, err
	}
	return entry.Permission != nil && entry.Permission.SubjectID == actor && entry.Permission.Point == string(kind)+".write", false, nil
}
func (s *Store) catalogEntryTx(ctx context.Context, tx *sql.Tx, scope domain.Scope, kind domain.CatalogKind, name string) (domain.CatalogEntry, error) {
	var out domain.CatalogEntry
	var body []byte
	err := tx.QueryRowContext(ctx, `SELECT body FROM `+s.table("catalog_entries")+` WHERE scope_key=$1 AND kind=$2 AND name=$3 FOR UPDATE`, scope.Key(), kind, name).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return out, domain.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(body, &out)
	return out, err
}
func (s *Store) catalogReplayTx(ctx context.Context, tx *sql.Tx, t domain.Task, key string) (domain.CatalogEntry, error) {
	var out domain.CatalogEntry
	var body []byte
	err := tx.QueryRowContext(ctx, `SELECT body FROM `+s.table("catalog_mutations")+` WHERE scope_key=$1 AND task_id=$2 AND execution_key=$3`, t.Scope.Key(), t.ID, key).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return out, domain.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(body, &out)
	return out, err
}
func (s *Store) saveCatalogTx(ctx context.Context, tx *sql.Tx, t domain.Task, key string, out domain.CatalogEntry) error {
	body, err := json.Marshal(out)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO `+s.table("catalog_entries")+`(scope_key,kind,name,revision,deleted,creator,body,updated_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(scope_key,kind,name) DO UPDATE SET revision=EXCLUDED.revision,deleted=EXCLUDED.deleted,body=EXCLUDED.body,updated_at=EXCLUDED.updated_at`, t.Scope.Key(), out.Kind, out.Name, out.Revision, out.Deleted, out.CreatedBy, body, out.UpdatedAt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO `+s.table("catalog_mutations")+`(scope_key,task_id,execution_key,body)VALUES($1,$2,$3,$4)`, t.Scope.Key(), t.ID, key, body)
	return err
}
func (s *Store) PutCatalog(ctx context.Context, j domain.Job, t domain.Task, key, digest string, in domain.CatalogPutInput, admin CatalogAdmin) (out domain.CatalogEntry, err error) {
	if !catalogValueValid(in) {
		return out, domain.ErrInvalid
	}
	err = s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, actor, e := s.catalogGuardTx(ctx, tx, j, t, key, digest, "catalog_put", in)
		if e != nil {
			return e
		}
		elevated, bootstrap, e := s.catalogAdminTx(ctx, tx, t.Scope, actor, in.Kind, admin)
		if e != nil {
			return e
		}
		personal := in.Kind == domain.CatalogAsset || in.Kind == domain.CatalogPhraseKind || in.Kind == domain.CatalogReplyRuleKind
		if !personal && !elevated {
			return domain.ErrUnauthorized
		}
		if in.Kind == domain.CatalogPermission && !bootstrap {
			return domain.ErrUnauthorized
		}
		old, e := s.catalogEntryTx(ctx, tx, t.Scope, in.Kind, in.Name)
		exists := e == nil
		if e != nil && !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		if exists && old.CreatedBy != actor && !elevated {
			return domain.ErrUnauthorized
		}
		replay, e := s.catalogReplayTx(ctx, tx, t, key)
		if e == nil {
			out = replay
			return nil
		}
		if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		if (exists && old.Revision != in.ExpectedRevision) || (!exists && in.ExpectedRevision != 0) {
			return domain.ErrConflict
		}
		if !exists {
			var count int
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM `+s.table("catalog_entries")+` WHERE scope_key=$1 AND kind=$2`, t.Scope.Key(), in.Kind).Scan(&count); e != nil {
				return e
			}
			if count >= 100 {
				return fmt.Errorf("%w: catalog capacity is 100 names per kind", domain.ErrConflict)
			}
		}
		if in.Rule != nil && in.Rule.AssetName != "" {
			asset, e := s.catalogEntryTx(ctx, tx, t.Scope, domain.CatalogAsset, in.Rule.AssetName)
			if e != nil {
				return e
			}
			if asset.Deleted || asset.Asset == nil || asset.AssetSource == nil {
				return domain.ErrStaleSource
			}
			if e = s.lockSourcesTx(ctx, tx, []domain.SourceRef{*asset.AssetSource}); e != nil {
				return e
			}
			if e = s.checkSourcesTx(ctx, tx, []domain.SourceRef{*asset.AssetSource}); e != nil {
				return e
			}
		}
		var source *domain.SourceRef
		if in.Asset != nil {
			m, e := s.messageTx(ctx, tx, t.Scope, in.Asset.SourceMessageID)
			if e != nil {
				return e
			}
			if m.Deleted {
				return domain.ErrStaleSource
			}
			if m.Source.AuthorID != actor && !bootstrap {
				return domain.ErrUnauthorized
			}
			if e = s.lockSourcesTx(ctx, tx, []domain.SourceRef{m.Source}); e != nil {
				return e
			}
			if e = s.checkSourcesTx(ctx, tx, []domain.SourceRef{m.Source}); e != nil {
				return e
			}
			found := false
			for _, media := range m.Media {
				if media.Type == domain.MediaImage && media.ResourceID == in.Asset.ResourceID && media.Source == domain.MediaSourceLark {
					found = true
				}
			}
			if !found {
				return domain.ErrInvalid
			}
			ref := m.Source
			source = &ref
		}
		now := time.Now().UTC()
		out = domain.CatalogEntry{Kind: in.Kind, Name: in.Name, Revision: in.ExpectedRevision + 1, CreatedBy: actor, UpdatedBy: actor, Origin: workOrigin(current), AssetSource: source, CreatedAt: now, UpdatedAt: now, CatalogValue: in.CatalogValue}
		if exists {
			out.CreatedBy = old.CreatedBy
			out.CreatedAt = old.CreatedAt
		}
		if e = s.saveCatalogTx(ctx, tx, current, key, out); e != nil {
			return e
		}
		return s.checkRuntimeClaimTx(ctx, tx, j, current)
	})
	return
}
func (s *Store) DeleteCatalog(ctx context.Context, j domain.Job, t domain.Task, key, digest string, in domain.CatalogDeleteInput, admin CatalogAdmin) (out domain.CatalogEntry, err error) {
	if !in.Kind.Valid() || !domain.CatalogNameValid(in.Name) || in.ExpectedRevision < 1 || in.ExpectedRevision == int64(^uint64(0)>>1) {
		return out, domain.ErrInvalid
	}
	err = s.runtimeTransact(ctx, j, t, func(tx *sql.Tx) error {
		current, actor, e := s.catalogGuardTx(ctx, tx, j, t, key, digest, "catalog_delete", in)
		if e != nil {
			return e
		}
		elevated, bootstrap, e := s.catalogAdminTx(ctx, tx, t.Scope, actor, in.Kind, admin)
		if e != nil {
			return e
		}
		if in.Kind != domain.CatalogAsset && in.Kind != domain.CatalogPhraseKind && in.Kind != domain.CatalogReplyRuleKind && !elevated {
			return domain.ErrUnauthorized
		}
		if in.Kind == domain.CatalogPermission && !bootstrap {
			return domain.ErrUnauthorized
		}
		old, e := s.catalogEntryTx(ctx, tx, t.Scope, in.Kind, in.Name)
		if e != nil {
			return e
		}
		if old.CreatedBy != actor && !elevated {
			return domain.ErrUnauthorized
		}
		replay, e := s.catalogReplayTx(ctx, tx, t, key)
		if e == nil {
			out = replay
			return nil
		}
		if !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		if old.Revision != in.ExpectedRevision || old.Deleted {
			return domain.ErrConflict
		}
		out = old
		out.Revision++
		out.Deleted = true
		out.UpdatedBy = actor
		out.Origin = workOrigin(current)
		out.UpdatedAt = time.Now().UTC()
		if e = s.saveCatalogTx(ctx, tx, current, key, out); e != nil {
			return e
		}
		return s.checkRuntimeClaimTx(ctx, tx, j, current)
	})
	return
}
func (s *Store) CatalogEntry(ctx context.Context, scope domain.Scope, kind domain.CatalogKind, name string) (domain.CatalogEntry, error) {
	var out domain.CatalogEntry
	if scope.Validate() != nil || !kind.Valid() || !domain.CatalogNameValid(name) {
		return out, domain.ErrInvalid
	}
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT body FROM `+s.table("catalog_entries")+` WHERE scope_key=$1 AND kind=$2 AND name=$3`, scope.Key(), kind, name).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return out, domain.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(b, &out)
	}
	if err == nil {
		out, err = s.catalogReadState(ctx, scope, out)
	}
	return out, err
}
func (s *Store) ListCatalog(ctx context.Context, scope domain.Scope, kind domain.CatalogKind, after string, limit int) ([]domain.CatalogEntry, error) {
	if scope.Validate() != nil || !kind.Valid() || (after != "" && !domain.CatalogNameValid(after)) || limit < 1 || limit > 50 {
		return nil, domain.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM `+s.table("catalog_entries")+` WHERE scope_key=$1 AND kind=$2 AND name>$3 AND NOT deleted ORDER BY name LIMIT $4`, scope.Key(), kind, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CatalogEntry{}
	for rows.Next() {
		var body []byte
		var entry domain.CatalogEntry
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &entry); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i], err = s.catalogReadState(ctx, scope, out[i])
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Store) catalogReadState(ctx context.Context, scope domain.Scope, entry domain.CatalogEntry) (domain.CatalogEntry, error) {
	if entry.Deleted {
		return entry, nil
	}
	var source *domain.SourceRef
	switch entry.Kind {
	case domain.CatalogAsset:
		if entry.Asset == nil || entry.AssetSource == nil {
			return entry, domain.ErrInvalid
		}
		source = entry.AssetSource
	case domain.CatalogPhraseKind:
		if entry.Phrase != nil && !entry.Phrase.Enabled {
			entry.InactiveReason = "disabled"
			return entry, nil
		}
		source = &entry.Origin
	case domain.CatalogReplyRuleKind:
		if entry.Rule != nil && entry.Rule.AssetName != "" {
			asset, err := s.CatalogEntry(ctx, scope, domain.CatalogAsset, entry.Rule.AssetName)
			if errors.Is(err, domain.ErrNotFound) || (err == nil && (asset.Deleted || asset.InactiveReason != "" || asset.AssetSource == nil)) {
				entry.InactiveReason = "asset_unavailable"
				return entry, nil
			}
			if err != nil {
				return entry, err
			}
		}
		if entry.Rule != nil && !entry.Rule.Enabled {
			entry.InactiveReason = "disabled"
			return entry, nil
		}
		source = &entry.Origin
	case domain.CatalogCorrectionKind:
		source = &entry.Origin
	case domain.CatalogGroupConfig:
		if entry.Name == "reply_instruction" {
			source = &entry.Origin
		}
	}
	if source != nil {
		valid, err := s.ValidSources(ctx, domain.Query{Scope: scope, Anchor: time.Now().UTC(), Mode: domain.QueryLiteral, Limit: 1}, []domain.SourceRef{*source})
		if err != nil {
			return entry, err
		}
		if !valid {
			entry.InactiveReason = "stale_source"
		}
	}
	return entry, nil
}
func (s *Store) CatalogRuntime(ctx context.Context, scope domain.Scope) (domain.CatalogRuntime, error) {
	out := domain.CatalogRuntime{Features: map[string]bool{}, Phrases: []domain.CatalogPhrase{}, Rules: []domain.CatalogReplyRule{}, Corrections: []domain.CatalogCorrection{}, Sources: []domain.SourceRef{}, Inactive: []domain.CatalogInactive{}}
	if scope.Validate() != nil {
		return out, domain.ErrInvalid
	}
	entries := []domain.CatalogEntry{}
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM `+s.table("catalog_entries")+` WHERE scope_key=$1 AND NOT deleted AND kind IN ('group_config','feature','reply_rule','correction','phrase') ORDER BY kind,name`, scope.Key())
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var body []byte
		var e domain.CatalogEntry
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal(body, &e); err != nil {
			rows.Close()
			return out, err
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, e := range entries {
		input := domain.CatalogPutInput{Kind: e.Kind, Name: e.Name, CatalogValue: e.CatalogValue}
		if !catalogValueValid(input) {
			return out, domain.ErrInvalid
		}
		if e.Kind == domain.CatalogGroupConfig && e.Name == "history_since" {
			out.HistorySince, _ = time.Parse(time.RFC3339Nano, e.Config.Value)
		}
	}
	seen := map[domain.SourceRef]bool{}
	for _, e := range entries {
		sourceBound := e.Kind == domain.CatalogPhraseKind || e.Kind == domain.CatalogReplyRuleKind || e.Kind == domain.CatalogCorrectionKind || (e.Kind == domain.CatalogGroupConfig && e.Name == "reply_instruction")
		if sourceBound {
			if !out.HistorySince.IsZero() && e.Origin.OccurredAt.Before(out.HistorySince) {
				out.Inactive = append(out.Inactive, domain.CatalogInactive{Kind: e.Kind, Name: e.Name, Reason: "before_history_cutoff"})
				continue
			}
			valid, err := s.ValidSources(ctx, domain.Query{Scope: scope, Anchor: time.Now().UTC(), Mode: domain.QueryLiteral, Limit: 1}, []domain.SourceRef{e.Origin})
			if err != nil {
				return out, err
			}
			if !valid {
				out.Inactive = append(out.Inactive, domain.CatalogInactive{Kind: e.Kind, Name: e.Name, Reason: "stale_source"})
				continue
			}
			if !seen[e.Origin] {
				out.Sources = append(out.Sources, e.Origin)
				seen[e.Origin] = true
			}
		}
		switch e.Kind {
		case domain.CatalogGroupConfig:
			switch e.Name {
			case "reply_instruction":
				out.ReplyInstruction = e.Config.Value
				ref := e.Origin
				out.ReplyInstructionSource = &ref
			case "mute_until":
				out.MuteUntil, _ = time.Parse(time.RFC3339Nano, e.Config.Value)
			}
		case domain.CatalogFeature:
			out.Features[e.Name] = e.Feature.Enabled
		case domain.CatalogPhraseKind:
			if e.Phrase.Enabled {
				p := *e.Phrase
				p.Name = e.Name
				p.Origin = e.Origin
				out.Phrases = append(out.Phrases, p)
			}
		case domain.CatalogReplyRuleKind:
			if e.Rule.Enabled {
				r := *e.Rule
				r.Name = e.Name
				r.Origin = e.Origin
				if r.AssetName != "" {
					asset, assetErr := s.CatalogEntry(ctx, scope, domain.CatalogAsset, r.AssetName)
					if errors.Is(assetErr, domain.ErrNotFound) || (assetErr == nil && (asset.Deleted || asset.InactiveReason != "" || asset.AssetSource == nil)) {
						out.Inactive = append(out.Inactive, domain.CatalogInactive{Kind: e.Kind, Name: e.Name, Reason: "asset_unavailable"})
						continue
					}
					if assetErr != nil {
						return out, assetErr
					}
					r.Asset = asset.Asset
					r.AssetSource = asset.AssetSource
					if !seen[*asset.AssetSource] {
						out.Sources = append(out.Sources, *asset.AssetSource)
						seen[*asset.AssetSource] = true
					}
				}
				out.Rules = append(out.Rules, r)
			}
		case domain.CatalogCorrectionKind:
			c := *e.Correction
			c.Name = e.Name
			c.Origin = e.Origin
			out.Corrections = append(out.Corrections, c)
		}
	}
	sort.Slice(out.Rules, func(i, j int) bool {
		if out.Rules[i].Weight == out.Rules[j].Weight {
			return out.Rules[i].Name < out.Rules[j].Name
		}
		return out.Rules[i].Weight > out.Rules[j].Weight
	})
	return out, nil
}

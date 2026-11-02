package atoms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

type CatalogOptions struct {
	Admin pgstore.CatalogAdmin
	// Effective is true only when the host applies CatalogRuntime to routing,
	// retrieval and feature admission. Catalog CRUD never implies that wiring.
	Effective bool
}

func catalogDecode(raw string, out any) error {
	canonical, err := domain.CanonicalArguments(raw)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewBufferString(canonical))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return domain.ErrInvalid
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return domain.ErrInvalid
	}
	return nil
}
func catalogSources(raw string) ([]domain.SourceRef, error) {
	var out struct {
		Sources []domain.SourceRef `json:"sources"`
	}
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil, domain.ErrInvalid
	}
	return out.Sources, nil
}
func catalogEffective(e domain.CatalogEntry, enabled bool) bool {
	if e.Deleted || e.InactiveReason != "" || (e.Rule != nil && !e.Rule.Enabled) || (e.Phrase != nil && !e.Phrase.Enabled) {
		return false
	}
	return e.Kind == domain.CatalogAsset || e.Kind == domain.CatalogPermission || enabled
}
func catalogOutput(entry domain.CatalogEntry, enabled bool) map[string]any {
	sources := []domain.SourceRef{}
	if entry.AssetSource != nil && !entry.Deleted && entry.InactiveReason == "" {
		sources = append(sources, *entry.AssetSource)
	}
	return map[string]any{"entry": entry, "stored": true, "effective": catalogEffective(entry, enabled), "runtime_wired": enabled, "sources": sources}
}
func catalogObject(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
func catalogString(max int) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
}
func catalogEnum(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}
func catalogSchema(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func catalogKinds() map[string]any {
	return catalogEnum("asset", "phrase", "reply_rule", "group_config", "feature", "permission", "correction")
}
func catalogPutSchema() json.RawMessage {
	payloads := map[string]any{
		"phrase":     catalogObject(map[string]any{"text": catalogString(512), "weight": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "enabled": map[string]any{"type": "boolean"}}, "text", "weight", "enabled"),
		"asset":      catalogObject(map[string]any{"source_message_id": catalogString(1024), "resource_id": catalogString(1024), "type": catalogEnum("image", "sticker"), "description": map[string]any{"type": "string", "maxLength": 1024}}, "source_message_id", "resource_id", "type"),
		"rule":       catalogObject(map[string]any{"pattern": catalogString(512), "match": catalogEnum("exact", "contains", "prefix", "regex"), "reply": catalogString(4096), "asset_name": catalogString(128), "probability": map[string]any{"type": "number", "minimum": 0, "maximum": 1}, "weight": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "enabled": map[string]any{"type": "boolean"}}, "pattern", "match", "probability", "weight", "enabled"),
		"config":     catalogObject(map[string]any{"value": catalogString(4096)}, "value"),
		"feature":    catalogObject(map[string]any{"enabled": map[string]any{"type": "boolean"}}, "enabled"),
		"permission": catalogObject(map[string]any{"subject_id": catalogString(1024), "point": catalogEnum("asset.write", "phrase.write", "reply_rule.write", "group_config.write", "feature.write", "correction.write")}, "subject_id", "point"),
		"correction": catalogObject(map[string]any{"text": catalogString(2048)}, "text"),
	}
	cases := []any{}
	for _, pair := range [][2]string{{"asset", "asset"}, {"phrase", "phrase"}, {"reply_rule", "rule"}, {"group_config", "config"}, {"feature", "feature"}, {"permission", "permission"}, {"correction", "correction"}} {
		kind, member := pair[0], pair[1]
		name := catalogString(128)
		if kind == "group_config" {
			name = catalogEnum("reply_instruction", "history_since", "mute_until")
		}
		if kind == "feature" {
			name = catalogEnum("conversation", "reminders", "web_search", "music", "luckin", "public_data")
		}
		cases = append(cases, catalogObject(map[string]any{"kind": catalogEnum(kind), "name": name, "expected_revision": map[string]any{"type": "integer", "minimum": 0, "maximum": 9007199254740990}, member: payloads[member]}, "kind", "name", "expected_revision", member))
	}
	return catalogSchema(map[string]any{"type": "object", "oneOf": cases})
}

// CatalogDefinitions provides composable atoms. All administrative authority is
// server-owned, all changes require approval, and all scopes come from the task.
func CatalogDefinitions(store *pgstore.Store, options ...CatalogOptions) []Definition {
	if store == nil {
		return nil
	}
	var opts CatalogOptions
	if len(options) > 0 {
		opts = options[0]
	}
	names := map[string]any{"kind": catalogKinds(), "name": catalogString(128)}
	return []Definition{
		{Name: "catalog_get", Description: "Read one named group catalog entry and its revision, ownership and origin. Deleted entries remain as tombstones for safe revisioned recreation. Stored entries report effective and runtime_wired explicitly.", Parameters: catalogSchema(catalogObject(names, "kind", "name")), sourceRefs: catalogSources, Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a struct {
				Kind domain.CatalogKind `json:"kind"`
				Name string             `json:"name"`
			}
			if err := catalogDecode(args, &a); err != nil {
				return "", err
			}
			entry, err := store.CatalogEntry(ctx, in.Task.Scope, a.Kind, a.Name)
			if err != nil {
				return "", err
			}
			return encodeCommunity(catalogOutput(entry, opts.Effective))
		}},
		{Name: "catalog_list", Description: "List active entries of one catalog kind in this group, ordered by name. Use next_after for the next page. source references are current verified originals; no cross-group or user scope override exists.", Parameters: catalogSchema(catalogObject(map[string]any{"kind": catalogKinds(), "after": catalogString(128), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}}, "kind")), sourceRefs: catalogSources, Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a struct {
				Kind  domain.CatalogKind `json:"kind"`
				After string             `json:"after"`
				Limit int                `json:"limit"`
			}
			if err := catalogDecode(args, &a); err != nil {
				return "", err
			}
			if a.Limit == 0 {
				a.Limit = 10
			}
			entries, err := store.ListCatalog(ctx, in.Task.Scope, a.Kind, a.After, a.Limit)
			if err != nil {
				return "", err
			}
			rows := []any{}
			refs := []domain.SourceRef{}
			next := ""
			for _, e := range entries {
				rows = append(rows, catalogOutput(e, opts.Effective))
				next = e.Name
				if e.AssetSource != nil && e.InactiveReason == "" {
					refs = append(refs, *e.AssetSource)
				}
			}
			return encodeCommunity(map[string]any{"items": rows, "sources": refs, "next_after": next, "may_have_more": len(entries) == a.Limit, "runtime_wired": opts.Effective})
		}},
		{Name: "catalog_put", Description: "Create or replace exactly one typed group catalog entry, requiring explicit approval and expected_revision (0 for new). asset: save current actor's source-verified group image/sticker reference. phrase: weighted personal phrase pool entry (text, weight, enabled). reply_rule: own exact/contains/prefix/RE2-regex text/asset_name reply with probability 0..1 and weight 1..100. group_config allows reply_instruction/history_since/mute_until only; timestamps RFC3339. feature enables/disables a listed runtime feature. Group controls/corrections need bootstrap admin or an exact catalog grant. Only bootstrap admin grants permissions; use catalog_permission_query to obtain deterministic grant name. Runtime application is reported explicitly.", Parameters: catalogPutSchema(), SideEffect: true, sourceRefs: catalogSources, Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a domain.CatalogPutInput
			if err := catalogDecode(args, &a); err != nil {
				return "", err
			}
			entry, err := store.PutCatalog(ctx, in.Job, in.Task, in.ExecutionKey, in.PolicyDigest, a, opts.Admin)
			if err != nil {
				return "", err
			}
			return encodeCommunity(catalogOutput(entry, opts.Effective))
		}},
		{Name: "catalog_delete", Description: "Delete one catalog entry using its current expected_revision, requiring explicit approval and ownership/admin authority. Tombstones retain audit and revision. Deleting config/feature restores host default; deleting a grant immediately revokes catalog delegation.", Parameters: catalogSchema(catalogObject(map[string]any{"kind": catalogKinds(), "name": catalogString(128), "expected_revision": map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740990}}, "kind", "name", "expected_revision")), SideEffect: true, Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a domain.CatalogDeleteInput
			if err := catalogDecode(args, &a); err != nil {
				return "", err
			}
			entry, err := store.DeleteCatalog(ctx, in.Job, in.Task, in.ExecutionKey, in.PolicyDigest, a, opts.Admin)
			if err != nil {
				return "", err
			}
			return encodeCommunity(catalogOutput(entry, opts.Effective))
		}},
		{Name: "catalog_permission_query", Description: "Read an exact non-escalating catalog grant for a subject in this group and obtain its deterministic grant_name. Omit subject_id to inspect the current speaker. Grants authorize catalog operations only; they never grant task approval, infrastructure access or permission management.", Parameters: catalogSchema(catalogObject(map[string]any{"subject_id": catalogString(1024), "point": catalogEnum("asset.write", "phrase.write", "reply_rule.write", "group_config.write", "feature.write", "correction.write")}, "point")), Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a struct {
				Subject string `json:"subject_id"`
				Point   string `json:"point"`
			}
			if err := catalogDecode(args, &a); err != nil {
				return "", err
			}
			if a.Subject == "" {
				a.Subject = in.Task.LastActorID
			}
			if a.Subject == "" || !domain.CatalogPermissionValid(a.Point) {
				return "", domain.ErrInvalid
			}
			name := pgstore.CatalogPermissionName(a.Subject, a.Point)
			entry, err := store.CatalogEntry(ctx, in.Task.Scope, domain.CatalogPermission, name)
			if errors.Is(err, domain.ErrNotFound) {
				return encodeCommunity(map[string]any{"grant_name": name, "subject_id": a.Subject, "point": a.Point, "granted": false, "revision": 0, "authority": "catalog_only"})
			}
			if err != nil {
				return "", err
			}
			return encodeCommunity(map[string]any{"grant_name": name, "subject_id": a.Subject, "point": a.Point, "granted": !entry.Deleted, "revision": entry.Revision, "authority": "catalog_only", "entry": entry})
		}},
		{Name: "catalog_feature_list", Description: "List the closed set of group runtime features with their current enabled state. Missing records use enabled=true. runtime_wired=false means these preferences have no host effect yet.", Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a struct{}
			if err := catalogDecode(args, &a); err != nil {
				return "", err
			}
			rt, err := store.CatalogRuntime(ctx, in.Task.Scope)
			if err != nil {
				return "", err
			}
			features := []any{}
			for _, name := range []string{"conversation", "reminders", "web_search", "music", "luckin", "public_data"} {
				features = append(features, map[string]any{"name": name, "enabled": rt.FeatureEnabled(name)})
			}
			return encodeCommunity(map[string]any{"features": features, "muted": rt.Muted(time.Now()), "mute_until": rt.MuteUntil, "runtime_wired": opts.Effective})
		}},
		{Name: "catalog_capacity_stats", Description: "Read actual enforced catalog capacity (100 durable names per kind per group, including tombstones) and current usage. These are resource quotas; legacy rate limiter settings are not imported.", Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a struct{}
			if err := catalogDecode(args, &a); err != nil {
				return "", err
			}
			out, err := store.CatalogCapacityStats(ctx, in.Task.Scope)
			if err != nil {
				return "", err
			}
			return encodeCommunity(out)
		}},
		{Name: "catalog_execution_stats", Description: "Read observed tool execution ledger counts for this group over the last hour, grouped by tool and status. This is measured workload, not a configured API rate limit; legacy limits/counters are not imported.", Run: func(ctx context.Context, in Invocation, args string) (string, error) {
			var a struct{}
			if err := catalogDecode(args, &a); err != nil {
				return "", err
			}
			out, err := store.CatalogExecutionStats(ctx, in.Task.Scope)
			if err != nil {
				return "", err
			}
			return encodeCommunity(out)
		}},
	}
}

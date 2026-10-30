package pgstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"reflect"
	"testing"
	"time"
)

func TestCatalogMigrationCreatesDurableScopedRevisionedState(t *testing.T) {
	f := setup(t)
	var table sql.NullString
	err := f.db.QueryRowContext(context.Background(), `SELECT to_regclass($1)::text`, f.schema+".catalog_entries").Scan(&table)
	if err != nil {
		t.Fatal(err)
	}
	if !table.Valid {
		t.Fatal("catalog durable scoped revisioned state not implemented")
	}
}

func TestCatalogStoreExposesTypedRuntimeSnapshot(t *testing.T) {
	method, ok := reflect.TypeOf((*pgstore.Store)(nil)).MethodByName("CatalogRuntime")
	if !ok {
		t.Fatal("catalog runtime snapshot not implemented")
	}
	snapshot := method.Type.Out(0)
	for _, name := range []string{"ReplyInstruction", "HistorySince", "MuteUntil", "Features", "Rules", "Corrections"} {
		if _, ok := snapshot.FieldByName(name); !ok {
			t.Fatalf("runtime snapshot lacks %s", name)
		}
	}
}

func catalogTurn(t *testing.T, f fixture, id, actor, tool string, arguments any) (domain.Job, domain.Task, domain.ToolExecution) {
	t.Helper()
	ctx := context.Background()
	e := event(id, 1)
	e.Message.Source.MessageID = id
	e.Message.Source.AuthorID = actor
	accept(t, f, e)
	task, err := f.CreateTask(ctx, e.Message, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "catalog-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	p := pgstore.ToolPolicy{Name: tool, SideEffect: true, Digest: "catalog-policy"}
	if err = f.SyncToolPolicies(ctx, []pgstore.ToolPolicy{{Name: "catalog_put", SideEffect: true, Digest: p.Digest}, {Name: "catalog_delete", SideEffect: true, Digest: p.Digest}}); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(arguments)
	approval, err := f.FreezeApproval(ctx, j, task, tool, string(b), p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.BindInterrupt(ctx, j, task, approval.ID, "catalog-interrupt"); err != nil {
		t.Fatal(err)
	}
	task, err = f.ControlTask(ctx, task.Scope, actor, domain.ControlAction{Action: "approve", TaskID: task.ID, Revision: task.Revision, ApprovalID: approval.ID}, domain.SourceRef{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, err = f.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "catalog-approved-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	task, err = f.StartTurn(ctx, j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision, ApprovalID: approval.ID})
	if err != nil {
		t.Fatal(err)
	}
	x, _, err := f.BeginTool(ctx, j, task, tool, string(b), approval.ID, true, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	return j, task, x
}
func catalogRuleInput(name, reply string, rev int64) domain.CatalogPutInput {
	return domain.CatalogPutInput{Kind: domain.CatalogReplyRuleKind, Name: name, ExpectedRevision: rev, CatalogValue: domain.CatalogValue{Rule: &domain.CatalogReplyRule{Pattern: "hello", Match: "exact", Reply: reply, Probability: 1, Weight: 1, Enabled: true}}}
}
func catalogFinish(t *testing.T, f fixture, j domain.Job, task domain.Task, x domain.ToolExecution) {
	t.Helper()
	if err := f.FinishTool(context.Background(), j, task, x, "saved", nil); err != nil {
		t.Fatal(err)
	}
	if err := f.CompleteJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}
}
func TestCatalogWritesRequireReservedApprovedExactArgumentsAndCAS(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	input := catalogRuleInput("greeting", "hi", 0)
	j, task, x := catalogTurn(t, f, "create-rule", "alice", "catalog_put", input)
	if _, err := f.PutCatalog(ctx, j, task, "not-reserved", "catalog-policy", input, nil); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("unreserved write: %v", err)
	}
	forged := input
	forged.Name = "forged"
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", forged, nil); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("arguments not bound: %v", err)
	}
	out, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Revision != 1 || out.CreatedBy != "alice" || out.UpdatedBy != "alice" {
		t.Fatalf("unfrozen attribution: %+v", out)
	}
	again, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil)
	if err != nil || again.Revision != 1 {
		t.Fatalf("retry reapplied: %+v %v", again, err)
	}
	catalogFinish(t, f, j, task, x)
	input = catalogRuleInput("greeting", "new", 0)
	j, task, x = catalogTurn(t, f, "stale-edit", "alice", "catalog_put", input)
	if _, err = f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("lost update accepted: %v", err)
	}
}
func TestCatalogPersonalOwnershipAdminFailClosedAndRuntimeSnapshot(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	input := catalogRuleInput("greeting", "hi", 0)
	j, task, x := catalogTurn(t, f, "alice-rule", "alice", "catalog_put", input)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil); err != nil {
		t.Fatal(err)
	}
	catalogFinish(t, f, j, task, x)
	input = catalogRuleInput("greeting", "hijacked", 1)
	j, task, x = catalogTurn(t, f, "bob-rule", "bob", "catalog_put", input)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("other member overwrote rule: %v", err)
	}
	catalogFinish(t, f, j, task, x)
	input = domain.CatalogPutInput{Kind: domain.CatalogGroupConfig, Name: "reply_instruction", CatalogValue: domain.CatalogValue{Config: &domain.CatalogConfigValue{Value: "Keep replies brief."}}}
	j, task, x = catalogTurn(t, f, "bob-config", "bob", "catalog_put", input)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("missing admin resolver allowed config: %v", err)
	}
	admin := func(_ context.Context, _ domain.Scope, actor string) (bool, error) { return actor == "bob", nil }
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, admin); err != nil {
		t.Fatal(err)
	}
	rt, err := f.CatalogRuntime(ctx, task.Scope)
	if err != nil || rt.ReplyInstruction != "Keep replies brief." || rt.ReplyInstructionSource == nil || *rt.ReplyInstructionSource != task.Root || len(rt.Rules) != 1 || rt.Rules[0].Reply != "hi" {
		t.Fatalf("runtime snapshot: %+v %v", rt, err)
	}
	other := task.Scope
	other.ChatID = "another"
	rt, err = f.CatalogRuntime(ctx, other)
	if err != nil || rt.ReplyInstruction != "" || len(rt.Rules) != 0 {
		t.Fatalf("cross group leak: %+v %v", rt, err)
	}
}
func TestCatalogAssetsRequireAuthoritativeSameGroupMedia(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	src := event("image-source", 1)
	src.Message.Source.MessageID = "image-source"
	src.Message.Source.AuthorID = "alice"
	src.Message.Media = []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "img_real"}}
	accept(t, f, src)
	input := domain.CatalogPutInput{Kind: domain.CatalogAsset, Name: "smile", CatalogValue: domain.CatalogValue{Asset: &domain.CatalogAssetValue{SourceMessageID: "image-source", ResourceID: "img_invented", Type: "image"}}}
	j, task, x := catalogTurn(t, f, "save-false-image", "alice", "catalog_put", input)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invented resource accepted: %v", err)
	}
	catalogFinish(t, f, j, task, x)
	input.Asset.ResourceID = "img_real"
	j, task, x = catalogTurn(t, f, "save-real-image", "alice", "catalog_put", input)
	saved, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil)
	if err != nil || saved.AssetSource == nil || *saved.AssetSource != src.Message.Source {
		t.Fatalf("asset provenance missing: %+v %v", saved, err)
	}
}
func TestCatalogRejectsRuntimeCredentialAndPermissionEscalation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	input := domain.CatalogPutInput{Kind: domain.CatalogGroupConfig, Name: "ark_api_key", CatalogValue: domain.CatalogValue{Config: &domain.CatalogConfigValue{Value: "credential"}}}
	j, task, x := catalogTurn(t, f, "illegal-config", "admin", "catalog_put", input)
	admin := func(context.Context, domain.Scope, string) (bool, error) { return true, nil }
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, admin); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("arbitrary config accepted: %v", err)
	}
	catalogFinish(t, f, j, task, x)
	input = domain.CatalogPutInput{Kind: domain.CatalogPermission, Name: "admin", CatalogValue: domain.CatalogValue{Permission: &domain.CatalogPermissionValue{SubjectID: "alice", Point: "permission.manage"}}}
	j, task, x = catalogTurn(t, f, "illegal-grant", "admin", "catalog_put", input)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, admin); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("permission escalation accepted: %v", err)
	}
}

func TestCatalogDelegatedGrantCannotEscalateAndDeleteRevokesImmediately(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	admin := func(_ context.Context, _ domain.Scope, actor string) (bool, error) { return actor == "admin", nil }
	grant := domain.CatalogPutInput{Kind: domain.CatalogPermission, Name: pgstore.CatalogPermissionName("alice", "feature.write"), CatalogValue: domain.CatalogValue{Permission: &domain.CatalogPermissionValue{SubjectID: "alice", Point: "feature.write"}}}
	j, task, x := catalogTurn(t, f, "grant-feature", "admin", "catalog_put", grant)
	row, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", grant, admin)
	if err != nil {
		t.Fatal(err)
	}
	catalogFinish(t, f, j, task, x)
	feature := domain.CatalogPutInput{Kind: domain.CatalogFeature, Name: "music", CatalogValue: domain.CatalogValue{Feature: &domain.CatalogFeatureValue{Enabled: false}}}
	j, task, x = catalogTurn(t, f, "alice-feature", "alice", "catalog_put", feature)
	if _, err = f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", feature, admin); err != nil {
		t.Fatal(err)
	}
	catalogFinish(t, f, j, task, x)
	revoke := domain.CatalogDeleteInput{Kind: domain.CatalogPermission, Name: row.Name, ExpectedRevision: row.Revision}
	j, task, x = catalogTurn(t, f, "revoke-feature", "admin", "catalog_delete", revoke)
	if _, err = f.DeleteCatalog(ctx, j, task, x.Key, "catalog-policy", revoke, admin); err != nil {
		t.Fatal(err)
	}
	catalogFinish(t, f, j, task, x)
	feature.ExpectedRevision = 1
	feature.Feature.Enabled = true
	j, task, x = catalogTurn(t, f, "alice-feature-revoked", "alice", "catalog_put", feature)
	if _, err = f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", feature, admin); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked grant stayed effective: %v", err)
	}
}
func TestCatalogSourceEditDeactivatesRulesWithoutTrustingOldOriginal(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	input := catalogRuleInput("source-bound", "hi", 0)
	j, task, x := catalogTurn(t, f, "source-rule", "alice", "catalog_put", input)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil); err != nil {
		t.Fatal(err)
	}
	catalogFinish(t, f, j, task, x)
	rt, err := f.CatalogRuntime(ctx, task.Scope)
	if err != nil || len(rt.Rules) != 1 || len(rt.Sources) != 1 {
		t.Fatalf("missing current rule provenance: %+v %v", rt, err)
	}
	edit := event("rule-edited", 2)
	edit.Message.Source.MessageID = "source-rule"
	edit.Message.Source.AuthorID = "alice"
	edit.Message.Text = "withdrawn preference"
	accept(t, f, edit)
	rt, err = f.CatalogRuntime(ctx, task.Scope)
	if err != nil || len(rt.Rules) != 0 || len(rt.Inactive) != 1 {
		t.Fatalf("edited original stayed effective: %+v %v", rt, err)
	}
}

func TestCatalogReadsReportWithdrawnAssetsAndMeasuredLimits(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	src := event("limit-image", 1)
	src.Message.Source.MessageID = "limit-image"
	src.Message.Source.AuthorID = "alice"
	src.Message.Media = []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "img_limit"}}
	accept(t, f, src)
	input := domain.CatalogPutInput{Kind: domain.CatalogAsset, Name: "limit", CatalogValue: domain.CatalogValue{Asset: &domain.CatalogAssetValue{SourceMessageID: "limit-image", ResourceID: "img_limit", Type: "image"}}}
	j, task, x := catalogTurn(t, f, "save-limit-image", "alice", "catalog_put", input)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil); err != nil {
		t.Fatal(err)
	}
	stats, err := f.CatalogCapacityStats(ctx, task.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Kinds) != 7 || stats.Kinds[0].Names != 1 || stats.Kinds[0].Active != 1 || !stats.Kinds[0].Enforced || stats.Kinds[0].MaxNames != 100 || stats.ImportedLegacyLimits {
		t.Fatalf("capacity fiction: %+v", stats)
	}
	exec, err := f.CatalogExecutionStats(ctx, task.Scope)
	if err != nil || len(exec.Rows) != 1 || exec.Rows[0].Count != 1 || exec.Rows[0].Tool != "catalog_put" || exec.ImportedLegacyLimits {
		t.Fatalf("ledger stats: %+v %v", exec, err)
	}
	catalogFinish(t, f, j, task, x)
	edit := event("changed-limit-image", 2)
	edit.Message.Source.MessageID = "limit-image"
	edit.Message.Source.AuthorID = "alice"
	edit.Message.Text = "image withdrawn"
	accept(t, f, edit)
	saved, err := f.CatalogEntry(ctx, task.Scope, domain.CatalogAsset, "limit")
	if err != nil || saved.InactiveReason != "stale_source" {
		t.Fatalf("withdrawn asset still effective: %+v %v", saved, err)
	}
}

func TestCatalogPhraseAndImageReplyHaveDistinctVerifiedRuntimeValues(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	phrase := domain.CatalogPutInput{Kind: domain.CatalogPhraseKind, Name: "hello-pool", CatalogValue: domain.CatalogValue{Phrase: &domain.CatalogPhrase{Text: "hello there", Weight: 7, Enabled: true}}}
	j, task, x := catalogTurn(t, f, "phrase-origin", "alice", "catalog_put", phrase)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", phrase, nil); err != nil {
		t.Fatal(err)
	}
	catalogFinish(t, f, j, task, x)
	src := event("reply-image", 1)
	src.Message.Source.MessageID = "reply-image"
	src.Message.Source.AuthorID = "alice"
	src.Message.Media = []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "img_reply"}}
	accept(t, f, src)
	asset := domain.CatalogPutInput{Kind: domain.CatalogAsset, Name: "happy-image", CatalogValue: domain.CatalogValue{Asset: &domain.CatalogAssetValue{SourceMessageID: "reply-image", ResourceID: "img_reply", Type: "image"}}}
	j, task, x = catalogTurn(t, f, "reply-asset-origin", "alice", "catalog_put", asset)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", asset, nil); err != nil {
		t.Fatal(err)
	}
	catalogFinish(t, f, j, task, x)
	rule := catalogRuleInput("image-match", "", 0)
	rule.Rule.Match = "regex"
	rule.Rule.Pattern = "^hello[!]+$"
	rule.Rule.AssetName = "happy-image"
	j, task, x = catalogTurn(t, f, "image-rule-origin", "alice", "catalog_put", rule)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", rule, nil); err != nil {
		t.Fatal(err)
	}
	rt, err := f.CatalogRuntime(ctx, task.Scope)
	if err != nil || len(rt.Phrases) != 1 || rt.Phrases[0].Weight != 7 || len(rt.Rules) != 1 || rt.Rules[0].Asset == nil || rt.Rules[0].Asset.ResourceID != "img_reply" || rt.Rules[0].AssetSource == nil {
		t.Fatalf("phrase/image rule collapsed: %+v %v", rt, err)
	}
	catalogFinish(t, f, j, task, x)
	rule.Name = "invalid-regex"
	rule.Rule.Pattern = "["
	j, task, x = catalogTurn(t, f, "invalid-regex-origin", "alice", "catalog_put", rule)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", rule, nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid regexp accepted: %v", err)
	}
}

func TestCatalogLivePolicyRevocationDeniesReservedMutation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	input := catalogRuleInput("revocation", "hi", 0)
	j, task, x := catalogTurn(t, f, "revocation-origin", "alice", "catalog_put", input)
	if _, err := f.db.Exec(`UPDATE "` + f.schema + `".tool_policies SET enabled=false WHERE tool_name='catalog_put'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked reserved effect applied: %v", err)
	}
	if _, err := f.CatalogEntry(ctx, task.Scope, domain.CatalogReplyRuleKind, input.Name); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked mutation persisted: %v", err)
	}
}
func TestCatalogConcurrentWritersEnforceOneExpectedRevisionWinner(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	input := catalogRuleInput("shared-revision", "first", 0)
	j, task, x := catalogTurn(t, f, "revision-first", "alice", "catalog_put", input)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil); err != nil {
		t.Fatal(err)
	}
	catalogFinish(t, f, j, task, x)
	a, b := catalogRuleInput(input.Name, "a", 1), catalogRuleInput(input.Name, "b", 1)
	aj, at, ax := catalogTurn(t, f, "revision-a", "alice", "catalog_put", a)
	bj, bt, bx := catalogTurn(t, f, "revision-b", "alice", "catalog_put", b)
	results := make(chan error, 2)
	go func() { _, err := f.PutCatalog(ctx, aj, at, ax.Key, "catalog-policy", a, nil); results <- err }()
	go func() { _, err := f.PutCatalog(ctx, bj, bt, bx.Key, "catalog-policy", b, nil); results <- err }()
	success, conflict := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, domain.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS race success=%d conflict=%d", success, conflict)
	}
}

func TestCatalogDurableNameQuotaIsEnforcedBeforeActualWrite(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	input := catalogRuleInput("quota-first", "hi", 0)
	j, task, x := catalogTurn(t, f, "quota-origin", "alice", "catalog_put", input)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil); err != nil {
		t.Fatal(err)
	}
	catalogFinish(t, f, j, task, x)
	_, err := f.db.Exec(`INSERT INTO "` + f.schema + `".catalog_entries(scope_key,kind,name,revision,deleted,creator,body,updated_at) SELECT scope_key,kind,'seed-'||n,revision,deleted,creator,convert_to(jsonb_set(convert_from(body,'UTF8')::jsonb,'{name}',to_jsonb('seed-'||n))::text,'UTF8'),updated_at FROM "` + f.schema + `".catalog_entries CROSS JOIN generate_series(1,99) AS n WHERE name='quota-first'`)
	if err != nil {
		t.Fatal(err)
	}
	input = catalogRuleInput("quota-overflow", "hi", 0)
	j, task, x = catalogTurn(t, f, "quota-overflow-origin", "alice", "catalog_put", input)
	if _, err = f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", input, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("quota overflow persisted: %v", err)
	}
	if _, err = f.CatalogEntry(ctx, task.Scope, domain.CatalogReplyRuleKind, input.Name); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("overflow entry exists: %v", err)
	}
}

func TestCatalogAccessReadsOnlyLightweightEffectiveControls(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	feature := domain.CatalogPutInput{Kind: domain.CatalogFeature, Name: "music", CatalogValue: domain.CatalogValue{Feature: &domain.CatalogFeatureValue{Enabled: false}}}
	admin := func(context.Context, domain.Scope, string) (bool, error) { return true, nil }
	j, task, x := catalogTurn(t, f, "access-feature-origin", "admin", "catalog_put", feature)
	if _, err := f.PutCatalog(ctx, j, task, x.Key, "catalog-policy", feature, admin); err != nil {
		t.Fatal(err)
	}
	// Malformed unrelated preference must not be queried by access-only gating.
	_, err := f.db.Exec(`INSERT INTO "`+f.schema+`".catalog_entries(scope_key,kind,name,revision,deleted,creator,body,updated_at) VALUES($1,'reply_rule','unrelated',1,false,'alice',$2,clock_timestamp())`, task.Scope.Key(), []byte("unreadable preference"))
	if err != nil {
		t.Fatal(err)
	}
	access, err := f.CatalogAccess(ctx, task.Scope)
	if err != nil || access.FeatureEnabled("music") || !access.FeatureEnabled("conversation") || len(access.Rules) != 0 || len(access.Sources) != 0 {
		t.Fatalf("access loaded unrelated preferences: %+v %v", access, err)
	}
}

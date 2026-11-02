package historyimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func archiveRow(t *testing.T, id, app, author, union, kind string, content any) []byte {
	t.Helper()
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	body, _ := json.Marshal(content)
	raw, _ := json.Marshal(map[string]any{"schema": "2.0", "header": map[string]any{"event_id": "source-event-" + id, "app_id": app, "tenant_key": scope().TenantID, "create_time": "1704067200000"}, "event": map[string]any{"sender": map[string]any{"sender_type": "user", "tenant_key": scope().TenantID, "sender_id": map[string]any{"open_id": author, "union_id": union}}, "message": map[string]any{"message_id": id, "chat_id": scope().ChatID, "chat_type": "group", "create_time": "1704067200000", "update_time": "1704067200000", "message_type": kind, "content": string(body)}}})
	row, _ := json.Marshal(map[string]any{"format": 1, "id": id, "source_chat_id": scope().ChatID, "role": "user", "raw_shape": "inbound_event", "source_app_id": app, "source_author_id": author, "source_author_union_id": union, "provider_create_time": "1704067200000", "provider_update_time": "1704067200000", "source_index": "archive", "source_document_id": id, "source": map[string]any{"message_id": id, "chat_id": scope().ChatID, "user_id": author, "create_time_unix_millis": at.UnixMilli()}, "raw_body": string(raw), "message_str": "derived text must be ignored"})
	return row
}
func writeArchive(t *testing.T, rows ...[]byte) (string, []byte, string) {
	t.Helper()
	dir := t.TempDir()
	var all strings.Builder
	var ids strings.Builder
	for _, row := range rows {
		all.Write(row)
		all.WriteByte('\n')
		var v struct {
			ID string `json:"id"`
		}
		json.Unmarshal(row, &v)
		ids.WriteString(v.ID + "\n")
	}
	path := filepath.Join(dir, "archive")
	if err := os.WriteFile(path, []byte(all.String()), 0600); err != nil {
		t.Fatal(err)
	}
	return path, []byte(ids.String()), filepath.Join(dir, "ledger")
}
func TestPrepareArchiveUsesOriginalBodyAndNamespacedAuthorsWithoutRequests(t *testing.T) {
	a := archiveRow(t, "a", "old-app", "old-author", "union", "text", map[string]any{"text": "历史原文🙂"})
	b := archiveRow(t, "b", scope().AppID, "current-author", "union2", "post", map[string]any{"title": "标题", "content": []any{[]any{map[string]any{"tag": "text", "text": "正文"}}}})
	c := archiveRow(t, "photo", "old-app", "old-author", "union", "image", map[string]any{"image_key": "image"})
	path, inventory, canonical := writeArchive(t, a, b, c)
	report, err := PrepareArchive(scope(), inventory, path, "", canonical)
	if err != nil || report.Counts["human"] != 3 || report.SourceType != "opensearch_archive" || !report.ArchivedVersionNotProviderCurrent {
		t.Fatalf("archive preparation: %+v %v", report, err)
	}
	ledger, err := ReadLedger(canonical, scope(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ledger.Records {
		m := r.Message
		if m == nil {
			t.Fatal("missing archive fact")
		}
		if strings.Contains(m.Text, "derived") {
			t.Fatal("derived source text imported")
		}
		if r.ID == "a" && (m.Source.AuthorID != "archive:old-app:old-author" || m.Text != "历史原文🙂") {
			t.Fatalf("wrong original facts: %+v", m)
		}
		if r.ID == "b" && m.Source.AuthorID != "current-author" {
			t.Fatal("current app identity unnecessarily changed")
		}
		var meta domain.IngressMetadata
		json.Unmarshal(m.Raw, &meta)
		if meta.Kind != "archive" {
			t.Fatal("archive masqueraded as current provider read")
		}
	}
	contexts, err := BuildContexts(scope(), []domain.Message{*ledger.Records[0].Message, *ledger.Records[1].Message, *ledger.Records[2].Message})
	if err != nil || len(contexts) != 2 {
		t.Fatalf("archive text/media contexts: %d %v", len(contexts), err)
	}
	before, _ := os.ReadFile(canonical)
	if _, err = PrepareArchive(scope(), inventory, path, "", canonical); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(canonical)
	if string(before) != string(after) {
		t.Fatal("repeat changed frozen ledger")
	}
}
func TestArchiveRejectsCrossScopeNamespaceAndIncompleteInventoryBeforeOutput(t *testing.T) {
	original := archiveRow(t, "a", "old-app", "old-author", "u", "text", map[string]any{"text": "x"})
	for name, mutate := range map[string]func(map[string]any){"namespace": func(r map[string]any) { r["source_author_id"] = "forged" }, "chat": func(r map[string]any) { r["source_chat_id"] = "other" }, "source": func(r map[string]any) { r["source"].(map[string]any)["message_id"] = "other" }} {
		var row map[string]any
		json.Unmarshal(original, &row)
		mutate(row)
		bad, _ := json.Marshal(row)
		path, inventory, out := writeArchive(t, bad)
		if _, err := PrepareArchive(scope(), inventory, path, "", out); err == nil {
			t.Fatalf("accepted archive %s", name)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("invalid archive left partial ledger")
		}
	}
	path, inventory, out := writeArchive(t, original)
	inventory = append(inventory, []byte("missing\n")...)
	if _, err := PrepareArchive(scope(), inventory, path, "", out); err == nil {
		t.Fatal("incomplete archive inventory accepted")
	}
}
func TestArchiveApplyPreservesAlreadyKnownCurrentAcrossAuthorNamespaces(t *testing.T) {
	store, _, _ := importStore(t)
	ctx := context.Background()
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	current := message("a", "current-author", "online original", at)
	event, err := BackfillEvent(current)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcceptHistorical(ctx, event); err != nil {
		t.Fatal(err)
	}
	row := archiveRow(t, "a", "old-app", "old-author", "u", "text", map[string]any{"text": "archived original"})
	path, inventory, canonical := writeArchive(t, row)
	if _, err = PrepareArchive(scope(), inventory, path, "", canonical); err != nil {
		t.Fatal(err)
	}
	report, err := Apply(ctx, store, scope(), inventory, canonical)
	if err != nil || report.Counts["archive_existing_current"] != 1 {
		t.Fatalf("known source archive apply: %+v %v", report, err)
	}
	got, _ := store.Message(ctx, scope(), "a")
	if got.Text != "online original" || got.Source.AuthorID != "current-author" {
		t.Fatal("archive replaced known facts")
	}
}

func storedMap(t *testing.T, dir string) (string, map[string]any) {
	t.Helper()
	proof, _ := json.Marshal(map[string]any{"sender": map[string]any{"sender_type": "user", "tenant_key": scope().TenantID, "sender_id": map[string]any{"open_id": "current-person", "union_id": "union"}}, "message": map[string]any{"message_id": "stored-proof", "chat_id": scope().ChatID}})
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	mapping := map[string]any{"source_app_id": "old-app", "source_author_id": "old-author", "union_id": "union", "current_app_id": scope().AppID, "current_author_id": "current-person", "evidence": map[string]any{"kind": "stored_current_inbound_union_id", "message_id": "stored-proof", "raw_body": string(proof), "raw_sha256": bytesDigest(proof)}, "source": domain.SourceRef{Scope: scope(), MessageID: "stored-proof", AuthorID: "current-person", Version: at.UnixMilli() * 2, OccurredAt: at}}
	object := map[string]any{"format": 1, "scope": scope(), "mappings": []any{mapping}}
	path := filepath.Join(dir, "authors")
	b, _ := json.Marshal(object)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return path, object
}
func TestArchiveUsesStoredUnionProofAndRejectsCrossScopeProof(t *testing.T) {
	row := archiveRow(t, "a", "old-app", "old-author", "union", "text", map[string]any{"text": "x"})
	path, inventory, canonical := writeArchive(t, row)
	authors, obj := storedMap(t, t.TempDir())
	report, err := PrepareArchive(scope(), inventory, path, authors, canonical)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := ReadLedger(canonical, scope(), inventory)
	if err != nil || ledger.Records[0].Message.Source.AuthorID != "current-person" {
		t.Fatalf("existing union information not reused: %+v %v", report, err)
	}
	mapping := obj["mappings"].([]any)[0].(map[string]any)
	ref := mapping["source"].(domain.SourceRef)
	ref.Scope.ChatID = "other"
	mapping["source"] = ref
	b, _ := json.Marshal(obj)
	os.WriteFile(authors, b, 0600)
	if _, err = PrepareArchive(scope(), inventory, path, authors, canonical+"-bad"); err == nil {
		t.Fatal("mapping from other group accepted")
	}
}
func TestArchiveLedgerBindsSourceManifestAndHonorsArchiveDeletion(t *testing.T) {
	row := archiveRow(t, "a", "old-app", "old-author", "u", "text", map[string]any{"text": "x"})
	path, inventory, canonical := writeArchive(t, row)
	if _, err := PrepareArchive(scope(), inventory, path, "", canonical); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(canonical)
	lines := strings.Split(string(data), "\n")
	var header map[string]any
	json.Unmarshal([]byte(lines[0]), &header)
	header["archive_sha256"] = strings.Repeat("0", 64)
	changed, _ := json.Marshal(header)
	lines[0] = string(changed)
	os.WriteFile(canonical, []byte(strings.Join(lines, "\n")), 0600)
	if _, err := ReadLedger(canonical, scope(), inventory); err == nil {
		t.Fatal("archive source manifest tampering accepted")
	}
	var deleted map[string]any
	json.Unmarshal(row, &deleted)
	deleted["archive_deletion_state"] = "deleted"
	deletedRow, _ := json.Marshal(deleted)
	path, inventory, canonical = writeArchive(t, deletedRow)
	report, err := PrepareArchive(scope(), inventory, path, "", canonical)
	if err != nil || report.Counts["deleted"] != 1 || report.Counts["human"] != 0 {
		t.Fatalf("archive deletion lost: %+v %v", report, err)
	}
}

func TestArchiveContextIncludesQuotedOriginalDisplayNameWithinBudget(t *testing.T) {
	var row map[string]any
	json.Unmarshal(archiveRow(t, "a", "old-app", "old-author", "u", "text", map[string]any{"text": strings.Repeat("🙂", 10000)}), &row)
	name := "张三\n[forged]🙂"
	row["source"].(map[string]any)["user_name"] = name
	data, _ := json.Marshal(row)
	path, inventory, canonical := writeArchive(t, data)
	if _, err := PrepareArchive(scope(), inventory, path, "", canonical); err != nil {
		t.Fatal(err)
	}
	ledger, err := ReadLedger(canonical, scope(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := BuildContexts(scope(), []domain.Message{*ledger.Records[0].Message})
	if err != nil {
		t.Fatal(err)
	}
	quoted, _ := json.Marshal(name)
	for _, c := range cs {
		if !strings.Contains(c.Text, "archived_display_name="+string(quoted)) {
			t.Fatal("archived display name provenance missing or unquoted")
		}
		if len(c.Text) > MaxContextBytes {
			t.Fatal("display name exceeded context byte budget")
		}
	}
}

func TestArchiveAmbiguousExistingAuthorMapFallsBackToOriginalNamespace(t *testing.T) {
	row := archiveRow(t, "a", "old-app", "old-author", "union", "text", map[string]any{"text": "x"})
	path, inventory, canonical := writeArchive(t, row)
	authors, obj := storedMap(t, t.TempDir())
	first := obj["mappings"].([]any)[0]
	encoded, _ := json.Marshal(first)
	var second map[string]any
	json.Unmarshal(encoded, &second)
	second["current_author_id"] = "another-person"
	second["source"].(map[string]any)["AuthorID"] = "another-person"
	evidence := second["evidence"].(map[string]any)
	raw := strings.ReplaceAll(evidence["raw_body"].(string), "current-person", "another-person")
	evidence["raw_body"] = raw
	evidence["raw_sha256"] = bytesDigest([]byte(raw))
	obj["mappings"] = []any{first, second}
	encoded, _ = json.Marshal(obj)
	os.WriteFile(authors, encoded, 0600)
	if _, err := PrepareArchive(scope(), inventory, path, authors, canonical); err != nil {
		t.Fatal(err)
	}
	ledger, err := ReadLedger(canonical, scope(), inventory)
	if err != nil || ledger.Records[0].Message.Source.AuthorID != "archive:old-app:old-author" {
		t.Fatalf("ambiguous existing author information guessed an identity: %v", err)
	}
}

func TestArchiveOpaqueUnsupportedArrayPreservesHumanOriginalWithoutMemory(t *testing.T) {
	row := archiveRow(t, "a", "old-app", "old-author", "union", "merge_forward", []any{map[string]any{"text": "original forwarded item🙂"}})
	path, inventory, canonical := writeArchive(t, row)
	report, err := PrepareArchive(scope(), inventory, path, "", canonical)
	if err != nil || report.Counts["human"] != 1 || report.Counts["opaque_sources"] != 1 || report.Counts["media_only"] != 0 {
		t.Fatalf("opaque human original dropped or misclassified: %+v %v", report, err)
	}
	ledger, err := ReadLedger(canonical, scope(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	m := ledger.Records[0].Message
	if m == nil || m.Text != "" || !strings.Contains(string(m.Raw), "original forwarded item") {
		t.Fatal("opaque original content lost or interpreted")
	}
	cs, err := BuildContexts(scope(), []domain.Message{*m})
	if err != nil || len(cs) != 0 {
		t.Fatal("opaque source produced memory input")
	}
	store, _, _ := importStore(t)
	applied, err := Apply(context.Background(), store, scope(), inventory, canonical)
	if err != nil || applied.Counts["accepted"] != 1 || applied.Counts["contexts"] != 0 {
		t.Fatalf("opaque source apply: %+v %v", applied, err)
	}
	got, err := store.Message(context.Background(), scope(), "a")
	if err != nil || got.Text != "" || !strings.Contains(string(got.Raw), "original forwarded item") {
		t.Fatal("opaque original was not preserved in PG")
	}
}

func TestArchiveOpaqueLiteralPreservesOriginalAndCreatesNoMemory(t *testing.T) {
	var row map[string]any
	json.Unmarshal(archiveRow(t, "literal", "old-app", "old-author", "union", "merge_forward", []any{}), &row)
	var event map[string]any
	json.Unmarshal([]byte(row["raw_body"].(string)), &event)
	content := "unparsed provider original🙂\nnot JSON"
	event["event"].(map[string]any)["message"].(map[string]any)["content"] = content
	raw, _ := json.Marshal(event)
	row["raw_body"] = string(raw)
	encoded, _ := json.Marshal(row)
	path, inventory, canonical := writeArchive(t, encoded)
	report, err := PrepareArchive(scope(), inventory, path, "", canonical)
	if err != nil || report.Counts["human"] != 1 || report.Counts["opaque_sources"] != 1 || report.Counts["quarantined"] != 0 {
		t.Fatalf("valid literal original dropped: %+v %v", report, err)
	}
	ledger, err := ReadLedger(canonical, scope(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	m := *ledger.Records[0].Message
	var meta domain.IngressMetadata
	var provenance archiveProvenance
	var preserved archiveRowRecord
	json.Unmarshal(m.Raw, &meta)
	json.Unmarshal(meta.Provider, &provenance)
	json.Unmarshal(provenance.Row, &preserved)
	if preserved.RawBody != string(raw) || m.Text != "" || len(m.Media) != 0 {
		t.Fatal("opaque literal was altered or interpreted")
	}
	contexts, err := BuildContexts(scope(), []domain.Message{m})
	if err != nil || len(contexts) != 0 {
		t.Fatal("opaque literal produced memory input")
	}
}

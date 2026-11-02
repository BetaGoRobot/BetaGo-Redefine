package historyimport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	_ "github.com/lib/pq"
)

func importStore(t *testing.T) (*pgstore.Store, *sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("real isolated PostgreSQL required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	schema := "history_test_" + hex.EncodeToString(nonce[:])
	store, err := pgstore.New(db, schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); db.Close() })
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store, db, schema
}
func frozen(t *testing.T, messages []domain.Message) (string, []byte) {
	t.Helper()
	var ids strings.Builder
	byID := map[string]domain.Message{}
	for _, m := range messages {
		ids.WriteString(m.Source.MessageID + "\n")
		byID[m.Source.MessageID] = m
	}
	path := filepath.Join(t.TempDir(), "canonical")
	inventory := []byte(ids.String())
	reader := readFunc(func(_ context.Context, _ domain.Scope, id string) (domain.Message, error) { return byID[id], nil })
	if _, err := Prepare(context.Background(), reader, scope(), inventory, path, 10); err != nil {
		t.Fatal(err)
	}
	return path, inventory
}

type brokenEnqueue struct {
	*pgstore.Store
	failed bool
}

func (s *brokenEnqueue) Enqueue(ctx context.Context, spec domain.JobSpec) (int64, error) {
	if !s.failed {
		s.failed = true
		return 0, errors.New("crash after PutContext")
	}
	return s.Store.Enqueue(ctx, spec)
}
func TestApplyIsIdempotentAcrossContextEnqueueCrashAndCompletedJobs(t *testing.T) {
	store, db, schema := importStore(t)
	ctx := context.Background()
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	path, inventory := frozen(t, []domain.Message{message("a", "alice", "source one", at), message("b", "bob", "source two", at)})
	broken := &brokenEnqueue{Store: store}
	if _, err := Apply(ctx, broken, scope(), inventory, path); err == nil {
		t.Fatal("injected context/enqueue crash ignored")
	}
	report, err := Apply(ctx, store, scope(), inventory, path)
	if err != nil || report.Counts["contexts"] != 2 {
		t.Fatalf("resume failed: %+v %v", report, err)
	}
	// Mark work completed directly in the isolated fixture. A rerun must preserve
	// its identity/status and fill only missing jobs.
	if _, err = db.Exec(`UPDATE "` + schema + `".jobs SET status='completed'`); err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, store, scope(), inventory, path); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT kind,status,count(*) FROM "` + schema + `".jobs GROUP BY kind,status`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var kind, status string
		var count int
		if err = rows.Scan(&kind, &status, &count); err != nil {
			t.Fatal(err)
		}
		if status != "completed" {
			t.Fatal("rerun reset completed work")
		}
		counts[kind] = count
	}
	if len(counts) != 3 || counts[string(domain.JobIndexMessage)] != 2 || counts[string(domain.JobEmbedContext)] != 2 || counts[string(domain.JobMemory)] != 2 {
		t.Fatalf("unexpected participation or duplicate work: %+v", counts)
	}
}
func TestApplyValidatesEntireLedgerBeforeFirstWriteAndUsesCurrentSource(t *testing.T) {
	store, db, schema := importStore(t)
	ctx := context.Background()
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	a := message("a", "alice", "old", at)
	b := message("b", "bob", "valid", at)
	path, inventory := frozen(t, []domain.Message{a, b})
	original, _ := os.ReadFile(path)
	forged := strings.Replace(string(original), `"sender_type":"user"`, `"sender_type":"app"`, 1)
	os.WriteFile(path, []byte(forged), 0600)
	if _, err := Apply(ctx, store, scope(), inventory, path); err == nil {
		t.Fatal("forged ledger accepted")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM "` + schema + `".events`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial write preceded full validation")
	}
	os.WriteFile(path, original, 0600)
	current := a.Clone()
	current.Source.Version += 2
	current.UpdatedAt = current.UpdatedAt.Add(time.Millisecond)
	current.Text = "latest canonical"
	current.EventID = "online"
	current.Raw = []byte(`{"kind":"message","sender_type":"user","addressed":true}`)
	if _, err := store.Accept(ctx, domain.Event{ID: current.EventID, Type: domain.EventMessage, Message: current, OccurredAt: current.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	report, err := Apply(ctx, store, scope(), inventory, path)
	if err != nil || report.Counts["stale"] != 1 {
		t.Fatalf("stale source apply: %+v %v", report, err)
	}
	rows, err := db.Query(`SELECT body FROM "` + schema + `".contexts`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "latest canonical") {
			found = true
		}
		if strings.Contains(string(body), `\\nold\\n`) {
			t.Fatal("stale input reached derived context")
		}
	}
	if !found {
		t.Fatal("current canonical facts were not used")
	}
}
func TestBuildContextsSkipsMediaOnlyButPreparePreservesHumanOriginal(t *testing.T) {
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	m := message("photo", "alice", "", at)
	m.Media = []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "image", Sequence: 0}}
	provider(m, "image", map[string]any{"image_key": "image"}, &m)
	path, inventory := frozen(t, []domain.Message{m})
	ledger, err := ReadLedger(path, scope(), inventory)
	if err != nil || len(ledger.Records) != 1 || ledger.Records[0].Status != "human" {
		t.Fatalf("human media lost: %+v %v", ledger, err)
	}
	contexts, err := BuildContexts(scope(), []domain.Message{m})
	if err != nil || len(contexts) != 0 {
		t.Fatalf("empty media made memory input: %+v %v", contexts, err)
	}
}

func TestBackfillEventDeduplicatesFactsAcrossArchiveAndRefreshAudit(t *testing.T) {
	store, _, _ := importStore(t)
	ctx := context.Background()
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	row := archiveRow(t, "a", scope().AppID, "alice", "union", "text", map[string]any{"text": "same canonical"})
	path, inventory, canonical := writeArchive(t, row)
	if _, err := PrepareArchive(scope(), inventory, path, "", canonical); err != nil {
		t.Fatal(err)
	}
	ledger, err := ReadLedger(canonical, scope(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := BackfillEvent(*ledger.Records[0].Message)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := BackfillEvent(message("a", "alice", "same canonical", at))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcceptHistorical(ctx, archived); err != nil {
		t.Fatal(err)
	}
	result, err := store.AcceptHistorical(ctx, refreshed)
	if err != nil || !result.Duplicate || len(result.JobIDs) != 0 {
		t.Fatalf("cross-source semantic duplicate: %+v %v", result, err)
	}
	if archived.ID == refreshed.ID {
		t.Fatal("distinct immutable audit events share identity")
	}
	again, _ := BackfillEvent(*ledger.Records[0].Message)
	if again.ID != archived.ID {
		t.Fatal("same frozen input changed backfill identity")
	}
	refreshed.Message.Text = "changed facts"
	if _, err = store.AcceptHistorical(ctx, refreshed); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("real immutable conflict escaped: %v", err)
	}
}

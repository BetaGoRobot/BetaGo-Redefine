package musiccards

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musicmedia"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	_ "github.com/lib/pq"
)

type fakeMessenger struct {
	mu                  sync.Mutex
	sends, patches      int
	failSend, failPatch bool
	contents, uuids     []string
	sequences           []int
	afterSend           func()
	unsupportedAudio    bool
}

func (f *fakeMessenger) Send(_ context.Context, _ domain.Scope, _, _, content, uuid string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends++
	f.contents = append(f.contents, content)
	f.uuids = append(f.uuids, uuid)
	if f.failSend {
		return "", errors.New("secret transport body")
	}
	if f.afterSend != nil {
		f.afterSend()
	}
	return "actual-message", nil
}
func (f *fakeMessenger) UpdateMusicCard(_ context.Context, _ domain.Scope, _, content, uuid string, seq int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patches++
	f.contents = append(f.contents, content)
	f.uuids = append(f.uuids, uuid)
	f.sequences = append(f.sequences, seq)
	if f.unsupportedAudio && strings.Contains(content, `"tag":"audio"`) {
		return domain.ErrMusicAudioUnsupported
	}
	if f.failPatch {
		return errors.New("secret patch body")
	}
	return nil
}
func (f *fakeMessenger) Retract(context.Context, domain.Scope, string) error { return nil }

type fakeMedia struct{ failAudio bool }

func (fakeMedia) ResolveCover(context.Context, domain.Scope, string, int64, string) (musicmedia.Cover, error) {
	return musicmedia.Cover{ImageKey: "img_actual", URL: "https://assets.test/cover"}, nil
}
func (fakeMedia) PrepareSong(_ context.Context, _ domain.Scope, id int64, _ bool) (musicmedia.Song, error) {
	return musicmedia.Song{Item: business.MusicItem{ID: id, Name: "selected", AlbumID: 72}, Lyrics: business.MusicLyrics{Full: "full lyrics"}, ImageKey: "img_actual", PlayerURL: "https://player.test/play", LyricsURL: "https://assets.test/lyrics"}, nil
}
func (f fakeMedia) UploadAudio(context.Context, domain.Scope, int64, bool) (musicmedia.Audio, error) {
	if f.failAudio {
		return musicmedia.Audio{}, errors.New("upload failed")
	}
	return musicmedia.Audio{FileKey: "file_actual", DurationMS: 5000}, nil
}

type fixture struct {
	*Service
	db        *sql.DB
	store     *pgstore.Store
	schema    string
	messenger *fakeMessenger
	identity  business.Identity
}

func setupRuntime(t *testing.T) fixture {
	t.Helper()
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("real isolated PostgreSQL required")
	}
	db, e := sql.Open("postgres", dsn)
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(16)
	var b [8]byte
	rand.Read(b[:])
	schema := "music_test_" + hex.EncodeToString(b[:])
	store, e := pgstore.New(db, schema)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); db.Close() })
	ctx := context.Background()
	if e = store.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = store.SyncToolPolicies(ctx, []pgstore.ToolPolicy{{Name: "music_send_card", Digest: "music-selected-test-v1", SideEffect: true, AutoExecute: true}, {Name: "music_send_search_card", Digest: "music-search-test-v1", SideEffect: true, AutoExecute: true}}); e != nil {
		t.Fatal(e)
	}
	scope := domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "chat", Visibility: "group"}
	now := time.Now().UTC()
	msg := domain.Message{Source: domain.SourceRef{Scope: scope, MessageID: "source", Version: 1, AuthorID: "requester", OccurredAt: now}, EventID: "source-event", Text: "music", UpdatedAt: now}
	if _, e = store.Accept(ctx, domain.Event{ID: "source-event", Type: domain.EventMessage, OccurredAt: now, Message: msg}); e != nil {
		t.Fatal(e)
	}
	task, e := store.CreateTask(ctx, msg, time.Now().Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/cloudsearch":
			w.Write([]byte(`{"code":200,"result":{"songCount":12,"songs":[{"id":41,"name":"one"},{"id":42,"name":"two"}]}}`))
		case "/comment/new/":
			w.Write([]byte(`{"code":200,"data":{"comments":[{"content":"comment","user":{"nickname":"author"}}]}}`))
		default:
			w.Write([]byte(`{"code":200,"songs":[{"id":41,"name":"one"},{"id":42,"name":"two"}]}`))
		}
	}))
	t.Cleanup(server.Close)
	provider, e := business.NewMusicClient(business.MusicConfig{BaseURL: server.URL})
	if e != nil {
		t.Fatal(e)
	}
	m := &fakeMessenger{}
	service, e := New(Options{DB: db, Schema: schema, Store: store, Provider: provider, Media: fakeMedia{}, Messenger: m, Member: func(context.Context, domain.Scope, string) (bool, error) { return true, nil }, SigningKey: []byte(strings.Repeat("k", 32))})
	if e != nil {
		t.Fatal(e)
	}
	if e = service.Initialize(ctx); e != nil {
		t.Fatal(e)
	}
	return fixture{service, db, store, schema, m, business.Identity{Scope: scope, ActorID: "requester", TaskID: task.ID, ExecutionKey: "execution"}}
}
func (f fixture) claim(t *testing.T) domain.Job {
	t.Helper()
	j, e := f.store.ClaimJob(context.Background(), []domain.JobKind{domain.JobPresentation}, "worker", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func findToken(t *testing.T, content, action string) string {
	t.Helper()
	var walk func(any) string
	walk = func(v any) string {
		switch x := v.(type) {
		case map[string]any:
			if token, _ := x["token"].(string); token != "" {
				c, e := verifyAction([]byte(strings.Repeat("k", 32)), token, time.Now())
				if e == nil && c.Action == action {
					return token
				}
			}
			for _, v := range x {
				if token := walk(v); token != "" {
					return token
				}
			}
		case []any:
			for _, v := range x {
				if token := walk(v); token != "" {
					return token
				}
			}
		}
		return ""
	}
	var v any
	json.Unmarshal([]byte(content), &v)
	token := walk(v)
	if token == "" {
		t.Fatalf("no %s token in %s", action, content)
	}
	return token
}

func TestInitialSendUnknownIsDurableAndNeverReplayed(t *testing.T) {
	f := setupRuntime(t)
	f.messenger.failSend = true
	ctx := context.Background()
	r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if !errors.Is(e, domain.ErrEffectUnknown) || r.State != "unknown" {
		t.Fatalf("receipt %#v %v", r, e)
	}
	if strings.Contains(e.Error(), "secret") {
		t.Fatal("raw error escaped")
	}
	f.messenger.failSend = false
	r, e = f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if !errors.Is(e, domain.ErrEffectUnknown) || f.messenger.sends != 1 {
		t.Fatalf("replayed %#v %v sends=%d", r, e, f.messenger.sends)
	}
}

func TestCallbacksBindMessageScopeDisplayedIDsAndDeduplicate(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if e != nil || r.MessageID != "actual-message" {
		t.Fatal(r, e)
	}
	token := findToken(t, f.messenger.contents[0], "play")
	in := ActionInvocation{Scope: f.identity.Scope, ActorID: "member", EventID: "click", MessageID: r.MessageID, Token: token}
	bad := in
	bad.Scope.ChatID = "other"
	if e = f.HandleAction(ctx, bad); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("cross group %v", e)
	}
	bad = in
	bad.MessageID = "forged"
	if e = f.HandleAction(ctx, bad); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("forged target %v", e)
	}
	c, _ := verifyAction(f.key, token, time.Now())
	c.ID = 999
	bad = in
	bad.Token = signAction(f.key, c)
	if e = f.HandleAction(ctx, bad); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("hidden ID %v", e)
	}
	if e = f.HandleAction(ctx, in); e != nil {
		t.Fatal(e)
	}
	if e = f.HandleAction(ctx, in); e != nil {
		t.Fatalf("duplicate %v", e)
	}
	bad = in
	bad.ActorID = "altered"
	if e = f.HandleAction(ctx, bad); !errors.Is(e, domain.ErrConflict) {
		t.Fatalf("altered duplicate %v", e)
	}
	in.EventID = "replacement-public-intent"
	if e = f.HandleAction(ctx, in); e != nil {
		t.Fatalf("still-public view cannot replace pending intent %v", e)
	}
}

func TestPatchRetryKeepsExactFrozenBodyUUIDAndSequence(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	_, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if e != nil {
		t.Fatal(e)
	}
	j := f.claim(t)
	f.messenger.failPatch = true
	if e = f.Process(ctx, j); e == nil {
		t.Fatal("expected patch receipt failure")
	}
	f.messenger.failPatch = false
	if e = f.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	if len(f.messenger.contents) < 3 {
		t.Fatal("no retry patch")
	}
	// Other independent resources may finish first on retry; locate the exact
	// frozen UUID rather than depending on worker completion order.
	found := false
	for i := 2; i < len(f.messenger.contents); i++ {
		if f.messenger.uuids[i] == f.messenger.uuids[1] {
			found = true
			if f.messenger.contents[i] != f.messenger.contents[1] {
				t.Fatal("retry changed frozen body")
			}
		}
	}
	if !found {
		t.Fatal("frozen effect was not retried")
	}

}

func TestStaleLeaseCannotPersistOrPatch(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	_, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if e != nil {
		t.Fatal(e)
	}
	j := f.claim(t)
	if _, e = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET fence=fence+1 WHERE id=$1`, j.ID); e != nil {
		t.Fatal(e)
	}
	if e = f.Process(ctx, j); !errors.Is(e, domain.ErrStaleClaim) {
		t.Fatalf("stale claim %v", e)
	}
	if f.messenger.patches != 0 {
		t.Fatal("stale worker patched")
	}
}

func TestNewPageInvalidatesSlowEnrichmentAndWithdrawalNeedsRequester(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if e != nil {
		t.Fatal(e)
	}
	slow := f.claim(t)
	content := f.messenger.contents[0]
	withdraw := findToken(t, content, "withdraw")
	in := ActionInvocation{Scope: f.identity.Scope, ActorID: "member", EventID: "withdraw-member", MessageID: r.MessageID, Token: withdraw}
	if e = f.HandleAction(ctx, in); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("member withdrawal %v", e)
	}
	in.EventID = "next-page"
	in.Token = findToken(t, content, "page")
	c, _ := verifyAction(f.key, in.Token, time.Now())
	c.Page = 2
	in.Token = signAction(f.key, c)
	if e = f.HandleAction(ctx, in); e != nil {
		t.Fatal(e)
	}
	if e = f.Process(ctx, slow); e != nil && !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	if f.messenger.patches != 0 {
		t.Fatal("older enrichment patched newer page")
	}
	next := f.claim(t)
	if e = f.Process(ctx, next); e != nil {
		t.Fatal(e)
	}
	if f.messenger.patches == 0 {
		t.Fatal("navigation did not patch same card")
	}
}

func TestAudioFailureKeepsPlayerAndActualCardWithoutFakeFile(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	f.Service.media = fakeMedia{failAudio: true}
	r, e := f.Send(ctx, f.identity, "source", Request{Kind: "song", ID: 41})
	if e != nil {
		t.Fatal(e)
	}
	if r.MessageID != "actual-message" || !strings.Contains(f.messenger.contents[0], "player.test") {
		t.Fatal("detail player missing")
	}
	j := f.claim(t)
	if e = f.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	if f.messenger.patches != 1 || !strings.Contains(f.messenger.contents[1], "player.test") || strings.Contains(f.messenger.contents[1], `"tag":"audio"`) {
		t.Fatal("partial audio failure lost detail or fabricated audio")
	}
}

func TestCompletedInitialReceiptSurvivesNewServiceInstance(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	request := Request{Scene: "song", Query: "test"}
	first, e := f.Send(ctx, f.identity, "source", request)
	if e != nil {
		t.Fatal(e)
	}
	restarted, e := New(Options{DB: f.db, Schema: f.schema, Store: f.store, Provider: f.provider, Media: fakeMedia{}, Messenger: f.messenger, SigningKey: f.key})
	if e != nil {
		t.Fatal(e)
	}
	if e = restarted.Initialize(ctx); e != nil {
		t.Fatal(e)
	}
	again, e := restarted.Send(ctx, f.identity, "source", request)
	if e != nil || again != first || f.messenger.sends != 1 {
		t.Fatalf("restart lost actual receipt %#v %#v %v", first, again, e)
	}
}

func TestInitialSendRejectsActorForgeryBeforeProviderEffect(t *testing.T) {
	f := setupRuntime(t)
	forged := f.identity
	forged.ActorID = "other-actor"
	r, e := f.Send(context.Background(), forged, "source", Request{Scene: "song", Query: "test"})
	if e == nil || r.State != "rejected" || f.messenger.sends != 0 {
		t.Fatalf("actor forged %#v %v", r, e)
	}
}

func TestConcurrentDuplicateCallbacksEnqueueOneNavigation(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if e != nil {
		t.Fatal(e)
	}
	in := ActionInvocation{Scope: f.identity.Scope, ActorID: "member", EventID: "concurrent-click", MessageID: r.MessageID, Token: findToken(t, f.messenger.contents[0], "play")}
	errorsCh := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errorsCh <- f.HandleAction(ctx, in) }()
	}
	wg.Wait()
	close(errorsCh)
	for e := range errorsCh {
		if e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if e = f.db.QueryRow(`SELECT count(*) FROM "` + f.schema + `".jobs WHERE kind='presentation'`).Scan(&n); e != nil || n != 2 {
		t.Fatalf("duplicate continuation count %d %v", n, e)
	}
}

func TestNativeAudioUsesActualHandleAndLyricsRemainNewReply(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	r, e := f.Send(ctx, f.identity, "source", Request{Kind: "song", ID: 41})
	if e != nil {
		t.Fatal(e)
	}
	j := f.claim(t)
	if e = f.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	if e = f.store.CompleteJob(ctx, j); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(f.messenger.contents[1], "file_actual") || !strings.Contains(f.messenger.contents[1], `"tag":"audio"`) {
		t.Fatal("actual uploaded audio missing")
	}
	in := ActionInvocation{Scope: f.identity.Scope, ActorID: "member", EventID: "lyrics-click", MessageID: r.MessageID, Token: findToken(t, f.messenger.contents[1], "lyrics")}
	if e = f.HandleAction(ctx, in); e != nil {
		t.Fatal(e)
	}
	j = f.claim(t)
	if e = f.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	if f.messenger.sends != 2 || f.messenger.patches != 1 || !strings.Contains(f.messenger.contents[2], "full lyrics") {
		t.Fatal("lyrics did not create separate actual reply")
	}
	if e = f.Process(ctx, j); e != nil || f.messenger.sends != 2 {
		t.Fatalf("lyrics reply replayed %v", e)
	}
}

func TestHigherSequenceMakesFrozenOlderPatchHarmless(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if e != nil {
		t.Fatal(e)
	}
	j := f.claim(t)
	s, e := f.load(ctx, f.db, r.SessionID, false)
	if e != nil {
		t.Fatal(e)
	}
	c := continuation{Session: s.ID, Version: s.Version, Work: "enrich"}
	f.messenger.failPatch = true
	if e = f.patch(ctx, j, s, c, "first", func(v *session) { v.Model.Notice = "first" }); e == nil {
		t.Fatal("expected lost receipt")
	}
	f.messenger.failPatch = false
	if e = f.patch(ctx, j, s, c, "later", func(v *session) { v.Model.Notice = "later" }); e != nil {
		t.Fatal(e)
	}
	before := f.messenger.patches
	if e = f.patch(ctx, j, s, c, "first", func(v *session) { v.Model.Notice = "must not happen" }); e != nil {
		t.Fatal(e)
	}
	if f.messenger.patches != before {
		t.Fatal("an older frozen sequence was sent over a newer frame")
	}
}

func TestSelectedSongAlbumActionCreatesSeparateTracklistReply(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	r, e := f.Send(ctx, f.identity, "source", Request{Kind: "song", ID: 41})
	if e != nil {
		t.Fatal(e)
	}
	token := findToken(t, f.messenger.contents[0], "album")
	c, e := verifyAction(f.key, token, time.Now())
	if e != nil || c.ID != 72 {
		t.Fatal("selected album identity lost", e)
	}
	in := ActionInvocation{Scope: f.identity.Scope, ActorID: "member", EventID: "album-click", MessageID: r.MessageID, Token: token}
	if e = f.HandleAction(ctx, in); e != nil {
		t.Fatal(e)
	}
	// The initial detail's audio continuation precedes the album action.
	j := f.claim(t)
	if e = f.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	if e = f.store.CompleteJob(ctx, j); e != nil {
		t.Fatal(e)
	}
	j = f.claim(t)
	if e = f.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	if f.messenger.sends != 2 || !strings.Contains(f.messenger.contents[len(f.messenger.contents)-1], "one") {
		t.Fatal("album did not create tracklist reply")
	}
}

func TestLeaseExpiresWhileWaitingForSourceOrSessionLock(t *testing.T) {
	for _, lockKind := range []string{"source", "session"} {
		t.Run(lockKind, func(t *testing.T) {
			f := setupRuntime(t)
			ctx := context.Background()
			r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
			if e != nil {
				t.Fatal(e)
			}
			j, e := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobPresentation}, "worker", 100*time.Millisecond)
			if e != nil {
				t.Fatal(e)
			}
			s, e := f.load(ctx, f.db, r.SessionID, false)
			if e != nil {
				t.Fatal(e)
			}
			blocker, e := f.db.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer blocker.Rollback()
			if lockKind == "source" {
				_, e = blocker.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, s.Identity.Scope.Key()+":"+s.Source.MessageID)
			} else {
				_, e = blocker.Exec(`SELECT id FROM "`+f.schema+`".music_card_sessions WHERE id=$1 FOR UPDATE`, s.ID)
			}
			if e != nil {
				t.Fatal(e)
			}
			result := make(chan error, 1)
			go func() {
				result <- f.patch(ctx, j, s, continuation{Version: s.Version}, "held-lock", func(current *session) { current.Model.Notice = "stale worker" })
			}()
			time.Sleep(200 * time.Millisecond)
			if e = blocker.Commit(); e != nil {
				t.Fatal(e)
			}
			if e = <-result; !errors.Is(e, domain.ErrStaleClaim) {
				t.Fatalf("lease guard %v", e)
			}
			current, e := f.load(ctx, f.db, s.ID, false)
			if e != nil {
				t.Fatal(e)
			}
			if current.Sequence != 1 {
				t.Fatalf("expired lease persisted sequence %d", current.Sequence)
			}
		})
	}
}

func TestCallbackActorMustStillBeCurrentMemberBeforeAnyEffect(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if e != nil {
		t.Fatal(e)
	}
	in := ActionInvocation{Scope: f.identity.Scope, ActorID: "left-member", EventID: "left-click", MessageID: r.MessageID, Token: findToken(t, f.messenger.contents[0], "play")}
	if e = f.HandleAction(ctx, in); e != nil {
		t.Fatal(e)
	}
	f.member = func(_ context.Context, scope domain.Scope, actor string) (bool, error) {
		if scope != in.Scope || actor != in.ActorID {
			t.Error("membership identity changed")
		}
		return false, nil
	}
	j := f.claim(t)
	if e = f.Process(ctx, j); e != nil && !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	if e = f.store.CompleteJob(ctx, j); e != nil {
		t.Fatal(e)
	}
	j = f.claim(t)
	if e = f.Process(ctx, j); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("nonmember action effect %v", e)
	}
	if f.messenger.sends != 1 || f.messenger.patches != 0 {
		t.Fatal("nonmember triggered external card effect")
	}
}

func TestSuccessfulSendReceiptFinalizationHasBoundedDetachedContext(t *testing.T) {
	f := setupRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var blocker *sql.Tx
	f.messenger.afterSend = func() {
		token := findToken(t, f.messenger.contents[0], "play")
		claims, e := verifyAction(f.key, token, time.Now())
		if e != nil {
			t.Error(e)
			return
		}
		blocker, e = f.db.BeginTx(context.Background(), nil)
		if e != nil {
			t.Error(e)
			return
		}
		if _, e = blocker.Exec(`SELECT id FROM "`+f.schema+`".music_card_sessions WHERE id=$1 FOR UPDATE`, claims.Session); e != nil {
			t.Error(e)
		}
		cancel()
	}
	result := make(chan error, 1)
	go func() { _, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"}); result <- e }()
	select {
	case e := <-result:
		if !errors.Is(e, domain.ErrEffectUnknown) {
			t.Fatalf("blocked receipt %v", e)
		}
	case <-time.After(7 * time.Second):
		if blocker != nil {
			blocker.Rollback()
		}
		<-result
		t.Fatal("successful send receipt persistence ignored bounded deadline")
	}
	if blocker != nil {
		blocker.Rollback()
	}
	if f.messenger.sends != 1 {
		t.Fatal("unexpected duplicate send")
	}
	if _, e := f.Send(context.Background(), f.identity, "source", Request{Scene: "song", Query: "test"}); !errors.Is(e, domain.ErrEffectUnknown) || f.messenger.sends != 1 {
		t.Fatal("blocked successful send replayed")
	}
}

type recoverCoverMedia struct {
	fakeMedia
	mu        sync.Mutex
	refreshes int
}

func (*recoverCoverMedia) ResolveCover(context.Context, domain.Scope, string, int64, string) (musicmedia.Cover, error) {
	return musicmedia.Cover{}, domain.ErrEffectUnknown
}
func (m *recoverCoverMedia) RefreshCover(context.Context, domain.Scope, string, int64, string) (musicmedia.Cover, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshes++
	return musicmedia.Cover{ImageKey: "img_refreshed"}, nil
}
func TestExplicitListRefreshRecoversUnknownCoverWithNewViewTokens(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	media := &recoverCoverMedia{}
	f.Service.media = media
	r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if e != nil {
		t.Fatal(e)
	}
	j := f.claim(t)
	if e = f.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	if e = f.store.CompleteJob(ctx, j); e != nil {
		t.Fatal(e)
	}
	in := ActionInvocation{Scope: f.identity.Scope, ActorID: "member", EventID: "explicit-refresh", MessageID: r.MessageID, Token: findToken(t, f.messenger.contents[len(f.messenger.contents)-1], "refresh")}
	if e = f.HandleAction(ctx, in); e != nil {
		t.Fatal(e)
	}
	j = f.claim(t)
	if e = f.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	if e = f.store.CompleteJob(ctx, j); e != nil {
		t.Fatal(e)
	}
	j = f.claim(t)
	if e = f.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	if media.refreshes != 2 {
		t.Fatalf("explicit refresh did not force independent cover recovery: %d", media.refreshes)
	}
	last := f.messenger.contents[len(f.messenger.contents)-1]
	if !strings.Contains(last, "img_refreshed") {
		t.Fatal("fresh actual cover handle not patched")
	}
	token := findToken(t, last, "play")
	c, e := verifyAction(f.key, token, time.Now())
	if e != nil || c.Version != 2 || c.Scope != f.identity.Scope {
		t.Fatalf("refresh lost newview callback binding %#v %v", c, e)
	}
}

type blockingViewMedia struct {
	fakeMedia
	cover, audio bool
	started      chan struct{}
	once         sync.Once
}

func (m *blockingViewMedia) ResolveCover(ctx context.Context, _ domain.Scope, _ string, _ int64, _ string) (musicmedia.Cover, error) {
	if !m.cover {
		return m.fakeMedia.ResolveCover(ctx, domain.Scope{}, "", 0, "")
	}
	m.once.Do(func() { close(m.started) })
	<-ctx.Done()
	return musicmedia.Cover{}, ctx.Err()
}
func (m *blockingViewMedia) UploadAudio(ctx context.Context, _ domain.Scope, _ int64, _ bool) (musicmedia.Audio, error) {
	if !m.audio {
		return m.fakeMedia.UploadAudio(ctx, domain.Scope{}, 0, false)
	}
	m.once.Do(func() { close(m.started) })
	<-ctx.Done()
	return musicmedia.Audio{}, ctx.Err()
}
func TestViewSupersessionCancelsBlockedMediaAndReleasesPresentationWorker(t *testing.T) {
	for _, kind := range []string{"cover", "audio"} {
		t.Run(kind, func(t *testing.T) {
			f := setupRuntime(t)
			ctx := context.Background()
			media := &blockingViewMedia{cover: kind == "cover", audio: kind == "audio", started: make(chan struct{})}
			f.Service.media = media
			request := Request{Scene: "song", Query: "test"}
			action := "page"
			if kind == "audio" {
				request = Request{Kind: "song", ID: 41}
				action = "refresh"
			}
			r, e := f.Send(ctx, f.identity, "source", request)
			if e != nil {
				t.Fatal(e)
			}
			j := f.claim(t)
			workCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- f.Process(workCtx, j) }()
			select {
			case <-media.started:
			case <-time.After(time.Second):
				cancel()
				<-result
				t.Fatal("media work never started")
			}
			f.messenger.mu.Lock()
			initialContent := f.messenger.contents[0]
			f.messenger.mu.Unlock()
			token := findToken(t, initialContent, action)
			if kind == "cover" {
				claims, e := verifyAction(f.key, token, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				claims.Page = 2
				token = signAction(f.key, claims)
			}
			in := ActionInvocation{Scope: f.identity.Scope, ActorID: "member", EventID: "supersede-" + kind, MessageID: r.MessageID, Token: token}
			if e = f.HandleAction(ctx, in); e != nil {
				t.Fatal(e)
			}
			atChange, e := f.load(ctx, f.db, r.SessionID, false)
			if e != nil {
				t.Fatal(e)
			}
			select {
			case e = <-result:
				if !errors.Is(e, domain.ErrConflict) {
					t.Fatalf("superseded worker outcome %v", e)
				}
			case <-time.After(time.Second):
				cancel()
				<-result
				t.Fatal("old view still occupies a presentation worker")
			}
			old, e := f.load(ctx, f.db, r.SessionID, false)
			if e != nil {
				t.Fatal(e)
			}
			if old.Version != 1 || old.Pending == nil || old.Sequence != atChange.Sequence {
				t.Fatalf("old media mutated new view %#v", old)
			}
			patchesBefore := f.messenger.patches
			if e = f.store.CompleteJob(ctx, j); e != nil {
				t.Fatal(e)
			}
			next := f.claim(t)
			if e = f.Process(ctx, next); e != nil {
				t.Fatal(e)
			}
			if f.messenger.patches != patchesBefore+1 {
				t.Fatal("new view did not use released worker")
			}
			latest := findToken(t, f.messenger.contents[len(f.messenger.contents)-1], "refresh")
			claims, e := verifyAction(f.key, latest, time.Now())
			if e != nil || claims.Version != 2 {
				t.Fatal("new view callback version lost", e)
			}
		})
	}
}

func TestMediaViewWatcherPreservesParentAndLeaseCancellation(t *testing.T) {
	for _, kind := range []string{"parent", "lease"} {
		t.Run(kind, func(t *testing.T) {
			f := setupRuntime(t)
			ctx := context.Background()
			media := &blockingViewMedia{audio: true, started: make(chan struct{})}
			f.Service.media = media
			_, e := f.Send(ctx, f.identity, "source", Request{Kind: "song", ID: 41})
			if e != nil {
				t.Fatal(e)
			}
			j := f.claim(t)
			workCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- f.Process(workCtx, j) }()
			select {
			case <-media.started:
			case <-time.After(time.Second):
				cancel()
				<-result
				t.Fatal("audio never started")
			}
			want := error(context.Canceled)
			if kind == "parent" {
				cancel()
			} else {
				want = domain.ErrStaleClaim
				if _, e = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID); e != nil {
					t.Fatal(e)
				}
			}
			select {
			case e = <-result:
				if !errors.Is(e, want) {
					t.Fatalf("cancellation cause %v wanted %v", e, want)
				}
			case <-time.After(time.Second):
				cancel()
				<-result
				t.Fatal("watcher ignored cancellation")
			}
			if f.messenger.patches != 0 {
				t.Fatal("canceled media patched")
			}
		})
	}
}

func TestRevokedPresentationGrantStopsEnrichmentWithoutFreezingPatch(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`UPDATE "` + f.schema + `".tool_policies SET enabled=false WHERE tool_name='music_send_search_card'`); e != nil {
		t.Fatal(e)
	}
	j := f.claim(t)
	if e = f.Process(ctx, j); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("revoked search presentation grant %v", e)
	}
	current, e := f.load(ctx, f.db, r.SessionID, false)
	if e != nil {
		t.Fatal(e)
	}
	var frames int
	if e = f.db.QueryRow(`SELECT count(*) FROM "`+f.schema+`".music_card_frames WHERE session_id=$1`, r.SessionID).Scan(&frames); e != nil {
		t.Fatal(e)
	}
	if f.messenger.sends != 1 || f.messenger.patches != 0 || current.Sequence != 1 || frames != 1 {
		t.Fatalf("revoked grant admitted effects: sends=%d patches=%d sequence=%d frames=%d", f.messenger.sends, f.messenger.patches, current.Sequence, frames)
	}
}

type countingPrepareMedia struct {
	fakeMedia
	prepared int
}

func (m *countingPrepareMedia) PrepareSong(ctx context.Context, scope domain.Scope, id int64, refresh bool) (musicmedia.Song, error) {
	m.prepared++
	return m.fakeMedia.PrepareSong(ctx, scope, id, refresh)
}
func TestSearchGrantDoesNotAuthorizeSelectedSongPlay(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	media := &countingPrepareMedia{}
	f.Service.media = media
	if _, e := f.db.Exec(`UPDATE "` + f.schema + `".tool_policies SET enabled=false WHERE tool_name='music_send_card'`); e != nil {
		t.Fatal(e)
	}
	r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if e != nil {
		t.Fatal("enabled search grant rejected", e)
	}
	in := ActionInvocation{Scope: f.identity.Scope, ActorID: "member", EventID: "selected-grant-play", MessageID: r.MessageID, Token: findToken(t, f.messenger.contents[0], "play")}
	if e = f.HandleAction(ctx, in); e != nil {
		t.Fatal(e)
	}
	old := f.claim(t)
	if e = f.Process(ctx, old); e != nil && !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	if e = f.store.CompleteJob(ctx, old); e != nil {
		t.Fatal(e)
	}
	j := f.claim(t)
	if e = f.Process(ctx, j); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("search grant authorized selected media %v", e)
	}
	current, e := f.load(ctx, f.db, r.SessionID, false)
	if e != nil {
		t.Fatal(e)
	}
	var frames int
	if e = f.db.QueryRow(`SELECT count(*) FROM "`+f.schema+`".music_card_frames WHERE session_id=$1`, r.SessionID).Scan(&frames); e != nil {
		t.Fatal(e)
	}
	if media.prepared != 0 || f.messenger.sends != 1 || f.messenger.patches != 0 || current.Sequence != 1 || frames != 1 {
		t.Fatalf("selected grant bypass: prepared=%d sends=%d patches=%d sequence=%d frames=%d", media.prepared, f.messenger.sends, f.messenger.patches, current.Sequence, frames)
	}
}

func TestInitialPresentationGrantFailsClosedForMissingOrRestrictedPolicy(t *testing.T) {
	for _, restriction := range []string{"missing", "disabled", "not-effect", "not-automatic"} {
		t.Run(restriction, func(t *testing.T) {
			f := setupRuntime(t)
			query := `DELETE FROM "` + f.schema + `".tool_policies WHERE tool_name='music_send_card'`
			switch restriction {
			case "disabled":
				query = `UPDATE "` + f.schema + `".tool_policies SET enabled=false WHERE tool_name='music_send_card'`
			case "not-effect":
				query = `UPDATE "` + f.schema + `".tool_policies SET side_effect=false WHERE tool_name='music_send_card'`
			case "not-automatic":
				query = `UPDATE "` + f.schema + `".tool_policies SET auto_execute=false WHERE tool_name='music_send_card'`
			}
			if _, e := f.db.Exec(query); e != nil {
				t.Fatal(e)
			}
			r, e := f.Send(context.Background(), f.identity, "source", Request{Kind: "song", ID: 41})
			if !errors.Is(e, domain.ErrUnauthorized) || r.State != "rejected" || f.messenger.sends != 0 {
				t.Fatalf("restricted initial grant admitted %#v %v", r, e)
			}
			var frames int
			if e = f.db.QueryRow(`SELECT count(*) FROM "` + f.schema + `".music_card_frames`).Scan(&frames); e != nil || frames != 0 {
				t.Fatalf("unauthorized initial frame reserved %d %v", frames, e)
			}
		})
	}
}

type recordingAudioRefreshMedia struct {
	fakeMedia
	flags                            []bool
	prepared, prepareRefresh, forced int
}

func (m *recordingAudioRefreshMedia) PrepareSong(ctx context.Context, scope domain.Scope, id int64, refresh bool) (musicmedia.Song, error) {
	m.prepared++
	if refresh {
		m.prepareRefresh++
	}
	return m.fakeMedia.PrepareSong(ctx, scope, id, refresh)
}
func (m *recordingAudioRefreshMedia) UploadAudio(_ context.Context, _ domain.Scope, _ int64, refresh bool) (musicmedia.Audio, error) {
	m.flags = append(m.flags, refresh)
	if !refresh {
		return musicmedia.Audio{}, domain.ErrEffectUnknown
	}
	return musicmedia.Audio{FileKey: "file_refreshed_current_source", DurationMS: 5000}, nil
}

type currentSourceAudioRefresher struct{ *recordingAudioRefreshMedia }

func (m *currentSourceAudioRefresher) RefreshAudio(context.Context, domain.Scope, int64) (musicmedia.Audio, error) {
	m.forced++
	return musicmedia.Audio{FileKey: "file_refreshed_current_source", DurationMS: 5000}, nil
}
func TestExplicitAudioRefreshForcesCurrentNativeReceiptRecovery(t *testing.T) {
	for _, mode := range []string{"current-source-port", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			f := setupRuntime(t)
			ctx := context.Background()
			media := &recordingAudioRefreshMedia{}
			f.Service.media = media
			if mode == "current-source-port" {
				f.Service.media = &currentSourceAudioRefresher{media}
			}
			r, e := f.Send(ctx, f.identity, "source", Request{Kind: "song", ID: 41})
			if e != nil {
				t.Fatal(e)
			}
			j := f.claim(t)
			if e = f.Process(ctx, j); e != nil {
				t.Fatal(e)
			}
			if e = f.store.CompleteJob(ctx, j); e != nil {
				t.Fatal(e)
			}
			if strings.Contains(f.messenger.contents[len(f.messenger.contents)-1], `"tag":"audio"`) {
				t.Fatal("unknown native upload fabricated a handle")
			}
			token := findToken(t, f.messenger.contents[len(f.messenger.contents)-1], "refresh")
			if e = f.HandleAction(ctx, ActionInvocation{Scope: f.identity.Scope, ActorID: "member", EventID: "native-refresh-" + mode, MessageID: r.MessageID, Token: token}); e != nil {
				t.Fatal(e)
			}
			j = f.claim(t)
			if e = f.Process(ctx, j); e != nil {
				t.Fatal(e)
			}
			if e = f.store.CompleteJob(ctx, j); e != nil {
				t.Fatal(e)
			}
			j = f.claim(t)
			if e = f.Process(ctx, j); e != nil {
				t.Fatal(e)
			}
			if mode == "current-source-port" {
				if media.forced != 1 || len(media.flags) != 1 || media.flags[0] {
					t.Fatalf("current-source refresh was not forced independently: forced=%d flags=%v", media.forced, media.flags)
				}
			} else if media.forced != 0 || len(media.flags) != 2 || media.flags[0] || !media.flags[1] {
				t.Fatalf("fallback native refresh not forced: forced=%d flags=%v", media.forced, media.flags)
			}
			if media.prepared != 2 || media.prepareRefresh != 1 {
				t.Fatalf("explicit audio refresh prepared source again: prepared=%d refresh=%d", media.prepared, media.prepareRefresh)
			}
			last := f.messenger.contents[len(f.messenger.contents)-1]
			if !strings.Contains(last, "file_refreshed_current_source") || !strings.Contains(last, `"tag":"audio"`) {
				t.Fatal("fresh actual native handle was not patched")
			}
			claims, e := verifyAction(f.key, findToken(t, last, "refresh"), time.Now())
			if e != nil || claims.Version != 2 {
				t.Fatalf("new native receipt lost refreshed view binding %#v %v", claims, e)
			}
		})
	}
}

func TestConcurrentInitializeSerializesFreshMusicTables(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	if _, e := f.db.Exec(`DROP TABLE "` + f.schema + `".music_card_frames,"` + f.schema + `".music_card_events,"` + f.schema + `".music_card_sessions`); e != nil {
		t.Fatal(e)
	}
	const workers = 12
	start := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- f.Initialize(ctx) }()
	}
	close(start)
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Errorf("concurrent initial music schema creation: %v", e)
		}
	}
	if t.Failed() {
		return
	}
	if e := f.Initialize(ctx); e != nil {
		t.Fatalf("repeat initialization: %v", e)
	}
	var tables int
	if e := f.db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name IN ('music_card_sessions','music_card_frames','music_card_events')`, f.schema).Scan(&tables); e != nil || tables != 3 {
		t.Fatalf("incomplete initialized music tables count=%d error=%v", tables, e)
	}
}

type countingUploadMedia struct {
	fakeMedia
	uploads int
}

func (m *countingUploadMedia) UploadAudio(ctx context.Context, scope domain.Scope, id int64, refresh bool) (musicmedia.Audio, error) {
	m.uploads++
	return m.fakeMedia.UploadAudio(ctx, scope, id, refresh)
}
func TestUnsupportedNativeAudioFreezesNewFallbackAndResumesWithoutUpload(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	media := &countingUploadMedia{}
	f.Service.media = media
	r, e := f.Send(ctx, f.identity, "source", Request{Kind: "song", ID: 41})
	if e != nil {
		t.Fatal(e)
	}
	f.messenger.unsupportedAudio = true
	f.messenger.failPatch = true
	j := f.claim(t)
	if e = f.Process(ctx, j); e == nil {
		t.Fatal("fallback receipt failure not reported")
	}
	original, e := f.readFrame(ctx, r.SessionID, f.workKey(j, "audio"))
	if e != nil {
		t.Fatal(e)
	}
	if original.State != "unsupported" || original.Sequence != 2 || !strings.Contains(original.Content, "file_actual") {
		t.Fatalf("original frame was not terminal and immutable %#v", original)
	}
	current, e := f.load(ctx, f.db, r.SessionID, false)
	if e != nil {
		t.Fatal(e)
	}
	var fallback frame
	e = f.db.QueryRow(`SELECT sequence,view_version,uuid,content,mode,state,message_id FROM "`+f.schema+`".music_card_frames WHERE session_id=$1 AND sequence=3`, r.SessionID).Scan(&fallback.Sequence, &fallback.Version, &fallback.UUID, &fallback.Content, &fallback.Mode, &fallback.State, &fallback.MessageID)
	if e != nil || current.Sequence != 3 || strings.Contains(fallback.Content, `"tag":"audio"`) || !strings.Contains(fallback.Content, "player.test") || !strings.Contains(fallback.Content, "内嵌音频") {
		t.Fatalf("missing frozen no-audio fallback %#v %v", fallback, e)
	}
	f.messenger.failPatch = false
	restarted, e := New(Options{DB: f.db, Schema: f.schema, Store: f.store, Provider: f.provider, Media: media, Messenger: f.messenger, SigningKey: f.key})
	if e != nil {
		t.Fatal(e)
	}
	if e = restarted.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	again, e := f.readFrame(ctx, r.SessionID, f.workKey(j, "audio"))
	if e != nil {
		t.Fatal(e)
	}
	if again != original || media.uploads != 1 || f.messenger.sends != 1 {
		t.Fatal("fallback retry changed original effect or uploaded again")
	}
	last := len(f.messenger.contents) - 1
	if f.messenger.contents[last] != fallback.Content || f.messenger.uuids[last] != fallback.UUID || f.messenger.sequences[len(f.messenger.sequences)-1] != fallback.Sequence {
		t.Fatal("fallback retry did not reuse exact frozen effect")
	}
}
func TestOrdinaryNativePatchErrorDoesNotCreateUnsupportedFallback(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	r, e := f.Send(ctx, f.identity, "source", Request{Kind: "song", ID: 41})
	if e != nil {
		t.Fatal(e)
	}
	f.messenger.failPatch = true
	j := f.claim(t)
	if e = f.Process(ctx, j); e == nil {
		t.Fatal("missing native patch failure")
	}
	original, e := f.readFrame(ctx, r.SessionID, f.workKey(j, "audio"))
	if e != nil {
		t.Fatal(e)
	}
	current, e := f.load(ctx, f.db, r.SessionID, false)
	if e != nil {
		t.Fatal(e)
	}
	if original.State != "pending" || current.Sequence != 2 || !strings.Contains(original.Content, `"tag":"audio"`) {
		t.Fatal("ordinary patch error downgraded native audio")
	}
}

func TestDeniedMemberIntentPreservesOriginalCardAndCannotReplayIntoLaterView(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if e != nil {
		t.Fatal(e)
	}
	original := f.messenger.contents[0]
	in := ActionInvocation{Scope: f.identity.Scope, ActorID: "not-member", EventID: "denied-intent", MessageID: r.MessageID, Token: findToken(t, original, "play")}
	if e = f.HandleAction(ctx, in); e != nil {
		t.Fatal(e)
	}
	pending, e := f.load(ctx, f.db, r.SessionID, false)
	if e != nil {
		t.Fatal(e)
	}
	if pending.Version != 1 || pending.Request.Scene != "song" {
		t.Error("unverified actor changed public view")
	}
	old := f.claim(t)
	if e = f.Process(ctx, old); e != nil && !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	if e = f.store.CompleteJob(ctx, old); e != nil {
		t.Fatal(e)
	}
	f.member = func(context.Context, domain.Scope, string) (bool, error) { return false, nil }
	denied := f.claim(t)
	if e = f.Process(ctx, denied); !errors.Is(e, domain.ErrUnauthorized) {
		t.Fatalf("nonmember intent %v", e)
	}
	restored, e := f.load(ctx, f.db, r.SessionID, false)
	if e != nil {
		t.Fatal(e)
	}
	if restored.Version != 1 || restored.Request.Scene != "song" || restored.Sequence != 1 {
		t.Fatal("denied intent invalidated original public card")
	}
	owner := ActionInvocation{Scope: f.identity.Scope, ActorID: f.identity.ActorID, EventID: "owner-after-denial", MessageID: r.MessageID, Token: findToken(t, original, "refresh")}
	if e = f.HandleAction(ctx, owner); e != nil {
		t.Fatalf("original owner card controls no longer usable: %v", e)
	}
	f.member = func(context.Context, domain.Scope, string) (bool, error) { return true, nil }
	next := f.claim(t)
	if e = f.Process(ctx, next); e != nil {
		t.Fatal(e)
	}
	before := f.messenger.patches
	if e = f.Process(ctx, denied); e != nil && !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	if f.messenger.patches != before {
		t.Fatal("denied older intent replayed into later same-number view")
	}
}
func TestTransientMemberErrorRetriesPendingIntentAndSurvivesRestart(t *testing.T) {
	f := setupRuntime(t)
	ctx := context.Background()
	media := &countingPrepareMedia{}
	f.Service.media = media
	r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.HandleAction(ctx, ActionInvocation{Scope: f.identity.Scope, ActorID: "member", EventID: "transient-member", MessageID: r.MessageID, Token: findToken(t, f.messenger.contents[0], "play")}); e != nil {
		t.Fatal(e)
	}
	old := f.claim(t)
	if e = f.Process(ctx, old); e != nil && !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	if e = f.store.CompleteJob(ctx, old); e != nil {
		t.Fatal(e)
	}
	f.member = func(context.Context, domain.Scope, string) (bool, error) {
		return false, errors.New("temporary membership backend")
	}
	j := f.claim(t)
	if e = f.Process(ctx, j); !errors.Is(e, business.ErrProvider) {
		t.Fatalf("transient member error is not retryable %v", e)
	}
	unchanged, e := f.load(ctx, f.db, r.SessionID, false)
	if e != nil {
		t.Fatal(e)
	}
	if unchanged.Version != 1 || unchanged.Request.Scene != "song" || unchanged.Sequence != 1 || media.prepared != 0 {
		t.Fatal("transient membership lookup changed public view")
	}
	f.member = func(context.Context, domain.Scope, string) (bool, error) { return true, nil }
	if e = f.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	if media.prepared != 1 || f.messenger.patches != 1 {
		t.Fatal("pending intent did not recover")
	}
	restarted, e := New(Options{DB: f.db, Schema: f.schema, Store: f.store, Provider: f.provider, Media: media, Messenger: f.messenger, SigningKey: f.key, Member: f.member})
	if e != nil {
		t.Fatal(e)
	}
	if e = restarted.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	if media.prepared != 1 || f.messenger.patches != 1 {
		t.Fatal("admitted intent restart replayed view preparation/effect")
	}
}

func TestFreshPublicActionReplacesFailedOrOlderPendingIntent(t *testing.T) {
	for _, mode := range []string{"live-older", "attempts-exhausted"} {
		t.Run(mode, func(t *testing.T) {
			f := setupRuntime(t)
			ctx := context.Background()
			r, e := f.Send(ctx, f.identity, "source", Request{Scene: "song", Query: "test"})
			if e != nil {
				t.Fatal(e)
			}
			original := f.messenger.contents[0]
			if e = f.HandleAction(ctx, ActionInvocation{Scope: f.identity.Scope, ActorID: "member", EventID: "old-pending", MessageID: r.MessageID, Token: findToken(t, original, "play")}); e != nil {
				t.Fatal(e)
			}
			initial := f.claim(t)
			if e = f.Process(ctx, initial); e != nil && !errors.Is(e, domain.ErrConflict) {
				t.Fatal(e)
			}
			if e = f.store.CompleteJob(ctx, initial); e != nil {
				t.Fatal(e)
			}
			old := f.claim(t)
			f.member = func(context.Context, domain.Scope, string) (bool, error) {
				return false, errors.New("temporary membership failure")
			}
			for attempt := 0; attempt < 8; attempt++ {
				if e = f.Process(ctx, old); !errors.Is(e, business.ErrProvider) {
					t.Fatal(e)
				}
			}
			if mode == "attempts-exhausted" {
				if _, e = f.db.Exec(`UPDATE "`+f.schema+`".jobs SET attempt=8,max_attempts=8 WHERE id=$1`, old.ID); e != nil {
					t.Fatal(e)
				}
				if e = f.store.RetryJob(ctx, old, business.ErrProvider); e != nil {
					t.Fatal(e)
				}
				failed, e := f.store.Job(ctx, old.ID)
				if e != nil || failed.Status != domain.JobFailed {
					t.Fatal("fixture did not exhaust failed pending job", e)
				}
			}
			owner := ActionInvocation{Scope: f.identity.Scope, ActorID: f.identity.ActorID, EventID: "fresh-owner-refresh", MessageID: r.MessageID, Token: findToken(t, original, "refresh")}
			if e = f.HandleAction(ctx, owner); e != nil {
				t.Fatalf("original public card cannot replace stuck pending intent: %v", e)
			}
			f.member = func(context.Context, domain.Scope, string) (bool, error) { return false, nil }
			if mode == "live-older" {
				if e = f.Process(ctx, old); !errors.Is(e, domain.ErrConflict) {
					t.Fatalf("older pending event was not rejected %v", e)
				}
			}
			pending, e := f.load(ctx, f.db, r.SessionID, false)
			if e != nil {
				t.Fatal(e)
			}
			if pending.Pending == nil || pending.Pending.Event != owner.EventID || pending.Version != 1 || pending.IntentVersion != 2 {
				t.Fatal("old pending job modified replacement intent")
			}
			f.member = func(context.Context, domain.Scope, string) (bool, error) { return true, nil }
			next := f.claim(t)
			if e = f.Process(ctx, next); e != nil {
				t.Fatal(e)
			}
			current, e := f.load(ctx, f.db, r.SessionID, false)
			if e != nil {
				t.Fatal(e)
			}
			if current.Version != 2 || current.Pending != nil || f.messenger.patches != 1 {
				t.Fatal("fresh owner action did not restore usable card")
			}
		})
	}
}

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/engine"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/lark"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musiccards"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// The native model is scripted; effect admission, tool execution, presentation
// persistence, media publication and SDK calls all use production components.
type musicParityModel struct{ observations []string }

func (m *musicParityModel) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	for _, msg := range r.Messages {
		if msg.Role == "tool" {
			observation, ok := msg.Content.(string)
			if !ok {
				return modelapi.ChatResponse{}, errors.New("music observation must be text")
			}
			m.observations = append(m.observations, observation)
			return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: `{"choice":"observe","summary":"","messages":[]}`}}, nil
		}
	}
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", ToolCalls: []modelapi.ToolCall{{ID: "music-search", Type: "function", Function: modelapi.ToolFunction{Name: "music_send_search_card", Arguments: `{"type":"song","keywords":"parity fixture","page":1}`}}}}}, nil
}

type musicParityTransport struct{ origin string }

func (g musicParityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme+"://"+r.URL.Host != g.origin {
		return nil, errors.New("music parity fixture rejected a nonfixture origin")
	}
	return http.DefaultTransport.RoundTrip(r)
}

// Secrets are read directly, never printed, sourced by a shell, or installed in
// process-global environment variables. Normal CI can supply the same variables.
func musicParityEnvironment(t *testing.T) map[string]string {
	t.Helper()
	values := map[string]string{}
	path := os.Getenv("AGENT_TEST_FIXTURE_ENV")
	if path == "" {
		path = "/tmp/betago-music-parity-tests/tests.env"
	}
	if f, err := os.Open(path); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
			if ok {
				values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
			}
		}
		if scanner.Err() != nil {
			t.Fatal("cannot read isolated music fixture environment")
		}
	}
	for _, key := range []string{"AGENT_TEST_POSTGRES_DSN", "AGENT_TEST_MINIO_ENDPOINT", "AGENT_TEST_MINIO_ACCESS_KEY", "AGENT_TEST_MINIO_SECRET_KEY"} {
		if value := os.Getenv(key); value != "" {
			values[key] = value
		}
		if values[key] == "" {
			t.Skip("isolated PostgreSQL and MinIO fixtures required")
		}
	}
	return values
}

type musicParityRemote struct {
	t       *testing.T
	mu      sync.Mutex
	origin  string
	counts  map[string]int
	sends   []musicParitySend
	patches []musicParityPatch
	png     []byte
}

type musicParitySend struct{ Parent, Content, UUID, MessageID string }
type musicParityPatch struct {
	Content, UUID string
	Sequence      int
}

func (f *musicParityRemote) snapshot() (map[string]int, []musicParitySend, []musicParityPatch) {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts := map[string]int{}
	for k, n := range f.counts {
		counts[k] = n
	}
	return counts, append([]musicParitySend(nil), f.sends...), append([]musicParityPatch(nil), f.patches...)
}

func musicParityWAV() []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(16036))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	for _, n := range []uint16{1, 1} {
		binary.Write(&b, binary.LittleEndian, n)
	}
	for _, n := range []uint32{8000, 16000} {
		binary.Write(&b, binary.LittleEndian, n)
	}
	for _, n := range []uint16{2, 16} {
		binary.Write(&b, binary.LittleEndian, n)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(16000))
	b.Write(make([]byte, 16000))
	return b.Bytes()
}

func (f *musicParityRemote) song(id int) map[string]any {
	return map[string]any{"id": id, "name": fmt.Sprintf("chosen %d", id), "dt": 1000, "ar": []any{map[string]any{"name": "fixture artist"}}, "al": map[string]any{"id": 8, "name": "fixture album", "picUrl": fmt.Sprintf("%s/origin/cover-%d.png", f.origin, id)}}
}

func (f *musicParityRemote) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.counts[r.Method+" "+r.URL.Path]++
	f.mu.Unlock()
	write := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
	ok := func(data any) { write(map[string]any{"code": 0, "data": data}) }
	if strings.HasPrefix(r.URL.Path, "/origin/") {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			f.t.Error("provider credentials reached media origin")
		}
		if strings.HasSuffix(r.URL.Path, ".png") {
			w.Header().Set("Content-Type", "image/png")
			w.Write(f.png)
		} else {
			w.Header().Set("Content-Type", "audio/wav")
			w.Write(musicParityWAV())
		}
		return
	}
	switch r.URL.Path {
	case "/cloudsearch":
		r.ParseForm()
		if r.Form.Get("keywords") != "parity fixture" || r.Form.Get("limit") != "5" || r.Form.Get("type") != "1" {
			f.t.Error("search lost its server-bound page/query")
		}
		offset, _ := strconv.Atoi(r.Form.Get("offset"))
		var songs []any
		for id := 41 + offset; id <= min(45+offset, 46); id++ {
			songs = append(songs, f.song(id))
		}
		write(map[string]any{"code": 200, "result": map[string]any{"songCount": 6, "songs": songs}})
	case "/song/detail":
		r.ParseForm()
		if r.Form.Get("ids") != "42" {
			f.t.Error("detail changed the exact clicked song ID")
		}
		write(map[string]any{"code": 200, "songs": []any{f.song(999), f.song(42)}})
	case "/lyric":
		write(map[string]any{"code": 200, "lrc": map[string]any{"lyric": "[00:00.00]full first line\n[00:00.50]full final line"}})
	case "/comment/new/":
		write(map[string]any{"code": 200, "data": map[string]any{"comments": []any{map[string]any{"content": "fixture hot comment", "time": int64(1793059200000), "user": map[string]any{"nickname": "fixture commenter"}}}}})
	case "/song/url/v1":
		r.ParseForm()
		if r.Form.Get("id") != "42" || r.Form.Get("unblock") != "true" {
			f.t.Error("audio lookup lost selected song or unblock policy")
		}
		write(map[string]any{"code": 200, "data": []any{map[string]any{"id": 42, "url": f.origin + "/origin/track.wav"}}})
	case "/open-apis/auth/v3/tenant_access_token/internal":
		write(map[string]any{"code": 0, "tenant_access_token": "synthetic-music-token", "expire": 7200})
	case "/open-apis/im/v1/images":
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			f.t.Error("image multipart could not be parsed")
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, _, err := r.FormFile("image")
		if err != nil {
			f.t.Error("image multipart has no file")
			w.WriteHeader(400)
			return
		}
		defer file.Close()
		if _, err := png.Decode(file); err != nil {
			f.t.Error("uploaded cover was not a valid PNG")
		}
		f.mu.Lock()
		n := f.counts[r.Method+" "+r.URL.Path]
		f.mu.Unlock()
		ok(map[string]any{"image_key": fmt.Sprintf("img-uploaded-%d", n)})
	case "/open-apis/im/v1/files":
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			f.t.Error("audio multipart could not be parsed")
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, _, err := r.FormFile("file")
		if err != nil {
			f.t.Error("native audio multipart has no file")
			w.WriteHeader(400)
			return
		}
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil || !bytes.Contains(data, []byte("OpusHead")) || r.FormValue("duration") != "1000" || r.FormValue("file_type") != "opus" {
			f.t.Error("actual ffmpeg conversion or native upload duration/type is missing")
		}
		ok(map[string]any{"file_key": "file-uploaded-native-42"})
	case "/open-apis/cardkit/v1/cards/id_convert":
		var body struct {
			MessageID string `json:"message_id"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.MessageID != "music-card" {
			f.t.Error("CardKit conversion changed the pinned message")
		}
		ok(map[string]any{"card_id": "music-entity"})
	case "/open-apis/cardkit/v1/cards/music-entity":
		var body struct {
			Card     struct{ Type, Data string }
			UUID     string
			Sequence int
		}
		if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Card.Type != "card_json" || body.UUID == "" || body.Sequence < 2 || !json.Valid([]byte(body.Card.Data)) {
			f.t.Error("CardKit patch lost a real frozen body, UUID or sequence")
		}
		f.mu.Lock()
		f.patches = append(f.patches, musicParityPatch{body.Card.Data, body.UUID, body.Sequence})
		f.mu.Unlock()
		ok(map[string]any{})
	case "/open-apis/im/v1/chats/group/members":
		ok(map[string]any{"items": []any{map[string]any{"member_id": "alice", "member_id_type": "open_id", "name": "Alice"}}, "has_more": false, "member_total": 1})
	case "/open-apis/im/v1/messages/request/reply", "/open-apis/im/v1/messages/music-card/reply":
		var body struct {
			Content string `json:"content"`
			UUID    string `json:"uuid"`
			MsgType string `json:"msg_type"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.UUID == "" || body.MsgType != "interactive" {
			f.t.Error("music send was not an actual interactive SDK reply")
		}
		id := "music-card"
		parent := "request"
		if strings.Contains(r.URL.Path, "/music-card/") {
			id, parent = "music-lyrics", "music-card"
		}
		f.mu.Lock()
		f.sends = append(f.sends, musicParitySend{parent, body.Content, body.UUID, id})
		f.mu.Unlock()
		ok(map[string]any{"message_id": id})
	case "/open-apis/im/v1/messages/music-card":
		if r.Method == http.MethodDelete {
			ok(map[string]any{})
			return
		}
		ok(map[string]any{"items": []any{map[string]any{"message_id": "music-card", "chat_id": "group", "sender": map[string]any{"id": "music-parity-app", "id_type": "app_id", "sender_type": "app", "open_bot_id": "bot", "tenant_key": "tenant"}}}})
	default:
		f.t.Errorf("unexpected music fixture operation %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}
}

func musicParityNodes(v any, match func(map[string]any) bool) []map[string]any {
	var out []map[string]any
	switch node := v.(type) {
	case map[string]any:
		if match(node) {
			out = append(out, node)
		}
		for _, child := range node {
			out = append(out, musicParityNodes(child, match)...)
		}
	case []any:
		for _, child := range node {
			out = append(out, musicParityNodes(child, match)...)
		}
	}
	return out
}

func musicParityCard(t *testing.T, raw string) map[string]any {
	t.Helper()
	var card map[string]any
	if len(raw) > 28<<10 || json.Unmarshal([]byte(raw), &card) != nil || card["schema"] != "2.0" {
		t.Fatal("music card must contain a bounded schema2 object")
	}
	cfg, ok := card["config"].(map[string]any)
	if !ok || cfg["update_multi"] != true {
		t.Fatal("music card cannot update the same message")
	}
	for _, img := range musicParityNodes(card, func(n map[string]any) bool { return n["tag"] == "img" }) {
		key, _ := img["img_key"].(string)
		if !strings.HasPrefix(key, "img-uploaded-") {
			t.Fatal("card cover lacks an actual uploaded image handle")
		}
	}
	return card
}

func musicParityButton(t *testing.T, card any, label string) map[string]any {
	t.Helper()
	for _, button := range musicParityNodes(card, func(n map[string]any) bool { return n["tag"] == "button" }) {
		text, _ := button["text"].(map[string]any)
		if text["content"] == label {
			return button
		}
	}
	t.Fatalf("music card lacks %q control", label)
	return nil
}

// Decode only to select a token already present in the rendered card. The test
// never signs or alters a claim, and production verifies its signature/scope.
func musicParityToken(t *testing.T, card any, action string, id int64, page int) string {
	t.Helper()
	for _, n := range musicParityNodes(card, func(n map[string]any) bool { return n["kind"] == "music" }) {
		token, _ := n["token"].(string)
		parts := strings.Split(token, ".")
		if len(parts) != 2 {
			continue
		}
		data, err := base64.RawURLEncoding.DecodeString(parts[0])
		var claim struct {
			Action string
			ID     int64
			Page   int
		}
		if err == nil && json.Unmarshal(data, &claim) == nil && claim.Action == action && (id == 0 || claim.ID == id) && (page == 0 || claim.Page == page) {
			return token
		}
	}
	t.Fatalf("rendered card lacks requested %s action", action)
	return ""
}

func TestAssembledMusicSearchClickPublishesAssetsAndPatchesNativeAudio(t *testing.T) {
	env := musicParityEnvironment(t)
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("real ffmpeg required by assembled music test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var nonce [10]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	owned := hex.EncodeToString(nonce[:])
	schema, bucket := "music_parity_"+owned, "music-parity-"+owned
	db, err := sql.Open("postgres", env["AGENT_TEST_POSTGRES_DSN"])
	if err != nil {
		t.Fatal("cannot open isolated music database")
	}
	db.SetMaxOpenConns(16)
	t.Cleanup(func() {
		if _, err := db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`); err != nil {
			t.Error("cannot remove owned music schema")
		}
		db.Close()
	})
	store, err := pgstore.New(db, schema)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal("cannot migrate isolated music database")
	}
	objects, err := minio.New(env["AGENT_TEST_MINIO_ENDPOINT"], &minio.Options{Creds: credentials.NewStaticV4(env["AGENT_TEST_MINIO_ACCESS_KEY"], env["AGENT_TEST_MINIO_SECRET_KEY"], "")})
	if err != nil {
		t.Fatal("cannot configure isolated music object store")
	}
	exists, err := objects.BucketExists(ctx, bucket)
	if err != nil || exists {
		t.Fatal("music fixture bucket is unavailable or already owned")
	}
	if err = objects.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatal("cannot create owned music bucket")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for object := range objects.ListObjects(cleanup, bucket, minio.ListObjectsOptions{Recursive: true}) {
			if object.Err != nil {
				t.Error("cannot list owned music objects")
				break
			}
			if err := objects.RemoveObject(cleanup, bucket, object.Key, minio.RemoveObjectOptions{}); err != nil {
				t.Error("cannot remove owned music object")
			}
		}
		if err := objects.RemoveBucket(cleanup, bucket); err != nil {
			t.Error("cannot remove owned music bucket")
		}
	})
	var cover bytes.Buffer
	pic := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	pic.Set(0, 0, color.NRGBA{R: 200, G: 80, B: 10, A: 255})
	if err = png.Encode(&cover, pic); err != nil {
		t.Fatal(err)
	}
	remote := &musicParityRemote{t: t, counts: map[string]int{}, png: cover.Bytes()}
	server := httptest.NewServer(remote)
	defer server.Close()
	remote.origin = server.URL
	httpClient := &http.Client{Transport: musicParityTransport{server.URL}, Timeout: 10 * time.Second}
	p := &components{db: db, store: store}
	publicMux := http.NewServeMux()
	public := httptest.NewServer(publicMux)
	defer public.Close()
	c := config.Config{App: config.App{TenantID: "tenant", Visibility: "group", AllowedChatIDs: []string{"group"}, PublicURL: public.URL, APIKey: strings.Repeat("fixture-signing-key-", 3)}, Database: config.Database{Schema: schema}, Lark: config.Lark{AppID: "music-parity-app", AppSecret: "fixture-app-secret", BotOpenID: "bot", VerificationToken: "fixture-verification", Transport: "callback"}, Objects: config.Objects{Endpoint: env["AGENT_TEST_MINIO_ENDPOINT"], AccessKey: env["AGENT_TEST_MINIO_ACCESS_KEY"], SecretKey: env["AGENT_TEST_MINIO_SECRET_KEY"], Bucket: bucket}, Capabilities: config.Capabilities{MusicURL: server.URL, MusicPlayerURL: "https://player.example.test/player?theme=dark", MusicCookie: "private-provider-cookie-canary", AudioEnabled: true, FFmpegPath: ffmpeg, AudioMediaHosts: []string{"127.0.0.1"}, TimeoutSeconds: 10}}
	c.Capabilities.MusicImageHosts = []string{"127.0.0.1"}
	c.Models.Answer = config.Endpoint{BaseURL: server.URL, APIKey: "fixture-model", Model: "scripted-unused", Protocol: "openai", TimeoutSeconds: 5}
	c.Runtime = config.Runtime{MaxConcurrentTasks: 2, MaxTurns: 3, MaxContextBytes: 131072, TurnLeaseSeconds: 30, TaskTimeoutSeconds: 60}
	c.Worker.PollMilliseconds = 10
	p.lark, err = lark.NewClient(lark.ClientConfig{AppID: c.Lark.AppID, AppSecret: c.Lark.AppSecret, BotOpenID: c.Lark.BotOpenID, TenantID: c.App.TenantID, AllowedChatIDs: c.App.AllowedChatIDs, BaseURL: server.URL, HTTPClient: httpClient, LocalMessages: store, ValidateTarget: func(ctx context.Context, s domain.Scope, id string) error { return musicTargetValidator(p)(ctx, s, id) }, ValidateMusicCard: func(ctx context.Context, s domain.Scope, id string) error {
		if p.musicService == nil {
			return domain.ErrUnauthorized
		}
		return p.musicService.CheckMessage(ctx, s, id)
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Explicitly injected uploader prevents any default real Feishu upload.
	p.musicAudio, err = business.NewLarkAudioUploader(business.LarkAudioConfig{AppID: c.Lark.AppID, AppSecret: c.Lark.AppSecret, TenantID: c.App.TenantID, BaseURL: server.URL, HTTPClient: httpClient, FFmpegPath: ffmpeg, AllowedMediaHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = initializeMusic(ctx, c, p); err != nil {
		t.Fatal("music factory initialization failed")
	}
	if p.musicCards == nil || p.musicService == nil || p.musicMedia == nil {
		t.Fatal("music factory did not assemble production adapters")
	}
	defs, err := capabilityDefinitions(c, p)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := atoms.New(atoms.Options{Extensions: defs, Tasks: store, Governance: store})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"music_send_card", "music_send_search_card"} {
		found := false
		for _, d := range registry.Definitions() {
			if d.Name == name {
				found = true
				if !d.SideEffect || !d.AutoExecute || !d.PerActorTurn {
					t.Fatal("rich music send lost ordinary effect classification")
				}
			}
		}
		if !found {
			t.Fatalf("production registry lacks %s", name)
		}
	}
	model := &musicParityModel{}
	native, err := engine.New(engine.Options{Store: store, Registry: registry, Model: model, ConversationOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	assembled, err := assembleAgent(c, p)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the command's actual presentation dispatch, callback registration
	// and public asset mount, while the initial turn uses the native scripted model.
	runtime := assembled.service
	publicMux.Handle("/", assembled.handler)
	scope := domain.Scope{TenantID: "tenant", AppID: c.Lark.AppID, ChatID: "group", Visibility: "group"}
	now := time.Now().UTC().Add(-time.Second)
	meta, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user", Addressed: true, MessageType: "text"})
	m := domain.Message{Source: domain.SourceRef{Scope: scope, MessageID: "request", AuthorID: "alice", Version: 1, OccurredAt: now}, EventID: "request-event", Text: "please show music results", UpdatedAt: now, Raw: meta}
	if _, err = store.Accept(ctx, domain.Event{ID: m.EventID, Type: domain.EventMessage, Message: m, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(ctx, m, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j, err := store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "music-native", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = native.Execute(ctx, j, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision}); err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	counts, sends, patches := remote.snapshot()
	if len(sends) != 1 || len(patches) != 0 || counts["POST /cloudsearch"] != 1 || len(model.observations) != 1 {
		t.Fatal("native tool did not reserve/send one metadata-first card")
	}
	var receipt musiccards.Receipt
	if json.Unmarshal([]byte(model.observations[0]), &receipt) != nil || receipt.MessageID != "music-card" || receipt.State != "active" || receipt.SessionID == "" {
		t.Fatal("native model did not receive the actual music receipt")
	}
	initial := musicParityCard(t, sends[0].Content)
	rows := musicParityNodes(initial, func(n map[string]any) bool {
		id, _ := n["element_id"].(string)
		return strings.HasPrefix(id, "music_row_")
	})
	if len(rows) != 5 || !strings.Contains(sends[0].Content, "共 6 项") || !strings.Contains(sends[0].Content, "fixture artist") {
		t.Fatal("initial rich list lost rows, artist or pagination")
	}
	if musicParityButton(t, initial, "上一页")["disabled"] != true || musicParityButton(t, initial, "下一页")["disabled"] == true {
		t.Fatal("first-page navigation boundaries are incorrect")
	}
	var sessionState, actualMessage, frameBody, frameUUID string
	if err = db.QueryRowContext(ctx, `SELECT state,message_id FROM "`+schema+`".music_card_sessions WHERE id=$1`, receipt.SessionID).Scan(&sessionState, &actualMessage); err != nil || sessionState != "active" || actualMessage != "music-card" {
		t.Fatal("initial session receipt was not pinned")
	}
	if err = db.QueryRowContext(ctx, `SELECT content,uuid FROM "`+schema+`".music_card_frames WHERE session_id=$1 AND sequence=1 AND state='sent'`, receipt.SessionID).Scan(&frameBody, &frameUUID); err != nil || frameBody != sends[0].Content || frameUUID != sends[0].UUID {
		t.Fatal("initial frozen receipt does not match SDK bytes")
	}
	var effects, approvals int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM "`+schema+`".tool_executions WHERE status='completed'`).Scan(&effects); err != nil || effects != 1 {
		t.Fatal("native music send bypassed effect ledger")
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM "`+schema+`".approvals`).Scan(&approvals); err != nil || approvals != 0 {
		t.Fatal("ordinary music send incorrectly required approval")
	}
	drain := func() {
		t.Helper()
		for range 12 {
			err := runtime.Step(ctx, domain.JobPresentation)
			if errors.Is(err, domain.ErrNotFound) {
				return
			}
			if err != nil {
				t.Fatalf("presentation queue failed: %v", err)
			}
		}
		t.Fatal("music presentation queue did not drain")
	}
	latest := func() (string, map[string]any) {
		t.Helper()
		_, _, patches := remote.snapshot()
		if len(patches) == 0 {
			t.Fatal("expected a same-message CardKit patch")
		}
		body := patches[len(patches)-1].Content
		return body, musicParityCard(t, body)
	}
	drain()
	body, card := latest()
	if len(musicParityNodes(card, func(n map[string]any) bool { return n["tag"] == "img" })) != 5 || !strings.Contains(body, "fixture hot comment") || !strings.Contains(body, "fixture commenter") || !strings.Contains(body, "2026-10-27") {
		t.Fatal("real enrichment lost uploaded cover/comment/date metadata")
	}
	ingress := assembled.ingress
	click := func(token, event string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"schema": "2.0", "header": map[string]any{"event_type": "card.action.trigger", "event_id": event, "app_id": c.Lark.AppID, "tenant_key": "tenant", "create_time": strconv.FormatInt(time.Now().UnixMicro(), 10), "token": c.Lark.VerificationToken}, "event": map[string]any{"host": "im_message", "operator": map[string]any{"open_id": "alice", "tenant_key": "tenant"}, "context": map[string]any{"open_chat_id": "group", "open_message_id": "music-card"}, "action": map[string]any{"tag": "button", "value": map[string]any{"kind": "music", "token": token, "actor_id": "mallory", "chat_id": "forged"}}}})
		before, beforeSends, beforePatches := remote.snapshot()
		response, err := ingress.Dispatcher().Do(ctx, payload)
		result, ok := response.(*callback.CardActionTriggerResponse)
		if err != nil || !ok || result.Toast == nil || result.Toast.Type != "info" || result.Card != nil {
			t.Fatal("verified music callback was not acknowledged as persisted queue work")
		}
		var ordinaryEvents int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM "`+schema+`".events`).Scan(&ordinaryEvents); err != nil || ordinaryEvents != 1 {
			t.Fatal("music callback entered ordinary message/task ingress")
		}
		after, afterSends, afterPatches := remote.snapshot()
		if len(beforeSends) != len(afterSends) || len(beforePatches) != len(afterPatches) || fmt.Sprint(before) != fmt.Sprint(after) {
			t.Fatal("callback performed remote work before queue processing")
		}
	}
	next := musicParityToken(t, card, "page", 0, 2)
	click(next, "page-next")
	click(next, "page-next")
	var events int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM "`+schema+`".music_card_events`).Scan(&events); err != nil || events != 1 {
		t.Fatal("duplicate SDK click was not deduplicated")
	}
	drain()
	body, card = latest()
	if !strings.Contains(body, "chosen 46") || !strings.Contains(body, "第 2 / 2 页") {
		t.Fatal("next-page callback did not update the original card")
	}
	rows = musicParityNodes(card, func(n map[string]any) bool {
		id, _ := n["element_id"].(string)
		return strings.HasPrefix(id, "music_row_")
	})
	if len(rows) != 1 || musicParityButton(t, card, "下一页")["disabled"] != true || musicParityButton(t, card, "上一页")["disabled"] == true {
		t.Fatal("last-page row count or navigation boundaries are incorrect")
	}
	click(musicParityToken(t, card, "page", 0, 1), "page-prev")
	drain()
	_, card = latest()
	click(musicParityToken(t, card, "play", 42, 0), "play-selected-42")
	drain()
	body, card = latest()
	audio := musicParityNodes(card, func(n map[string]any) bool { return n["tag"] == "audio" })
	if len(audio) != 1 || audio[0]["file_key"] != "file-uploaded-native-42" || audio[0]["audio_id"] != "42" || audio[0]["show_progress_bar"] != true || audio[0]["show_time"] != true || !strings.Contains(body, "chosen 42") || !strings.Contains(body, "full final line") {
		t.Fatal("same-card selected detail/native audio contract is incomplete")
	}
	counts, sends, patches = remote.snapshot()
	if counts["POST /cloudsearch"] != 3 || counts["POST /song/detail"] != 1 || counts["POST /song/url/v1"] != 1 || counts["POST /open-apis/im/v1/files"] != 1 || len(sends) != 1 || counts["POST /open-apis/cardkit/v1/cards/id_convert"] != 1 || counts["GET /open-apis/im/v1/chats/group/members"] == 0 {
		t.Fatal("selected ID repeated keyword search, extra send, or skipped real native/member adapters")
	}
	var playerURL string
	for _, behavior := range musicParityNodes(card, func(n map[string]any) bool { return n["type"] == "open_url" }) {
		if raw, _ := behavior["default_url"].(string); strings.HasPrefix(raw, "https://player.example.test/") {
			playerURL = raw
		}
	}
	player, err := url.Parse(playerURL)
	if err != nil || playerURL == "" || player.Query().Get("theme") != "dark" || !strings.HasPrefix(player.Query().Get("target"), public.URL+"/music/assets/") {
		t.Fatal("detail lost external player and signed manifest target")
	}
	resp, err := http.Get(player.Query().Get("target"))
	if err != nil {
		t.Fatal("signed manifest GET failed")
	}
	var manifest struct {
		Title, LyricsURL, MusicURL, PictureURL, Album string
		Duration                                      int
	}
	decodeErr := json.NewDecoder(resp.Body).Decode(&manifest)
	resp.Body.Close()
	if resp.StatusCode != 200 || decodeErr != nil || manifest.Title != "chosen 42" || manifest.Album != "fixture album" || manifest.Duration != 1000 {
		t.Fatal("published player manifest lost selected metadata")
	}
	for _, raw := range []string{manifest.LyricsURL, manifest.MusicURL, manifest.PictureURL} {
		if !strings.HasPrefix(raw, public.URL+"/music/assets/") {
			t.Fatal("manifest exposed unpublished provider media")
		}
		r, err := http.Get(raw)
		if err != nil {
			t.Fatal("published manifest asset GET failed")
		}
		data, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil || r.StatusCode != 200 || len(data) == 0 || bytes.Contains(data, []byte(c.Capabilities.MusicCookie)) || bytes.Contains(data, []byte(c.Lark.AppSecret)) {
			t.Fatal("published asset is empty, unsigned or leaks credentials")
		}
	}
	resp, err = http.Get(player.Query().Get("target") + "tamper")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatal("tampered published asset signature was accepted")
	}
	click(musicParityToken(t, card, "lyrics", 42, 0), "full-lyrics")
	drain()
	_, sends, _ = remote.snapshot()
	if len(sends) != 2 || sends[1].Parent != "music-card" || !strings.Contains(sends[1].Content, "full first line") || !strings.Contains(sends[1].Content, "full final line") {
		t.Fatal("full lyrics did not produce a persisted scoped reply")
	}
	musicParityCard(t, sends[1].Content)
	click(musicParityToken(t, card, "refresh", 0, 0), "refresh-selected")
	drain()
	body, card = latest()
	counts, _, patches = remote.snapshot()
	if counts["POST /song/url/v1"] < 2 || counts["POST /open-apis/im/v1/files"] != 2 || counts["POST /cloudsearch"] != 3 || !strings.Contains(body, "file-uploaded-native-42") {
		t.Fatalf("explicit refresh failed to renew selected source/audio: source=%d audio_upload=%d searches=%d", counts["POST /song/url/v1"], counts["POST /open-apis/im/v1/files"], counts["POST /cloudsearch"])
	}
	for _, patch := range patches {
		var content, uuid string
		if err = db.QueryRowContext(ctx, `SELECT content,uuid FROM "`+schema+`".music_card_frames WHERE session_id=$1 AND sequence=$2 AND mode='patch' AND state='sent'`, receipt.SessionID, patch.Sequence).Scan(&content, &uuid); err != nil || content != patch.Content || uuid != patch.UUID {
			t.Fatal("actual patch differs from persisted frozen receipt")
		}
	}
	_, confirmed := musicParityButton(t, card, "撤回")["confirm"].(map[string]any)
	if !confirmed {
		t.Fatal("withdrawal lacks the required UI confirmation")
	}
	click(musicParityToken(t, card, "withdraw", 0, 0), "withdraw-owner")
	drain()
	counts, sends, patches = remote.snapshot()
	if counts["DELETE /open-apis/im/v1/messages/music-card"] != 1 || len(sends) != 2 {
		t.Fatal("confirmed owner withdrawal did not use exact original message")
	}
	if err = db.QueryRowContext(ctx, `SELECT state,message_id FROM "`+schema+`".music_card_sessions WHERE id=$1`, receipt.SessionID).Scan(&sessionState, &actualMessage); err != nil || sessionState != "withdrawn" || actualMessage != "music-card" {
		t.Fatal("withdrawal lost the durable original-card receipt")
	}
	t.Logf("assembled parity: search=%d selected_detail=%d source=%d image_upload=%d audio_upload=%d sends=%d frozen_patches=%d", counts["POST /cloudsearch"], counts["POST /song/detail"], counts["POST /song/url/v1"], counts["POST /open-apis/im/v1/images"], counts["POST /open-apis/im/v1/files"], len(sends), len(patches))
}

package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	cfg "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mememilvus"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mememodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memesource"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
	_ "github.com/lib/pq"
)

type liveEncodingDiagnostics struct {
	memes.Completion
	t *testing.T
}

func (m liveEncodingDiagnostics) Complete(ctx context.Context, in memes.CompletionRequest) (memes.CompletionResponse, error) {
	response, err := m.Completion.Complete(ctx, in)
	m.t.Logf("meme stage=%s repair=%t duration_ms=%d input_tokens=%d output_tokens=%d", in.Stage, in.Repair, response.Metadata.DurationMS, response.Metadata.InputTokens, response.Metadata.OutputTokens)
	if err != nil {
		status, code, param := mememodel.SafeTransportDetails(err)
		m.t.Logf("meme provider error: status=%d code=%s param=%s input_tokens=%d output_tokens=%d", status, code, param, response.Metadata.InputTokens, response.Metadata.OutputTokens)
	}
	if err == nil && in.Stage == memes.StageEncode {
		var encoded memes.EncodedWiki
		if decodeErr := memes.DecodeStrict(response.JSON, &encoded); decodeErr != nil {
			m.t.Logf("public-source encoding structure error: %v; output_bytes=%d input_tokens=%d output_tokens=%d", decodeErr, len(response.JSON), response.Metadata.InputTokens, response.Metadata.OutputTokens)
		}
	}
	return response, err
}

// This explicit transport check uses one public Wiki item and the existing Ark
// roles, but writes only a temporary DB schema and isolated search generation.
// It never creates a Lark client or sends messages and is not a humor evaluation.
func TestLiveWikiEncodingPublicationAndReferenceLookup(t *testing.T) {
	if os.Getenv("MEME_LIVE_WIKI") != "1" {
		t.Skip("set MEME_LIVE_WIKI=1 for one-item real Wiki/Ark isolated integration")
	}
	dsn, search := os.Getenv("MEME_TEST_POSTGRES_DSN"), os.Getenv("MEME_TEST_MILVUS_URL")
	if dsn == "" || search == "" {
		t.Fatal("isolated PostgreSQL and Milvus endpoints required")
	}
	conf, err := cfg.LoadFileE(os.Getenv("BETAGO_CONFIG_PATH"))
	if err != nil || conf.ArkConfig == nil {
		t.Fatal("existing private Ark config unavailable")
	}
	ark_dal.Init(conf.ArkConfig)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(random[:])
	schema := "meme_live_" + suffix
	prefix := "meme_live_" + suffix
	generation := "g1"
	options := indexOptionsForImport(conf, search, prefix, conf.ArkConfig.EmbeddingModel, 2048)
	tracked := &liveMilvusTransport{base: http.DefaultTransport, prefix: prefix, collections: map[string]bool{}}
	t.Cleanup(func() {
		defer db.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		db.ExecContext(cleanup, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`)
		tracked.Lock()
		defer tracked.Unlock()
		for name := range tracked.collections {
			raw, _ := json.Marshal(map[string]string{"collectionName": name})
			req, _ := http.NewRequestWithContext(cleanup, http.MethodPost, search+"/v2/vectordb/collections/drop", strings.NewReader(string(raw)))
			req.Header.Set("Content-Type", "application/json")
			if options.Username != "" || options.Password != "" {
				req.Header.Set("Authorization", "Bearer "+options.Username+":"+options.Password)
			}
			if resp, e := http.DefaultClient.Do(req); e == nil {
				resp.Body.Close()
			}
		}
	})
	store, err := memestore.New(db, schema)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	wiki, err := memesource.NewWikiClient(memesource.DefaultWikiBase, nil)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := wiki.Catalog(ctx, memesource.CatalogOptions{PageSize: 1, Limit: 1})
	if err != nil || len(catalog.Sources) != 1 {
		t.Fatalf("public source: count=%d err=%v", len(catalog.Sources), err)
	}
	modelID := conf.ArkConfig.NormalModel
	if override := os.Getenv("MEME_LIVE_MODEL_ID"); override != "" {
		modelID = override
	}
	model, err := mememodel.New(mememodel.Options{ModelID: modelID, MaxOutputTokens: 8192})
	if err != nil {
		t.Fatal(err)
	}
	catalogStore := store.ForScope("wiki")
	options.Scope, options.Embedder = "wiki", &mememodel.Embedder{}
	options.Ledger, options.Records = catalogStore, catalogStore
	options.HTTPClient = &http.Client{Transport: tracked, Timeout: 20 * time.Second}
	index, err := mememilvus.New(options)
	if err != nil {
		t.Fatal(err)
	}
	report, err := memesource.Sync(ctx, catalog, memesource.SyncDependencies{Store: store.ForScope("wiki"), Model: liveEncodingDiagnostics{model, t}, Publisher: index}, memesource.SyncOptions{Generation: generation, ModelID: modelID})
	if err != nil {
		t.Fatalf("source encoding/publication failed: %v; source errors=%+v", err, report.Errors)
	}
	if !report.Active || report.Manifest == nil || report.Manifest.Backend != "milvus" || report.CardCount < 1 {
		t.Fatalf("unverified generation: %+v", report)
	}
	retriever, err := index.ForGeneration(*report.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := memes.NewReferenceService(memes.ReferenceDependencies{Model: model, Retriever: retriever}, memes.ReferenceOptions{RequestTimeout: 60 * time.Second, StageTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	source := catalog.Sources[0]
	result := reference.Retrieve(ctx, memes.Request{RequestID: "public-transport-fixture", Dialogue: []memes.Turn{{TurnID: "one", Speaker: "member", Text: "刚看到有人提到“" + source.Item.Title + "”，这个梗怎么用？"}}, MaxChars: 64})
	if result.Status == memes.ReferenceStatusError {
		t.Fatalf("real query integration failed: reason=%s errors=%+v", result.Reason, result.Errors)
	}
	if len(result.Plan.Queries) == 0 || len(result.Retrieval.Queries) == 0 {
		t.Fatalf("no actual retrieval requests: status=%s", result.Status)
	}
	if len(result.Cards) == 0 || len(result.Cards) > 5 {
		t.Fatalf("bounded one-source lookup returned %d cards (status=%s)", len(result.Cards), result.Status)
	}
	t.Logf("public_sources=%d encoded_cards=%d query_calls=%d queries=%d reference_cards=%d status=%s lark_sends=0", report.SourceCount, report.CardCount, result.Metadata.SemanticCalls, len(result.Plan.Queries), len(result.Cards), result.Status)
}

// Track only collection names created by this random-prefix fixture, including
// failed publications, so cleanup never lists or touches unrelated collections.
type liveMilvusTransport struct {
	sync.Mutex
	base        http.RoundTripper
	prefix      string
	collections map[string]bool
}

func (t *liveMilvusTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if strings.HasSuffix(request.URL.Path, "/collections/create") && request.GetBody != nil {
		body, err := request.GetBody()
		if err == nil {
			var collection struct {
				Name string `json:"collectionName"`
			}
			if json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&collection) == nil && strings.HasPrefix(collection.Name, t.prefix+"_") {
				t.Lock()
				t.collections[collection.Name] = true
				t.Unlock()
			}
			body.Close()
		}
	}
	return t.base.RoundTrip(request)
}

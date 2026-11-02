// meme-sync imports public Wiki sources, encodes retrieval cards offline and
// publishes a verified immutable generation. It does not initialize the bot.
package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mememilvus"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mememodel"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memeruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memesource"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
	_ "github.com/jackc/pgx/v5/stdlib"
	arkmodel "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(parent context.Context, args []string, out, errOut io.Writer) int {
	out = &serializedWriter{Writer: out}
	errOut = &serializedWriter{Writer: errOut}
	flags := flag.NewFlagSet("meme-sync", flag.ContinueOnError)
	flags.SetOutput(out)
	flags.Usage = func() {
		fmt.Fprintln(out, "Usage: meme-sync [flags]\nOffline Wiki catalog import, encoding and verified publication.\nFull imports (--limit 0) stream fetching and encoding and activate only after the whole catalog succeeds. Explicit bounded batches remain standalone generations; reruns reuse immutable encodings.")
		flags.PrintDefaults()
	}
	defaultConfig := os.Getenv("BETAGO_CONFIG_PATH")
	if defaultConfig == "" {
		defaultConfig = ".dev/config.toml"
	}
	configPath := flags.String("config", defaultConfig, "Existing master config.toml (never copied or printed)")
	wikiBase := flags.String("wiki-base", memesource.DefaultWikiBase, "Public Wiki base URL")
	wikiInterval := flags.Duration("wiki-interval", time.Second, "Minimum spacing between public Wiki requests")
	wikiRetries := flags.Int("wiki-retries", 4, "Bounded retries for transient Wiki GET failures (0..8)")
	fetchWorkers := flags.Int("fetch-workers", 4, "Concurrent Wiki detail fetches (1..16), sharing Wiki request spacing and cooldown")
	sourceCache := flags.String("source-cache-dir", "", "Optional public source snapshot cache for resumable imports")
	sourceWorkers := flags.Int("source-workers", 2, "Concurrent source encoders (1..64), also constrained by shared model quota")
	indexEndpoint := flags.String("index-endpoint", "", "Milvus REST URL; defaults to meme_config.search_endpoint or "+memeruntime.DefaultMilvusEndpoint)
	indexInsecure := flags.Bool("index-insecure-tls", false, "Allow the configured Milvus self-signed TLS certificate")
	schema := flags.String("schema", "meme_reply", "PostgreSQL schema owned by meme retrieval")
	prefix := flags.String("prefix", "memes", "Owned Milvus collection prefix")
	scope := flags.String("scope", "wiki", "Shared corpus storage scope")
	dimensions := flags.Int("dimensions", 2048, "Expected embedding vector dimensions")
	modelID := flags.String("model", "", "Offline encoder model; defaults to ark_config.normal_model")
	embeddingModel := flags.String("embedding-model", "", "Embedding model; defaults to ark_config.embedding_model")
	limit := flags.Int("limit", 30, "Maximum unique source attempts in this batch; 0 browses all pages")
	pageSize := flags.Int("page-size", 20, "Wiki catalog page size (1..30)")
	offset := flags.Int("offset", 0, "Starting Wiki catalog offset for this batch")
	generation := flags.String("generation", "", "Immutable generation ID; defaults to a deterministic corpus hash")
	timeout := flags.Duration("timeout", 15*time.Minute, "Total sync deadline; individual model calls are capped at 90s")
	modelInterval := flags.Duration("model-interval", 100*time.Millisecond, "Shared model request start spacing")
	modelConcurrency := flags.Int("model-concurrency", 2, "Shared maximum in-flight model requests")
	dryRun := flags.Bool("dry-run", false, "Fetch sources and report only; no config load, model calls, database or index writes")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *limit < 0 || *offset < 0 || *pageSize < 1 || *pageSize > 30 || *timeout <= 0 || *dimensions < 1 || *dimensions > 16000 || *scope == "" || *modelInterval < 0 || *modelInterval > time.Minute || *modelConcurrency < 1 || *modelConcurrency > 64 || *wikiInterval < 0 || *wikiInterval > time.Minute || *wikiRetries < 0 || *wikiRetries > 8 || *sourceWorkers < 1 || *sourceWorkers > 64 || *fetchWorkers < 1 || *fetchWorkers > 16 {
		fmt.Fprintln(errOut, "invalid sync flags")
		return 2
	}
	cacheDir := *sourceCache
	if *dryRun {
		cacheDir = "" // Dry runs never write even an explicitly configured local cache.
	}
	wiki, err := memesource.NewWikiClientWithOptions(*wikiBase, &http.Client{Timeout: 20 * time.Second}, memesource.WikiClientOptions{
		RequestInterval: *wikiInterval,
		MaxRetries:      *wikiRetries,
		MaxRetryAfter:   30 * time.Second,
		CacheDir:        cacheDir,
		Progress: func(p memesource.CatalogProgress) {
			fmt.Fprintf(out, "progress stage=wiki phase=%s pages=%d sources=%d errors=%d next_offset=%d source=%q\n", p.Phase, p.Pages, p.Sources, p.Errors, p.NextOffset, p.SourceID)
		},
	})
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	fetchOptions := memesource.CatalogOptions{PageSize: *pageSize, Offset: *offset, Limit: *limit, FetchWorkers: *fetchWorkers}
	if *dryRun {
		catalog, fetchErr := wiki.Catalog(ctx, fetchOptions)
		if fetchErr != nil {
			catalog.Errors = append(catalog.Errors, memesource.SourceError{SourceID: "wiki:catalog", Stage: "catalog", Error: fetchErr.Error()})
		}
		fmt.Fprintf(out, "dry_run=true source_count=%d sources_fetched=%d card_count=0 model_calls=0 writes=0 errors=%d catalog_complete=%t has_more=%t next_offset=%d\n", len(catalog.Sources)+len(catalog.Errors), len(catalog.Sources), len(catalog.Errors), catalog.Complete, catalog.HasMore, catalog.NextOffset)
		for _, source := range catalog.Sources {
			fmt.Fprintf(out, "source=%q title=%q revision=%s description_chars=%d\n", source.ID, source.Item.Title, source.Revision, len([]rune(source.Description)))
		}
		printSourceErrors(out, catalog.Errors)
		if len(catalog.Errors) > 0 {
			return 1
		}
		return 0
	}
	if ctx.Err() != nil {
		fmt.Fprintln(errOut, "sync deadline or cancellation reached before initialization")
		return 1
	}
	// Parse errors can contain raw TOML values. Keep credentials out of diagnostics.
	cfg, err := infraConfig.LoadFileE(*configPath)
	if err != nil {
		fmt.Fprintln(errOut, "load config failed")
		return 1
	}
	if cfg.DBConfig == nil || cfg.ArkConfig == nil || cfg.ArkConfig.APIKey == "" {
		fmt.Fprintln(errOut, "config requires db_config and ark_config with an API key")
		return 1
	}
	if *modelID == "" {
		*modelID = cfg.ArkConfig.NormalModel
	}
	if *embeddingModel == "" {
		*embeddingModel = cfg.ArkConfig.EmbeddingModel
	}
	if *modelID == "" || *embeddingModel == "" {
		fmt.Fprintln(errOut, "encoding and embedding model IDs are required")
		return 1
	}
	indexOptions := indexOptionsForImport(cfg, *indexEndpoint, *prefix, *embeddingModel, *dimensions)
	indexOptions.Scope = *scope
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if *indexInsecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	} // Explicit CLI flag for a self-hosted cluster.
	indexOptions.HTTPClient = &http.Client{Transport: transport, Timeout: 20 * time.Second}
	defer transport.CloseIdleConnections()
	// Validate endpoints and index names before opening a database or invoking a model.
	if _, err = mememilvus.New(indexOptions); err != nil {
		fmt.Fprintln(errOut, "invalid Milvus configuration")
		return 1
	}
	sqlDB, err := sql.Open("pgx", cfg.DBConfig.DSN())
	if err != nil {
		fmt.Fprintln(errOut, "database initialization failed")
		return 1
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(2)
	if err = sqlDB.PingContext(ctx); err != nil {
		fmt.Fprintln(errOut, "database connection failed")
		return 1
	}
	store, err := memestore.New(sqlDB, *schema)
	if err != nil {
		fmt.Fprintln(errOut, "invalid meme storage schema")
		return 1
	}
	if err = store.Migrate(ctx); err != nil {
		fmt.Fprintln(errOut, "meme storage migration failed")
		return 1
	}
	gate, err := store.Gate("ark:meme-reference", *modelInterval, *modelConcurrency)
	if err != nil {
		fmt.Fprintln(errOut, "invalid shared model quota")
		return 1
	}
	usageDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), DisableAutomaticPing: true})
	if err != nil {
		fmt.Fprintln(errOut, "usage recorder initialization failed")
		return 1
	}
	llmusage.SetDefaultRecorder(llmusage.NewRecorder(usageDB))
	arkConfig := *cfg.ArkConfig
	arkConfig.EmbeddingModel = *embeddingModel
	ark_dal.Init(&arkConfig)
	usageScope := llmusage.Scope{SourceType: llmusage.SourceTypeBackground, Source: "meme_sync"}
	encoder, err := mememodel.New(mememodel.Options{ModelID: *modelID, Scope: usageScope, Gate: gate, MaxOutputTokens: 8192})
	if err != nil {
		fmt.Fprintln(errOut, "encoding model initialization failed")
		return 1
	}
	indexOptions.Embedder = &mememodel.Embedder{Scope: usageScope, Gate: gate, Concurrency: *modelConcurrency, Call: func(callCtx context.Context, text string, usage llmusage.Scope) ([]float32, arkmodel.Usage, error) {
		bounded, cancelCall := context.WithTimeout(callCtx, 90*time.Second)
		defer cancelCall()
		return ark_dal.EmbeddingTextPrivate(bounded, text, usage)
	}}
	catalogStore := store.ForScope(*scope)
	indexOptions.Ledger, indexOptions.Records = catalogStore, catalogStore
	index, err := mememilvus.New(indexOptions)
	if err != nil {
		fmt.Fprintln(errOut, "meme index initialization failed")
		return 1
	}
	deps := memesource.SyncDependencies{Store: catalogStore, Model: boundedModel{encoder}, Publisher: index}
	syncOptions := memesource.SyncOptions{
		ModelID: *modelID, Generation: *generation, SourceWorkers: *sourceWorkers,
		Progress: func(p memesource.SyncProgress) {
			fmt.Fprintf(out, "progress stage=sync phase=%s completed=%d fetched=%d total=%d catalog_complete=%t encoded=%d reused=%d cards=%d errors=%d source=%q\n", p.Phase, p.Completed, p.Fetched, p.Total, p.CatalogComplete, p.EncodedSources, p.ReusedSources, p.CardCount, p.Errors, p.SourceID)
			if p.Failure != nil {
				printSourceErrors(out, []memesource.SourceError{*p.Failure})
			}
		},
	}
	var report memesource.SyncReport
	var syncErr error
	if *limit == 0 {
		report, syncErr = memesource.SyncFromWiki(ctx, wiki, fetchOptions, deps, syncOptions)
	} else {
		catalog, fetchErr := wiki.Catalog(ctx, fetchOptions)
		if fetchErr != nil {
			catalog.Errors = append(catalog.Errors, memesource.SourceError{SourceID: "wiki:catalog", Stage: "catalog", Error: fetchErr.Error()})
		}
		report, syncErr = memesource.Sync(ctx, catalog, deps, syncOptions)
	}
	printReport(out, report)
	if syncErr != nil {
		fmt.Fprintln(errOut, syncErr)
		return 1
	}
	return 0
}

// Fetch progress and the encoding coordinator run concurrently in the pipeline.
// Serialize writes even when the caller provides a non-thread-safe buffer.
type serializedWriter struct {
	sync.Mutex
	io.Writer
}

func (w *serializedWriter) Write(data []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	return w.Writer.Write(data)
}

type boundedModel struct{ memes.Completion }

func (m boundedModel) Complete(ctx context.Context, in memes.CompletionRequest) (memes.CompletionResponse, error) {
	bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return m.Completion.Complete(bounded, in)
}

func indexOptionsForImport(config *infraConfig.BaseConfig, endpoint, prefix, embeddingModel string, dimensions int) mememilvus.Options {
	options := mememilvus.Options{Endpoint: memeruntime.DefaultMilvusEndpoint, IndexPrefix: prefix, EmbeddingModel: embeddingModel, Dimensions: dimensions}
	if config != nil && config.MemeConfig != nil {
		if config.MemeConfig.SearchEndpoint != "" {
			options.Endpoint = config.MemeConfig.SearchEndpoint
		}
		options.Username, options.Password = config.MemeConfig.SearchUsername, config.MemeConfig.SearchPassword
	}
	if endpoint != "" {
		options.Endpoint = endpoint
	}
	if username := os.Getenv("MEME_INDEX_USERNAME"); username != "" {
		options.Username = username
	}
	if password := os.Getenv("MEME_INDEX_PASSWORD"); password != "" {
		options.Password = password
	}
	return options
}
func printSourceErrors(out io.Writer, sourceErrors []memesource.SourceError) {
	for _, failure := range sourceErrors {
		fmt.Fprintf(out, "source_error source=%q stage=%q error=%q\n", failure.SourceID, failure.Stage, failure.Error)
	}
}
func printReport(out io.Writer, report memesource.SyncReport) {
	var modelLatency int64
	for _, call := range report.Calls {
		modelLatency += call.DurationMS
	}
	fmt.Fprintf(out, "dry_run=false source_count=%d encoded_sources=%d reused_sources=%d card_count=%d errors=%d generation=%q active=%t catalog_complete=%t has_more=%t next_offset=%d model_calls=%d model_latency_ms=%d elapsed_ms=%d\n", report.SourceCount, report.EncodedSources, report.ReusedSources, report.CardCount, len(report.Errors), report.Generation, report.Active, report.CatalogComplete, report.HasMore, report.NextOffset, len(report.Calls), modelLatency, report.ElapsedMS)
	printSourceErrors(out, report.Errors)
	if report.Manifest != nil {
		raw, _ := json.Marshal(report.Manifest)
		fmt.Fprintf(out, "manifest=%s\n", raw)
	}
}

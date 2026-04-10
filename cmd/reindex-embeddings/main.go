package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/tools/reindexembeddings"
)

func main() {
	var (
		configPath  = flag.String("config", ".dev/config.toml", "Path to config.toml")
		index       = flag.String("index", "", "OpenSearch index name, defaults to opensearch_config.lark_msg_index")
		model       = flag.String("model", "", "Ark embedding model override, defaults to ark_config.embedding_model")
		days        = flag.Int("days", 0, "Only process docs from the last N days")
		dryRun      = flag.Bool("dry-run", false, "Show updates without writing")
		batchSize   = flag.Int("batch-size", 50, "Number of docs buffered before flushing")
		concurrency = flag.Int("concurrency", 32, "Concurrent Ark embedding requests")
		scrollSize  = flag.Int("scroll-size", 500, "OpenSearch scroll page size")
		dimensions  = flag.Int("dimensions", 2048, "Embedding vector dimensions")
		timeout     = flag.Duration("timeout", 24*time.Hour, "Per-request timeout for Ark embedding calls")
	)
	flag.Parse()

	cfg, err := infraConfig.LoadFileE(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if cfg.OpensearchConfig == nil || cfg.ArkConfig == nil {
		fmt.Fprintln(os.Stderr, "config missing opensearch_config or ark_config")
		os.Exit(1)
	}

	targetIndex := *index
	if targetIndex == "" {
		targetIndex = cfg.OpensearchConfig.LarkMsgIndex
	}
	targetModel := *model
	if targetModel == "" {
		targetModel = cfg.ArkConfig.EmbeddingModel
	}

	fmt.Printf("Config: %s\n", *configPath)
	fmt.Printf("OpenSearch index: %s\n", targetIndex)
	fmt.Printf("Ark model: %s\n", targetModel)
	fmt.Printf("Dry run: %v\n", *dryRun)
	fmt.Printf("Batch size: %d\n", *batchSize)
	fmt.Printf("Concurrency: %d\n", *concurrency)

	osClient, err := reindexembeddings.CreateOpenSearchClient(cfg.OpensearchConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create opensearch client: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	stats, mapping, err := reindexembeddings.AnalyzeIndex(ctx, osClient, targetIndex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze index: %v\n", err)
		os.Exit(1)
	}
	printAnalyze("Before", stats, mapping)

	arkClient := reindexembeddings.CreateArkClient(cfg.ArkConfig.APIKey, *timeout)
	summary, err := reindexembeddings.Run(ctx, osClient, arkClient, reindexembeddings.RunOptions{
		Index:          targetIndex,
		Model:          targetModel,
		Dimensions:     *dimensions,
		Days:           *days,
		DryRun:         *dryRun,
		BatchSize:      *batchSize,
		Concurrency:    *concurrency,
		ScrollSize:     *scrollSize,
		ScrollTimeout:  5 * time.Minute,
		RequestTimeout: *timeout,
		ExpectedTotal:  stats.MissingMessageV2,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "run reindex: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Run summary:")
	fmt.Printf("  processed: %d\n", summary.Processed)
	fmt.Printf("  updated: %d\n", summary.Updated)
	fmt.Printf("  errors: %d\n", summary.Errors)
	fmt.Printf("  skipped_no_text: %d\n", summary.SkippedNoText)
	fmt.Printf("  skipped_existing: %d\n", summary.SkippedExisting)
	fmt.Printf("  prompt_tokens: %d\n", summary.PromptTokens)
	fmt.Printf("  total_tokens: %d\n", summary.TotalTokens)

	afterStats, afterMapping, err := reindexembeddings.AnalyzeIndex(ctx, osClient, targetIndex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify analyze index: %v\n", err)
		os.Exit(1)
	}
	printAnalyze("After", afterStats, afterMapping)
}

func printAnalyze(stage string, stats reindexembeddings.AnalyzeStats, mapping map[string]any) {
	fmt.Printf("%s stats:\n", stage)
	fmt.Printf("  total_docs: %d\n", stats.TotalDocs)
	fmt.Printf("  with_message: %d\n", stats.WithMessageField)
	fmt.Printf("  with_message_v2: %d\n", stats.WithMessageV2Field)
	fmt.Printf("  missing_message_v2: %d\n", stats.MissingMessageV2)
	if len(mapping) == 0 {
		return
	}
	if props, ok := mapping["properties"].(map[string]any); ok {
		fmt.Printf("  message mapping: %v\n", props["message"])
		fmt.Printf("  message_v2 mapping: %v\n", props["message_v2"])
		fmt.Printf("  raw_message mapping: %v\n", props["raw_message"])
	}
}

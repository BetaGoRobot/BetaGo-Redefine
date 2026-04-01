package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/replay"
	replaytui "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/replay/tui"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) < 2 || strings.TrimSpace(args[0]) != "replay" {
		return replayUsageError()
	}
	cfg, err := infraConfig.LoadFileE(loadConfigPath())
	if err != nil {
		return err
	}
	initializeRuntime(cfg)

	switch strings.TrimSpace(args[1]) {
	case "intent":
		return runReplayIntent(ctx, cfg, args[2:])
	case "tui":
		return runReplayTUI(cfg, args[2:])
	default:
		return replayUsageError()
	}
}

func loadConfigPath() string {
	if path := os.Getenv("BETAGO_CONFIG_PATH"); path != "" {
		return path
	}
	return ".dev/config.toml"
}

func IntentReplayService() replay.IntentReplayService {
	return replay.IntentReplayService{}
}

func runReplayIntent(ctx context.Context, cfg *infraConfig.BaseConfig, args []string) error {
	cliArgs, err := replay.ParseCLIArgs(args)
	if err != nil {
		return err
	}
	if cliArgs.LiveModel {
		ark_dal.Init(cfg.ArkConfig)
	}

	service := IntentReplayService()
	report, err := service.Replay(ctx, cliArgs.ChatID, cliArgs.MessageID, cliArgs.RunOptions())
	if err != nil {
		return err
	}
	output, err := replay.RenderCLIOutput(report, cliArgs.JSON)
	if err != nil {
		return err
	}
	if path := strings.TrimSpace(cliArgs.OutputPath); path != "" {
		if err := os.WriteFile(path, output, 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote replay report to %s\n", path)
		return nil
	}
	fmt.Println(string(output))
	return nil
}

func runReplayTUI(cfg *infraConfig.BaseConfig, args []string) error {
	cliArgs, err := replay.ParseReplayTUIArgs(args)
	if err != nil {
		return err
	}
	if cliArgs.LiveModel {
		ark_dal.Init(cfg.ArkConfig)
	}

	program := tea.NewProgram(replaytui.NewModelWithConfig(
		replaytui.Config{
			Days:      cliArgs.Days,
			Limit:     cliArgs.Limit,
			LiveModel: cliArgs.LiveModel,
			OutputDir: cliArgs.OutputDir,
		},
		buildReplayTUIServices(cliArgs),
	))
	_, err = program.Run()
	return err
}

func initializeRuntime(cfg *infraConfig.BaseConfig) {
	logs.Init()
	if cfg != nil && cfg.OtelConfig != nil {
		otel.Init(cfg.OtelConfig)
	}
	lark_dal.Init()
	opensearch.Init(cfg.OpensearchConfig)
}

func replayUsageError() error {
	return fmt.Errorf("usage: go run ./cmd/betago replay <intent|tui> [flags]\nintent: --chat-id <chat_id> --message-id <message_id> [--json] [--output <path>] [--live-model] [--history-limit N] [--profile-limit N] [--disable-history] [--disable-profile]\ntui: [--days N] [--limit N] [--live-model] [--output-dir <path>]")
}

func buildReplayTUIServices(cliArgs replay.ReplayTUIArgs) replaytui.Services {
	return replaytui.Services{
		LoadCatalog: func(days, limit int) ([]replay.ChatCandidate, error) {
			return replay.ChatCatalogService{}.LoadCatalog(context.Background(), replay.ChatCatalogQuery{
				Days:  days,
				Limit: limit,
			})
		},
		SelectSamples: func(options replay.SampleFilterOptions) ([]replay.ReplaySample, error) {
			return replay.SampleSelectorService{}.Select(context.Background(), options)
		},
		RunBatch: func(req replay.ReplayBatchRequest) (replay.ReplayBatchResult, error) {
			result, err := replay.ReplayBatchRunner{}.Run(context.Background(), req)
			if err != nil {
				return result, err
			}
			return replay.ReplayBatchReportWriter{}.Write(firstNonEmpty(cliArgs.OutputDir, "."), result)
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

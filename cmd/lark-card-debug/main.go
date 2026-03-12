package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/carddebug"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/bytedance/sonic"
)

type options struct {
	ListSpecs     bool
	ListTemplates bool

	Spec     string
	Template string
	VarsJSON string
	CardJSON string
	CardFile string

	ToChatID     string
	ToOpenID     string
	ChatID       string
	ID           string
	ActorOpenID  string
	TargetOpenID string
	Scope        string

	DryRun       bool
	PrintPayload bool
}

type bootstrapPlan struct {
	NeedLark            bool
	NeedDB              bool
	NeedRedis           bool
	NeedFeatureRegistry bool
}

type cleanupStack struct {
	closers []func(context.Context) error
}

func (s *cleanupStack) Add(fn func(context.Context) error) {
	if fn == nil {
		return
	}
	s.closers = append(s.closers, fn)
}

func (s *cleanupStack) Close(ctx context.Context) error {
	var errs []error
	for i := len(s.closers) - 1; i >= 0; i-- {
		if err := s.closers[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	opts, err := parseFlags(args)
	if err != nil {
		return err
	}

	if opts.ListSpecs {
		printSpecs()
		return nil
	}
	if opts.ListTemplates {
		printTemplates()
		return nil
	}

	plan := buildPlan(opts)
	var cfg *infraConfig.BaseConfig
	if needConfig(plan, opts) {
		var err error
		cfg, err = loadConfig()
		if err != nil {
			return err
		}
	}
	cleanup, err := bootstrap(ctx, cfg, plan)
	if err != nil {
		return err
	}
	defer cleanup.Close(ctx)

	built, err := buildCard(ctx, opts, cfg)
	if err != nil {
		return err
	}

	if opts.PrintPayload || opts.DryRun {
		if err := printPayload(built); err != nil {
			return err
		}
	}
	if opts.DryRun {
		return nil
	}

	target, err := carddebug.ResolveReceiveTarget(opts.ToChatID, opts.ToOpenID, "")
	if err != nil {
		return err
	}
	if err := carddebug.Send(ctx, target, built); err != nil {
		return err
	}

	fmt.Printf("sent %s to %s\n", built.Label, target.String())
	return nil
}

func parseFlags(args []string) (options, error) {
	fs := flag.NewFlagSet("lark-card-debug", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var opts options
	fs.BoolVar(&opts.ListSpecs, "list-specs", false, "列出内置 card spec")
	fs.BoolVar(&opts.ListTemplates, "list-templates", false, "列出已注册模板别名")
	fs.StringVar(&opts.Spec, "spec", "", "内置卡片 spec，例如 config、ratelimit.sample")
	fs.StringVar(&opts.Template, "template", "", "模板名称或模板 ID")
	fs.StringVar(&opts.VarsJSON, "vars-json", "", "模板变量 JSON")
	fs.StringVar(&opts.CardJSON, "card-json", "", "原生 schema v2 card JSON")
	fs.StringVar(&opts.CardFile, "card-file", "", "原生 schema v2 card JSON 文件路径")
	fs.StringVar(&opts.ToChatID, "to-chat-id", "", "发送目标 chat_id")
	fs.StringVar(&opts.ToOpenID, "to-open-id", "", "发送目标 open_id")
	fs.StringVar(&opts.ChatID, "chat-id", "", "业务上下文 chat_id，管理卡通常需要")
	fs.StringVar(&opts.ID, "id", "", "业务对象 ID，例如 schedule.task 需要 task_id")
	fs.StringVar(&opts.ActorOpenID, "actor-open-id", "", "业务上下文操作者 open_id")
	fs.StringVar(&opts.TargetOpenID, "target-open-id", "", "业务上下文目标用户 open_id")
	fs.StringVar(&opts.Scope, "scope", "", "业务上下文 scope，例如 chat/global/user")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "只构卡并输出 payload，不发送")
	fs.BoolVar(&opts.PrintPayload, "print-payload", false, "发送前输出 payload")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  go run ./cmd/lark-card-debug --list-specs")
		fmt.Fprintln(os.Stderr, "  go run ./cmd/lark-card-debug --spec ratelimit.sample --to-open-id ou_xxx")
		fmt.Fprintln(os.Stderr, "  go run ./cmd/lark-card-debug --template NormalCardReplyTemplate --vars-json '{\"content\":\"调试卡片\"}' --to-open-id ou_xxx")
		fmt.Fprintln(os.Stderr, "  go run ./cmd/lark-card-debug --card-file /tmp/card.json --to-open-id ou_xxx")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	if !opts.ListSpecs && !opts.ListTemplates && !hasBuildSource(opts) {
		fs.Usage()
		return options{}, fmt.Errorf("one of --spec, --template, --card-json, or --card-file is required")
	}

	return opts, nil
}

func buildPlan(opts options) bootstrapPlan {
	plan := bootstrapPlan{
		NeedLark: !opts.DryRun,
	}
	switch {
	case strings.TrimSpace(opts.Template) != "":
		plan.NeedDB = true
	case strings.TrimSpace(opts.Spec) == carddebug.SpecConfig,
		strings.TrimSpace(opts.Spec) == carddebug.SpecFeature,
		strings.TrimSpace(opts.Spec) == carddebug.SpecPermission,
		strings.TrimSpace(opts.Spec) == carddebug.SpecScheduleList,
		strings.TrimSpace(opts.Spec) == carddebug.SpecScheduleTask,
		strings.TrimSpace(opts.Spec) == carddebug.SpecWordCountSample,
		strings.TrimSpace(opts.Spec) == carddebug.SpecChunkSample:
		plan.NeedDB = true
	case strings.TrimSpace(opts.Spec) == carddebug.SpecRateLimit:
		plan.NeedRedis = true
	}
	if strings.TrimSpace(opts.Spec) == carddebug.SpecFeature {
		plan.NeedFeatureRegistry = true
	}
	return plan
}

func bootstrap(ctx context.Context, cfg *infraConfig.BaseConfig, plan bootstrapPlan) (*cleanupStack, error) {
	logs.Init()
	if cfg != nil && cfg.OtelConfig != nil {
		otel.Init(cfg.OtelConfig)
	}

	cleanup := &cleanupStack{}
	if plan.NeedDB {
		if cfg == nil || cfg.DBConfig == nil {
			return nil, fmt.Errorf("db_config is required for the selected card")
		}
		db.Init(cfg.DBConfig)
		cleanup.Add(func(context.Context) error {
			return closeDB()
		})
	}
	if plan.NeedRedis {
		if err := redis_dal.Init(ctx); err != nil {
			return nil, err
		}
		cleanup.Add(func(context.Context) error {
			return redis_dal.Close()
		})
	}
	if plan.NeedLark {
		lark_dal.Init()
	}
	if plan.NeedFeatureRegistry {
		messages.NewMessageProcessor(appconfig.GetManager())
	}
	return cleanup, nil
}

func buildCard(ctx context.Context, opts options, cfg *infraConfig.BaseConfig) (*carddebug.BuiltCard, error) {
	if strings.TrimSpace(opts.CardJSON) != "" || strings.TrimSpace(opts.CardFile) != "" {
		card, err := loadRawCard(opts.CardJSON, opts.CardFile)
		if err != nil {
			return nil, err
		}
		return &carddebug.BuiltCard{
			Mode:     carddebug.BuiltCardModeCardJSON,
			Label:    "raw.card_json",
			CardJSON: card,
		}, nil
	}

	actorOpenID := strings.TrimSpace(opts.ActorOpenID)
	if actorOpenID == "" && cfg != nil && cfg.LarkConfig != nil {
		actorOpenID = strings.TrimSpace(cfg.LarkConfig.BootstrapAdminOpenID)
	}
	if actorOpenID == "" {
		actorOpenID = strings.TrimSpace(opts.ToOpenID)
	}

	req := carddebug.BuildRequest{
		Spec:         strings.TrimSpace(opts.Spec),
		Template:     strings.TrimSpace(opts.Template),
		VarsJSON:     strings.TrimSpace(opts.VarsJSON),
		ChatID:       strings.TrimSpace(opts.ChatID),
		ID:           strings.TrimSpace(opts.ID),
		ActorOpenID:  actorOpenID,
		TargetOpenID: strings.TrimSpace(opts.TargetOpenID),
		Scope:        strings.TrimSpace(opts.Scope),
	}
	return carddebug.Build(ctx, req)
}

func loadRawCard(cardJSON, cardFile string) (map[string]any, error) {
	raw := strings.TrimSpace(cardJSON)
	if raw == "" {
		data, err := os.ReadFile(strings.TrimSpace(cardFile))
		if err != nil {
			return nil, err
		}
		raw = string(data)
	}
	card := make(map[string]any)
	if err := sonic.UnmarshalString(raw, &card); err != nil {
		return nil, fmt.Errorf("parse raw card json: %w", err)
	}
	return card, nil
}

func printPayload(built *carddebug.BuiltCard) error {
	if built == nil {
		return fmt.Errorf("built card is nil")
	}
	switch built.Mode {
	case carddebug.BuiltCardModeTemplate:
		if built.TemplateCard == nil {
			return fmt.Errorf("template card is nil")
		}
		fmt.Println(built.TemplateCard.String())
		return nil
	case carddebug.BuiltCardModeCardJSON:
		if built.CardJSON == nil {
			return fmt.Errorf("card json is nil")
		}
		data, err := json.MarshalIndent(built.CardJSON, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	default:
		return fmt.Errorf("unsupported built card mode %q", built.Mode)
	}
}

func printSpecs() {
	for _, spec := range carddebug.ListSpecs() {
		fmt.Printf("%-20s %s\n", spec.Name, spec.Description)
	}
}

func printTemplates() {
	for _, tpl := range carddebug.ListTemplates() {
		fmt.Printf("%-30s %s\n", tpl.Name, tpl.ID)
	}
}

func loadConfig() (*infraConfig.BaseConfig, error) {
	return infraConfig.LoadFileE(loadConfigPath())
}

func needConfig(plan bootstrapPlan, opts options) bool {
	if plan.NeedLark || plan.NeedDB || plan.NeedRedis {
		return true
	}
	return strings.TrimSpace(opts.ActorOpenID) == "" &&
		(strings.TrimSpace(opts.Spec) != "" || strings.TrimSpace(opts.Template) != "")
}

func loadConfigPath() string {
	if path := os.Getenv("BETAGO_CONFIG_PATH"); path != "" {
		return path
	}
	return ".dev/config.toml"
}

func hasBuildSource(opts options) bool {
	return strings.TrimSpace(opts.Spec) != "" ||
		strings.TrimSpace(opts.Template) != "" ||
		strings.TrimSpace(opts.CardJSON) != "" ||
		strings.TrimSpace(opts.CardFile) != ""
}

func closeDB() error {
	database := db.DB()
	if database == nil {
		return nil
	}
	sqlDB, err := database.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

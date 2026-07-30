package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	chatmetrics "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chatmetrics"
	larkchunking "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chunking"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckinaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/recording"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/reaction"
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	todoapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentcardcapability"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentcardcompiler"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentcardstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentcardsurface"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/akshareapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/conversationindex"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationindex"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationwindow"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/gotify"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/miniodal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/neteaseapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/opensearch"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/retriever"
	larkiface "github.com/BetaGoRobot/BetaGo-Redefine/internal/interfaces/lark"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/interfaces/webui"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhttp"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	uuid "github.com/satori/go.uuid"
)

type appComponents struct {
	messageExecutor              *appruntime.Executor
	reactionExecutor             *appruntime.Executor
	recordingExecutor            *appruntime.Executor
	chunkExecutor                *appruntime.Executor
	scheduleExecutor             *appruntime.Executor
	conversationExecutor         *appruntime.Executor
	projectionExecutor           *appruntime.Executor
	conversationRuntime          *agentruntime.Runtime
	conversationWorker           *agentruntime.ConversationWorker
	conversationProjectionWorker *agentruntime.ProjectionWorker
	continuationDispatcher       *appcardaction.ContinuationChain
	messageProcessor             *messages.MessageHandler
	feedbackRouter               *conversationeval.FeedbackRouter
	handlerSet                   *larkiface.HandlerSet
	eventDispatcher              *dispatcher.EventDispatcher
	agentCardSettings            appruntime.AgentCardSettings
	agentCardPatchReconciler     *agentcard.PatchReconciler
}

const (
	conversationContinuationLeaseTTL = 3 * time.Minute
	conversationProjectionLeaseTTL   = 2 * time.Minute
	conversationProjectionWriteTTL   = 30 * time.Second
	conversationInteractionWaitTTL   = 30 * time.Minute
)

// scheduler 仍保留为包级句柄，是因为当前调度器本身还没有实现
// runtime.Module。真正的生命周期仍由装配阶段注册的模块接管。
var scheduler *scheduleapp.Scheduler

// buildApp 是当前单体进程的装配根。这里集中完成：
// 1. 构造受控执行器和 handler 入口；
// 2. 按依赖顺序注册基础设施模块；
// 3. 注册应用服务、管理面和 websocket ingress。
func buildApp(cfg *infraConfig.BaseConfig) (*appruntime.App, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if cfg.LarkConfig == nil {
		return nil, errors.New("lark config is nil")
	}

	appcardaction.RegisterBuiltins()

	components, err := newAppComponents(cfg)
	if err != nil {
		return nil, err
	}
	app := appruntime.NewApp()

	addInfrastructureModules(app, cfg)
	addExecutorModules(app, components)
	addApplicationModules(app, cfg, components)

	return app, nil
}

// newAppComponents 只负责构造“会被多个模块共享”的装配对象，不直接向
// App 注册模块，避免对象创建和生命周期注册混在一起。
func newAppComponents(cfg *infraConfig.BaseConfig) (*appComponents, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if cfg.LarkConfig == nil {
		return nil, errors.New("lark config is nil")
	}
	agentCardSettings, err := appruntime.AgentCardRolloutSettings(cfg)
	if err != nil {
		return nil, err
	}
	if agentCardSettings.ToolsAvailable() && !agentCardSettings.Shadow() &&
		(strings.TrimSpace(cfg.LarkConfig.AppSecret) == "" ||
			strings.TrimSpace(cfg.LarkConfig.AppID) == "" ||
			strings.TrimSpace(cfg.LarkConfig.BotOpenID) == "") {
		return nil, errors.New(
			"agent card delivery requires complete lark bot identity and secret",
		)
	}
	executorConfigs := appruntime.ExecutorConfigs(cfg)
	if err := validateConversationRuntimeBudgets(executorConfigs); err != nil {
		return nil, err
	}

	messageExecutor := appruntime.NewExecutor(executorConfigs["message"])
	reactionExecutor := appruntime.NewExecutor(executorConfigs["reaction"])
	recordingExecutor := appruntime.NewExecutor(executorConfigs["recording"])
	chunkExecutor := appruntime.NewExecutor(executorConfigs["chunk"])
	scheduleExecutor := appruntime.NewExecutor(executorConfigs["schedule"])
	conversationExecutor := appruntime.NewExecutor(executorConfigs["conversation"])
	projectionExecutor := appruntime.NewExecutor(executorConfigs["projection"])

	conversationRuntime, err := agentruntime.NewRuntime(agentruntime.RuntimeOptions{
		ConversationExecutor: conversationExecutor,
		CallbackContinuationEnabled: func(ctx context.Context, chatID string) bool {
			return appconfig.IsConversationCallbackContinuationEnabled(ctx, chatID, "")
		},
	})
	if err != nil {
		return nil, err
	}
	scheduleContinuationDispatcher, err := appcardaction.NewScheduleInteractionDispatcher(
		conversationRuntime,
		appcardaction.ScheduleInteractionDispatcherOptions{
			IndexAlias: appruntime.ConversationEventIndex(cfg),
		},
	)
	if err != nil {
		return nil, err
	}
	continuationDispatcher, err := appcardaction.NewContinuationChain(
		scheduleContinuationDispatcher,
	)
	if err != nil {
		return nil, err
	}
	conversationWorker, err := agentruntime.NewConversationWorker(
		conversationRuntime,
		agentruntime.ConversationWorkerOptions{
			Interval: 2 * time.Second, MaxBackoff: time.Minute, BatchSize: 64,
		},
	)
	if err != nil {
		return nil, err
	}
	conversationProjectionWorker, err := agentruntime.NewProjectionWorker(
		conversationRuntime,
		agentruntime.ProjectionWorkerOptions{
			Interval: time.Second, MaxBackoff: time.Minute, BatchSize: 64,
		},
	)
	if err != nil {
		return nil, err
	}

	feedbackRouter := conversationeval.NewFeedbackRouter()
	messageProcessor := messages.NewMessageProcessorWithOptions(
		appconfig.GetManager(),
		messages.MessageHandlerOptions{
			InteractionStarter: conversationRuntime,
			FeedbackSink:       feedbackRouter,
			AgentCardEnabled: func(_ context.Context, chatID string) bool {
				if agentCardSettings.Shadow() {
					return true
				}
				return agentCardSettings.CanSend(chatID)
			},
		},
	)
	reactionProcessor := reaction.NewReactionProcessorWithOptions(reaction.ProcessorOptions{
		FeedbackSink: feedbackRouter,
	})
	handlerSet := larkiface.NewHandlerSet(larkiface.HandlerSetOptions{
		MessageProcessor:       messageProcessor,
		ReactionProcessor:      reactionProcessor,
		MessageExecutor:        messageExecutor,
		ReactionExecutor:       reactionExecutor,
		ContinuationDispatcher: continuationDispatcher,
		FeedbackSink:           feedbackRouter,
	})

	return &appComponents{
		messageExecutor: messageExecutor, reactionExecutor: reactionExecutor,
		recordingExecutor: recordingExecutor, chunkExecutor: chunkExecutor,
		scheduleExecutor: scheduleExecutor, conversationExecutor: conversationExecutor,
		projectionExecutor:  projectionExecutor,
		conversationRuntime: conversationRuntime, conversationWorker: conversationWorker,
		conversationProjectionWorker: conversationProjectionWorker,
		continuationDispatcher:       continuationDispatcher,
		messageProcessor:             messageProcessor,
		feedbackRouter:               feedbackRouter,
		handlerSet:                   handlerSet,
		eventDispatcher:              newEventDispatcher(cfg, handlerSet),
		agentCardSettings:            agentCardSettings,
	}, nil
}

func validateConversationRuntimeBudgets(configs map[string]appruntime.ExecutorConfig) error {
	conversation := configs["conversation"]
	projection := configs["projection"]
	if conversation.TaskTimeout <= 0 ||
		conversation.TaskTimeout >= conversationContinuationLeaseTTL {
		return fmt.Errorf(
			"conversation executor timeout %s must be positive and shorter than continuation lease %s",
			conversation.TaskTimeout,
			conversationContinuationLeaseTTL,
		)
	}
	if projection.TaskTimeout <= conversationProjectionWriteTTL ||
		projection.TaskTimeout >= conversationProjectionLeaseTTL {
		return fmt.Errorf(
			"projection budget must satisfy write timeout %s < executor timeout %s < lease %s",
			conversationProjectionWriteTTL,
			projection.TaskTimeout,
			conversationProjectionLeaseTTL,
		)
	}
	return nil
}

func agentCardBindingKey(secret string) []byte {
	returnKey := sha256.Sum256([]byte("betago-agent-card-binding\x00" + secret))
	return returnKey[:]
}

// addInfrastructureModules 注册基础设施层模块。顺序严格反映依赖方向：
// 先准备底层连接和客户端，再让上层应用服务接入它们。
func addInfrastructureModules(app *appruntime.App, cfg *infraConfig.BaseConfig) {
	app.AddModule(newRecoverModule("otel", false, func() {
		otel.Init(cfg.OtelConfig)
	}))
	app.AddModule(newRecoverModule("vm_metrics", false, func() {
		initVMMetrics(cfg.VMConfig)
	}))
	app.AddModule(newRecoverModule("logging", true, func() {
		logs.Init()
	}))
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "db",
		Critical: true,
		Init: func(context.Context) error {
			return appruntime.RecoverError("db.Init", func() {
				db.Init(cfg.DBConfig)
				llmusage.SetDefaultBotIDProvider(func() string {
					id := botidentity.Current()
					if id.AppID != "" {
						return "lark:" + id.AppID
					}
					return ""
				})
				llmusage.SetDefaultRecorder(llmusage.NewRecorder(db.DB()))
			})
		},
		Ready: func(ctx context.Context) error {
			return pingDB(ctx, db.DB())
		},
		Stop: func(context.Context) error {
			return closeDB(db.DB())
		},
	}))
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "redis",
		Critical: true,
		Start: func(ctx context.Context) error {
			return redis_dal.Init(ctx)
		},
		Ready: func(ctx context.Context) error {
			return redis_dal.Ping(ctx)
		},
		Stop: func(context.Context) error {
			return redis_dal.Close()
		},
	}))
	app.AddModule(newOptionalModule("lark_api", func() {
		lark_dal.Init()
	}, func(context.Context) error {
		if lark_dal.Client() == nil {
			return errors.New("lark client unavailable")
		}
		return nil
	}))
	app.AddModule(newOptionalModule("opensearch", func() {
		opensearch.Init(cfg.OpensearchConfig)
	}, func(context.Context) error {
		if ok, reason := opensearch.Status(); !ok {
			return errors.New(reason)
		}
		return nil
	}))
	app.AddModule(newOptionalModule("ark_runtime", func() {
		ark_dal.Init(cfg.ArkConfig)
	}, func(context.Context) error {
		if ok, reason := ark_dal.Status(); !ok {
			return errors.New(reason)
		}
		return nil
	}))
	app.AddModule(newOptionalModule("minio", func() {
		miniodal.Init(cfg.MinioConfig)
	}, func(context.Context) error {
		if ok, reason := miniodal.Status(); !ok {
			return errors.New(reason)
		}
		return nil
	}))
	app.AddModule(newOptionalModule("gotify", func() {
		gotify.Init()
	}, func(context.Context) error {
		return gotify.ErrUnavailable()
	}))
	app.AddModule(newOptionalModule("akshareapi", func() {
		akshareapi.Init()
	}, func(context.Context) error {
		if ok, reason := akshareapi.Status(); !ok {
			return errors.New(reason)
		}
		return nil
	}))
	app.AddModule(newRecoverModule("xhttp", false, func() {
		xhttp.Init()
	}))
	app.AddModule(newRecoverModule("netease_music", false, func() {
		neteaseapi.Init()
	}))
	app.AddModule(newOptionalModule("retriever", func() {
		retriever.Init()
	}, func(context.Context) error {
		if ok, reason := retriever.Status(); !ok {
			return errors.New(reason)
		}
		return nil
	}))
}

// addExecutorModules 把受控执行器作为一等运行时模块接入健康检查和关闭
// 流程，避免“工作池存在但运行时看不见”。
func addExecutorModules(app *appruntime.App, components *appComponents) {
	app.AddModule(components.messageExecutor)
	app.AddModule(components.reactionExecutor)
	app.AddModule(components.recordingExecutor)
	app.AddModule(components.chunkExecutor)
	app.AddModule(components.scheduleExecutor)
	app.AddModule(components.conversationExecutor)
	app.AddModule(components.projectionExecutor)
}

// addApplicationModules 注册依赖基础设施和执行器的上层模块。这里把遗留
// 包级初始化收敛成有序的运行时阶段。
func addApplicationModules(app *appruntime.App, cfg *infraConfig.BaseConfig, components *appComponents) {
	app.AddModule(chatmetrics.NewModule(chatmetrics.ModuleOptions{
		Interval:     5 * time.Minute,
		Timeout:      2 * time.Minute,
		RecentWindow: 24 * time.Hour,
		Collector: chatmetrics.Collector{
			CountRecentMessages: countRecentChatMessages,
		},
	}))
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "application_services",
		Critical: true,
		Start: func(context.Context) error {
			recording.SetBackgroundSubmitter(components.recordingExecutor)
			larkchunking.SetEnabledForChat(func(ctx context.Context, chatID string) bool {
				return appconfig.IsChunkEnabled(ctx, chatID, "")
			})
			larkchunking.SetExecutor(components.chunkExecutor)
			todoapp.Init(db.DB())
			scheduleapp.Init(db.DB(), handlers.BuildSchedulableTools())
			repository := agentstore.NewRepository(db.DB())
			agentCardRepository := agentcardstore.NewRepository(db.DB())
			agentCardCompiler := agentcardcompiler.New()
			agentCardSurfaceClient := agentcardsurface.NewClient(
				agentcardsurface.ClientOptions{},
			)
			patchProcessors := make([]agentcard.PatchProcessor, 0,
				components.agentCardSettings.PatchWorkerCount)
			for range components.agentCardSettings.PatchWorkerCount {
				patchWorker, patchErr := agentcard.NewPatchWorker(
					agentcard.PatchWorkerOptions{
						Store: agentCardRepository, Client: agentCardSurfaceClient,
						WorkerID: "agent-card-patch-" + uuid.NewV4().String(),
						LeaseTTL: components.agentCardSettings.PatchLease,
					},
				)
				if patchErr != nil {
					return patchErr
				}
				patchProcessors = append(patchProcessors, patchWorker)
			}
			agentCardPatchReconciler, err :=
				agentcard.NewPatchReconciler(agentcard.PatchReconcilerOptions{
					Catalog: agentCardRepository, Processors: patchProcessors,
					BatchSize: 64, Interval: time.Second,
				})
			if err != nil {
				return err
			}
			components.agentCardPatchReconciler = agentCardPatchReconciler
			agentCardCallback, err := agentcard.NewCallbackDispatcher(
				agentcard.CallbackDispatcherOptions{
					Store:    agentCardRepository,
					Compiler: agentCardCompiler,
					Now:      func() time.Time { return time.Now().UTC() },
				},
			)
			if err != nil {
				return err
			}
			if err := components.continuationDispatcher.Add(agentCardCallback); err != nil {
				return err
			}
			agentCardCapabilityExecutor, err :=
				agentcardcapability.NewAgentCardCapabilityExecutor(
					handlers.BuildRuntimeCapabilityTools(),
				)
			if err != nil {
				return err
			}
			agentCardCapabilityService, err := agentcard.NewCapabilityService(
				agentcard.CapabilityServiceOptions{
					Store:    agentCardRepository,
					Executor: agentCardCapabilityExecutor,
					Compiler: agentCardCompiler,
					Now:      func() time.Time { return time.Now().UTC() },
				},
			)
			if err != nil {
				return err
			}
			appID := ""
			botOpenID := ""
			tokenSecret := ""
			if cfg.LarkConfig != nil {
				appID = cfg.LarkConfig.AppID
				botOpenID = cfg.LarkConfig.BotOpenID
				tokenSecret = cfg.LarkConfig.AppSecret
			}
			if components.agentCardSettings.ToolsAvailable() {
				composerOptions := agentcard.RolloutAuthoringComposerOptions{
					Compiler:        agentCardCompiler,
					ProjectionIndex: appruntime.ConversationEventIndex(cfg),
					Shadow:          components.agentCardSettings.Shadow(),
					CanSend:         components.agentCardSettings.CanSend,
				}
				if !components.agentCardSettings.Shadow() {
					binder, binderErr := agentcard.NewBinder(
						agentcard.BinderOptions{
							Store: agentCardRepository, Compiler: agentCardCompiler,
							BindingKey: agentCardBindingKey(tokenSecret),
							Policy:     agentcard.PolicyConfig{},
						},
					)
					if binderErr != nil {
						return binderErr
					}
					runResolver, resolverErr :=
						agentcard.NewDurableAuthoringRunResolver(
							agentcard.DurableAuthoringRunResolverOptions{
								Store: repository, AppID: appID,
								BotOpenID: botOpenID,
							},
						)
					if resolverErr != nil {
						return resolverErr
					}
					composerOptions.RunResolver = runResolver
					composerOptions.Delivery = agentcard.NewService(
						binder,
						agentCardRepository,
						agentCardSurfaceClient,
					)
				}
				authoringComposer, composerErr :=
					agentcard.NewRolloutAuthoringComposer(composerOptions)
				if composerErr != nil {
					return composerErr
				}
				components.messageProcessor.SetAgentCardService(
					agentcard.NewToolService(agentcard.ToolServiceOptions{
						Catalog: agentcard.NewCatalog(), Composer: authoringComposer,
						Policy:            agentcard.PolicyConfig{},
						MaxRepairAttempts: components.agentCardSettings.MaxRepairAttempts,
						DefaultExpiry:     components.agentCardSettings.DefaultExpiry,
					}),
				)
			}
			starter, err := agentruntime.NewDurableScheduleEditStarter(
				agentruntime.DurableScheduleEditStarterOptions{
					Store: repository, AppID: appID, BotOpenID: botOpenID,
					TokenSecret:     []byte(tokenSecret),
					WaitTTL:         conversationInteractionWaitTTL,
					ProjectionIndex: appruntime.ConversationEventIndex(cfg),
				},
			)
			if err != nil {
				return err
			}
			generator := agentruntime.NewContinuationGenerator(continuationModelID(cfg))
			enabledProcessor := agentruntime.NewContinuationProcessor(
				repository,
				generator,
				agentruntime.NewLarkReplyDeliverer(),
				agentruntime.ContinuationProcessorConfig{
					WorkerID:            "conversation-enabled-" + uuid.NewV4().String(),
					LeaseTTL:            conversationContinuationLeaseTTL,
					RetryDelay:          5 * time.Second,
					RecentStepLimit:     32,
					CapabilityProcessor: agentCardCapabilityService,
				},
			)
			disabledProcessor := agentruntime.NewDisabledContinuationProcessor(
				repository,
				agentruntime.DisabledContinuationProcessorConfig{
					WorkerID:            "conversation-disabled-" + uuid.NewV4().String(),
					LeaseTTL:            conversationContinuationLeaseTTL,
					CapabilityProcessor: agentCardCapabilityService,
				},
			)
			projector := agentruntime.NewProjector(
				conversationindex.NewStore(db.DB()),
				conversationindex.OpenSearchWriter{},
				components.projectionExecutor,
				agentruntime.ProjectorConfig{
					WorkerID:     "conversation-projection-" + uuid.NewV4().String(),
					LeaseTTL:     conversationProjectionLeaseTTL,
					WriteTimeout: conversationProjectionWriteTTL,
					Now:          func() time.Time { return time.Now().UTC() },
				},
			)
			scheduleInteractionService := agentruntime.NewScheduleInteractionService(
				repository,
				scheduleapp.NewRuntimeScheduleEditCapability(scheduleapp.GetService()),
				components.conversationRuntime,
			)
			return components.conversationRuntime.Bind(agentruntime.RuntimeDependencies{
				InteractionStarter: starter,
				ScheduleResolver:   scheduleInteractionService,
				EnabledProcessor:   enabledProcessor,
				DisabledProcessor:  disabledProcessor,
				Catalog:            repository,
				Projector:          projector,
				Expirer:            repository,
			})
		},
		Ready: func(context.Context) error {
			if !scheduleapp.GetService().Available() {
				return errors.New("schedule service unavailable")
			}
			if !todoapp.GetService().Available() {
				return errors.New("todo service unavailable")
			}
			return nil
		},
	}))
	app.AddModule(&agentCardPatchModule{components: components})
	app.AddModule(components.conversationWorker)
	app.AddModule(components.conversationProjectionWorker)
	addConversationEvaluationModule(app, cfg, components)
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "chunking",
		Critical: false,
		Start: func(ctx context.Context) error {
			larkchunking.Start(ctx)
			if !larkchunking.Enabled() {
				return fmt.Errorf("%w: %s", appruntime.ErrDisabled, larkchunking.DisableReason())
			}
			return nil
		},
		Ready: func(context.Context) error {
			if !larkchunking.Enabled() {
				return errors.New(larkchunking.DisableReason())
			}
			return nil
		},
		Stop: func(context.Context) error {
			larkchunking.Stop()
			return nil
		},
	}))
	app.AddModule(appruntime.NewHealthHTTPModule(
		managementAddr(cfg),
		appruntime.ManagementShutdownTimeout(cfg),
		app.Registry(),
		appruntime.PrometheusProvider{},
	))
	app.AddModule(webui.NewModule(webui.Options{
		Config:           webuiConfig(cfg),
		ConfigManager:    appconfig.GetManager(),
		DBProvider:       db.DB,
		ChatService:      webui.NewLarkChatService(),
		MemberCount:      webui.LarkMemberCount,
		MemberList:       webui.LarkMemberList,
		MessageStats:     countRecentChatMessages,
		RecentChatIDs:    recentChatIDs,
		ChatActivity:     chatActivityHourOfWeek,
		ChatKeywords:     chatKeywordsToken,
		ChatCommands:     chatCommandsTop,
		ChatTopSenders:   chatTopSenders,
		ChatMessageKinds: chatMessageKinds,
		ChatCommandTrend: chatCommandTrend,
		ChatTopMentions:  chatTopMentions,
		ChatTopicTrend:   chatTopicTrend,
		RobotName: func() string {
			if cfg.BaseInfo != nil {
				return cfg.BaseInfo.RobotName
			}
			return ""
		}(),
		Instance: cfg.LarkConfig.AppID,
		BotID: func() string {
			if cfg.LarkConfig != nil && cfg.LarkConfig.AppID != "" {
				return "lark:" + cfg.LarkConfig.AppID
			}
			return ""
		}(),
		AppID:     cfg.LarkConfig.AppID,
		BotOpenID: cfg.LarkConfig.BotOpenID,
	}))
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "scheduler",
		Critical: false,
		Start: func(context.Context) error {
			service := scheduleapp.GetService()
			if !service.Available() {
				return fmt.Errorf("%w: schedule service unavailable", appruntime.ErrDisabled)
			}
			scheduler = scheduleapp.NewSchedulerWithExecutor(service, components.scheduleExecutor)
			scheduler.Start()
			return nil
		},
		Stop: func(context.Context) error {
			if scheduler != nil {
				scheduler.Stop()
			}
			return nil
		},
		Stats: components.scheduleExecutor.Stats,
	}))

	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "luckin_order_poller",
		Critical: false,
		Start: func(context.Context) error {
			luckinaction.StartOrderPoller()
			return nil
		},
		Stop: func(context.Context) error {
			luckinaction.StopOrderPoller()
			return nil
		},
	}))

	app.AddModule(appruntime.NewLarkWSModule(
		cfg.LarkConfig.AppID,
		cfg.LarkConfig.AppSecret,
		components.eventDispatcher,
	))
}

func addConversationEvaluationModule(
	app *appruntime.App,
	cfg *infraConfig.BaseConfig,
	components *appComponents,
) {
	var candidateWorker *conversationeval.CandidateWorker
	var judgeWorker *conversationeval.JudgeWorker
	var projectionWorker *evaluationindex.ProjectionWorker
	var repository *evaluationstore.Repository
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "conversation_evaluation",
		Critical: false,
		Init: func(ctx context.Context) error {
			var installed bool
			if err := db.DB().WithContext(ctx).Raw(`
				SELECT
					to_regclass('betago.evaluation_episode_messages') IS NOT NULL
					AND to_regclass('betago.evaluation_candidate_tasks') IS NOT NULL
					AND EXISTS (
						SELECT 1
						FROM information_schema.columns
						WHERE table_schema = 'betago'
						  AND table_name = 'evaluation_episodes'
						  AND column_name = 'post_window_reason'
					)`,
			).Scan(&installed).Error; err != nil {
				return err
			}
			if !installed {
				return errors.New(
					"conversation evaluation runtime migration 20260729 is not installed",
				)
			}
			return nil
		},
		Start: func(ctx context.Context) error {
			repository = evaluationstore.NewRepository(db.DB())
			service, err := conversationeval.NewService(conversationeval.ServiceOptions{
				Repository: repository, PreWindowSource: evaluationwindow.OpenSearchPreWindowSource{},
				CandidateSubmitter: repository,
			})
			if err != nil {
				return err
			}
			runtimeConfig := cfg.RuntimeConfig
			if runtimeConfig == nil {
				runtimeConfig = &infraConfig.RuntimeConfig{}
			}
			processor, err := conversationeval.NewCandidateProcessor(
				repository,
				service,
				handlers.BuildCandidateRunnerForTask,
				conversationeval.CandidateProcessorConfig{
					WorkerID: "evaluation-candidate-" + uuid.NewV4().String(),
					LeaseTTL: durationSeconds(
						runtimeConfig.EvaluationCandidateLeaseSeconds,
						10*time.Minute,
					),
					RetryDelay: durationSeconds(
						runtimeConfig.EvaluationCandidateRetrySeconds,
						15*time.Second,
					),
				},
			)
			if err != nil {
				return err
			}
			pollInterval := time.Second
			if runtimeConfig.EvaluationCandidatePollMillis > 0 {
				pollInterval = time.Duration(runtimeConfig.EvaluationCandidatePollMillis) *
					time.Millisecond
			}
			candidateWorker, err = conversationeval.NewCandidateWorker(
				processor,
				conversationeval.CandidateWorkerOptions{
					Workers:  runtimeConfig.EvaluationCandidateWorkers,
					Interval: pollInterval,
					WindowSweepInterval: durationSeconds(
						runtimeConfig.EvaluationWindowSweepSeconds,
						5*time.Second,
					),
				},
			)
			if err != nil {
				return err
			}
			judgeModelID := ""
			if !runtimeConfig.EvaluationJudgeDisabled {
				judgeModelID = evaluationJudgeModelID(cfg, runtimeConfig)
			}
			if judgeModelID != "" {
				judge, judgeErr := conversationeval.NewJudge(
					conversationeval.JudgeConfig{ModelID: judgeModelID},
					repository,
				)
				if judgeErr != nil {
					return judgeErr
				}
				judgeProcessor, judgeErr := conversationeval.NewJudgeProcessor(
					repository,
					judge,
					nil,
				)
				if judgeErr != nil {
					return judgeErr
				}
				judgePollInterval := time.Second
				if runtimeConfig.EvaluationJudgePollMillis > 0 {
					judgePollInterval = time.Duration(
						runtimeConfig.EvaluationJudgePollMillis,
					) * time.Millisecond
				}
				judgeWorker, judgeErr = conversationeval.NewJudgeWorker(
					judgeProcessor,
					conversationeval.JudgeWorkerOptions{
						Workers:    runtimeConfig.EvaluationJudgeWorkers,
						Interval:   judgePollInterval,
						MaxBackoff: 2 * time.Minute,
					},
				)
				if judgeErr != nil {
					return judgeErr
				}
			}
			if cfg.OpensearchConfig != nil {
				indexStore, indexErr := evaluationindex.NewStoreWithBackend(
					runtimeConfig.EvaluationIndex,
					evaluationindex.NewOpenSearchBackend(),
				)
				if indexErr != nil {
					return indexErr
				}
				projectionProcessor, indexErr := evaluationindex.NewProjectionProcessor(
					repository,
					indexStore,
					runtimeConfig.EvaluationProjectionBatchSize,
				)
				if indexErr != nil {
					return indexErr
				}
				projectionWorker, indexErr = evaluationindex.NewProjectionWorker(
					projectionProcessor,
					evaluationindex.ProjectionWorkerOptions{
						Interval: durationSeconds(
							runtimeConfig.EvaluationProjectionIntervalSeconds,
							30*time.Second,
						),
						MaxBackoff: 5 * time.Minute,
					},
				)
				if indexErr != nil {
					return indexErr
				}
			}
			components.messageProcessor.SetEvaluationService(service)
			components.feedbackRouter.Bind(service)
			if err := candidateWorker.Start(ctx); err != nil {
				components.feedbackRouter.Bind(nil)
				components.messageProcessor.SetEvaluationService(nil)
				return err
			}
			if judgeWorker != nil {
				if err := judgeWorker.Start(ctx); err != nil {
					_ = candidateWorker.Stop(ctx)
					components.feedbackRouter.Bind(nil)
					components.messageProcessor.SetEvaluationService(nil)
					return err
				}
			}
			if projectionWorker != nil {
				if err := projectionWorker.Start(ctx); err != nil {
					if judgeWorker != nil {
						_ = judgeWorker.Stop(ctx)
					}
					_ = candidateWorker.Stop(ctx)
					components.feedbackRouter.Bind(nil)
					components.messageProcessor.SetEvaluationService(nil)
					return err
				}
			}
			return nil
		},
		Ready: func(context.Context) error {
			if candidateWorker == nil {
				return errors.New("conversation evaluation worker is not running")
			}
			return nil
		},
		Stop: func(ctx context.Context) error {
			components.feedbackRouter.Bind(nil)
			components.messageProcessor.SetEvaluationService(nil)
			var stopErr error
			if projectionWorker != nil {
				stopErr = errors.Join(stopErr, projectionWorker.Stop(ctx))
			}
			if judgeWorker != nil {
				stopErr = errors.Join(stopErr, judgeWorker.Stop(ctx))
			}
			if candidateWorker != nil {
				stopErr = errors.Join(stopErr, candidateWorker.Stop(ctx))
			}
			return stopErr
		},
		Stats: func() map[string]any {
			if candidateWorker == nil {
				return map[string]any{"running": false}
			}
			stats := map[string]any{
				"running":            true,
				"candidate":          candidateWorker.Stats(),
				"judge_enabled":      judgeWorker != nil,
				"projection_enabled": projectionWorker != nil,
			}
			if judgeWorker != nil {
				stats["judge"] = judgeWorker.Stats()
			}
			if projectionWorker != nil {
				stats["projection"] = projectionWorker.Stats()
			}
			if repository != nil {
				metricsCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				cursor := evaluationindex.ProjectionCursor{}
				if projectionWorker != nil {
					cursor = projectionWorker.Cursor()
				}
				if metrics, err := repository.EvaluationMetrics(metricsCtx, cursor); err != nil {
					stats["metrics_error"] = err.Error()
				} else {
					stats["metrics"] = metrics
				}
			}
			return stats
		},
	}))
}

func evaluationJudgeModelID(
	cfg *infraConfig.BaseConfig,
	runtimeConfig *infraConfig.RuntimeConfig,
) string {
	if runtimeConfig != nil {
		if modelID := strings.TrimSpace(runtimeConfig.EvaluationJudgeModel); modelID != "" {
			return modelID
		}
	}
	if cfg == nil || cfg.ArkConfig == nil {
		return ""
	}
	if modelID := strings.TrimSpace(cfg.ArkConfig.ReasoningModel); modelID != "" {
		return modelID
	}
	return strings.TrimSpace(cfg.ArkConfig.NormalModel)
}

func durationSeconds(value int, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return time.Duration(value) * time.Second
}

// newEventDispatcher 负责把运行时管理的 HandlerSet 绑定到当前订阅的
// Lark 事件类型上。
func newEventDispatcher(
	cfg *infraConfig.BaseConfig,
	handlerSet *larkiface.HandlerSet,
) *dispatcher.EventDispatcher {
	verificationToken := ""
	encryptionKey := ""
	if cfg != nil && cfg.LarkConfig != nil {
		verificationToken = cfg.LarkConfig.VerificationToken
		encryptionKey = cfg.LarkConfig.EncryptionKey
	}
	return dispatcher.
		NewEventDispatcher(verificationToken, encryptionKey).
		OnP2MessageReactionCreatedV1(handlerSet.MessageReactionHandler).
		OnP2MessageReceiveV1(handlerSet.MessageV2Handler).
		OnP2ApplicationAppVersionAuditV6(handlerSet.AuditV6Handler).
		OnP2CardActionTrigger(handlerSet.CardActionHandler).
		OnP2MessageRecalledV1(emptyHandler).
		OnP2ChatMemberUserAddedV1(emptyHandler).
		OnP2ChatMemberBotDeletedV1(emptyHandler).
		OnP2ChatMemberUserDeletedV1(emptyHandler)
}

func continuationModelID(cfg *infraConfig.BaseConfig) string {
	if cfg == nil || cfg.ArkConfig == nil {
		return ""
	}
	if modelID := strings.TrimSpace(cfg.ArkConfig.NormalModel); modelID != "" {
		return modelID
	}
	return strings.TrimSpace(cfg.ArkConfig.ReasoningModel)
}

func initVMMetrics(cfg *infraConfig.VMConfig) {
	if cfg == nil {
		return
	}
	pushInterval := time.Duration(cfg.PushInterval) * time.Second
	xhandler.InitMetrics(cfg.PushURL, pushInterval, cfg.Instance)
}

func emptyHandler[T any](context.Context, T) error {
	return nil
}

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
	chatmetrics "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chatmetrics"
	larkchunking "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chunking"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/handlers"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckinaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/recording"
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	todoapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentcardcapability"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentcardcompiler"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentcardstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentcardsurface"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/agentstore"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/conversationindex"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/interfaces/webui"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	uuid "github.com/satori/go.uuid"
)

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
			owner, err := tenant.New(
				cfg.LarkConfig.AppID,
				cfg.LarkConfig.BotOpenID,
			)
			if err != nil {
				return fmt.Errorf("derive runtime tenant: %w", err)
			}
			repository, err := agentstore.NewRepository(db.DB(), owner)
			if err != nil {
				return fmt.Errorf("create agent repository: %w", err)
			}
			agentCardRepository, err := agentcardstore.NewRepository(db.DB(), owner)
			if err != nil {
				return fmt.Errorf("create agent card repository: %w", err)
			}
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
					ProjectionIndex: components.conversationIndexAlias,
					Shadow:          components.agentCardSettings.Shadow(),
					CanSend: func(chatID string) bool {
						return components.agenticRollouts.AgentCardEnabled(
							context.Background(),
							chatID,
						)
					},
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
					ProjectionIndex: components.conversationIndexAlias,
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
			projectionStore, err := conversationindex.NewStore(
				db.DB(), owner, components.conversationIndexAlias,
			)
			if err != nil {
				return fmt.Errorf("create conversation index store: %w", err)
			}
			projector := agentruntime.NewProjector(
				projectionStore,
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
		AppID:           cfg.LarkConfig.AppID,
		BotOpenID:       cfg.LarkConfig.BotOpenID,
		AgenticRollouts: components.agenticRollouts,
	}))
	app.AddModule(appruntime.NewFuncModule(appruntime.FuncModuleOptions{
		Name:     "scheduler",
		Critical: false,
		Start: func(context.Context) error {
			service := scheduleapp.GetService()
			if !service.Available() {
				return fmt.Errorf("%w: schedule service unavailable", appruntime.ErrDisabled)
			}
			components.scheduler = scheduleapp.NewSchedulerWithExecutor(service, components.scheduleExecutor)
			components.scheduler.Start()
			return nil
		},
		Stop: func(context.Context) error {
			if components.scheduler != nil {
				components.scheduler.Stop()
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

func agentCardBindingKey(secret string) []byte {
	returnKey := sha256.Sum256([]byte("betago-agent-card-binding\x00" + secret))
	return returnKey[:]
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

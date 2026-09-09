package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/agenticrollout"
	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcard"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/reaction"
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/evaluationindex"
	larkiface "github.com/BetaGoRobot/BetaGo-Redefine/internal/interfaces/lark"
	appruntime "github.com/BetaGoRobot/BetaGo-Redefine/internal/runtime"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"go.uber.org/zap"
)

type appComponentDependencies struct {
	newTenantIndexProvisioner func() (tenantIndexProvisioner, error)
}

type appComponents struct {
	deps                         appComponentDependencies
	scheduler                    *scheduleapp.Scheduler
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
	evaluationSettings           appruntime.EvaluationSettings
	tenant                       tenant.Tenant
	conversationIndexAlias       string
	evaluationIndexAlias         string
	schemaBootstrap              *bootstrapStatus
	searchBootstrap              *bootstrapStatus
	evaluationSearchMu           sync.Mutex
	evaluationSearchReady        bool
	agentCardPatchReconciler     *agentcard.PatchReconciler
	agenticRollouts              *agenticrollout.Service
}

const (
	conversationContinuationLeaseTTL = 3 * time.Minute
	conversationProjectionLeaseTTL   = 2 * time.Minute
	conversationProjectionWriteTTL   = 30 * time.Second
	conversationInteractionWaitTTL   = 30 * time.Minute
)

// newAppComponents 只负责构造“会被多个模块共享”的装配对象，不直接向
// App 注册模块，避免对象创建和生命周期注册混在一起。
func newAppComponents(cfg *infraConfig.BaseConfig) (*appComponents, error) {
	return newAppComponentsWithDependencies(cfg, appComponentDependencies{})
}

func newAppComponentsWithDependencies(
	cfg *infraConfig.BaseConfig,
	deps appComponentDependencies,
) (*appComponents, error) {
	if deps.newTenantIndexProvisioner == nil {
		deps.newTenantIndexProvisioner = newTenantIndexProvisioner
	}
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
	evaluationSettings, err := appruntime.EvaluationRolloutSettings(cfg)
	if err != nil {
		return nil, err
	}
	runtimeTenant, err := tenant.New(
		cfg.LarkConfig.AppID,
		cfg.LarkConfig.BotOpenID,
	)
	if err != nil {
		return nil, fmt.Errorf("derive runtime tenant: %w", err)
	}
	conversationIndexAlias, err := runtimeTenant.IndexAlias(
		appruntime.ConversationEventIndex(cfg),
	)
	if err != nil {
		return nil, fmt.Errorf("derive conversation index alias: %w", err)
	}
	evaluationIndexBase := evaluationindex.DefaultIndexAlias
	if cfg.RuntimeConfig != nil &&
		strings.TrimSpace(cfg.RuntimeConfig.EvaluationIndex) != "" {
		evaluationIndexBase = cfg.RuntimeConfig.EvaluationIndex
	}
	evaluationIndexAlias, err := runtimeTenant.IndexAlias(evaluationIndexBase)
	if err != nil {
		return nil, fmt.Errorf("derive evaluation index alias: %w", err)
	}
	schemaBootstrap := newBootstrapStatus(map[string]any{
		"tenant_id":       runtimeTenant.ID,
		"schema":          runtimeSchemaName(cfg),
		"binary_revision": runtimeSchemaRevision(),
	})
	searchStats := map[string]any{
		"tenant_id":              runtimeTenant.ID,
		"conversation_alias":     conversationIndexAlias,
		"conversation_schema":    "conversation_event.v1",
		"evaluation_mode":        string(evaluationSettings.Mode),
		"evaluation_allow_count": evaluationSettings.AllowedChatCount(),
	}
	if evaluationSettings.Enabled() {
		searchStats["evaluation_alias"] = evaluationIndexAlias
		searchStats["evaluation_schema"] = "conversation_evaluation.v1"
	}
	searchBootstrap := newBootstrapStatus(searchStats)
	if agentCardSettings.ToolsAvailable() && !agentCardSettings.Shadow() &&
		(strings.TrimSpace(cfg.LarkConfig.AppSecret) == "" ||
			strings.TrimSpace(cfg.LarkConfig.AppID) == "" ||
			strings.TrimSpace(cfg.LarkConfig.BotOpenID) == "") {
		return nil, errors.New(
			"agent card delivery requires complete lark bot identity and secret",
		)
	}
	agenticRollouts, err := agenticrollout.NewService(
		agenticrollout.ServiceOptions{
			Store:     appconfig.GetManager(),
			Namespace: runtimeTenant.ID,
			Static: agenticrollout.StaticPolicies{
				EvaluationAvailable: evaluationSettings.Enabled(),
				EvaluationAllows:    evaluationSettings.Allows,
				AgentCardAvailable: agentCardSettings.ToolsAvailable() &&
					!agentCardSettings.Shadow(),
				AgentCardAllows: agentCardSettings.CanSend,
				AgentCardUnavailableReason: agentCardUnavailableReason(
					agentCardSettings,
				),
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create agentic rollout service: %w", err)
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
			return agenticRollouts.CallbackContinuationEnabled(ctx, chatID)
		},
	})
	if err != nil {
		return nil, err
	}
	scheduleContinuationDispatcher, err := appcardaction.NewScheduleInteractionDispatcher(
		conversationRuntime,
		appcardaction.ScheduleInteractionDispatcherOptions{
			IndexAlias: conversationIndexAlias,
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
	var components *appComponents
	messageProcessor := messages.NewMessageProcessorWithOptions(
		appconfig.GetManager(),
		messages.MessageHandlerOptions{
			InteractionStarter: conversationRuntime,
			FeedbackSink:       feedbackRouter,
			RuntimeEnabled:     agenticRollouts.RuntimeEnabled,
			AgentCardEnabled: func(ctx context.Context, chatID string) bool {
				if agentCardSettings.Shadow() {
					return true
				}
				return agenticRollouts.AgentCardEnabled(ctx, chatID)
			},
			EvaluationEnabled: func(ctx context.Context, chatID string) bool {
				if !agenticRollouts.EvaluationEnabled(ctx, chatID) {
					return false
				}
				if err := ensureEvaluationSearchIndex(ctx, cfg, components); err != nil {
					logs.L().Ctx(ctx).Error(
						"ensure evaluation search index failed",
						zap.Error(err),
					)
					return false
				}
				return true
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

	components = &appComponents{
		deps:            deps,
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
		evaluationSettings:           evaluationSettings,
		tenant:                       runtimeTenant,
		conversationIndexAlias:       conversationIndexAlias,
		evaluationIndexAlias:         evaluationIndexAlias,
		schemaBootstrap:              schemaBootstrap,
		searchBootstrap:              searchBootstrap,
		agenticRollouts:              agenticRollouts,
	}
	return components, nil
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

func agentCardUnavailableReason(
	settings appruntime.AgentCardSettings,
) string {
	switch {
	case settings.Shadow():
		return "agent_card_shadow_mode"
	case !settings.ToolsAvailable():
		return "agent_card_off"
	default:
		return ""
	}
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

func emptyHandler[T any](context.Context, T) error {
	return nil
}

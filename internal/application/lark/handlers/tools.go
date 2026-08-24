package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcardtool"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	scheduleapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/schedule"
	todoapp "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/todo"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/toolmeta"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func NewCandidateRunnerFactory(
	modelID string,
) (conversationeval.CandidateRunnerFactory, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil, fmt.Errorf("candidate Ark model id is required")
	}
	return func(
		_ context.Context,
		task conversationeval.CandidateTask,
	) (conversationeval.CandidateRunner, error) {
		if err := task.Validate(); err != nil {
			return nil, err
		}
		engine, err := conversationeval.NewArkCandidateStageEngine(
			conversationeval.ArkCandidateEngineConfig{
				ModelID: modelID,
				Scope: llmusage.Scope{
					ChatID: task.Message.ChatID, OpenID: task.Message.SenderOpenID,
					SourceType:        llmusage.SourceTypeBackground,
					Source:            "conversation_evaluation_candidate",
					BusinessScene:     llmusage.SceneEvaluation,
					BusinessOperation: llmusage.OperationCandidateGeneration,
				},
			},
		)
		if err != nil {
			return nil, err
		}
		registry, err := BuildCandidateShadowRegistry(
			conversationeval.NewObservationCache(),
			nil,
			task.Message.ChatID,
			task.Message.SenderOpenID,
			task.Episode.AnchorAt,
		)
		if err != nil {
			return nil, err
		}
		return conversationeval.NewCandidateRunner(engine, registry), nil
	}, nil
}

func BuildLarkTools() *tools.Impl[larkim.P2MessageReceiveV1] {
	return BuildLarkToolsWithAgentCardService(nil)
}

func BuildLarkToolsWithAgentCardService(
	agentCardService agentcardtool.Service,
) *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := buildTools(true, true, true, true)
	xcommand.RegisterTool(ins, PermissionManage)
	registerLuckinTools(ins)
	registerAgentCardTools(ins, agentCardService)
	return ins
}

func BuildInjectableFinanceTools() *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := tools.New[larkim.P2MessageReceiveV1]()
	registerInjectableFinanceTools(ins)
	return ins
}

func BuildRuntimeCapabilityTools() *tools.Impl[larkim.P2MessageReceiveV1] {
	return BuildRuntimeCapabilityToolsForContext(context.Background())
}

func BuildRuntimeCapabilityToolsForContext(
	ctx context.Context,
) *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := larktools(ctx)
	registerInjectableFinanceTools(ins)
	return ins
}

func BuildCandidateShadowTools() (*tools.Impl[larkim.P2MessageReceiveV1], error) {
	production := BuildRuntimeCapabilityTools()
	shadow := tools.New[larkim.P2MessageReceiveV1]()
	for _, name := range conversationeval.CandidateShadowToolNames() {
		behavior, explicit := toolmeta.LookupRuntimeBehavior(name)
		if !explicit {
			return nil, fmt.Errorf("candidate shadow tool %q has no explicit runtime behavior", name)
		}
		if behavior.SideEffectLevel != toolmeta.SideEffectLevelNone {
			return nil, fmt.Errorf(
				"candidate shadow tool %q has side effect level %q",
				name,
				behavior.SideEffectLevel,
			)
		}
		unit, registered := production.Get(name)
		if !registered {
			return nil, fmt.Errorf("candidate shadow tool %q is not registered", name)
		}
		shadow.Add(unit)
	}
	return shadow, nil
}

func BuildCandidateShadowRegistry(
	cache *conversationeval.ObservationCache,
	event *larkim.P2MessageReceiveV1,
	chatID, openID string,
	anchorAt time.Time,
) (*conversationeval.ShadowToolRegistry, error) {
	if anchorAt.IsZero() {
		return nil, fmt.Errorf("candidate shadow tool anchor is required")
	}
	larkTools, err := BuildCandidateShadowTools()
	if err != nil {
		return nil, err
	}
	registry := conversationeval.NewAnchoredShadowToolRegistry(cache, anchorAt)
	for _, name := range conversationeval.CandidateShadowToolNames() {
		unit, ok := larkTools.Get(name)
		if !ok {
			return nil, fmt.Errorf("candidate shadow tool %q disappeared during adapter build", name)
		}
		if err := registry.Register(name, func(ctx context.Context, arguments json.RawMessage) (string, error) {
			if name == "search_history" {
				clampedArguments, clampErr := conversationeval.ClampCandidateSearchHistoryArguments(
					arguments,
					anchorAt,
				)
				if clampErr != nil {
					return "", clampErr
				}
				arguments = clampedArguments
				ctx = withCandidateHistoryAnchor(ctx, anchorAt)
			}
			result := unit.Function(ctx, string(arguments), tools.FCMeta[larkim.P2MessageReceiveV1]{
				ChatID: chatID,
				OpenID: openID,
				Data:   event,
			})
			if result.IsErr() {
				return "", result.Err()
			}
			return result.Value(), nil
		}); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func larktools(ctx context.Context) *tools.Impl[larkim.P2MessageReceiveV1] {
	service, _ := agentcardtool.ServiceFromContext(ctx)
	return BuildLarkToolsWithAgentCardService(service)
}

func BuildSchedulableTools() *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := buildTools(false, false, false, false)
	scheduleapp.RegisterRuntimeTools(ins)
	return ins
}

func buildTools(enableWebSearch, includeDebugRevert, includeScheduleTools, allowTargetChatOverride bool) *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := tools.New[larkim.P2MessageReceiveV1]()
	if enableWebSearch {
		ins.WebSearch()
	}

	registerBaseTools(ins, allowTargetChatOverride)
	if includeDebugRevert {
		xcommand.RegisterTool(ins, DebugRevert)
	}
	if includeScheduleTools {
		scheduleapp.RegisterTools(ins)
	}
	return ins
}

func registerBaseTools(ins *tools.Impl[larkim.P2MessageReceiveV1], allowTargetChatOverride bool) {
	xcommand.RegisterTool(ins, ChatMembers)
	xcommand.RegisterTool(ins, RecentActiveMembers)
	xcommand.RegisterTool(ins, SearchHistory)
	xcommand.RegisterTool(ins, ResearchReadURL)
	xcommand.RegisterTool(ins, ResearchExtractEvidence)
	xcommand.RegisterTool(ins, ResearchSourceLedger)
	xcommand.RegisterTool(ins, FinanceToolDiscover)
	xcommand.RegisterTool(ins, MusicSearch)
	xcommand.RegisterTool(ins, Mute)
	xcommand.RegisterTool(ins, Gold)
	xcommand.RegisterTool(ins, OneWord)
	xcommand.RegisterTool(ins, ZhAStock)
	xcommand.RegisterTool(ins, Trend)
	xcommand.RegisterTool(ins, WordCloud)
	xcommand.RegisterTool(ins, WordCloudGraph)
	xcommand.RegisterTool(ins, WordChunks)
	xcommand.RegisterTool(ins, WordChunkDetail)

	xcommand.RegisterTool(ins, ConfigList)
	xcommand.RegisterTool(ins, ConfigSet)
	xcommand.RegisterTool(ins, ConfigDelete)

	xcommand.RegisterTool(ins, SetHistoryCutoff)
	xcommand.RegisterTool(ins, StoreCorrection)
	xcommand.RegisterTool(ins, SetChatContext)

	xcommand.RegisterTool(ins, FeatureList)
	xcommand.RegisterTool(ins, FeatureBlock)
	xcommand.RegisterTool(ins, FeatureUnblock)

	xcommand.RegisterTool(ins, WordAdd)
	xcommand.RegisterTool(ins, WordGet)

	xcommand.RegisterTool(ins, ReplyAdd)
	xcommand.RegisterTool(ins, ReplyGet)

	xcommand.RegisterTool(ins, ImageAdd)
	xcommand.RegisterTool(ins, ImageGet)
	xcommand.RegisterTool(ins, ImageDelete)

	xcommand.RegisterTool(ins, AddEmojiReaction)

	xcommand.RegisterTool(ins, RateLimitStats)
	xcommand.RegisterTool(ins, RateLimitList)

	if allowTargetChatOverride {
		xcommand.RegisterTool(ins, SendMessage)
	} else {
		xcommand.RegisterTool(ins, ScheduledSendMessage)
	}
	todoapp.RegisterTools(ins)
}

func registerInjectableFinanceTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	xcommand.RegisterTool(ins, FinanceMarketData)
	xcommand.RegisterTool(ins, FinanceNews)
	xcommand.RegisterTool(ins, EconomyIndicator)
}

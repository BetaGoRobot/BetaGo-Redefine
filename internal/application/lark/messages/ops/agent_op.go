package ops

import (
	"context"
	"strings"
	"sync"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	capdef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/capability"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

const (
	agentRuntimeShadowEnterKey      = "agent_runtime.shadow.enter_runtime"
	agentRuntimeShadowTriggerKey    = "agent_runtime.shadow.trigger_type"
	agentRuntimeShadowReasonKey     = "agent_runtime.shadow.reason"
	agentRuntimeShadowScopeKey      = "agent_runtime.shadow.scope"
	agentRuntimeShadowCandidatesKey = "agent_runtime.shadow.candidates"
	agentRuntimeShadowRunIDKey      = "agent_runtime.shadow.run_id"
	agentRuntimeShadowSessionIDKey  = "agent_runtime.shadow.session_id"
)

type agentRuntimeShadowConfig interface {
	ChatMode() appconfig.ChatMode
}

type agentRuntimeShadowObserver interface {
	Observe(context.Context, agentruntime.ShadowObserveInput) agentruntime.ShadowObservation
}

type agentRuntimeActiveRunProvider interface {
	ActiveRunSnapshot(context.Context, string, string) (*agentruntime.ActiveRunSnapshot, error)
}

type AgentShadowOperator struct {
	OpBase
	now                func() time.Time
	configAccessor     func(context.Context, *larkim.P2MessageReceiveV1, *xhandler.BaseMetaData) agentRuntimeShadowConfig
	observer           agentRuntimeShadowObserver
	coordinator        agentruntime.ShadowRunStarter
	coordinatorLoader  func(context.Context) agentruntime.ShadowRunStarter
	coordinatorMu      sync.RWMutex
	mentionDetector    func(*larkim.P2MessageReceiveV1) bool
	replyToBotDetector func(context.Context, *larkim.P2MessageReceiveV1) bool
	commandDetector    func(context.Context, *larkim.P2MessageReceiveV1) (bool, string)
}

func NewAgentShadowOperator() *AgentShadowOperator {
	registry := agentruntime.NewCapabilityRegistry()
	for _, capability := range capdef.BuildDefaultCommandBridgeCapabilities() {
		if err := registry.Register(capability); err != nil {
			logs.L().Warn("register default command bridge capability failed", zap.Error(err))
		}
	}

	op := &AgentShadowOperator{
		now: func() time.Time { return time.Now() },
		configAccessor: func(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) agentRuntimeShadowConfig {
			return messageConfigAccessor(ctx, event, meta)
		},
		coordinatorLoader: buildDefaultShadowRunCoordinator,
		mentionDetector: func(event *larkim.P2MessageReceiveV1) bool {
			if event == nil || event.Event == nil || event.Event.Message == nil {
				return false
			}
			return larkmsg.IsMentioned(event.Event.Message.Mentions)
		},
		replyToBotDetector: detectReplyToCurrentBot,
		commandDetector: func(ctx context.Context, event *larkim.P2MessageReceiveV1) (bool, string) {
			if !isCommandMessage(ctx, event) {
				return false, ""
			}
			parts := xcommand.GetCommand(ctx, messageText(ctx, event))
			if len(parts) == 0 {
				return false, ""
			}
			return true, parts[0]
		},
	}
	op.observer = agentruntime.NewShadowObserver(
		agentruntime.NewDefaultGroupPolicy(agentruntime.DefaultGroupPolicyConfig{}),
		registry,
		func(ctx context.Context, chatID, actorOpenID string) *agentruntime.ActiveRunSnapshot {
			return op.activeRunSnapshot(ctx, chatID, actorOpenID)
		},
	)
	return op
}

func (r *AgentShadowOperator) Name() string {
	return "AgentShadowOperator"
}

func (r *AgentShadowOperator) PreRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	accessor := r.runtimeConfigAccessor(ctx, event, meta)
	if accessor == nil || accessor.ChatMode().Normalize() != appconfig.ChatModeStandard {
		return skipStage(r.Name(), "agent runtime shadow disabled")
	}
	return nil
}

func (r *AgentShadowOperator) Run(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)

	observer := r.runtimeObserver()
	observation := observer.Observe(ctx, agentruntime.ShadowObserveInput{
		Now:         r.currentTime(),
		ChatID:      messageChatID(event, meta),
		ChatType:    currentChatType(event),
		Mentioned:   r.isMentioned(event),
		ReplyToBot:  r.isReplyToBot(ctx, event),
		IsCommand:   r.isCommand(ctx, event),
		CommandName: r.commandName(ctx, event),
		ActorOpenID: messageOpenID(event, meta),
		InputText:   strings.TrimSpace(messageText(ctx, event)),
	})

	recordShadowObservation(meta, observation)
	r.persistShadowRun(ctx, event, meta, observation)
	logs.L().Ctx(ctx).Info("agent runtime shadow observation",
		zap.Bool("enter_runtime", observation.EnterRuntime),
		zap.String("trigger_type", string(observation.TriggerType)),
		zap.String("reason", observation.Reason),
		zap.String("scope", string(observation.Scope)),
		zap.Strings("candidate_capabilities", observation.CandidateCapabilities),
		zap.String("chat_id", messageChatID(event, meta)),
		zap.String("open_id", messageOpenID(event, meta)),
	)
	return nil
}

func (r *AgentShadowOperator) runtimeConfigAccessor(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) agentRuntimeShadowConfig {
	if r != nil && r.configAccessor != nil {
		return r.configAccessor(ctx, event, meta)
	}
	return nil
}

func (r *AgentShadowOperator) runtimeObserver() agentRuntimeShadowObserver {
	if r != nil && r.observer != nil {
		return r.observer
	}
	return agentruntime.NewShadowObserver(agentruntime.NewDefaultGroupPolicy(agentruntime.DefaultGroupPolicyConfig{}), nil, nil)
}

func (r *AgentShadowOperator) currentTime() time.Time {
	if r != nil && r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *AgentShadowOperator) isMentioned(event *larkim.P2MessageReceiveV1) bool {
	if r != nil && r.mentionDetector != nil {
		return r.mentionDetector(event)
	}
	return false
}

func (r *AgentShadowOperator) isReplyToBot(ctx context.Context, event *larkim.P2MessageReceiveV1) bool {
	if r != nil && r.replyToBotDetector != nil {
		return r.replyToBotDetector(ctx, event)
	}
	return false
}

func (r *AgentShadowOperator) isCommand(ctx context.Context, event *larkim.P2MessageReceiveV1) bool {
	isCommand, _ := r.detectCommand(ctx, event)
	return isCommand
}

func (r *AgentShadowOperator) commandName(ctx context.Context, event *larkim.P2MessageReceiveV1) string {
	_, commandName := r.detectCommand(ctx, event)
	return commandName
}

func (r *AgentShadowOperator) detectCommand(ctx context.Context, event *larkim.P2MessageReceiveV1) (bool, string) {
	if r != nil && r.commandDetector != nil {
		return r.commandDetector(ctx, event)
	}
	return false, ""
}

func currentChatType(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.ChatType == nil {
		return ""
	}
	return strings.TrimSpace(*event.Event.Message.ChatType)
}

func detectReplyToCurrentBot(ctx context.Context, event *larkim.P2MessageReceiveV1) bool {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.ParentId == nil {
		return false
	}
	parentResp := larkmsg.GetMsgFullByID(ctx, *event.Event.Message.ParentId)
	if parentResp == nil || !parentResp.Success() || parentResp.Data == nil || len(parentResp.Data.Items) == 0 || parentResp.Data.Items[0] == nil {
		return false
	}
	parent := parentResp.Data.Items[0]
	if parent.Sender == nil || parent.Sender.Id == nil {
		return false
	}
	identity := botidentity.Current()
	parentID := strings.TrimSpace(*parent.Sender.Id)
	if parentID == "" {
		return false
	}
	if identity.BotOpenID != "" && parentID == identity.BotOpenID {
		return true
	}
	if identity.AppID != "" && parentID == identity.AppID {
		return true
	}
	return false
}

func recordShadowObservation(meta *xhandler.BaseMetaData, observation agentruntime.ShadowObservation) {
	if meta == nil {
		return
	}
	meta.SetExtra(agentRuntimeShadowEnterKey, boolString(observation.EnterRuntime))
	meta.SetExtra(agentRuntimeShadowTriggerKey, string(observation.TriggerType))
	meta.SetExtra(agentRuntimeShadowReasonKey, observation.Reason)
	meta.SetExtra(agentRuntimeShadowScopeKey, string(observation.Scope))
	meta.SetExtra(agentRuntimeShadowCandidatesKey, strings.Join(observation.CandidateCapabilities, ","))
}

func (r *AgentShadowOperator) persistShadowRun(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData, observation agentruntime.ShadowObservation) {
	if r == nil || !observation.EnterRuntime {
		return
	}
	coordinator := r.runtimeCoordinator(ctx)
	if coordinator == nil {
		return
	}

	run, err := coordinator.StartShadowRun(ctx, agentruntime.StartShadowRunRequest{
		ChatID:           messageChatID(event, meta),
		ActorOpenID:      messageOpenID(event, meta),
		TriggerType:      observation.TriggerType,
		TriggerMessageID: currentMessageID(event),
		AttachToRunID:    observation.AttachToRunID,
		SupersedeRunID:   observation.SupersedeRunID,
		InputText:        strings.TrimSpace(messageText(ctx, event)),
		Now:              r.currentTime(),
	})
	if err != nil {
		logs.L().Ctx(ctx).Warn("agent runtime shadow run persist failed",
			zap.Error(err),
			zap.String("chat_id", messageChatID(event, meta)),
			zap.String("trigger_type", string(observation.TriggerType)),
		)
		return
	}
	if meta == nil || run == nil {
		return
	}
	if run.ID != "" {
		meta.SetExtra(agentRuntimeShadowRunIDKey, run.ID)
	}
	if run.SessionID != "" {
		meta.SetExtra(agentRuntimeShadowSessionIDKey, run.SessionID)
	}
}

func (r *AgentShadowOperator) runtimeCoordinator(ctx context.Context) agentruntime.ShadowRunStarter {
	if r == nil {
		return nil
	}

	r.coordinatorMu.RLock()
	coordinator := r.coordinator
	r.coordinatorMu.RUnlock()
	if coordinator != nil {
		return coordinator
	}
	if r.coordinatorLoader == nil {
		return nil
	}

	loaded := r.coordinatorLoader(ctx)
	if loaded == nil {
		return nil
	}

	r.coordinatorMu.Lock()
	defer r.coordinatorMu.Unlock()
	if r.coordinator == nil {
		r.coordinator = loaded
	}
	return r.coordinator
}

func (r *AgentShadowOperator) activeRunSnapshot(ctx context.Context, chatID, actorOpenID string) *agentruntime.ActiveRunSnapshot {
	coordinator := r.runtimeCoordinator(ctx)
	if coordinator == nil {
		return nil
	}
	provider, ok := coordinator.(agentRuntimeActiveRunProvider)
	if !ok || provider == nil {
		return nil
	}
	snapshot, err := provider.ActiveRunSnapshot(ctx, strings.TrimSpace(chatID), strings.TrimSpace(actorOpenID))
	if err != nil {
		logs.L().Ctx(ctx).Warn("agent runtime active run snapshot lookup failed",
			zap.Error(err),
			zap.String("chat_id", strings.TrimSpace(chatID)),
			zap.String("actor_open_id", strings.TrimSpace(actorOpenID)),
		)
		return nil
	}
	return snapshot
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

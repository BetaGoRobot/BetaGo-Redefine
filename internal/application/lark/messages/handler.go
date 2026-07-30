package messages

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentcardtool"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	larkchunking "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/chunking"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/ops"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages/recording"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkuser"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.uber.org/zap"
)

type MessageHandler struct {
	processor          *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]
	interactionStarter agentruntime.InteractionStarter
	agentCardService   atomic.Pointer[agentCardServiceHolder]
	agentCardEnabled   func(context.Context, string) bool
	runtimeEnabled     func(context.Context, string) bool
	evaluationEnabled  func(context.Context, string) bool
	feedbackSink       conversationeval.FeedbackSink
	evaluationService  atomic.Pointer[conversationeval.Service]
}

type agentCardServiceHolder struct {
	service agentcardtool.Service
}

type MessageHandlerOptions struct {
	InteractionStarter agentruntime.InteractionStarter
	AgentCardService   agentcardtool.Service
	AgentCardEnabled   func(context.Context, string) bool
	RuntimeEnabled     func(context.Context, string) bool
	EvaluationEnabled  func(context.Context, string) bool
	EvaluationService  *conversationeval.Service
	FeedbackSink       conversationeval.FeedbackSink
}

// Handler 消息处理入口。
var Handler *MessageHandler

// ConfigManager 全局配置管理器（新代码应该使用依赖注入）
var ConfigManager *appconfig.Manager

var (
	getChatName     = larkchat.GetChatName
	getUserNameByID = larkuser.GetUserNameCache
)

func NewMessageProcessor(cfgManager *appconfig.Manager) *MessageHandler {
	return NewMessageProcessorWithOptions(cfgManager, MessageHandlerOptions{})
}

func NewMessageProcessorWithOptions(
	cfgManager *appconfig.Manager,
	options MessageHandlerOptions,
) *MessageHandler {
	if cfgManager == nil {
		cfgManager = appconfig.GetManager()
	}
	if options.RuntimeEnabled == nil {
		options.RuntimeEnabled = func(ctx context.Context, chatID string) bool {
			return cfgManager.GetBool(
				ctx,
				appconfig.KeyConversationRuntimeEnabled,
				chatID,
				"",
			)
		}
	}
	if options.EvaluationEnabled == nil {
		options.EvaluationEnabled = func(ctx context.Context, chatID string) bool {
			return cfgManager.GetBool(
				ctx,
				appconfig.KeyConversationParallelEvaluationEnabled,
				chatID,
				"",
			)
		}
	}
	handler := &MessageHandler{
		interactionStarter: options.InteractionStarter,
		agentCardEnabled:   options.AgentCardEnabled,
		runtimeEnabled:     options.RuntimeEnabled,
		evaluationEnabled:  options.EvaluationEnabled,
		feedbackSink:       options.FeedbackSink,
		processor: newMessageProcessorBase(cfgManager).
			AddAsync(&ops.ReplyChatOperator{}).
			AddAsync(&ops.CommandOperator{}).
			AddAsync(&ops.ChatMsgOperator{}),
	}
	handler.SetAgentCardService(options.AgentCardService)
	handler.evaluationService.Store(options.EvaluationService)
	cfgManager.SetGetFeaturesFunc(func() []appconfig.Feature {
		return collectMessageFeatures(handler.processor)
	})
	return handler
}

func (h *MessageHandler) Run(ctx context.Context, event *larkim.P2MessageReceiveV1) {
	if h == nil {
		return
	}
	processor := h.processor
	if processor == nil {
		return
	}
	ctx = h.contextForEvent(ctx, event)
	var evaluationSession *conversationeval.MessageSession
	evaluationService := h.evaluationService.Load()
	if evaluationService != nil || h.feedbackSink != nil {
		input, err := evaluationMessageInput(ctx, event)
		if err != nil {
			logs.L().Ctx(ctx).Warn("build evaluation message input failed", zap.Error(err))
		} else {
			if h.feedbackSink != nil {
				if feedbackErr := h.feedbackSink.ObserveMessage(
					ctx,
					messageFeedbackFromInput(input),
				); feedbackErr != nil {
					logs.L().Ctx(ctx).Warn(
						"observe conversation feedback failed",
						zap.Error(feedbackErr),
					)
				}
			}
			if evaluationService != nil && h.evaluationEnabled(ctx, input.ChatID) {
				evaluationSession, err = evaluationService.BeginMessage(ctx, input)
				if err != nil {
					logs.L().Ctx(ctx).Warn("begin conversation evaluation failed", zap.Error(err))
				} else if evaluationSession.Enabled() {
					ctx = conversationeval.WithCapture(ctx, evaluationSession.Capture())
				}
			}
		}
	}
	processor.NewExecution().WithCtx(ctx).WithData(event).Run()
	if evaluationSession != nil {
		if err := evaluationService.CompleteMessage(ctx, evaluationSession); err != nil {
			logs.L().Ctx(ctx).Warn("complete conversation evaluation failed", zap.Error(err))
		}
	}
}

func messageFeedbackFromInput(input conversationeval.MessageInput) conversationeval.MessageFeedback {
	return conversationeval.MessageFeedback{
		EventID: input.EventID, MessageID: input.MessageID, ChatID: input.ChatID,
		TopicID: input.TopicID, ActorOpenID: input.SenderOpenID,
		ReplyToMessageID:   input.ReplyToMessageID,
		Content:            input.Content,
		ExplicitCorrection: conversationeval.IsExplicitCorrection(input.Content),
		OccurredAt:         input.OccurredAt,
	}
}

func (h *MessageHandler) SetEvaluationService(service *conversationeval.Service) {
	if h != nil {
		h.evaluationService.Store(service)
	}
}

func (h *MessageHandler) SetAgentCardService(service agentcardtool.Service) {
	if h == nil {
		return
	}
	if service == nil {
		h.agentCardService.Store(nil)
		return
	}
	h.agentCardService.Store(&agentCardServiceHolder{service: service})
}

func evaluationMessageInput(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
) (conversationeval.MessageInput, error) {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return conversationeval.MessageInput{}, conversationeval.ErrInvalidContract
	}
	message := event.Event.Message
	occurredMillis, err := strconv.ParseInt(
		strings.TrimSpace(utils.AddrOrNil(message.CreateTime)),
		10,
		64,
	)
	if err != nil || occurredMillis <= 0 {
		return conversationeval.MessageInput{}, conversationeval.ErrInvalidContract
	}
	messageID := strings.TrimSpace(utils.AddrOrNil(message.MessageId))
	identity := botidentity.Current()
	content := strings.TrimSpace(utils.AddrOrNil(message.Content))
	if parsed := safelyParseEvaluationContent(ctx, event); parsed != "" {
		content = parsed
	}
	return conversationeval.MessageInput{
		AppID: identity.AppID, BotOpenID: identity.BotOpenID,
		ChatID:  strings.TrimSpace(utils.AddrOrNil(message.ChatId)),
		EventID: messageID, MessageID: messageID,
		TopicID:          strings.TrimSpace(utils.AddrOrNil(message.ThreadId)),
		SenderOpenID:     botidentity.MessageSenderOpenID(event),
		ReplyToMessageID: strings.TrimSpace(utils.AddrOrNil(message.ParentId)),
		Content:          content, OccurredAt: time.UnixMilli(occurredMillis),
	}, nil
}

func safelyParseEvaluationContent(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
) (content string) {
	defer func() {
		if recover() != nil {
			content = ""
		}
	}()
	result := larkmsg.PreGetTextMsg(ctx, event)
	if result == nil {
		return ""
	}
	return strings.TrimSpace(result.GetText())
}

func (h *MessageHandler) contextForEvent(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if h == nil || event == nil || event.Event == nil {
		return ctx
	}
	chatID := ""
	if event.Event.Message != nil && event.Event.Message.ChatId != nil {
		chatID = *event.Event.Message.ChatId
	}
	agentCardHolder := h.agentCardService.Load()
	if agentCardHolder != nil && agentCardHolder.service != nil &&
		h.agentCardEnabled != nil && h.agentCardEnabled(ctx, chatID) {
		ctx = agentcardtool.WithService(ctx, agentCardHolder.service)
	}
	if h.interactionStarter != nil && h.runtimeEnabled != nil &&
		h.runtimeEnabled(ctx, chatID) {
		ctx = agentruntime.WithInteractionStarter(ctx, h.interactionStarter)
	}
	return ctx
}

func newMessageProcessorBase(cfgManager *appconfig.Manager) *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData] {
	return (&xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]{}).
		OnPanic(func(ctx context.Context, err error, event *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData) {
			larkmsg.SendRecoveredMsg(ctx, err, *event.Event.Message.MessageId)
		}).
		WithMetaDataProcess(metaInit).
		WithPreRun(func(p *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]) {
			go utils.AddTrace2DB(p, *p.Data().Event.Message.MessageId)
		}).
		WithDefer(recording.CollectMessage).
		WithDefer(func(ctx context.Context, event *larkim.P2MessageReceiveV1, meta *xhandler.BaseMetaData) {
			if !meta.IsCommandMarked() {
				if privateModeEnabled, err := larkmsg.IsPrivateModeEnabled(ctx, *event.Event.Message.ChatId); err != nil {
					return
				} else if privateModeEnabled {
					return
				}
				_ = larkchunking.SubmitMessage(ctx, &larkchunking.LarkMessageEvent{P2MessageReceiveV1: event})
			}
		}).
		WithFeatureChecker(cfgManager.FeatureCheckFunc()).
		WithStageFilter(newMessageStageFilter()).
		AddAsync(&ops.RecordMsgOperator{}).
		AddAsync(&ops.RepeatMsgOperator{}).
		AddAsync(&ops.ReactMsgOperator{}).
		AddAsync(ops.WordReplyStage)
}

func collectMessageFeatures(processors ...*xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]) []appconfig.Feature {
	features := make([]appconfig.Feature, 0)
	seen := make(map[string]struct{})
	for _, processor := range processors {
		if processor == nil {
			continue
		}
		for _, fi := range processor.ListFeatures() {
			if _, ok := seen[fi.ID]; ok {
				continue
			}
			seen[fi.ID] = struct{}{}
			features = append(features, appconfig.Feature{
				Name:           fi.ID,
				Description:    fi.Description,
				Category:       "message",
				DefaultEnabled: fi.Default,
			})
		}
	}
	return features
}

func init() {
	ConfigManager = appconfig.NewManager()
	Handler = NewMessageProcessor(ConfigManager)
}

func metaInit(event *larkim.P2MessageReceiveV1) *xhandler.BaseMetaData {
	chatID := *event.Event.Message.ChatId
	isP2P := *event.Event.Message.ChatType == "p2p"
	openID := botidentity.MessageSenderOpenID(event)
	chatName := resolveChatName(context.Background(), chatID, isP2P, openID)
	return &xhandler.BaseMetaData{
		ChatID:   chatID,
		IsP2P:    isP2P,
		ChatName: chatName,
		OpenID:   openID,
	}
}

func resolveChatName(ctx context.Context, chatID string, isP2P bool, openID string) string {
	if isP2P {
		if openID != "" {
			if name, err := getUserNameByID(ctx, chatID, openID); err == nil && name != "" {
				return "[单聊]" + name
			}
		}
		return "p2p"
	}
	if name := getChatName(ctx, chatID); name != "" {
		return name
	}
	return "unknown"
}

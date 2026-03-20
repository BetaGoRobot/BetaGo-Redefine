package agentruntime

import (
	"context"
	"iter"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/mutestate"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const chatEntryModelTypeReason = "reason"

type chatResponseMode string

const (
	chatResponseModeStandard chatResponseMode = "standard"
	chatResponseModeAgentic  chatResponseMode = "agentic"
)

type chatEntryConfigAccessor interface {
	ChatMode() appconfig.ChatMode
	AgentRuntimeEnabled() bool
	AgentRuntimeChatCutover() bool
	ChatReasoningModel() string
	ChatNormalModel() string
}

type ChatResponseRequest struct {
	Event          *larkim.P2MessageReceiveV1
	Plan           ChatGenerationPlan
	RuntimeEnabled bool
	CutoverEnabled bool
	StartedAt      time.Time
	Ownership      InitialRunOwnership
}

type ChatResponseDispatcher func(context.Context, ChatResponseRequest) error

type ChatEntryHandlerOptions struct {
	Now               func() time.Time
	AccessorBuilder   func(context.Context, string, string) chatEntryConfigAccessor
	MentionChecker    func(*larkim.P2MessageReceiveV1) bool
	MuteChecker       func(context.Context, string) (bool, error)
	FileCollector     func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error)
	AgenticResponder  ChatResponseDispatcher
	StandardResponder ChatResponseDispatcher
}

type ChatEntryHandler struct {
	now               func() time.Time
	accessorBuilder   func(context.Context, string, string) chatEntryConfigAccessor
	mentionChecker    func(*larkim.P2MessageReceiveV1) bool
	muteChecker       func(context.Context, string) (bool, error)
	fileCollector     func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error)
	agenticResponder  ChatResponseDispatcher
	standardResponder ChatResponseDispatcher
}

func NewChatEntryHandler(opts ChatEntryHandlerOptions) *ChatEntryHandler {
	handler := &ChatEntryHandler{
		now: defaultChatEntryNow,
		accessorBuilder: func(ctx context.Context, chatID, openID string) chatEntryConfigAccessor {
			return appconfig.NewAccessor(ctx, chatID, openID)
		},
		mentionChecker: defaultMentionChecker,
		muteChecker:    defaultMuteChecker,
		fileCollector:  defaultFileCollector,
	}
	if opts.Now != nil {
		handler.now = opts.Now
	}
	if opts.AccessorBuilder != nil {
		handler.accessorBuilder = opts.AccessorBuilder
	}
	if opts.MentionChecker != nil {
		handler.mentionChecker = opts.MentionChecker
	}
	if opts.MuteChecker != nil {
		handler.muteChecker = opts.MuteChecker
	}
	if opts.FileCollector != nil {
		handler.fileCollector = opts.FileCollector
	}
	handler.agenticResponder = opts.AgenticResponder
	handler.standardResponder = opts.StandardResponder
	return handler
}

func (h *ChatEntryHandler) Handle(ctx context.Context, event *larkim.P2MessageReceiveV1, chatType string, size *int, args ...string) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	if h == nil || h.accessorBuilder == nil {
		return nil
	}

	chatID := chatEntryChatID(event)
	openID := chatEntryOpenID(event)
	accessor := h.accessorBuilder(ctx, chatID, openID)
	if accessor == nil {
		return nil
	}

	if h.mentionChecker != nil && !h.mentionChecker(event) {
		muted, muteErr := h.muteChecker(ctx, chatID)
		if muteErr != nil {
			return muteErr
		}
		if muted {
			return nil
		}
	}

	files, err := h.fileCollector(ctx, event)
	if err != nil {
		return err
	}

	responseMode := resolveChatResponseMode(accessor.ChatMode(), chatType)
	runtimeEnabled := accessor.AgentRuntimeEnabled()
	cutoverEnabled := accessor.AgentRuntimeChatCutover()
	req := ChatResponseRequest{
		Event:          event,
		Plan:           buildChatGenerationPlan(resolveChatModelID(accessor, chatType), resolvePlanChatMode(responseMode), size, files, args, responseMode == chatResponseModeAgentic && runtimeEnabled && cutoverEnabled),
		RuntimeEnabled: runtimeEnabled,
		CutoverEnabled: cutoverEnabled,
		StartedAt:      h.resolveStartedAt(),
	}
	if ownership, ok := InitialRunOwnershipFromContext(ctx); ok {
		req.Ownership = ownership
	}

	if responseMode == chatResponseModeAgentic {
		if h.agenticResponder == nil {
			return nil
		}
		return h.agenticResponder(ctx, req)
	}
	if h.standardResponder == nil {
		return nil
	}
	return h.standardResponder(ctx, req)
}

func (h *ChatEntryHandler) resolveStartedAt() time.Time {
	if h != nil && h.now != nil {
		return h.now().UTC()
	}
	return defaultChatEntryNow()
}

func buildChatGenerationPlan(modelID string, mode appconfig.ChatMode, size *int, files []string, args []string, enableDeferredToolCollector bool) ChatGenerationPlan {
	plan := ChatGenerationPlan{
		ModelID:                     modelID,
		Mode:                        mode.Normalize(),
		Size:                        20,
		Files:                       append([]string(nil), files...),
		Args:                        append([]string(nil), args...),
		EnableDeferredToolCollector: enableDeferredToolCollector,
	}
	if size != nil {
		plan.Size = *size
	}
	return plan
}

func resolvePlanChatMode(mode chatResponseMode) appconfig.ChatMode {
	switch mode {
	case chatResponseModeAgentic:
		return appconfig.ChatModeAgentic
	default:
		return appconfig.ChatModeStandard
	}
}

func resolveChatResponseMode(mode appconfig.ChatMode, chatType string) chatResponseMode {
	if strings.EqualFold(strings.TrimSpace(chatType), chatEntryModelTypeReason) {
		return chatResponseModeAgentic
	}
	if mode.Normalize() == appconfig.ChatModeAgentic {
		return chatResponseModeAgentic
	}
	return chatResponseModeStandard
}

func resolveChatModelID(accessor chatEntryConfigAccessor, chatType string) string {
	if accessor == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(chatType), chatEntryModelTypeReason) {
		return accessor.ChatReasoningModel()
	}
	return accessor.ChatNormalModel()
}

func defaultChatEntryNow() time.Time {
	return time.Now().UTC()
}

func defaultMentionChecker(event *larkim.P2MessageReceiveV1) bool {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return false
	}
	return larkmsg.IsMentioned(event.Event.Message.Mentions)
}

func defaultMuteChecker(ctx context.Context, chatID string) (bool, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false, nil
	}
	client := redis_dal.GetRedisClient()
	if client == nil {
		return false, nil
	}
	exists, err := client.Exists(ctx, mutestate.RedisKey(chatID)).Result()
	if err != nil {
		return false, err
	}
	return exists != 0, nil
}

func defaultFileCollector(ctx context.Context, event *larkim.P2MessageReceiveV1) ([]string, error) {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil, nil
	}
	files := make([]string, 0)
	if event.Event.Message.MessageId != nil {
		urlSeq, err := larkimg.GetAllImgURLFromMsg(ctx, *event.Event.Message.MessageId)
		if err != nil {
			return nil, err
		}
		files = append(files, collectChatEntryFiles(urlSeq)...)
	}
	urlSeq, err := larkimg.GetAllImgURLFromParent(ctx, event)
	if err != nil {
		return nil, err
	}
	files = append(files, collectChatEntryFiles(urlSeq)...)
	return files, nil
}

func collectChatEntryFiles(seq iter.Seq[string]) []string {
	if seq == nil {
		return nil
	}
	files := make([]string, 0)
	for item := range seq {
		files = append(files, item)
	}
	return files
}

func chatEntryChatID(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.ChatId == nil {
		return ""
	}
	return *event.Event.Message.ChatId
}

func chatEntryOpenID(event *larkim.P2MessageReceiveV1) string {
	return botidentity.MessageSenderOpenID(event)
}

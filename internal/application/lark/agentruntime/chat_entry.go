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

const agenticChatEntryModelTypeReason = "reason"

type agenticChatEntryConfigAccessor interface {
	ChatReasoningModel() string
	ChatNormalModel() string
}

type ChatResponseRequest struct {
	Event     *larkim.P2MessageReceiveV1
	Plan      ChatGenerationPlan
	StartedAt time.Time
	Ownership InitialRunOwnership
}

type AgenticChatEntryHandler struct {
	now             func() time.Time
	accessorBuilder func(context.Context, string, string) agenticChatEntryConfigAccessor
	mentionChecker  func(*larkim.P2MessageReceiveV1) bool
	muteChecker     func(context.Context, string) (bool, error)
	fileCollector   func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error)
}

func NewAgenticChatEntryHandler() *AgenticChatEntryHandler {
	return &AgenticChatEntryHandler{
		now: defaultChatEntryNow,
		accessorBuilder: func(ctx context.Context, chatID, openID string) agenticChatEntryConfigAccessor {
			return appconfig.NewAccessor(ctx, chatID, openID)
		},
		mentionChecker: defaultMentionChecker,
		muteChecker:    defaultMuteChecker,
		fileCollector:  defaultFileCollector,
	}
}

func (h *AgenticChatEntryHandler) Handle(ctx context.Context, event *larkim.P2MessageReceiveV1, chatType string, size *int, args ...string) (err error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	defer func() { otel.RecordError(span, err) }()

	req, err := h.buildRequest(ctx, event, chatType, size, args...)
	if err != nil || req == nil {
		return err
	}
	return handleAgenticChatResponse(ctx, req.Event, req.Plan, req.StartedAt, req.Ownership)
}

func (h *AgenticChatEntryHandler) buildRequest(ctx context.Context, event *larkim.P2MessageReceiveV1, chatType string, size *int, args ...string) (*ChatResponseRequest, error) {
	if h == nil || h.accessorBuilder == nil {
		return nil, nil
	}

	chatID := chatEntryChatID(event)
	openID := chatEntryOpenID(event)
	accessor := h.accessorBuilder(ctx, chatID, openID)
	if accessor == nil {
		return nil, nil
	}

	if h.mentionChecker != nil && !h.mentionChecker(event) {
		muted, muteErr := h.muteChecker(ctx, chatID)
		if muteErr != nil {
			return nil, muteErr
		}
		if muted {
			return nil, nil
		}
	}

	files, err := h.fileCollector(ctx, event)
	if err != nil {
		return nil, err
	}

	req := ChatResponseRequest{
		Event:     event,
		Plan:      buildChatGenerationPlan(resolveChatModelID(accessor, chatType), appconfig.ChatModeAgentic, size, files, args, true),
		StartedAt: h.resolveStartedAt(),
	}
	if ownership, ok := InitialRunOwnershipFromContext(ctx); ok {
		req.Ownership = ownership
	}
	return &req, nil
}

func (h *AgenticChatEntryHandler) resolveStartedAt() time.Time {
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

func resolveChatModelID(accessor agenticChatEntryConfigAccessor, chatType string) string {
	if accessor == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(chatType), agenticChatEntryModelTypeReason) {
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

package chatflow

import (
	"context"
	"iter"
	"strings"
	"time"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/intent"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/mutestate"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkimg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

const AgenticChatEntryModelTypeReason = "reason"

// ConfigAccessor defines a chat flow contract.
type ConfigAccessor interface {
	ChatReasoningModel() string
	ChatNormalModel() string
}

// Request carries chat flow state.
type Request struct {
	Event     *larkim.P2MessageReceiveV1
	Plan      Plan
	StartedAt time.Time
}

// AgenticEntryHandler carries chat flow state.
type AgenticEntryHandler struct {
	now             func() time.Time
	accessorBuilder func(context.Context, string, string) ConfigAccessor
	mentionChecker  func(*larkim.P2MessageReceiveV1) bool
	muteChecker     func(context.Context, string) (bool, error)
	fileCollector   func(context.Context, *larkim.P2MessageReceiveV1) ([]string, error)
}

// NewAgenticEntryHandler implements chat flow behavior.
func NewAgenticEntryHandler() *AgenticEntryHandler {
	return &AgenticEntryHandler{
		now: DefaultNow,
		accessorBuilder: func(ctx context.Context, chatID, openID string) ConfigAccessor {
			return appconfig.NewAccessor(ctx, chatID, openID)
		},
		mentionChecker: DefaultMentionChecker,
		muteChecker:    DefaultMuteChecker,
		fileCollector:  DefaultFileCollector,
	}
}

// BuildRequest implements chat flow behavior.
func (h *AgenticEntryHandler) BuildRequest(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
	meta *xhandler.BaseMetaData,
	chatType string,
	size *int,
	args ...string,
) (*Request, error) {
	ctx, span := otel.Start(ctx)
	defer span.End()
	
	if h == nil || h.accessorBuilder == nil {
		return nil, nil
	}

	chatID := ChatID(event)
	openID := OpenID(event)
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

	return &Request{
		Event: event,
		Plan: BuildPlan(
			ResolveModelID(accessor, chatType),
			appconfig.ChatModeAgentic,
			ResolveReasoningEffort(meta),
			size,
			files,
			args,
			true,
		),
		StartedAt: h.resolveStartedAt(),
	}, nil
}

func (h *AgenticEntryHandler) resolveStartedAt() time.Time {
	if h != nil && h.now != nil {
		return h.now().UTC()
	}
	return DefaultNow()
}

// BuildPlan implements chat flow behavior.
func BuildPlan(
	modelID string,
	mode appconfig.ChatMode,
	reasoningEffort responses.ReasoningEffort_Enum,
	size *int,
	files []string,
	args []string,
	enableDeferredToolCollector bool,
) Plan {
	plan := Plan{
		ModelID:                     modelID,
		Mode:                        mode.Normalize(),
		ReasoningEffort:             reasoningEffort,
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

// ResolveReasoningEffort implements chat flow behavior.
func ResolveReasoningEffort(meta *xhandler.BaseMetaData) responses.ReasoningEffort_Enum {
	if meta != nil {
		if analysis, ok := meta.GetIntentAnalysis(); ok && analysis != nil {
			return intent.NormalizeReasoningEffort(analysis.ReasoningEffort, analysis.InteractionMode)
		}
	}
	return intent.DefaultReasoningEffort(intent.InteractionModeAgentic)
}

// ResolveModelID implements chat flow behavior.
func ResolveModelID(accessor ConfigAccessor, chatType string) string {
	if accessor == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(chatType), AgenticChatEntryModelTypeReason) {
		return accessor.ChatReasoningModel()
	}
	return accessor.ChatNormalModel()
}

// DefaultNow implements chat flow behavior.
func DefaultNow() time.Time {
	return time.Now().UTC()
}

// DefaultMentionChecker implements chat flow behavior.
func DefaultMentionChecker(event *larkim.P2MessageReceiveV1) bool {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return false
	}
	return larkmsg.IsMentioned(event.Event.Message.Mentions)
}

// DefaultMuteChecker implements chat flow behavior.
func DefaultMuteChecker(ctx context.Context, chatID string) (bool, error) {
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

// DefaultFileCollector implements chat flow behavior.
func DefaultFileCollector(ctx context.Context, event *larkim.P2MessageReceiveV1) ([]string, error) {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil, nil
	}
	files := make([]string, 0)
	if event.Event.Message.MessageId != nil {
		urlSeq, err := larkimg.GetAllImgURLFromMsg(ctx, *event.Event.Message.MessageId)
		if err != nil {
			return nil, err
		}
		files = append(files, collectFiles(urlSeq)...)
	}
	urlSeq, err := larkimg.GetAllImgURLFromParent(ctx, event)
	if err != nil {
		return nil, err
	}
	files = append(files, collectFiles(urlSeq)...)
	return files, nil
}

func collectFiles(seq iter.Seq[string]) []string {
	if seq == nil {
		return nil
	}
	files := make([]string, 0)
	for item := range seq {
		files = append(files, item)
	}
	return files
}

// ChatID implements chat flow behavior.
func ChatID(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.ChatId == nil {
		return ""
	}
	return *event.Event.Message.ChatId
}

// OpenID implements chat flow behavior.
func OpenID(event *larkim.P2MessageReceiveV1) string {
	return botidentity.MessageSenderOpenID(event)
}

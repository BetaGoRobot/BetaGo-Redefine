package reaction

import (
	"context"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkchat"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/larkeventstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type ProcessorOptions struct {
	FeedbackSink conversationeval.FeedbackSink
}

// Handler 反应处理器模板（保留用于向后兼容）
var Handler *xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]

type (
	OpBase = xhandler.OperatorBase[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]
	Op     = xhandler.Operator[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]
)

func NewReactionProcessor() *xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData] {
	return NewReactionProcessorWithOptions(ProcessorOptions{})
}

func NewReactionProcessorWithOptions(
	options ProcessorOptions,
) *xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData] {
	return (&xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]{}).
		OnPanic(func(ctx context.Context, err error, event *larkim.P2MessageReactionCreatedV1, metaData *xhandler.BaseMetaData) {
			larkmsg.SendRecoveredMsg(ctx, err, *event.Event.MessageId)
		}).
		WithMetaDataProcess(metaInit).
		WithPreRun(func(p *xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]) {
			prepareReaction(p.Context, p.Data(), p.MetaData(), larkeventstore.Default(), larkeventstore.CurrentScope(), larkmsg.GetChatIDFromMsgID, larkchat.GetChatName)
		}).
		AddAsync(&FollowReactionOperator{}).
		AddAsync(&RecordReactionOperator{feedbackSink: options.FeedbackSink})
}

func metaInit(event *larkim.P2MessageReactionCreatedV1) *xhandler.BaseMetaData {
	return &xhandler.BaseMetaData{
		OpenID:   botidentity.ReactionOpenID(event),
		ChatName: "unknown",
	}
}

func prepareReaction(ctx context.Context, event *larkim.P2MessageReactionCreatedV1, meta *xhandler.BaseMetaData, recorder *larkeventstore.Recorder, scope larkeventstore.Scope, resolve func(context.Context, string) (string, error), chatName func(context.Context, string) string) {
	if event == nil || event.Event == nil || event.Event.MessageId == nil || meta == nil {
		return
	}
	ctx, span := otel.StartNamed(ctx, "lark.reaction.resolve")
	defer span.End()
	larkeventstore.AnnotateReaction(ctx, event)
	result := recorder.Resolve(ctx, scope, *event.Event.MessageId, resolve)
	meta.ChatID = result.ChatID
	if result.ChatID != "" {
		meta.ChatName = result.ChatID
		nameCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if chatName != nil {
			if name := chatName(nameCtx, result.ChatID); name != "" {
				meta.ChatName = name
			}
		}
		cancel()
	}
	recorder.RecordResolution(ctx, scope, larkeventstore.ReactionID(ctx, scope, event), result)
}

// ResolveChatNameForReaction resolves the chat name from a message ID for reaction events.
func ResolveChatNameForReaction(ctx context.Context, msgID string) string {
	chatID, err := larkmsg.GetChatIDFromMsgID(ctx, msgID)
	if err != nil || chatID == "" {
		return "unknown"
	}
	if name := larkchat.GetChatName(ctx, chatID); name != "" {
		return name
	}
	return "unknown"
}

func init() {
	Handler = NewReactionProcessor()
}

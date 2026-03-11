package lark

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/messages"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/reaction"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/otel"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkapplication "github.com/larksuite/oapi-sdk-go/v3/service/application/v6"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// taskSubmitter 是入口处理器向受控后台执行器投递任务所需的最小契约。
type taskSubmitter interface {
	Submit(context.Context, string, func(context.Context) error) error
}

// HandlerSet 是当前服务消费的所有 Lark websocket / callback 事件的传输层入口。
//
// 这轮运行时改造里最关键的变化是：消息和 reaction 不再默认可以无限制
// 起 goroutine，而是可以挂到有界执行器上，在入口边界统一施加队列上限
// 和超时约束。
type HandlerSet struct {
	messageProcessor  *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]
	reactionProcessor *xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]
	messageExecutor   taskSubmitter
	reactionExecutor  taskSubmitter
}

var (
	defaultHandlersMu sync.RWMutex
	defaultHandlers   = NewHandlerSet(HandlerSetOptions{})

	buildCardActionMetaData = xhandler.NewBaseMetaDataWithChatIDOpenID
	recordCardAction        = larkmsg.RecordCardAction2Opensearch
)

// HandlerSetOptions 允许测试代码和运行时装配代码注入实际要使用的
// processor / executor 组合。
type HandlerSetOptions struct {
	MessageProcessor  *xhandler.Processor[larkim.P2MessageReceiveV1, xhandler.BaseMetaData]
	ReactionProcessor *xhandler.Processor[larkim.P2MessageReactionCreatedV1, xhandler.BaseMetaData]
	MessageExecutor   taskSubmitter
	ReactionExecutor  taskSubmitter
}

// NewHandlerSet 构造一个 handler 集合。某个 processor 如果没有显式传入，
// 就回退到包级默认 handler，以兼容旧调用路径。
func NewHandlerSet(options HandlerSetOptions) *HandlerSet {
	handlerSet := &HandlerSet{
		messageProcessor:  messages.Handler,
		reactionProcessor: reaction.Handler,
		messageExecutor:   options.MessageExecutor,
		reactionExecutor:  options.ReactionExecutor,
	}
	if options.MessageProcessor != nil {
		handlerSet.messageProcessor = options.MessageProcessor
	}
	if options.ReactionProcessor != nil {
		handlerSet.reactionProcessor = options.ReactionProcessor
	}
	return handlerSet
}

// SetDefaultHandlerSet 替换下面这些包级转发函数实际使用的 handler facade。
// 运行时装配根会在启动阶段调用它。
func SetDefaultHandlerSet(handlerSet *HandlerSet) {
	if handlerSet == nil {
		return
	}
	defaultHandlersMu.Lock()
	defer defaultHandlersMu.Unlock()
	defaultHandlers = handlerSet
}

// getDefaultHandlerSet 返回旧 dispatcher 接线仍在使用的全局 facade。
func getDefaultHandlerSet() *HandlerSet {
	defaultHandlersMu.RLock()
	defer defaultHandlersMu.RUnlock()
	return defaultHandlers
}

// isOutDated 用来过滤明显过期的事件。这类事件常见于重连或重复投递后。
// 当前策略是在入口处忽略 10 秒前的消息。
func isOutDated(createTime string) bool {
	stamp, err := strconv.ParseInt(createTime, 10, 64)
	if err != nil {
		panic(err)
	}
	return time.Now().Sub(time.UnixMilli(stamp)) > time.Second*10
}

// submitMessageProcessor 把 request/response 风格的 websocket 回调桥接到
// 更长生命周期的消息处理流水线上。
//
// 这里刻意使用 context.WithoutCancel：一旦 Lark 入口已经接受这条事件，
// 后续业务处理就应该由执行器超时控制，而不是被短生命周期的回调上下文
// 直接打断，避免业务副作用做到一半被静默取消。
func (h *HandlerSet) submitMessageProcessor(ctx context.Context, msgID string, event *larkim.P2MessageReceiveV1) error {
	if h == nil {
		return nil
	}
	backgroundCtx := context.WithoutCancel(ctx)
	if h.messageProcessor == nil {
		return nil
	}
	run := func(taskCtx context.Context) error {
		h.messageProcessor.NewExecution().WithCtx(taskCtx).WithData(event).Run()
		return nil
	}
	if h.messageExecutor == nil {
		// nil executor 只作为遗留路径和测试回退。生产装配应始终提供受控执行器。
		go func() {
			_ = run(backgroundCtx)
		}()
		return nil
	}
	return h.messageExecutor.Submit(backgroundCtx, "message:"+msgID, run)
}

// submitReactionProcessor 对 reaction 事件应用同样的有界入口策略。
func (h *HandlerSet) submitReactionProcessor(ctx context.Context, msgID string, event *larkim.P2MessageReactionCreatedV1) error {
	if h == nil || h.reactionProcessor == nil {
		return nil
	}
	backgroundCtx := context.WithoutCancel(ctx)
	run := func(taskCtx context.Context) error {
		h.reactionProcessor.NewExecution().WithCtx(taskCtx).WithData(event).Run()
		return nil
	}
	if h.reactionExecutor == nil {
		go func() {
			_ = run(backgroundCtx)
		}()
		return nil
	}
	return h.reactionExecutor.Submit(backgroundCtx, "reaction:"+msgID, run)
}

// MessageV2Handler 是聊天消息进入系统后的主 websocket 入口。
// 它只做传输层边界检查，真正的重活交给挂了执行器的 processor 流水线。
func (h *HandlerSet) MessageV2Handler(ctx context.Context, event *larkim.P2MessageReceiveV1) (err error) {
	ctx, span := otel.StartEntry(ctx, "lark.message.handle")
	defer larkmsg.RecoverMsg(ctx, *event.Event.Message.MessageId)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	span.SetAttributes(
		attribute.String("message.id", utils.AddrOrNil(event.Event.Message.MessageId)),
		attribute.String("chat.id", utils.AddrOrNil(event.Event.Message.ChatId)),
		attribute.String("chat.type", utils.AddrOrNil(event.Event.Message.ChatType)),
		attribute.String("sender.open_id", utils.AddrOrNil(event.Event.Sender.SenderId.OpenId)),
	)
	senderOpenID := botidentity.MessageSenderOpenID(event)

	if isOutDated(*event.Event.Message.CreateTime) {
		span.AddEvent("message.skipped", trace.WithAttributes(attribute.String("reason", "outdated")))
		return nil
	}
	// 忽略机器人自己发出的消息，避免形成回声循环。
	if current := botidentity.Current(); senderOpenID != "" && current.BotOpenID != "" && senderOpenID == current.BotOpenID {
		span.AddEvent("message.skipped", trace.WithAttributes(attribute.String("reason", "self_message")))
		return nil
	}
	msgID := utils.AddrOrNil(event.Event.Message.MessageId)
	err = h.submitMessageProcessor(ctx, msgID, event)
	if err != nil {
		logs.L().Ctx(ctx).Error("submit message processor failed", zap.String("msg_id", msgID), zap.Error(err))
		return err
	}

	return nil
}

// MessageReactionHandler 通过受控 reaction 执行器处理表情 / reaction 事件。
func (h *HandlerSet) MessageReactionHandler(ctx context.Context, event *larkim.P2MessageReactionCreatedV1) (err error) {
	ctx, span := otel.StartEntry(ctx, "lark.reaction.handle")
	defer larkmsg.RecoverMsg(ctx, *event.Event.MessageId)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	span.SetAttributes(
		attribute.String("message.id", utils.AddrOrNil(event.Event.MessageId)),
		attribute.String("reaction.type", fmt.Sprintf("%v", event.Event.ReactionType)),
	)

	msgID := utils.AddrOrNil(event.Event.MessageId)
	err = h.submitReactionProcessor(ctx, msgID, event)
	if err != nil {
		logs.L().Ctx(ctx).Error("submit reaction processor failed", zap.String("msg_id", msgID), zap.Error(err))
	}
	return nil
}

// CardActionHandler 目前仍保持同步处理，因为 Lark 回调方期待立即拿到响应
// 载荷。审计写入仍然是 best-effort 异步，这也是当前执行器模型之外的
// 一个已知例外。
func (h *HandlerSet) CardActionHandler(ctx context.Context, cardAction *callback.CardActionTriggerEvent) (resp *callback.CardActionTriggerResponse, err error) {
	ctx, span := otel.StartEntry(ctx, "lark.card_action.handle")
	defer larkmsg.RecoverMsg(ctx, cardAction.Event.Context.OpenMessageID)
	defer span.End()
	defer otel.RecordErrorPtr(span, &err)
	span.SetAttributes(
		attribute.String("message.id", cardAction.Event.Context.OpenMessageID),
		attribute.String("chat.id", cardAction.Event.Context.OpenChatID),
		attribute.String("operator.open_id", cardAction.Event.Operator.OpenID),
	)
	metaData := buildCardActionMetaData(ctx, cardAction.Event.Context.OpenChatID, cardAction.Event.Operator.OpenID)
	// 记录一下操作记录
	defer func() { go recordCardAction(ctx, cardAction) }()
	appcardaction.RegisterBuiltins()
	resp, dispatchErr := appcardaction.Dispatch(ctx, cardAction, metaData)
	if dispatchErr != nil {
		logs.L().Ctx(ctx).Warn("dispatch card action failed", zap.Error(dispatchErr))
		return nil, nil
	}
	return resp, nil
}

// AuditV6Handler 目前只是一个占位实现，用来满足 dispatcher 接线。
func (h *HandlerSet) AuditV6Handler(ctx context.Context, event *larkapplication.P2ApplicationAppVersionAuditV6) (err error) {
	return
}

// MessageV2Handler 转发到运行时当前配置好的默认 handler set。
func MessageV2Handler(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	return getDefaultHandlerSet().MessageV2Handler(ctx, event)
}

// MessageReactionHandler 转发到运行时当前配置好的默认 handler set。
func MessageReactionHandler(ctx context.Context, event *larkim.P2MessageReactionCreatedV1) error {
	return getDefaultHandlerSet().MessageReactionHandler(ctx, event)
}

// CardActionHandler 转发到运行时当前配置好的默认 handler set。
func CardActionHandler(ctx context.Context, cardAction *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	return getDefaultHandlerSet().CardActionHandler(ctx, cardAction)
}

// AuditV6Handler 转发到运行时当前配置好的默认 handler set。
func AuditV6Handler(ctx context.Context, event *larkapplication.P2ApplicationAppVersionAuditV6) error {
	return getDefaultHandlerSet().AuditV6Handler(ctx, event)
}

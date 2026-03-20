package runtimecutover

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/runtimewire"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type StandardHandler struct {
	now              func() time.Time
	processorBuilder func(context.Context, agentruntime.InitialReplyEmitter) runProcessor
	output           *replyOrchestrator
	replySender      func(context.Context, *larkim.EventMessage, string) (string, error)
}

func BuildDefaultStandardHandler(context.Context) agentruntime.RuntimeStandardCutoverHandler {
	return &StandardHandler{
		now: func() time.Time { return time.Now().UTC() },
		processorBuilder: func(ctx context.Context, emitter agentruntime.InitialReplyEmitter) runProcessor {
			return runtimewire.BuildRunProcessor(ctx, emitter)
		},
		output: &replyOrchestrator{
			standardSender:  sendStandardReply,
			standardPatcher: larkmsg.PatchTextMessage,
		},
		replySender: sendStandardReply,
	}
}

func (h *StandardHandler) Handle(ctx context.Context, req agentruntime.RuntimeStandardCutoverRequest) error {
	if req.Event == nil || req.Event.Event == nil || req.Event.Event.Message == nil {
		return fmt.Errorf("runtime standard cutover event is required")
	}
	if h == nil || h.replySender == nil {
		return fmt.Errorf("runtime standard cutover reply sender is not configured")
	}

	output := h.outputOrchestrator()
	processor := h.buildProcessor(ctx, output)
	initial := agentruntime.InitialRunInput{
		Start: agentruntime.StartShadowRunRequest{
			ChatID:           messageChatID(req.Event),
			ActorOpenID:      resolveActorOpenID(req.Event),
			TriggerType:      resolveInitialTriggerType(ctx, req.Event, req.Ownership),
			TriggerMessageID: messageID(req.Event),
			AttachToRunID:    strings.TrimSpace(req.Ownership.AttachToRunID),
			SupersedeRunID:   strings.TrimSpace(req.Ownership.SupersedeRunID),
			InputText:        resolveStandardInputText(ctx, req),
			Now:              h.resolveStartedAt(req.StartedAt),
		},
		Event:      req.Event,
		Plan:       req.Plan,
		OutputMode: agentruntime.InitialReplyOutputModeStandard,
	}
	if processor == nil {
		executor, err := initial.BuildExecutor(output)
		if err != nil {
			return err
		}
		_, err = executor.ProduceInitialReply(ctx)
		return err
	}

	return processor.ProcessRun(ctx, agentruntime.RunProcessorInput{
		Initial: &initial,
	})
}

func (h *StandardHandler) buildProcessor(ctx context.Context, emitter agentruntime.InitialReplyEmitter) runProcessor {
	if h != nil && h.processorBuilder != nil {
		return h.processorBuilder(ctx, emitter)
	}
	return nil
}

func (h *StandardHandler) resolveStartedAt(v time.Time) time.Time {
	if !v.IsZero() {
		return v.UTC()
	}
	if h != nil && h.now != nil {
		return h.now().UTC()
	}
	return time.Now().UTC()
}

func (h *StandardHandler) outputOrchestrator() *replyOrchestrator {
	if h != nil && h.output != nil {
		return h.output
	}
	if h != nil && h.replySender != nil {
		return &replyOrchestrator{
			standardSender:  h.replySender,
			standardPatcher: larkmsg.PatchTextMessage,
		}
	}
	return nil
}

func resolveStandardInputText(ctx context.Context, req agentruntime.RuntimeStandardCutoverRequest) string {
	if input := resolveArgsInput(req.Plan.Args); input != "" {
		return input
	}
	return extractEventText(ctx, req.Event)
}

func resolveActorOpenID(event *larkim.P2MessageReceiveV1) string {
	if event == nil || event.Event == nil || event.Event.Sender == nil || event.Event.Sender.SenderId == nil || event.Event.Sender.SenderId.OpenId == nil {
		return ""
	}
	return *event.Event.Sender.SenderId.OpenId
}

func resolveArgsInput(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(args, " "))
}

func sendStandardReply(ctx context.Context, msg *larkim.EventMessage, replyText string) (string, error) {
	if msg == nil || msg.MessageId == nil {
		return "", nil
	}
	resp, err := larkmsg.ReplyMsgText(ctx, replyText, *msg.MessageId, "_chat_random", false)
	if err != nil {
		return "", err
	}
	if resp != nil && resp.Data != nil && resp.Data.MessageId != nil {
		return *resp.Data.MessageId, nil
	}
	return "", nil
}

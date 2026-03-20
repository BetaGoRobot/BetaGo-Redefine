package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type InitialReplyExecutor interface {
	ProduceInitialReply(context.Context) (InitialReplyResult, error)
}

type InitialRunInput struct {
	Start      StartShadowRunRequest
	Event      *larkim.P2MessageReceiveV1 `json:"event,omitempty"`
	Plan       ChatGenerationPlan         `json:"plan,omitempty"`
	OutputMode InitialReplyOutputMode     `json:"output_mode,omitempty"`
}

type InitialReplyResult struct {
	CapabilityCalls   []CompletedCapabilityCall `json:"capability_calls,omitempty"`
	PendingCapability *QueuedCapabilityCall     `json:"pending_capability,omitempty"`
	ThoughtText       string                    `json:"thought_text,omitempty"`
	ReplyText         string                    `json:"reply_text,omitempty"`
	ResponseMessageID string                    `json:"response_message_id,omitempty"`
	ResponseCardID    string                    `json:"response_card_id,omitempty"`
	DeliveryMode      ReplyDeliveryMode         `json:"delivery_mode,omitempty"`
	TargetMessageID   string                    `json:"target_message_id,omitempty"`
	TargetCardID      string                    `json:"target_card_id,omitempty"`
}

type RunProcessorInput struct {
	Initial *InitialRunInput `json:"initial,omitempty"`
	Resume  *ResumeEvent     `json:"resume,omitempty"`
}

type RunProcessor interface {
	ProcessRun(context.Context, RunProcessorInput) error
}

func (p *ContinuationProcessor) ProcessRun(ctx context.Context, input RunProcessorInput) error {
	switch {
	case input.Initial != nil && input.Resume != nil:
		return fmt.Errorf("run processor input cannot contain both initial and resume payload")
	case input.Initial != nil:
		return p.processInitialRun(ctx, *input.Initial)
	case input.Resume != nil:
		return p.ProcessResume(ctx, *input.Resume)
	default:
		return fmt.Errorf("run processor input is empty")
	}
}

func (p *ContinuationProcessor) processInitialRun(ctx context.Context, input InitialRunInput) error {
	if p == nil || p.coordinator == nil {
		return nil
	}
	executor, err := input.BuildExecutor(p.initialReplyEmitter)
	if err != nil {
		return err
	}

	run, err := p.coordinator.StartShadowRun(ctx, input.Start)
	if err != nil {
		return err
	}
	if run == nil || run.Status.IsTerminal() {
		return nil
	}
	decideStepIndex := run.CurrentStepIndex

	if target, err := p.resolveReplyTarget(ctx, run); err != nil {
		return err
	} else if target.MessageID != "" || target.CardID != "" {
		ctx = WithInitialReplyTarget(ctx, InitialReplyTarget{
			MessageID: strings.TrimSpace(target.MessageID),
			CardID:    strings.TrimSpace(target.CardID),
		})
	}
	if recorder := newRunInitialCapabilityTraceRecorder(p.coordinator, run, normalizeObservedAt(input.Start.Now)); recorder != nil {
		ctx = WithInitialCapabilityTraceRecorder(ctx, recorder)
	}

	reply, err := executor.ProduceInitialReply(ctx)
	if err != nil {
		_ = p.coordinator.CancelRun(ctx, run.ID, err.Error())
		return err
	}
	if refreshedRun, refreshErr := p.coordinator.runRepo.GetByID(ctx, run.ID); refreshErr == nil && refreshedRun != nil {
		run = refreshedRun
	}
	run, err = p.coordinator.QueuePlanStep(ctx, QueuePlanStepInput{
		RunID:             run.ID,
		Revision:          run.Revision,
		FromStepIndex:     decideStepIndex,
		ThoughtText:       strings.TrimSpace(reply.ThoughtText),
		ReplyText:         strings.TrimSpace(reply.ReplyText),
		PendingCapability: buildPlanPendingCapability(reply.PendingCapability),
		PlannedAt:         normalizeObservedAt(input.Start.Now),
	})
	if err != nil {
		return err
	}

	if reply.PendingCapability == nil {
		_, err = p.coordinator.CompleteRunWithReply(ctx, CompleteRunWithReplyInput{
			RunID:             run.ID,
			Revision:          run.Revision,
			CapabilityCalls:   reply.CapabilityCalls,
			ThoughtText:       strings.TrimSpace(reply.ThoughtText),
			ReplyText:         strings.TrimSpace(reply.ReplyText),
			ResponseMessageID: strings.TrimSpace(reply.ResponseMessageID),
			ResponseCardID:    strings.TrimSpace(reply.ResponseCardID),
			DeliveryMode:      reply.DeliveryMode,
			TargetMessageID:   strings.TrimSpace(reply.TargetMessageID),
			TargetCardID:      strings.TrimSpace(reply.TargetCardID),
			CompletedAt:       normalizeObservedAt(input.Start.Now),
		})
		return err
	}

	continuedRun, err := p.coordinator.ContinueRunWithReply(ctx, ContinueRunWithReplyInput{
		RunID:             run.ID,
		Revision:          run.Revision,
		CapabilityCalls:   reply.CapabilityCalls,
		ThoughtText:       strings.TrimSpace(reply.ThoughtText),
		ReplyText:         strings.TrimSpace(reply.ReplyText),
		ResponseMessageID: strings.TrimSpace(reply.ResponseMessageID),
		ResponseCardID:    strings.TrimSpace(reply.ResponseCardID),
		DeliveryMode:      reply.DeliveryMode,
		TargetMessageID:   strings.TrimSpace(reply.TargetMessageID),
		TargetCardID:      strings.TrimSpace(reply.TargetCardID),
		QueuedCapability:  reply.PendingCapability,
		ContinuedAt:       normalizeObservedAt(input.Start.Now),
	})
	if err != nil {
		return err
	}
	if continuedRun == nil {
		return nil
	}
	return p.processQueuedRun(ctx, continuedRun.ID, normalizeObservedAt(input.Start.Now))
}

func buildPlanPendingCapability(call *QueuedCapabilityCall) *PlanPendingCapability {
	if call == nil {
		return nil
	}

	pending := &PlanPendingCapability{
		CallID:         strings.TrimSpace(call.CallID),
		CapabilityName: strings.TrimSpace(call.CapabilityName),
		Arguments:      strings.TrimSpace(string(call.Input.Request.PayloadJSON)),
	}
	if pending.CallID == "" && pending.CapabilityName == "" && pending.Arguments == "" {
		return nil
	}
	return pending
}

func (input InitialRunInput) BuildExecutor(emitter InitialReplyEmitter) (InitialReplyExecutor, error) {
	if emitter == nil {
		return nil, fmt.Errorf("initial run reply emitter is required")
	}
	if input.Event == nil || input.Event.Event == nil || input.Event.Event.Message == nil {
		return nil, fmt.Errorf("initial run event is required")
	}
	mode := input.OutputMode
	if mode == "" {
		mode = InitialReplyOutputModeAgentic
	}
	return NewDefaultInitialReplyExecutor(mode, input.Event, cloneChatGenerationPlan(input.Plan), emitter), nil
}

func (p *ContinuationProcessor) processQueuedRun(ctx context.Context, runID string, observedAt time.Time) error {
	if p == nil || p.coordinator == nil {
		return nil
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("queued run id is required")
	}

	run, err := p.coordinator.runRepo.GetByID(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil || run.Status.IsTerminal() {
		return nil
	}

	steps, err := p.loadSteps(ctx, run.ID)
	if err != nil {
		return err
	}
	currentStep := findStepByIndex(steps, run.CurrentStepIndex)
	if currentStep == nil {
		return fmt.Errorf("agent runtime current step missing: run_id=%s index=%d", run.ID, run.CurrentStepIndex)
	}
	if currentStep.Kind != StepKindCapabilityCall {
		return nil
	}

	return p.processCapabilityCall(ctx, run, currentStep, ResumeEvent{
		RunID:      run.ID,
		Revision:   run.Revision,
		OccurredAt: normalizeObservedAt(observedAt),
	})
}

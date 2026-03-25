package initialcore

import (
	"iter"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
)

// Delivery names a initial-run core logic type.
type Delivery string

const (
	DeliveryCreate Delivery = "create"
	DeliveryPatch  Delivery = "patch"
	DeliveryReply  Delivery = "reply"
)

// DeliveryRequest carries initial-run core logic state.
type DeliveryRequest struct {
	OutputKind      string
	TargetMode      string
	TargetMessageID string
	TargetCardID    string
	CanPatch        bool
	CanReply        bool
}

// CapturedReply carries initial-run core logic state.
type CapturedReply struct {
	ThoughtText       string
	ReplyText         string
	CapabilityCalls   []CapabilityCall
	PendingCapability *PendingCapability
}

// CapabilityCall carries initial-run core logic state.
type CapabilityCall struct {
	CallID             string
	CapabilityName     string
	Arguments          string
	Output             string
	PreviousResponseID string
}

// PendingCapability carries initial-run core logic state.
type PendingCapability struct {
	CallID             string
	CapabilityName     string
	Arguments          string
	PreviousResponseID string
	Approval           *Approval
	QueueTail          []PendingCapability
}

// Approval carries initial-run core logic state.
type Approval struct {
	Type              string
	Title             string
	Summary           string
	ExpiresAt         time.Time
	ReservationStepID string
	ReservationToken  string
}

const (
	// StartedThought is an exported initial-run core logic constant.
	StartedThought = "slot 已释放，开始执行。"
	// StartedReply is an exported initial-run core logic constant.
	StartedReply = "已开始执行，我先处理这条任务。"
)

// ResolveDelivery implements initial-run core logic behavior.
func ResolveDelivery(req DeliveryRequest) Delivery {
	if normalizeOutputKind(req.OutputKind) == "model_reply" &&
		strings.TrimSpace(req.TargetMode) == "patch" &&
		strings.TrimSpace(req.TargetCardID) != "" &&
		req.CanPatch {
		return DeliveryPatch
	}
	if strings.TrimSpace(req.TargetMode) == "reply" &&
		strings.TrimSpace(req.TargetMessageID) != "" &&
		req.CanReply {
		return DeliveryReply
	}
	return DeliveryCreate
}

// MapSnapshot implements initial-run core logic behavior.
func MapSnapshot(reply ReplySnapshot) CapturedReply {
	result := CapturedReply{
		ThoughtText: strings.TrimSpace(reply.ThoughtText),
		ReplyText:   strings.TrimSpace(reply.ReplyText),
	}
	if len(reply.CapabilityCalls) > 0 {
		result.CapabilityCalls = make([]CapabilityCall, 0, len(reply.CapabilityCalls))
		for _, call := range reply.CapabilityCalls {
			result.CapabilityCalls = append(result.CapabilityCalls, CapabilityCall{
				CallID:             strings.TrimSpace(call.CallID),
				CapabilityName:     strings.TrimSpace(call.CapabilityName),
				Arguments:          strings.TrimSpace(call.Arguments),
				Output:             strings.TrimSpace(call.Output),
				PreviousResponseID: strings.TrimSpace(call.PreviousResponseID),
			})
		}
	}
	result.PendingCapability = mapPending(reply.PendingCapability)
	return result
}

// StartedSeq implements initial-run core logic behavior.
func StartedSeq(thoughtText, replyText string) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		yield(&ark_dal.ModelStreamRespReasoning{
			ContentStruct: ark_dal.ContentStruct{
				Thought: strings.TrimSpace(thoughtText),
				Reply:   strings.TrimSpace(replyText),
			},
		})
	}
}

func mapPending(pending *PendingCapabilitySnapshot) *PendingCapability {
	if pending == nil {
		return nil
	}
	result := &PendingCapability{
		CallID:             strings.TrimSpace(pending.CallID),
		CapabilityName:     strings.TrimSpace(pending.CapabilityName),
		Arguments:          strings.TrimSpace(pending.Arguments),
		PreviousResponseID: strings.TrimSpace(pending.PreviousResponseID),
	}
	if pending.Approval != nil {
		result.Approval = &Approval{
			Type:              strings.TrimSpace(pending.Approval.Type),
			Title:             strings.TrimSpace(pending.Approval.Title),
			Summary:           strings.TrimSpace(pending.Approval.Summary),
			ExpiresAt:         pending.Approval.ExpiresAt.UTC(),
			ReservationStepID: strings.TrimSpace(pending.Approval.ReservationStepID),
			ReservationToken:  strings.TrimSpace(pending.Approval.ReservationToken),
		}
	}
	if len(pending.QueueTail) > 0 {
		result.QueueTail = make([]PendingCapability, 0, len(pending.QueueTail))
		for _, item := range pending.QueueTail {
			mapped := mapPending(&item)
			if mapped == nil {
				continue
			}
			result.QueueTail = append(result.QueueTail, *mapped)
		}
	}
	return result
}

func normalizeOutputKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return "model_reply"
	}
	return kind
}

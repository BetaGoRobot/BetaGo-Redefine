package initialcore

import (
	"iter"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
)

// CompletedCapabilitySnapshot carries initial-run core logic state.
type CompletedCapabilitySnapshot struct {
	CallID             string
	CapabilityName     string
	Arguments          string
	Output             string
	PreviousResponseID string
}

// ApprovalSnapshot carries initial-run core logic state.
type ApprovalSnapshot struct {
	Type              string
	Title             string
	Summary           string
	ExpiresAt         time.Time
	ReservationStepID string
	ReservationToken  string
}

// PendingCapabilitySnapshot carries initial-run core logic state.
type PendingCapabilitySnapshot struct {
	CallID             string
	CapabilityName     string
	Arguments          string
	PreviousResponseID string
	Approval           *ApprovalSnapshot
	QueueTail          []PendingCapabilitySnapshot
}

// ReplySnapshot carries initial-run core logic state.
type ReplySnapshot struct {
	CapabilityCalls   []CompletedCapabilitySnapshot
	PendingCapability *PendingCapabilitySnapshot
	ThoughtText       string
	ReplyText         string
}

// CaptureInitialReplyStream implements initial-run core logic behavior.
func CaptureInitialReplyStream(seq iter.Seq[*ark_dal.ModelStreamRespReasoning]) (iter.Seq[*ark_dal.ModelStreamRespReasoning], func() ReplySnapshot) {
	var thoughtBuilder strings.Builder
	var replyBuilder strings.Builder
	result := ReplySnapshot{}
	seenCapabilityCalls := make(map[string]struct{})

	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
			for item := range seq {
				if item == nil {
					continue
				}
				if trace := item.CapabilityCall; trace != nil {
					if trace.Pending {
						pending := PendingCapabilitySnapshot{
							CallID:             strings.TrimSpace(trace.CallID),
							CapabilityName:     strings.TrimSpace(trace.FunctionName),
							Arguments:          strings.TrimSpace(trace.Arguments),
							PreviousResponseID: strings.TrimSpace(trace.PreviousResponseID),
							Approval:           capturePendingApproval(trace),
						}
						if result.PendingCapability == nil {
							result.PendingCapability = &pending
						} else {
							result.PendingCapability.QueueTail = append(result.PendingCapability.QueueTail, pending)
						}
						if !yield(item) {
							return
						}
						continue
					}
					callID := strings.TrimSpace(trace.CallID)
					completed := CompletedCapabilitySnapshot{
						CallID:             callID,
						CapabilityName:     strings.TrimSpace(trace.FunctionName),
						Arguments:          strings.TrimSpace(trace.Arguments),
						Output:             strings.TrimSpace(trace.Output),
						PreviousResponseID: strings.TrimSpace(trace.PreviousResponseID),
					}
					if callID == "" {
						result.CapabilityCalls = append(result.CapabilityCalls, completed)
					} else if _, exists := seenCapabilityCalls[callID]; !exists {
						seenCapabilityCalls[callID] = struct{}{}
						result.CapabilityCalls = append(result.CapabilityCalls, completed)
					}
				}
				if item.ReasoningContent != "" {
					thoughtBuilder.WriteString(item.ReasoningContent)
					result.ThoughtText = strings.TrimSpace(thoughtBuilder.String())
				}
				if thought := strings.TrimSpace(item.ContentStruct.Thought); thought != "" {
					result.ThoughtText = thought
				}
				if reply := strings.TrimSpace(item.ContentStruct.Reply); reply != "" {
					result.ReplyText = reply
				} else if item.Content != "" {
					replyBuilder.WriteString(item.Content)
					if strings.TrimSpace(result.ReplyText) == "" {
						result.ReplyText = strings.TrimSpace(replyBuilder.String())
					}
				}
				if !yield(item) {
					return
				}
			}
		}, func() ReplySnapshot {
			return ReplySnapshot{
				CapabilityCalls:   append([]CompletedCapabilitySnapshot(nil), result.CapabilityCalls...),
				PendingCapability: clonePendingCapability(result.PendingCapability),
				ThoughtText:       strings.TrimSpace(result.ThoughtText),
				ReplyText:         strings.TrimSpace(result.ReplyText),
			}
		}
}

func capturePendingApproval(trace *ark_dal.CapabilityCallTrace) *ApprovalSnapshot {
	if trace == nil {
		return nil
	}
	if strings.TrimSpace(trace.ApprovalTitle) == "" &&
		strings.TrimSpace(trace.ApprovalSummary) == "" &&
		strings.TrimSpace(trace.ApprovalType) == "" &&
		trace.ApprovalExpiresAt.IsZero() &&
		strings.TrimSpace(trace.ApprovalStepID) == "" &&
		strings.TrimSpace(trace.ApprovalToken) == "" {
		return nil
	}
	return &ApprovalSnapshot{
		Type:              strings.TrimSpace(trace.ApprovalType),
		Title:             strings.TrimSpace(trace.ApprovalTitle),
		Summary:           strings.TrimSpace(trace.ApprovalSummary),
		ExpiresAt:         trace.ApprovalExpiresAt.UTC(),
		ReservationStepID: strings.TrimSpace(trace.ApprovalStepID),
		ReservationToken:  strings.TrimSpace(trace.ApprovalToken),
	}
}

func clonePendingCapability(src *PendingCapabilitySnapshot) *PendingCapabilitySnapshot {
	if src == nil {
		return nil
	}
	copied := *src
	if src.Approval != nil {
		approval := *src.Approval
		copied.Approval = &approval
	}
	if len(src.QueueTail) > 0 {
		copied.QueueTail = clonePendingQueue(src.QueueTail)
	}
	return &copied
}

func clonePendingQueue(src []PendingCapabilitySnapshot) []PendingCapabilitySnapshot {
	if len(src) == 0 {
		return nil
	}
	dst := make([]PendingCapabilitySnapshot, 0, len(src))
	for _, item := range src {
		copied := item
		if item.Approval != nil {
			approval := *item.Approval
			copied.Approval = &approval
		}
		if len(item.QueueTail) > 0 {
			copied.QueueTail = clonePendingQueue(item.QueueTail)
		}
		dst = append(dst, copied)
	}
	return dst
}

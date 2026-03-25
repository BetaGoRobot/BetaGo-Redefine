package initialstate

import capdef "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime/capability"

// CapturedPendingCapability carries initial-run context state state.
type CapturedPendingCapability struct {
	CallID             string                      `json:"call_id,omitempty"`
	CapabilityName     string                      `json:"capability_name,omitempty"`
	Arguments          string                      `json:"arguments,omitempty"`
	PreviousResponseID string                      `json:"previous_response_id,omitempty"`
	Approval           *capdef.ApprovalSpec        `json:"approval,omitempty"`
	QueueTail          []CapturedPendingCapability `json:"queue_tail,omitempty"`
}

// CapturedReply carries initial-run context state state.
type CapturedReply struct {
	CapabilityCalls   []capdef.CompletedCall     `json:"capability_calls,omitempty"`
	PendingCapability *CapturedPendingCapability `json:"pending_capability,omitempty"`
	ThoughtText       string                     `json:"thought_text,omitempty"`
	ReplyText         string                     `json:"reply_text,omitempty"`
}

package agentruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/agentruntime"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func TestLarkApprovalSenderRepliesToTriggerMessage(t *testing.T) {
	now := time.Now().UTC()
	req := agentruntime.ApprovalRequest{
		RunID:          "run_approval",
		StepID:         "step_approval",
		Revision:       4,
		ApprovalType:   "side_effect",
		Title:          "审批发送消息",
		Summary:        "将向群里发送一条消息",
		CapabilityName: "send_message",
		Token:          "approval_token",
		RequestedAt:    now,
		ExpiresAt:      now.Add(30 * time.Minute),
	}

	var replied bool
	sender := agentruntime.NewLarkApprovalSenderForTest(
		func(ctx context.Context, msgID string, cardData any, suffix string, replyInThread bool) error {
			replied = true
			if msgID != "om_trigger" {
				t.Fatalf("reply msg id = %q, want om_trigger", msgID)
			}
			if suffix != "_agent_runtime_approval" {
				t.Fatalf("reply suffix = %q", suffix)
			}
			assertApprovalCardJSON(t, cardData)
			return nil
		},
		func(context.Context, string, string, any, string, string) error {
			t.Fatal("create path should not be used when reply target exists")
			return nil
		},
	)

	err := sender.SendApprovalCard(context.Background(), agentruntime.ApprovalCardTarget{
		ChatID:           "oc_chat",
		ReplyToMessageID: "om_trigger",
	}, req)
	if err != nil {
		t.Fatalf("SendApprovalCard() error = %v", err)
	}
	if !replied {
		t.Fatal("expected reply path to be used")
	}
}

func TestLarkApprovalSenderCreatesCardWhenNoReplyTarget(t *testing.T) {
	now := time.Now().UTC()
	req := agentruntime.ApprovalRequest{
		RunID:          "run_approval",
		StepID:         "step_approval",
		Revision:       4,
		ApprovalType:   "side_effect",
		Title:          "审批发送消息",
		Summary:        "将向群里发送一条消息",
		CapabilityName: "send_message",
		Token:          "approval_token",
		RequestedAt:    now,
		ExpiresAt:      now.Add(30 * time.Minute),
	}

	var created bool
	sender := agentruntime.NewLarkApprovalSenderForTest(
		func(context.Context, string, any, string, bool) error {
			t.Fatal("reply path should not be used when trigger message is absent")
			return nil
		},
		func(ctx context.Context, receiveIDType, receiveID string, cardData any, msgID, suffix string) error {
			created = true
			if receiveIDType != "chat_id" {
				t.Fatalf("receive id type = %q, want chat_id", receiveIDType)
			}
			if receiveID != "oc_chat" {
				t.Fatalf("receive id = %q, want oc_chat", receiveID)
			}
			if suffix != "_agent_runtime_approval" {
				t.Fatalf("create suffix = %q", suffix)
			}
			assertApprovalCardJSON(t, cardData)
			return nil
		},
	)

	err := sender.SendApprovalCard(context.Background(), agentruntime.ApprovalCardTarget{
		ChatID: "oc_chat",
	}, req)
	if err != nil {
		t.Fatalf("SendApprovalCard() error = %v", err)
	}
	if !created {
		t.Fatal("expected create path to be used")
	}
}

func TestLarkApprovalSenderCreatesActorVisibleCardBeforeReplyTarget(t *testing.T) {
	now := time.Now().UTC()
	req := agentruntime.ApprovalRequest{
		RunID:          "run_approval",
		StepID:         "step_approval",
		Revision:       4,
		ApprovalType:   "side_effect",
		Title:          "审批发送消息",
		Summary:        "将向群里发送一条消息",
		CapabilityName: "send_message",
		Token:          "approval_token",
		RequestedAt:    now,
		ExpiresAt:      now.Add(30 * time.Minute),
	}

	var created bool
	sender := agentruntime.NewLarkApprovalSenderForTest(
		func(context.Context, string, any, string, bool) error {
			t.Fatal("reply path should not be used when actor visible target exists")
			return nil
		},
		func(ctx context.Context, receiveIDType, receiveID string, cardData any, msgID, suffix string) error {
			created = true
			if receiveIDType != "open_id" {
				t.Fatalf("receive id type = %q, want open_id", receiveIDType)
			}
			if receiveID != "ou_actor" {
				t.Fatalf("receive id = %q, want ou_actor", receiveID)
			}
			if suffix != "_agent_runtime_approval" {
				t.Fatalf("create suffix = %q", suffix)
			}
			assertApprovalCardJSON(t, cardData)
			return nil
		},
	)

	err := sender.SendApprovalCard(context.Background(), agentruntime.ApprovalCardTarget{
		ChatID:           "oc_chat",
		ReplyToMessageID: "om_trigger",
		VisibleOpenID:    "ou_actor",
	}, req)
	if err != nil {
		t.Fatalf("SendApprovalCard() error = %v", err)
	}
	if !created {
		t.Fatal("expected actor-visible create path to be used")
	}
}

func TestLarkApprovalSenderFallsBackToReplyWhenActorVisibleCreateFails(t *testing.T) {
	now := time.Now().UTC()
	req := agentruntime.ApprovalRequest{
		RunID:          "run_approval",
		StepID:         "step_approval",
		Revision:       4,
		ApprovalType:   "side_effect",
		Title:          "审批发送消息",
		Summary:        "将向群里发送一条消息",
		CapabilityName: "send_message",
		Token:          "approval_token",
		RequestedAt:    now,
		ExpiresAt:      now.Add(30 * time.Minute),
	}

	var replied bool
	sender := agentruntime.NewLarkApprovalSenderForTest(
		func(ctx context.Context, msgID string, cardData any, suffix string, replyInThread bool) error {
			replied = true
			if msgID != "om_trigger" {
				t.Fatalf("reply msg id = %q, want om_trigger", msgID)
			}
			assertApprovalCardJSON(t, cardData)
			return nil
		},
		func(ctx context.Context, receiveIDType, receiveID string, cardData any, msgID, suffix string) error {
			if receiveIDType != "open_id" || receiveID != "ou_actor" {
				t.Fatalf("unexpected actor-visible target: type=%q id=%q", receiveIDType, receiveID)
			}
			return errors.New("cannot send actor-visible card")
		},
	)

	err := sender.SendApprovalCard(context.Background(), agentruntime.ApprovalCardTarget{
		ChatID:           "oc_chat",
		ReplyToMessageID: "om_trigger",
		VisibleOpenID:    "ou_actor",
	}, req)
	if err != nil {
		t.Fatalf("SendApprovalCard() error = %v", err)
	}
	if !replied {
		t.Fatal("expected reply fallback after actor-visible failure")
	}
}

func assertApprovalCardJSON(t *testing.T, cardData any) {
	t.Helper()

	raw, err := json.Marshal(cardData)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	jsonStr := string(raw)

	if !strings.Contains(jsonStr, "审批发送消息") || !strings.Contains(jsonStr, "send_message") {
		t.Fatalf("expected approval title and capability in card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, cardactionproto.ActionAgentRuntimeResume) {
		t.Fatalf("expected approve action in card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, cardactionproto.ActionAgentRuntimeReject) {
		t.Fatalf("expected reject action in card: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"source":"approval"`) {
		t.Fatalf("expected approval source in card payload: %s", jsonStr)
	}
}

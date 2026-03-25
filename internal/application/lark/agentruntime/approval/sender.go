package approval

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const approvalCardSuffix = "_agent_runtime_approval"

// ApprovalCardTarget carries approval flow state.
type ApprovalCardTarget struct {
	ChatID           string `json:"chat_id,omitempty"`
	ReplyToMessageID string `json:"reply_to_message_id,omitempty"`
	ReplyInThread    bool   `json:"reply_in_thread,omitempty"`
	VisibleOpenID    string `json:"visible_open_id,omitempty"`
}

// ApprovalSender defines a approval flow contract.
type ApprovalSender interface {
	SendApprovalCard(context.Context, ApprovalCardTarget, ApprovalRequest) error
}

// ApprovalCardState names a approval flow type.
type ApprovalCardState string

const (
	ApprovalCardStatePending  ApprovalCardState = "pending"
	ApprovalCardStateApproved ApprovalCardState = "approved"
	ApprovalCardStateRejected ApprovalCardState = "rejected"
	ApprovalCardStateExpired  ApprovalCardState = "expired"
)

// LarkApprovalSender carries approval flow state.
type LarkApprovalSender struct {
	replyCardJSON             func(context.Context, string, any, string, bool) error
	createCardJSONByReceiveID func(context.Context, string, string, any, string, string) error
	sendEphemeralCard         func(context.Context, string, string, any) error
}

// NewLarkApprovalSender implements approval flow behavior.
func NewLarkApprovalSender() *LarkApprovalSender {
	return &LarkApprovalSender{
		replyCardJSON:             larkmsg.ReplyCardJSON,
		createCardJSONByReceiveID: larkmsg.CreateCardJSONByReceiveID,
		sendEphemeralCard: func(ctx context.Context, chatID, openID string, cardData any) error {
			_, err := larkmsg.SendEphemeralCard(ctx, chatID, openID, cardData)
			return err
		},
	}
}

// NewLarkApprovalSenderForTest implements approval flow behavior.
func NewLarkApprovalSenderForTest(
	reply func(context.Context, string, any, string, bool) error,
	create func(context.Context, string, string, any, string, string) error,
	sendEphemeral func(context.Context, string, string, any) error,
) *LarkApprovalSender {
	return &LarkApprovalSender{
		replyCardJSON:             reply,
		createCardJSONByReceiveID: create,
		sendEphemeralCard:         sendEphemeral,
	}
}

// SendApprovalCard implements approval flow behavior.
func (s *LarkApprovalSender) SendApprovalCard(ctx context.Context, target ApprovalCardTarget, request ApprovalRequest) error {
	if s == nil {
		return fmt.Errorf("lark approval sender is nil")
	}
	if err := request.Validate(time.Now().UTC()); err != nil {
		return err
	}

	if visibleOpenID := strings.TrimSpace(target.VisibleOpenID); visibleOpenID != "" {
		if s.sendEphemeralCard != nil && strings.TrimSpace(target.ChatID) != "" {
			ephemeralRequest := request
			ephemeralRequest.Delivery = ApprovalCardDeliveryEphemeral
			if err := s.sendEphemeralCard(ctx, strings.TrimSpace(target.ChatID), visibleOpenID, buildApprovalCard(ctx, ephemeralRequest, ApprovalCardStatePending)); err == nil {
				return nil
			}
		}
	}

	messageRequest := request
	messageRequest.Delivery = ApprovalCardDeliveryMessage
	card := buildApprovalCard(ctx, messageRequest, ApprovalCardStatePending)
	if replyTo := strings.TrimSpace(target.ReplyToMessageID); replyTo != "" {
		if s.replyCardJSON == nil {
			return fmt.Errorf("lark approval sender reply function is nil")
		}
		return s.replyCardJSON(ctx, replyTo, card, approvalCardSuffix, target.ReplyInThread)
	}

	chatID := strings.TrimSpace(target.ChatID)
	if chatID == "" {
		return fmt.Errorf("approval card target chat_id is required")
	}
	if s.createCardJSONByReceiveID == nil {
		return fmt.Errorf("lark approval sender create function is nil")
	}
	return s.createCardJSONByReceiveID(ctx, larkim.ReceiveIdTypeChatId, chatID, card, strings.TrimSpace(request.RunID), approvalCardSuffix)
}

// BuildApprovalCard implements approval flow behavior.
func BuildApprovalCard(ctx context.Context, request ApprovalRequest, state ApprovalCardState) larkmsg.RawCard {
	return buildApprovalCard(ctx, request, state)
}

func buildApprovalCard(ctx context.Context, request ApprovalRequest, state ApprovalCardState) larkmsg.RawCard {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "审批请求"
	}

	expireText := "未设置"
	if !request.ExpiresAt.IsZero() {
		expireText = request.ExpiresAt.In(time.FixedZone("UTC+8", 8*3600)).Format(time.DateTime)
	}

	detailLines := make([]string, 0, 4)
	if summary := strings.TrimSpace(request.Summary); summary != "" {
		detailLines = append(detailLines, summary)
	}
	detailLines = append(detailLines,
		fmt.Sprintf("能力：`%s`", fallbackApprovalField(request.CapabilityName, "unknown")),
		fmt.Sprintf("过期：`%s`", expireText),
	)
	if stateText := approvalCardStateText(state); stateText != "" {
		detailLines = append(detailLines, stateText)
	}

	elements := []any{
		larkmsg.CollapsiblePanel(
			"详情",
			[]any{larkmsg.Markdown(strings.Join(detailLines, "\n"))},
			larkmsg.CollapsiblePanelOptions{
				Expanded:        false,
				Padding:         "6px 8px",
				Margin:          "0px",
				VerticalSpacing: "4px",
			},
		),
	}
	if state == ApprovalCardStatePending {
		elements = append(elements,
			larkmsg.ButtonRow("flow",
				larkmsg.Button("批准", larkmsg.ButtonOptions{
					Type:    "primary_filled",
					Payload: larkmsg.StringMapToAnyMap(request.ApprovePayload()),
				}),
				larkmsg.Button("拒绝", larkmsg.ButtonOptions{
					Type:    "danger",
					Payload: larkmsg.StringMapToAnyMap(request.RejectPayload()),
				}),
			),
		)
	}
	return larkmsg.NewStandardPanelCard(ctx, title, elements)
}

func approvalCardStateText(state ApprovalCardState) string {
	switch state {
	case ApprovalCardStateApproved:
		return "状态：已批准，运行将继续执行。"
	case ApprovalCardStateRejected:
		return "状态：已拒绝，运行已取消。"
	case ApprovalCardStateExpired:
		return "状态：已过期，审批请求不再可用。"
	default:
		return "状态：等待审批。"
	}
}

func fallbackApprovalField(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

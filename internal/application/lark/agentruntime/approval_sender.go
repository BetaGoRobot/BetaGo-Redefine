package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const approvalCardSuffix = "_agent_runtime_approval"

type ApprovalCardTarget struct {
	ChatID           string `json:"chat_id,omitempty"`
	ReplyToMessageID string `json:"reply_to_message_id,omitempty"`
	VisibleOpenID    string `json:"visible_open_id,omitempty"`
}

type ApprovalSender interface {
	SendApprovalCard(context.Context, ApprovalCardTarget, ApprovalRequest) error
}

type ApprovalCardState string

const (
	ApprovalCardStatePending  ApprovalCardState = "pending"
	ApprovalCardStateApproved ApprovalCardState = "approved"
	ApprovalCardStateRejected ApprovalCardState = "rejected"
	ApprovalCardStateExpired  ApprovalCardState = "expired"
)

type LarkApprovalSender struct {
	replyCardJSON             func(context.Context, string, any, string, bool) error
	createCardJSONByReceiveID func(context.Context, string, string, any, string, string) error
}

func NewLarkApprovalSender() *LarkApprovalSender {
	return &LarkApprovalSender{
		replyCardJSON:             larkmsg.ReplyCardJSON,
		createCardJSONByReceiveID: larkmsg.CreateCardJSONByReceiveID,
	}
}

func NewLarkApprovalSenderForTest(
	reply func(context.Context, string, any, string, bool) error,
	create func(context.Context, string, string, any, string, string) error,
) *LarkApprovalSender {
	return &LarkApprovalSender{
		replyCardJSON:             reply,
		createCardJSONByReceiveID: create,
	}
}

func (s *LarkApprovalSender) SendApprovalCard(ctx context.Context, target ApprovalCardTarget, request ApprovalRequest) error {
	if s == nil {
		return fmt.Errorf("lark approval sender is nil")
	}
	if err := request.Validate(time.Now().UTC()); err != nil {
		return err
	}

	card := BuildApprovalCard(ctx, request, ApprovalCardStatePending)
	if visibleOpenID := strings.TrimSpace(target.VisibleOpenID); visibleOpenID != "" {
		if s.createCardJSONByReceiveID != nil {
			if err := s.createCardJSONByReceiveID(ctx, larkim.ReceiveIdTypeOpenId, visibleOpenID, card, strings.TrimSpace(request.RunID), approvalCardSuffix); err == nil {
				return nil
			}
		}
	}
	if replyTo := strings.TrimSpace(target.ReplyToMessageID); replyTo != "" {
		if s.replyCardJSON == nil {
			return fmt.Errorf("lark approval sender reply function is nil")
		}
		return s.replyCardJSON(ctx, replyTo, card, approvalCardSuffix, false)
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

func BuildApprovalCard(ctx context.Context, request ApprovalRequest, state ApprovalCardState) larkmsg.RawCard {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "审批请求"
	}

	expireText := "未设置"
	if !request.ExpiresAt.IsZero() {
		expireText = request.ExpiresAt.In(time.FixedZone("UTC+8", 8*3600)).Format(time.DateTime)
	}

	elements := []any{
		larkmsg.HintMarkdown("Agent Runtime 审批请求"),
		larkmsg.Markdown(strings.TrimSpace(request.Summary)),
		larkmsg.SplitColumns(
			[]any{larkmsg.Markdown(strings.Join([]string{
				"**能力**",
				fmt.Sprintf("`%s`", fallbackApprovalField(request.CapabilityName, "unknown")),
			}, "\n"))},
			[]any{larkmsg.Markdown(strings.Join([]string{
				"**过期时间**",
				fmt.Sprintf("`%s`", expireText),
			}, "\n"))},
			larkmsg.SplitColumnsOptions{
				Left: larkmsg.ColumnOptions{
					Weight:        1,
					VerticalAlign: "top",
				},
				Right: larkmsg.ColumnOptions{
					Weight:        1,
					VerticalAlign: "top",
				},
				Row: larkmsg.ColumnSetOptions{
					HorizontalSpacing: "12px",
					FlexMode:          "stretch",
				},
			},
		),
	}
	if stateText := approvalCardStateText(state); stateText != "" {
		elements = append(elements, larkmsg.HintMarkdown(stateText))
	}
	if state == ApprovalCardStatePending {
		elements = append(elements,
			larkmsg.Divider(),
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
		return "状态：等待审批。批准将继续执行，拒绝将取消当前运行。"
	}
}

func fallbackApprovalField(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

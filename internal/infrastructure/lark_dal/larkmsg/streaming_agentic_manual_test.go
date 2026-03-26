package larkmsg

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestManualSendAgenticStreamingCard(t *testing.T) {
	chatID := os.Getenv("BETAGO_MANUAL_CHAT_ID")
	if chatID == "" {
		t.Skip("BETAGO_MANUAL_CHAT_ID is empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	cardID, err := createAgentStreamingCardEntity(ctx, AgentStreamingCardOptions{})
	if err != nil {
		t.Fatalf("createAgentStreamingCardEntity() error = %v", err)
	}

	resp, err := lark_dal.Client().Im.V1.Message.Create(
		ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(larkim.ReceiveIdTypeChatId).
			Body(
				larkim.NewCreateMessageReqBodyBuilder().
					ReceiveId(chatID).
					MsgType(larkim.MsgTypeInteractive).
					Content(larkcard.NewCardEntityContent(cardID).String()).
					Build(),
			).
			Build(),
	)
	if err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}
	if !resp.Success() {
		t.Fatalf("CreateMessage() resp error = %s", resp.Error())
	}

	sequence := 1
	if err := setCardStreamingMode(ctx, cardID, true, sequence); err != nil {
		t.Fatalf("setCardStreamingMode(true) error = %v", err)
	}

	steps := []struct {
		elementID string
		content   string
		wait      time.Duration
	}{
		{
			elementID: agentThoughtElementID,
			content:   formatAgentThoughtContent("再次建立卡片实体\n再次开启 streaming_mode\n再次准备逐步更新元素内容"),
			wait:      500 * time.Millisecond,
		},
		{
			elementID: agentReplyElementID,
			content:   "正在再次推送 agentic 卡片流式更新...",
			wait:      700 * time.Millisecond,
		},
		{
			elementID: agentThoughtElementID,
			content:   formatAgentThoughtContent("思考过程仍然位于卡片最前部\n正文区域仍然独立更新\n这次是重复验证发送"),
			wait:      500 * time.Millisecond,
		},
		{
			elementID: agentReplyElementID,
			content:   "第二次 agentic 卡片发送完成。\n\n- 折叠思考在前\n- 正文在后\n- 通过 CardKit entity + element streaming 更新",
			wait:      0,
		},
	}

	for _, step := range steps {
		sequence++
		if err := updateAgentCardElement(ctx, cardID, step.elementID, step.content, sequence); err != nil {
			t.Fatalf("updateAgentCardElement(%s) error = %v", step.elementID, err)
		}
		if step.wait > 0 {
			time.Sleep(step.wait)
		}
	}

	sequence++
	if err := setCardStreamingMode(ctx, cardID, false, sequence); err != nil {
		t.Fatalf("setCardStreamingMode(false) error = %v", err)
	}

	t.Logf("sent agentic streaming card to chat_id=%s card_id=%s", chatID, cardID)
}

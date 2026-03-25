package larkmsg

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	. "github.com/bytedance/mockey"
	. "github.com/smartystreets/goconvey/convey"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestBuildAgentStreamingCardPlacesThoughtPanelBeforeReply(t *testing.T) {
	card := newAgentStreamingCard(AgentStreamingCardOptions{})
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	jsonStr := string(raw)
	thoughtPanelIdx := strings.Index(jsonStr, `"element_id":"agt_thought_panel"`)
	replyIdx := strings.Index(jsonStr, `"element_id":"agt_reply"`)
	if thoughtPanelIdx < 0 {
		t.Fatalf("expected thought panel element id in card json: %s", jsonStr)
	}
	if replyIdx < 0 {
		t.Fatalf("expected reply element id in card json: %s", jsonStr)
	}
	if thoughtPanelIdx > replyIdx {
		t.Fatalf("expected thought panel before reply element: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"tag":"collapsible_panel"`) || !strings.Contains(jsonStr, `"expanded":false`) {
		t.Fatalf("expected collapsed thought panel in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"element_id":"agt_thought"`) {
		t.Fatalf("expected thought content element id in card json: %s", jsonStr)
	}
}

func TestBuildAgentStreamingCardIncludesMentionBlockWhenConfigured(t *testing.T) {
	card := newAgentStreamingCard(AgentStreamingCardOptions{MentionOpenID: "ou_actor"})
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	jsonStr := string(raw)
	if !strings.Contains(jsonStr, `"element_id":"agt_root_mention"`) {
		t.Fatalf("expected mention element id in card json: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `\u003cat ids=\"ou_actor\"\u003e\u003c/at\u003e`) {
		t.Fatalf("expected mention markup in card json: %s", jsonStr)
	}
}

func TestFormatAgentThoughtContentBuildsQuoteBlock(t *testing.T) {
	got := formatAgentThoughtContent("第一行\n第二行")
	want := "> 第一行\n> 第二行"
	if got != want {
		t.Fatalf("formatAgentThoughtContent() = %q, want %q", got, want)
	}
}

func TestFormatAgentReplyContentPrefersStructuredReply(t *testing.T) {
	got := formatAgentReplyContent(&ark_dal.ModelStreamRespReasoning{
		Content: "ignored raw delta",
		ContentStruct: ark_dal.ContentStruct{
			Reply: "这是正文",
		},
	})
	if got != "这是正文" {
		t.Fatalf("formatAgentReplyContent() = %q, want %q", got, "这是正文")
	}
}

func TestFormatAgentReplyContentFallsBackToRawDelta(t *testing.T) {
	got := formatAgentReplyContent(&ark_dal.ModelStreamRespReasoning{
		Content: "流式正文增量",
	})
	if got != "流式正文增量" {
		t.Fatalf("formatAgentReplyContent() = %q, want %q", got, "流式正文增量")
	}
}

func TestUpdateAgentStreamingCardDoesNotMixRawReplyDeltaIntoThoughtPanel(t *testing.T) {
	originalUpdater := updateAgentCardElementFunc
	defer func() {
		updateAgentCardElementFunc = originalUpdater
	}()

	type updateCall struct {
		elementID string
		content   string
	}
	var updates []updateCall
	updateAgentCardElementFunc = func(ctx context.Context, cardID, elementID, content string, sequence int) error {
		updates = append(updates, updateCall{
			elementID: elementID,
			content:   content,
		})
		return nil
	}

	_, err := updateAgentStreamingCard(context.Background(), "card_test", func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		yield(&ark_dal.ModelStreamRespReasoning{ReasoningContent: "先判断是否需要调用工具"})
		yield(&ark_dal.ModelStreamRespReasoning{Content: `{"thought":"内部","reply":"这是误流出的 JSON"}`})
	}, 1)
	if err != nil {
		t.Fatalf("updateAgentStreamingCard() error = %v", err)
	}

	if len(updates) == 0 {
		t.Fatal("expected at least one card element update")
	}
	if updates[0].elementID != agentThoughtElementID {
		t.Fatalf("first updated element = %q, want %q", updates[0].elementID, agentThoughtElementID)
	}
	if strings.Contains(updates[0].content, "这是误流出的 JSON") {
		t.Fatalf("thought panel content = %q, should not contain raw reply delta", updates[0].content)
	}
}

func TestSendAndUpdateStreamingCardPreservesRefsFromWithRefsVariant(t *testing.T) {
	original := sendAgentStreamingCreateCardFunc
	defer func() {
		sendAgentStreamingCreateCardFunc = original
	}()

	sendAgentStreamingCreateCardFunc = func(ctx context.Context, msg *larkim.EventMessage, seq iter.Seq[*ark_dal.ModelStreamRespReasoning], opts ...AgentStreamingCardOptions) (AgentStreamingCardRefs, error) {
		return AgentStreamingCardRefs{
			MessageID: "om_runtime_reply",
			CardID:    "card_runtime_reply",
		}, nil
	}

	msg := &larkim.EventMessage{}
	refs, err := SendAndUpdateStreamingCardWithRefs(context.Background(), msg, func(func(*ark_dal.ModelStreamRespReasoning) bool) {})
	if err != nil {
		t.Fatalf("SendAndUpdateStreamingCardWithRefs() error = %v", err)
	}
	if refs.MessageID != "om_runtime_reply" || refs.CardID != "card_runtime_reply" {
		t.Fatalf("refs = %+v, want message/card ids", refs)
	}

	if err := SendAndUpdateStreamingCard(context.Background(), msg, func(func(*ark_dal.ModelStreamRespReasoning) bool) {}); err != nil {
		t.Fatalf("SendAndUpdateStreamingCard() error = %v", err)
	}
}

func TestPatchAgentStreamingCardWithRefsPreservesMonotonicSequenceAcrossPatchRounds(t *testing.T) {
	PatchConvey("same card patch rounds should keep sequence increasing", t, func() {
		var settings []int
		var updates []int

		Mock(setCardStreamingMode).To(func(ctx context.Context, cardID string, enabled bool, sequence int) error {
			settings = append(settings, sequence)
			return nil
		}).Build()
		Mock(updateAgentCardElement).To(func(ctx context.Context, cardID, elementID, content string, sequence int) error {
			updates = append(updates, sequence)
			return nil
		}).Build()

		refs := AgentStreamingCardRefs{
			MessageID: "om_pending_root",
			CardID:    fmt.Sprintf("card_pending_root_%d", time.Now().UnixNano()),
		}
		firstRound := func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
			yield(&ark_dal.ModelStreamRespReasoning{
				ReasoningContent: "排队中",
				ContentStruct: ark_dal.ContentStruct{
					Reply: "等待前一个任务完成",
				},
			})
		}
		secondRound := func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
			yield(&ark_dal.ModelStreamRespReasoning{
				ReasoningContent: "开始执行",
				ContentStruct: ark_dal.ContentStruct{
					Reply: "任务已恢复",
				},
			})
		}

		_, err := PatchAgentStreamingCardWithRefs(context.Background(), refs, firstRound)
		So(err, ShouldBeNil)
		_, err = PatchAgentStreamingCardWithRefs(context.Background(), refs, secondRound)
		So(err, ShouldBeNil)

		So(settings, ShouldResemble, []int{1, 4, 5, 8})
		So(updates, ShouldResemble, []int{2, 3, 6, 7})
	})
}

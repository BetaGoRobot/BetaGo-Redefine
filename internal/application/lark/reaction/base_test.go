package reaction

import (
	"context"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/conversationeval"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestMetaInitPrefersOpenID(t *testing.T) {
	openID := "ou_open"
	legacyOpenIDName := "cli_legacy"

	meta := metaInit(&larkim.P2MessageReactionCreatedV1{
		Event: &larkim.P2MessageReactionCreatedV1Data{
			UserId: &larkim.UserId{
				OpenId: &openID,
				UserId: &legacyOpenIDName,
			},
		},
	})

	if meta.OpenID != openID {
		t.Fatalf("metaInit() open id = %q, want %q", meta.OpenID, openID)
	}
}

func TestMetaInitReturnsEmptyWithoutOpenID(t *testing.T) {
	legacyUserID := "cli_legacy"

	meta := metaInit(&larkim.P2MessageReactionCreatedV1{
		Event: &larkim.P2MessageReactionCreatedV1Data{
			UserId: &larkim.UserId{
				UserId: &legacyUserID,
			},
		},
	})

	if meta.OpenID != "" {
		t.Fatalf("metaInit() open id = %q, want empty string when only legacy user id %q exists", meta.OpenID, legacyUserID)
	}
}

func TestReactionOpenIDUsesMetaOpenID(t *testing.T) {
	meta := &xhandler.BaseMetaData{
		OpenID: "ou_open",
	}

	if got := reactionOpenID(nil, meta); got != "ou_open" {
		t.Fatalf("reactionOpenID() = %q, want %q", got, "ou_open")
	}
}

func TestRecordReactionOperatorEmitsFeedback(t *testing.T) {
	messageID := "delivered-message"
	reactionType := "THUMBSUP"
	operatorType := "user"
	actionTime := "1785398400123"
	openID := "ou-feedback"
	sink := &reactionFeedbackSinkFake{}
	operator := &RecordReactionOperator{feedbackSink: sink}
	event := &larkim.P2MessageReactionCreatedV1{
		EventV2Base: &larkevent.EventV2Base{Header: &larkevent.EventHeader{
			EventID: "reaction-event",
		}},
		Event: &larkim.P2MessageReactionCreatedV1Data{
			MessageId: &messageID, OperatorType: &operatorType, ActionTime: &actionTime,
			ReactionType: &larkim.Emoji{EmojiType: &reactionType},
			UserId:       &larkim.UserId{OpenId: &openID},
		},
	}

	if err := operator.observeFeedback(context.Background(), event, "chat-feedback"); err != nil {
		t.Fatalf("observeFeedback() error = %v", err)
	}
	if len(sink.reactions) != 1 {
		t.Fatalf("reaction feedback = %#v, want one item", sink.reactions)
	}
	got := sink.reactions[0]
	if got.EventID != "reaction-event" || got.ChatID != "chat-feedback" ||
		got.TargetMessageID != messageID || got.ActorOpenID != openID ||
		got.ReactionType != reactionType ||
		got.OccurredAt != time.UnixMilli(1785398400123) {
		t.Fatalf("reaction feedback = %#v", got)
	}
}

type reactionFeedbackSinkFake struct {
	reactions []conversationeval.ReactionFeedback
}

func (*reactionFeedbackSinkFake) ObserveMessage(
	context.Context,
	conversationeval.MessageFeedback,
) error {
	return nil
}

func (f *reactionFeedbackSinkFake) ObserveReaction(
	_ context.Context,
	event conversationeval.ReactionFeedback,
) error {
	f.reactions = append(f.reactions, event)
	return nil
}

func (*reactionFeedbackSinkFake) ObserveCardAction(
	context.Context,
	conversationeval.CardFeedback,
) error {
	return nil
}

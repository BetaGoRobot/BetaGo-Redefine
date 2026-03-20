package handlers

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestSendCompatibleTextSuppressesOutputDuringRuntimeCapabilityExecution(t *testing.T) {
	replyCalled := false
	createCalled := false

	originalReply := scheduleCompatReplyText
	originalCreate := scheduleCompatCreateText
	t.Cleanup(func() {
		scheduleCompatReplyText = originalReply
		scheduleCompatCreateText = originalCreate
	})

	scheduleCompatReplyText = func(ctx context.Context, text, msgID, suffix string, replyInThread bool) (string, error) {
		replyCalled = true
		return "om_reply", nil
	}
	scheduleCompatCreateText = func(ctx context.Context, text, msgID, chatID string) error {
		createCalled = true
		return nil
	}

	msgID := "om_test"
	chatID := "oc_test"
	err := sendCompatibleText(
		runtimecontext.WithCapabilityExecution(context.Background(), "config_set"),
		&larkim.P2MessageReceiveV1{
			Event: &larkim.P2MessageReceiveV1Data{
				Message: &larkim.EventMessage{
					MessageId: &msgID,
					ChatId:    &chatID,
				},
			},
		},
		nil,
		"ok",
		"_test",
		false,
	)
	if err != nil {
		t.Fatalf("sendCompatibleText() error = %v", err)
	}
	if replyCalled {
		t.Fatal("reply path should be suppressed during runtime capability execution")
	}
	if createCalled {
		t.Fatal("create path should be suppressed during runtime capability execution")
	}
}

func TestSendCompatibleTextUsesReplyPathOutsideRuntimeCapabilityExecution(t *testing.T) {
	replyCalled := false

	originalReply := scheduleCompatReplyText
	originalCreate := scheduleCompatCreateText
	t.Cleanup(func() {
		scheduleCompatReplyText = originalReply
		scheduleCompatCreateText = originalCreate
	})

	scheduleCompatReplyText = func(ctx context.Context, text, msgID, suffix string, replyInThread bool) (string, error) {
		replyCalled = true
		if text != "ok" || msgID != "om_test" || suffix != "_test" {
			t.Fatalf("unexpected reply args: text=%q msgID=%q suffix=%q", text, msgID, suffix)
		}
		return "om_reply", nil
	}
	scheduleCompatCreateText = func(ctx context.Context, text, msgID, chatID string) error {
		t.Fatal("create path should not be used when message id exists")
		return nil
	}

	msgID := "om_test"
	chatID := "oc_test"
	err := sendCompatibleText(
		context.Background(),
		&larkim.P2MessageReceiveV1{
			Event: &larkim.P2MessageReceiveV1Data{
				Message: &larkim.EventMessage{
					MessageId: &msgID,
					ChatId:    &chatID,
				},
			},
		},
		nil,
		"ok",
		"_test",
		false,
	)
	if err != nil {
		t.Fatalf("sendCompatibleText() error = %v", err)
	}
	if !replyCalled {
		t.Fatal("expected reply path to be used")
	}
}

func TestSendCompatibleCardJSONAllowsOutputWhenRuntimeCapabilityExecutionDisablesSuppression(t *testing.T) {
	replyCalled := false

	originalReply := scheduleCompatReplyCardJSON
	originalCreate := scheduleCompatCreateCardJSON
	t.Cleanup(func() {
		scheduleCompatReplyCardJSON = originalReply
		scheduleCompatCreateCardJSON = originalCreate
	})

	scheduleCompatReplyCardJSON = func(ctx context.Context, msgID string, cardData any, suffix string, replyInThread bool) (string, error) {
		replyCalled = true
		if msgID != "om_test" || suffix != "_test" {
			t.Fatalf("unexpected reply args: msgID=%q suffix=%q", msgID, suffix)
		}
		return "om_reply", nil
	}
	scheduleCompatCreateCardJSON = func(ctx context.Context, chatID string, cardData any, msgID, suffix string) error {
		t.Fatal("create path should not be used when message id exists")
		return nil
	}

	msgID := "om_test"
	chatID := "oc_test"
	err := sendCompatibleCardJSON(
		runtimecontext.WithCapabilityExecutionOptions(context.Background(), "permission_manage", false),
		&larkim.P2MessageReceiveV1{
			Event: &larkim.P2MessageReceiveV1Data{
				Message: &larkim.EventMessage{
					MessageId: &msgID,
					ChatId:    &chatID,
				},
			},
		},
		nil,
		map[string]any{"card": "ok"},
		"_test",
		false,
	)
	if err != nil {
		t.Fatalf("sendCompatibleCardJSON() error = %v", err)
	}
	if !replyCalled {
		t.Fatal("expected reply card json path to be used")
	}
}

func TestSendCompatibleCardWithMessageIDAllowsOutputWhenRuntimeCapabilityExecutionDisablesSuppression(t *testing.T) {
	replyCalled := false

	originalReply := scheduleCompatReplyCardWithMessageID
	originalCreate := scheduleCompatCreateCardWithMessageID
	t.Cleanup(func() {
		scheduleCompatReplyCardWithMessageID = originalReply
		scheduleCompatCreateCardWithMessageID = originalCreate
	})

	scheduleCompatReplyCardWithMessageID = func(ctx context.Context, msgID string, cardContent *larktpl.TemplateCardContent, suffix string, replyInThread bool) (string, error) {
		replyCalled = true
		if msgID != "om_test" || suffix != "_test" {
			t.Fatalf("unexpected reply args: msgID=%q suffix=%q", msgID, suffix)
		}
		return "om_reply", nil
	}
	scheduleCompatCreateCardWithMessageID = func(ctx context.Context, chatID string, cardContent *larktpl.TemplateCardContent) (string, error) {
		t.Fatal("create path should not be used when message id exists")
		return "", nil
	}

	msgID := "om_test"
	chatID := "oc_test"
	got, err := sendCompatibleCardWithMessageID(
		runtimecontext.WithCapabilityExecutionOptions(context.Background(), "music_search", false),
		&larkim.P2MessageReceiveV1{
			Event: &larkim.P2MessageReceiveV1Data{
				Message: &larkim.EventMessage{
					MessageId: &msgID,
					ChatId:    &chatID,
				},
			},
		},
		nil,
		nil,
		"_test",
		false,
	)
	if err != nil {
		t.Fatalf("sendCompatibleCardWithMessageID() error = %v", err)
	}
	if got != "om_reply" {
		t.Fatalf("message id = %q, want %q", got, "om_reply")
	}
	if !replyCalled {
		t.Fatal("expected reply card path to be used")
	}
}

package handlers

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larktpl"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
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
	scheduleCompatCreateText = func(ctx context.Context, text, msgID, chatID string) (string, error) {
		createCalled = true
		return "", nil
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
	scheduleCompatCreateText = func(ctx context.Context, text, msgID, chatID string) (string, error) {
		t.Fatal("create path should not be used when message id exists")
		return "", nil
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
	scheduleCompatCreateCardJSON = func(ctx context.Context, chatID string, cardData any, msgID, suffix string) (string, error) {
		t.Fatal("create path should not be used when message id exists")
		return "", nil
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

func TestSendCompatibleCardWithMessageIDKeepsCallerThreadFlagInStandardMode(t *testing.T) {
	originalReply := scheduleCompatReplyCardWithMessageID
	originalCreate := scheduleCompatCreateCardWithMessageID
	t.Cleanup(func() {
		scheduleCompatReplyCardWithMessageID = originalReply
		scheduleCompatCreateCardWithMessageID = originalCreate
	})

	scheduleCompatReplyCardWithMessageID = func(ctx context.Context, msgID string, cardContent *larktpl.TemplateCardContent, suffix string, replyInThread bool) (string, error) {
		if replyInThread {
			t.Fatal("expected standard compatible card reply to preserve caller thread flag")
		}
		return "om_reply", nil
	}
	scheduleCompatCreateCardWithMessageID = func(ctx context.Context, chatID string, cardContent *larktpl.TemplateCardContent) (string, error) {
		t.Fatal("create path should not be used when message id exists")
		return "", nil
	}

	msgID := "om_test"
	chatID := "oc_test"
	_, err := sendCompatibleCardWithMessageID(
		context.Background(),
		&larkim.P2MessageReceiveV1{
			Event: &larkim.P2MessageReceiveV1Data{
				Message: &larkim.EventMessage{
					MessageId: &msgID,
					ChatId:    &chatID,
				},
			},
		},
		&xhandler.BaseMetaData{},
		nil,
		"_test",
		false,
	)
	if err != nil {
		t.Fatalf("sendCompatibleCardWithMessageID() error = %v", err)
	}
}

func TestSendCompatibleTextCreatePathRecordsServerMessageID(t *testing.T) {
	originalReply := scheduleCompatReplyText
	originalCreate := scheduleCompatCreateText
	t.Cleanup(func() {
		scheduleCompatReplyText = originalReply
		scheduleCompatCreateText = originalCreate
	})

	scheduleCompatReplyText = func(ctx context.Context, text, msgID, suffix string, replyInThread bool) (string, error) {
		t.Fatal("reply path should not be used without source message id")
		return "", nil
	}
	scheduleCompatCreateText = func(ctx context.Context, text, msgID, chatID string) (string, error) {
		if chatID != "oc_test" {
			t.Fatalf("chat id = %q, want %q", chatID, "oc_test")
		}
		return "om_created_text", nil
	}

	meta := &xhandler.BaseMetaData{ChatID: "oc_test"}
	ctx := runtimecontext.WithCompatibleReplyRecorder(context.Background(), runtimecontext.NewCompatibleReplyRecorder())
	err := sendCompatibleText(ctx, nil, meta, "ok", "_test", false)
	if err != nil {
		t.Fatalf("sendCompatibleText() error = %v", err)
	}

	replyRef, ok := runtimecontext.LatestCompatibleReplyRef(ctx)
	if !ok {
		t.Fatal("expected compatible reply ref to be recorded")
	}
	if replyRef.MessageID != "om_created_text" {
		t.Fatalf("compatible reply message id = %q, want %q", replyRef.MessageID, "om_created_text")
	}
	if messageID, kind := meta.LastReplyRef(); messageID != "om_created_text" || kind != "text" {
		t.Fatalf("last reply ref = (%q,%q), want (%q,%q)", messageID, kind, "om_created_text", "text")
	}
}

func TestSendCompatibleCardJSONCreatePathRecordsServerMessageID(t *testing.T) {
	originalReply := scheduleCompatReplyCardJSON
	originalCreate := scheduleCompatCreateCardJSON
	t.Cleanup(func() {
		scheduleCompatReplyCardJSON = originalReply
		scheduleCompatCreateCardJSON = originalCreate
	})

	scheduleCompatReplyCardJSON = func(ctx context.Context, msgID string, cardData any, suffix string, replyInThread bool) (string, error) {
		t.Fatal("reply path should not be used without source message id")
		return "", nil
	}
	scheduleCompatCreateCardJSON = func(ctx context.Context, chatID string, cardData any, msgID, suffix string) (string, error) {
		if chatID != "oc_test" {
			t.Fatalf("chat id = %q, want %q", chatID, "oc_test")
		}
		return "om_created_card_json", nil
	}

	meta := &xhandler.BaseMetaData{ChatID: "oc_test"}
	ctx := runtimecontext.WithCompatibleReplyRecorder(context.Background(), runtimecontext.NewCompatibleReplyRecorder())
	err := sendCompatibleCardJSON(ctx, nil, meta, map[string]any{"card": "ok"}, "_test", false)
	if err != nil {
		t.Fatalf("sendCompatibleCardJSON() error = %v", err)
	}

	replyRef, ok := runtimecontext.LatestCompatibleReplyRef(ctx)
	if !ok {
		t.Fatal("expected compatible reply ref to be recorded")
	}
	if replyRef.MessageID != "om_created_card_json" {
		t.Fatalf("compatible reply message id = %q, want %q", replyRef.MessageID, "om_created_card_json")
	}
	if messageID, kind := meta.LastReplyRef(); messageID != "om_created_card_json" || kind != "card_json" {
		t.Fatalf("last reply ref = (%q,%q), want (%q,%q)", messageID, kind, "om_created_card_json", "card_json")
	}
}

func TestSendCompatibleRawCardCreatePathRecordsServerMessageID(t *testing.T) {
	originalCreate := scheduleCompatCreateRawCard
	t.Cleanup(func() {
		scheduleCompatCreateRawCard = originalCreate
	})

	scheduleCompatCreateRawCard = func(ctx context.Context, chatID, content, msgID, suffix string) (string, error) {
		if chatID != "oc_test" {
			t.Fatalf("chat id = %q, want %q", chatID, "oc_test")
		}
		return "om_created_raw_card", nil
	}

	meta := &xhandler.BaseMetaData{ChatID: "oc_test"}
	ctx := runtimecontext.WithCompatibleReplyRecorder(context.Background(), runtimecontext.NewCompatibleReplyRecorder())
	err := sendCompatibleRawCard(ctx, nil, meta, `{"type":"card"}`, "_test", false)
	if err != nil {
		t.Fatalf("sendCompatibleRawCard() error = %v", err)
	}

	replyRef, ok := runtimecontext.LatestCompatibleReplyRef(ctx)
	if !ok {
		t.Fatal("expected compatible reply ref to be recorded")
	}
	if replyRef.MessageID != "om_created_raw_card" {
		t.Fatalf("compatible reply message id = %q, want %q", replyRef.MessageID, "om_created_raw_card")
	}
	if messageID, kind := meta.LastReplyRef(); messageID != "om_created_raw_card" || kind != "raw_card" {
		t.Fatalf("last reply ref = (%q,%q), want (%q,%q)", messageID, kind, "om_created_raw_card", "raw_card")
	}
}

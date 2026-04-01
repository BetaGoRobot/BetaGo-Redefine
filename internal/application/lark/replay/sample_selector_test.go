package replay

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
)

func TestSampleSelectorClassifiesMessageShapes(t *testing.T) {
	service := SampleSelectorService{
		loadMessages: func(context.Context, SampleFilterOptions) ([]*xmodel.MessageIndex, error) {
			return []*xmodel.MessageIndex{
				testSampleMessage("om_mention", "@bot 帮我看下", "2026-03-31 10:00:00", "text"),
				testSampleMessage("om_command", "/bb 帮我总结", "2026-03-31 09:00:00", "text"),
				testSampleMessage("om_ambient", "大家先看下这个方案", "2026-03-31 08:00:00", "text"),
			}, nil
		},
		isMention: func(message *xmodel.MessageIndex) bool { return message.MessageID == "om_mention" },
		isReplyToBot: func(context.Context, *xmodel.MessageIndex) bool {
			return false
		},
	}

	got, err := service.Select(context.Background(), SampleFilterOptions{
		ChatID: "oc_chat",
		Days:   7,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if got[0].PrimaryShape != MessageShapeMention {
		t.Fatalf("got[0].PrimaryShape = %q, want mention", got[0].PrimaryShape)
	}
	if got[1].PrimaryShape != MessageShapeCommand {
		t.Fatalf("got[1].PrimaryShape = %q, want command", got[1].PrimaryShape)
	}
	if got[2].PrimaryShape != MessageShapeAmbientGroupMessage {
		t.Fatalf("got[2].PrimaryShape = %q, want ambient_group_message", got[2].PrimaryShape)
	}
}

func TestSampleSelectorAppliesShapeAndContentFilters(t *testing.T) {
	service := SampleSelectorService{
		loadMessages: func(context.Context, SampleFilterOptions) ([]*xmodel.MessageIndex, error) {
			return []*xmodel.MessageIndex{
				testSampleMessage("om_match", "请看 https://example.com 这个链接怎么样？", "2026-03-31 10:00:00", "text"),
				testSampleMessage("om_skip_shape", "@bot 看一下", "2026-03-31 09:00:00", "text"),
				testSampleMessage("om_skip_keyword", "这个链接也不错 https://example.com", "2026-03-31 08:00:00", "text"),
			}, nil
		},
		isMention: func(message *xmodel.MessageIndex) bool { return message.MessageID == "om_skip_shape" },
		isReplyToBot: func(context.Context, *xmodel.MessageIndex) bool {
			return false
		},
	}

	got, err := service.Select(context.Background(), SampleFilterOptions{
		ChatID:          "oc_chat",
		Days:            7,
		Limit:           10,
		AllowedShapes:   []MessageShape{MessageShapeAmbientGroupMessage},
		RequireQuestion: true,
		RequireLink:     true,
		Keyword:         "怎么样",
	})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].MessageID != "om_match" {
		t.Fatalf("got[0].MessageID = %q, want %q", got[0].MessageID, "om_match")
	}
}

func TestSampleSelectorSupportsReplyToBotAndAttachmentAndLimit(t *testing.T) {
	service := SampleSelectorService{
		loadMessages: func(context.Context, SampleFilterOptions) ([]*xmodel.MessageIndex, error) {
			return []*xmodel.MessageIndex{
				testSampleMessage("om_new", "继续推进", "2026-03-31 10:00:00", "image"),
				testSampleMessage("om_old", "继续推进", "2026-03-30 10:00:00", "file"),
				testSampleMessage("om_skip", "继续推进", "2026-03-29 10:00:00", "text"),
			}, nil
		},
		isMention: func(*xmodel.MessageIndex) bool { return false },
		isReplyToBot: func(_ context.Context, message *xmodel.MessageIndex) bool {
			return message.MessageID == "om_new" || message.MessageID == "om_old"
		},
	}

	got, err := service.Select(context.Background(), SampleFilterOptions{
		ChatID:            "oc_chat",
		Days:              7,
		Limit:             1,
		AllowedShapes:     []MessageShape{MessageShapeReplyToBot},
		RequireAttachment: true,
	})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].MessageID != "om_new" {
		t.Fatalf("got[0].MessageID = %q, want newest reply_to_bot sample", got[0].MessageID)
	}
}

func testSampleMessage(messageID, rawMessage, createTime, messageType string) *xmodel.MessageIndex {
	return &xmodel.MessageIndex{
		MessageLog: &xmodel.MessageLog{
			MessageID:   messageID,
			ChatID:      "oc_chat",
			MessageType: messageType,
			Content:     rawMessage,
		},
		CreateTime: createTime,
		RawMessage: rawMessage,
	}
}

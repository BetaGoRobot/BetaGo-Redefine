package agentruntime

import (
	"context"
	"testing"
)

type textMessengerFake struct {
	route string
	key   string
}

func (f *textMessengerFake) ReplyText(_ context.Context, messageID, text, key string) (string, error) {
	f.route, f.key = "reply:"+messageID+":"+text, key
	return "om-reply", nil
}

func (f *textMessengerFake) CreateText(_ context.Context, chatID, text, key string) (string, error) {
	f.route, f.key = "create:"+chatID+":"+text, key
	return "om-create", nil
}

func TestLarkReplyDelivererRoutesReplyAndCreateWithStableKey(t *testing.T) {
	tests := []struct {
		name    string
		request ReplyRequest
		wantID  string
		route   string
	}{
		{
			name: "reply", request: ReplyRequest{
				Text: "完成", TriggerMessageID: "om-trigger", ChatID: "oc-chat",
				IdempotencyKey: "step-reply-1",
			},
			wantID: "om-reply", route: "reply:om-trigger:完成",
		},
		{
			name: "create", request: ReplyRequest{
				Text: "完成", ChatID: "oc-chat", IdempotencyKey: "step-reply-1",
			},
			wantID: "om-create", route: "create:oc-chat:完成",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messenger := &textMessengerFake{}
			deliverer := newLarkReplyDeliverer(messenger)
			messageID, err := deliverer.Deliver(context.Background(), tt.request)
			if err != nil {
				t.Fatalf("Deliver() error = %v", err)
			}
			if messageID != tt.wantID || messenger.route != tt.route ||
				messenger.key != tt.request.IdempotencyKey {
				t.Fatalf("message=%q route=%q key=%q", messageID, messenger.route, messenger.key)
			}
		})
	}
}

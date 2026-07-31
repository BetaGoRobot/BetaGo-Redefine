package larkchunking

import (
	"context"
	"testing"
)

type contextTestMessage struct{}

func (contextTestMessage) GroupID() string           { return "oc_context" }
func (contextTestMessage) MsgID() string             { return "om_context" }
func (contextTestMessage) TimeStamp() int64          { return 1 }
func (contextTestMessage) BuildLine() (string, bool) { return "message", true }

func TestSubmitMessageChecksPolicyWithDurableContext(t *testing.T) {
	previous := enabledForChat
	t.Cleanup(func() {
		enabledForChat = previous
	})
	var policyContextErr error
	enabledForChat = func(ctx context.Context, _ string) bool {
		policyContextErr = ctx.Err()
		return false
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SubmitMessage(ctx, contextTestMessage{}); err != nil {
		t.Fatal(err)
	}
	if policyContextErr != nil {
		t.Fatalf("SubmitMessage() inherited canceled context: %v", policyContextErr)
	}
}

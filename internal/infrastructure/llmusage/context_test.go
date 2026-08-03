package llmusage

import (
	"context"
	"testing"
)

func TestApplyBusinessAttributionUsesContextBeforeFallback(t *testing.T) {
	ctx := WithBusinessAttribution(context.Background(), SceneCommand, OperationCommandChat)
	got := ApplyBusinessAttribution(ctx, Scope{Source: "chat"}, SceneConversation, OperationChatReply)
	if got.BusinessScene != SceneCommand || got.BusinessOperation != OperationCommandChat {
		t.Fatalf("context attribution = %q/%q", got.BusinessScene, got.BusinessOperation)
	}
}

func TestApplyBusinessAttributionUsesFallbackForInvalidContext(t *testing.T) {
	ctx := WithBusinessAttribution(context.Background(), SceneUnknown, OperationUnknown)
	got := ApplyBusinessAttribution(ctx, Scope{Source: "chat"}, SceneConversation, OperationChatReply)
	if got.BusinessScene != SceneConversation || got.BusinessOperation != OperationChatReply {
		t.Fatalf("fallback attribution = %q/%q", got.BusinessScene, got.BusinessOperation)
	}
}

func TestWithBusinessAttributionDoesNotMutateParentContext(t *testing.T) {
	parent := context.Background()
	child := WithBusinessAttribution(parent, SceneConversation, OperationMentionReply)
	if _, _, ok := BusinessAttributionFromContext(parent); ok {
		t.Fatal("parent context unexpectedly contains attribution")
	}
	scene, operation, ok := BusinessAttributionFromContext(child)
	if !ok || scene != SceneConversation || operation != OperationMentionReply {
		t.Fatalf("child attribution = %q/%q, %v", scene, operation, ok)
	}
}

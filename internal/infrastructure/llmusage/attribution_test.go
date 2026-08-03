package llmusage

import "testing"

func TestNormalizeScopePrefersExplicitBusinessAttribution(t *testing.T) {
	got := NormalizeScope(Scope{
		SourceType:        SourceTypeUser,
		Source:            " chat ",
		BusinessScene:     SceneCommand,
		BusinessOperation: OperationCommandChat,
	})
	if got.BusinessScene != SceneCommand || got.BusinessOperation != OperationCommandChat {
		t.Fatalf("normalized attribution = %q/%q", got.BusinessScene, got.BusinessOperation)
	}
	if got.AttributionMode != AttributionExplicit {
		t.Fatalf("AttributionMode = %q, want %q", got.AttributionMode, AttributionExplicit)
	}
}

func TestNormalizeScopeMapsKnownLegacySources(t *testing.T) {
	tests := []struct {
		source    string
		scene     BusinessScene
		operation BusinessOperation
	}{
		{source: "chat", scene: SceneConversation, operation: OperationChatReply},
		{source: "intent", scene: SceneRouting, operation: OperationIntentRecognition},
		{source: "history_search", scene: SceneRetrieval, operation: OperationHistorySearch},
		{source: "topic_recall", scene: SceneRetrieval, operation: OperationTopicRecall},
		{source: "retriever_embedding", scene: SceneRetrieval, operation: OperationRetrieverEmbedding},
		{source: "retriever_recall", scene: SceneRetrieval, operation: OperationRetrieverRecall},
		{source: "retriever_answer", scene: SceneRetrieval, operation: OperationRetrieverAnswer},
		{source: "message_recording", scene: SceneBackground, operation: OperationMessageEmbedding},
		{source: "outbound_message_recording", scene: SceneBackground, operation: OperationOutboundEmbedding},
		{source: "chunking", scene: SceneBackground, operation: OperationChunkMerge},
		{source: "chunking_embedding", scene: SceneBackground, operation: OperationChunkEmbedding},
		{source: "reindex_embeddings", scene: SceneBackground, operation: OperationReindexEmbedding},
		{source: "conversation_evaluation_candidate", scene: SceneEvaluation, operation: OperationCandidateGeneration},
		{source: "agent_callback_continuation", scene: SceneAgentRuntime, operation: OperationCallbackContinuation},
		{source: "debug_image", scene: SceneDebug, operation: OperationDebugImage},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			got := NormalizeScope(Scope{SourceType: SourceTypeSystem, Source: " " + tt.source + " "})
			if got.BusinessScene != tt.scene || got.BusinessOperation != tt.operation {
				t.Fatalf("attribution = %q/%q, want %q/%q", got.BusinessScene, got.BusinessOperation, tt.scene, tt.operation)
			}
			if got.AttributionMode != AttributionLegacyMapping {
				t.Fatalf("AttributionMode = %q, want %q", got.AttributionMode, AttributionLegacyMapping)
			}
		})
	}
}

func TestNormalizeScopeRejectsPartialOrInvalidExplicitAttribution(t *testing.T) {
	got := NormalizeScope(Scope{
		SourceType:        SourceTypeBackground,
		Source:            "chunking",
		BusinessScene:     BusinessScene("not-real"),
		BusinessOperation: OperationCommandChat,
	})
	if got.BusinessScene != SceneBackground || got.BusinessOperation != OperationChunkMerge {
		t.Fatalf("fallback attribution = %q/%q", got.BusinessScene, got.BusinessOperation)
	}
	if got.AttributionMode != AttributionLegacyMapping {
		t.Fatalf("AttributionMode = %q, want legacy mapping", got.AttributionMode)
	}
}

func TestNormalizeScopeUsesUnknownForUnmappedSource(t *testing.T) {
	got := NormalizeScope(Scope{SourceType: SourceTypeSystem, Source: "brand_new_source"})
	if got.BusinessScene != SceneUnknown || got.BusinessOperation != OperationUnknown {
		t.Fatalf("unknown attribution = %q/%q", got.BusinessScene, got.BusinessOperation)
	}
	if got.AttributionMode != AttributionUnknown {
		t.Fatalf("AttributionMode = %q, want %q", got.AttributionMode, AttributionUnknown)
	}
}

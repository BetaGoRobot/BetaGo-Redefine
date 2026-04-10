package xmodel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageIndexMarshalsV2VectorFieldOnly(t *testing.T) {
	data, err := json.Marshal(&MessageIndex{
		RawMessage: "hello",
		MessageV2:  []float32{0.1, 0.2},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	jsonText := string(data)
	if !strings.Contains(jsonText, "\"message_v2\"") {
		t.Fatalf("marshal = %s, want message_v2", jsonText)
	}
	if strings.Contains(jsonText, "\"message\":") {
		t.Fatalf("marshal = %s, want no legacy message field", jsonText)
	}
}

func TestMessageChunkLogV3MarshalsV2VectorFieldOnly(t *testing.T) {
	data, err := json.Marshal(&MessageChunkLogV3{
		Summary:                 "topic",
		ConversationEmbeddingV2: []float32{0.1, 0.2},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	jsonText := string(data)
	if !strings.Contains(jsonText, "\"conversation_embedding_v2\"") {
		t.Fatalf("marshal = %s, want conversation_embedding_v2", jsonText)
	}
	if strings.Contains(jsonText, "\"conversation_embedding\":") {
		t.Fatalf("marshal = %s, want no legacy conversation_embedding field", jsonText)
	}
}

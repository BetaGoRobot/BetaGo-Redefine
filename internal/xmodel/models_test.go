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

func TestMessageIndexPreservesRawCreateTimeMilliseconds(t *testing.T) {
	const rawMillis = "1785301200123"
	index := &MessageIndex{
		CreateTimeUnixMillis: MessageCreateTimeUnixMillis(rawMillis),
	}
	if index.CreateTimeUnixMillis != 1785301200123 {
		t.Fatalf("create_time_unix_millis = %d, want 1785301200123", index.CreateTimeUnixMillis)
	}

	data, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"create_time_unix_millis":1785301200123`) {
		t.Fatalf("marshal = %s, want exact raw milliseconds", data)
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

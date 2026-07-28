package opensearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func TestNoopUpsertReportsUnavailable(t *testing.T) {
	previous := backend
	backend = noopBackend{reason: "disabled in test"}
	t.Cleanup(func() { backend = previous })

	err := UpsertData(context.Background(), "agent_conversation_events", "event-1", map[string]any{"status": "completed"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("UpsertData() error = %v, want ErrUnavailable", err)
	}
}

func TestLiveUpsertUsesExactIndexWithoutDateSuffix(t *testing.T) {
	var requestPath string
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.EscapedPath()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		requestBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"_index":"agent_conversation_events","_id":"event-1","result":"created","_version":1,"_seq_no":0,"_primary_term":1}`))
	}))
	defer server.Close()

	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{Addresses: []string{server.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = (liveBackend{client: client}).UpsertData(
		context.Background(),
		"agent_conversation_events",
		"event-1",
		map[string]any{"status": "completed"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if requestPath != "/agent_conversation_events/_doc/event-1" {
		t.Fatalf("request path = %q", requestPath)
	}
	if !strings.Contains(requestBody, `"status":"completed"`) {
		t.Fatalf("request body = %q", requestBody)
	}
}

func TestLiveUpsertReturnsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"type":"unavailable_shards_exception","reason":"try later"},"status":503}`))
	}))
	defer server.Close()
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{Addresses: []string{server.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = (liveBackend{client: client}).UpsertData(
		context.Background(), "agent_conversation_events", "event-1", map[string]any{"status": "completed"},
	)
	if err == nil {
		t.Fatal("UpsertData() error = nil for HTTP 503")
	}
}

func TestConversationEventMappingHasStableAliasFieldTypes(t *testing.T) {
	content, err := os.ReadFile("../../../script/opensearch/agent_conversation_events_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var mapping struct {
		Mappings struct {
			Dynamic    any `json:"dynamic"`
			Properties map[string]struct {
				Type    string `json:"type"`
				Enabled *bool  `json:"enabled"`
				Fields  map[string]struct {
					Type string `json:"type"`
				} `json:"fields"`
			} `json:"properties"`
		} `json:"mappings"`
	}
	if err := json.Unmarshal(content, &mapping); err != nil {
		t.Fatal(err)
	}
	if mapping.Mappings.Dynamic != false {
		t.Fatalf("root dynamic = %#v, want false so legacy callback fields remain in _source without mapping", mapping.Mappings.Dynamic)
	}
	for _, field := range []string{
		"schema_version", "event_id", "event_type", "run_id", "step_id", "session_id",
		"source_step_id", "parent_step_id", "chat_id", "actor_open_id",
		"status", "step_status", "outcome_status", "external_ref",
		"message_id", "source_message_id", "interaction_id", "capability_name", "action",
	} {
		if got := mapping.Mappings.Properties[field].Type; got != "keyword" {
			t.Fatalf("%s type = %q, want keyword", field, got)
		}
	}
	if got := mapping.Mappings.Properties["occurred_at"].Type; got != "date" {
		t.Fatalf("occurred_at type = %q, want date", got)
	}
	if got := mapping.Mappings.Properties["revision"].Type; got != "long" {
		t.Fatalf("revision type = %q, want long", got)
	}
	if got := mapping.Mappings.Properties["relevance"].Type; got != "float" {
		t.Fatalf("relevance type = %q, want float", got)
	}
	contentField := mapping.Mappings.Properties["content"]
	if contentField.Type != "text" || contentField.Fields["keyword"].Type != "keyword" {
		t.Fatalf("content mapping = %#v", contentField)
	}
	structured := mapping.Mappings.Properties["structured_payload"]
	if structured.Type != "object" || structured.Enabled == nil || *structured.Enabled {
		t.Fatalf("structured_payload mapping = %#v", structured)
	}
}

func TestConversationEventMappingAcceptsLegacyScheduleFactSourceShape(t *testing.T) {
	content, err := os.ReadFile("../../../script/opensearch/agent_conversation_events_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var mapping struct {
		Mappings struct {
			Dynamic    any                       `json:"dynamic"`
			Properties map[string]map[string]any `json:"properties"`
		} `json:"mappings"`
	}
	if err := json.Unmarshal(content, &mapping); err != nil {
		t.Fatal(err)
	}
	legacyScheduleFact := map[string]any{
		"type": "schedule_edit_outcome", "interaction_id": "interaction-1",
		"revision": 2, "action": "confirm", "task_id": "task-1",
		"event_type": "capability_result", "step_id": "step-1",
	}
	if mapping.Mappings.Dynamic != false {
		t.Fatalf("legacy callback source would be rejected when root dynamic = %#v", mapping.Mappings.Dynamic)
	}
	for field := range legacyScheduleFact {
		if field != "type" && field != "task_id" {
			continue
		}
		if _, mapped := mapping.Mappings.Properties[field]; mapped {
			t.Fatalf("legacy business field %q unexpectedly expands the stable mapping", field)
		}
	}
	encoded, err := json.Marshal(legacyScheduleFact)
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("legacy schedule fact source is not preserved JSON: %s, %v", encoded, err)
	}
}

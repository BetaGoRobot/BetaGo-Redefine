package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/tenant"
	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	opensearchschema "github.com/BetaGoRobot/BetaGo-Redefine/script/opensearch"
)

func TestLiveEvaluationProvisioningAndWrite(t *testing.T) {
	if os.Getenv("BETAGO_LIVE_OPENSEARCH_TEST") != "1" {
		t.Skip("set BETAGO_LIVE_OPENSEARCH_TEST=1 to run")
	}
	configPath := os.Getenv("BETAGO_CONFIG_PATH")
	if configPath == "" {
		t.Fatal("BETAGO_CONFIG_PATH is required")
	}
	cfg, err := infraConfig.LoadFileE(configPath)
	if err != nil {
		t.Fatal(err)
	}
	Init(cfg.OpensearchConfig)
	provisioner, err := NewProvisioner()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := tenant.New("betago-live-probe-app", "betago-live-probe-bot")
	if err != nil {
		t.Fatal(err)
	}
	baseAlias := fmt.Sprintf("betago-evaluation-mapping-probe-%d", time.Now().UnixNano())
	alias, err := owner.IndexAlias(baseAlias)
	if err != nil {
		t.Fatal(err)
	}
	physical := physicalIndexName(alias)
	t.Cleanup(func() {
		status, cleanupErr := provisioner.perform(
			context.Background(),
			http.MethodDelete,
			"/"+url.PathEscape(physical),
			nil,
		)
		if cleanupErr != nil || (status != http.StatusOK && status != http.StatusNotFound) {
			t.Errorf("cleanup temporary index: status=%d err=%v", status, cleanupErr)
		}
	})

	resource, err := provisioner.EnsureTenantIndex(
		context.Background(),
		owner,
		baseAlias,
		"conversation_evaluation.v1",
		opensearchschema.ConversationEvaluationsV1,
	)
	if err != nil {
		t.Fatal(err)
	}

	status, mappingBody, err := provisioner.performWithResponse(
		context.Background(),
		http.MethodGet,
		"/"+url.PathEscape(resource.PhysicalIndex)+"/_mapping",
		nil,
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("read mapping: status=%d err=%v", status, err)
	}
	var mappingResponse map[string]struct {
		Mappings map[string]any `json:"mappings"`
	}
	if err := json.Unmarshal(mappingBody, &mappingResponse); err != nil {
		t.Fatal(err)
	}
	properties, ok := mappingResponse[resource.PhysicalIndex].Mappings["properties"].(map[string]any)
	if !ok {
		t.Fatal("mapping properties missing")
	}
	preMessages, ok := properties["pre_messages"].(map[string]any)
	if !ok {
		t.Fatal("pre_messages mapping missing")
	}
	latestJudgments, ok := properties["latest_judgments"].(map[string]any)
	if !ok {
		t.Fatal("latest_judgments mapping missing")
	}
	t.Logf(
		"real mapping: pre_messages.type=%v has_properties=%t latest_judgments.type=%v",
		preMessages["type"],
		preMessages["properties"] != nil,
		latestJudgments["type"],
	)

	now := time.Now().UTC().Truncate(time.Millisecond)
	document := map[string]any{
		"schema_version":      "conversation_evaluation.v1",
		"tenant_id":           owner.ID,
		"app_id":              owner.AppID,
		"bot_open_id":         owner.BotOpenID,
		"episode_id":          "episode-probe",
		"cohort_id":           "cohort-probe",
		"chat_id":             "chat-probe",
		"anchor_event_id":     "event-probe",
		"anchor_message_id":   "message-probe",
		"status":              "complete",
		"serving_lane":        "candidate",
		"anchor_at":           now,
		"late_feedback_until": now.Add(time.Hour),
		"updated_at":          now,
		"pre_messages": []any{
			map[string]any{
				"message_id":     "message-before",
				"content":        "before",
				"occurred_at":    now.Add(-time.Minute),
				"sender_open_id": "user-probe",
			},
		},
		"anchor_message": map[string]any{
			"message_id":  "message-probe",
			"content":     "anchor",
			"occurred_at": now,
		},
		"post_messages": []any{
			map[string]any{
				"message_id":  "message-after",
				"content":     "after",
				"occurred_at": now.Add(time.Minute),
			},
		},
		"control": map[string]any{
			"join_decision": "silent",
			"has_error":     false,
		},
		"candidate": map[string]any{
			"join_decision": "reply",
			"has_error":     false,
		},
		"latest_judgments": []any{
			map[string]any{
				"source":       "probe",
				"version":      1,
				"winner":       "candidate",
				"rationale":    "probe",
				"confidence":   100,
				"needs_review": false,
				"created_at":   now,
			},
		},
		"full_snapshot": map[string]any{"probe": true},
	}
	documentBody, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	status, responseBody, err := provisioner.performWithResponse(
		context.Background(),
		http.MethodPut,
		"/"+url.PathEscape(resource.Alias)+"/_doc/probe?refresh=true",
		documentBody,
	)
	if err != nil || (status != http.StatusOK && status != http.StatusCreated) {
		t.Fatalf("write evaluation: status=%d err=%v body=%s", status, err, responseBody)
	}
	status, responseBody, err = provisioner.performWithResponse(
		context.Background(),
		http.MethodGet,
		"/"+url.PathEscape(resource.Alias)+"/_doc/probe",
		nil,
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("read evaluation: status=%d err=%v body=%s", status, err, responseBody)
	}
	var getResponse struct {
		Found  bool           `json:"found"`
		Source map[string]any `json:"_source"`
	}
	if err := json.Unmarshal(responseBody, &getResponse); err != nil {
		t.Fatal(err)
	}
	if !getResponse.Found || getResponse.Source["episode_id"] != "episode-probe" {
		t.Fatalf("evaluation was not read back: %#v", getResponse)
	}

	searchBody := []byte(`{
		"query": {
			"bool": {
				"filter": [
					{"term": {"pre_messages.message_id": "message-before"}},
					{
						"nested": {
							"path": "latest_judgments",
							"query": {
								"term": {
									"latest_judgments.winner": "candidate"
								}
							}
						}
					}
				]
			}
		}
	}`)
	status, responseBody, err = provisioner.performWithResponse(
		context.Background(),
		http.MethodPost,
		"/"+url.PathEscape(resource.Alias)+"/_search",
		searchBody,
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("search evaluation: status=%d err=%v body=%s", status, err, responseBody)
	}
	var searchResponse struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(responseBody, &searchResponse); err != nil {
		t.Fatal(err)
	}
	if searchResponse.Hits.Total.Value != 1 {
		t.Fatalf("mapped object and nested search hits = %d, want 1", searchResponse.Hits.Total.Value)
	}
}

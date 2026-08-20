package evaluationwindow

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPreWindowQueryUsesStrictModernAndConservativeLegacyCutoff(t *testing.T) {
	anchor := time.Date(2026, 7, 29, 10, 0, 0, 456000000, time.UTC)
	query := preWindowQuery("chat-1", anchor, 20)
	encoded, err := json.Marshal(query)
	if err != nil {
		t.Fatalf("json.Marshal(query) error = %v", err)
	}
	var decoded struct {
		Size  int `json:"size"`
		Query struct {
			Bool struct {
				Filter []json.RawMessage `json:"filter"`
			} `json:"bool"`
		} `json:"query"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(query) error = %v", err)
	}
	if decoded.Size != 20 || len(decoded.Query.Bool.Filter) != 2 {
		t.Fatalf("query shape = %s", encoded)
	}
	var causal struct {
		Bool struct {
			Minimum int `json:"minimum_should_match"`
			Should  []struct {
				Range map[string]map[string]any `json:"range"`
				Bool  struct {
					MustNot []struct {
						Exists map[string]string `json:"exists"`
					} `json:"must_not"`
					Filter []struct {
						Range map[string]map[string]any `json:"range"`
					} `json:"filter"`
				} `json:"bool"`
			} `json:"should"`
		} `json:"bool"`
	}
	if err := json.Unmarshal(decoded.Query.Bool.Filter[1], &causal); err != nil {
		t.Fatalf("decode causal filter: %v", err)
	}
	if causal.Bool.Minimum != 1 || len(causal.Bool.Should) != 2 {
		t.Fatalf("causal filter = %#v", causal)
	}
	if got := causal.Bool.Should[0].Range["create_time_unix_millis"]["lt"]; got != float64(anchor.UnixMilli()) {
		t.Fatalf("modern cutoff = %#v, want %d", got, anchor.UnixMilli())
	}
	legacy := causal.Bool.Should[1].Bool
	if len(legacy.MustNot) != 1 ||
		legacy.MustNot[0].Exists["field"] != "create_time_unix_millis" {
		t.Fatalf("legacy missing-field guard = %#v", legacy.MustNot)
	}
	wantLegacy := anchor.Truncate(time.Second).Add(-time.Nanosecond).Format(time.RFC3339Nano)
	if got := legacy.Filter[0].Range["create_time_v2"]["lte"]; got != wantLegacy {
		t.Fatalf("legacy cutoff = %#v, want %q", got, wantLegacy)
	}
}

func TestPreWindowQuerySortsByKeywordMessageID(t *testing.T) {
	query := preWindowQuery("chat-1", time.Now(), 20)
	sorts, ok := query["sort"].([]any)
	if !ok || len(sorts) != 2 {
		t.Fatalf("sort = %#v, want two sort clauses", query["sort"])
	}
	tieBreaker, ok := sorts[1].(map[string]any)
	if !ok {
		t.Fatalf("message ID sort = %#v, want object", sorts[1])
	}
	if _, ok := tieBreaker["message_id.keyword"]; !ok {
		t.Fatalf(
			"message ID sort = %#v, want message_id.keyword to avoid text fielddata",
			tieBreaker,
		)
	}
}

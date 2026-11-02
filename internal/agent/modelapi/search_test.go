package modelapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArkWebSearchRequiresActualSearchAndCitations(t *testing.T) {
	body := `{"status":"completed","output":[{"type":"web_search_call","status":"completed"},{"type":"message","content":[{"type":"output_text","text":"搜索整理的内容","annotations":[{"type":"url_citation","url":"https://go.dev/doc/devel/release","title":"Release history","summary":"来源摘录"}]}]}]}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer search-secret" {
			t.Error("responses protocol/auth")
		}
		var b map[string]any
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			t.Fatal("request JSON")
		}
		if b["model"] != "search-model" || b["store"] != false || b["max_tool_calls"] != float64(1) || b["tool_choice"].(map[string]any)["type"] != "web_search" {
			t.Errorf("wrong search contract: %v", b)
		}
		tools := b["tools"].([]any)
		if len(tools) != 1 || tools[0].(map[string]any)["type"] != "web_search" {
			t.Error("builtin search omitted")
		}
		encoded, _ := json.Marshal(b["input"])
		if !strings.Contains(string(encoded), "Go releases") || strings.Contains(string(encoded), "tenant_id") {
			t.Error("query missing or group internals sent")
		}
		io.WriteString(w, body)
	}))
	defer s.Close()
	search, err := NewWebSearch(Endpoint{BaseURL: s.URL, APIKey: "search-secret", Model: "search-model", Protocol: "ark_responses"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := search.Search(context.Background(), "Go releases", 3)
	if err != nil || out.Text != "搜索整理的内容" || len(out.Sources) != 1 || out.Sources[0].Excerpt != "来源摘录" || !out.Untrusted || out.RetrievedAt.IsZero() {
		t.Fatalf("lost grounding: %+v %v", out, err)
	}
	for _, bad := range []string{`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"I know the answer"}]}]}`, `{"status":"incomplete","output":[]}`, `{"status":"completed","output":[{"type":"web_search_call","status":"completed"},{"type":"message","content":[{"type":"output_text","text":"text","annotations":[{"type":"url_citation","url":"http://127.0.0.1/","title":"internal"}]}]}]}`} {
		body = bad
		if _, err := search.Search(context.Background(), "Go releases", 3); err == nil {
			t.Error("ungrounded response accepted")
		}
	}
	for _, q := range []string{"", strings.Repeat("x", 1025)} {
		if _, err := search.Search(context.Background(), q, 3); err == nil {
			t.Error("unbounded query")
		}
	}
}

package presentation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestComposeTypedCardContainsOnlyBoundedSchema2DisplayComponents(t *testing.T) {
	card := Card{Title: "Service report", Sections: []Section{{Kind: "markdown", Text: "**Ready**"}, {Kind: "table", Columns: []string{"Service", "State"}, Rows: [][]string{{"agent", "ready"}}}, {Kind: "bar_chart", Bars: []Bar{{Label: "done", Value: 12}, {Label: "pending", Value: 3}}}}, Links: []Link{{Label: "Reference", URL: "https://example.com/report"}}}
	raw, err := Compose(card)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil || v["schema"] != "2.0" {
		t.Fatalf("not schema 2: %s", raw)
	}
	text := string(raw)
	if !strings.Contains(text, "Service: agent") || !strings.Contains(text, "done") || !strings.Contains(text, "████") || strings.Contains(text, "callback") || strings.Contains(text, "behaviors") {
		t.Fatalf("typed composition lost data or added actions: %s", text)
	}
}
func TestComposeRejectsUnsafeLinksMarkupOversizeAndNonfiniteCharts(t *testing.T) {
	for _, c := range []Card{
		{Title: "bad", Sections: []Section{{Kind: "markdown", Text: "<at id=all>all</at>"}}},
		{Title: "bad", Sections: []Section{{Kind: "markdown", Text: "[secret](https://u:p@example.com)"}}},
		{Title: "bad", Sections: []Section{{Kind: "markdown", Text: "x"}}, Links: []Link{{Label: "x", URL: "https://u:p@example.com"}}},
		{Title: "bad", Sections: []Section{{Kind: "markdown", Text: "x"}}, Links: []Link{{Label: "x", URL: "https://localhost/private"}}},
		{Title: "bad", Sections: []Section{{Kind: "markdown", Text: "x"}}, Links: []Link{{Label: "x", URL: "https://example.com/?api_key=secret"}}},
		{Title: "bad", Sections: []Section{{Kind: "markdown", Text: strings.Repeat("x", 9000)}}},
		{Title: "bad", Sections: []Section{{Kind: "table", Columns: []string{"x", "y"}, Rows: [][]string{{"one"}}}}},
	} {
		if _, err := Compose(c); err == nil {
			t.Fatalf("unsafe typed card accepted: %+v", c)
		}
	}
}

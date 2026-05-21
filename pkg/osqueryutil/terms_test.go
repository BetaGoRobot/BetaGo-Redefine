package osqueryutil

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTermsFromStringsBuildsFlatTermsArray(t *testing.T) {
	query := TermsFromStrings("message_id", []string{"om_1", "om_2"})

	raw, err := json.Marshal(query.Map())
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}
	queryJSON := string(raw)
	if strings.Contains(queryJSON, `[[`) {
		t.Fatalf("query JSON = %s, want flat terms array", queryJSON)
	}
	if !strings.Contains(queryJSON, `"message_id":["om_1","om_2"]`) {
		t.Fatalf("query JSON = %s, want message_id terms array", queryJSON)
	}
}

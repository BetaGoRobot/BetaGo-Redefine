package tools

import (
	"encoding/json"
	"testing"
)

func TestParamJSONEmitsStrictCompatibleObjectSchema(t *testing.T) {
	params := NewParams("object").
		AddProp("query", &Prop{Type: "string", Desc: "search query"}).
		AddProp("filters", &Prop{
			Type: "object",
			Desc: "structured filters",
			Props: map[string]*Prop{
				"user": {Type: "string", Desc: "user name"},
			},
		}).
		AddProp("tags", &Prop{
			Type:  "array",
			Desc:  "tag list",
			Items: &Prop{Type: "string", Desc: "tag"},
		})

	var schema map[string]any
	if err := json.Unmarshal(params.JSON(), &schema); err != nil {
		t.Fatalf("schema json should unmarshal: %v", err)
	}

	if schema["additionalProperties"] != false {
		t.Fatalf("root additionalProperties = %#v, want false", schema["additionalProperties"])
	}
	props := schema["properties"].(map[string]any)
	filters := props["filters"].(map[string]any)
	if filters["additionalProperties"] != false {
		t.Fatalf("nested object additionalProperties = %#v, want false", filters["additionalProperties"])
	}
	tags := props["tags"].(map[string]any)
	if _, ok := tags["items"].(map[string]any); !ok {
		t.Fatalf("array items = %#v, want schema object", tags["items"])
	}
}

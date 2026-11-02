package atoms_test

import (
	"encoding/json"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
)

func TestReactionToolExposesCompleteCaseSensitiveCatalogue(t *testing.T) {
	f := fixtureCommunity()
	r, err := atoms.New(atoms.Options{Sources: f, Group: f})
	if err != nil {
		t.Fatal(err)
	}
	d := namedTool(t, r, "feishu_add_reaction")
	var schema struct {
		Properties map[string]struct{ Enum []string }
	}
	if err = json.Unmarshal(d.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	values := schema.Properties["emoji"].Enum
	if len(values) < 160 {
		t.Fatalf("model cannot see complete reaction knowledge: %d emoji types", len(values))
	}
	seen := map[string]bool{}
	for _, v := range values {
		if v == "" || seen[v] {
			t.Fatalf("invalid catalogue entry: %q", v)
		}
		seen[v] = true
		args, _ := json.Marshal(map[string]string{"message_id": "target", "emoji": v})
		if _, err = d.Validate(string(args)); err != nil {
			t.Fatalf("supported emoji rejected: %q %v", v, err)
		}
	}
	for _, required := range []string{"THUMBSUP", "COMFORT", "FISTBUMP", "HIGHFIVE", "LOL", "OneSecond", "GeneralTravellingCar", "Coffee"} {
		if !seen[required] {
			t.Fatalf("existing emoji missing: %s", required)
		}
	}
	for _, invalid := range []string{"thumbsup", "😀", "invented_reaction"} {
		args, _ := json.Marshal(map[string]string{"message_id": "target", "emoji": invalid})
		if _, err = d.Validate(string(args)); err == nil {
			t.Fatalf("unknown emoji reached execution: %q", invalid)
		}
	}
}

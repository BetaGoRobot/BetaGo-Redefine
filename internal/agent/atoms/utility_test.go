package atoms_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
)

func namedTool(t *testing.T, r *atoms.Registry, name string) atoms.Definition {
	t.Helper()
	for _, d := range r.Definitions() {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("missing tool %s", name)
	return atoms.Definition{}
}
func TestCalculateExactAndBounded(t *testing.T) {
	r, err := atoms.New(atoms.Options{})
	if err != nil {
		t.Fatal(err)
	}
	d := namedTool(t, r, "calculate")
	for expression, want := range map[string]string{"0.1+0.2": "3/10", "(1 / 3 + 2 / 3) * 7": "7", "-7 % 3": "-1", "2^10": "1024", "1e-3 + .009": "1/100"} {
		args, _ := json.Marshal(map[string]string{"expression": expression})
		raw, err := d.Run(context.Background(), atoms.Invocation{}, string(args))
		if err != nil {
			t.Fatalf("%s: %v", expression, err)
		}
		var out struct {
			Exact   string `json:"exact"`
			Decimal string `json:"decimal"`
		}
		if json.Unmarshal([]byte(raw), &out) != nil || out.Exact != want || out.Decimal == "" {
			t.Fatalf("%s: %s", expression, raw)
		}
	}
	for _, expression := range []string{"1/0", "1%0", "1.1%2", "2^1000000", "2^(1/2)", strings.Repeat("(", 70) + "1" + strings.Repeat(")", 70), strings.Repeat("1+", 1000) + "1", "os.Open(\"secret\")", "1<<3", "NaN", "1e99999", strings.Repeat("9", 1025)} {
		args, _ := json.Marshal(map[string]string{"expression": expression})
		if _, err := d.Run(context.Background(), atoms.Invocation{}, string(args)); err == nil {
			t.Errorf("accepted unsafe expression %q", expression)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = d.Run(ctx, atoms.Invocation{}, `{"expression":"1+1"}`); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation swallowed")
	}
}
func TestCurrentTimeHasEmbeddedZoneAndRejectsOverrides(t *testing.T) {
	r, _ := atoms.New(atoms.Options{})
	d := namedTool(t, r, "current_time")
	for _, args := range []string{`{}`, `{"timezone":"UTC"}`, `{"timezone":"America/New_York"}`} {
		raw, err := d.Run(context.Background(), atoms.Invocation{}, args)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Time     string `json:"time"`
			Timezone string `json:"timezone"`
		}
		if json.Unmarshal([]byte(raw), &out) != nil {
			t.Fatal(raw)
		}
		at, err := time.Parse(time.RFC3339Nano, out.Time)
		if err != nil || time.Since(at) > time.Minute {
			t.Fatalf("invalid clock %s", raw)
		}
		if args == `{}` && out.Timezone != "Asia/Shanghai" {
			t.Fatal("wrong default zone")
		}
	}
	if _, err := d.Run(context.Background(), atoms.Invocation{}, `{"timezone":"No/Such_Zone"}`); err == nil {
		t.Fatal("invalid zone accepted")
	}
	if _, err := d.Validate(`{"timezone":"UTC","chat_id":"foreign"}`); err == nil {
		t.Fatal("scope override accepted")
	}
}

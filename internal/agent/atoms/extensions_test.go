package atoms

import (
	"context"
	"encoding/json"
	"testing"
)

func TestExtensionsUseSameSchemaPolicyAndExecutionContract(t *testing.T) {
	called := false
	r, err := New(Options{Extensions: []Definition{{Name: "migrated_effect", Description: "classified business effect", SideEffect: true, Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`), Run: func(_ context.Context, in Invocation, raw string) (string, error) {
		called = in.ActorID == "actual-actor" && raw == `{"id":"selected"}`
		return `{"status":"ok"}`, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	var d Definition
	for _, item := range r.Definitions() {
		if item.Name == "migrated_effect" {
			d = item
		}
	}
	if d.Digest == "" || d.AutoExecute || !d.SideEffect {
		t.Fatalf("lost policy: %+v", d)
	}
	if _, err = d.Validate(`{"id":"selected","actor_id":"forged"}`); err == nil {
		t.Fatal("accepted caller authority")
	}
	args, err := d.Validate(`{"id":"selected"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Run(context.Background(), Invocation{ActorID: "actual-actor"}, args); err != nil || !called {
		t.Fatalf("extension not callable: %v", err)
	}
	for _, policy := range r.Policies() {
		if policy.Name == d.Name && policy.Digest == d.Digest && policy.SideEffect && !policy.AutoExecute {
			return
		}
	}
	t.Fatal("extension absent from persisted policies")
}

func TestExtensionsCannotShadowBuiltinsOrBypassValidation(t *testing.T) {
	run := func(context.Context, Invocation, string) (string, error) { return "", nil }
	for _, d := range []Definition{
		{Name: "current_time", Run: run},
		{Name: "unclassified", AutoExecute: true, Run: run},
		{Name: "invalid_schema", Parameters: json.RawMessage(`{"type":false}`), Run: run},
	} {
		if _, err := New(Options{Extensions: []Definition{d}}); err == nil {
			t.Fatalf("accepted invalid extension %s", d.Name)
		}
	}
}

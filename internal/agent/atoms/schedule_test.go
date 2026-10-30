package atoms_test

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"strings"
	"testing"
)

func TestScheduleAtomsUseTypedPlanAndOwnerMutations(t *testing.T) {
	if len(atoms.ScheduleDefinitions(atoms.Options{})) != 0 {
		t.Fatal("scheduler registered without store")
	}
	defs := atoms.ScheduleDefinitions(atoms.Options{Tasks: &pgstore.Store{}})
	r, err := atoms.New(atoms.Options{Extensions: defs})
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 4 {
		t.Fatalf("definitions %d", len(defs))
	}
	create := namedTool(t, r, "schedule_create")
	update := namedTool(t, r, "schedule_update")
	if !create.SideEffect || !create.AutoExecute || !update.SideEffect || !update.AutoExecute {
		t.Fatal("wrong execution classification")
	}
	for _, raw := range []string{`{"text":"喝水","start_at":"2026-11-01T10:00:00+08:00","interval_seconds":60,"runs":3}`} {
		if _, err = create.Validate(raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`{"text":"喝水","start_at":"2026-11-01T10:00:00+08:00","interval_seconds":1,"runs":3}`, `{"text":"喝水","start_at":"2026-11-01T10:00:00+08:00","interval_seconds":60,"runs":1000}`, `{"prompt":"run arbitrary model","cron":"* * * * *"}`} {
		if _, err = create.Validate(raw); err == nil {
			t.Fatalf("unsafe schedule accepted %s", raw)
		}
	}
	for _, action := range []string{"pause", "resume", "cancel"} {
		if _, err = update.Validate(`{"id":"schedule_1","action":"` + action + `"}`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = update.Validate(`{"id":"schedule_1","action":"edit","text":"new"}`); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(create.Description, "arbitrary prompt") {
		t.Fatal("unexpected prompt scheduler")
	}
	for _, name := range []string{"schedule_list", "schedule_query"} {
		d := namedTool(t, r, name)
		if d.SideEffect {
			t.Fatalf("read owns mutation %s", name)
		}
	}
}
func TestScheduleQuerySchemaAdvertisesOnlyConfiguredReadLeaves(t *testing.T) {
	public, err := atoms.PublicDataDefinitions(atoms.PublicDataOptions{AKShareURL: "https://akshare.example"})
	if err != nil {
		t.Fatal(err)
	}
	defs := atoms.ScheduleDefinitions(atoms.Options{Tasks: &pgstore.Store{}, Extensions: public})
	r, err := atoms.New(atoms.Options{Extensions: defs})
	if err != nil {
		t.Fatal(err)
	}
	create := namedTool(t, r, "schedule_create")
	if _, err = create.Validate(`{"query":{"tool_name":"gold_price_get","arguments":{"days":1}},"start_at":"2026-11-01T10:00:00+08:00","interval_seconds":86400,"runs":3}`); err != nil {
		t.Fatalf("typed data schedule rejected %v", err)
	}
	for _, name := range []string{"music_search", "feishu_send_message", "oneword_get"} {
		if _, err = create.Validate(`{"query":{"tool_name":"` + name + `","arguments":{}},"start_at":"2026-11-01T10:00:00+08:00","interval_seconds":60,"runs":3}`); err == nil {
			t.Fatalf("unconfigured/effect leaf %s", name)
		}
	}
}

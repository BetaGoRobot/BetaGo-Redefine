package main

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
)

func TestIndependentCapabilityWiringRegistersOnlyConfiguredBusinessProviders(t *testing.T) {
	c := config.Config{}
	defs, err := capabilityDefinitions(c, &components{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if d.Name == "music_search" || d.Name == "gold_price_get" || d.Name == "luckin_order_create" {
			t.Fatal("unconfigured business advertised")
		}
	}
	c.Capabilities = config.Capabilities{AKShareURL: "http://127.0.0.1:1", HitokotoURL: "http://127.0.0.1:2", MusicURL: "http://127.0.0.1:3"}
	defs, err = capabilityDefinitions(c, &components{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, d := range defs {
		seen[d.Name] = true
	}
	for _, name := range []string{"music_search", "music_song_detail", "gold_price_get", "finance_market_data_get", "finance_tool_discover", "oneword_get"} {
		if !seen[name] {
			t.Errorf("configured domain absent: %s", name)
		}
	}
	if seen["luckin_order_create"] || seen["music_send_audio"] {
		t.Fatal("unconfigured external side effect enabled")
	}
}

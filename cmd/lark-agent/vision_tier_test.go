package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
)

func TestVisionConfigurationPreservesFlexTier(t *testing.T) {
	const model = "doubao-seed-2-1-lite-260915"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model       string          `json:"model"`
			ServiceTier string          `json:"service_tier"`
			Messages    json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/chat/completions" || body.Model != model || body.ServiceTier != "flex" || !strings.Contains(string(body.Messages), "image_url") {
			t.Error("vision request lost its model, flex tier, or image")
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OCR TEST 12345"}}]}`)
	}))
	defer s.Close()
	b, err := os.ReadFile("../../deploy/group-agent/agent.example.toml")
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(key string) (string, bool) {
		switch {
		case key == "AGENT_NEXT_VISION_MODEL":
			return model, true
		case strings.HasSuffix(key, "_ENDPOINT"):
			return s.URL, true
		case strings.HasSuffix(key, "_KEY"):
			return "fixture-key", true
		case strings.HasSuffix(key, "_MODEL"):
			return "fixture-model", true
		}
		return "", false
	}
	c, err := config.Decode(strings.NewReader(string(b)), "models", lookup)
	if err != nil {
		t.Fatal(err)
	}
	vision, err := modelapi.NewChat(endpoint(c.Models.Vision))
	if err != nil {
		t.Fatal(err)
	}
	text, err := vision.Describe(context.Background(), "image/png", []byte("synthetic image fixture"))
	if err != nil || text != "OCR TEST 12345" {
		t.Fatalf("vision extraction failed: %v", err)
	}
}

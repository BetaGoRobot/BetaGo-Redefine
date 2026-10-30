package config

import (
	"os"
	"strings"
	"testing"
)

func example(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../../deploy/group-agent/agent.example.toml")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func modelEnvironment(key string) (string, bool) {
	switch key {
	case "AGENT_NEXT_ARK_ENDPOINT":
		return "https://ark.example.test/api/v3", true
	case "AGENT_NEXT_ARK_API_KEY":
		return "private-key", true
	case "AGENT_NEXT_EMBEDDING_MODEL":
		return "private-embedding-endpoint", true
	case "AGENT_NEXT_RERANK_MODEL":
		return "rank-model", true
	case "AGENT_NEXT_MODEL_GATEWAY_KEY":
		return "gateway-secret", true
	}
	return "", false
}

func TestModelsModeStartsWithoutDatabaseOrLarkCredentials(t *testing.T) {
	c, err := Decode(strings.NewReader(example(t)), "models", modelEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	if c.Models.Embedding.Dimensions != 1024 || c.Models.Embedding.Model != "private-embedding-endpoint" || c.Database.URL != "" {
		t.Fatal("wrong role or unresolved irrelevant fields")
	}
}

func TestWebSearchRoleCanUseSharedOrIndependentEndpoint(t *testing.T) {
	c, err := Decode(strings.NewReader(example(t)), "agent", allEnvironment)
	if err != nil || c.Models.WebSearch.BaseURL != "https://ark.example.test/api/v3" || c.Models.WebSearch.APIKey != "private-key" || c.Models.WebSearch.Protocol != "ark_responses" {
		t.Fatalf("search role fallback failed: %v", err)
	}
	lookup := func(k string) (string, bool) {
		switch k {
		case "AGENT_NEXT_WEB_SEARCH_ENDPOINT":
			return "https://search.example.test/v3", true
		case "AGENT_NEXT_WEB_SEARCH_API_KEY":
			return "separate-search-key", true
		case "AGENT_NEXT_WEB_SEARCH_MODEL":
			return "selected-search-model", true
		}
		return allEnvironment(k)
	}
	c, err = Decode(strings.NewReader(example(t)), "agent", lookup)
	if err != nil || c.Models.WebSearch.Model != "selected-search-model" || c.Models.WebSearch.APIKey != "separate-search-key" || c.Models.Answer.Model == c.Models.WebSearch.Model {
		t.Fatalf("search role cannot change independently: %v", err)
	}
}

func TestAgentModeRequiresOwnIdentityAndDatabase(t *testing.T) {
	_, err := Decode(strings.NewReader(example(t)), "agent", modelEnvironment)
	if err == nil {
		t.Fatal("agent accepted missing operational credentials")
	}
	if strings.Contains(err.Error(), "private-key") || strings.Contains(err.Error(), "gateway-secret") {
		t.Fatal("error leaked credentials")
	}
}

func TestEmbeddingDimensionsCannotDriftFromMemoryDeployment(t *testing.T) {
	lookup := func(key string) (string, bool) {
		if key == "AGENT_NEXT_EMBEDDING_DIMENSIONS" {
			return "2048", true
		}
		return modelEnvironment(key)
	}
	if _, err := Decode(strings.NewReader(example(t)), "models", lookup); err == nil {
		t.Fatal("gateway dimensions differ from memory deployment")
	}
}

func TestEnvironmentValuesCannotInjectTOML(t *testing.T) {
	secret := "key\"\n[database]\nurl=\"injected"
	lookup := func(key string) (string, bool) {
		if key == "AGENT_NEXT_ARK_API_KEY" {
			return secret, true
		}
		return modelEnvironment(key)
	}
	_, err := Decode(strings.NewReader(example(t)), "models", lookup)
	if err == nil {
		t.Fatal("header credential with newline accepted")
	}
	if strings.Contains(err.Error(), "injected") {
		t.Fatal("credential included in error")
	}
	lookup = func(key string) (string, bool) {
		if key == "AGENT_NEXT_ARK_API_KEY" {
			return "key\"literal", true
		}
		return modelEnvironment(key)
	}
	c, err := Decode(strings.NewReader(example(t)), "models", lookup)
	if err != nil || c.Models.Embedding.APIKey != "key\"literal" || c.Database.URL != "" {
		t.Fatal("environment value was parsed as TOML")
	}
}

func TestInvalidConfigurationReportsSafeErrors(t *testing.T) {
	for name, text := range map[string]string{
		"unknown field":        example(t) + "\nmisspelled_option = true\n",
		"oversize":             strings.Repeat("# comment\n", 150000),
		"embedding dimensions": strings.Replace(example(t), "dimensions = 1024", "dimensions = 0", 1),
		"unsupported protocol": strings.Replace(example(t), "ark_multimodal", "made_up_protocol", 1),
		"endpoint credentials": strings.Replace(example(t), "${AGENT_NEXT_EMBEDDING_ENDPOINT}", "https://user:private-password@ark.example.test", -1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(text), "models", modelEnvironment)
			if err == nil {
				t.Fatal("invalid config accepted")
			}
			if strings.Contains(err.Error(), "private-password") {
				t.Fatal("error leaked credentials")
			}
		})
	}
	if _, err := Decode(strings.NewReader(example(t)), "unknown", modelEnvironment); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestModelRolesOverrideEndpointsAndKeysIndependently(t *testing.T) {
	lookup := func(key string) (string, bool) {
		switch key {
		case "AGENT_NEXT_EMBEDDING_ENDPOINT":
			return "https://embed.example.test/v1", true
		case "AGENT_NEXT_EMBEDDING_API_KEY":
			return "embedding-only-key", true
		case "AGENT_NEXT_RERANK_ENDPOINT":
			return "https://rank.example.test/v1", true
		case "AGENT_NEXT_RERANK_API_KEY":
			return "ranking-only-key", true
		}
		return modelEnvironment(key)
	}
	c, err := Decode(strings.NewReader(example(t)), "models", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if c.Models.Embedding.BaseURL == c.Models.Rerank.BaseURL || c.Models.Embedding.APIKey != "embedding-only-key" || c.Models.Rerank.APIKey != "ranking-only-key" {
		t.Fatal("model roles share credentials or endpoints")
	}
}

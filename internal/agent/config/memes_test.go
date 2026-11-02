package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMemeReferencesDefaultDisabledAndUseIndependentServerEnvironment(t *testing.T) {
	c, err := Decode(strings.NewReader(example(t)), "agent", allEnvironment)
	if err != nil || c.Memes.Enabled {
		t.Fatalf("meme references must default disabled: %v", err)
	}
	values := map[string]string{"AGENT_NEXT_MEMES_ENABLED": "true", "AGENT_NEXT_MEMES_SEARCH_ENDPOINT": "http://betago-meme-milvus:19530", "AGENT_NEXT_MEMES_SEARCH_USERNAME": "catalog-user", "AGENT_NEXT_MEMES_SEARCH_PASSWORD": "private-meme-password", "AGENT_NEXT_MEMES_DATABASE_URL": "postgres://meme:private-db-password@catalog/memes", "AGENT_NEXT_MEMES_ALLOWED_CHAT_IDS": "chat-a,chat-b"}
	lookup := func(key string) (string, bool) {
		if v, ok := values[key]; ok {
			return v, true
		}
		return allEnvironment(key)
	}
	c, err = Decode(strings.NewReader(example(t)), "agent", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Memes.Enabled || c.Memes.Schema != "meme_reply" || c.Memes.Scope != "wiki" || c.Memes.Dimensions != 2048 || c.Models.Embedding.Dimensions != 1024 || c.Memes.QueryModel != c.Models.Answer.Model || c.Memes.EmbeddingModel != c.Models.Embedding.Model || len(c.Memes.AllowedChatIDs) != 2 || c.Memes.DatabaseURL == c.Database.URL {
		t.Fatal("meme settings lost independent corpus or existing model configuration")
	}
	encoded, _ := json.Marshal(c.Memes)
	for _, secret := range []string{"private-db-password", "private-meme-password", "catalog-user"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("meme configuration exported a credential")
		}
	}
	delete(values, "AGENT_NEXT_MEMES_DATABASE_URL")
	c, err = Decode(strings.NewReader(example(t)), "agent", lookup)
	if err != nil || c.Memes.DatabaseURL != c.Database.URL {
		t.Fatalf("default catalog database is not the NEXT database: %v", err)
	}
}

func TestMemeReferencesDefaultToIndependentMilvus(t *testing.T) {
	lookup := func(key string) (string, bool) {
		if key == "AGENT_NEXT_MEMES_ENABLED" {
			return "true", true
		}
		if key == "AGENT_NEXT_MEILI_KEY" {
			return "message-search-secret", true
		}
		return allEnvironment(key)
	}
	text := strings.ReplaceAll(example(t), `search_endpoint = "http://betago-meme-milvus:19530"`, "")
	c, err := Decode(strings.NewReader(text), "agent", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Memes.Enabled || c.Memes.SearchEndpoint != "http://betago-meme-milvus:19530" || c.Memes.SearchEndpoint == c.Search.Endpoint || c.Memes.SearchUsername != "" || c.Memes.SearchPassword != "" {
		t.Fatal("Milvus endpoint or credentials inherited another search role")
	}
	if c.Memes.Dimensions != 2048 || c.Models.Embedding.Dimensions != 1024 {
		t.Fatal("meme dimensions changed memory embedding")
	}
}

func TestMemeReferencesConfigurationRejectsInvalidEnabledSettings(t *testing.T) {
	for _, bad := range []string{"not-bool"} {
		lookup := func(key string) (string, bool) {
			if key == "AGENT_NEXT_MEMES_ENABLED" {
				return bad, true
			}
			return allEnvironment(key)
		}
		if _, err := Decode(strings.NewReader(example(t)), "agent", lookup); err == nil {
			t.Fatal("invalid enabled meme configuration accepted")
		}
	}
	text := strings.Replace(example(t), `schema = "meme_reply"`, `schema = "invalid-schema"`, 1)
	lookup := func(key string) (string, bool) {
		if key == "AGENT_NEXT_MEMES_ENABLED" {
			return "true", true
		}
		return allEnvironment(key)
	}
	if _, err := Decode(strings.NewReader(text), "agent", lookup); err == nil {
		t.Fatal("invalid meme schema accepted")
	}
}

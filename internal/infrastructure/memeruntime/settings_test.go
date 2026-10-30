package memeruntime

import "testing"

func TestSettingsNormalizeRequiresExplicitInfrastructureWhenEnabled(t *testing.T) {
	s, err := (Settings{}).Normalize()
	if err != nil || s.Enabled {
		t.Fatalf("disabled defaults: %+v %v", s, err)
	}
	if _, err = (Settings{Enabled: true}).Normalize(); err == nil {
		t.Fatal("enabled missing corpus infrastructure accepted")
	}
	cfg, err := (Settings{Enabled: true, DatabaseURL: "postgres://fixture", SearchEndpoint: "http://localhost:9200", QueryModel: "query", EmbeddingModel: "vectors"}).Normalize()
	if err != nil || cfg.Scope != "wiki" || cfg.Dimensions != 2048 || cfg.Schema != "meme_reply" || cfg.RequestTimeoutSeconds != 12 {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	for _, change := range []func(*Settings){func(c *Settings) { c.Schema = "x;drop" }, func(c *Settings) { c.Dimensions = 16001 }, func(c *Settings) { c.QueryTimeoutSeconds = 30 }, func(c *Settings) { c.SearchEndpoint = "http://user:pass@host" }} {
		bad := cfg
		change(&bad)
		if _, err = bad.Normalize(); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestEnabledSettingsDefaultToIndependentMilvusEndpoint(t *testing.T) {
	settings, err := (Settings{Enabled: true, DatabaseURL: "postgres://fixture", QueryModel: "query", EmbeddingModel: "vectors"}).Normalize()
	if err != nil || settings.SearchEndpoint != "http://betago-meme-milvus:19530" || settings.SearchUsername != "" || settings.SearchPassword != "" {
		t.Fatalf("Milvus defaults: %+v %v", settings, err)
	}
	settings.SearchEndpoint = "https://milvus.internal:19530"
	settings.SearchUsername, settings.SearchPassword = "meme-user", "meme-password"
	explicit, err := settings.Normalize()
	if err != nil || explicit.SearchEndpoint != settings.SearchEndpoint || explicit.SearchUsername != settings.SearchUsername || explicit.SearchPassword != settings.SearchPassword {
		t.Fatalf("explicit Milvus credentials changed: %v", err)
	}
}

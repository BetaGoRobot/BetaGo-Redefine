package main

import (
	"testing"

	cfg "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memeruntime"
)

func TestMasterMemeDefaultsUseIndependentMilvusWithoutEnabling(t *testing.T) {
	disabled, err := masterMemeSettings(&cfg.BaseConfig{})
	if err != nil || disabled.Enabled {
		t.Fatalf("disabled defaults: %+v %v", disabled, err)
	}
	input := &cfg.BaseConfig{MemeConfig: &memeruntime.Settings{Enabled: true}, DBConfig: &cfg.DBConfig{Host: "database.internal", DBName: "fixture"}, OpensearchConfig: &cfg.OpensearchConfig{Domain: "search.internal", User: "user", Password: "secret"}, ArkConfig: &cfg.ArkConfig{NormalModel: "query", EmbeddingModel: "embedding"}}
	result, err := masterMemeSettings(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.SearchEndpoint != "http://betago-meme-milvus:19530" || result.SearchUsername != "" || result.SearchPassword != "" || result.QueryModel != "query" || result.EmbeddingModel != "embedding" || result.Scope != "wiki" {
		t.Fatalf("wrong infrastructure defaults: %+v", result)
	}
	if input.MemeConfig.SearchEndpoint != "" {
		t.Fatal("normalization mutated config supplied by caller")
	}
	input.ArkConfig.LiteModel = "light-query"
	light, err := masterMemeSettings(input)
	if err != nil || light.QueryModel != "query" {
		t.Fatalf("query default did not preserve the configured normal model: %v", err)
	}
}

func TestMasterMemeCredentialsOnlyComeFromMemeSettings(t *testing.T) {
	input := &cfg.BaseConfig{MemeConfig: &memeruntime.Settings{Enabled: true, SearchEndpoint: "http://milvus.internal:19530", SearchUsername: "meme-user", SearchPassword: "meme-password"}, DBConfig: &cfg.DBConfig{Host: "database.internal", DBName: "fixture"}, OpensearchConfig: &cfg.OpensearchConfig{Domain: "search.internal", User: "other-user", Password: "other-password"}, ArkConfig: &cfg.ArkConfig{NormalModel: "query", EmbeddingModel: "embedding"}}
	settings, err := masterMemeSettings(input)
	if err != nil || settings.SearchEndpoint != input.MemeConfig.SearchEndpoint || settings.SearchUsername != "meme-user" || settings.SearchPassword != "meme-password" {
		t.Fatalf("explicit Milvus configuration changed: %v", err)
	}
}

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	infraConfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memeruntime"
)

func TestHelpDescribesOfflineSourceSync(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help exit=%d: %s", code, stderr.String())
	}
	for _, flag := range []string{"-dry-run", "-wiki-base", "-offset", "-limit", "-generation", "-model", "-dimensions", "-schema", "-index-endpoint", "-wiki-interval", "-wiki-retries", "-source-workers", "-source-cache-dir", "-fetch-workers"} {
		if !strings.Contains(stdout.String(), flag) {
			t.Fatalf("help missing %s: %s", flag, stdout.String())
		}
	}
	if !strings.Contains(stdout.String(), "Milvus") || strings.Contains(stdout.String(), "OpenSearch") || strings.Contains(stdout.String(), "-index-password") {
		t.Fatal("help does not describe independent Milvus configuration")
	}
}

func TestDryRunOnlyFetchesPublicSourcesWithoutConfigModelOrWrites(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Query().Has("q") {
			t.Error("dry-run must not query Wiki with conversation keywords")
		}
		if r.URL.Path == "/api/v1/public/memes" {
			fmt.Fprint(w, `{"schema_version":1,"items":[{"slug":"one"}],"limit":1,"offset":0,"has_more":true}`)
			return
		}
		if r.URL.Path == "/api/v1/public/memes/one" {
			fmt.Fprint(w, `{"schema_version":1,"item":{"slug":"one","title":"测试词条","content_html":"<p>原始描述</p>"}}`)
			return
		}
		t.Errorf("non-Wiki request during dry-run: %s", r.URL)
		http.NotFound(w, r)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--dry-run", "--wiki-interval", "0", "--config", "/does/not/exist/config.toml", "--wiki-base", server.URL, "--index-endpoint", server.URL, "--limit", "1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("dry-run exit=%d err=%s out=%s", code, stderr.String(), stdout.String())
	}
	if len(paths) != 2 {
		t.Fatalf("dry-run requested model/index endpoint: %v", paths)
	}
	for _, text := range []string{"dry_run=true", "source_count=1", "card_count=0", "model_calls=0", "writes=0", "catalog_complete=false", "next_offset=1"} {
		if !strings.Contains(stdout.String(), text) {
			t.Fatalf("missing %s: %s", text, stdout.String())
		}
	}
}

func TestDryRunDoesNotHideMissingSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/public/memes" {
			fmt.Fprint(w, `{"schema_version":1,"items":[{"slug":"missing"}],"limit":1,"offset":0,"has_more":false}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--dry-run", "--wiki-interval", "0", "--wiki-base", server.URL, "--limit", "1"}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stdout.String(), "errors=1") || !strings.Contains(stdout.String(), "Wiki HTTP status 404") {
		t.Fatalf("missing detail treated as success: code=%d out=%s err=%s", code, stdout.String(), stderr.String())
	}
}

func TestMilvusImportOptionsUseOnlyMemeInfrastructureAndEnvironmentCredentials(t *testing.T) {
	t.Setenv("MEME_INDEX_USERNAME", "")
	t.Setenv("MEME_INDEX_PASSWORD", "")
	config := &infraConfig.BaseConfig{OpensearchConfig: &infraConfig.OpensearchConfig{Domain: "unrelated-search.internal", User: "os-user", Password: "os-password"}}
	options := indexOptionsForImport(config, "", "memes", "embedding", 3)
	if options.Endpoint != "http://betago-meme-milvus:19530" || options.Username != "" || options.Password != "" {
		t.Fatal("Milvus inherited unrelated OpenSearch settings")
	}
	config.MemeConfig = &memeruntime.Settings{SearchEndpoint: "http://milvus.internal:19530", SearchUsername: "meme-user", SearchPassword: "meme-password"}
	options = indexOptionsForImport(config, "", "memes", "embedding", 3)
	if options.Endpoint != config.MemeConfig.SearchEndpoint || options.Username != "meme-user" || options.Password != "meme-password" {
		t.Fatal("explicit meme infrastructure ignored")
	}
	options = indexOptionsForImport(config, "http://override.internal:19530", "memes", "embedding", 3)
	if options.Endpoint != "http://override.internal:19530" {
		t.Fatal("CLI Milvus endpoint did not override configured endpoint")
	}
	t.Setenv("MEME_INDEX_USERNAME", "env-user")
	t.Setenv("MEME_INDEX_PASSWORD", "env-password")
	options = indexOptionsForImport(config, "", "memes", "embedding", 3)
	if options.Username != "env-user" || options.Password != "env-password" {
		t.Fatal("environment credentials did not override meme credentials")
	}
}

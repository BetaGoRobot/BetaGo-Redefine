package memesource

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWikiCatalogPaginatesWithoutUserKeywordsAndKeepsDetailProvenance(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RequestURI())
		if r.Method != http.MethodGet || r.URL.Query().Has("q") || r.URL.Query().Has("tag") || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("catalog fetch must be unauthenticated browsing: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/public/memes" {
			switch r.URL.Query().Get("offset") {
			case "0":
				fmt.Fprint(w, `{"schema_version":1,"items":[{"slug":"甲"},{"slug":"乙"}],"limit":2,"offset":0,"has_more":true}`)
			case "2":
				fmt.Fprint(w, `{"schema_version":1,"items":[{"slug":"乙"},{"slug":"丙"}],"limit":2,"offset":2,"has_more":false}`)
			default:
				t.Errorf("unexpected page: %s", r.URL)
			}
			return
		}
		slug := strings.TrimPrefix(r.URL.Path, "/api/v1/public/memes/")
		json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "item": map[string]any{
			"slug": slug, "title": slug + "标题", "summary": "简短摘要", "updated_at": "2026-10-01T00:00:00Z", "content_html": "<h2>用法</h2><p>甲<b>乙</b> &amp; 丙</p><script>忽略</script>",
			"source_video": map[string]any{"bvid": "BV1234567890", "creator_name": "来源作者"},
		}})
	}))
	defer server.Close()
	client, err := NewWikiClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := client.Catalog(context.Background(), CatalogOptions{PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Sources) != 3 || len(catalog.Errors) != 0 || !catalog.Complete || catalog.NextOffset != 4 || catalog.Pages != 2 {
		t.Fatalf("catalog: %+v", catalog)
	}
	source := catalog.Sources[0]
	if source.ID != "wiki:甲" || source.Description != "用法\n甲乙 & 丙" || source.Item.Title != "甲标题" || source.Item.UpdatedAt == nil || source.Item.SourceVideo == nil || source.Item.SourceVideo.CreatorName == nil || source.Revision == "" {
		t.Fatalf("lost source fields: %+v", source)
	}
	if !strings.Contains(string(source.RawDetail), "content_html") || source.URL != server.URL+"/api/v1/public/memes/%E7%94%B2" || !json.Valid(source.RawCatalogItem) {
		t.Fatalf("lost raw provenance: %+v", source)
	}
	if len(requests) != 5 {
		t.Fatalf("duplicate slug fetched twice: %v", requests)
	}
}

func TestWikiCatalogLimitAndOffsetAreResumable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/public/memes" {
			if r.URL.Query().Get("offset") != "17" || r.URL.Query().Get("limit") != "1" {
				t.Errorf("pagination: %s", r.URL)
			}
			fmt.Fprint(w, `{"schema_version":1,"items":[{"slug":"one"}],"limit":1,"offset":17,"has_more":true}`)
			return
		}
		fmt.Fprint(w, `{"schema_version":1,"item":{"slug":"one","title":"一","summary":"用法","content_html":""}}`)
	}))
	defer server.Close()
	client, _ := NewWikiClient(server.URL, server.Client())
	catalog, err := client.Catalog(context.Background(), CatalogOptions{PageSize: 30, Offset: 17, Limit: 1})
	if err != nil || len(catalog.Sources) != 1 || catalog.Complete || !catalog.HasMore || catalog.NextOffset != 18 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
}

func TestWikiCatalogRecordsDetailFailureAndRejectsUnknownSchema(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/public/memes" {
					fmt.Fprintf(w, `{"schema_version":%d,"items":[{"slug":"gone"}],"limit":1,"offset":0,"has_more":false}`, version)
					return
				}
				http.Error(w, "private source error body", http.StatusNotFound)
			}))
			defer server.Close()
			client, _ := NewWikiClient(server.URL, server.Client())
			catalog, err := client.Catalog(context.Background(), CatalogOptions{PageSize: 1})
			if version == 2 && err == nil {
				t.Fatal("accepted unknown schema")
			}
			if version == 1 && (err != nil || len(catalog.Errors) != 1 || catalog.Errors[0].SourceID != "wiki:gone") {
				t.Fatalf("missing detail failure: %+v %v", catalog, err)
			}
			if strings.Contains(fmt.Sprint(catalog.Errors), "private source") {
				t.Fatal("leaked upstream response body")
			}
		})
	}
}

func TestWikiRevisionDependsOnFullRawDetail(t *testing.T) {
	content := "<p>第一版</p>"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/public/memes" {
			fmt.Fprint(w, `{"schema_version":1,"items":[{"slug":"one"}],"limit":1,"offset":0,"has_more":false}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "item": map[string]any{"slug": "one", "title": "一", "content_html": content}})
	}))
	defer server.Close()
	client, _ := NewWikiClient(server.URL, server.Client())
	one, _ := client.Catalog(context.Background(), CatalogOptions{PageSize: 1})
	two, _ := client.Catalog(context.Background(), CatalogOptions{PageSize: 1})
	content = "<p>第二版</p>"
	three, _ := client.Catalog(context.Background(), CatalogOptions{PageSize: 1})
	if one.Sources[0].Revision != two.Sources[0].Revision || one.Sources[0].Revision == three.Sources[0].Revision {
		t.Fatal("revision does not identify immutable detail content")
	}
}

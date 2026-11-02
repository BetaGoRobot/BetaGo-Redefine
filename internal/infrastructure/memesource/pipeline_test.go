package memesource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func pipelineServer(t *testing.T, detail func(http.ResponseWriter, *http.Request, string)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/public/memes" {
			fmt.Fprint(w, `{"schema_version":1,"items":[{"slug":"one"},{"slug":"two"}],"limit":2,"offset":0,"has_more":false}`)
			return
		}
		detail(w, r, strings.TrimPrefix(r.URL.Path, "/api/v1/public/memes/"))
	}))
	t.Cleanup(server.Close)
	return server
}
func pipelineDetail(w http.ResponseWriter, slug string) {
	fmt.Fprintf(w, `{"schema_version":1,"item":{"slug":%q,"title":%q,"summary":"original source","content_html":""}}`, slug, slug)
}

func TestWikiFetchWorkersAreConcurrentAndAggregateDirectoryOrder(t *testing.T) {
	second := make(chan struct{})
	var once sync.Once
	server := pipelineServer(t, func(w http.ResponseWriter, r *http.Request, slug string) {
		if slug == "two" {
			once.Do(func() { close(second) })
		} else {
			select {
			case <-second:
			case <-r.Context().Done():
				return
			}
		}
		pipelineDetail(w, slug)
	})
	wiki, _ := NewWikiClient(server.URL, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	catalog, err := wiki.Catalog(ctx, CatalogOptions{PageSize: 2, FetchWorkers: 2})
	if err != nil || len(catalog.Sources) != 2 || catalog.Sources[0].ID != "wiki:one" || catalog.Sources[1].ID != "wiki:two" {
		t.Fatalf("fetches remained serial or reordered: %+v %v", catalog, err)
	}
}

func TestSyncProgressReportsSafeFailureBeforeRemainingSourceFinishes(t *testing.T) {
	for _, streamed := range []bool{false, true} {
		t.Run(fmt.Sprint(streamed), func(t *testing.T) {
			server := pipelineServer(t, func(w http.ResponseWriter, _ *http.Request, slug string) { pipelineDetail(w, slug) })
			wiki, _ := NewWikiClient(server.URL, server.Client())
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			catalog, err := wiki.Catalog(ctx, CatalogOptions{PageSize: 2})
			if err != nil {
				t.Fatal(err)
			}
			reported := make(chan struct{})
			var once sync.Once
			var failure *SourceError
			fixture, _ := (&fakeCompletion{}).Complete(context.Background(), memes.CompletionRequest{})
			model := completionFunc(func(callCtx context.Context, in memes.CompletionRequest) (memes.CompletionResponse, error) {
				var input struct {
					Source memes.WikiSource `json:"source"`
				}
				_ = json.Unmarshal(in.InputJSON, &input)
				if input.Source.ID == "wiki:one" {
					return memes.CompletionResponse{}, errors.New("PRIVATE_PROVIDER_ERROR_BODY")
				}
				select {
				case <-reported:
					return fixture, nil
				case <-callCtx.Done():
					return memes.CompletionResponse{}, callCtx.Err()
				}
			})
			store := newMemoryStore()
			publisher := &fakePublisher{store: store, verified: true}
			options := SyncOptions{ModelID: "model", Progress: func(p SyncProgress) {
				if p.Failure != nil {
					copy := *p.Failure
					failure = &copy
					once.Do(func() { close(reported) })
				}
			}}
			deps := SyncDependencies{Store: store, Model: model, Publisher: publisher}
			var report SyncReport
			if streamed {
				report, err = SyncFromWiki(ctx, wiki, CatalogOptions{PageSize: 2, FetchWorkers: 2}, deps, options)
			} else {
				report, err = Sync(ctx, catalog, deps, options)
			}
			if !errors.Is(err, ErrIncomplete) || report.EncodedSources != 1 || publisher.calls != 0 || failure == nil || failure.SourceID != "wiki:one" || failure.Stage != "encode" || strings.Contains(failure.Error, "PRIVATE_") {
				t.Fatalf("failure was delayed or unsafe: %+v %+v %v", report, failure, err)
			}
		})
	}
}

func TestPipelineCancellationStopsFetchAndModelWorkers(t *testing.T) {
	fetchStarted := make(chan struct{})
	fetchExited := make(chan struct{})
	modelStarted := make(chan struct{})
	var once sync.Once
	var active atomic.Int32
	server := pipelineServer(t, func(w http.ResponseWriter, r *http.Request, slug string) {
		if slug == "two" {
			close(fetchStarted)
			<-r.Context().Done()
			close(fetchExited)
			return
		}
		pipelineDetail(w, slug)
	})
	wiki, _ := NewWikiClient(server.URL, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	model := completionFunc(func(callCtx context.Context, _ memes.CompletionRequest) (memes.CompletionResponse, error) {
		active.Add(1)
		defer active.Add(-1)
		once.Do(func() { close(modelStarted) })
		<-callCtx.Done()
		return memes.CompletionResponse{}, callCtx.Err()
	})
	go func() { <-fetchStarted; <-modelStarted; cancel() }()
	store := newMemoryStore()
	publisher := &fakePublisher{store: store, verified: true}
	report, err := SyncFromWiki(ctx, wiki, CatalogOptions{PageSize: 2, FetchWorkers: 2}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model", SourceWorkers: 2})
	if !errors.Is(err, context.Canceled) || active.Load() != 0 || publisher.calls != 0 || report.Active {
		t.Fatalf("cancel left work or activated: %+v %v active=%d", report, err, active.Load())
	}
	select {
	case <-fetchExited:
	case <-time.After(time.Second):
		t.Fatal("HTTP fetch was not cancelled")
	}
}

func TestPipelineStartsEncodingBeforeSlowFetchEndsAndKeepsSourceOrder(t *testing.T) {
	encoded := make(chan struct{})
	var once sync.Once
	var modelCalls atomic.Int32
	server := pipelineServer(t, func(w http.ResponseWriter, r *http.Request, slug string) {
		if slug == "two" {
			select {
			case <-encoded:
			case <-r.Context().Done():
				return
			}
		}
		pipelineDetail(w, slug)
	})
	wiki, _ := NewWikiClient(server.URL, server.Client())
	fixture, _ := (&fakeCompletion{}).Complete(context.Background(), memes.CompletionRequest{})
	model := completionFunc(func(_ context.Context, in memes.CompletionRequest) (memes.CompletionResponse, error) {
		modelCalls.Add(1)
		once.Do(func() { close(encoded) })
		return fixture, nil
	})
	store := newMemoryStore()
	publisher := &fakePublisher{store: store, verified: true}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	report, err := SyncFromWiki(ctx, wiki, CatalogOptions{PageSize: 2, FetchWorkers: 2}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model", SourceWorkers: 2})
	if err != nil || !report.Active || modelCalls.Load() != 2 || report.CardCount != 2 || !report.CatalogComplete {
		t.Fatalf("fetch/encode did not overlap: %+v %v calls=%d", report, err, modelCalls.Load())
	}
	for i, slug := range []string{"one", "two"} {
		if !strings.Contains(publisher.records[i].Full.Sources[0].Scope, "source_id=wiki:"+slug) {
			t.Fatal("publication lost directory order")
		}
	}
	// The streamed path uses exactly the same immutable encoder cache profile.
	catalog, err := wiki.Catalog(ctx, CatalogOptions{PageSize: 2, FetchWorkers: 2})
	if err != nil {
		t.Fatal(err)
	}
	reused, err := Sync(ctx, catalog, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model"})
	if err != nil || reused.ReusedSources != 2 || modelCalls.Load() != 2 {
		t.Fatalf("pipeline changed cache profile: %+v %v", reused, err)
	}
}

func TestPipelineFetchFailureNeverPublishesSuccessfulPartialEncoding(t *testing.T) {
	encoded := make(chan struct{})
	var once sync.Once
	server := pipelineServer(t, func(w http.ResponseWriter, r *http.Request, slug string) {
		if slug == "two" {
			select {
			case <-encoded:
			case <-r.Context().Done():
				return
			}
			http.NotFound(w, r)
			return
		}
		pipelineDetail(w, slug)
	})
	wiki, _ := NewWikiClient(server.URL, server.Client())
	fixture, _ := (&fakeCompletion{}).Complete(context.Background(), memes.CompletionRequest{})
	model := completionFunc(func(context.Context, memes.CompletionRequest) (memes.CompletionResponse, error) {
		once.Do(func() { close(encoded) })
		return fixture, nil
	})
	store := newMemoryStore()
	publisher := &fakePublisher{store: store, verified: true}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	report, err := SyncFromWiki(ctx, wiki, CatalogOptions{PageSize: 2, FetchWorkers: 2}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model", SourceWorkers: 2})
	if !errors.Is(err, ErrIncomplete) || publisher.calls != 0 || report.Active || report.EncodedSources != 1 || len(report.Errors) != 1 {
		t.Fatalf("partial source published or good cache lost: %+v %v", report, err)
	}
}

func TestPipelineOutOfOrderModelsAndCancellationAreBounded(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			server := pipelineServer(t, func(w http.ResponseWriter, _ *http.Request, slug string) { pipelineDetail(w, slug) })
			wiki, _ := NewWikiClient(server.URL, server.Client())
			fixture, _ := (&fakeCompletion{}).Complete(context.Background(), memes.CompletionRequest{})
			second := make(chan struct{})
			var once sync.Once
			var active atomic.Int32
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			model := completionFunc(func(callCtx context.Context, in memes.CompletionRequest) (memes.CompletionResponse, error) {
				active.Add(1)
				defer active.Add(-1)
				var input struct {
					Source memes.WikiSource `json:"source"`
				}
				_ = json.Unmarshal(in.InputJSON, &input)
				if input.Source.ID == "wiki:two" {
					once.Do(func() { close(second) })
					if cancelled {
						cancel()
					}
				}
				if input.Source.ID == "wiki:one" {
					select {
					case <-second:
					case <-callCtx.Done():
					}
				}
				if callCtx.Err() != nil {
					return memes.CompletionResponse{}, callCtx.Err()
				}
				return fixture, nil
			})
			store := newMemoryStore()
			publisher := &fakePublisher{store: store, verified: true}
			report, err := SyncFromWiki(ctx, wiki, CatalogOptions{PageSize: 2, FetchWorkers: 2}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model", SourceWorkers: 2})
			if active.Load() != 0 {
				t.Fatal("model worker leaked after return")
			}
			if cancelled {
				if !errors.Is(err, context.Canceled) || publisher.calls != 0 || report.Active {
					t.Fatalf("cancellation published: %+v %v", report, err)
				}
				return
			}
			if err != nil || !report.Active || !strings.Contains(publisher.records[0].Full.Sources[0].Scope, "wiki:one") || !strings.Contains(publisher.records[1].Full.Sources[0].Scope, "wiki:two") {
				t.Fatalf("out of order completion reordered publication: %+v %v", report, err)
			}
		})
	}
}

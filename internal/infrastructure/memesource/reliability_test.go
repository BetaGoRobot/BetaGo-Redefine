package memesource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

func TestWikiRetryHonorsCancellationAndDoesNotRetryPermanentFailure(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if status == http.StatusServiceUnavailable {
					w.Header().Set("Retry-After", "1")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			client, err := NewWikiClientWithOptions(server.URL, server.Client(), WikiClientOptions{MaxRetries: 3, RetryDelay: time.Millisecond, MaxRetryAfter: 2 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			_, err = client.Catalog(ctx, CatalogOptions{Limit: 1})
			if status == http.StatusServiceUnavailable && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("retry did not respect context: %v", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("unexpected retry before deadline/permanent status: %d", calls.Load())
			}
		})
	}
}

func TestWikiRetryRecoversTransientStatusAndReportsSerialProgress(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/public/memes" {
			if calls.Add(1) < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, `{"schema_version":1,"items":[{"slug":"one"}],"limit":1,"offset":0,"has_more":false}`)
			return
		}
		fmt.Fprint(w, `{"schema_version":1,"item":{"slug":"one","title":"one","summary":"source","content_html":""}}`)
	}))
	defer server.Close()
	var progress []CatalogProgress
	client, err := NewWikiClientWithOptions(server.URL, server.Client(), WikiClientOptions{MaxRetries: 2, RetryDelay: time.Millisecond, Progress: func(p CatalogProgress) { progress = append(progress, p) }})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := client.Catalog(context.Background(), CatalogOptions{PageSize: 1})
	if err != nil || len(catalog.Sources) != 1 || !catalog.Complete || calls.Load() != 3 {
		t.Fatalf("transient failure did not recover: %+v %v", catalog, err)
	}
	if len(progress) != 2 || progress[0].Phase != "page" || progress[1].Phase != "source" || progress[1].Sources != 1 {
		t.Fatalf("missing progress: %+v", progress)
	}
}

func TestWikiSourceCacheReusesExactCatalogAndRejectsCorruptSnapshot(t *testing.T) {
	var detailCalls atomic.Int32
	version := 1
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/public/memes" {
			fmt.Fprintf(w, `{"schema_version":1,"items":[{"slug":"one","summary":"revision-%d"}],"limit":1,"offset":0,"has_more":false}`, version)
			return
		}
		detailCalls.Add(1)
		fmt.Fprintf(w, `{"schema_version":1,"item":{"slug":"one","title":"one","summary":"source-%d","content_html":""}}`, version)
	}))
	defer server.Close()
	dir := t.TempDir()
	client, err := NewWikiClientWithOptions(server.URL, server.Client(), WikiClientOptions{CacheDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Catalog(context.Background(), CatalogOptions{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Catalog(context.Background(), CatalogOptions{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if detailCalls.Load() != 1 || first.Sources[0].Revision != second.Sources[0].Revision {
		t.Fatal("unchanged public snapshot was not reused")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) != 1 {
		t.Fatalf("cache files=%v", files)
	}
	if err = os.WriteFile(files[0], []byte(`{"revision":"forged","detail":{"schema_version":1,"item":{"slug":"one","title":"forged","summary":"forged"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	third, err := client.Catalog(context.Background(), CatalogOptions{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if detailCalls.Load() != 2 || third.Sources[0].Revision != first.Sources[0].Revision {
		t.Fatal("corrupt cache trusted or failed to recover")
	}
	version = 2
	fourth, err := client.Catalog(context.Background(), CatalogOptions{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if detailCalls.Load() != 3 || fourth.Sources[0].Revision == first.Sources[0].Revision {
		t.Fatal("catalog revision change reused old detail")
	}
}

func TestSyncBoundedSourceWorkersPreserveOrderCacheAndFailureBoundary(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			store := newMemoryStore()
			publisher := &fakePublisher{store: store, verified: true}
			one, two := testSource(), testSource()
			two.ID = "wiki:second"
			two.Item.Slug = "second"
			two.Revision = ""
			two.Revision, _ = memes.Fingerprint(two)
			fixture, _ := (&fakeCompletion{}).Complete(context.Background(), memes.CompletionRequest{})
			started := make(chan struct{}, 2)
			release := make(chan struct{})
			var active, max atomic.Int32
			model := completionFunc(func(ctx context.Context, in memes.CompletionRequest) (memes.CompletionResponse, error) {
				var input struct {
					Source memes.WikiSource `json:"source"`
				}
				_ = json.Unmarshal(in.InputJSON, &input)
				n := active.Add(1)
				defer active.Add(-1)
				for old := max.Load(); n > old && !max.CompareAndSwap(old, n); old = max.Load() {
				}
				started <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return memes.CompletionResponse{}, ctx.Err()
				}
				if fail && input.Source.ID == two.ID {
					return memes.CompletionResponse{}, errors.New("provider failed")
				}
				return fixture, nil
			})
			go func() { <-started; <-started; close(release) }()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var progress []SyncProgress
			report, err := Sync(ctx, Catalog{Sources: []Source{one, two}}, SyncDependencies{Store: store, Model: model, Publisher: publisher}, SyncOptions{ModelID: "model", SourceWorkers: 2, Progress: func(p SyncProgress) {
				if p.Phase == "source" {
					progress = append(progress, p)
				}
			}})
			if max.Load() != 2 {
				t.Fatalf("sources were not concurrent: max=%d err=%v", max.Load(), err)
			}
			if len(progress) != 2 || progress[0].SourceID != one.ID || progress[1].SourceID != two.ID {
				t.Fatalf("progress/cache aggregation lost catalog order: %+v", progress)
			}
			if fail {
				if !errors.Is(err, ErrIncomplete) || publisher.calls != 0 || report.Active || report.EncodedSources != 1 {
					t.Fatalf("partial result activated or lost good cache: %+v %v", report, err)
				}
				return
			}
			if err != nil || publisher.calls != 1 || !report.Active || report.CardCount != 2 {
				t.Fatalf("parallel sync: %+v %v", report, err)
			}
			if publisher.records[0].Full.Sources[0].Scope == publisher.records[1].Full.Sources[0].Scope {
				t.Fatal("source identity lost")
			}
			for _, entry := range store.order[2:4] {
				if entry != "meme_card_versions" {
					t.Fatalf("cache writes not serialized: %v", store.order)
				}
			}
		})
	}
}

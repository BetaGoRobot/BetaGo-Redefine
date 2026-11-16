package music

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNeteasePipelineAccountFailureNegativeCacheIncludesPreview(t *testing.T) {
	var accounts atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			accounts.Add(1)
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"code":200,"data":[{"id":42,"code":200,"url":"https://m801.music.126.net/preview.mp3","freeTrialInfo":{"start":0,"end":30}}]}`)
	}))
	defer api.Close()
	p := newNeteasePipeline(AnonymousNetease{BaseURL: api.URL}, "", "test-account")
	now := time.Now()
	p.now = func() time.Time { return now }
	track := Track{ID: "netease:42", Source: "netease", ExternalID: "42"}
	for i := 0; i < 2; i++ {
		r, _ := p.Resolve(context.Background(), track)
		if r.Status != "preview" {
			t.Fatalf("lost legitimate preview: %#v", r)
		}
	}
	if accounts.Load() != 1 {
		t.Fatalf("preview failure retried account within negative TTL: %d", accounts.Load())
	}
	now = now.Add(16 * time.Second)
	p.Resolve(context.Background(), track)
	if accounts.Load() != 2 {
		t.Fatalf("negative TTL did not expire: %d", accounts.Load())
	}
}

func TestNeteasePipelineBudgetAndNegativeTTLWorkWithoutMinIO(t *testing.T) {
	var accounts, anonymous atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if r.Header.Get("Cookie") != "" {
			accounts.Add(1)
			fmt.Fprintf(w, `{"code":200,"data":[{"id":%s,"code":200,"url":"https://m801.music.126.net/account.mp3"}]}`, id)
			return
		}
		anonymous.Add(1)
		fmt.Fprintf(w, `{"code":200,"data":[{"id":%s,"code":200,"url":null}]}`, id)
	}))
	defer api.Close()
	p := newNeteasePipeline(AnonymousNetease{BaseURL: api.URL}, "", "test-account")
	now := time.Now()
	p.now = func() time.Time { return now }
	for i := 1; i <= 14; i++ {
		id := strconv.Itoa(i)
		r, _ := p.Resolve(context.Background(), Track{ID: "netease:" + id, Source: "netease", ExternalID: id})
		if (r.Status == "available") != (i <= 12) {
			t.Fatalf("unexpected account budget at %d: %#v", i, r)
		}
	}
	if accounts.Load() != 12 {
		t.Fatalf("account budget was bypassed: %d", accounts.Load())
	}
	blocked := Track{ID: "netease:14", Source: "netease", ExternalID: "14"}
	p.Resolve(context.Background(), blocked)
	if anonymous.Load() != 14 {
		t.Fatal("negative outcome was not cached")
	}
	now = now.Add(16 * time.Second)
	p.Resolve(context.Background(), blocked)
	if anonymous.Load() != 15 || accounts.Load() != 12 {
		t.Fatal("negative TTL/budget boundary wrong")
	}
	now = now.Add(time.Minute)
	r, _ := p.Resolve(context.Background(), blocked)
	if r.Status != "available" || accounts.Load() != 13 {
		t.Fatal("budget window never recovered")
	}
}

func TestNeteasePipelineAccountConcurrencyAndSameIDCoalescing(t *testing.T) {
	var active, maximum, accounts atomic.Int32
	entered := make(chan struct{}, 20)
	release := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if r.Header.Get("Cookie") == "" {
			fmt.Fprintf(w, `{"code":200,"data":[{"id":%s,"code":200,"url":null}]}`, id)
			return
		}
		accounts.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		entered <- struct{}{}
		<-release
		fmt.Fprintf(w, `{"code":200,"data":[{"id":%s,"code":200,"url":"https://m801.music.126.net/account.mp3"}]}`, id)
	}))
	defer api.Close()
	p := newNeteasePipeline(AnonymousNetease{BaseURL: api.URL}, "", "test-account")
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Resolve(context.Background(), Track{ID: "netease:42", Source: "netease", ExternalID: "42"})
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("account not entered")
	}
	// Other IDs share the same global account slots; cache writing is absent.
	for i := 1; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := strconv.Itoa(i)
			p.Resolve(context.Background(), Track{ID: "netease:" + id, Source: "netease", ExternalID: id})
		}(i)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("second account slot not entered")
	}
	time.Sleep(30 * time.Millisecond)
	if maximum.Load() > 2 || accounts.Load() != 2 {
		t.Errorf("global concurrency or same-ID coalescing failed: max=%d starts=%d", maximum.Load(), accounts.Load())
	}
	close(release)
	wg.Wait()
}

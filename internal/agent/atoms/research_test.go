package atoms

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/web"
)

type researchPageFixture struct {
	page  web.Page
	reads int
}

func (f *researchPageFixture) Read(context.Context, string) (web.Page, error) {
	f.reads++
	return f.page, nil
}
func researchNamed(t *testing.T, r *Registry, name string) Definition {
	t.Helper()
	for _, d := range r.Definitions() {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("missing %s", name)
	return Definition{}
}
func TestResearchEvidenceUsesExactCurrentLocalOriginalAndByteOffsets(t *testing.T) {
	m := sourceLocalMessage()
	m.Text = "alpha 世界 alpha"
	f := &localSourceFixture{message: m}
	r, err := New(Options{Extensions: ResearchDefinitions(Options{Sources: f})})
	if err != nil {
		t.Fatal(err)
	}
	d := researchNamed(t, r, "research_extract_evidence")
	in := Invocation{Task: domain.Task{Scope: m.Source.Scope}}
	out, err := d.Run(context.Background(), in, `{"message_id":"message","quote":"世界"}`)
	if err != nil {
		t.Fatal(err)
	}
	var ev struct {
		Start int    `json:"start_byte"`
		End   int    `json:"end_byte"`
		Quote string `json:"quote"`
	}
	if json.Unmarshal([]byte(out), &ev) != nil || ev.Start != 6 || ev.End != 12 || ev.Quote != "世界" {
		t.Fatalf("fabricated offsets: %s", out)
	}
	refs, err := d.Sources(out)
	if err != nil || len(refs) != 1 || refs[0] != m.Source || f.reads != 0 {
		t.Fatalf("local provenance/remote read: %+v %v reads=%d", refs, err, f.reads)
	}
	if _, err = d.Run(context.Background(), in, `{"message_id":"message","quote":"invented"}`); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("invented quote accepted: %v", err)
	}
	f.message.Deleted = true
	if _, err = d.Run(context.Background(), in, `{"message_id":"message","quote":"世界"}`); !errors.Is(err, domain.ErrStaleSource) {
		t.Fatalf("deleted source used: %v", err)
	}
}
func TestResearchWebEvidenceRefetchesAndLedgerDeduplicatesVerifiedSources(t *testing.T) {
	m := sourceLocalMessage()
	local := &localSourceFixture{message: m}
	pages := &researchPageFixture{page: web.Page{URL: "https://example.org/article", Text: "the current page", Title: "Article", FetchedAt: time.Now(), Untrusted: true}}
	opts := Options{Sources: local, WebPages: pages}
	r, err := New(Options{Extensions: ResearchDefinitions(opts)})
	if err != nil {
		t.Fatal(err)
	}
	in := Invocation{Task: domain.Task{Scope: m.Source.Scope}}
	d := researchNamed(t, r, "research_extract_evidence")
	if _, err = d.Run(context.Background(), in, `{"url":"https://example.org/article","quote":"current"}`); err != nil {
		t.Fatal(err)
	}
	pages.page.Text = "replacement page"
	if _, err = d.Run(context.Background(), in, `{"url":"https://example.org/article","quote":"current"}`); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("old fetched quote trusted: %v", err)
	}
	ledger := researchNamed(t, r, "research_source_ledger")
	out, err := ledger.Run(context.Background(), in, `{"message_ids":["message","message"],"urls":["https://example.org/article","https://example.org/article"]}`)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Entries []any              `json:"entries"`
		Sources []domain.SourceRef `json:"sources"`
	}
	if json.Unmarshal([]byte(out), &v) != nil || len(v.Entries) != 2 || len(v.Sources) != 1 || strings.Contains(out, "approval") {
		t.Fatalf("ledger echoes identities rather than verifying: %s", out)
	}
	if _, err = ledger.Validate(`{"message_ids":["message"],"labels":["official"]}`); err == nil {
		t.Fatal("model authority label accepted")
	}
}

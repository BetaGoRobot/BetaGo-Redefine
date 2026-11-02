package atoms_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/web"
)

type webFixture struct{ query, url string }

func (f *webFixture) Search(_ context.Context, q string, n int) (web.SearchResult, error) {
	f.query = q
	return web.SearchResult{Query: q, Text: "public result", Untrusted: true}, nil
}

type unavailableWeb struct{}

func (unavailableWeb) Search(context.Context, string, int) (web.SearchResult, error) {
	return web.SearchResult{}, errors.New("private credential=secret")
}
func (unavailableWeb) Read(context.Context, string) (web.Page, error) {
	return web.Page{}, errors.New("private credential=secret")
}
func TestUnavailableWebReadReturnsSafeObservationWithoutAbortingConversation(t *testing.T) {
	r, err := atoms.New(atoms.Options{WebSearch: unavailableWeb{}, WebPages: unavailableWeb{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []atoms.Definition{namedTool(t, r, "read_webpage"), namedTool(t, r, "web_search")} {
		args := `{"query":"public topic"}`
		if d.Name == "read_webpage" {
			args = `{"url":"https://example.org"}`
		}
		out, err := d.Run(context.Background(), atoms.Invocation{}, args)
		if err != nil || !strings.Contains(out, `"available":false`) || strings.Contains(out, "secret") || strings.Contains(out, "credential") {
			t.Fatalf("read failure lost useful/credential-safe observation: %s %v", out, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err = d.Run(ctx, atoms.Invocation{}, args); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation converted to ordinary tool result")
		}
	}
}
func (f *webFixture) Read(_ context.Context, u string) (web.Page, error) {
	f.url = u
	return web.Page{URL: u, Text: "untrusted web text", Untrusted: true}, nil
}
func TestWebToolsAreReadOnlyScopedArguments(t *testing.T) {
	f := &webFixture{}
	r, err := atoms.New(atoms.Options{WebSearch: f, WebPages: f})
	if err != nil {
		t.Fatal(err)
	}
	defs := []atoms.Definition{namedTool(t, r, "read_webpage"), namedTool(t, r, "web_search")}
	if len(defs) != 2 {
		t.Fatalf("web capabilities omitted: %d", len(defs))
	}
	for _, d := range defs {
		if d.SideEffect {
			t.Fatalf("ordinary public read asks approval: %s", d.Name)
		}
		a := `{"query":"Go release history","limit":3}`
		if d.Name == "read_webpage" {
			a = `{"url":"https://go.dev/doc/devel/release"}`
		} else if d.Name != "web_search" {
			t.Fatal("wrong names")
		}
		valid, err := d.Validate(a)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = d.Run(context.Background(), atoms.Invocation{}, valid); err != nil {
			t.Fatal(err)
		}
		if _, err = d.Validate(`{"query":"a","chat_id":"other"}`); err == nil {
			t.Fatal("scope selector accepted")
		}
	}
	if f.query != "Go release history" || f.url != "https://go.dev/doc/devel/release" {
		t.Fatal("arguments not usable")
	}
}

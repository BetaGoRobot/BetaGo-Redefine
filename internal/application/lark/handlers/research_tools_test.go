package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
)

func TestResearchReadURLHandleReadsHTMLAndReturnsStructuredResult(t *testing.T) {
	originalFetcher := researchReadURLFetcher
	defer func() {
		researchReadURLFetcher = originalFetcher
	}()

	researchReadURLFetcher = func(ctx context.Context, rawURL string) (researchFetchedDocument, error) {
		return researchFetchedDocument{
			FinalURL:    "https://example.com/articles/agentic",
			ContentType: "text/html; charset=utf-8",
			Body: `<html><head><title>Agentic Runtime Notes</title><meta property="article:published_time" content="2026-03-22T08:00:00Z"></head><body><main><p>Agentic runtime needs durable turns.</p><p>Deep research needs read_url and evidence extraction.</p></main></body></html>`,
		}, nil
	}

	arg, err := ResearchReadURL.ParseTool(`{"url":"https://example.com/articles/agentic","max_chars":64}`)
	if err != nil {
		t.Fatalf("ParseTool() error = %v", err)
	}

	meta := &xhandler.BaseMetaData{}
	if err := ResearchReadURL.Handle(context.Background(), nil, meta, arg); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	raw, _ := meta.GetExtra(researchReadURLResultKey)
	if !strings.Contains(raw, `"title":"Agentic Runtime Notes"`) {
		t.Fatalf("result = %q, want contain title", raw)
	}
	if !strings.Contains(raw, `"url":"https://example.com/articles/agentic"`) {
		t.Fatalf("result = %q, want contain canonical url", raw)
	}
	if !strings.Contains(raw, `"published_at":"2026-03-22T08:00:00Z"`) {
		t.Fatalf("result = %q, want contain published time", raw)
	}
	if !strings.Contains(raw, `Agentic runtime needs durable turns.`) {
		t.Fatalf("result = %q, want contain extracted text", raw)
	}
	if !strings.Contains(raw, `"truncated":true`) {
		t.Fatalf("result = %q, want contain truncated flag", raw)
	}
}

func TestResearchExtractEvidenceHandleFindsRelevantPassages(t *testing.T) {
	arg, err := ResearchExtractEvidence.ParseTool(`{
		"document_text":"Durable agent runtime keeps runs and steps. Deep research needs read_url, evidence extraction, and citations. A single web search is usually not enough.",
		"questions":["What does deep research need?"],
		"max_quotes":2
	}`)
	if err != nil {
		t.Fatalf("ParseTool() error = %v", err)
	}

	meta := &xhandler.BaseMetaData{}
	if err := ResearchExtractEvidence.Handle(context.Background(), nil, meta, arg); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	raw, _ := meta.GetExtra(researchExtractEvidenceResultKey)
	if !strings.Contains(raw, `"question":"What does deep research need?"`) {
		t.Fatalf("result = %q, want contain question", raw)
	}
	if !strings.Contains(raw, `Deep research needs read_url, evidence extraction, and citations.`) {
		t.Fatalf("result = %q, want contain relevant passage", raw)
	}
}

func TestResearchSourceLedgerHandleMergesAndDeduplicatesSources(t *testing.T) {
	arg, err := ResearchSourceLedger.ParseTool(`{
		"existing_sources":[
			{"title":"Existing Note","url":"https://example.com/a","published_at":"2026-03-20T00:00:00Z"}
		],
		"new_sources":[
			{"title":"Existing Note Duplicate","url":"https://example.com/a?ref=dup","published_at":"2026-03-20T00:00:00Z"},
			{"title":"Fresh Source","url":"https://docs.example.org/b","published_at":"2026-03-21T00:00:00Z"}
		]
	}`)
	if err != nil {
		t.Fatalf("ParseTool() error = %v", err)
	}

	meta := &xhandler.BaseMetaData{}
	if err := ResearchSourceLedger.Handle(context.Background(), nil, meta, arg); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	raw, _ := meta.GetExtra(researchSourceLedgerResultKey)
	if strings.Count(raw, `"url":"`) != 2 {
		t.Fatalf("result = %q, want exactly 2 deduplicated sources", raw)
	}
	if !strings.Contains(raw, `"citation":"[1] Existing Note - example.com - 2026-03-20"`) {
		t.Fatalf("result = %q, want contain first citation", raw)
	}
	if !strings.Contains(raw, `"citation":"[2] Fresh Source - docs.example.org - 2026-03-21"`) {
		t.Fatalf("result = %q, want contain second citation", raw)
	}
}

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/meili"
)

func TestRealPGMeiliPipelineModelFailureMediaEditDelete(t *testing.T) {
	url, key := os.Getenv("AGENT_TEST_MEILI_URL"), os.Getenv("AGENT_TEST_MEILI_API_KEY")
	if url == "" || key == "" {
		t.Skip("isolated Meili required")
	}
	s, _, db, schema := fixture(t)
	ctx := context.Background()
	messages, contexts := schema+"_messages", schema+"_contexts"
	index, err := meili.New(meili.Options{Endpoint: url, APIKey: key, MessagesIndex: messages, ContextsIndex: contexts, VectorDimensions: 2, EmbeddingModel: "synthetic", PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err = index.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, name := range []string{messages, contexts} {
			r, _ := http.NewRequest("DELETE", url+"/indexes/"+name, nil)
			r.Header.Set("Authorization", "Bearer "+key)
			resp, e := http.DefaultClient.Do(r)
			if e != nil {
				t.Error("isolated index cleanup failed")
				continue
			}
			resp.Body.Close()
		}
	})
	e := event("media", 1)
	e.Message.Text = "原文测试 synthetic"
	e.Message.Media = []domain.Media{{Type: domain.MediaImage, ResourceID: "synthetic-image", MIME: "image/png"}}
	if _, err = s.Accept(ctx, e); err != nil {
		t.Fatal(err)
	}
	mem := &memory{}
	w, _ := New(Config{Store: s, Index: index, Memory: mem, Embedder: embedding{fmt.Errorf("offline model")}, Owner: "real-pipeline", Lease: time.Second})
	if err = w.Step(ctx, domain.JobIndexMessage); err != nil {
		t.Fatal(err)
	}
	q := domain.Query{Scope: scope, Anchor: time.Now().UTC(), Text: "synthetic", Mode: domain.QueryLiteral, Limit: 10}
	hits, err := index.Literal(ctx, q)
	if err != nil || len(hits) != 1 || hits[0].Text != e.Message.Text {
		t.Fatalf("literal unavailable without models %v %v", hits, err)
	}
	mediaJob, err := s.ClaimJob(ctx, []domain.JobKind{domain.JobMedia}, "synthetic", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutMedia(ctx, mediaJob, domain.MediaArtifact{Source: e.Message.Source, Parts: []domain.MediaPart{{Text: "视觉资料 transcript budget 427", Page: 3, Object: domain.Object{ResourceID: "synthetic-image"}}}}); err != nil {
		t.Fatal(err)
	}
	s.CompleteJob(ctx, mediaJob)
	if err = w.Step(ctx, domain.JobContexts); err != nil {
		t.Fatal(err)
	}
	if err = w.Step(ctx, domain.JobEmbedContext); err == nil {
		t.Fatal("model failure hidden")
	}
	hits, err = index.Literal(ctx, q)
	if err != nil || len(hits) != 1 {
		t.Fatal("raw search lost after embedding failure")
	}
	w.c.Embedder = embedding{}
	db.Exec(`UPDATE "` + schema + `".jobs SET available_at=clock_timestamp() WHERE kind='embed_context'`)
	if err = w.Step(ctx, domain.JobEmbedContext); err != nil {
		t.Fatal(err)
	}
	q.Mode = domain.QueryDiscussion
	q.Text = "transcript"
	hits, err = index.Keyword(ctx, q)
	if err != nil || len(hits) != 1 {
		t.Fatalf("media interpretation not indexed %v %v", hits, err)
	}
	oldRefs := append([]domain.SourceRef(nil), hits[0].Sources...)
	edit := event("media", 2)
	edit.Message.Text = "edited latest"
	if _, err = s.Accept(ctx, edit); err != nil {
		t.Fatal(err)
	}
	valid, err := s.ValidSources(ctx, q, oldRefs)
	if err != nil || valid {
		t.Fatal("edit not immediately rejected at source store")
	}
	if err = w.Step(ctx, domain.JobCleanup); err != nil {
		t.Fatal(err)
	}
	q.Text = "transcript"
	hits, err = index.Keyword(ctx, q)
	if err != nil || len(hits) != 0 {
		t.Fatal("stale media context remains after cleanup")
	}
	if err = w.Step(ctx, domain.JobIndexMessage); err != nil {
		t.Fatal(err)
	}
	q.Mode = domain.QueryLiteral
	q.Text = "latest"
	hits, err = index.Literal(ctx, q)
	if err != nil || len(hits) != 1 {
		t.Fatal("edited canonical message missing")
	}
	del := event("media", 3)
	del.ID = "delete"
	del.Message.EventID = del.ID
	del.Type = domain.EventDelete
	del.Message.Deleted = true
	del.Message.Source.AuthorID = ""
	del.Message.Raw = json.RawMessage(`{}`)
	if _, err = s.Accept(ctx, del); err != nil {
		t.Fatal(err)
	}
	if err = w.Step(ctx, domain.JobCleanup); err != nil {
		t.Fatal(err)
	}
	if err = w.Step(ctx, domain.JobIndexMessage); err != nil {
		t.Fatal(err)
	}
	hits, err = index.Literal(ctx, q)
	if err != nil || len(hits) != 0 {
		t.Fatal("deleted message remains externally indexed")
	}
}

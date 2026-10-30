package meili

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func realClient(t *testing.T) (*Client, Options) {
	t.Helper()
	endpoint, secret := os.Getenv("AGENT_TEST_MEILI_URL"), os.Getenv("AGENT_TEST_MEILI_API_KEY")
	if endpoint == "" || secret == "" {
		t.Skip("set AGENT_TEST_MEILI_URL and AGENT_TEST_MEILI_API_KEY for isolated Meilisearch integration")
	}
	o := options(endpoint)
	o.APIKey = secret
	o.Timeout = 15 * time.Second
	o.PollInterval = 5 * time.Millisecond
	prefix := "clean_meili_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	o.MessagesIndex = prefix + "_messages"
	o.ContextsIndex = prefix + "_contexts"
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, index := range []string{o.MessagesIndex, o.ContextsIndex} {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := c.mutate(ctx, "DELETE", "/indexes/"+index, nil)
			cancel()
			if err != nil {
				t.Errorf("isolated index cleanup: %v", err)
			}
		}
	})
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c, o
}

func ids(hits []domain.Evidence) map[string]bool {
	m := map[string]bool{}
	for _, hit := range hits {
		for _, s := range hit.Sources {
			m[s.MessageID] = true
		}
	}
	return m
}

func TestRealMeiliLiteralProvenanceAndExactTimeBoundaries(t *testing.T) {
	c, _ := realClient(t)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("idempotent production initialization: %v", err)
	}
	ctx := context.Background()
	base := message()
	base.Source.Version = 1_792_663_200_123_456_789
	base.Source.MessageID = "both"
	base.Text = "预算 审批 方案已经通过 approval"
	anchor := base.UpdatedAt
	docs := []domain.Message{base}
	add := func(id, text string, change func(*domain.Message)) {
		m := base
		m.Source.MessageID = id
		m.Text = text
		if change != nil {
			change(&m)
		}
		docs = append(docs, m)
	}
	add("one", "预算", nil)
	add("phrase-separated", "方案需要调整 已经重新讨论 通过后再通知", nil)
	add("future-source", "预算 审批", func(m *domain.Message) {
		m.Source.OccurredAt = anchor.Add(time.Nanosecond)
		m.UpdatedAt = m.Source.OccurredAt
	})
	add("future-edit", "预算 审批", func(m *domain.Message) { m.UpdatedAt = anchor.Add(time.Nanosecond) })
	add("future-source-second", "预算 审批", func(m *domain.Message) {
		m.Source.OccurredAt = anchor.Add(time.Second)
		m.UpdatedAt = m.Source.OccurredAt
	})
	add("other-author", "预算 审批", func(m *domain.Message) { m.Source.AuthorID = "someone-else" })
	add("other-tenant", "预算 审批", func(m *domain.Message) { m.Source.Scope.TenantID = "other" })
	add("other-app", "预算 审批", func(m *domain.Message) { m.Source.Scope.AppID = "other" })
	add("other-chat", "预算 审批", func(m *domain.Message) { m.Source.Scope.ChatID = "other" })
	add("other-visibility", "预算 审批", func(m *domain.Message) { m.Source.Scope.Visibility = "other" })
	for _, m := range docs {
		if err := c.UpsertMessage(ctx, m); err != nil {
			t.Fatalf("index %s: %v", m.Source.MessageID, err)
		}
	}
	q := query()
	q.Anchor = anchor
	q.Text = "预算 审批"
	q.AuthorID = base.Source.AuthorID
	hits, err := c.Literal(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids(hits), map[string]bool{"both": true}) {
		t.Fatalf("all terms/scopes/exact nanosecond anchors: %#v", ids(hits))
	}
	if len(hits) != 1 || !reflect.DeepEqual(hits[0].Sources, []domain.SourceRef{base.Source}) {
		t.Fatalf("precise >2^53 version / nanosecond provenance: %#v", hits)
	}
	for _, tc := range []struct {
		text string
		want int
	}{{`"方案已经通过"`, 1}, {"approv", 0}, {"apprvoal", 0}} {
		q.Text = tc.text
		hits, err = c.Literal(ctx, q)
		if err != nil || len(hits) != tc.want {
			t.Fatalf("literal %q: %#v %v", tc.text, ids(hits), err)
		}
	}
	q.Text = "预算 审批"
	q.Anchor = anchor.Add(time.Nanosecond)
	hits, err = c.Literal(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids(hits), map[string]bool{"both": true, "future-source": true, "future-edit": true}) {
		t.Fatalf("one-nanosecond boundary: %#v", ids(hits))
	}
	// Replacing a stable scoped document cannot leave a previous version behind.
	base.Text = "替换后的记录"
	base.Source.Version++
	base.UpdatedAt = anchor.Add(2 * time.Nanosecond)
	if err := c.UpsertMessage(ctx, base); err != nil {
		t.Fatal(err)
	}
	q.Anchor = base.UpdatedAt
	q.Text = "approval"
	hits, err = c.Literal(ctx, q)
	if err != nil || len(hits) != 0 {
		t.Fatalf("stale replacement retained: %#v %v", hits, err)
	}
	q.Text = base.Text
	hits, err = c.Literal(ctx, q)
	if err != nil || len(hits) != 1 || hits[0].Sources[0].Version != base.Source.Version {
		t.Fatalf("replacement provenance: %#v %v", hits, err)
	}
	base.Deleted = true
	base.Source.AuthorID = ""
	if err := c.UpsertMessage(ctx, base); err != nil {
		t.Fatal(err)
	}
	hits, err = c.Literal(ctx, q)
	if err != nil || len(hits) != 0 {
		t.Fatalf("tombstone: %#v %v", hits, err)
	}
	// Values are data inside the filter expression, even when valid provider
	// identifiers contain quotes, backslashes, operators, or HTML characters.
	special := message()
	special.Source.Scope = domain.Scope{TenantID: `tenant"<&`, AppID: `app\`, ChatID: `chat" OR chat_id = "other`, Visibility: `group>&`}
	special.Source.AuthorID = `a" OR chat_id = "other`
	special.Source.MessageID = "escaped"
	special.Text = "隔离转义"
	if err := c.UpsertMessage(ctx, special); err != nil {
		t.Fatal(err)
	}
	q = query()
	q.Scope = special.Source.Scope
	q.AuthorID = special.Source.AuthorID
	q.Text = special.Text
	hits, err = c.Literal(ctx, q)
	if err != nil || len(hits) != 1 || !reflect.DeepEqual(hits[0].Sources, []domain.SourceRef{special.Source}) {
		t.Fatalf("escaped filter value: %#v %v", hits, err)
	}
}

func TestRealMeiliExternalVectorsModelIsolationAndContextSources(t *testing.T) {
	c, o := realClient(t)
	ctx := context.Background()
	d := derived()
	d.Version = 1_792_663_200_123_456_788
	d.Sources[0].Version = 1_792_663_200_123_456_789
	second := d.Sources[0]
	second.MessageID = "msg-2"
	second.Version++
	second.OccurredAt = second.OccurredAt.Add(time.Nanosecond)
	d.Sources = append(d.Sources, second)
	if err := c.UpsertContext(ctx, d, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Version string `json:"version"`
	}
	if err := c.request(ctx, "GET", "/indexes/"+o.ContextsIndex+"/documents/"+documentID(d.Scope, d.ID), nil, &stored); err != nil || stored.Version != strconv.FormatInt(d.Version, 10) {
		t.Fatalf("context version above 2^53: %q %v", stored.Version, err)
	}
	other := d
	other.ID = "context-2"
	other.Text = "财务记录"
	other.Sources = append([]domain.SourceRef(nil), d.Sources...)
	other.Sources[0].MessageID = "msg-other"
	if err := c.UpsertContext(ctx, other, []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	future := d
	future.ID = "future-context"
	future.UpdatedAt = d.UpdatedAt.Add(time.Nanosecond)
	if err := c.UpsertContext(ctx, future, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	q := query()
	q.Mode = domain.QueryDiscussion
	q.Text = d.Text
	q.Anchor = d.UpdatedAt
	kw, err := c.Keyword(ctx, q)
	if err != nil || len(kw) != 1 || !reflect.DeepEqual(kw[0].Sources, d.Sources) {
		t.Fatalf("context keyword provenance: %#v %v", kw, err)
	}
	q.Limit = 1
	q.Text = "财务记录" // Pure vector ordering must ignore this text.
	vec, err := c.Vector(ctx, q, []float32{1, 0, 0})
	if err != nil || len(vec) != 1 || !reflect.DeepEqual(vec[0].Sources, d.Sources) || vec[0].Score <= 0 {
		t.Fatalf("pure external vector: %#v %v", vec, err)
	}
	// Equal dimensions with a new provider/config fingerprint cannot see old
	// vectors. The client only installs userProvided settings, never model calls.
	o.EmbeddingModel = "different-provider/model@v2;configuration-sha=abc"
	changed, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := changed.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	vec, err = changed.Vector(ctx, q, []float32{1, 0, 0})
	if err != nil || len(vec) != 0 {
		t.Fatalf("old-model vector leaked: %#v %v", vec, err)
	}
	q.Text = d.Text
	kw, err = changed.Keyword(ctx, q)
	if err != nil || len(kw) != 0 {
		t.Fatalf("old-model keyword context leaked: %#v %v", kw, err)
	}
	d.Version++
	if err := changed.UpsertContext(ctx, d, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	vec, err = changed.Vector(ctx, q, []float32{1, 0, 0})
	if err != nil || len(vec) != 1 || !reflect.DeepEqual(vec[0].Sources, d.Sources) {
		t.Fatalf("new-model context: %#v %v", vec, err)
	}
	d.Invalidated = true
	if err := changed.UpsertContext(ctx, d, nil); err != nil {
		t.Fatal(err)
	}
	vec, err = changed.Vector(ctx, q, []float32{1, 0, 0})
	if err != nil || len(vec) != 0 {
		t.Fatalf("invalidated context retained: %#v %v", vec, err)
	}
}

func TestRealMeiliCaseSensitiveScopeAndAuthorBeforeLimit(t *testing.T) {
	c, _ := realClient(t)
	ctx := context.Background()
	for _, field := range []string{"tenant", "app", "chat", "visibility", "author"} {
		t.Run(field, func(t *testing.T) {
			valid := message()
			valid.Source.Scope.ChatID = "chat-" + field
			valid.Source.MessageID = "valid-" + field
			valid.Text = "case " + field + " boundary"
			foreign := valid
			foreign.Source.MessageID = "foreign-" + field
			switch field {
			case "tenant":
				foreign.Source.Scope.TenantID = "TENANT"
			case "app":
				foreign.Source.Scope.AppID = "APP"
			case "chat":
				foreign.Source.Scope.ChatID = "CHAT-" + field
			case "visibility":
				foreign.Source.Scope.Visibility = "GROUP"
			case "author":
				foreign.Source.AuthorID = "AUTHOR"
			}
			// The case variant is deliberately indexed first, so accepting it inside
			// Meili's candidate limit would starve the exact valid match afterwards.
			for _, m := range []domain.Message{foreign, valid} {
				if err := c.UpsertMessage(ctx, m); err != nil {
					t.Fatal(err)
				}
			}
			contextFor := func(m domain.Message) domain.Context {
				return domain.Context{ID: "context-" + m.Source.MessageID, Scope: m.Source.Scope, Version: 1, Text: m.Text, Sources: []domain.SourceRef{m.Source}, CreatedAt: m.UpdatedAt, UpdatedAt: m.UpdatedAt.Add(time.Second)}
			}
			if err := c.UpsertContext(ctx, contextFor(foreign), []float32{1, 0, 0}); err != nil {
				t.Fatal(err)
			}
			if err := c.UpsertContext(ctx, contextFor(valid), []float32{.9, .1, 0}); err != nil {
				t.Fatal(err)
			}
			q := query()
			q.Scope = valid.Source.Scope
			q.Text = valid.Text
			q.Limit = 1
			if field == "author" {
				q.AuthorID = valid.Source.AuthorID
			}
			for _, channel := range []string{"literal", "keyword", "vector"} {
				var hits []domain.Evidence
				var err error
				switch channel {
				case "literal":
					hits, err = c.Literal(ctx, q)
				case "keyword":
					hits, err = c.Keyword(ctx, q)
				case "vector":
					hits, err = c.Vector(ctx, q, []float32{1, 0, 0})
				}
				if err != nil || len(hits) != 1 || !reflect.DeepEqual(hits[0].Sources, []domain.SourceRef{valid.Source}) {
					t.Errorf("%s exact %s with limit1: %#v %v", channel, field, hits, err)
				}
			}
		})
	}
}

package retrieval_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/meili"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/retrieval"
)

func TestRealMeiliChineseAllTermsSurviveCurrentOriginalVerification(t *testing.T) {
	endpoint, key := os.Getenv("AGENT_TEST_MEILI_URL"), os.Getenv("AGENT_TEST_MEILI_API_KEY")
	if endpoint == "" || key == "" {
		t.Skip("isolated Meilisearch required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	prefix := "retrieval_zh_" + hex.EncodeToString(random[:])
	options := meili.Options{Endpoint: endpoint, APIKey: key, MessagesIndex: prefix + "_messages", ContextsIndex: prefix + "_contexts", EmbeddingModel: "deterministic-fixture-v1", VectorDimensions: 2, Timeout: 15 * time.Second, PollInterval: 5 * time.Millisecond}
	idx, err := meili.New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer deleteIndexes(t, endpoint, key, options.MessagesIndex, options.ContextsIndex)
	cases := []struct{ id, query, text string }{
		{"budget", "预算审批", "预算需要两人审批"},
		{"traffic", "上海交通", "上海的交通便利"},
		{"project", "项目预算", "项目需要更多预算"},
		{"user", "用户管理", "用户的账号管理"},
		{"report", "测试报告", "测试完成后生成报告"},
		{"deploy", "部署流程", "部署需要遵守流程"},
		{"long7", "预算审批流程部署报告用户管理", "预算需要两人审批，流程包括部署，报告说明用户管理。"},
		{"long9", "预算审批流程部署报告用户管理测试系统", "预算需要两人审批，流程包括部署，报告说明用户管理，测试系统。"},
		{"latin_case", "APPROVAL", "approval"},
	}
	pg := &source{messages: map[string]domain.Message{}}
	for _, tc := range cases {
		m := message(tc.id, tc.text)
		pg.messages[tc.id] = m
		if err := idx.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"scope_case", "author_case"} {
		m := message(kind, "预算需要两人审批")
		if kind == "scope_case" {
			m.Source.Scope.AppID = "App"
		} else {
			m.Source.AuthorID = "Alice"
		}
		pg.messages[kind] = m
		if err := idx.UpsertMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	service, err := retrieval.New(retrieval.Options{Sources: pg, Search: idx, RecentLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			q := query(domain.QueryLiteral, tc.query)
			direct, err := idx.Literal(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range direct {
				if len(e.Sources) == 1 && e.Sources[0].MessageID == tc.id {
					found = true
				}
			}
			if !found {
				t.Fatal("real engine did not supply the expected all-term fixture match")
			}
			_, count, err := idx.LiteralAnalyzed(ctx, q)
			if err != nil || count <= 0 || count >= 10 {
				t.Fatalf("actual engine query analysis failed: count=%d error=%v", count, err)
			}
			if tc.id == "long9" && count != 9 {
				t.Fatalf("long Chinese query was not nine actual words: %d", count)
			}
			result, err := service.Retrieve(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			found = false
			for _, e := range result.Evidence {
				if len(e.Sources) == 1 && e.Sources[0].MessageID == tc.id {
					found = e.Text == tc.text && e.DerivedText == ""
				}
			}
			if !found {
				t.Fatal("verified real Chinese all-term match discarded")
			}
			// The exact quoted string is absent even though both words match.
			q.Text = `"` + tc.query + `"`
			result, err = service.Retrieve(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range result.Evidence {
				if e.Sources[0].MessageID == tc.id {
					t.Fatal("Chinese word semantics weakened full raw quote verification")
				}
			}
			// Keep the index untouched: PG's newer version must revoke even a
			// genuine engine word match immediately.
			q.Text = tc.query
			current := pg.messages[tc.id]
			changed := current
			changed.Source.Version++
			pg.messages[tc.id] = changed
			result, err = service.Retrieve(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range result.Evidence {
				if e.Sources[0].MessageID == tc.id {
					t.Fatal("engine match bypassed current source-version proof")
				}
			}
			pg.messages[tc.id] = current
		})
	}
	t.Run("mixed_quoted_and_unquoted", func(t *testing.T) {
		q := query(domain.QueryLiteral, `预算审批 "流程包括部署"`)
		q.AuthorID = "alice"
		hits, count, err := idx.LiteralAnalyzed(ctx, q)
		if err != nil || count <= 0 || count >= 10 || len(hits) == 0 {
			t.Fatalf("mixed literal analysis failed: %d %v", count, err)
		}
		for _, e := range hits {
			if e.Sources[0].Scope != q.Scope || e.Sources[0].AuthorID != q.AuthorID {
				t.Fatal("case-sensitive source filters lost during literal analysis")
			}
		}
		r, err := service.Retrieve(ctx, q)
		if err != nil || len(r.Evidence) == 0 {
			t.Fatalf("verified mixed quoted/unquoted match discarded: %v", err)
		}
		q.Text = `预算审批 "流程包括 部署"`
		r, err = service.Retrieve(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Evidence) != 0 {
			t.Fatal("word proof weakened mixed raw phrase verification")
		}
	})
	t.Run("long_quoted_only", func(t *testing.T) {
		q := query(domain.QueryLiteral, `"`+pg.messages["long9"].Text+`"`)
		r, err := service.Retrieve(ctx, q)
		if err != nil || len(r.Evidence) != 1 || r.Evidence[0].Sources[0].MessageID != "long9" {
			t.Fatalf("complete long quote proof lost: %+v %v", r, err)
		}
	})
	for _, text := range []string{"approv", "apprvoal"} {
		t.Run(text, func(t *testing.T) {
			q := query(domain.QueryLiteral, text)
			hits, _, err := idx.LiteralAnalyzed(ctx, q)
			if err != nil || len(hits) != 0 {
				t.Fatalf("literal analysis enabled prefix/typo expansion: hits=%d error=%v", len(hits), err)
			}
		})
	}
	// Both distinct and repeated query terms truncate at the engine's actual
	// ten-word boundary. Repetition must never make a truncated query appear
	// to be a complete one-word query.
	for _, tc := range []struct{ id, query, text string }{
		{"distinct_over_limit", "预算 审批 流程 部署 报告 用户 管理 测试 系统 方案 数据 服务", "预算 审批 流程 部署 报告 用户 管理 测试 系统 方案"},
		{"repeat_over_limit", strings.Repeat("预算 ", 10) + "不存在", "预算"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			m := message(tc.id, tc.text)
			pg.messages[tc.id] = m
			if err := idx.UpsertMessage(ctx, m); err != nil {
				t.Fatal(err)
			}
			q := query(domain.QueryLiteral, tc.query)
			hits, count, err := idx.LiteralAnalyzed(ctx, q)
			if err != nil || len(hits) == 0 || count != 10 {
				t.Fatalf("actual truncation fixture failed: count=%d hits=%d error=%v", count, len(hits), err)
			}
			r, err := service.Retrieve(ctx, q)
			if err != nil || len(r.Evidence) != 0 || !r.Degraded {
				t.Fatalf("ambiguous ten-word query accepted or not degraded: %+v %v", r, err)
			}
		})
	}
}

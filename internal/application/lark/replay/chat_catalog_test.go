package replay

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
)

func TestChatCatalogSearchByKeywordGroupsCandidates(t *testing.T) {
	service := ChatCatalogService{
		loadMessages: func(_ context.Context, query ChatCatalogQuery) ([]*xmodel.MessageIndex, error) {
			if query.Keyword != "投资" {
				t.Fatalf("query.Keyword = %q, want %q", query.Keyword, "投资")
			}
			return []*xmodel.MessageIndex{
				testCatalogMessage("oc_invest", "投资研究群", "2026-03-31 09:00:00"),
				testCatalogMessage("oc_invest", "投资研究群", "2026-03-30 09:00:00"),
				testCatalogMessage("oc_other", "产品讨论", "2026-03-31 10:00:00"),
			}, nil
		},
	}

	got, err := service.Search(context.Background(), ChatCatalogQuery{
		Keyword: "投资",
		Days:    7,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].ChatID != "oc_invest" || got[0].ChatName != "投资研究群" {
		t.Fatalf("candidate = %+v", got[0])
	}
	if got[0].MessageCountInWindow != 2 {
		t.Fatalf("MessageCountInWindow = %d, want 2", got[0].MessageCountInWindow)
	}
	if got[0].MatchedBy != "投资" {
		t.Fatalf("MatchedBy = %q, want %q", got[0].MatchedBy, "投资")
	}
	if got[0].LastMessageAt != "2026-03-31 09:00:00" {
		t.Fatalf("LastMessageAt = %q, want %q", got[0].LastMessageAt, "2026-03-31 09:00:00")
	}
}

func TestChatCatalogEmptyKeywordReturnsBoundedRecentChats(t *testing.T) {
	service := ChatCatalogService{
		loadMessages: func(_ context.Context, query ChatCatalogQuery) ([]*xmodel.MessageIndex, error) {
			if query.Keyword != "" {
				t.Fatalf("query.Keyword = %q, want empty", query.Keyword)
			}
			return []*xmodel.MessageIndex{
				testCatalogMessage("oc_new", "最近群", "2026-03-31 11:00:00"),
				testCatalogMessage("oc_mid", "中间群", "2026-03-30 11:00:00"),
				testCatalogMessage("oc_old", "", "2026-03-29 11:00:00"),
			}, nil
		},
	}

	got, err := service.Search(context.Background(), ChatCatalogQuery{
		Days:  7,
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].ChatID != "oc_new" || got[1].ChatID != "oc_mid" {
		t.Fatalf("got ids = %q,%q, want oc_new,oc_mid", got[0].ChatID, got[1].ChatID)
	}
	if got[1].ChatName != "中间群" {
		t.Fatalf("got[1].ChatName = %q, want %q", got[1].ChatName, "中间群")
	}
}

func TestChatCatalogFallsBackToChatIDWhenNameMissing(t *testing.T) {
	service := ChatCatalogService{
		loadMessages: func(context.Context, ChatCatalogQuery) ([]*xmodel.MessageIndex, error) {
			return []*xmodel.MessageIndex{
				testCatalogMessage("oc_unknown", "", "2026-03-31 08:00:00"),
			}, nil
		},
	}

	got, err := service.Search(context.Background(), ChatCatalogQuery{Days: 1, Limit: 5})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].ChatName != "oc_unknown" {
		t.Fatalf("ChatName = %q, want fallback chat id", got[0].ChatName)
	}
}

func testCatalogMessage(chatID, chatName, createTime string) *xmodel.MessageIndex {
	timestamp, err := time.ParseInLocation("2006-01-02 15:04:05", createTime, time.Local)
	if err != nil {
		panic(err)
	}
	return &xmodel.MessageIndex{
		ChatName:   chatName,
		CreateTime: createTime,
		MessageLog: &xmodel.MessageLog{
			ChatID:    chatID,
			CreatedAt: timestamp,
		},
	}
}

func TestChatCatalogSearchFiltersCaseInsensitiveKeyword(t *testing.T) {
	service := ChatCatalogService{
		loadMessages: func(context.Context, ChatCatalogQuery) ([]*xmodel.MessageIndex, error) {
			return []*xmodel.MessageIndex{
				testCatalogMessage("oc_a", "Macro Squad", "2026-03-31 08:00:00"),
				testCatalogMessage("oc_b", "macro research", "2026-03-31 07:00:00"),
			}, nil
		},
	}

	got, err := service.Search(context.Background(), ChatCatalogQuery{Keyword: "MACRO", Days: 7, Limit: 10})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if !strings.EqualFold(got[0].MatchedBy, "MACRO") {
		t.Fatalf("MatchedBy = %q, want preserve query keyword", got[0].MatchedBy)
	}
}

func TestChatCatalogSearchUsesCatalogLoaderAndInMemoryFilter(t *testing.T) {
	service := ChatCatalogService{
		loadCatalog: func(_ context.Context, query ChatCatalogQuery) ([]ChatCandidate, error) {
			if query.Keyword != "macro" {
				t.Fatalf("query.Keyword = %q, want %q", query.Keyword, "macro")
			}
			return []ChatCandidate{
				{ChatID: "oc_1", ChatName: "Macro Research", LastMessageAt: "2026-03-31 09:00:00"},
				{ChatID: "oc_2", ChatName: "Equity Desk", LastMessageAt: "2026-03-31 08:00:00"},
			}, nil
		},
	}

	got, err := service.Search(context.Background(), ChatCatalogQuery{Keyword: "macro", Days: 7, Limit: 10})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 1 || got[0].ChatID != "oc_1" {
		t.Fatalf("Search() = %+v, want local filtered result", got)
	}
}

func TestChatCatalogLoadCatalogRespectsLimit(t *testing.T) {
	service := ChatCatalogService{
		loadCatalog: func(context.Context, ChatCatalogQuery) ([]ChatCandidate, error) {
			return []ChatCandidate{
				{ChatID: "oc_1", ChatName: "A"},
				{ChatID: "oc_2", ChatName: "B"},
			}, nil
		},
	}

	got, err := service.LoadCatalog(context.Background(), ChatCatalogQuery{Days: 7, Limit: 1})
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}
	if len(got) != 1 || got[0].ChatID != "oc_1" {
		t.Fatalf("LoadCatalog() = %+v, want first limited candidate", got)
	}
}

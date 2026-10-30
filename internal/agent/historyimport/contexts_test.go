package historyimport

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func scope() domain.Scope {
	return domain.Scope{TenantID: "t", AppID: "a", ChatID: "c", Visibility: "group"}
}
func message(id, author, text string, at time.Time) domain.Message {
	m := domain.Message{Source: domain.SourceRef{Scope: scope(), MessageID: id, Version: at.UnixMilli() * 2, AuthorID: author, OccurredAt: at}, Text: text, UpdatedAt: at}
	m.EventID = refreshID(m)
	provider(m, "text", map[string]any{"text": text}, &m)
	return m
}
func provider(original domain.Message, kind string, content any, target *domain.Message) {
	body, _ := json.Marshal(content)
	raw, _ := json.Marshal(map[string]any{"message_id": original.Source.MessageID, "chat_id": original.Source.Scope.ChatID, "create_time": fmt.Sprint(original.Source.OccurredAt.UnixMilli()), "update_time": fmt.Sprint(original.UpdatedAt.UnixMilli()), "msg_type": kind, "sender": map[string]any{"id": original.Source.AuthorID, "id_type": "open_id", "sender_type": "user", "tenant_key": original.Source.Scope.TenantID}, "body": map[string]any{"content": string(body)}, "parent_id": original.ParentID, "root_id": original.RootID, "thread_id": original.ThreadID})
	target.Raw, _ = json.Marshal(domain.IngressMetadata{Kind: "refresh", SenderType: "user", MessageType: kind, Provider: raw})
}

func TestBuildContextsDeterministicSingleAuthorBoundedAndLossless(t *testing.T) {
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	long := strings.Repeat("雪🙂界", 5000)
	messages := []domain.Message{message("b", "bob", "other", at), message("long", "alice", long, at.Add(time.Hour)), message("a", "alice", "first", at)}
	got, err := BuildContexts(scope(), messages)
	if err != nil {
		t.Fatal(err)
	}
	reversed := []domain.Message{messages[2], messages[1], messages[0]}
	again, err := BuildContexts(scope(), reversed)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("nondeterministic contexts: %v", err)
	}
	reconstructed := ""
	for _, c := range got {
		if len(c.Text) > 16<<10 || !utf8.ValidString(c.Text) || len(c.Sources) > 64 {
			t.Fatalf("invalid context bounds: bytes=%d refs=%d", len(c.Text), len(c.Sources))
		}
		for _, r := range c.Sources {
			if r.AuthorID != c.Sources[0].AuthorID {
				t.Fatal("mixed-author context")
			}
			if r.OccurredAt.After(c.UpdatedAt) {
				t.Fatal("invalid stable timestamp")
			}
		}
		if strings.Contains(c.Text, "message=long ") {
			parts := strings.SplitN(c.Text[strings.Index(c.Text, "[message=long "):], "\n", 2)
			if len(parts) != 2 {
				t.Fatal("missing provenance")
			}
			reconstructed += strings.TrimSuffix(parts[1], "\n")
		}
	}
	if reconstructed != long {
		t.Fatalf("long text not lossless: %d != %d", len(reconstructed), len(long))
	}
}
func TestBuildContextsRejectsEntireInvalidInputAndSeparatesGaps(t *testing.T) {
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	a := message("a", "alice", "a", at)
	b := message("b", "alice", "b", at.Add(48*time.Hour))
	cs, err := BuildContexts(scope(), []domain.Message{a, b})
	if err != nil || len(cs) != 2 {
		t.Fatalf("gap merged: %d %v", len(cs), err)
	}
	for _, bad := range []domain.Message{func() domain.Message { b := a.Clone(); b.Source.Scope.ChatID = "other"; return b }(), func() domain.Message {
		b := a.Clone()
		b.Raw = json.RawMessage(`{"kind":"refresh","sender_type":"app"}`)
		return b
	}(), func() domain.Message { b := a.Clone(); b.Deleted = true; return b }()} {
		if cs, err = BuildContexts(scope(), []domain.Message{a, bad}); err == nil || len(cs) > 0 {
			t.Fatal("invalid input partially accepted")
		}
	}
}

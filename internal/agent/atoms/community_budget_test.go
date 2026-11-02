package atoms_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

type communityBudgetFixture struct {
	*communityFixture
	history []domain.Message
	replies []pgstore.ConversationReply
	members domain.GroupMembers
}

func (f *communityBudgetFixture) History(_ context.Context, q domain.HistoryQuery) (domain.HistoryPage, error) {
	p := domain.HistoryPage{Messages: []domain.Message{}}
	for _, m := range f.history {
		if q.Cursor != nil && (m.Source.OccurredAt.After(q.Cursor.Time) || (m.Source.OccurredAt.Equal(q.Cursor.Time) && m.Source.MessageID <= q.Cursor.MessageID)) {
			continue
		}
		p.Messages = append(p.Messages, m)
	}
	if len(p.Messages) > q.Limit {
		p.HasMore = true
		p.Messages = p.Messages[:q.Limit]
		m := p.Messages[len(p.Messages)-1]
		p.Next = &domain.HistoryCursor{Time: m.Source.OccurredAt, MessageID: m.Source.MessageID}
	}
	return p, nil
}
func (f *communityBudgetFixture) RecentReplies(context.Context, domain.Scope, time.Time, int) ([]pgstore.ConversationReply, error) {
	return f.replies, nil
}
func (f *communityBudgetFixture) GroupMembers(context.Context, domain.Scope, int, string) (domain.GroupMembers, error) {
	return f.members, nil
}
func budgetFixture() *communityBudgetFixture {
	f := &communityBudgetFixture{communityFixture: fixtureCommunity()}
	at := time.Now().UTC().Add(-time.Minute)
	for i := 0; i < 5; i++ {
		m := f.messages["target"].Clone()
		m.Source.MessageID = fmt.Sprintf("dense-%d", i)
		m.Source.OccurredAt = at.Add(-time.Duration(i) * time.Second)
		m.ParentID = ""
		m.Text = strings.Repeat("\x00<>&", 12000)
		m.Media = nil
		for j := 0; j < 1000; j++ {
			m.Media = append(m.Media, domain.Media{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: fmt.Sprintf("actual-%d-%d", i, j), Sequence: j})
		}
		f.messages[m.Source.MessageID] = m
		f.history = append(f.history, m)
	}
	return f
}
func TestCommunityHistoryBudgetPreservesPaginationAndTrueMedia(t *testing.T) {
	f := budgetFixture()
	r, err := atoms.New(atoms.Options{Sources: f, Group: f})
	if err != nil {
		t.Fatal(err)
	}
	d := namedTool(t, r, "feishu_message_history")
	in := atoms.Invocation{Task: domain.Task{Scope: f.history[0].Source.Scope}}
	seen := map[string]bool{}
	token := ""
	for page := 0; page < 10; page++ {
		args, _ := json.Marshal(map[string]any{"limit": 5, "page_token": token})
		raw, err := d.Run(context.Background(), in, string(args))
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > 64<<10 {
			t.Fatalf("complete encoded result exceeds 64KiB: %d", len(raw))
		}
		var out struct {
			Available bool `json:"available"`
			Data      struct {
				Messages []struct {
					Source     domain.SourceRef `json:"source"`
					Media      []domain.Media   `json:"media"`
					MediaTotal int              `json:"media_total"`
					MediaMore  bool             `json:"media_has_more"`
					Truncated  bool             `json:"truncated"`
				} `json:"messages"`
				HasMore bool   `json:"has_more"`
				Token   string `json:"page_token"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(raw), &out) != nil || !out.Available || len(out.Data.Messages) == 0 {
			t.Fatalf("fake empty success/unavailable dense normal page: %.300s", raw)
		}
		for _, m := range out.Data.Messages {
			if seen[m.Source.MessageID] {
				t.Fatal("repeated history message")
			}
			seen[m.Source.MessageID] = true
			if m.MediaTotal != 1000 || !m.MediaMore || !m.Truncated || len(m.Media) == 0 || len(m.Media) >= 1000 {
				t.Fatalf("media preview omits truncation/count: %+v", m)
			}
			for _, media := range m.Media {
				original := f.messages[m.Source.MessageID].Media[media.Sequence]
				if media.ResourceID != original.ResourceID || media.Type != original.Type {
					t.Fatal("preview forged locator/sequence")
				}
			}
		}
		if !out.Data.HasMore {
			break
		}
		if out.Data.Token == "" || out.Data.Token == token {
			t.Fatal("missing or repeated exact continuation")
		}
		token = out.Data.Token
	}
	if len(seen) != len(f.history) {
		t.Fatalf("budget truncation skipped messages: %d/%d", len(seen), len(f.history))
	}
}
func TestCommunityAllReadResultsBoundEscapedMetadataAndKeepProof(t *testing.T) {
	f := budgetFixture()
	first := f.history[0]
	first.ParentID = f.history[1].Source.MessageID
	f.messages[first.Source.MessageID] = first
	f.members = domain.GroupMembers{Members: []domain.GroupMember{{ID: "real-member", Name: strings.Repeat("\x00", 60000)}}, HasMore: true, PageToken: "opaque-next"}
	f.artifact.Source = first.Source
	f.artifact.Parts = []domain.MediaPart{{Object: domain.Object{Source: first.Source, ResourceID: first.Media[0].ResourceID, Sequence: 0}, Text: strings.Repeat("\x00", 32768), Page: 1}}
	r, _ := atoms.New(atoms.Options{Sources: f, Group: f})
	in := atoms.Invocation{Task: domain.Task{Scope: first.Source.Scope}}
	for _, name := range []string{"feishu_read_message", "feishu_message_context", "feishu_read_attachment", "feishu_group_members"} {
		t.Run(name, func(t *testing.T) {
			d := namedTool(t, r, name)
			args := `{"message_id":"dense-0"}`
			if name == "feishu_group_members" {
				args = `{}`
			}
			raw, err := d.Run(context.Background(), in, args)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) > 64<<10 {
				t.Fatalf("encoded metadata/escaping exceeded cap: %d", len(raw))
			}
			var state struct {
				Available *bool  `json:"available"`
				Truncated bool   `json:"truncated"`
				Reason    string `json:"reason"`
			}
			if json.Unmarshal([]byte(raw), &state) != nil {
				t.Fatal("invalid JSON")
			}
			refs, err := d.Sources(raw)
			if err != nil {
				t.Fatal(err)
			}
			if state.Available != nil && !*state.Available {
				if !state.Truncated || state.Reason == "" || len(refs) != 0 {
					t.Fatal("unrepresentable evidence was not explicitly withheld")
				}
			} else if len(refs) == 0 {
				t.Fatal("successful source-derived read omitted provenance")
			}
		})
	}
}
func TestCommunityOwnReplyAdmissionDeduplicatesAndNeverDropsPartOfProof(t *testing.T) {
	f := budgetFixture()
	f.history = f.history[:1]
	f.history[0].Text = "small"
	f.history[0].Media = nil
	f.messages[f.history[0].Source.MessageID] = f.history[0]
	var refs []domain.SourceRef
	for i := 0; i < 256; i++ {
		m := f.history[0].Clone()
		m.Source.MessageID = fmt.Sprintf("proof-%d", i)
		f.messages[m.Source.MessageID] = m
		refs = append(refs, m.Source)
	}
	f.replies = []pgstore.ConversationReply{{MessageID: "whole-reply", Text: "must keep every proof or skip whole reply", OccurredAt: time.Now().Add(-time.Second), Sources: refs}}
	r, _ := atoms.New(atoms.Options{Sources: f, Group: f})
	d := namedTool(t, r, "feishu_message_history")
	in := atoms.Invocation{Task: domain.Task{Scope: f.history[0].Source.Scope, ContextSources: refs[:255]}}
	raw, err := d.Run(context.Background(), in, `{"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data struct {
			Replies   []pgstore.ConversationReply `json:"own_replies"`
			More      bool                        `json:"own_replies_more"`
			Truncated bool                        `json:"own_replies_truncated"`
		} `json:"data"`
	}
	json.Unmarshal([]byte(raw), &out)
	if len(out.Data.Replies) != 0 || !out.Data.More || !out.Data.Truncated {
		t.Fatal("whole unrepresentable reply must be skipped explicitly")
	}
	f.replies[0].Sources = []domain.SourceRef{f.history[0].Source, f.history[0].Source}
	raw, err = d.Run(context.Background(), atoms.Invocation{Task: domain.Task{Scope: in.Task.Scope}}, `{"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &out)
	got, err := d.Sources(raw)
	if err != nil || len(got) != 1 || len(out.Data.Replies) != 1 || len(out.Data.Replies[0].Sources) == 0 {
		t.Fatalf("repeated evidence was not admitted/deduplicated: refs=%d replies=%d %v", len(got), len(out.Data.Replies), err)
	}
}

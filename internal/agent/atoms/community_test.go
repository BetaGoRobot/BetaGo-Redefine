package atoms_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
)

type communityFixture struct {
	messages map[string]domain.Message
	artifact domain.MediaArtifact
	latest   *domain.Message
	err      error
	uuid     string
	writes   int
}

func fixtureCommunity() *communityFixture {
	scope := domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "group", Visibility: "group"}
	at := time.Now().UTC().Add(-time.Minute)
	f := &communityFixture{messages: map[string]domain.Message{}}
	for _, id := range []string{"root", "target", "parallel"} {
		f.messages[id] = domain.Message{Source: domain.SourceRef{Scope: scope, MessageID: id, Version: 1, AuthorID: "alice", OccurredAt: at}, Text: id, UpdatedAt: at}
	}
	m := f.messages["target"]
	m.ParentID = "root"
	m.Media = []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "img", Sequence: 0}}
	f.messages["target"] = m
	f.artifact = domain.MediaArtifact{Source: m.Source, Parts: []domain.MediaPart{{Object: domain.Object{Key: "secret-signed-url", Source: m.Source, ResourceID: "img", Sequence: 0}, Text: strings.Repeat("解析", 20000), Page: 2, Start: time.Second, End: 2 * time.Second}}}
	return f
}
func (f *communityFixture) Message(_ context.Context, s domain.Scope, id string) (domain.Message, error) {
	m, ok := f.messages[id]
	if !ok || m.Source.Scope != s {
		return domain.Message{}, domain.ErrNotFound
	}
	return m, nil
}
func (f *communityFixture) Recent(context.Context, domain.Scope, time.Time, int) ([]domain.Message, error) {
	return nil, nil
}
func (f *communityFixture) ValidSources(_ context.Context, q domain.Query, refs []domain.SourceRef) (bool, error) {
	for _, ref := range refs {
		m, ok := f.messages[ref.MessageID]
		if !ok || m.Deleted || m.Source != ref || ref.Scope != q.Scope {
			return false, nil
		}
	}
	return true, nil
}
func (f *communityFixture) ReadMessage(ctx context.Context, s domain.Scope, id string) (domain.Message, error) {
	if f.err != nil {
		return domain.Message{}, f.err
	}
	if f.latest != nil {
		return *f.latest, nil
	}
	return f.Message(ctx, s, id)
}
func (f *communityFixture) GroupInfo(context.Context, domain.Scope) (domain.GroupInfo, error) {
	return domain.GroupInfo{Name: "Current group"}, f.err
}
func (f *communityFixture) GroupMembers(context.Context, domain.Scope, int, string) (domain.GroupMembers, error) {
	return domain.GroupMembers{Members: []domain.GroupMember{{ID: "alice", Name: "Alice"}}}, f.err
}
func (f *communityFixture) ListReactions(context.Context, domain.Scope, string, int, string) (domain.ReactionPage, error) {
	return domain.ReactionPage{Reactions: []domain.Reaction{{ID: "own", Emoji: "THUMBSUP", Own: true}}}, f.err
}
func (f *communityFixture) AddReaction(_ context.Context, _ domain.Scope, _, _, uuid string) (domain.Reaction, error) {
	f.writes++
	f.uuid = uuid
	return domain.Reaction{ID: "own", Own: true}, f.err
}
func (f *communityFixture) RemoveReaction(_ context.Context, _ domain.Scope, _, _, uuid string) error {
	f.writes++
	f.uuid = uuid
	return f.err
}
func (f *communityFixture) SendImage(_ context.Context, _ domain.Scope, _ domain.Message, _ domain.Media, _, uuid string) (string, error) {
	f.writes++
	f.uuid = uuid
	return "sent-image", f.err
}
func (f *communityFixture) History(context.Context, domain.HistoryQuery) (domain.HistoryPage, error) {
	return domain.HistoryPage{Messages: []domain.Message{f.messages["target"]}}, f.err
}
func (f *communityFixture) RecentReplies(context.Context, domain.Scope, time.Time, int) ([]pgstore.ConversationReply, error) {
	return []pgstore.ConversationReply{{MessageID: "delivered", ParentID: "target", Text: "own reply", OccurredAt: time.Now().Add(-time.Second), Sources: []domain.SourceRef{f.messages["target"].Source}}}, f.err
}
func (f *communityFixture) Media(context.Context, domain.SourceRef) (domain.MediaArtifact, error) {
	return f.artifact, f.err
}
func (f *communityFixture) SourceJobStatus(context.Context, domain.SourceRef, domain.JobKind) (domain.JobStatus, error) {
	return domain.JobCompleted, nil
}
func TestCommunityToolsScopeEffectsAndSources(t *testing.T) {
	f := fixtureCommunity()
	r, err := atoms.New(atoms.Options{Sources: f, Group: f})
	if err != nil {
		t.Fatal(err)
	}
	in := atoms.Invocation{Task: domain.Task{Scope: f.messages["target"].Source.Scope, ID: "task", Root: f.messages["target"].Source}, ExecutionKey: "effect"}
	for _, name := range []string{"feishu_group_info", "feishu_group_members", "feishu_message_history", "feishu_message_context", "feishu_read_attachment", "feishu_list_reactions", "feishu_add_reaction", "feishu_remove_reaction", "feishu_send_image", "current_time", "calculate", "feishu_read_message"} {
		d := namedTool(t, r, name)
		social := name == "feishu_add_reaction" || name == "feishu_remove_reaction" || name == "feishu_send_image"
		if d.SideEffect != social || d.AutoExecute != social {
			t.Fatalf("incorrect policy %s", name)
		}
		if _, err := d.Validate(`{"chat_id":"foreign","message_id":"target"}`); err == nil {
			t.Fatalf("scope override %s", name)
		}
	}
	for _, name := range []string{"feishu_message_history", "feishu_message_context", "feishu_read_attachment", "feishu_list_reactions"} {
		d := namedTool(t, r, name)
		args := `{"message_id":"target"}`
		if name == "feishu_message_history" {
			args = `{}`
		}
		out, err := d.Run(context.Background(), in, args)
		if err != nil {
			t.Fatal(name, err)
		}
		refs, err := d.Sources(out)
		if err != nil || len(refs) == 0 {
			t.Fatalf("read %s has no dependencies: %s %v", name, out, err)
		}
		if strings.Contains(out, "secret-signed-url") || strings.Contains(out, `"Raw"`) {
			t.Fatalf("storage internals exposed: %s", name)
		}
		if name == "feishu_message_context" && strings.Contains(out, "parallel") {
			t.Fatal("mixed parallel topic")
		}
		if name == "feishu_read_attachment" && (!strings.Contains(out, `"truncated":true`) || len(out) > 40000 || !strings.Contains(out, `"page":2`)) {
			t.Fatalf("attachment budget/status missing: %d", len(out))
		}
	}
	for _, name := range []string{"feishu_add_reaction", "feishu_remove_reaction", "feishu_send_image"} {
		d := namedTool(t, r, name)
		args := `{"message_id":"target","emoji":"THUMBSUP"}`
		if name == "feishu_remove_reaction" {
			args = `{"message_id":"target","reaction_id":"own"}`
		}
		if name == "feishu_send_image" {
			args = `{"message_id":"target","sequence":0}`
		}
		if _, err := d.Run(context.Background(), in, args); err != nil {
			t.Fatal(name, err)
		}
		if want := domain.StableUUID(in.Task.Scope.Key(), in.Task.ID, in.ExecutionKey); f.uuid != want {
			t.Fatalf("unstable execution UUID %s", f.uuid)
		}
	}
}
func TestCommunityReadsPreserveGuardsAndReturnSafeUnavailable(t *testing.T) {
	f := fixtureCommunity()
	r, _ := atoms.New(atoms.Options{Sources: f, Group: f})
	in := atoms.Invocation{Task: domain.Task{Scope: f.messages["target"].Source.Scope}}
	for _, name := range []string{"feishu_group_info", "feishu_group_members", "feishu_list_reactions", "feishu_read_message"} {
		d := namedTool(t, r, name)
		args := `{}`
		if name == "feishu_list_reactions" || name == "feishu_read_message" {
			args = `{"message_id":"target"}`
		}
		if name == "feishu_read_message" {
			// Only a missing local original needs the provider read exercised here.
			delete(f.messages, "target")
		}
		f.err = errors.New("upstream credential=secret")
		out, err := d.Run(context.Background(), in, args)
		if err != nil || !strings.Contains(out, `"available":false`) || strings.Contains(out, "secret") {
			t.Fatalf("safe unavailable %s %s %v", name, out, err)
		}
		for _, cause := range []error{context.Canceled, domain.ErrUnauthorized, domain.ErrStaleSource} {
			f.err = cause
			if _, err := d.Run(context.Background(), in, args); !errors.Is(err, cause) {
				t.Fatalf("guard swallowed: %s %v", name, err)
			}
		}
	}
}
func TestCommunityAttachmentRejectsStaleArtifactAndMissingImage(t *testing.T) {
	f := fixtureCommunity()
	r, _ := atoms.New(atoms.Options{Sources: f, Group: f})
	in := atoms.Invocation{Task: domain.Task{Scope: f.messages["target"].Source.Scope, ID: "task", Root: f.messages["target"].Source}, ExecutionKey: "key"}
	f.artifact.Source.Version--
	if _, err := namedTool(t, r, "feishu_read_attachment").Run(context.Background(), in, `{"message_id":"target"}`); !errors.Is(err, domain.ErrStaleSource) {
		t.Fatal("stale artifact accepted", err)
	}
	if _, err := namedTool(t, r, "feishu_send_image").Run(context.Background(), in, `{"message_id":"target","sequence":2}`); err == nil || f.writes != 0 {
		t.Fatal("image without actual attachment reached write")
	}
	d := namedTool(t, r, "feishu_send_image")
	for _, args := range []string{`{"message_id":"target","image_key":"arbitrary"}`, `{"message_id":"target","url":"https://x"}`, `{"message_id":"target","path":"/secret"}`} {
		if _, err := d.Validate(args); err == nil {
			t.Fatal("unproved image accepted")
		}
	}
}
func TestGroupReaderMissingOriginalDoesNotRegisterApprovalTextSender(t *testing.T) {
	f := fixtureCommunity()
	m := f.messages["target"].Clone()
	m.Source.Version++
	m.Text = "actual latest"
	f.latest = &m
	delete(f.messages, "target")
	r, _ := atoms.New(atoms.Options{Sources: f, Group: f})
	in := atoms.Invocation{Task: domain.Task{Scope: m.Source.Scope}}
	out, err := namedTool(t, r, "feishu_read_message").Run(context.Background(), in, `{"message_id":"target"}`)
	if err != nil || !strings.Contains(out, "actual latest") {
		t.Fatalf("Group reader not used: %s %v", out, err)
	}
	for _, d := range r.Definitions() {
		if d.Name == "feishu_send_message" {
			t.Fatal("Group reader reenabled approval text tool")
		}
	}
}

package atoms_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/atoms"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/business"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/musiccards"
)

type musicPresenterFixture struct {
	calls    int
	identity business.Identity
	parent   string
	request  musiccards.Request
	reject   bool
}

func (f *musicPresenterFixture) Send(_ context.Context, in business.Identity, parent string, r musiccards.Request) (musiccards.Receipt, error) {
	f.calls++
	f.identity = in
	f.parent = parent
	f.request = r
	if f.reject {
		return musiccards.Receipt{State: "rejected"}, errors.New("private-upstream-error")
	}
	return musiccards.Receipt{MessageID: "rich-card", SessionID: "presentation", State: "active"}, nil
}
func musicReadAdapter(t *testing.T) *business.MusicClient {
	t.Helper()
	c, err := business.NewMusicClient(business.MusicConfig{BaseURL: "https://music.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestMusicCardsRequireRichPresenterRatherThanSilentPlainFallback(t *testing.T) {
	r := businessRegistry(t, atoms.BusinessOptions{Music: musicReadAdapter(t), Messenger: &businessMessenger{}})
	for _, d := range r.Definitions() {
		if d.Name == "music_send_card" || d.Name == "music_send_search_card" {
			t.Fatal("missing rich presenter silently fell back to a plain card")
		}
	}
}
func TestSelectedAndSearchCardsUseTypedPresenterWithServerIdentity(t *testing.T) {
	f := &musicPresenterFixture{}
	r := businessRegistry(t, atoms.BusinessOptions{Music: musicReadAdapter(t), MusicCards: f})
	in := atoms.Invocation{ActorID: "alice", ExecutionKey: "effect", Task: domain.Task{ID: "task", Root: domain.SourceRef{MessageID: "original"}, Scope: domain.Scope{TenantID: "t", AppID: "a", ChatID: "c", Visibility: "group"}}}
	for _, test := range []struct {
		name, args string
		want       musiccards.Request
	}{{"music_send_card", `{"type":"song","id":42}`, musiccards.Request{Kind: "song", ID: 42}}, {"music_send_search_card", `{"type":"album","keywords":"范特西","page":2}`, musiccards.Request{Scene: "album", Query: "范特西", Page: 2}}} {
		d := namedTool(t, r, test.name)
		if !d.SideEffect || !d.AutoExecute || !d.PerActorTurn {
			t.Fatal("music card is missing ordinary-delivery receipt classification")
		}
		args, err := d.Validate(test.args)
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.Run(context.Background(), in, args)
		if err != nil || f.request != test.want || f.identity.Scope != in.Task.Scope || f.identity.ActorID != in.ActorID || f.identity.ExecutionKey != in.ExecutionKey || f.parent != "original" {
			t.Fatal("music presenter lost selected identity or search scope")
		}
		if _, err = d.Validate(`{"type":"song","id":42,"actor_id":"other"}`); err == nil {
			t.Fatal("model supplied music execution identity")
		}
	}
}
func TestDefiniteMusicPresentationRejectionDoesNotBecomeUnknownSend(t *testing.T) {
	f := &musicPresenterFixture{reject: true}
	r := businessRegistry(t, atoms.BusinessOptions{Music: musicReadAdapter(t), MusicCards: f})
	d := namedTool(t, r, "music_send_card")
	out, err := d.Run(context.Background(), atoms.Invocation{ActorID: "actor", ExecutionKey: "effect", Task: domain.Task{ID: "task", Root: domain.SourceRef{MessageID: "original"}}}, `{"type":"song","id":42}`)
	if err != nil || out == "" {
		t.Fatal("definite no-send rejection became unknown and blocked retry")
	}
}

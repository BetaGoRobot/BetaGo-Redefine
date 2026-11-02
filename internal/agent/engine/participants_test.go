package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func sceneScope() domain.Scope {
	return domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "group", Visibility: "group"}
}
func TestMemberSnapshotIsolatesEveryScopeComponentAndKeepsLocalFacts(t *testing.T) {
	ctx := context.Background()
	calls := 0
	e := &Engine{c: Options{GroupMembers: sceneMemberReader(func(_ context.Context, s domain.Scope, n int, token string) (domain.GroupMembers, error) {
		calls++
		if n != 100 || token != "" {
			t.Fatal("unexpected member page")
		}
		return domain.GroupMembers{Members: []domain.GroupMember{{ID: "same-id", Name: s.Key(), IDType: "open_id"}}}, nil
	})}}
	scopes := []domain.Scope{sceneScope(), {TenantID: "tenant2", AppID: "app", ChatID: "group", Visibility: "group"}, {TenantID: "tenant", AppID: "app2", ChatID: "group", Visibility: "group"}, {TenantID: "tenant", AppID: "app", ChatID: "group2", Visibility: "group"}, {TenantID: "tenant", AppID: "app", ChatID: "group", Visibility: "private"}}
	for _, s := range scopes {
		for i := 0; i < 2; i++ {
			snapshot, err := e.memberSnapshot(ctx, s)
			if err != nil || !snapshot.status.Complete || snapshot.people["same-id"].Name != s.Key() {
				t.Fatalf("cross-scope cache: %v %+v", err, snapshot)
			}
		}
	}
	if calls != len(scopes) {
		t.Fatalf("cache calls=%d", calls)
	}
	e.members.mu.Lock()
	entry := e.members.entries[scopes[0]]
	entry.expires = time.Now().Add(-time.Second)
	e.members.entries[scopes[0]] = entry
	e.members.mu.Unlock()
	if _, err := e.memberSnapshot(ctx, scopes[0]); err != nil || calls != len(scopes) {
		t.Fatal("expired member facts were reused")
	}
}
func TestMemberSnapshotBoundsPagesPeopleScopesAndMarksIncomplete(t *testing.T) {
	calls := 0
	e := &Engine{c: Options{GroupMembers: sceneMemberReader(func(_ context.Context, s domain.Scope, _ int, token string) (domain.GroupMembers, error) {
		calls++
		people := []domain.GroupMember{}
		for i := 0; i < 100; i++ {
			people = append(people, domain.GroupMember{ID: fmt.Sprintf("id-%s-%d", token, i), Name: "同名", IDType: "open_id"})
		}
		return domain.GroupMembers{Members: people, HasMore: true, PageToken: token + "x"}, nil
	})}}
	snapshot, err := e.memberSnapshot(context.Background(), sceneScope())
	if err != nil || calls != 4 || len(snapshot.people) != 400 || snapshot.status.Complete || !snapshot.status.Truncated {
		t.Fatalf("unbounded/false complete snapshot: %d %+v %v", calls, snapshot.status, err)
	}
	for i := 0; i < memberCacheCapacity+1; i++ {
		scope := sceneScope()
		scope.ChatID = fmt.Sprint(i)
		if _, err = e.memberSnapshot(context.Background(), scope); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.members.entries) != memberCacheCapacity {
		t.Fatalf("member cache size=%d", len(e.members.entries))
	}
	over := &Engine{c: Options{GroupMembers: sceneMemberReader(func(context.Context, domain.Scope, int, string) (domain.GroupMembers, error) {
		people := make([]domain.GroupMember, 401)
		for i := range people {
			people[i] = domain.GroupMember{ID: fmt.Sprint(i), Name: "name", IDType: "open_id"}
		}
		return domain.GroupMembers{Members: people}, nil
	})}}
	snapshot, err = over.memberSnapshot(context.Background(), sceneScope())
	if err != nil || len(snapshot.people) != 400 || snapshot.status.Complete || !snapshot.status.Truncated {
		t.Fatalf("same-join batch escaped bound: %+v %v", snapshot.status, err)
	}
}
func TestMemberSnapshotFailureCancellationAndConcurrentReads(t *testing.T) {
	e := &Engine{c: Options{GroupMembers: sceneMemberReader(func(ctx context.Context, _ domain.Scope, _ int, _ string) (domain.GroupMembers, error) {
		return domain.GroupMembers{}, errors.New("provider unavailable")
	})}}
	snapshot, err := e.memberSnapshot(context.Background(), sceneScope())
	if err != nil || !snapshot.status.Unavailable || snapshot.status.Complete {
		t.Fatalf("failed snapshot invented completeness: %+v %v", snapshot.status, err)
	}
	p := newParticipantSet(sceneScope(), "bot", snapshot)
	unknown := p.person("historical-id", "member")
	if unknown.NameOrigin != "unknown" || unknown.Name == "" || unknown != p.person("historical-id", "member") {
		t.Fatal("unknown identity unstable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = e.memberSnapshot(ctx, sceneScope()); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel was degraded to ordinary unknown")
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.memberSnapshot(context.Background(), sceneScope()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
func TestParticipantsKeepSameNamesSeparateAndMentionHintsMessageLocal(t *testing.T) {
	p := newParticipantSet(sceneScope(), "bot", memberSnapshot{people: map[string]domain.GroupMember{"current": {ID: "current", Name: "同名"}, "other": {ID: "other", Name: "同名"}}})
	a, b := p.person("current", "member"), p.person("other", "member")
	old := p.person("historical", "member")
	if a.MemberRef == b.MemberRef || a.ID == b.ID || old.NameOrigin != "unknown" || old.Name == a.Name {
		t.Fatal("display names merged identities")
	}
	presenter := scenePresenter{people: p, messages: map[string]domain.Message{}}
	m := domain.Message{Source: domain.SourceRef{Scope: sceneScope(), MessageID: "msg", AuthorID: "historical", Version: 1, OccurredAt: time.Now()}, Text: "@_user_1 @_user_10 @_user_11"}
	m.Raw = []byte(`{"kind":"message","sender_type":"user","mentions":[{"Key":"@_user_1","OpenID":"historical","Name":"同名"},{"Key":"@_user_10","OpenID":"other","Name":"wrong"}]}`)
	view := presenter.message(m)
	if view.SpeakerName == "同名" || view.Text != "@同名 @同名 @_user_11" || len(view.Mentions) != 2 || view.Mentions[0].NameOrigin != "message_hint" || p.person("historical", "member").NameOrigin != "unknown" {
		t.Fatalf("message hint became an identity fact: %+v", view)
	}
	if len(p.selected([]sceneMessage{view})) != 2 {
		t.Fatal("participant table included unreferenced whole directory")
	}
}

func TestLegacyRefreshMentionHintsRemainLocalAndRequireOpenIDType(t *testing.T) {
	p := newParticipantSet(sceneScope(), "bot", memberSnapshot{people: map[string]domain.GroupMember{"author": {ID: "author", Name: "甲"}, "target": {ID: "target", Name: "乙"}}})
	presenter := scenePresenter{people: p, messages: map[string]domain.Message{}}
	m := domain.Message{Source: domain.SourceRef{Scope: sceneScope(), MessageID: "old-refresh", AuthorID: "author", Version: 1, OccurredAt: time.Now()}, Text: "@_user_1 @_user_2"}
	m.Raw = []byte(`{"kind":"refresh","sender_type":"user","addressed":false,"provider":{"mentions":[{"key":"@_user_1","id":"target","id_type":"open_id","name":"wrong-name"},{"key":"@_user_2","id":"target","id_type":"union_id","name":"wrong-person"}]}}`)
	view := presenter.message(m)
	if view.Text != "@乙 @_user_2" || view.Addressed || len(view.Mentions) != 1 || view.Mentions[0].MemberRef == "" {
		t.Fatalf("legacy refresh lost or broadened local identity hints: %+v", view)
	}
	m.Source.MessageID = "another-message"
	m.Raw = []byte(`{"kind":"refresh","sender_type":"user","provider":{"mentions":[{"key":"@_user_1","id":"author","id_type":"open_id","name":"甲"}]}}`)
	if view = presenter.message(m); view.Text != "@甲 @_user_2" {
		t.Fatalf("legacy placeholder treated as global: %+v", view)
	}
}

func TestSceneRoleFactsSurviveMentionAndBoundMaterialOrder(t *testing.T) {
	scope := sceneScope()
	at := time.Now().UTC()
	botRaw := []byte(`{"kind":"refresh","sender_type":"bot"}`)
	bot := domain.Message{Source: domain.SourceRef{Scope: scope, MessageID: "bound-bot", AuthorID: "other-bot", Version: 1, OccurredAt: at}, Text: "其他机器人的原话", Raw: botRaw}
	current := domain.Message{Source: domain.SourceRef{Scope: scope, MessageID: "current-human", AuthorID: "human-a", Version: 1, OccurredAt: at.Add(time.Second)}, Text: "@_user_1 你怎么看", ParentID: bot.Source.MessageID, Raw: []byte(`{"kind":"message","sender_type":"user","mentions":[{"Key":"@_user_1","OpenID":"other-bot","Name":"Bot B"},{"Key":"@_user_2","OpenID":"mention-only","Name":"Mention only"}]}`)}
	for _, order := range []string{"current_then_bound", "bound_then_current"} {
		t.Run(order, func(t *testing.T) {
			people := newParticipantSet(scope, "own-bot", memberSnapshot{people: map[string]domain.GroupMember{}})
			presenter := scenePresenter{people: people, messages: map[string]domain.Message{bot.Source.MessageID: bot, current.Source.MessageID: current}}
			var currentView, boundView sceneMessage
			if order == "current_then_bound" {
				currentView = presenter.message(current)
				boundView = presenter.message(bot)
			} else {
				boundView = presenter.message(bot)
				currentView = presenter.message(current)
			}
			if currentView.Role != "member" || boundView.Role != "bot" || currentView.ReplyTo == nil || currentView.ReplyTo.Role != "bot" {
				t.Fatalf("mention determined a real speaker/reply role: current=%+v bound=%+v", currentView, boundView)
			}
			participants := people.selected([]sceneMessage{currentView, boundView})
			foundBot, foundUnknown := false, false
			for _, person := range participants {
				if person.ID == "other-bot" {
					foundBot = true
					if person.Role != "bot" {
						t.Fatalf("bound bot role was cached as %s", person.Role)
					}
				}
				if person.ID == "mention-only" {
					foundUnknown = true
					if person.Role != "unknown" {
						t.Fatalf("mention asserted participant role %s", person.Role)
					}
				}
			}
			if !foundBot || !foundUnknown {
				t.Fatal("role test omitted referenced participant")
			}
		})
	}
}

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
)

type sceneMemberReader func(context.Context, domain.Scope, int, string) (domain.GroupMembers, error)

func (f sceneMemberReader) GroupMembers(ctx context.Context, s domain.Scope, n int, token string) (domain.GroupMembers, error) {
	return f(ctx, s, n, token)
}

type sceneCapture struct{ requests []modelapi.ChatRequest }

func (m *sceneCapture) Generate(_ context.Context, r modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.requests = append(m.requests, r)
	return modelapi.ChatResponse{Message: modelapi.ChatMessage{Role: "assistant", Content: "read"}}, nil
}

type capturedSceneMessage struct {
	MessageID, SpeakerRef, SpeakerName, Role, ParentID, Text string
	Mentions                                                 []struct{ Key, MemberRef, Name, NameOrigin string }
	ReplyTo                                                  *struct {
		MessageID, SpeakerRef, SpeakerName, Role string
		Available                                bool
	}
}
type capturedScene struct {
	Participants                       []struct{ MemberRef, ID, Name, Role, NameOrigin string }
	MemberDirectory                    struct{ Complete, Unavailable, Truncated bool }
	CurrentUtterance                   capturedSceneMessage
	RecentConversation, BoundMaterials []capturedSceneMessage
	Truncated                          bool
}

func sceneInput(t *testing.T, request modelapi.ChatRequest) (capturedScene, string) {
	t.Helper()
	for _, m := range request.Messages {
		if m.Role == "user" {
			raw, ok := m.Content.(string)
			if !ok {
				t.Fatal("scene is not text")
			}
			var got capturedScene
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatal(err)
			}
			return got, raw
		}
	}
	t.Fatal("native request has no scene")
	return capturedScene{}, ""
}
func sceneAccept(t *testing.T, f *autoEffectFixture, id, author, text, parent string, at time.Time, mentions ...domain.IngressMention) domain.Message {
	t.Helper()
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user", Mentions: mentions})
	m := domain.Message{Source: domain.SourceRef{Scope: f.task.Scope, MessageID: id, AuthorID: author, Version: 1, OccurredAt: at}, EventID: id, Text: text, ParentID: parent, UpdatedAt: at, Raw: raw}
	if _, err := f.store.Accept(context.Background(), domain.Event{ID: id, Type: domain.EventMessage, Message: m, OccurredAt: at}); err != nil {
		t.Fatal(err)
	}
	return m
}
func TestNativeSpeakerSceneKeepsCurrentAuthorLocalMentionsAndReplyDistinct(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	now := time.Now().UTC().Add(-time.Second)
	a := sceneAccept(t, f, "quote-a", "member-a", "@_user_1 说得对", "", now, domain.IngressMention{Key: "@_user_1", OpenID: "member-c", Name: "丙"})
	b := sceneAccept(t, f, "reply-b", "member-b", "我在这里", "", now.Add(time.Millisecond))
	c := sceneAccept(t, f, "quote-c", "member-c", "@_user_1 认识乙，乙很会讲课", "", now.Add(2*time.Millisecond), domain.IngressMention{Key: "@_user_1", OpenID: "member-a", Name: "甲"})
	current := sceneAccept(t, f, "scene-current", "member-a", "@_user_1 是谁？", b.Source.MessageID, now.Add(3*time.Millisecond), domain.IngressMention{Key: "@_user_1", OpenID: "member-b", Name: "乙"})
	task, err := f.store.CreateTask(context.Background(), current, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.store.ClaimJob(context.Background(), []domain.JobKind{domain.JobAgentTurn}, "scene-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	members := sceneMemberReader(func(context.Context, domain.Scope, int, string) (domain.GroupMembers, error) {
		return domain.GroupMembers{Members: []domain.GroupMember{{ID: "member-a", Name: "甲", IDType: "open_id"}, {ID: "member-b", Name: "乙", IDType: "open_id"}, {ID: "member-c", Name: "丙", IDType: "open_id"}}}, nil
	})
	f.engine.c.GroupMembers = members
	model := &sceneCapture{}
	f.engine.c.Model = model
	if err = f.engine.Execute(context.Background(), job, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("native calls=%d", len(model.requests))
	}
	got, raw := sceneInput(t, model.requests[0])
	if got.CurrentUtterance.SpeakerName != "甲" || got.CurrentUtterance.Text != "@乙 是谁？" {
		t.Fatalf("native request lost speaker and per-message target: %+v", got.CurrentUtterance)
	}
	if got.CurrentUtterance.ReplyTo == nil || got.CurrentUtterance.ReplyTo.SpeakerName != "乙" || got.CurrentUtterance.ReplyTo.MessageID != b.Source.MessageID {
		t.Fatalf("reply author was not bound: %+v", got.CurrentUtterance.ReplyTo)
	}
	entries := append(append([]capturedSceneMessage{}, got.RecentConversation...), got.BoundMaterials...)
	byID := map[string]capturedSceneMessage{}
	for _, m := range entries {
		if _, ok := byID[m.MessageID]; ok {
			t.Fatalf("duplicate material %s", m.MessageID)
		}
		byID[m.MessageID] = m
		if m.MessageID == current.Source.MessageID {
			t.Fatal("current utterance repeated in background")
		}
	}
	if byID[a.Source.MessageID].Text != "@丙 说得对" || byID[c.Source.MessageID].Text != "@甲 认识乙，乙很会讲课" || byID[c.Source.MessageID].SpeakerName != "丙" {
		t.Fatalf("local mention tokens or quoted speaker confused: %+v", byID)
	}
	if !got.MemberDirectory.Complete || len(got.Participants) < 3 {
		t.Fatalf("environment members missing: %+v", got)
	}
	for _, key := range []string{`"Scope"`, `"Version"`, `"Source"`, `"Sources"`} {
		if strings.Contains(raw, key) {
			t.Fatalf("private proof leaked in reading view: %s", key)
		}
	}
	pinned, err := f.store.Task(context.Background(), task.Scope, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []domain.SourceRef{a.Source, b.Source, c.Source, current.Source} {
		found := false
		for _, ref := range pinned.ContextSources {
			found = found || ref == want
		}
		if !found {
			t.Fatalf("reading view dropped backend dependency %s", want.MessageID)
		}
	}
}

func TestNativeSpeakerSceneTrimsProofUnionWithoutLosingCurrentReplyIdentity(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	ctx := context.Background()
	refs := []domain.SourceRef{}
	for i := 0; i < 256; i++ {
		m := sceneAccept(t, f, fmt.Sprintf("proof-%03d", i), "past-author", "background", "", time.Now().UTC().Add(-time.Hour))
		refs = append(refs, m.Source)
	}
	receipt := domain.Delivery{ID: "proof-receipt", TaskID: f.task.ID, Scope: f.task.Scope, Revision: 1, Kind: "final", MessageType: "text", Content: `{"text":"old assistant claim"}`, Status: "delivered", MessageID: "past-assistant", Sources: refs, UpdatedAt: time.Now().UTC().Add(-time.Second)}
	body, _ := json.Marshal(receipt)
	if _, err := f.db.Exec(`INSERT INTO "`+f.schema+`".deliveries(scope_key,delivery_id,task_id,revision,message_id,status,body) VALUES($1,$2,$3,$4,$5,$6,$7)`, receipt.Scope.Key(), receipt.ID, receipt.TaskID, receipt.Revision, receipt.MessageID, receipt.Status, body); err != nil {
		t.Fatal(err)
	}
	current := sceneAccept(t, f, "proof-current", "current-author", "你刚才是什么意思？", receipt.MessageID, time.Now().UTC().Add(-time.Millisecond))
	task, err := f.store.CreateTask(ctx, current, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "proof-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	model := &sceneCapture{}
	f.engine.c.Model = model
	if err = f.engine.Execute(ctx, job, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision}); err != nil {
		t.Fatalf("proof union must trim old text before losing current reply: %v", err)
	}
	got, _ := sceneInput(t, model.requests[0])
	if !got.Truncated || got.CurrentUtterance.ReplyTo == nil || got.CurrentUtterance.ReplyTo.Role != "assistant" || got.CurrentUtterance.ReplyTo.SpeakerRef != "self" {
		t.Fatalf("trim lost current reply identity: %+v", got)
	}
	for _, m := range got.RecentConversation {
		if m.MessageID == receipt.MessageID {
			t.Fatal("256 proof reply was not omitted as a whole")
		}
	}
	pinned, err := f.store.Task(ctx, task.Scope, task.ID)
	if err != nil || len(pinned.ContextSources) > 256 {
		t.Fatalf("private refs escaped cap: %d %v", len(pinned.ContextSources), err)
	}
}

func TestNativeDerivedMediaReadingViewKeepsTextAndHidesPrivateObjectProof(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Second)
	raw, _ := json.Marshal(domain.IngressMetadata{Kind: "message", SenderType: "user"})
	message := domain.Message{Source: domain.SourceRef{Scope: f.task.Scope, MessageID: "media-scene", AuthorID: "image-author", Version: 1, OccurredAt: at}, EventID: "media-scene", Text: "看这张图片", UpdatedAt: at, Raw: raw, Media: []domain.Media{{Type: domain.MediaImage, Source: domain.MediaSourceLark, ResourceID: "image-resource", Sequence: 0}}}
	if _, err := f.store.Accept(ctx, domain.Event{ID: message.EventID, Type: domain.EventMessage, Message: message, OccurredAt: at}); err != nil {
		t.Fatal(err)
	}
	mediaJob, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobMedia}, "media-scene-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	artifact := domain.MediaArtifact{Source: message.Source, Parts: []domain.MediaPart{{Object: domain.Object{Source: message.Source, ResourceID: "image-resource", Sequence: 0, Key: "private-object-proof-path"}, Text: "图片中的课程表", Page: 1}}}
	if err = f.store.PutMedia(ctx, mediaJob, artifact); err != nil {
		t.Fatal(err)
	}
	task, err := f.store.CreateTask(ctx, message, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "media-scene-turn", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	model := &sceneCapture{}
	f.engine.c.Model = model
	if err = f.engine.Execute(ctx, job, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision}); err != nil {
		t.Fatal(err)
	}
	_, view := sceneInput(t, model.requests[0])
	if !strings.Contains(view, "图片中的课程表") || !strings.Contains(view, "image-resource") {
		t.Fatal("reading view lost parsed media text or locator")
	}
	for _, key := range []string{`"Scope"`, `"Version"`, `"Source"`, `"Object"`, "private-object-proof-path"} {
		if strings.Contains(view, key) {
			t.Fatalf("private media proof leaked into model scene: %s", key)
		}
	}
	pinned, err := f.store.Task(ctx, task.Scope, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range pinned.ContextSources {
		found = found || ref == message.Source
	}
	if !found {
		t.Fatal("hiding media proof discarded backend dependency")
	}
}

func TestNativeCurrentMentionAndReplyKeepOtherBotRole(t *testing.T) {
	f := newAutoEffectFixture(t, true, false)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Second)
	bot := domain.Message{Source: domain.SourceRef{Scope: f.task.Scope, MessageID: "native-bound-bot", AuthorID: "other-bot", Version: 1, OccurredAt: at}, EventID: "native-bound-bot", Text: "其他机器人的原话", Raw: []byte(`{"kind":"refresh","sender_type":"bot"}`), UpdatedAt: at}
	if _, err := f.store.Accept(ctx, domain.Event{ID: bot.EventID, Type: domain.EventMessage, Message: bot, OccurredAt: at}); err != nil {
		t.Fatal(err)
	}
	current := sceneAccept(t, f, "native-role-current", "human-a", "@_user_1 你怎么看", bot.Source.MessageID, at.Add(time.Millisecond), domain.IngressMention{Key: "@_user_1", OpenID: "other-bot", Name: "Bot B"})
	task, err := f.store.CreateTask(ctx, current, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.store.ClaimJob(ctx, []domain.JobKind{domain.JobAgentTurn}, "native-role-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	model := &sceneCapture{}
	f.engine.c.Model = model
	f.engine.c.BotOpenID = "own-bot"
	if err = f.engine.Execute(ctx, job, domain.TurnRequest{TaskID: task.ID, Revision: task.Revision}); err != nil {
		t.Fatal(err)
	}
	got, _ := sceneInput(t, model.requests[0])
	if got.CurrentUtterance.ReplyTo == nil || got.CurrentUtterance.ReplyTo.Role != "bot" {
		t.Fatalf("other bot reply partner became a member: %+v", got.CurrentUtterance.ReplyTo)
	}
	found := false
	for _, m := range got.BoundMaterials {
		if m.MessageID == bot.Source.MessageID {
			found = true
			if m.Role != "bot" {
				t.Fatalf("other bot original became %s", m.Role)
			}
		}
	}
	if !found {
		t.Fatal("other bot explicit bound material missing")
	}
	found = false
	for _, person := range got.Participants {
		if person.ID == bot.Source.AuthorID {
			found = true
			if person.Role != "bot" {
				t.Fatalf("other bot participant became %s", person.Role)
			}
		}
	}
	if !found {
		t.Fatal("other bot participant missing")
	}
}

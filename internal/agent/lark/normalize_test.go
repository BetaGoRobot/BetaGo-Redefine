package lark

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func incoming(t *testing.T, typ, content string) *im.P2MessageReceiveV1 {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"schema": "2.0", "header": map[string]any{"event_id": "ev1", "event_type": "im.message.receive_v1", "app_id": "app", "tenant_key": "tenant", "create_time": "1792650000000"}, "event": map[string]any{"sender": map[string]any{"sender_id": map[string]any{"open_id": "alice"}, "sender_type": "user", "tenant_key": "tenant"}, "message": map[string]any{"message_id": "msg", "chat_id": "group", "chat_type": "group", "message_type": typ, "content": content, "create_time": "1792649990000", "update_time": "1792649991000", "parent_id": "parent", "root_id": "root", "thread_id": "omt_thread", "mentions": []any{map[string]any{"key": "@_user_1", "id": map[string]any{"open_id": "bot"}}}}}})
	var e im.P2MessageReceiveV1
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	return &e
}

func normalizer(t *testing.T) *Normalizer {
	t.Helper()
	n, err := NewNormalizer(NormalizeConfig{AppID: "app", BotOpenID: "bot", AllowedChatIDs: []string{"group"}})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNormalizeSDKTextAndMentionPreservesProviderIdentity(t *testing.T) {
	e, err := normalizer(t).Message(incoming(t, "text", `{"text":"@_user_1 查预算"}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.Message.Source.Scope.TenantID != "tenant" || e.Message.Source.AuthorID != "alice" || e.Message.ParentID != "parent" || e.Message.ThreadID != "omt_thread" || e.Message.RootID != "root" || e.Message.Text != "@_user_1 查预算" || e.Message.Source.Version != 1792649991000*2 {
		t.Fatal("source identity, reply or provider version lost")
	}
	var meta domain.IngressMetadata
	if json.Unmarshal(e.Message.Raw, &meta) != nil || !meta.Addressed || meta.SenderType != "user" {
		t.Fatal("trusted ingress metadata missing")
	}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeIgnoresPrivateChatsAndRejectsCrossIdentity(t *testing.T) {
	n := normalizer(t)
	e := incoming(t, "text", `{"text":"hi"}`)
	private := "p2p"
	e.Event.Message.ChatType = &private
	if _, err := n.Message(e); !errors.Is(err, ErrIgnored) {
		t.Fatal("private chat was accepted")
	}
	e = incoming(t, "text", `{"text":"hi"}`)
	e.EventV2Base.Header.AppID = "other"
	if _, err := n.Message(e); err == nil {
		t.Fatal("another app event accepted")
	}
	e = incoming(t, "text", `{"text":"hi"}`)
	other := "other-tenant"
	e.Event.Sender.TenantKey = &other
	if _, err := n.Message(e); err == nil {
		t.Fatal("mismatched sender tenant accepted")
	}
	e = incoming(t, "text", `{"text":"hi"}`)
	other = "other-group"
	e.Event.Message.ChatId = &other
	if _, err := n.Message(e); !errors.Is(err, ErrIgnored) {
		t.Fatal("disallowed group accepted")
	}
}

func TestNormalizeRichPostKeepsMediaOrderAndOriginalContent(t *testing.T) {
	content := `{"zh_cn":{"title":"项目","content":[[{"tag":"text","text":"第一段"},{"tag":"img","image_key":"img1"}],[{"tag":"a","text":"说明","href":"https://example.test"},{"tag":"img","image_key":"img2"}]]}}`
	e, err := normalizer(t).Message(incoming(t, "post", content))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Message.Media) != 2 || e.Message.Media[0].ResourceID != "img1" || e.Message.Media[1].ResourceID != "img2" || e.Message.Media[1].Sequence != 1 || !strings.Contains(e.Message.Text, "第一段") || !strings.Contains(e.Message.Text, "https://example.test") {
		t.Fatal("rich post lost content/order")
	}
}

func TestRecalledMessageAtSameMillisecondSupersedesLiveVersion(t *testing.T) {
	var e im.P2MessageRecalledV1
	json.Unmarshal([]byte(`{"header":{"event_id":"recalled","app_id":"app","tenant_key":"tenant","create_time":"1792649991000"},"event":{"message_id":"msg","chat_id":"group","recall_time":"1792649991000"}}`), &e)
	d, err := normalizer(t).Recalled(&e)
	if err != nil {
		t.Fatal(err)
	}
	if d.Type != domain.EventDelete || !d.Message.Deleted || d.Message.Source.Version != 1792649991000*2+1 || d.Message.Source.AuthorID != "" {
		t.Fatal("delete invented author or lost ordering")
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCardActionUsesVerifiedOperatorAndFrozenRevision(t *testing.T) {
	var e callback.CardActionTriggerEvent
	json.Unmarshal([]byte(`{"header":{"event_id":"card1","app_id":"app","tenant_key":"tenant","create_time":"1792650000000000"},"event":{"operator":{"open_id":"bob","tenant_key":"tenant"},"context":{"open_chat_id":"group","open_message_id":"card-msg"},"token":"must-not-persist","action":{"value":{"action":"approve","task_id":"task1","revision":"3","approval_id":"approval1"}}}}`), &e)
	d, err := normalizer(t).Card(&e)
	if err != nil {
		t.Fatal(err)
	}
	var meta domain.IngressMetadata
	json.Unmarshal(d.Message.Raw, &meta)
	if d.Message.Source.AuthorID != "bob" || meta.Control == nil || meta.Control.TaskID != "task1" || meta.Control.Revision != 3 || meta.Control.ApprovalID != "approval1" || strings.Contains(string(d.Message.Raw), "must-not-persist") {
		t.Fatal("card identity or frozen approval reference invalid")
	}
	e.Event.Action.Value["revision"] = "0"
	if _, err := normalizer(t).Card(&e); err == nil {
		t.Fatal("invalid revision accepted")
	}
}

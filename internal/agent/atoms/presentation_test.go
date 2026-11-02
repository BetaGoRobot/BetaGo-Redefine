package atoms

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type presentationMessengerFixture struct {
	scope                      domain.Scope
	parent, typ, content, uuid string
	calls                      int
}

func (f *presentationMessengerFixture) Send(_ context.Context, s domain.Scope, parent, typ, content, uuid string) (string, error) {
	f.scope = s
	f.parent = parent
	f.typ = typ
	f.content = content
	f.uuid = uuid
	f.calls++
	return "card-receipt", nil
}

type presentationRetractorFixture struct{}

func (presentationRetractorFixture) Retract(context.Context, domain.Scope, string) error { return nil }
func TestPresentationSendHasStableSameGroupReceiptAndRejectsWithdrawnSources(t *testing.T) {
	message := sourceLocalMessage()
	sources := &localSourceFixture{message: message}
	sender := &presentationMessengerFixture{}
	r, err := New(Options{Extensions: PresentationDefinitions(PresentationOptions{Messenger: sender, Retractor: presentationRetractorFixture{}, Sources: sources})})
	if err != nil {
		t.Fatal(err)
	}
	d := researchNamed(t, r, "feishu_send_card")
	if !d.SideEffect || !d.AutoExecute {
		t.Fatal("ordinary requested group card not automatically executable")
	}
	if retract := researchNamed(t, r, "feishu_retract_message"); !retract.SideEffect || retract.AutoExecute {
		t.Fatal("retraction needs explicit approval")
	}
	input := `{"card":{"title":"Report","sections":[{"kind":"markdown","text":"Ready"}]},"source_message_ids":["message"]}`
	in := Invocation{Task: domain.Task{ID: "task", Scope: message.Source.Scope, Root: message.Source}, ExecutionKey: "execution"}
	out, err := d.Run(context.Background(), in, input)
	if err != nil {
		t.Fatal(err)
	}
	if sender.scope != message.Source.Scope || sender.parent != "message" || sender.typ != "interactive" || sender.uuid != domain.StableUUID(message.Source.Scope.Key(), "task", "execution") {
		t.Fatalf("send scope/idempotency wrong: %+v", sender)
	}
	var receipt struct {
		MessageID string `json:"message_id"`
	}
	if json.Unmarshal([]byte(out), &receipt) != nil || receipt.MessageID != "card-receipt" {
		t.Fatalf("missing actual receipt: %s", out)
	}
	refs, err := d.Sources(out)
	if err != nil || len(refs) != 1 || refs[0] != message.Source {
		t.Fatalf("source laundering: %+v %v", refs, err)
	}
	sources.message.Deleted = true
	if _, err = d.Run(context.Background(), in, input); !errors.Is(err, domain.ErrStaleSource) || sender.calls != 1 {
		t.Fatalf("withdrawn evidence sent: %v calls=%d", err, sender.calls)
	}
	for _, raw := range []string{`{"card":{"title":"X","sections":[{"kind":"markdown","text":"x"}],"actions":[]}}`, `{"card":{"title":"X","sections":[{"kind":"markdown","text":"x"}]},"chat_id":"foreign"}`} {
		if _, err = d.Validate(raw); err == nil {
			t.Fatalf("raw action/scope accepted: %s", raw)
		}
	}
}

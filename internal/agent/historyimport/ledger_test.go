package historyimport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type readFunc func(context.Context, domain.Scope, string) (domain.Message, error)

func (f readFunc) ReadMessage(ctx context.Context, s domain.Scope, id string) (domain.Message, error) {
	return f(ctx, s, id)
}
func TestPrepareFreezesExactInventoryAndResumesWithoutRereading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "canonical.jsonl")
	inventory := []byte("a\nb\nc\nd\ne\n")
	calls := map[string]int{}
	reader := readFunc(func(_ context.Context, s domain.Scope, id string) (domain.Message, error) {
		calls[id]++
		m := message(id, "human", "evidence", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
		switch id {
		case "b":
			m.Raw = json.RawMessage(`{"kind":"refresh","sender_type":"app"}`)
		case "c":
			m.Deleted = true
		case "d":
			return domain.Message{}, domain.ErrNotFound
		case "e":
			m.Text = ""
			provider(m, "text", map[string]any{"text": ""}, &m)
		}
		return m, nil
	})
	report, err := Prepare(context.Background(), reader, scope(), inventory, path, 10)
	if err != nil || !report.Complete || report.Counts["human"] != 1 || report.Counts["bot"] != 1 || report.Counts["deleted"] != 1 || report.Counts["unreadable"] != 1 || report.Counts["unsupported"] != 1 {
		t.Fatalf("prepare report: %+v %v", report, err)
	}
	ledger, err := ReadLedger(path, scope(), inventory)
	if err != nil || len(ledger.Records) != 5 {
		t.Fatalf("frozen ledger: %+v %v", ledger, err)
	}
	if _, err = Prepare(context.Background(), reader, scope(), inventory, path, 10); err != nil {
		t.Fatal(err)
	}
	for id, n := range calls {
		if n != 1 {
			t.Fatalf("reread %s %d times", id, n)
		}
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatalf("ledger mode=%o", st.Mode().Perm())
	}
	if _, err = ReadLedger(path, scope(), []byte("a\nb\n")); err == nil {
		t.Fatal("changed inventory accepted")
	}
	other := scope()
	other.ChatID = "elsewhere"
	if _, err = ReadLedger(path, other, inventory); err == nil {
		t.Fatal("changed scope accepted")
	}
}
func TestLedgerRejectsIncompleteDuplicateAndForgedHumanBeforeApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger")
	inventory := []byte("a\n")
	reader := readFunc(func(_ context.Context, _ domain.Scope, id string) (domain.Message, error) {
		return message(id, "u", "x", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)), nil
	})
	if _, err := Prepare(context.Background(), reader, scope(), inventory, path, 10); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	lines := strings.Split(string(original), "\n")
	for name, body := range map[string]string{"incomplete": strings.Join(lines[:2], "\n") + "\n", "duplicate": lines[0] + "\n" + lines[1] + "\n" + lines[1] + "\n" + lines[2] + "\n", "forged": strings.ReplaceAll(string(original), `\"sender_type\":\"user\"`, `\"sender_type\":\"app\"`)} {
		// RawMessage is embedded JSON; replacement is deliberately direct.
		if name == "forged" {
			body = strings.ReplaceAll(string(original), `"sender_type":"user"`, `"sender_type":"app"`)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadLedger(path, scope(), inventory); err == nil {
			t.Fatalf("accepted %s ledger", name)
		}
	}
}
func TestPrepareTransientFailuresRemainResumable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger")
	inventory := []byte("a\n")
	calls := 0
	reader := readFunc(func(_ context.Context, _ domain.Scope, id string) (domain.Message, error) {
		calls++
		if calls <= 3 {
			return domain.Message{}, errors.New("Lark atomic API call failed")
		}
		return message(id, "u", "recovered", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)), nil
	})
	report, err := Prepare(context.Background(), reader, scope(), inventory, path, 10)
	if err == nil || report.Complete {
		t.Fatal("transient ledger marked complete")
	}
	if _, err = ReadLedger(path, scope(), inventory); err == nil {
		t.Fatal("incomplete ledger accepted")
	}
	report, err = Prepare(context.Background(), reader, scope(), inventory, path, 10)
	if err != nil || !report.Complete || calls != 4 {
		t.Fatalf("resume failed: %+v calls=%d err=%v", report, calls, err)
	}
}

func TestCanonicalValidationRejectsFactsThatDisagreeWithProvider(t *testing.T) {
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	original := message("a", "alice", "actual provider text", at)
	for name, mutate := range map[string]func(*domain.Message){"author": func(m *domain.Message) { m.Source.AuthorID = "forged" }, "text": func(m *domain.Message) { m.Text = "forged" }, "parent": func(m *domain.Message) { m.ParentID = "forged" }, "time": func(m *domain.Message) {
		m.UpdatedAt = m.UpdatedAt.Add(time.Millisecond)
		m.Source.Version += 2
		m.EventID = refreshID(*m)
	}} {
		m := original.Clone()
		mutate(&m)
		if err := validateCanonical(m, scope(), "a"); err == nil {
			t.Fatalf("provider disagreement accepted: %s", name)
		}
	}
}

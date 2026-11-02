package atoms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type localSourceFixture struct {
	message   domain.Message
	err       error
	remote    domain.Message
	remoteErr error
	reads     int
}

func (f *localSourceFixture) Message(context.Context, domain.Scope, string) (domain.Message, error) {
	return f.message, f.err
}
func (f *localSourceFixture) Recent(context.Context, domain.Scope, time.Time, int) ([]domain.Message, error) {
	return nil, nil
}
func (f *localSourceFixture) ValidSources(context.Context, domain.Query, []domain.SourceRef) (bool, error) {
	return true, nil
}
func (f *localSourceFixture) Send(context.Context, domain.Scope, string, string, string, string) (string, error) {
	return "", nil
}
func (f *localSourceFixture) ReadMessage(context.Context, domain.Scope, string) (domain.Message, error) {
	f.reads++
	return f.remote, f.remoteErr
}

func sourceLocalMessage() domain.Message {
	at := time.Unix(1792650000, 0).UTC()
	return domain.Message{Source: domain.SourceRef{Scope: domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "group", Visibility: "group"}, MessageID: "message", AuthorID: "alice", Version: 1, OccurredAt: at}, Text: "local original", UpdatedAt: at}
}
func TestCurrentMessageKeepsLocalOriginalAndTombstoneWithoutRemoteReads(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		for _, remoteUnavailable := range []bool{false, true} {
			m := sourceLocalMessage()
			m.Deleted = deleted
			if deleted {
				m.Source.AuthorID = ""
				m.Source.OccurredAt = time.Time{}
			}
			newer := sourceLocalMessage()
			newer.Source.Version++
			newer.Text = "remote newer"
			f := &localSourceFixture{message: m, remote: newer}
			if remoteUnavailable {
				f.remoteErr = errors.New("remote unavailable")
			}
			got, err := currentMessage(context.Background(), Options{Sources: f, Messenger: f}, m.Source.Scope, m.Source.MessageID)
			if err != nil || got.Text != m.Text || got.Deleted != deleted || got.Source != m.Source || f.reads != 0 {
				t.Fatalf("local row not authoritative: deleted=%v unavailable=%v got=%+v err=%v reads=%d", deleted, remoteUnavailable, got, err, f.reads)
			}
		}
	}
}
func TestCurrentMessageFailsClosedOnLocalErrorsAndWrongIdentity(t *testing.T) {
	storageErr := errors.New("database unavailable")
	for _, mode := range []string{"database", "scope", "id", "canceled"} {
		m := sourceLocalMessage()
		f := &localSourceFixture{message: m, remote: m}
		ctx := context.Background()
		want := domain.ErrUnauthorized
		switch mode {
		case "database":
			f.err = storageErr
			want = storageErr
		case "scope":
			f.message.Source.Scope.ChatID = "other"
		case "id":
			f.message.Source.MessageID = "other"
		case "canceled":
			c, cancel := context.WithCancel(ctx)
			cancel()
			ctx = c
			want = context.Canceled
		}
		if _, err := currentMessage(ctx, Options{Sources: f, Messenger: f}, m.Source.Scope, m.Source.MessageID); !errors.Is(err, want) || f.reads != 0 {
			t.Fatalf("%s: err=%v reads=%d", mode, err, f.reads)
		}
	}
}
func TestCurrentMessageFetchesOnlyMissingLocalAndVerifiesRemoteIdentity(t *testing.T) {
	for _, mode := range []string{"missing", "scope", "id", "failure"} {
		m := sourceLocalMessage()
		f := &localSourceFixture{err: domain.ErrNotFound, remote: m}
		var want error
		switch mode {
		case "scope":
			f.remote.Source.Scope.ChatID = "other"
			want = domain.ErrUnauthorized
		case "id":
			f.remote.Source.MessageID = "other"
			want = domain.ErrUnauthorized
		case "failure":
			f.remoteErr = errors.New("remote unavailable")
			want = f.remoteErr
		}
		got, err := currentMessage(context.Background(), Options{Sources: f, Messenger: f}, m.Source.Scope, m.Source.MessageID)
		if !errors.Is(err, want) || f.reads != 1 {
			t.Fatalf("%s: err=%v reads=%d", mode, err, f.reads)
		}
		if want == nil && got.Source != m.Source {
			t.Fatal("missing row not explicitly read")
		}
	}
}

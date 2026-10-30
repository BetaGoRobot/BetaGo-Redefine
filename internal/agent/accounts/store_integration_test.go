package accounts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

type fixture struct {
	*Store
	db *sql.DB
}

// Each test owns only a random schema in the explicitly configured test DB.
// This fixture never loads application configuration or production credentials.
func setup(t *testing.T) fixture {
	t.Helper()
	dsn := os.Getenv("AGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required: set AGENT_TEST_POSTGRES_DSN")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal("cannot open isolated PostgreSQL")
	}
	db.SetMaxOpenConns(16)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Fatal("isolated PostgreSQL unavailable")
	}
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		db.Close()
		t.Fatal("test randomness unavailable")
	}
	schema := "accounts_test_" + hex.EncodeToString(b[:])
	s, err := New(db, schema, []byte("01234567890123456789012345678901"))
	if err != nil {
		db.Close()
		t.Fatal("cannot construct test store")
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`); err != nil {
			t.Error("cannot remove owned test schema")
		}
		db.Close()
	})
	if err := s.Migrate(ctx); err != nil {
		t.Fatal("accounts migration failed:", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal("accounts migration is not idempotent:", err)
	}
	return fixture{s, db}
}

func mustStatus(t *testing.T, s *Store, p Principal, want string) {
	t.Helper()
	got, err := s.Status(context.Background(), p)
	if err != nil || got != want {
		t.Fatal("unexpected credential status", got, err)
	}
}

func mustResolve(t *testing.T, s *Store, p Principal, want string) {
	t.Helper()
	got, err := s.Resolve(context.Background(), p)
	if err != nil || got != want {
		t.Fatal("unexpected resolved credential")
	}
}

func issued(t *testing.T, s *Store, p Principal, key, message string) Binding {
	t.Helper()
	b, err := s.BeginBinding(context.Background(), p, key)
	if err != nil {
		t.Fatal("cannot begin binding", err)
	}
	if err := s.SetBindingMessage(context.Background(), p, b.ID, message); err != nil {
		t.Fatal("cannot attach private message", err)
	}
	b.MessageID = message
	return b
}

func TestCredentialsArePersonalEncryptedAndNeverShared(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := principal()
	mustStatus(t, f.Store, p, StatusUnbound)
	if _, err := f.Resolve(ctx, p); !errors.Is(err, ErrUnbound) {
		t.Fatal("unbound actor resolved")
	}
	token := "test-opaque-personal-credential-a"
	if err := f.Put(ctx, p, token); err != nil {
		t.Fatal("credential write failed", err)
	}
	mustStatus(t, f.Store, p, StatusBound)
	mustResolve(t, f.Store, p, token)
	otherChat := p
	otherChat.Scope.ChatID, otherChat.Scope.Visibility = "another-chat", "private"
	mustResolve(t, f.Store, otherChat, token)
	for _, change := range []func(*Principal){
		func(p *Principal) { p.ActorID = "other" },
		func(p *Principal) { p.Scope.TenantID = "other" },
		func(p *Principal) { p.Scope.AppID = "other" },
	} {
		other := p
		change(&other)
		mustStatus(t, f.Store, other, StatusUnbound)
		if _, err := f.Resolve(ctx, other); !errors.Is(err, ErrUnbound) {
			t.Fatal("credential leaked to another principal")
		}
	}
	var row string
	if err := f.db.QueryRowContext(ctx, `SELECT row_to_json(c)::text FROM `+f.table("personal_credentials")+` c`).Scan(&row); err != nil {
		t.Fatal("cannot inspect encrypted row")
	}
	if strings.Contains(row, token) {
		t.Fatal("credential stored in plaintext")
	}
}

func TestCredentialInvalidationDoesNotInvalidateReplacement(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := principal()
	old, current := "test-opaque-old-credential", "test-opaque-current-credential"
	if err := f.Put(ctx, p, old); err != nil {
		t.Fatal(err)
	}
	if err := f.Put(ctx, p, current); err != nil {
		t.Fatal(err)
	}
	if err := f.Invalidate(ctx, p, old); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, f.Store, p, StatusBound)
	mustResolve(t, f.Store, p, current)
	if err := f.Invalidate(ctx, p, current); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, f.Store, p, StatusInvalid)
	if got, err := f.Resolve(ctx, p); got != "" || !errors.Is(err, ErrInvalidCredential) {
		t.Fatal("invalid credential was resolved")
	}
	if err := f.Put(ctx, p, old); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, f.Store, p, StatusBound)
}

func TestSwappedCiphertextFailsAuthenticationAndDigestVerification(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	a := principal()
	b := a
	b.ActorID = "another-actor"
	if err := f.Put(ctx, a, "test-opaque-a"); err != nil {
		t.Fatal(err)
	}
	if err := f.Put(ctx, b, "test-opaque-b"); err != nil {
		t.Fatal(err)
	}
	var cipher, digest []byte
	if err := f.db.QueryRowContext(ctx, `SELECT ciphertext,token_digest FROM `+f.table("personal_credentials")+` WHERE actor_id=$1`, b.ActorID).Scan(&cipher, &digest); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE `+f.table("personal_credentials")+` SET ciphertext=$1,token_digest=$2 WHERE actor_id=$3`, cipher, digest, a.ActorID); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Resolve(ctx, a); got != "" || !errors.Is(err, ErrCorruptCredential) {
		t.Fatal("swapped encrypted credential accepted")
	}
	// The keyed token digest is verified as well as GCM authentication.
	if _, err := f.db.ExecContext(ctx, `UPDATE `+f.table("personal_credentials")+` SET token_digest=$1 WHERE actor_id=$2`, make([]byte, 32), b.ActorID); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Resolve(ctx, b); got != "" || !errors.Is(err, ErrCorruptCredential) {
		t.Fatal("altered credential digest accepted")
	}
}

func TestBindingRequiresExactScopeActorAndSentMessage(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := principal()
	b, err := f.BeginBinding(ctx, p, "request-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.ID) < 32 || b.MessageID != "" || time.Until(b.ExpiresAt) < 14*time.Minute || time.Until(b.ExpiresAt) > BindingTTL+time.Second {
		t.Fatal("invalid opaque challenge metadata")
	}
	again, err := f.BeginBinding(ctx, p, "request-one")
	if err != nil || b.ID != again.ID || !b.ExpiresAt.Equal(again.ExpiresAt) {
		t.Fatal("binding request is not idempotent")
	}
	if err := f.CompleteBinding(ctx, p, b.ID, "sent-private-message", "test-opaque-a", false); !errors.Is(err, ErrBindingRejected) {
		t.Fatal("unsent challenge accepted")
	}
	if err := f.SetBindingMessage(ctx, p, b.ID, "sent-private-message"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetBindingMessage(ctx, p, b.ID, "sent-private-message"); err != nil {
		t.Fatal("same message retry rejected")
	}
	if err := f.SetBindingMessage(ctx, p, b.ID, "different-message"); !errors.Is(err, ErrBindingRejected) {
		t.Fatal("challenge message replaced")
	}
	for _, change := range []func(*Principal){
		func(p *Principal) { p.ActorID = "other" },
		func(p *Principal) { p.Scope.TenantID = "other" },
		func(p *Principal) { p.Scope.AppID = "other" },
		func(p *Principal) { p.Scope.ChatID = "other" },
		func(p *Principal) { p.Scope.Visibility = "private" },
	} {
		other := p
		change(&other)
		if err := f.SetBindingMessage(ctx, other, b.ID, "sent-private-message"); !errors.Is(err, ErrBindingRejected) {
			t.Fatal("binding message accepted for another owner")
		}
		if err := f.CompleteBinding(ctx, other, b.ID, "sent-private-message", "test-opaque-a", false); !errors.Is(err, ErrBindingRejected) {
			t.Fatal("binding accepted for another owner")
		}
	}
	if err := f.CompleteBinding(ctx, p, b.ID, "different-message", "test-opaque-a", false); !errors.Is(err, ErrBindingRejected) {
		t.Fatal("binding accepted from different message")
	}
	mustStatus(t, f.Store, p, StatusUnbound)
	if err := f.CompleteBinding(ctx, p, b.ID, "sent-private-message", "test-opaque-a", false); err != nil {
		t.Fatal(err)
	}
	mustResolve(t, f.Store, p, "test-opaque-a")
}

func TestBindingRejectsAlteredReplayButAllowsExactDuplicateWithoutRollback(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := principal()
	b := issued(t, f.Store, p, "request-one", "private-one")
	if err := f.CompleteBinding(ctx, p, b.ID, b.MessageID, "test-opaque-a", false); err != nil {
		t.Fatal(err)
	}
	if err := f.CompleteBinding(ctx, p, b.ID, b.MessageID, "test-opaque-a", false); err != nil {
		t.Fatal("exact callback retry failed", err)
	}
	if err := f.CompleteBinding(ctx, p, b.ID, b.MessageID, "test-opaque-b", false); !errors.Is(err, ErrBindingRejected) {
		t.Fatal("altered callback replay accepted")
	}
	if err := f.CompleteBinding(ctx, p, b.ID, b.MessageID, "", true); !errors.Is(err, ErrBindingRejected) {
		t.Fatal("changed revoke action accepted")
	}
	newBinding := issued(t, f.Store, p, "request-two", "private-two")
	if err := f.CompleteBinding(ctx, p, newBinding.ID, newBinding.MessageID, "test-opaque-b", false); err != nil {
		t.Fatal(err)
	}
	if err := f.CompleteBinding(ctx, p, b.ID, b.MessageID, "test-opaque-a", false); err != nil {
		t.Fatal(err)
	}
	mustResolve(t, f.Store, p, "test-opaque-b")
	var row string
	if err := f.db.QueryRowContext(ctx, `SELECT row_to_json(b)::text FROM `+f.table("private_bindings")+` b WHERE binding_id=$1`, b.ID).Scan(&row); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row, "test-opaque") {
		t.Fatal("binding challenge persisted credential material")
	}
}

func TestRevokeIsAtomicAndIdempotent(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := principal()
	if err := f.Put(ctx, p, "test-opaque-a"); err != nil {
		t.Fatal(err)
	}
	b := issued(t, f.Store, p, "revoke-request", "private-revoke")
	if err := f.CompleteBinding(ctx, p, b.ID, b.MessageID, "", true); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, f.Store, p, StatusUnbound)
	if _, err := f.Resolve(ctx, p); !errors.Is(err, ErrUnbound) {
		t.Fatal("revoked credential still resolves")
	}
	if err := f.Put(ctx, p, "test-opaque-b"); err != nil {
		t.Fatal(err)
	}
	if err := f.CompleteBinding(ctx, p, b.ID, b.MessageID, "", true); err != nil {
		t.Fatal("duplicate revoke callback rejected", err)
	}
	mustResolve(t, f.Store, p, "test-opaque-b")
	if err := f.CompleteBinding(ctx, p, b.ID, b.MessageID, "test-opaque-b", false); !errors.Is(err, ErrBindingRejected) {
		t.Fatal("consumed revoke became a binding")
	}
}

func TestExpiredBindingRejectsNewOrDuplicateSubmission(t *testing.T) {
	for _, consumed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unused", true: "consumed"}[consumed], func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			p := principal()
			b := issued(t, f.Store, p, "request", "private-message")
			if consumed {
				if err := f.CompleteBinding(ctx, p, b.ID, b.MessageID, "test-opaque-a", false); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.db.ExecContext(ctx, `UPDATE `+f.table("private_bindings")+` SET expires_at=clock_timestamp()-interval '1 second' WHERE binding_id=$1`, b.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.CompleteBinding(ctx, p, b.ID, b.MessageID, "test-opaque-a", false); !errors.Is(err, ErrBindingRejected) {
				t.Fatal("expired challenge accepted")
			}
			if err := f.SetBindingMessage(ctx, p, b.ID, b.MessageID); !errors.Is(err, ErrBindingRejected) {
				t.Fatal("expired challenge message accepted")
			}
			if _, err := f.BeginBinding(ctx, p, "request"); !errors.Is(err, ErrBindingRejected) {
				t.Fatal("expired idempotency key issued another binding")
			}
		})
	}
}

func TestBindingRequestIdempotencyIsScopedToExactRequester(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	a := principal()
	b, err := f.BeginBinding(ctx, a, "same-request")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Principal){func(p *Principal) { p.ActorID = "other" }, func(p *Principal) { p.Scope.ChatID = "other" }, func(p *Principal) { p.Scope.AppID = "other" }} {
		p := a
		change(&p)
		other, err := f.BeginBinding(ctx, p, "same-request")
		if err != nil || other.ID == b.ID {
			t.Fatal("request idempotency crossed requester boundary")
		}
	}
}

func TestCredentialFailureDoesNotConsumeBinding(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := principal()
	b := issued(t, f.Store, p, "request", "private-message")
	if _, err := f.db.ExecContext(ctx, `ALTER TABLE `+f.table("personal_credentials")+` ADD CONSTRAINT test_deny_bound CHECK (status='invalid') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if err := f.CompleteBinding(ctx, p, b.ID, b.MessageID, "test-opaque-a", false); err == nil {
		t.Fatal("database rejection ignored")
	}
	var consumed bool
	if err := f.db.QueryRowContext(ctx, `SELECT consumed_at IS NOT NULL FROM `+f.table("private_bindings")+` WHERE binding_id=$1`, b.ID).Scan(&consumed); err != nil || consumed {
		t.Fatal("failed credential write consumed challenge")
	}
	if _, err := f.db.ExecContext(ctx, `ALTER TABLE `+f.table("personal_credentials")+` DROP CONSTRAINT test_deny_bound`); err != nil {
		t.Fatal(err)
	}
	if err := f.CompleteBinding(ctx, p, b.ID, b.MessageID, "test-opaque-a", false); err != nil {
		t.Fatal("atomic retry failed", err)
	}
	mustResolve(t, f.Store, p, "test-opaque-a")
}

func TestConcurrentBindingIssuanceAndCompletion(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := principal()
	var wg sync.WaitGroup
	results := make(chan Binding, 12)
	errs := make(chan error, 12)
	for range 12 {
		wg.Go(func() { b, err := f.BeginBinding(ctx, p, "concurrent-request"); results <- b; errs <- err })
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal("concurrent issuance failed", err)
		}
	}
	var b Binding
	for r := range results {
		if b.ID != "" && b.ID != r.ID {
			t.Fatal("concurrent duplicate issued different challenge")
		}
		b = r
	}
	if err := f.SetBindingMessage(ctx, p, b.ID, "private-message"); err != nil {
		t.Fatal(err)
	}
	// Different submissions race for one challenge. Exactly one can win.
	type result struct {
		token string
		err   error
	}
	submissions := make(chan result, 2)
	for _, token := range []string{"test-opaque-a", "test-opaque-b"} {
		wg.Go(func() { submissions <- result{token, f.CompleteBinding(ctx, p, b.ID, "private-message", token, false)} })
	}
	wg.Wait()
	close(submissions)
	winner := ""
	rejected := 0
	for r := range submissions {
		if r.err == nil {
			winner = r.token
		} else if errors.Is(r.err, ErrBindingRejected) {
			rejected++
		} else {
			t.Fatal("unexpected concurrent callback error", r.err)
		}
	}
	if winner == "" || rejected != 1 {
		t.Fatal("single-use challenge did not serialize competing submissions")
	}
	mustResolve(t, f.Store, p, winner)
	errs = make(chan error, 12)
	for range 12 {
		wg.Go(func() { errs <- f.CompleteBinding(ctx, p, b.ID, "private-message", winner, false) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal("concurrent exact duplicate callback rejected", err)
		}
	}
}

func TestMigrationLeavesUnrelatedTablesAndImportsNothing(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.db.ExecContext(ctx, `CREATE TABLE "`+f.schema+`".legacy_accounts (note text); INSERT INTO "`+f.schema+`".legacy_accounts VALUES ('legacy-marker')`); err != nil {
		t.Fatal(err)
	}
	if err := f.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM "`+f.schema+`".legacy_accounts`).Scan(&count); err != nil || count != 1 {
		t.Fatal("migration modified unrelated data")
	}
	mustStatus(t, f.Store, principal(), StatusUnbound)
}

func TestConcurrentInvalidationPreservesDifferentReplacement(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := principal()
	for range 12 {
		if err := f.Put(ctx, p, "test-opaque-old"); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Go(func() { errs <- f.Invalidate(ctx, p, "test-opaque-old") })
		wg.Go(func() { errs <- f.Put(ctx, p, "test-opaque-new") })
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal("concurrent credential mutation failed", err)
			}
		}
		mustStatus(t, f.Store, p, StatusBound)
		mustResolve(t, f.Store, p, "test-opaque-new")
	}
}

func TestBindingExpiryAfterWaitingForCredentialLockRollsBackWrite(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := principal()
	if err := f.Put(ctx, p, "test-opaque-old"); err != nil {
		t.Fatal(err)
	}
	b := issued(t, f.Store, p, "blocked-request", "private-message")
	if err := f.db.QueryRowContext(ctx, `UPDATE `+f.table("private_bindings")+` SET expires_at=clock_timestamp()+interval '1 second' WHERE binding_id=$1 RETURNING expires_at`, b.ID).Scan(&b.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM `+f.table("personal_credentials")+` WHERE actor_id=$1 FOR UPDATE`, p.ActorID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.CompleteBinding(ctx, p, b.ID, b.MessageID, "test-opaque-new", false) }()
	// Confirm the callback passed its initial expiry check and is blocked on
	// the credential row, then hold the lock until its real deadline expires.
	for {
		var blocked int
		if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE $1`, "%"+f.table("personal_credentials")+"%").Scan(&blocked); err != nil {
			t.Fatal("cannot observe isolated test lock")
		}
		if blocked > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatal("binding did not wait for credential lock", err)
		case <-ctx.Done():
			t.Fatal("callback did not reach credential lock")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if remaining := time.Until(b.ExpiresAt) + 10*time.Millisecond; remaining > 0 {
		time.Sleep(remaining)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrBindingRejected) {
		t.Fatal("callback accepted after credential lock exceeded deadline", err)
	}
	mustResolve(t, f.Store, p, "test-opaque-old")
	var consumed bool
	if err := f.db.QueryRowContext(ctx, `SELECT consumed_at IS NOT NULL FROM `+f.table("private_bindings")+` WHERE binding_id=$1`, b.ID).Scan(&consumed); err != nil || consumed {
		t.Fatal("expired callback consumed challenge")
	}
}

package accounts

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func principal() Principal {
	return Principal{Scope: domain.Scope{TenantID: "tenant", AppID: "app", ChatID: "chat", Visibility: "group"}, ActorID: "actor"}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(&sql.DB{}, "accounts_test", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal("cannot construct credential store")
	}
	return s
}

func TestNewRequiresAES256KeyAndSafeSchema(t *testing.T) {
	for _, n := range []int{0, 16, 24, 31, 33} {
		if _, err := New(&sql.DB{}, "accounts_test", make([]byte, n)); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("accepted invalid encryption key")
		}
	}
	for _, schema := range []string{"", "public;drop", "x.y", "UPPER", strings.Repeat("a", 64)} {
		if _, err := New(&sql.DB{}, schema, make([]byte, 32)); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("accepted unsafe schema")
		}
	}
	if _, err := New(nil, "accounts_test", make([]byte, 32)); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("accepted missing database")
	}
}

func TestCiphertextIsRandomizedAndAuthenticatedToPersonalPrincipal(t *testing.T) {
	s := testStore(t)
	p := principal()
	secret := "opaque-personal-credential"
	a, err := s.seal(p, secret)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.seal(p, secret)
	if err != nil || bytes.Equal(a, b) || bytes.Contains(a, []byte(secret)) {
		t.Fatal("credential encryption lacks nonce or confidentiality")
	}
	got, err := s.open(p, a)
	if err != nil || got != secret {
		t.Fatal("encrypted credential did not round trip")
	}
	p.Scope.ChatID, p.Scope.Visibility = "other-chat", "private"
	if got, err := s.open(p, a); err != nil || got != secret {
		t.Fatal("personal credential was incorrectly bound to a chat")
	}
	for _, change := range []func(*Principal){
		func(p *Principal) { p.ActorID = "other" },
		func(p *Principal) { p.Scope.TenantID = "other" },
		func(p *Principal) { p.Scope.AppID = "other" },
	} {
		other := principal()
		change(&other)
		if _, err := s.open(other, a); !errors.Is(err, ErrCorruptCredential) {
			t.Fatal("ciphertext accepted under wrong principal")
		}
	}
	for _, data := range [][]byte{nil, a[:5], append([]byte(nil), a...)} {
		if len(data) > 5 {
			data[len(data)-1] ^= 1
		}
		if _, err := s.open(principal(), data); !errors.Is(err, ErrCorruptCredential) {
			t.Fatal("tampered ciphertext accepted")
		}
	}
	otherKey, _ := New(&sql.DB{}, "accounts_test", bytes.Repeat([]byte{8}, 32))
	if _, err := otherKey.open(principal(), a); !errors.Is(err, ErrCorruptCredential) {
		t.Fatal("ciphertext accepted with wrong key")
	}
}

func TestKeyedDigestsBindPurposeAndSubmission(t *testing.T) {
	s := testStore(t)
	p := principal()
	secret := "opaque-personal-credential"
	d := s.tokenDigest(p, secret)
	if bytes.Equal(d, []byte(secret)) || len(d) != 32 {
		t.Fatal("token digest is not opaque")
	}
	other := p
	other.ActorID = "other"
	if bytes.Equal(d, s.tokenDigest(other, secret)) {
		t.Fatal("digest not bound to principal")
	}
	if bytes.Equal(s.submissionDigest(p, "binding", "message", secret, false), s.submissionDigest(p, "binding", "message", secret, true)) {
		t.Fatal("revoke bit not authenticated")
	}
	if bytes.Equal(d, s.submissionDigest(p, "binding", "message", secret, false)) {
		t.Fatal("digest purposes overlap")
	}
	otherKey, _ := New(&sql.DB{}, "accounts_test", bytes.Repeat([]byte{8}, 32))
	if bytes.Equal(d, otherKey.tokenDigest(p, secret)) {
		t.Fatal("token digest is not keyed")
	}
}

func TestInvalidInputIsRejectedBeforeDatabaseUseWithoutEchoingSecrets(t *testing.T) {
	s := testStore(t)
	p := principal()
	ctx := context.Background()
	for _, token := range []string{"", " leading-secret", "trailing-secret ", "secret\nvalue", "secret\rvalue", "secret\x00value", "secret\u200bvalue", strings.Repeat("s", 8193), string([]byte{0xff})} {
		for _, invoke := range []func() error{
			func() error { return s.Put(ctx, p, token) },
			func() error { return s.Invalidate(ctx, p, token) },
			func() error { return s.CompleteBinding(ctx, p, "opaque-binding", "sent-message", token, false) },
		} {
			err := invoke()
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatal("malformed credential was not rejected")
			}
			if token != "" && strings.Contains(err.Error(), token) {
				t.Fatal("credential echoed in error")
			}
		}
	}
	for _, change := range []func(*Principal){
		func(p *Principal) { p.ActorID = "" },
		func(p *Principal) { p.ActorID = " actor" },
		func(p *Principal) { p.ActorID = "actor\n" },
		func(p *Principal) { p.Scope.ChatID = "" },
		func(p *Principal) { p.Scope.AppID = "" },
	} {
		bad := p
		change(&bad)
		for _, invoke := range []func() error{
			func() error { _, err := s.Resolve(ctx, bad); return err },
			func() error { _, err := s.Status(ctx, bad); return err },
			func() error { return s.Put(ctx, bad, "opaque-secret") },
			func() error { return s.Invalidate(ctx, bad, "opaque-secret") },
			func() error { _, err := s.BeginBinding(ctx, bad, "request"); return err },
			func() error { return s.SetBindingMessage(ctx, bad, "opaque-binding", "sent-message") },
			func() error {
				return s.CompleteBinding(ctx, bad, "opaque-binding", "sent-message", "opaque-secret", false)
			},
		} {
			if err := invoke(); !errors.Is(err, domain.ErrInvalid) {
				t.Fatal("malformed principal accepted")
			}
		}
	}
	for _, key := range []string{"", " request", "request\n", strings.Repeat("r", 1025)} {
		if _, err := s.BeginBinding(ctx, p, key); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("invalid request key accepted")
		}
	}
	if err := s.SetBindingMessage(ctx, p, "opaque-binding", ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("empty sent message accepted")
	}
	if err := s.CompleteBinding(ctx, p, "", "sent-message", "opaque-secret", false); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("empty challenge accepted")
	}
	if err := s.CompleteBinding(ctx, p, "opaque-binding", "sent-message", "opaque-secret", true); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("revoke with credential accepted")
	}
	for _, err := range []error{ErrUnbound, ErrInvalidCredential, ErrCorruptCredential} {
		if !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatal("credential resolution error lacks unauthorized identity")
		}
	}
}

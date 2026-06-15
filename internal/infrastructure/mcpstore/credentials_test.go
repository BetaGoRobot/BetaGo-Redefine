package mcpstore

import (
	"errors"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"gorm.io/gorm"
)

func TestEncryptDecryptToken(t *testing.T) {
	codec, err := NewTokenCodec("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewTokenCodec error = %v", err)
	}
	encrypted, err := codec.Encrypt("token-secret")
	if err != nil {
		t.Fatalf("Encrypt error = %v", err)
	}
	if encrypted == "token-secret" {
		t.Fatalf("token stored in plaintext")
	}
	decrypted, err := codec.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt error = %v", err)
	}
	if decrypted != "token-secret" {
		t.Fatalf("decrypted = %q", decrypted)
	}
}

func TestNewTokenCodecRequires32ByteKey(t *testing.T) {
	if _, err := NewTokenCodec("short"); err == nil {
		t.Fatalf("NewTokenCodec short key error = nil")
	}
}

func TestCredentialRepositoryBuildsEncryptedRowAndDecryptsCredential(t *testing.T) {
	codec := newTestCodec(t)
	lookup := luckin.CredentialLookup{
		Provider:  luckin.ProviderLuckin,
		AppID:     "app",
		BotOpenID: "bot",
		Scope:     luckin.CredentialScope{Type: luckin.ScopeChat, ID: "chat"},
	}

	row, err := buildCredentialRow(codec, lookup, "token-two", "user-2", time.Unix(100, 0))
	if err != nil {
		t.Fatalf("buildCredentialRow error = %v", err)
	}
	if row.EncryptedToken == "token-two" {
		t.Fatalf("token stored in plaintext")
	}
	if row.TokenHint != "****-two" {
		t.Fatalf("TokenHint = %q", row.TokenHint)
	}
	if row.CreatedByOpenID != "user-2" {
		t.Fatalf("CreatedByOpenID = %q", row.CreatedByOpenID)
	}
	if row.UpdatedByOpenID != "user-2" {
		t.Fatalf("UpdatedByOpenID = %q", row.UpdatedByOpenID)
	}

	cred, err := credentialFromRow(codec, lookup, row)
	if err != nil {
		t.Fatalf("credentialFromRow error = %v", err)
	}
	if cred.Token != "token-two" {
		t.Fatalf("Token = %q", cred.Token)
	}
	if cred.TokenHint != "****-two" {
		t.Fatalf("TokenHint = %q", cred.TokenHint)
	}
}

func TestCredentialRepositoryUpsertConflictUpdatesExistingCredential(t *testing.T) {
	codec := newTestCodec(t)
	lookup := luckin.CredentialLookup{
		Provider:  luckin.ProviderLuckin,
		AppID:     "app",
		BotOpenID: "bot",
		Scope:     luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "user"},
	}

	row, err := buildCredentialRow(codec, lookup, "token-one", "user", time.Unix(100, 0))
	if err != nil {
		t.Fatalf("buildCredentialRow error = %v", err)
	}
	conflict := credentialUpsertConflict(row)
	gotColumns := make([]string, 0, len(conflict.Columns))
	for _, col := range conflict.Columns {
		gotColumns = append(gotColumns, col.Name)
	}
	wantColumns := []string{"provider", "app_id", "bot_open_id", "scope_type", "scope_id"}
	if len(gotColumns) != len(wantColumns) {
		t.Fatalf("conflict columns = %v", gotColumns)
	}
	for i, want := range wantColumns {
		if gotColumns[i] != want {
			t.Fatalf("conflict columns = %v", gotColumns)
		}
	}

	assignments := map[string]bool{}
	for _, assignment := range conflict.DoUpdates {
		assignments[assignment.Column.Name] = true
	}
	for _, want := range []string{"encrypted_token", "token_hint", "updated_by_open_id", "updated_at", "deleted_at"} {
		if !assignments[want] {
			t.Fatalf("upsert assignments missing %s: %#v", want, conflict.DoUpdates)
		}
	}
}

func TestCredentialRepositoryFindTokenMapsRecordNotFound(t *testing.T) {
	err := normalizeCredentialFindError(gorm.ErrRecordNotFound)
	if !errors.Is(err, luckin.ErrCredentialNotFound) {
		t.Fatalf("err = %v, want ErrCredentialNotFound", err)
	}
	if err := normalizeCredentialFindError(nil); err != nil {
		t.Fatalf("nil err normalized to %v", err)
	}
}

func newTestCodec(t *testing.T) TokenCodec {
	t.Helper()
	codec, err := NewTokenCodec("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewTokenCodec error = %v", err)
	}
	return codec
}

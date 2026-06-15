package mcpstore

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"gorm.io/driver/postgres"
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
		t.Fatalf("decrypted token mismatch: len=%d", len(decrypted))
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
		t.Fatalf("token mismatch: hint=%s len=%d", luckin.MaskToken(cred.Token), len(cred.Token))
	}
	if cred.TokenHint != "****-two" {
		t.Fatalf("TokenHint = %q", cred.TokenHint)
	}
}

func TestCredentialRepositoryIntegrationUpsertFindAndRevive(t *testing.T) {
	if os.Getenv("BETAGO_RUN_MCPSTORE_INTEGRATION") != "1" {
		t.Skip("set BETAGO_RUN_MCPSTORE_INTEGRATION=1 to run mcpstore repository integration test")
	}
	cfg, err := config.LoadFileE("../../../.dev/config.toml")
	if err != nil || cfg.DBConfig == nil {
		t.Skipf("database config is unavailable: %v", err)
	}
	db, err := gorm.Open(postgres.Open(cfg.DBConfig.DSN()), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	codec := newTestCodec(t)
	repo := NewCredentialRepository(db, codec)
	lookup := luckin.CredentialLookup{
		Provider:  luckin.ProviderLuckin,
		AppID:     "test-app",
		BotOpenID: "test-bot",
		Scope:     luckin.CredentialScope{Type: luckin.ScopePersonal, ID: "test-user"},
	}
	ctx := context.Background()
	cleanupCredential(t, db, lookup)
	t.Cleanup(func() { cleanupCredential(t, db, lookup) })

	if err := repo.UpsertToken(ctx, lookup, "token-one", "actor-1"); err != nil {
		t.Fatalf("UpsertToken first error = %v", err)
	}
	cred, err := repo.FindToken(ctx, lookup)
	if err != nil {
		t.Fatalf("FindToken first error = %v", err)
	}
	if cred.Token != "token-one" || cred.TokenHint != "****-one" {
		t.Fatalf("first credential mismatch: hint=%s len=%d", cred.TokenHint, len(cred.Token))
	}

	if err := repo.UpsertToken(ctx, lookup, "token-two", "actor-2"); err != nil {
		t.Fatalf("UpsertToken second error = %v", err)
	}
	cred, err = repo.FindToken(ctx, lookup)
	if err != nil {
		t.Fatalf("FindToken second error = %v", err)
	}
	if cred.Token != "token-two" || cred.TokenHint != "****-two" {
		t.Fatalf("second credential mismatch: hint=%s len=%d", cred.TokenHint, len(cred.Token))
	}

	q := query.Use(db)
	ins := q.McpCredential
	_, err = ins.WithContext(ctx).
		Where(ins.Provider.Eq(lookup.Provider)).
		Where(ins.AppID.Eq(lookup.AppID)).
		Where(ins.BotOpenID.Eq(lookup.BotOpenID)).
		Where(ins.ScopeType.Eq(string(lookup.Scope.Type))).
		Where(ins.ScopeID.Eq(lookup.Scope.ID)).
		Update(ins.DeletedAt, gorm.DeletedAt{Time: time.Now(), Valid: true})
	if err != nil {
		t.Fatalf("soft delete credential: %v", err)
	}
	if _, err := repo.FindToken(ctx, lookup); !errors.Is(err, luckin.ErrCredentialNotFound) {
		t.Fatalf("FindToken after soft delete err = %v, want ErrCredentialNotFound", err)
	}

	if err := repo.UpsertToken(ctx, lookup, "token-three", "actor-3"); err != nil {
		t.Fatalf("UpsertToken revive error = %v", err)
	}
	cred, err = repo.FindToken(ctx, lookup)
	if err != nil {
		t.Fatalf("FindToken revived error = %v", err)
	}
	if cred.Token != "token-three" || cred.TokenHint != "****hree" {
		t.Fatalf("revived credential mismatch: hint=%s len=%d", cred.TokenHint, len(cred.Token))
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

func cleanupCredential(t *testing.T, db *gorm.DB, lookup luckin.CredentialLookup) {
	t.Helper()
	err := db.Unscoped().
		Where("provider = ? AND app_id = ? AND bot_open_id = ? AND scope_type = ? AND scope_id = ?",
			lookup.Provider, lookup.AppID, lookup.BotOpenID, string(lookup.Scope.Type), lookup.Scope.ID).
		Delete(&model.McpCredential{}).Error
	if err != nil {
		t.Fatalf("cleanup credential: %v", err)
	}
}

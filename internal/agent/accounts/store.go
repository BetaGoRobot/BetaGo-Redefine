// Package accounts maintains service-owned, encrypted personal Luckin credentials.
// Callers must derive principals from authenticated transport identities. It has
// no static credential fallback and does not import legacy account data.
package accounts

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

const (
	Provider      = "luckin"
	StatusUnbound = "unbound"
	StatusBound   = "bound"
	StatusInvalid = "invalid"
	BindingTTL    = 15 * time.Minute
	maxTokenBytes = 8192
)

var (
	ErrUnbound           = fmt.Errorf("%w: personal credential is unbound", domain.ErrUnauthorized)
	ErrInvalidCredential = fmt.Errorf("%w: personal credential is invalid", domain.ErrUnauthorized)
	ErrCorruptCredential = fmt.Errorf("%w: personal credential authentication failed", domain.ErrUnauthorized)
	ErrBindingRejected   = errors.New("private binding challenge rejected")
	schemaName           = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
)

// Principal identifies an authenticated requester. Credentials follow the actor
// across chats in the same tenant/app; binding challenges retain the full scope.
type Principal struct {
	Scope   domain.Scope
	ActorID string
}

type Binding struct {
	ID        string
	ExpiresAt time.Time
	MessageID string
}

type Store struct {
	db        *sql.DB
	schema    string
	aead      cipher.AEAD
	digestKey []byte
}

// New requires a dedicated 32-byte deployment encryption key. The caller owns
// key provisioning and database lifecycle; neither is loaded from local files.
func New(db *sql.DB, schema string, key []byte) (*Store, error) {
	if db == nil || !schemaName.MatchString(schema) || len(key) != 32 {
		return nil, fmt.Errorf("%w: accounts database, schema, or encryption key", domain.ErrInvalid)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: accounts encryption key", domain.ErrInvalid)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("betago/accounts/digest-key/v1"))
	return &Store{db: db, schema: schema, aead: aead, digestKey: mac.Sum(nil)}, nil
}

func (s *Store) table(name string) string { return `"` + s.schema + `"."` + name + `"` }

func validID(v string) bool {
	return v != "" && len(v) <= 1024 && utf8.ValidString(v) && strings.TrimSpace(v) == v && !strings.ContainsFunc(v, unicode.IsControl)
}

func (p Principal) validate() error {
	if err := p.Scope.Validate(); err != nil {
		return err
	}
	if !validID(p.ActorID) {
		return fmt.Errorf("%w: account actor", domain.ErrInvalid)
	}
	return nil
}

func validateToken(token string) error {
	if token == "" || len(token) > maxTokenBytes || !utf8.ValidString(token) || strings.TrimSpace(token) != token || strings.ContainsFunc(token, func(r rune) bool { return !unicode.IsPrint(r) }) {
		return fmt.Errorf("%w: personal credential format", domain.ErrInvalid)
	}
	return nil
}

func credentialParts(p Principal) []string {
	return []string{p.Scope.TenantID, p.Scope.AppID, p.ActorID, Provider}
}

func encoded(v any) []byte { b, _ := json.Marshal(v); return b }

func credentialAAD(p Principal) []byte {
	return encoded(append([]string{"betago/accounts/aes256gcm/v1"}, credentialParts(p)...))
}

func (s *Store) seal(p Principal, token string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("accounts: encryption nonce unavailable")
	}
	return s.aead.Seal(nonce, nonce, []byte(token), credentialAAD(p)), nil
}

func (s *Store) open(p Principal, data []byte) (string, error) {
	n := s.aead.NonceSize()
	if len(data) < n+s.aead.Overhead() {
		return "", ErrCorruptCredential
	}
	plain, err := s.aead.Open(nil, data[:n], data[n:], credentialAAD(p))
	if err != nil {
		return "", ErrCorruptCredential
	}
	return string(plain), nil
}

func (s *Store) digest(purpose string, parts ...string) []byte {
	mac := hmac.New(sha256.New, s.digestKey)
	mac.Write(encoded(append([]string{purpose}, parts...)))
	return mac.Sum(nil)
}

func (s *Store) tokenDigest(p Principal, token string) []byte {
	return s.digest("credential/v1", append(credentialParts(p), token)...)
}

func (s *Store) submissionDigest(p Principal, id, messageID, token string, revoke bool) []byte {
	return s.digest("submission/v1", p.Scope.TenantID, p.Scope.AppID, p.Scope.ChatID, p.Scope.Visibility, p.ActorID, Provider, id, messageID, token, fmt.Sprint(revoke))
}

func (s *Store) transact(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

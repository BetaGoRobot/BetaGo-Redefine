package accounts

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
)

// Resolve returns only this actor's active personal credential. Invalid, absent,
// or unauthenticated ciphertext never falls back to another account.
func (s *Store) Resolve(ctx context.Context, p Principal) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	var ciphertext, digest []byte
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT ciphertext,token_digest,status FROM `+s.table("personal_credentials")+` WHERE tenant_id=$1 AND app_id=$2 AND actor_id=$3 AND provider=$4`, p.Scope.TenantID, p.Scope.AppID, p.ActorID, Provider).Scan(&ciphertext, &digest, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnbound
	}
	if err != nil {
		return "", err
	}
	if status != StatusBound {
		return "", ErrInvalidCredential
	}
	token, err := s.open(p, ciphertext)
	if err != nil {
		return "", err
	}
	if !hmac.Equal(digest, s.tokenDigest(p, token)) {
		return "", ErrCorruptCredential
	}
	return token, nil
}

// Status reads binding metadata only; it never decrypts or exposes a token.
func (s *Store) Status(ctx context.Context, p Principal) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM `+s.table("personal_credentials")+` WHERE tenant_id=$1 AND app_id=$2 AND actor_id=$3 AND provider=$4`, p.Scope.TenantID, p.Scope.AppID, p.ActorID, Provider).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return StatusUnbound, nil
	}
	return status, err
}

// Put stores a canonical opaque token encrypted with a fresh nonce. It performs
// no provider network call; the business adapter invalidates unauthorized tokens.
func (s *Store) Put(ctx context.Context, p Principal, token string) error {
	if err := p.validate(); err != nil {
		return err
	}
	if err := validateToken(token); err != nil {
		return err
	}
	ciphertext, err := s.seal(p, token)
	if err != nil {
		return err
	}
	return s.put(ctx, s.db, p, ciphertext, s.tokenDigest(p, token))
}

type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Store) put(ctx context.Context, q executor, p Principal, ciphertext, digest []byte) error {
	_, err := q.ExecContext(ctx, `INSERT INTO `+s.table("personal_credentials")+` (tenant_id,app_id,actor_id,provider,ciphertext,token_digest,status) VALUES ($1,$2,$3,$4,$5,$6,'bound') ON CONFLICT (tenant_id,app_id,actor_id,provider) DO UPDATE SET ciphertext=EXCLUDED.ciphertext,token_digest=EXCLUDED.token_digest,status='bound',updated_at=clock_timestamp()`, p.Scope.TenantID, p.Scope.AppID, p.ActorID, Provider, ciphertext, digest)
	return err
}

// Invalidate changes only the exact token that the failed upstream request used.
// A concurrent replacement is preserved, and a missing credential is a no-op.
func (s *Store) Invalidate(ctx context.Context, p Principal, token string) error {
	if err := p.validate(); err != nil {
		return err
	}
	if err := validateToken(token); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE `+s.table("personal_credentials")+` SET status='invalid',updated_at=clock_timestamp() WHERE tenant_id=$1 AND app_id=$2 AND actor_id=$3 AND provider=$4 AND token_digest=$5 AND status='bound'`, p.Scope.TenantID, p.Scope.AppID, p.ActorID, Provider, s.tokenDigest(p, token))
	return err
}

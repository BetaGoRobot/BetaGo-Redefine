package accounts

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

// BeginBinding issues an opaque, persisted challenge with a 15-minute lifetime.
// Retries of the same request key in the exact principal scope reuse the original
// challenge, including its deadline and sent message ID. Expired keys are rejected.
func (s *Store) BeginBinding(ctx context.Context, p Principal, requestKey string) (Binding, error) {
	return s.beginBinding(ctx, p, requestKey, false)
}

// EnsureBinding reserves at most one pending private form for the exact actor
// and chat, even when follow-ups have different task/request keys. A reservation
// without a confirmed message has an unknown delivery outcome and cannot be sent
// again. BeginBinding remains the explicit account-management operation.
func (s *Store) EnsureBinding(ctx context.Context, p Principal, requestKey string) (Binding, error) {
	return s.beginBinding(ctx, p, requestKey, true)
}

func (s *Store) beginBinding(ctx context.Context, p Principal, requestKey string, ensure bool) (Binding, error) {
	if err := p.validate(); err != nil {
		return Binding{}, err
	}
	if !validID(requestKey) {
		return Binding{}, fmt.Errorf("%w: binding request identity", domain.ErrInvalid)
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Binding{}, errors.New("accounts: binding randomness unavailable")
	}
	id := base64.RawURLEncoding.EncodeToString(random[:])
	requestDigest := s.digest("binding-request/v1", p.Scope.TenantID, p.Scope.AppID, p.Scope.ChatID, p.Scope.Visibility, p.ActorID, Provider, requestKey)
	var b Binding
	err := s.transact(ctx, func(tx *sql.Tx) error {
		// Serialize reservation across request keys before looking for an active
		// form. A row lock alone cannot protect the no-row-yet case.
		lock := "betago/accounts/binding/" + s.schema + "/" + base64.RawURLEncoding.EncodeToString(s.digest("binding-principal/v1", p.Scope.TenantID, p.Scope.AppID, p.Scope.ChatID, p.Scope.Visibility, p.ActorID, Provider))
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lock); err != nil {
			return err
		}
		if ensure {
			var consumed sql.NullTime
			// Retrying an expired or consumed key must never acquire a new
			// challenge, even when another task has since created a form.
			err := tx.QueryRowContext(ctx, `SELECT binding_id,expires_at,message_id,consumed_at FROM `+s.table("private_bindings")+` WHERE tenant_id=$1 AND app_id=$2 AND chat_id=$3 AND visibility=$4 AND actor_id=$5 AND provider=$6 AND request_digest=$7 FOR UPDATE`, p.Scope.TenantID, p.Scope.AppID, p.Scope.ChatID, p.Scope.Visibility, p.ActorID, Provider, requestDigest).Scan(&b.ID, &b.ExpiresAt, &b.MessageID, &consumed)
			if errors.Is(err, sql.ErrNoRows) {
				// Unknown reservations take priority over confirmed forms: no
				// task may send again while any delivery is still uncertain.
				err = tx.QueryRowContext(ctx, `SELECT binding_id,expires_at,message_id,consumed_at FROM `+s.table("private_bindings")+` WHERE tenant_id=$1 AND app_id=$2 AND chat_id=$3 AND visibility=$4 AND actor_id=$5 AND provider=$6 AND consumed_at IS NULL AND expires_at>clock_timestamp() ORDER BY (message_id='') DESC,expires_at DESC,binding_id DESC LIMIT 1 FOR UPDATE`, p.Scope.TenantID, p.Scope.AppID, p.Scope.ChatID, p.Scope.Visibility, p.ActorID, Provider).Scan(&b.ID, &b.ExpiresAt, &b.MessageID, &consumed)
			}
			if err == nil {
				if consumed.Valid {
					return ErrBindingRejected
				}
				if err := s.checkExpiry(ctx, tx, b.ID); err != nil {
					return err
				}
				if b.MessageID == "" {
					return domain.ErrEffectUnknown
				}
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+s.table("private_bindings")+` (binding_id,tenant_id,app_id,chat_id,visibility,actor_id,provider,request_digest,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp()+interval '15 minutes') ON CONFLICT (tenant_id,app_id,chat_id,visibility,actor_id,provider,request_digest) DO NOTHING`, id, p.Scope.TenantID, p.Scope.AppID, p.Scope.ChatID, p.Scope.Visibility, p.ActorID, Provider, requestDigest); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT binding_id,expires_at,message_id FROM `+s.table("private_bindings")+` WHERE tenant_id=$1 AND app_id=$2 AND chat_id=$3 AND visibility=$4 AND actor_id=$5 AND provider=$6 AND request_digest=$7 FOR UPDATE`, p.Scope.TenantID, p.Scope.AppID, p.Scope.ChatID, p.Scope.Visibility, p.ActorID, Provider, requestDigest).Scan(&b.ID, &b.ExpiresAt, &b.MessageID); err != nil {
			return err
		}
		return s.checkExpiry(ctx, tx, b.ID)
	})
	if err != nil {
		return Binding{}, err
	}
	return b, nil
}

type lockedBinding struct {
	Binding
	principal  Principal
	consumed   sql.NullTime
	submission []byte
}

func (s *Store) binding(ctx context.Context, tx *sql.Tx, p Principal, id string) (lockedBinding, error) {
	var b lockedBinding
	err := tx.QueryRowContext(ctx, `SELECT binding_id,tenant_id,app_id,chat_id,visibility,actor_id,message_id,expires_at,consumed_at,submission_digest FROM `+s.table("private_bindings")+` WHERE binding_id=$1 AND provider=$2 FOR UPDATE`, id, Provider).Scan(&b.ID, &b.principal.Scope.TenantID, &b.principal.Scope.AppID, &b.principal.Scope.ChatID, &b.principal.Scope.Visibility, &b.principal.ActorID, &b.MessageID, &b.ExpiresAt, &b.consumed, &b.submission)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedBinding{}, ErrBindingRejected
	}
	if err != nil {
		return lockedBinding{}, err
	}
	if b.principal != p {
		return lockedBinding{}, ErrBindingRejected
	}
	if err := s.checkExpiry(ctx, tx, id); err != nil {
		return lockedBinding{}, err
	}
	return b, nil
}

// Check the database clock after taking the row lock: a callback blocked on a
// concurrent transaction must not inherit an earlier transaction-start deadline.
func (s *Store) checkExpiry(ctx context.Context, tx *sql.Tx, id string) error {
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT expires_at>clock_timestamp() FROM `+s.table("private_bindings")+` WHERE binding_id=$1`, id).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrBindingRejected
	}
	return nil
}

// SetBindingMessage must receive the actual message ID returned by the private
// ephemeral-card send. The ID is immutable; it cannot be supplied by a tool/model.
func (s *Store) SetBindingMessage(ctx context.Context, p Principal, id, messageID string) error {
	if err := p.validate(); err != nil {
		return err
	}
	if !validID(id) || !validID(messageID) {
		return fmt.Errorf("%w: binding or message identity", domain.ErrInvalid)
	}
	return s.transact(ctx, func(tx *sql.Tx) error {
		b, err := s.binding(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if b.consumed.Valid || (b.MessageID != "" && b.MessageID != messageID) {
			return ErrBindingRejected
		}
		_, err = tx.ExecContext(ctx, `UPDATE `+s.table("private_bindings")+` SET message_id=$1 WHERE binding_id=$2`, messageID, id)
		return err
	})
}

// CompleteBinding is the only form submission operation. The transport must use
// authenticated actor/scope and message context, not values from the form. It
// atomically consumes the challenge and binds or revokes without provider I/O.
// Only byte-identical retries of an already consumed submission are successful;
// they never write the credential again, even if it has since been replaced.
func (s *Store) CompleteBinding(ctx context.Context, p Principal, id, messageID, token string, revoke bool) error {
	if err := p.validate(); err != nil {
		return err
	}
	if !validID(id) || !validID(messageID) {
		return fmt.Errorf("%w: binding or message identity", domain.ErrInvalid)
	}
	if revoke {
		if token != "" {
			return fmt.Errorf("%w: revoke credential must be empty", domain.ErrInvalid)
		}
	} else if err := validateToken(token); err != nil {
		return err
	}
	digest := s.submissionDigest(p, id, messageID, token, revoke)
	return s.transact(ctx, func(tx *sql.Tx) error {
		b, err := s.binding(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if b.MessageID == "" || b.MessageID != messageID {
			return ErrBindingRejected
		}
		if b.consumed.Valid {
			if hmac.Equal(b.submission, digest) {
				return nil
			}
			return ErrBindingRejected
		}
		if revoke {
			_, err = tx.ExecContext(ctx, `DELETE FROM `+s.table("personal_credentials")+` WHERE tenant_id=$1 AND app_id=$2 AND actor_id=$3 AND provider=$4`, p.Scope.TenantID, p.Scope.AppID, p.ActorID, Provider)
		} else {
			var ciphertext []byte
			ciphertext, err = s.seal(p, token)
			if err == nil {
				err = s.put(ctx, tx, p, ciphertext, s.tokenDigest(p, token))
			}
		}
		if err != nil {
			return err
		}
		// Recheck after the credential row lock, which may have blocked on a
		// different binding or a concurrent invalidation/replacement.
		result, err := tx.ExecContext(ctx, `UPDATE `+s.table("private_bindings")+` SET consumed_at=clock_timestamp(),submission_digest=$1 WHERE binding_id=$2 AND expires_at>clock_timestamp()`, digest, id)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrBindingRejected
		}
		return nil
	})
}

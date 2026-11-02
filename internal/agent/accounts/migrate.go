package accounts

import (
	"context"
	"database/sql"
)

// Migrate only creates the account service's own schema objects. It does not
// inspect, import, or modify legacy account tables or operator files.
func (s *Store) Migrate(ctx context.Context) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "betago/accounts/migrate/"+s.schema); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS "`+s.schema+`";
CREATE TABLE IF NOT EXISTS `+s.table("personal_credentials")+` (
 tenant_id text NOT NULL,
 app_id text NOT NULL,
 actor_id text NOT NULL,
 provider text NOT NULL CHECK (provider='luckin'),
 ciphertext bytea NOT NULL CHECK (octet_length(ciphertext)>=28),
 token_digest bytea NOT NULL CHECK (octet_length(token_digest)=32),
 status text NOT NULL CHECK (status IN ('bound','invalid')),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,app_id,actor_id,provider)
);
CREATE TABLE IF NOT EXISTS `+s.table("private_bindings")+` (
 binding_id text PRIMARY KEY,
 tenant_id text NOT NULL,
 app_id text NOT NULL,
 chat_id text NOT NULL,
 visibility text NOT NULL,
 actor_id text NOT NULL,
 provider text NOT NULL CHECK (provider='luckin'),
 request_digest bytea NOT NULL CHECK (octet_length(request_digest)=32),
 message_id text NOT NULL DEFAULT '',
 expires_at timestamptz NOT NULL,
 consumed_at timestamptz,
 submission_digest bytea CHECK (octet_length(submission_digest)=32),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id,app_id,chat_id,visibility,actor_id,provider,request_digest),
 CHECK ((consumed_at IS NULL)=(submission_digest IS NULL))
);`)
		return err
	})
}

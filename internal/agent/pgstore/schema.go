package pgstore

import (
	"context"
	"database/sql"
)

// Migrate only creates tables in this Store's validated private namespace.
func (s *Store) Migrate(ctx context.Context) error {
	return s.transact(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "group-agent-migrate:"+s.schema); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS "`+s.schema+`"`); err != nil {
			return err
		}
		statements := []string{
			`CREATE TABLE IF NOT EXISTS ` + s.table("events") + ` (
			 scope_key text NOT NULL,event_id text NOT NULL,payload_digest text NOT NULL,
			 body bytea NOT NULL,raw_content bytea NOT NULL,occurred_at timestamptz NOT NULL,
			 received_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(scope_key,event_id))`,
			`CREATE TABLE IF NOT EXISTS ` + s.table("messages") + ` (
			 scope_key text NOT NULL,message_id text NOT NULL,version bigint NOT NULL CHECK(version>0),
			 deleted boolean NOT NULL,occurred_at timestamptz,updated_at timestamptz NOT NULL,
			 occurred_ns numeric(30,0),updated_ns numeric(30,0) NOT NULL,
			 body bytea NOT NULL,raw_content bytea NOT NULL,PRIMARY KEY(scope_key,message_id))`,
			`CREATE INDEX IF NOT EXISTS messages_recent_idx ON ` + s.table("messages") + ` (scope_key,occurred_ns DESC,message_id) WHERE NOT deleted`,
			`CREATE TABLE IF NOT EXISTS ` + s.table("message_versions") + ` (
			 scope_key text NOT NULL,message_id text NOT NULL,version bigint NOT NULL CHECK(version>0),
			 event_id text NOT NULL,payload_digest text NOT NULL,body bytea NOT NULL,raw_content bytea NOT NULL,
			 received_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(scope_key,message_id,version),
			 FOREIGN KEY(scope_key,event_id) REFERENCES ` + s.table("events") + `(scope_key,event_id))`,
			`CREATE TABLE IF NOT EXISTS ` + s.table("jobs") + ` (
			 id bigserial PRIMARY KEY,scope_key text NOT NULL,scope_body bytea NOT NULL,source_body bytea NOT NULL,
			 kind text NOT NULL,payload bytea NOT NULL,dedupe_key text NOT NULL,priority integer NOT NULL,
			 max_attempts integer NOT NULL CHECK(max_attempts>0),attempt integer NOT NULL DEFAULT 0,
			 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','running','completed','failed')),
			 owner text NOT NULL DEFAULT '',fence bigint NOT NULL DEFAULT 0,
			 available_at timestamptz NOT NULL,expires_at timestamptz NOT NULL,lease_until timestamptz,
			 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
			 last_error text NOT NULL DEFAULT '',UNIQUE(scope_key,kind,dedupe_key))`,
			`CREATE INDEX IF NOT EXISTS jobs_claim_idx ON ` + s.table("jobs") + ` (status,available_at,priority DESC,id) WHERE status IN ('pending','running')`,
			`CREATE TABLE IF NOT EXISTS ` + s.table("contexts") + ` (
			 scope_key text NOT NULL,context_id text NOT NULL,version bigint NOT NULL CHECK(version>0),
			 body bytea NOT NULL,payload_digest text NOT NULL,invalidated boolean NOT NULL DEFAULT false,
			 invalidated_at timestamptz,PRIMARY KEY(scope_key,context_id))`,
			`CREATE TABLE IF NOT EXISTS ` + s.table("context_sources") + ` (
			 scope_key text NOT NULL,context_id text NOT NULL,message_id text NOT NULL,source_version bigint NOT NULL,
			 source_body bytea NOT NULL,PRIMARY KEY(scope_key,context_id,message_id,source_version),
			 FOREIGN KEY(scope_key,context_id) REFERENCES ` + s.table("contexts") + `(scope_key,context_id))`,
			`CREATE INDEX IF NOT EXISTS context_sources_reverse_idx ON ` + s.table("context_sources") + ` (scope_key,message_id)`,
		}
		for _, q := range statements {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		if err := s.migratePipeline(ctx, tx); err != nil {
			return err
		}
		if err := s.migrateWork(ctx, tx); err != nil {
			return err
		}
		if err := s.MigrateCatalog(ctx, tx); err != nil {
			return err
		}
		if err := s.MigrateSchedules(ctx, tx); err != nil {
			return err
		}
		return s.migrateRuntime(ctx, tx)
	})
}

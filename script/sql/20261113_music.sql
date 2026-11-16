SELECT pg_advisory_xact_lock(hashtext('betago-music-schema-v1'));
CREATE TABLE IF NOT EXISTS music_users (
 id text PRIMARY KEY, kind text NOT NULL, app_id text NOT NULL, tenant_key text NOT NULL,
 subject text NOT NULL, recovery_hash text UNIQUE, created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(app_id, tenant_key, subject)
);
CREATE TABLE IF NOT EXISTS music_sessions (
 hash text PRIMARY KEY, user_id text NOT NULL REFERENCES music_users(id), expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS music_sessions_expiry ON music_sessions(expires_at);
CREATE TABLE IF NOT EXISTS music_tracks (
 id text PRIMARY KEY, source text NOT NULL, external_id text NOT NULL, title text NOT NULL,
 artist text NOT NULL DEFAULT '', album text NOT NULL DEFAULT '', picture text NOT NULL DEFAULT '',
 duration_ms bigint NOT NULL DEFAULT 0 CHECK(duration_ms >= 0), UNIQUE(source, external_id)
);
CREATE TABLE IF NOT EXISTS music_playlists (
 id text PRIMARY KEY, owner_id text NOT NULL REFERENCES music_users(id), name text NOT NULL,
 revision bigint NOT NULL DEFAULT 1, deleted boolean NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS music_playlists_owner ON music_playlists(owner_id);
CREATE TABLE IF NOT EXISTS music_entries (
 playlist_id text NOT NULL REFERENCES music_playlists(id), track_id text NOT NULL REFERENCES music_tracks(id),
 position int NOT NULL, PRIMARY KEY(playlist_id, track_id), UNIQUE(playlist_id, position)
);
CREATE TABLE IF NOT EXISTS music_likes (
 owner_id text NOT NULL REFERENCES music_users(id), track_id text NOT NULL REFERENCES music_tracks(id),
 PRIMARY KEY(owner_id,track_id)
);
CREATE TABLE IF NOT EXISTS music_history (
 owner_id text NOT NULL REFERENCES music_users(id), track_id text NOT NULL REFERENCES music_tracks(id),
 position_ms bigint NOT NULL DEFAULT 0, played_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(owner_id,track_id)
);
CREATE TABLE IF NOT EXISTS music_queues (
 owner_id text NOT NULL REFERENCES music_users(id), device_id text NOT NULL, revision bigint NOT NULL DEFAULT 0,
 track_ids jsonb NOT NULL DEFAULT '[]', current_index int NOT NULL DEFAULT 0, position_ms bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(owner_id,device_id)
);
CREATE TABLE IF NOT EXISTS music_shares (
 id text PRIMARY KEY, owner_id text NOT NULL REFERENCES music_users(id), hash text NOT NULL UNIQUE,
 kind text NOT NULL CHECK(kind IN ('track','playlist')), target_id text NOT NULL,
 revoked boolean NOT NULL DEFAULT false, expires_at timestamptz NOT NULL
);

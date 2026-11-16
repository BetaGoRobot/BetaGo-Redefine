SELECT pg_advisory_xact_lock(hashtext('betago-music-schema-v1'));
CREATE TABLE IF NOT EXISTS music_search_history (
 owner_id text NOT NULL REFERENCES music_users(id),
 source text NOT NULL CHECK(source IN ('netease','kuwo')),
 query_key text NOT NULL,
 query text NOT NULL CHECK(length(query) BETWEEN 1 AND 200),
 count bigint NOT NULL DEFAULT 1 CHECK(count > 0),
 last_searched_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(owner_id,source,query_key)
);
CREATE INDEX IF NOT EXISTS music_search_history_owner_recent
 ON music_search_history(owner_id,last_searched_at DESC);

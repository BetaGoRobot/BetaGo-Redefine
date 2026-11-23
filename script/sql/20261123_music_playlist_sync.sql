-- Ordinary playlists keep their existing rows and entries.
ALTER TABLE music_playlists
  ADD COLUMN IF NOT EXISTS sync_source_id text,
  ADD COLUMN IF NOT EXISTS sync_track_ids jsonb,
  ADD COLUMN IF NOT EXISTS sync_last_success_at timestamptz,
  ADD COLUMN IF NOT EXISTS sync_last_attempt_at timestamptz,
  ADD COLUMN IF NOT EXISTS sync_last_error text,
  ADD COLUMN IF NOT EXISTS sync_next_auto_at timestamptz,
  ADD COLUMN IF NOT EXISTS sync_lease_token text,
  ADD COLUMN IF NOT EXISTS sync_lease_until timestamptz;

CREATE UNIQUE INDEX IF NOT EXISTS music_playlists_live_sync_source
  ON music_playlists(owner_id, sync_source_id) WHERE NOT deleted AND sync_source_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS music_playlists_sync_due
  ON music_playlists(sync_next_auto_at, id) WHERE NOT deleted AND sync_source_id IS NOT NULL;

SELECT pg_advisory_xact_lock(hashtext('betago-music-schema-v1'));
-- Old rows may originate in client JSON. Only server provider results may be shared as canonical metadata.
ALTER TABLE music_tracks ADD COLUMN IF NOT EXISTS canonical_verified boolean NOT NULL DEFAULT false;

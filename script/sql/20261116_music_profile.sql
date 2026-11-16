SELECT pg_advisory_xact_lock(hashtext('betago-music-schema-v1'));
-- Basic display profile is separate from stable account identity and sessions.
-- NULL means an existing Feishu account has not attempted a verified profile read.
ALTER TABLE music_users ADD COLUMN IF NOT EXISTS display_name text NOT NULL DEFAULT '';
ALTER TABLE music_users ADD COLUMN IF NOT EXISTS avatar_url text NOT NULL DEFAULT '';
ALTER TABLE music_users ADD COLUMN IF NOT EXISTS profile_updated_at timestamptz;

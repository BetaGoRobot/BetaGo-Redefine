-- 为 private_modes 增加 bot 维度隔离字段

ALTER TABLE IF EXISTS betago.private_modes
  ADD COLUMN IF NOT EXISTS app_id TEXT NOT NULL DEFAULT '';

ALTER TABLE IF EXISTS betago.private_modes
  ADD COLUMN IF NOT EXISTS bot_open_id TEXT NOT NULL DEFAULT '';

ALTER TABLE IF EXISTS betago.private_modes
  DROP CONSTRAINT IF EXISTS private_modes_pkey;

ALTER TABLE IF EXISTS betago.private_modes
  ADD CONSTRAINT private_modes_pkey PRIMARY KEY (chat_id, app_id, bot_open_id);

COMMENT ON COLUMN betago.private_modes.app_id IS '隐私模式所属的飞书应用 AppID';
COMMENT ON COLUMN betago.private_modes.bot_open_id IS '隐私模式所属的机器人 OpenID';

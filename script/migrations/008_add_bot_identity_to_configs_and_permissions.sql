-- 为配置相关表增加 bot 维度隔离字段

ALTER TABLE IF EXISTS betago.permission_grants
  ADD COLUMN IF NOT EXISTS app_id TEXT NOT NULL DEFAULT '';

ALTER TABLE IF EXISTS betago.permission_grants
  ADD COLUMN IF NOT EXISTS bot_open_id TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS betago.idx_permission_grants_subject_point_scope_resource_active;

CREATE UNIQUE INDEX IF NOT EXISTS idx_permission_grants_subject_point_scope_resource_bot_active
  ON betago.permission_grants(
    subject_type,
    subject_id,
    permission_point,
    scope,
    app_id,
    bot_open_id,
    COALESCE(resource_chat_id, ''),
    COALESCE(resource_user_id, '')
  )
  WHERE deleted_at IS NULL;

COMMENT ON COLUMN betago.permission_grants.app_id IS '授权所属的飞书应用 AppID';
COMMENT ON COLUMN betago.permission_grants.bot_open_id IS '授权所属的机器人 OpenID';

ALTER TABLE IF EXISTS betago.function_enablings
  ADD COLUMN IF NOT EXISTS app_id TEXT NOT NULL DEFAULT '';

ALTER TABLE IF EXISTS betago.function_enablings
  ADD COLUMN IF NOT EXISTS bot_open_id TEXT NOT NULL DEFAULT '';

ALTER TABLE IF EXISTS betago.function_enablings
  DROP CONSTRAINT IF EXISTS function_enablings_pkey;

ALTER TABLE IF EXISTS betago.function_enablings
  ADD CONSTRAINT function_enablings_pkey PRIMARY KEY (guild_id, function, app_id, bot_open_id);

COMMENT ON COLUMN betago.function_enablings.app_id IS '功能开关所属的飞书应用 AppID';
COMMENT ON COLUMN betago.function_enablings.bot_open_id IS '功能开关所属的机器人 OpenID';

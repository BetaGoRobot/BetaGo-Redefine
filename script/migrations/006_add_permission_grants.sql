-- 权限点授权表
-- 当前首个落点：global scope 配置写入需要 config.write@global

CREATE TABLE IF NOT EXISTS betago.permission_grants (
    id BIGSERIAL PRIMARY KEY,
    subject_type VARCHAR(32) NOT NULL,
    subject_id VARCHAR(128) NOT NULL,
    permission_point VARCHAR(128) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    resource_chat_id VARCHAR(128),
    resource_user_id VARCHAR(128),
    remark TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_permission_grants_subject_point_scope_resource_active
    ON betago.permission_grants(
        subject_type,
        subject_id,
        permission_point,
        scope,
        COALESCE(resource_chat_id, ''),
        COALESCE(resource_user_id, '')
    )
    WHERE deleted_at IS NULL;

COMMENT ON TABLE betago.permission_grants IS '权限点授权表';
COMMENT ON COLUMN betago.permission_grants.subject_type IS '授权主体类型，当前固定为 user';
COMMENT ON COLUMN betago.permission_grants.subject_id IS '授权主体标识，飞书侧优先使用 OpenID';
COMMENT ON COLUMN betago.permission_grants.permission_point IS '权限点，例如 config.write';
COMMENT ON COLUMN betago.permission_grants.scope IS '授权作用域，例如 global/chat/user';
COMMENT ON COLUMN betago.permission_grants.resource_chat_id IS '作用域资源 chat_id，global scope 为空';
COMMENT ON COLUMN betago.permission_grants.resource_user_id IS '作用域资源 user_id，global scope 为空';
COMMENT ON COLUMN betago.permission_grants.remark IS '授权备注';

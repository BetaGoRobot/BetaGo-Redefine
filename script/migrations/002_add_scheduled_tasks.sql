-- 统一 schedule 系统数据库迁移
-- 创建时间: 2026

CREATE TABLE IF NOT EXISTS betago.scheduled_tasks (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(256) NOT NULL,
    type VARCHAR(32) NOT NULL,
    chat_id VARCHAR(128) NOT NULL,
    creator_id VARCHAR(128) NOT NULL,
    tool_name VARCHAR(128) NOT NULL,
    tool_args TEXT NOT NULL DEFAULT '{}',
    run_at TIMESTAMPTZ,
    cron_expr VARCHAR(128),
    timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai',
    status VARCHAR(32) NOT NULL DEFAULT 'enabled',
    notify_on_error BOOLEAN NOT NULL DEFAULT FALSE,
    notify_result BOOLEAN NOT NULL DEFAULT FALSE,
    last_run_at TIMESTAMPTZ,
    next_run_at TIMESTAMPTZ NOT NULL,
    last_error TEXT,
    last_result TEXT,
    run_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_chat ON betago.scheduled_tasks(chat_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_type ON betago.scheduled_tasks(type);
CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_status ON betago.scheduled_tasks(status);
CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_next_run ON betago.scheduled_tasks(next_run_at);
CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_creator ON betago.scheduled_tasks(creator_id);

COMMENT ON TABLE betago.scheduled_tasks IS '统一 schedule 表';
COMMENT ON COLUMN betago.scheduled_tasks.type IS '调度类型: once, cron';
COMMENT ON COLUMN betago.scheduled_tasks.tool_name IS '要执行的工具名称';
COMMENT ON COLUMN betago.scheduled_tasks.tool_args IS '工具参数 JSON';
COMMENT ON COLUMN betago.scheduled_tasks.run_at IS '单次 schedule 的执行时间';
COMMENT ON COLUMN betago.scheduled_tasks.cron_expr IS 'cron schedule 的标准 5 段表达式';
COMMENT ON COLUMN betago.scheduled_tasks.status IS '任务状态: enabled, paused, completed, disabled';

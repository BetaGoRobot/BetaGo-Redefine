-- 待办事项和提醒系统数据库迁移
-- 创建时间: 2026

-- 待办事项表
CREATE TABLE IF NOT EXISTS betago.todo_items (
    id VARCHAR(64) PRIMARY KEY,
    chat_id VARCHAR(128) NOT NULL,
    creator_id VARCHAR(128) NOT NULL,
    creator_name VARCHAR(256),
    assignee_id VARCHAR(128),
    title VARCHAR(512) NOT NULL,
    description TEXT,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    priority VARCHAR(32) NOT NULL DEFAULT 'medium',
    due_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    tags TEXT[],
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 创建索引
CREATE INDEX IF NOT EXISTS idx_todo_chat ON betago.todo_items(chat_id);
CREATE INDEX IF NOT EXISTS idx_todo_status ON betago.todo_items(status);
CREATE INDEX IF NOT EXISTS idx_todo_due ON betago.todo_items(due_at);
CREATE INDEX IF NOT EXISTS idx_todo_creator ON betago.todo_items(creator_id);

-- 提醒表
CREATE TABLE IF NOT EXISTS betago.todo_reminders (
    id VARCHAR(64) PRIMARY KEY,
    todo_id VARCHAR(64),
    chat_id VARCHAR(128) NOT NULL,
    creator_id VARCHAR(128) NOT NULL,
    title VARCHAR(512) NOT NULL,
    content TEXT,
    type VARCHAR(32) NOT NULL DEFAULT 'once',
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    trigger_at TIMESTAMPTZ NOT NULL,
    repeat_rule VARCHAR(256),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 创建索引
CREATE INDEX IF NOT EXISTS idx_reminder_todo ON betago.todo_reminders(todo_id);
CREATE INDEX IF NOT EXISTS idx_reminder_chat ON betago.todo_reminders(chat_id);
CREATE INDEX IF NOT EXISTS idx_reminder_status ON betago.todo_reminders(status);
CREATE INDEX IF NOT EXISTS idx_reminder_trigger ON betago.todo_reminders(trigger_at);
CREATE INDEX IF NOT EXISTS idx_reminder_creator ON betago.todo_reminders(creator_id);

-- 注释
COMMENT ON TABLE betago.todo_items IS '待办事项表';
COMMENT ON TABLE betago.todo_reminders IS '提醒表';
COMMENT ON COLUMN betago.todo_items.status IS '状态: pending, doing, done, cancelled';
COMMENT ON COLUMN betago.todo_items.priority IS '优先级: low, medium, high, urgent';
COMMENT ON COLUMN betago.todo_reminders.type IS '提醒类型: once, daily, weekly, monthly, custom';
COMMENT ON COLUMN betago.todo_reminders.status IS '提醒状态: pending, triggered, cancelled';

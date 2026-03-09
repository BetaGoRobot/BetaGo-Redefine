-- 兼容旧版 scheduled_tasks 表，补齐统一 schedule 所需字段
-- 创建时间: 2026

ALTER TABLE IF EXISTS betago.scheduled_tasks
    ADD COLUMN IF NOT EXISTS name VARCHAR(256),
    ADD COLUMN IF NOT EXISTS type VARCHAR(32),
    ADD COLUMN IF NOT EXISTS run_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS cron_expr VARCHAR(128),
    ADD COLUMN IF NOT EXISTS timezone VARCHAR(64),
    ADD COLUMN IF NOT EXISTS notify_on_error BOOLEAN,
    ADD COLUMN IF NOT EXISTS notify_result BOOLEAN,
    ADD COLUMN IF NOT EXISTS last_error TEXT,
    ADD COLUMN IF NOT EXISTS last_result TEXT,
    ADD COLUMN IF NOT EXISTS run_count BIGINT;

UPDATE betago.scheduled_tasks
SET
    name = COALESCE(NULLIF(name, ''), tool_name, id),
    type = COALESCE(NULLIF(type, ''), CASE
        WHEN COALESCE(NULLIF(cron_expr, ''), '') <> '' THEN 'cron'
        ELSE 'once'
    END),
    timezone = COALESCE(NULLIF(timezone, ''), 'Asia/Shanghai'),
    notify_on_error = COALESCE(notify_on_error, FALSE),
    notify_result = COALESCE(notify_result, FALSE),
    run_count = COALESCE(run_count, 0)
WHERE EXISTS (
    SELECT 1
    FROM information_schema.tables
    WHERE table_schema = 'betago'
      AND table_name = 'scheduled_tasks'
);

ALTER TABLE IF EXISTS betago.scheduled_tasks
    ALTER COLUMN name SET NOT NULL,
    ALTER COLUMN type SET NOT NULL,
    ALTER COLUMN timezone SET DEFAULT 'Asia/Shanghai',
    ALTER COLUMN timezone SET NOT NULL,
    ALTER COLUMN notify_on_error SET DEFAULT FALSE,
    ALTER COLUMN notify_on_error SET NOT NULL,
    ALTER COLUMN notify_result SET DEFAULT FALSE,
    ALTER COLUMN notify_result SET NOT NULL,
    ALTER COLUMN run_count SET DEFAULT 0,
    ALTER COLUMN run_count SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_chat ON betago.scheduled_tasks(chat_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_type ON betago.scheduled_tasks(type);
CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_status ON betago.scheduled_tasks(status);
CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_next_run ON betago.scheduled_tasks(next_run_at);
CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_creator ON betago.scheduled_tasks(creator_id);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.tables
        WHERE table_schema = 'betago'
          AND table_name = 'todo_reminders'
    ) THEN
        INSERT INTO betago.scheduled_tasks (
            id,
            name,
            type,
            chat_id,
            creator_id,
            tool_name,
            tool_args,
            run_at,
            cron_expr,
            timezone,
            status,
            notify_on_error,
            notify_result,
            next_run_at,
            created_at,
            updated_at
        )
        SELECT
            r.id,
            COALESCE(NULLIF(r.title, ''), 'legacy reminder'),
            CASE
                WHEN r.type = 'once' THEN 'once'
                WHEN COALESCE(NULLIF(r.repeat_rule, ''), '') <> '' THEN 'cron'
                WHEN r.type IN ('daily', 'weekly', 'monthly') THEN 'cron'
                ELSE 'once'
            END,
            r.chat_id,
            r.creator_id,
            'send_message',
            json_build_object('content', COALESCE(NULLIF(r.content, ''), NULLIF(r.title, ''), 'legacy reminder'))::text,
            CASE
                WHEN r.type = 'once' OR (
                    COALESCE(NULLIF(r.repeat_rule, ''), '') = '' AND r.type NOT IN ('daily', 'weekly', 'monthly')
                ) THEN r.trigger_at
                ELSE NULL
            END,
            CASE
                WHEN COALESCE(NULLIF(r.repeat_rule, ''), '') <> '' THEN r.repeat_rule
                WHEN r.type = 'daily' THEN
                    EXTRACT(MINUTE FROM r.trigger_at AT TIME ZONE 'Asia/Shanghai')::int::text || ' ' ||
                    EXTRACT(HOUR FROM r.trigger_at AT TIME ZONE 'Asia/Shanghai')::int::text || ' * * *'
                WHEN r.type = 'weekly' THEN
                    EXTRACT(MINUTE FROM r.trigger_at AT TIME ZONE 'Asia/Shanghai')::int::text || ' ' ||
                    EXTRACT(HOUR FROM r.trigger_at AT TIME ZONE 'Asia/Shanghai')::int::text || ' * * ' ||
                    EXTRACT(DOW FROM r.trigger_at AT TIME ZONE 'Asia/Shanghai')::int::text
                WHEN r.type = 'monthly' THEN
                    EXTRACT(MINUTE FROM r.trigger_at AT TIME ZONE 'Asia/Shanghai')::int::text || ' ' ||
                    EXTRACT(HOUR FROM r.trigger_at AT TIME ZONE 'Asia/Shanghai')::int::text || ' ' ||
                    EXTRACT(DAY FROM r.trigger_at AT TIME ZONE 'Asia/Shanghai')::int::text || ' * *'
                ELSE NULL
            END,
            'Asia/Shanghai',
            'enabled',
            FALSE,
            FALSE,
            GREATEST(r.trigger_at, NOW()),
            r.created_at,
            r.updated_at
        FROM betago.todo_reminders r
        WHERE r.status = 'pending'
          AND NOT EXISTS (
              SELECT 1
              FROM betago.scheduled_tasks st
              WHERE st.id = r.id
          );
    END IF;
END $$;

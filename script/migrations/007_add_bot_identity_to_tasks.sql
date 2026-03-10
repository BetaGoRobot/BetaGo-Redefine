-- 为 todo 和 schedule 增加 bot 维度隔离字段

ALTER TABLE IF EXISTS betago.todo_items
  ADD COLUMN IF NOT EXISTS app_id TEXT NOT NULL DEFAULT '';

ALTER TABLE IF EXISTS betago.todo_items
  ADD COLUMN IF NOT EXISTS bot_open_id TEXT NOT NULL DEFAULT '';

ALTER TABLE IF EXISTS betago.scheduled_tasks
  ADD COLUMN IF NOT EXISTS app_id TEXT NOT NULL DEFAULT '';

ALTER TABLE IF EXISTS betago.scheduled_tasks
  ADD COLUMN IF NOT EXISTS bot_open_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_todo_items_bot_chat
  ON betago.todo_items(app_id, bot_open_id, chat_id);

CREATE INDEX IF NOT EXISTS idx_todo_items_bot_creator
  ON betago.todo_items(app_id, bot_open_id, creator_id);

CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_bot_chat
  ON betago.scheduled_tasks(app_id, bot_open_id, chat_id);

CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_bot_due
  ON betago.scheduled_tasks(app_id, bot_open_id, status, next_run_at);

COMMENT ON COLUMN betago.todo_items.app_id IS '创建该待办的飞书应用 AppID';
COMMENT ON COLUMN betago.todo_items.bot_open_id IS '创建该待办的机器人 OpenID';
COMMENT ON COLUMN betago.scheduled_tasks.app_id IS '创建该调度任务的飞书应用 AppID';
COMMENT ON COLUMN betago.scheduled_tasks.bot_open_id IS '创建该调度任务的机器人 OpenID';

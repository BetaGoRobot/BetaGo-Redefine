-- 为 scheduled_tasks 增加来源消息 ID，用于执行时回复原消息

ALTER TABLE IF EXISTS betago.scheduled_tasks
  ADD COLUMN IF NOT EXISTS source_message_id TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN betago.scheduled_tasks.source_message_id IS '创建/触发该任务的来源消息 ID';

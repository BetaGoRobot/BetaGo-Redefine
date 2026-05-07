-- 添加工作日配置字段到 scheduled_tasks 表
-- 创建时间: 2026-05-07

ALTER TABLE betago.scheduled_tasks
    ADD COLUMN IF NOT EXISTS skip_weekends BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS skip_holidays BOOLEAN NOT NULL DEFAULT FALSE;

-- 添加注释
COMMENT ON COLUMN betago.scheduled_tasks.skip_weekends IS '是否跳过周末（周六日）';
COMMENT ON COLUMN betago.scheduled_tasks.skip_holidays IS '是否跳过法定节假日';

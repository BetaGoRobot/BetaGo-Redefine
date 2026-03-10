# Todo 与 Schedule 设计

## 概述

当前系统把“提醒”和“cron”合并为统一的 `schedule` 能力：

- 一次性提醒：`create_schedule` + `type=once` + `run_at` + `message`
- 周期任务：`create_schedule` + `type=cron` + `cron_expr`
- 定时执行工具：`create_schedule` + `tool_name` + `tool_args`

`todo` 负责任务记录本身，`schedule` 负责未来时刻或周期性的触发执行。

## 运行结构

```text
用户自然语言
  -> LLM Function Call
    -> todo tools
    -> schedule tools
      -> application layer
        -> repository
          -> PostgreSQL
```

相关目录：

- `internal/application/lark/todo/`
- `internal/application/lark/schedule/`
- `internal/domain/todo/`
- `internal/infrastructure/db/model/scheduled_tasks*.go`
- `internal/infrastructure/db/query/scheduled_tasks*.go`
- `internal/infrastructure/todo/`
- `internal/infrastructure/schedule/`

## Todo 能力

支持的工具：

- `create_todo`
- `update_todo`
- `list_todos`
- `delete_todo`

数据库表：

- `betago.todo_items`

## Schedule 能力

支持的工具：

- `create_schedule`
- `list_schedules`
- `delete_schedule`
- `pause_schedule`
- `resume_schedule`

数据库表：

- `betago.scheduled_tasks`

调度动作：

- 发送消息：内部统一映射为 `send_message`
- 执行可调度工具：通过 `tool_name` 和 `tool_args`

## 数据迁移

相关 migration：

- `script/migrations/001_add_todo_tables.sql`
- `script/migrations/002_add_scheduled_tasks.sql`
- `script/migrations/003_upgrade_scheduled_tasks_to_unified_schedule.sql`
- `script/migrations/004_cleanup_legacy_todo_tables.sql`
- `script/migrations/005_drop_legacy_cron_cmd_tasks.sql`
- `script/migrations/007_add_bot_identity_to_tasks.sql`

说明：

- 新库不会再创建旧提醒表
- 已有旧库会通过 `003` 补齐 `scheduled_tasks` 的统一字段
- `004` 会清理已废弃的旧表
- `005` 会删除已废弃的 `cron_cmd_tasks` 表
- `007` 会为 `todo_items` 和 `scheduled_tasks` 增加 `app_id` / `bot_open_id`，用于多机器人隔离

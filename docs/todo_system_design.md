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
- `query_schedule`
- `delete_schedule`
- `pause_schedule`
- `resume_schedule`

补充说明：

- `list_schedules` / `query_schedule` 现在除了返回文本结果外，也会直接发送 schema v2 卡片，便于人工浏览和后续操作。

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
- `script/migrations/010_add_source_message_to_scheduled_tasks.sql`

说明：

- 新库不会再创建旧提醒表
- 已有旧库会通过 `003` 补齐 `scheduled_tasks` 的统一字段
- `004` 会清理已废弃的旧表
- `005` 会删除已废弃的 `cron_cmd_tasks` 表
- `007` 会为 `todo_items` 和 `scheduled_tasks` 增加 `app_id` / `bot_open_id`，用于多机器人隔离
- `010` 会为 `scheduled_tasks` 增加 `source_message_id`，用于执行时回链到原始触发消息

## 2026-03-11 · 阶段进展 · Schedule 查询卡片交互补齐

### 方案

- `list_schedules` / `query_schedule` 的 schema v2 卡片携带可回放的 view state，卡片回调不依赖临时内存状态。
- 每个 schedule 条目直接渲染操作按钮：`暂停` / `恢复` / `删除`，减少“先复制 ID 再输命令”的跳转成本。
- 回调执行后按原视图重建卡片：列表视图保留 limit，查询视图保留 `id/name/status/type/tool_name` 过滤条件。

### 修改

- 新增 `internal/application/lark/schedule/card_action.go`
  - 定义 schedule 卡片动作 payload / parser
  - 定义 `TaskCardViewState`
  - 支持按原 view 重建卡片 payload
- 更新 `internal/application/lark/schedule/card_view.go`
  - 每个 task 区块追加操作按钮
  - 继续复用统一 footer：`撤回` + `Trace`
- 更新发卡入口
  - `internal/application/lark/schedule/func_call_tools.go`
  - `internal/application/lark/handlers/schedule_handler.go`
- 更新回调注册
  - `internal/application/lark/cardaction/builtin.go`
  - 注册 `schedule.pause` / `schedule.resume` / `schedule.delete`

### 决策

- 单 ID 查询视图在删除成功后返回空卡片，而不是保留已失效条目。
- 列表 / 过滤查询视图在动作成功后重新拉取当前 chat 下的数据，再按原过滤条件刷新，避免展示陈旧状态。

## 2026-03-11 · 阶段进展 · Schedule 管理命令补齐

### 方案

- 把 `/schedule pause`、`/schedule resume`、`/schedule delete` 补齐到 slash command 层，避免只有 tool call 和卡片按钮可用。
- 三条写路径统一加上 chat 作用域校验：只能操作当前群聊下的 schedule，避免跨群误删/误暂停。
- 命令执行后直接回显单任务结果卡片，而不是只回一条文本，保持和 `query_schedule`、卡片回调一致的交互形态。

### 修改

- 更新 `internal/application/lark/handlers/schedule_handler.go`
  - 新增 `SchedulePause`
  - 新增 `ScheduleResume`
  - 新增 `ScheduleDelete`
  - 新增单任务结果卡片发送 helper
- 更新 `internal/application/lark/command/command.go`
  - 注册 `schedule pause|resume|delete`
- 更新 `internal/application/lark/schedule/card_action.go`
  - 新增 `GetTaskForChat(...)`
- 更新 `internal/application/lark/schedule/func_call_tools.go`
  - `pause_schedule` / `resume_schedule` / `delete_schedule` 增加 chat 校验
- 更新 `internal/application/lark/cardaction/builtin.go`
  - 卡片按钮回调执行前增加 chat 校验

### 决策

- 命令态的 delete 成功后，回显一个“空的单任务查询卡片”，明确告诉操作者该 ID 已不再可见。
- “当前 chat 下不可见”统一按 not found 处理，不向用户泄露其他 chat 的 schedule 是否存在。

## 2026-03-11 · 阶段进展 · Schedule 创建命令补齐

### 方案

- 把 `create_schedule` 的核心能力补到 `/schedule create`，让 slash command 也能直接创建 once / cron schedule。
- 创建成功后直接回显单任务卡片，后续可以立即继续点 `暂停` / `删除`，减少二次查询。
- CLI 侧参数尽量对齐 tool call：支持 `name/type/run_at/cron_expr/timezone/message/tool_name/tool_args/notify_*`。

### 修改

- 更新 `internal/application/lark/handlers/schedule_handler.go`
  - 新增 `ScheduleCreateArgs`
  - 新增 `ScheduleCreate`
  - 复用 `schedule.CreateTaskRequest`
- 更新 `internal/application/lark/schedule/func_call_tools.go`
  - 导出 `ParseScheduleTime(...)` 供 slash command 复用
- 更新 `internal/application/lark/command/command.go`
  - 注册 `schedule create`
- 更新测试
  - `internal/application/lark/handlers/schedule_handler_test.go`
  - `internal/application/lark/command/help_test.go`

### 决策

- 当前 `/schedule create` 优先走结构化参数，不额外包装自然语言 DSL，避免再造一层弱约束解析。
- 创建成功后回显单任务查询卡片，而不是纯文本确认，保持管理体验一致。

## 2026-03-11 · 阶段进展 · Schedule 统一管理面板入口

### 方案

- 增加 `/schedule manage` 作为显式入口，语义上就是“打开当前群聊的 schedule 管理卡片”。
- `manage` 默认限制在更适合人工浏览的数量级，避免首屏卡片过长。
- 保留 `list` / `query` 作为更细粒度的读路径；`manage` 负责承接日常人工运维入口。

### 修改

- 更新 `internal/application/lark/handlers/schedule_handler.go`
  - 新增 `ScheduleManageArgs`
  - 新增 `ScheduleManage`
- 更新 `internal/application/lark/command/command.go`
  - 注册 `schedule manage`
- 更新测试
  - `internal/application/lark/handlers/schedule_handler_test.go`
  - `internal/application/lark/command/help_test.go`

### 决策

- `manage` 当前默认最多展示 20 条任务；更大范围的浏览继续走 `list --limit=...`。
- 面板本身继续复用已经具备动作按钮的列表卡，不再单独维护第二套“管理卡”布局。

## 2026-03-11 · 阶段进展 · 命令默认重定向收口

### 方案

- 把“无参数时跳到默认子命令”做成 `xcommand` 通用能力，而不是只给 `schedule` 做特判。
- 仅给“存在明显默认落点”的命令组启用默认重定向，避免对多义命令组强行猜测。

### 修改

- 更新 `pkg/xcommand/base.go`
  - `Command` 新增默认子命令能力
- 更新 `internal/application/lark/command/command.go`
  - `/schedule` 默认跳到 `manage`
  - `/config` 默认跳到 `list`
  - `/feature` 默认跳到 `list`
  - `/ratelimit` 默认跳到 `stats`
- 更新测试
  - `pkg/xcommand/typed_test.go`

### 决策

- 当前没有给 `/word`、`/reply`、`/image`、`/stock`、`/debug` 做默认重定向：
  - `word/reply/image` 虽然有读取子命令，但“新增”和“查看”都常见，不够单义。
  - `stock` 和 `debug` 天然是多分支入口，不适合默认猜测。

## 2026-03-11 · 阶段进展 · Schedule 卡片支持原地刷新

### 方案

- 对 schedule 原生 schema v2 卡片补 `刷新` 按钮，直接复用卡片里的 view state 重建面板，不再依赖重新输入命令。
- refresh 继续走 card action，同一张卡片上保留 `刷新` / `撤回` / `Trace` 三个统一 footer 按钮。
- 单 ID 查询视图刷新时允许目标任务已不存在：这种情况下回显空结果卡，避免 toast 报错打断人工操作。

### 修改

- 更新 `internal/application/lark/schedule/card_action.go`
  - 新增 `schedule.view` payload builder / parser
  - 抽出共享 view state 解析
- 更新 `internal/application/lark/schedule/card_view.go`
  - footer 注入 `刷新` payload
- 更新 `internal/application/lark/cardaction/builtin.go`
  - 注册 `schedule.view`
  - refresh 时按原 view 重建卡片
- 更新测试
  - `internal/application/lark/schedule/card_action_test.go`
  - `internal/application/lark/schedule/card_view_test.go`

### 决策

- refresh 不额外引入临时 session / cache key，直接把 view state 编码进 payload，保证回调幂等且可回放。
- 删除后的单任务查询 refresh 返回空卡片，而不是报 not found，和 delete 后的回显策略保持一致。

## 2026-03-11 · 阶段进展 · Schedule 触发结果回到来源消息

### 方案

- 在创建 schedule 时记录来源 `msgID`，而不是只保留 `chat_id`。
- 调度执行时复用这条来源消息，优先以 reply 形式回包，让人工侧能直接看出“这是哪条消息触发出来的结果”。
- reply 失败时再回落到 chat 直发，避免来源消息不可用时整条结果丢失。

### 修改

- 新增 `scheduled_tasks.source_message_id`
- `/schedule create` 与 `create_schedule` 都会记录当前消息 ID
- schedule 执行器会构造最小消息上下文，把 `message_id` 透传给 schedulable tools
- `send_message`、兼容卡片/文本发送、错误通知都会优先回复来源消息
- schedule 查询/管理卡片增加来源消息展示

### 决策

- 当前只持久化来源消息 ID，不做消息正文快照；来源定位和回复链路先闭环，消息内容追溯后续如有需要再单独设计。

## 2026-03-11 · 阶段进展 · Schedule 结果持久化改为对象引用

### 方案

- 不再把 MinIO presigned URL / 短链直接存进 `last_result`，而是存对象引用。
- 卡片上的“查看结果”链接在每次卡片构建时重新签发 MinIO 临时下载链接，并立即转成 short link。
- 结果对象迁移到独立 bucket `betago-schedule-results`，不再与历史 `cloudmusic` 内容混用。

### 修改

- `last_result` 现在存 `schedule-result://<object_key>` 形式的稳定引用
- 卡片重建时会基于该引用现签 5 分钟 presigned URL，并转成 short link
- schedule 卡片只渲染新引用格式；历史纯文本 / 旧短链结果不再兼容展示

### 决策

- 点击用的下载 URL 只需要短时有效，因此统一收口成 5 分钟；链接过期后通过刷新/重新查询卡片即可拿到新 short link。
- 如果 MinIO 无法自动建新 bucket，需要由运维侧预先建好 `betago-schedule-results` 或授予建桶权限。

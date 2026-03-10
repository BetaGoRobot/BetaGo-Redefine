# BetaGo-Redefine

BetaGo-Redefine 是一个基于 Go 的飞书机器人服务端仓库，核心场景是群聊消息处理、LLM 对话、历史消息检索、待办与定时任务，以及围绕群机器人治理的一整套配置、频控和卡片交互能力。

![Visualization of the codebase](https://github.com/BetaGoRobot/BetaGo-Redefine/blob/diagram/diagram.svg)

## 项目概览

当前仓库已经落地的核心能力包括：

- 飞书事件驱动机器人主服务，接收消息、表情反应、卡片回调等事件
- 基于火山方舟模型的聊天、推理、图片输入处理和工具调用
- 历史消息记录、分块归档、OpenSearch 检索与群聊上下文召回
- `todo` 与 `schedule` 两套任务能力，支持一次性提醒、cron 任务和定时执行工具
- 运行时配置管理、功能开关管理、智能频控、消息撤回/禁言等运营能力
- 图片词库、回复词库、词库、音乐卡片、金价/A 股查询、词云和活跃度分析等插件能力

## 架构与运行链路

主入口是 [`cmd/larkrobot`](./cmd/larkrobot)，启动流程大致如下：

1. 加载 TOML 配置，默认读取根目录 `.dev/config.toml`，也支持通过 `BETAGO_CONFIG_PATH` 覆盖
2. 初始化基础设施层，包括 OTel、PostgreSQL、OpenSearch、Ark Runtime、MinIO、网易云 API、AKTools、Gotify、Lark DAL 等
3. 初始化应用层，包括卡片动作注册、Todo 服务、Schedule 服务
4. 启动后台调度器，轮询 `scheduled_tasks`
5. 建立飞书 WebSocket Client，持续消费事件

对应的分层关系如下：

- `internal/interfaces/lark`：飞书事件入口
- `internal/application/lark`：消息处理、命令、卡片回调、todo、schedule 等应用逻辑
- `internal/domain`：Todo / Schedule 等领域模型
- `internal/infrastructure`：数据库、OpenSearch、Redis、Ark、MinIO、Gotify、网易云等外部依赖适配
- `pkg`：仓库内通用组件，例如命令框架、处理器、日志、HTTP、chunk 管理等

## 快速开始

### 1. 环境准备

建议至少准备以下依赖：

- Go `1.25+`
- PostgreSQL
- 飞书应用凭证
- 火山方舟 API Key 与模型 ID

完整功能通常还会依赖：

- Redis
- OpenSearch
- MinIO
- 网易云 API 服务
- AKTools 服务
- Gotify
- OTLP Collector

如果你需要构建网易云 API 辅助服务，先初始化子模块：

```bash
git submodule update --init --recursive
```

### 2. 准备配置文件

服务默认读取 `.dev/config.toml`。更推荐把本地配置放在未提交路径，并通过环境变量覆盖：

```bash
export BETAGO_CONFIG_PATH=/path/to/config.toml
```

当前代码里的配置分组如下：

| Section | 作用 |
| --- | --- |
| `base_info` | 机器人基础信息，如机器人名称 |
| `db_config` | PostgreSQL 连接配置 |
| `lark_config` | 飞书应用 `app_id`、`app_secret`、机器人 OpenID 等 |
| `ark_config` | 方舟 API Key 与 `reasoning/normal/embedding/vision/chunk` 模型 |
| `otel_config` | OTLP 上报配置 |
| `opensearch_config` | 历史消息、卡片动作、chunk 检索相关索引配置 |
| `minio_config` | 对象存储配置，音乐/图片等上传能力会用到 |
| `netease_music_config` | 网易云 API、播放器地址、登录信息 |
| `rate_config` | 机器人回复概率与意图兜底参数 |
| `ratelimit_config` | 智能频控阈值 |
| `proxy_config` | 私有代理配置 |
| `aktool_config` | 金价与股票行情数据服务地址 |
| `gotify_config` | 推送通知配置 |
| `redis_config` | 频控、chunk 会话和部分状态缓存 |
| `kutt_config` | 短链接服务配置 |

### 3. 数据库准备

当前仓库不能简单理解为“只执行 Todo / Schedule migration 就能完成初始化”。实际是否能正常运行，取决于当前启用的消息链路和后台任务会访问到的表是否已经存在。

默认消息链路和后台调度至少需要以下表：

- `scheduled_tasks`
- `prompt_template_args`
- `repeat_words_rate_customs`
- `repeat_words_rates`
- `quote_reply_msg_customs`
- `quote_reply_msgs`

其中 `prompt_template_args` 不仅要有表，还至少需要两条基础数据：

- `prompt_id = 3`：消息 chunk 汇总使用
- `prompt_id = 5`：主聊天链路使用

按功能启用的附加表包括：

- `todo_items`：Todo 能力
- `dynamic_configs`、`function_enablings`：配置和功能开关
- `interaction_stats`：互动统计
- `react_image_meterials`、`sticker_mappings`、`lark_imgs`：图片/贴纸素材能力
- `private_modes`、`msg_trace_logs`、`template_versions`：隐私模式、链路追踪、卡片模板增强

仓库中现有的 SQL migration 主要覆盖 Todo / Schedule 相关演进，位于 [`script/migrations`](./script/migrations)：

- `001_add_todo_tables.sql`
- `002_add_scheduled_tasks.sql`
- `003_upgrade_scheduled_tasks_to_unified_schedule.sql`
- `004_cleanup_legacy_todo_tables.sql`
- `005_drop_legacy_cron_cmd_tasks.sql`

如果数据库 schema 发生变化，可以使用下面的命令重新生成 GORM model/query：

```bash
go run ./cmd/generate
```

### 4. 本地运行

```bash
go run ./cmd/larkrobot
```

或先编译再启动：

```bash
go build -o ./bin/larkrobot ./cmd/larkrobot
./bin/larkrobot
```

### 5. Docker 构建

主服务镜像：

```bash
docker build -f script/larkrobot/Dockerfile -t betago-larkrobot:local .
```

网易云 API 辅助镜像：

```bash
docker build -f script/neteaseapi/Dockerfile -t betago-neteaseapi:local .
```

## 核心能力

### 消息与对话

- `/bb`：与机器人对话，支持普通模式和推理模式
- 自动记录群消息到 OpenSearch，并进行 chunk 聚合
- 支持基于历史消息的检索和上下文增强
- 支持图片输入、表情响应、词库回复、回复词库等

### 命令与工具

常见命令入口在 [`internal/application/lark/command/command.go`](./internal/application/lark/command/command.go)，包括：

| 命令 | 说明 |
| --- | --- |
| `/help` | 查看命令帮助 |
| `/config list/set/delete` | 管理运行时配置 |
| `/feature list/block/unblock` | 管理功能开关 |
| `/word add/get` | 管理词库 |
| `/reply add/get` | 管理回复词库 |
| `/image add/get/del` | 管理图片词库 |
| `/music` | 搜索音乐/专辑并发送卡片 |
| `/stock gold` / `/stock zh_a` | 查询金价和 A 股行情 |
| `/talkrate` | 活跃度趋势分析 |
| `/wc` | 词云统计 |
| `/ratelimit stats/list` | 查看频控面板 |
| `/mute` | 群级别禁言机器人 |

### Todo 与 Schedule

LLM 工具和应用服务已经接入以下任务能力：

- `create_todo`
- `update_todo`
- `list_todos`
- `delete_todo`
- `create_schedule`
- `list_schedules`
- `delete_schedule`
- `pause_schedule`
- `resume_schedule`

其中 `schedule` 支持：

- 单次提醒：`type=once + run_at + message`
- 周期任务：`type=cron + cron_expr`
- 定时执行工具：`tool_name + tool_args`

设计说明见 [`docs/todo_system_design.md`](./docs/todo_system_design.md)。

## 仓库结构

| 目录 | 说明 |
| --- | --- |
| `cmd/larkrobot` | 飞书机器人主程序 |
| `cmd/generate` | GORM model/query 生成命令 |
| `internal/application` | 应用层，包含消息处理、配置、todo、schedule、card action 等 |
| `internal/domain` | 领域模型与领域规则 |
| `internal/infrastructure` | 外部依赖适配层 |
| `internal/interfaces` | 对外事件入口 |
| `pkg` | 通用基础组件 |
| `script/larkrobot` | 主服务 Dockerfile |
| `script/neteaseapi` | 网易云 API Dockerfile 与子仓库封装，实际 API 仓库位于 `script/neteaseapi/api_repo` 子模块 |
| `script/migrations` | 数据库迁移脚本 |
| `docs` | 仓库级设计文档 |

## CI / 构建

GitHub Actions 已配置以下工作流：

- `docker-image-lark.yaml`：构建并推送主服务镜像
- `docker-image-netease.yaml`：构建并推送网易云 API 镜像
- `merge_check.yaml`：PR 构建检查，默认忽略仅 README 变更
- `create_diagram.yml`：生成代码结构图

## 进一步阅读

- [`docs/todo_system_design.md`](./docs/todo_system_design.md)
- [`internal/application/lark/cardaction/README.md`](./internal/application/lark/cardaction/README.md)
- [`internal/application/lark/ratelimit/README.md`](./internal/application/lark/ratelimit/README.md)

## License

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2FBetaGoRobot%2FBetaGo-Redefine.svg?type=large)](https://app.fossa.com/projects/git%2Bgithub.com%2FBetaGoRobot%2FBetaGo-Redefine?ref=badge_large)

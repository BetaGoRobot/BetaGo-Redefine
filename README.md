# BetaGo-Redefine

BetaGo-Redefine 是一个企业级智能聊天机器人系统，基于 Go 语言开发，目前主要针对飞书 (Lark) 平台。项目采用清晰的分层架构，集成了多种 AI 能力和实用工具，核心场景包括群聊消息处理、LLM 对话、历史消息检索、待办与定时任务，以及围绕群机器人治理的一整套配置、频控和卡片交互能力。

![Visualization of the codebase](https://github.com/BetaGoRobot/BetaGo-Redefine/blob/diagram/diagram.svg)

**GitHub**: https://github.com/BetaGoRobot/BetaGo-Redefine

## 技术栈

| 类别 | 技术 | 版本 |
|------|------|------|
| 语言 | Go | 1.25.1 |
| Web 框架 | Hertz | v0.10.4 |
| ORM | GORM | v1.31.1 |
| 数据库 | PostgreSQL | - |
| 缓存 | Redis | v9.18.0 |
| 对象存储 | Minio | v7.0.98 |
| 搜索引擎 | OpenSearch/Elasticsearch | - |
| 日志 | Zap | v1.27.1 |
| 监控 | OpenTelemetry | v1.40.0 |
| JSON 处理 | Sonic | v1.15.0 |
| Lark SDK | oapi-sdk-go | v3.5.3 |
| LLM | 字节跳动 ARK (豆包) | - |

## 项目概览

当前仓库已经落地的核心能力包括：

- 飞书事件驱动机器人主服务，接收消息、表情反应、卡片回调等事件
- 基于火山方舟模型的聊天、推理、图片输入处理和工具调用
- 历史消息记录、分块归档、OpenSearch 检索与群聊上下文召回
- `todo` 与 `schedule` 两套任务能力，支持一次性提醒、cron 任务和定时执行工具
- 运行时配置管理、功能开关管理、智能频控、消息撤回/禁言等运营能力
- 图片词库、回复词库、音乐卡片、金价/A 股查询、词云和活跃度分析等插件能力
- 命令帮助卡、参数表单卡、枚举参数下拉与管理面板卡片回调
- `cmd/lark-card-debug` 调试入口，可把模板卡、schema v2 卡或内置 spec 直接发到指定 `chat_id` / `open_id`

## 核心功能

### 1. 智能聊天 (Chat)
- 支持文本和图片输入
- 提供 Reasoning（推理）和 Normal（普通）两种模式
- 集成上下文理解和历史消息管理
- 支持通过 @bot 或命令触发

### 2. 音乐搜索与分享
- 网易云音乐搜索（歌曲、专辑、歌单）
- 音乐卡片展示和分享
- 歌词显示
- 音乐播放器集成
- 长列表卡片的异步流式刷新与翻页

### 3. 股票查询
- 股票行情查询（A股）
- 黄金价格查询
- K线图展示

### 4. 工具与服务
- 单词计数和统计
- 图片分析与处理
- 网页截图
- 消息翻译
- 天气查询
- 提醒服务
- URL 缩短

### 5. 互动功能
- 消息反应和自动回复
- 重复消息检测
- 模仿用户说话
- 聊天记录查询
- 消息撤回

### 6. 管理功能
- 聊天静音管理
- 功能开关控制
- 自定义配置
- 性能监控

## 架构与运行链路

主入口是 [`cmd/larkrobot`](./cmd/larkrobot)，启动流程大致如下：

1. 加载 TOML 配置，默认读取根目录 `.dev/config.toml`，也支持通过 `BETAGO_CONFIG_PATH` 覆盖
2. 初始化基础设施层，包括 OTel、PostgreSQL、OpenSearch、Ark Runtime、MinIO、网易云 API、AKTools、Gotify、Lark DAL 等
3. 初始化应用层，包括卡片动作注册、Todo 服务、Schedule 服务
4. 启动后台调度器，轮询 `scheduled_tasks`
5. 建立飞书 WebSocket Client，持续消费事件

### 分层架构

```
┌─────────────────────────────────────────┐
│         Interfaces (接口层)              │
│  - Lark WebSocket 事件接收               │
└──────────────┬──────────────────────────┘
               │
┌──────────────▼──────────────────────────┐
│       Application (应用层)               │
│  - Handlers: 消息/卡片/反应处理          │
│  - Command: 命令解析                      │
│  - History: 历史消息                      │
└──────────────┬──────────────────────────┘
               │
┌──────────────▼──────────────────────────┐
│        Domain (领域层)                   │
│  - 核心业务模型                           │
└──────────────┬──────────────────────────┘
               │
┌──────────────▼──────────────────────────┐
│    Infrastructure (基础设施层)           │
│  - DB, Cache, Search, Storage           │
│  - Lark DAL, ARK DAL                    │
│  - Config, Logging, Tracing             │
└─────────────────────────────────────────┘
```

对应的分层关系如下：

- `internal/interfaces/lark`：飞书事件入口
- `internal/application/lark`：消息处理、命令、卡片回调、todo、schedule 等应用逻辑
- `internal/domain`：Todo / Schedule 等领域模型
- `internal/infrastructure`：数据库、OpenSearch、Redis、Ark、MinIO、Gotify、网易云等外部依赖适配
- `pkg`：仓库内通用组件，例如命令框架、处理器、日志、HTTP、chunk 管理等

### 消息处理流程

```
Lark WebSocket Event
    ↓
MessageV2Handler (interfaces/lark)
    ↓
messages.Handler (application/lark/messages)
    ↓
具体 Handler (chat/music/stock/...)
    ↓
基础设施服务 (DB/ARK/Search/...)
```

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

主要配置项示例：

```toml
[base_info]
    robot_name = "LarkBotV2"

[db_config]
    host = "localhost"
    port = 15432
    user = "postgres"
    password = "***"
    dbname = "betago"

[lark_config]
    app_id = "cli_***"
    app_secret = "***"
    bot_open_id = "ou_***"
    bootstrap_admin_open_id = "ou_xxx"

[ark_config]
    api_key = "***"
    chunk_model = "ep-***"
    embedding_model = "ep-***"
    normal_model = "doubao-seed-1-8-251228"
    reasoning_model = "doubao-seed-1-8-251228"
    vision_model = "doubao-seed-1-8-251228"

[minio_config]
    ak = "***"
    sk = "***"
    [minio_config.internal]
        endpoint = "minio.example.com:19000"
    [minio_config.external]
        endpoint = "minioapi.example.com:2443"

[opensearch_config]
    domain = "localhost"
    user = "***"
    password = "***"
    lark_msg_index = "lark_msg_index_jieba"

[redis_config]
    addr = "localhost:16379"

[netease_music_config]
    base_url = "http://localhost:3336"
    user_name = "***"
    pass_word = "***"

[otel_config]
    collector_endpoint = "localhost:4317"
    service_name = "BetaGoV2"
```

当前代码里的配置分组如下：

| Section | 作用 |
| --- | --- |
| `base_info` | 机器人基础信息，如机器人名称 |
| `db_config` | PostgreSQL 连接配置 |
| `lark_config` | 飞书应用 `app_id`、`app_secret`、机器人 OpenID，以及 `bootstrap_admin_open_id` |
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

当前仓库不能简单理解为"只执行 Todo / Schedule migration 就能完成初始化"。实际是否能正常运行，取决于当前启用的消息链路和后台任务会访问到的表是否已经存在。

#### 主要数据表 (约 30 个):

| 表名 | 用途 |
|------|------|
| message_logs | 消息日志 |
| chat_record_logs | 聊天记录 |
| channel_logs | 频道日志 |
| lark_imgs | 图片记录 |
| prompt_confs | 提示词配置 |
| prompt_template_args | 提示词模板参数 |
| command_infos | 命令信息 |
| function_enablings | 功能开关 |
| interaction_stats | 交互统计 |
| imitate_rate_customs | 模仿频率定制 |
| repeat_words_rates | 重复词频率 |
| reaction_whitelists | 反应白名单 |
| dynamic_configs | 动态配置 |
| cron_cmd_tasks | 定时任务 |
| scheduled_tasks | 调度任务 |
| todo_items | Todo 任务 |
| permission_grants | 权限授权 |

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
- `permission_grants`：权限点授权，当前用于约束 `config.write@global` 和权限面板的授权管理
- `interaction_stats`：互动统计
- `react_image_meterials`、`sticker_mappings`、`lark_imgs`：图片/贴纸素材能力
- `private_modes`、`msg_trace_logs`、`template_versions`：隐私模式、链路追踪、卡片模板增强

仓库中现有的 SQL migration 主要覆盖 Todo / Schedule 相关演进，位于 [`script/migrations`](./script/migrations)：

- `001_add_todo_tables.sql`
- `002_add_scheduled_tasks.sql`
- `003_upgrade_scheduled_tasks_to_unified_schedule.sql`
- `004_cleanup_legacy_todo_tables.sql`
- `005_drop_legacy_cron_cmd_tasks.sql`
- `006_add_permission_grants.sql`
- `007_add_bot_identity_to_tasks.sql`

如果数据库 schema 发生变化，可以使用下面的命令重新生成 GORM model/query：

```bash
go run ./cmd/generate
```

如果你要把历史库收敛到"当前运行时代码实际使用的 PG 表"，可以直接使用仓库内脚本迁移到新 schema：

```bash
DSN='postgres://user:pass@host:5432/dbname?sslmode=disable' \
NEW_SCHEMA=betago_clean \
./script/migrate_to_new_schema.sh
```

相关脚本：

- `script/migrate_to_new_schema.sh`：识别候选活跃表、建新 schema、复制数据、修正序列、校验行数
- `script/sql/copy_active_tables.sql`：按显式列名复制活跃表
- `script/sql/validate_active_tables.sql`：校验旧/新 schema 的行数与 `prompt_template_args` 种子数据

### 4. 本地运行

```bash
# 安装依赖
go mod download

# 运行
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
| `/help` | 查看命令帮助卡，并可直接跳转到子命令参数卡 |
| `/config list/set/delete` | 管理运行时配置 |
| `/feature list/block/unblock` | 管理功能开关 |
| `/word add/get` | 管理词库 |
| `/reply add/get` | 管理回复词库 |
| `/image add/get/del` | 管理图片词库 |
| `/music` | 搜索歌曲 / 专辑 / 歌单，并发送支持流式刷新与翻页的卡片 |
| `/stock gold` / `/stock zh_a` | 查询金价和 A 股行情 |
| `/talkrate` | 活跃度趋势分析 |
| `/wordcount` (`/wc`) | 词云、chunk 列表、chunk 详情、词云图、发言趋势分析 |
| `/ratelimit stats/list` | 查看频控面板 |
| `/mute` | 群级别禁言机器人 |
| `/permission` | 查看当前机器人支持的权限点，并交互式管理用户授权 |

命令系统当前额外支持：

- `/help <command>` 直接返回 schema v2 帮助卡，而不是纯文本
- 帮助卡可直接打开命令参数卡；显式子命令不会再错误回落到 default subcommand
- 通过 typed handler 注册的有限枚举参数会自动变成下拉选项，并在卡片回显当前值
- `/wordcount` 是 canonical command，`/wc` 是框架级 alias；help、form、执行链路都会统一解析

权限管理面板目前内建两个权限点：

- `permission.manage@global`
- `config.write@global`

首次引导时，可以在 `config.toml` 里配置：

```toml
[lark_config]
bootstrap_admin_open_id = "ou_xxx"
```

这个 bootstrap admin 只用于启动阶段进入权限面板和授予首批权限，不会自动写入 `permission_grants`。

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

## 开发调试

### 1. 卡片调试 CLI

仓库内置了一个专门的飞书卡片调试入口：

- 二进制入口：[`cmd/lark-card-debug`](./cmd/lark-card-debug)
- Codex skill：[`lark-card-debug`](./.codex/skills/lark-card-debug/SKILL.md)

它支持三类输入：

- 内置调试 spec，例如 `config`、`feature`、`permission`、`ratelimit.sample`、`schedule.sample`
- 模板卡片：`--template + --vars-json`
- 原生 schema v2 卡片：`--card-json` 或 `--card-file`

常用示例：

```bash
go run ./cmd/lark-card-debug --list-specs
go run ./cmd/lark-card-debug --spec ratelimit.sample --to-open-id ou_xxx
go run ./cmd/lark-card-debug --template NormalCardReplyTemplate --vars-json '{"title":"BetaGo","content":"调试卡片"}' --to-open-id ou_xxx
go run ./cmd/lark-card-debug --card-file /tmp/card.json --to-open-id ou_xxx
```

如果你在 Codex 中工作，优先复用 skill 包装脚本：

```bash
.codex/skills/lark-card-debug/scripts/send_card.sh --list-specs
.codex/skills/lark-card-debug/scripts/send_card.sh --spec config --to-open-id ou_xxx --chat-id oc_xxx
```

### 2. 命令卡片调试

命令帮助与参数卡片的主链路位于：

- [`internal/application/lark/command`](./internal/application/lark/command)
- [`internal/application/lark/cardaction`](./internal/application/lark/cardaction)

推荐从下面几个入口验证：

```text
/help music
/help wordcount
/music --type=playlist 3778678
/wc chunks --question_mode=question
```

更多约束和扩展建议见：

- [`internal/application/lark/command/README.md`](./internal/application/lark/command/README.md)
- [`internal/application/lark/cardaction/README.md`](./internal/application/lark/cardaction/README.md)
- [`internal/infrastructure/lark_dal/larkmsg/CARD_V2.md`](./internal/infrastructure/lark_dal/larkmsg/CARD_V2.md)

## 仓库结构

```
BetaGo-Redefine/
├── cmd/
│   ├── generate/          # 代码生成工具
│   ├── lark-card-debug/   # 飞书卡片调试 CLI
│   └── larkrobot/         # Lark 机器人主程序入口
├── internal/
│   ├── application/       # 应用层（业务逻辑）
│   │   └── lark/
│   │       ├── carddebug/       # 卡片调试 spec / build / send
│   │       ├── card_handlers/   # 卡片交互处理器
│   │       ├── chunking/        # 消息分块处理
│   │       ├── command/         # 命令解析器
│   │       ├── handlers/        # 事件处理器
│   │       ├── history/         # 历史消息管理
│   │       ├── messages/        # 消息处理
│   │       ├── reaction/        # 反应处理
│   │       └── utils/           # 工具函数
│   ├── domain/            # 领域层（核心业务模型）
│   ├── infrastructure/    # 基础设施层
│   │   ├── aktool/        # API 密钥管理
│   │   ├── ark_dal/       # 字节跳动 ARK 接口
│   │   ├── cache/         # 缓存服务
│   │   ├── config/        # 配置管理
│   │   ├── db/            # 数据库访问
│   │   ├── gotify/        # Gotify 通知
│   │   ├── hitokoto/      # 一言服务
│   │   ├── lark_dal/      # Lark 平台 DAL
│   │   ├── miniodal/      # Minio 对象存储
│   │   ├── music/         # 音乐服务
│   │   ├── neteaseapi/    # 网易云音乐 API
│   │   ├── opensearch/    # OpenSearch 搜索
│   │   ├── otel/          # OpenTelemetry 监控
│   │   ├── redis/         # Redis 服务
│   │   ├── retriver/      # 检索服务
│   │   ├── shorter/       # URL 缩短
│   │   └── vadvisor/      # 可视化顾问
│   ├── interfaces/        # 接口层（与外部系统交互）
│   │   └── lark/          # Lark 平台事件处理
│   └── xmodel/            # 数据模型
├── pkg/                   # 公共库
│   ├── logs/              # 日志系统
│   ├── utils/             # 通用工具
│   ├── xchunk/            # 分块处理
│   ├── xcmd/              # 命令系统
│   ├── xcommand/          # 命令接口
│   ├── xconstraints/      # 约束定义
│   ├── xcopywriting/      # 文案管理
│   ├── xerror/            # 错误处理
│   ├── xhandler/          # 事件处理基类
│   ├── xhttp/             # HTTP 工具
│   └── xrequest/          # 请求封装
├── configs/               # 配置文件
├── script/                # 脚本文件
│   └── neteaseapi/
│       └── api_repo/      # 网易云音乐 API（Git 子模块）
├── .dev/                  # 开发环境配置
├── docs/                  # 项目文档
├── go.mod/go.sum          # Go 依赖管理
└── README.md
```

## CI / 构建

GitHub Actions 已配置以下工作流：

- `docker-image-lark.yaml`：构建并推送主服务镜像
- `docker-image-netease.yaml`：构建并推送网易云 API 镜像
- `merge_check.yaml`：PR 构建检查，默认忽略仅 README 变更
- `create_diagram.yml`：生成代码结构图

## 扩展开发

### 添加新的消息处理器

1. 在 `internal/application/lark/handlers/` 下创建新文件
2. 实现 handler 函数
3. 在 `messages.Handler` 中注册

### 添加新的卡片交互

1. 在 `internal/application/lark/card_handlers/` 中添加处理函数
2. 在 `CardActionHandler` 中注册对应的 button type

## 进一步阅读

- [`docs/todo_system_design.md`](./docs/todo_system_design.md)
- [`docs/permission_scope_constraints.md`](./docs/permission_scope_constraints.md)
- [`internal/application/lark/cardaction/README.md`](./internal/application/lark/cardaction/README.md)
- [`internal/application/lark/ratelimit/README.md`](./internal/application/lark/ratelimit/README.md)

## License

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2FBetaGoRobot%2FBetaGo-Redefine.svg?type=large)](https://app.fossa.com/projects/git%2Bgithub.com%2FBetaGoRobot%2FBetaGo-Redefine?ref=badge_large)

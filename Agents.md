# BetaGo-Redefine 项目指南

## 项目概述

BetaGo-Redefine 是一个企业级智能聊天机器人系统，基于 Go 语言开发，目前主要针对飞书 (Lark) 平台。项目采用清晰的分层架构，集成了多种 AI 能力和实用工具。

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

## 项目结构

```
BetaGo-Redefine/
├── cmd/
│   ├── generate/          # 代码生成工具
│   └── larkrobot/         # Lark 机器人主程序入口
├── internal/
│   ├── application/       # 应用层（业务逻辑）
│   │   └── lark/
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
├── go.mod/go.sum          # Go 依赖管理
└── README.md
```

## 核心功能

### 1. 智能聊天 (Chat)
- 支持文本和图片输入
- 提供 Reasoning（推理）和 Normal（普通）两种模式
- 集成上下文理解和历史消息管理
- 支持通过 @bot 或命令触发

### 2. 音乐搜索与分享
- 网易云音乐搜索（歌曲、专辑）
- 音乐卡片展示和分享
- 歌词显示
- 音乐播放器集成

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

## 配置说明

配置文件位置: `.dev/config.toml`

主要配置项:

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

## 数据库模型

主要数据表 (约 30 个):

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

## Agent 数据库变更约束

凡是涉及新建表、改表、索引或约束变更，先遵循 `script/AGENT_DB_CHANGE_SOP.md`。

硬约束：

- 先生成 SQL，并保存到 `script/sql/`
- 先等待用户执行 SQL
- 再等待用户执行 `go run ./cmd/generate`
- 用户确认前，不继续写依赖新 schema 的业务代码

## 本地开发

### 前置要求
- Go 1.25+
- PostgreSQL 15+
- Redis
- Minio (可选)
- OpenSearch/Elasticsearch (可选)

### 快速开始

```bash
# 克隆仓库
git clone https://github.com/BetaGoRobot/BetaGo-Redefine.git
cd BetaGo-Redefine

# 初始化网易云音乐 API 子模块
git submodule update --init --recursive

# 安装依赖
go mod download

# 配置
cp .dev/config.toml.example .dev/config.toml
# 编辑 .dev/config.toml 填入你的配置

# 运行
go run cmd/larkrobot/main.go
```

### 环境变量

| 变量名 | 说明 |
|--------|------|
| BETAGO_CONFIG_PATH | 配置文件路径 |

## 架构设计

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

## 关键文件说明

| 文件路径 | 说明 |
|----------|------|
| `cmd/larkrobot/main.go` | 主程序入口 |
| `internal/interfaces/lark/handler.go` | Lark 事件处理入口 |
| `internal/application/lark/handlers/` | 各功能处理器 |
| `internal/infrastructure/config/` | 配置管理 |
| `internal/infrastructure/db/` | 数据库访问 |
| `internal/infrastructure/ark_dal/` | 字节跳动 ARK 接口 |
| `pkg/xhandler/` | 事件处理基类 |

## 扩展开发

### 添加新的消息处理器

1. 在 `internal/application/lark/handlers/` 下创建新文件
2. 实现 handler 函数
3. 在 `messages.Handler` 中注册

### 添加新的卡片交互

1. 在 `internal/application/lark/card_handlers/` 中添加处理函数
2. 在 `CardActionHandler` 中注册对应的 button type

## 许可证

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2FBetaGoRobot%2FBetaGo.svg?type=large)](https://app.fossa.com/projects/git%2Bgithub.com%2FBetaGoRobot%2FBetaGo?ref=badge_large)

# Plan: QQBot 兼容接入与多平台能力收口

**Generated**: 2026-03-19
**Estimated Complexity**: High

## Overview
当前仓库已经具备较成熟的运行时、执行器、命令框架和业务能力，但整体仍是明显的单平台 Lark 实现：

- 运行入口固定为 `cmd/larkrobot` + `internal/runtime/lark_ws.go`
- 接口层固定消费 `larkim.P2MessageReceiveV1` / `P2MessageReactionCreatedV1`
- 应用层命令、handler、agent runtime、history、chunking 基本都直接依赖 Lark 事件模型
- 基础设施层存在大量 `lark_dal/*`、Lark 卡片、Lark reaction、Lark 图片 key、Lark OpenID 语义
- 配置、索引名和消息存储模型也带有明显 Lark 命名

参考 QQ 官方 `botgo` 推荐接法，本次兼容不建议“把 QQ 逻辑直接塞进现有 Lark 包里”，而应该采用渐进式 strangler 方案：

1. 先新增 QQBot 的 webhook 回调入口、token 刷新和 OpenAPI 客户端。
2. 在入口与业务之间补一层最小平台抽象，先覆盖“文本消息 -> 命令/聊天 -> 文本回复”。
3. 只迁移平台无关或可降级的能力，把卡片、reaction、消息撤回、审批卡等能力继续保留为 Lark-only。

这样可以先得到一个可运行的 QQBot MVP，而不是先陷入一次性全量重构。

## Current Findings

### 已有可复用资产
- `internal/runtime` 的 `App` / `Module` / `Executor` 生命周期管理可直接复用。
- `pkg/xhandler` 与 `pkg/xcommand` 是泛型实现，理论上可以承载非 Lark 事件。
- 大部分业务服务本身依赖数据库、OpenSearch、Redis、Ark、MinIO，不依赖飞书连接方式。
- `sendCompatibleText` 这类 helper 已经体现出“优先 reply，失败则 create”的兼容意图，可作为后续抽象参考。

### 主要耦合点
- `cmd/larkrobot/bootstrap.go` 直接装配 `lark_dal`、`larkiface.HandlerSet`、`NewLarkWSModule(...)`。
- `internal/interfaces/lark/handler.go` 只认 Lark websocket/callback 事件。
- `internal/application/lark/messages/handler.go`、`internal/application/lark/command/command.go`、`internal/application/lark/handlers/*.go` 大量直接依赖 `*larkim.P2MessageReceiveV1`。
- `internal/infrastructure/lark_dal/larkmsg/*` 承担了解析、发送、卡片 patch、reaction、图片、提及、消息查询等平台能力。
- `internal/xmodel.MessageLog`、`internal/application/config/definitions.go`、`internal/infrastructure/config/configs.go` 仍以 `OpenID`、`lark_msg_index`、`lark_chunk_index`、`lark_card_action_index` 为中心。
- `internal/application/lark/agentruntime/*`、卡片动作、审批流、卡片回归测试几乎全部依赖 Lark 卡片协议。

## Recommendation

### 方案 A: 直接为 QQ 再写一套 handler
- **优点**: 上线最快，几乎不动现有 Lark 主链路。
- **缺点**: 业务逻辑会复制两份，后续维护成本最高。

### 方案 B: 先做彻底的平台抽象，再接 QQ
- **优点**: 架构最干净。
- **缺点**: 前置重构过大，短期没有可用增量，风险最高。

### 方案 C: 渐进式 strangler 改造（推荐）
- **优点**: 可以先交付 QQ 文本链路 MVP，同时为后续多平台演进铺路。
- **缺点**: 一段时间内会同时存在 Lark 旧路径和新抽象路径。

推荐采用方案 C：先抽“最小公共面”，只迁移 QQ 必需能力和可复用业务能力。

## Scope Assumptions
- 第一阶段目标以 QQBot webhook 回调模式为准，优先支持 `C2C_MESSAGE_CREATE`。
- 第一阶段不追求 Lark 与 QQ 完全同构；允许 QQ 端先只有文本/基础图片能力。
- 第一阶段不迁移 Lark card action、审批卡、streaming card、reaction、撤回/伪撤回、卡片回归调试。
- 若后续需要数据库字段或索引变更，必须遵循 [script/AGENT_DB_CHANGE_SOP.md](/mnt/RapidPool/workspace/BetaGo_v2/script/AGENT_DB_CHANGE_SOP.md)。

## Feature Compatibility Matrix

### P0: QQ MVP 必做
- webhook 回调接入
- QQ Bot token source 与自动刷新
- C2C 文本消息接收
- 文本命令解析
- 标准文本聊天回复
- 基础 `send_message`
- 配置/功能开关/禁言机器人这类纯文本管理命令

### P1: 可迁移但允许降级
- `oneword`、`stock`、`music`、`history search`、`word/reply` 这类工具能力
- `todo` / `schedule` 服务本体
- 历史消息记录与检索
- 图片输入与图片素材能力

### P2: 暂缓，保持 Lark-only
- 卡片按钮回调
- streaming agentic card
- approval / defer approval 卡片
- reaction 跟随、DONE/OnIt 表情
- 消息撤回/伪撤回
- card regression / card debug
- Lark 专属图片 key、会话线程、卡片 patch 语义

## Prerequisites
- 引入 `github.com/tencent-connect/botgo` 依赖，并锁定一个稳定版本。
- 明确 QQBot 的部署模式：固定公网 IP、回调地址、事件配置、IP 白名单。
- 明确是否仅支持 C2C，还是要同时覆盖群/频道事件。
- 接受“QQ 先文本优先，复杂富交互后补”的阶段性约束。

## Sprint 1: 接入 QQBot 运行入口
**Goal**: 在不影响现有 `cmd/larkrobot` 的前提下，新增一个可启动、可验活、可收事件、可发消息的 QQBot 进程入口。

**Demo/Validation**:
- `cmd/qqbot` 可以独立启动。
- 本地或测试环境能收到 QQ webhook 回调。
- 能用 `openapi.OpenAPI` 回发一条文本消息。

### Task 1.1: 新增 QQBot 配置模型
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/config/configs.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml.example`
- **Description**: 增加 `qqbot_config`、`qqbot_webhook_config`，至少覆盖 `app_id`、`app_secret`、监听地址、回调路径、调试开关、超时等字段。
- **Dependencies**: 无
- **Acceptance Criteria**:
  - QQ 配置与 `lark_config` 并列存在，不复用错误语义字段。
  - 空配置时模块可明确 disabled，而不是 panic。
- **Validation**:
  - 新增配置加载单测。

### Task 1.2: 新增 QQBot API / token 模块
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/qqbot_dal/client.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/qqbot_dal/token.go`
- **Description**: 基于 `token.NewQQBotTokenSource(...)`、`token.StartRefreshAccessToken(...)`、`botgo.NewOpenAPI(...)` 封装 QQ OpenAPI 客户端与 token 生命周期。
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - 客户端获取和 token 刷新不依赖包级裸全局。
  - 模块可暴露 `Status()` 或等价健康状态。
- **Validation**:
  - 单测覆盖空配置、客户端构造、重复初始化。

### Task 1.3: 新增 webhook ingress 模块
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/runtime/qq_webhook.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/interfaces/qqbot/handler.go`
- **Description**: 参考现有 `HealthHTTPModule` 与 QQ 官方示例，新增 `QQWebhookModule`，负责：
  - 注册 `/callback` 或配置化 path
  - 调用 `webhook.HTTPHandler(...)`
  - 将 SDK 事件桥接到仓库内部 handler set
- **Dependencies**: Task 1.2
- **Acceptance Criteria**:
  - QQ webhook 模块接入 `App` 生命周期，而不是匿名后台 goroutine。
  - 回调监听与管理面 HTTP 不互相干扰。
- **Validation**:
  - 使用伪请求或集成测试验证 handler 被调用。

### Task 1.4: 新增独立启动入口
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/cmd/qqbot/main.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/cmd/qqbot/bootstrap.go`
- **Description**: 参照 `cmd/larkrobot` 结构新增 QQBot 入口，不建议在第一阶段把 Lark 与 QQ 强行塞进同一二进制。
- **Dependencies**: Task 1.1, Task 1.2, Task 1.3
- **Acceptance Criteria**:
  - `cmd/qqbot` 与 `cmd/larkrobot` 共享基础设施初始化方式。
  - 能独立 start/stop，支持健康检查。
- **Validation**:
  - 运行 smoke test，确认模块 Ready 正常。

## Sprint 2: 建立最小平台抽象层
**Goal**: 不直接把 QQ 事件塞进 `internal/application/lark/*`，而是提炼一个最小公共消息模型和出站接口。

**Demo/Validation**:
- Lark 和 QQ 都能被转换为统一 `InboundMessage`。
- 至少一个文本 handler 同时支持两种平台适配。

### Task 2.1: 定义通用消息/身份模型
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/botcore/model.go`
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/botcore/identity.go`
- **Description**: 定义统一的消息契约，例如：
  - `Platform`
  - `ConversationID`
  - `ConversationType`
  - `MessageID`
  - `SenderID`
  - `SenderName`
  - `Text`
  - `Mentioned`
  - `Attachments`
  - `ReplyToMessageID`
  - `OccurredAt`
- **Dependencies**: Sprint 1
- **Acceptance Criteria**:
  - 不再把 `OpenID`、`ChatId`、`ThreadId` 当成跨平台通用字段名。
  - 身份模型能兼容 Lark 与 QQ，不破坏现有 ADR 的租户隔离原则。
- **Validation**:
  - 为 Lark/QQ 各写一组 adapter 单测。

### Task 2.2: 定义通用出站能力接口
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/botcore/outbound.go`
- **Description**: 定义统一 sender 能力，最小集先包括：
  - `ReplyText`
  - `SendText`
  - `ReplyImage` 或 `SendImage`（可选）
  - `SupportsCard`
  - `SupportsPatch`
  - `SupportsReaction`
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - 业务层可以按能力分支，而不是直接 import `larkmsg`.
  - 不支持的能力以显式 capability 标记返回，而不是静默失败。
- **Validation**:
  - fake sender 单测覆盖不支持场景。

### Task 2.3: 为 Lark 提供 adapter，验证抽象设计
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/botcore/lark_adapter.go`
  - 逐步替换 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/schedule_compat.go`
- **Description**: 先为现有 Lark 事件写 adapter，证明抽象能承载旧链路，再接 QQ，避免“只为 QQ 设计、却无法承载 Lark”。
- **Dependencies**: Task 2.1, Task 2.2
- **Acceptance Criteria**:
  - 一部分已有 helper 从 `*larkim.P2MessageReceiveV1` 迁移到通用模型。
  - 不要求第一阶段全量替换 Lark 旧路径。
- **Validation**:
  - 回归 `sendCompatibleText` 一类单测。

### Task 2.4: 建立 QQ adapter
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/botcore/qq_adapter.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/interfaces/qqbot/handler.go`
- **Description**: 把 `dto.WSC2CMessageData` 映射为统一模型；处理 self-message、过期事件、mention/命令触发规则、回复目标提取。
- **Dependencies**: Task 2.1, Task 2.2
- **Acceptance Criteria**:
  - QQ 入口不直接依赖 Lark handler 包。
  - 触发规则与 Lark 语义差异可在 adapter 层收敛。
- **Validation**:
  - 事件转换单测覆盖空消息、文本消息、带附件消息。

## Sprint 3: 落地 QQ 文本链路 MVP
**Goal**: 让 QQBot 至少可用“文本消息 -> 命令/聊天 -> 文本回复”主链路。

**Demo/Validation**:
- QQ 用户可发送普通文本或 `/command`。
- 机器人可完成标准聊天、帮助命令、配置/功能开关类命令。

### Task 3.1: 抽离通用命令入口
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/pkg/xcommand/`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/command/command.go`
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/botcore/command/`
- **Description**: 让命令树不再以 `LarkRootCommand` 命名与定型；保留 `pkg/xcommand` 泛型能力，把平台相关 data 类型与输出逻辑下沉到 adapter 或 handler wrapper。
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 至少有一个平台无关的 root command 装配方式。
  - Lark 旧命令入口可先保留兼容包装。
- **Validation**:
  - 命令解析单测可同时跑在 fake Lark/fake QQ 上。

### Task 3.2: 优先迁移文本优先功能
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/config_handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/mute_handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/oneword_handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/send_message_handler.go`
- **Description**: 先把纯文本/轻输出 handler 迁移为通用实现，或者在外层加 botcore wrapper。
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - `help/config/feature/mute/send_message/oneword` 这类能力可以在 QQ 使用。
  - 输出不再强依赖 Lark 卡片。
- **Validation**:
  - 端到端验证这几个命令在 QQ 上能得到正确回复。

### Task 3.3: 接通标准聊天模式
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/chat_handler.go`
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/botcore/chat.go`
- **Description**: 先只迁移标准文本聊天，不迁移 Lark agentic streaming card。QQ 侧默认使用 text reply mode。
- **Dependencies**: Task 3.2
- **Acceptance Criteria**:
  - QQ 收到文本问题后可以走 Ark 模型并文本回复。
  - `agentic` 模式在 QQ 显式关闭或降级。
- **Validation**:
  - 为 QQ 增加聊天链路 smoke test。

### Task 3.4: 建立 QQ 侧能力开关
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/config/`
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/botcore/capability.go`
- **Description**: 对平台差异大的功能设置 capability gate，避免 handler 内部大量 if/else。
- **Dependencies**: Task 3.2, Task 3.3
- **Acceptance Criteria**:
  - QQ 上调用不支持的卡片/reaction 能力时，能给出明确降级提示。
- **Validation**:
  - 单测覆盖 capability false 时的返回。

## Sprint 4: 收口消息记录、检索与任务能力
**Goal**: 让 QQBot 具备“可持续运营”的核心后端能力，而不仅是即时回复。

**Demo/Validation**:
- QQ 消息可落库、入索引、可检索。
- `todo` / `schedule` 基础服务可在 QQ 使用文本回执。

### Task 4.1: 泛化消息记录模型
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/xmodel/models.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/recording/service.go`
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/botcore/recording/`
- **Description**: 把 `MessageLog` 从 Lark 语义提升为平台中立语义，至少新增或替换：
  - `platform`
  - `sender_id`
  - `sender_name`
  - `conversation_id`
  - `conversation_type`
  - `raw_event`
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 不再要求所有平台都具备 `OpenID`、`ThreadID`。
  - Lark 旧字段的兼容迁移路径清晰。
- **Validation**:
  - 先出 schema 设计，再按 SOP 落 SQL 和生成代码。

### Task 4.2: 泛化 OpenSearch / retriever 索引命名
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/config/configs.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/config/definitions.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/config/manager.go`
- **Description**: 把 `lark_msg_index`、`lark_chunk_index` 逐步重命名为平台中立配置，或新增 `platform_msg_index` / `platform_chunk_index`。
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - 配置名不再把平台写死。
  - 可按平台区分不同索引。
- **Validation**:
  - 配置 accessor 与枚举选项测试更新。

### Task 4.3: 让 `todo` / `schedule` 先支持文本回执
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/todo/`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/schedule/`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/schedule_compat.go`
- **Description**: 服务本体继续复用，但出站与触发上下文改为通用 sender；QQ 先仅支持文本确认和文本通知。
- **Dependencies**: Sprint 3, Task 4.1
- **Acceptance Criteria**:
  - 不依赖 Lark 卡片也能完成基础创建/查询/提醒。
- **Validation**:
  - 新增 QQ 下的 `schedule`/`todo` 端到端用例。

## Sprint 5: 扩展可降级功能，明确长期不兼容项
**Goal**: 有选择地扩展 QQ 功能面，同时避免为了“看起来功能齐全”引入大量低质量兼容层。

**Demo/Validation**:
- 完成一轮能力分层文档，明确 QQ 可用、降级、不可用功能。

### Task 5.1: 逐项接入可文本降级的工具
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/music_handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/stock_handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/history_search_handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/word_handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/reply_handler.go`
- **Description**: 把结果渲染拆成 `text renderer` / `lark card renderer` 两层，QQ 先走 text renderer。
- **Dependencies**: Sprint 3, Sprint 4
- **Acceptance Criteria**:
  - 业务逻辑与展示逻辑分离。
  - QQ 端不会因为没有卡片协议而无法使用这些能力。
- **Validation**:
  - 按功能补充快照测试或字符串断言测试。

### Task 5.2: 明确长期 Lark-only 边界
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/docs/architecture/qqbot-compatibility-plan.md`
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/docs/architecture/platform-capability-matrix.md`
- **Description**: 记录哪些能力长期只保留在 Lark，例如 card regression、复杂审批卡、卡片 patch、reaction 跟随。
- **Dependencies**: Sprint 5
- **Acceptance Criteria**:
  - 产品与研发对平台差异有统一认知，不再默认“Lark 有的 QQ 都要有”。
- **Validation**:
  - 文档评审通过即可。

## Testing Strategy
- 为 `qqbot_dal`、QQ adapter、QQ webhook ingress 增加单元测试。
- 为通用消息模型、sender capability、命令解析增加 fake 平台测试。
- 为 QQ MVP 增加端到端 smoke test，至少覆盖：
  - 收到 C2C 文本消息
  - 执行 `/help`
  - 执行 `/config list`
  - 执行标准聊天
  - 主动 `send_message`
- Lark 现有关键用例必须继续回归，避免抽象层引入回归。

## Potential Risks & Gotchas
- `botgo` 官方 README 明确提到 websocket 事件链路将在 2024 年底后逐步下线，QQ 兼容应优先按 webhook 设计，而不是复刻 Lark websocket 模式。
- QQ webhook 部署需要回调地址与 IP 白名单，开发/测试环境连通性经常是首个阻塞点。
- 当前身份模型以 `OpenID` 为核心，若直接把 QQ 用户 ID 塞进 `OpenID` 语义，会污染权限、配置和任务归属。
- 当前大量 handler 把展示层和业务层揉在一起；若不先拆 renderer，就会被 Lark card API 长期绑死。
- `agentruntime`、approval、cardaction 这条链路现在不适合和 QQ MVP 一起做。
- 若消息记录模型要变更，必须按数据库变更 SOP 先落 SQL，再生成 model/query。

## Rollback Plan
- QQBot 入口保持为独立 `cmd/qqbot`，不影响现有 `cmd/larkrobot`。
- 新抽象层先以新增文件和包装接入，避免大面积替换旧 Lark 代码。
- 任何泛化字段或索引改造都先做兼容读取，确认回归后再清理旧命名。

## Sources
- QQ 官方 Go SDK `botgo` README: webhook 回调、token source、OpenAPI 初始化、`C2C_MESSAGE_CREATE` 示例  
  https://github.com/tencent-connect/botgo
- 仓库现状代码入口与运行时：
  - `/mnt/RapidPool/workspace/BetaGo_v2/cmd/larkrobot/bootstrap.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/runtime/lark_ws.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/interfaces/lark/handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/schedule_compat.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/config/configs.go`

# Plan: 基于 xcommand 与 xhandler 的 QQBot 最小兼容方案

**Generated**: 2026-03-19
**Estimated Complexity**: Medium

## Overview
目标不是先把仓库重构成完整多平台架构，而是利用现有 `pkg/xcommand` 和 `pkg/xhandler` 的泛型设计，尽快做出一个“能收 QQ 文本消息、能跑最小命令集、能做基础文本聊天回复”的 QQBot MVP。

这个方案刻意避免两件事：
- 不先抽完整 `botcore` / `platform abstraction`。
- 不尝试把现有 Lark handler 直接泛化到所有平台。

推荐路径是：
1. 为 QQ 单独新增入口、回调与文本发送能力。
2. 基于 `xhandler.Processor[T, K]` 新建一套 QQ 消息流水线。
3. 基于 `xcommand.Command[T]` 新建一套 QQ 命令树，但只接入少量最关键命令。
4. 只把必要的业务逻辑从 Lark handler 中抽到共享 service/helper，避免大面积重构。

这是一个“先跑通，再收口”的方案。它的核心不是平台抽象的完美，而是复用仓库里已经成熟的泛型执行框架。

## Assumptions
- 第一阶段只支持 QQ `C2C_MESSAGE_CREATE`。
- 第一阶段只做文本消息，不做卡片、按钮回调、reaction、撤回、流式卡片。
- 第一阶段 QQ 与 Lark 分别保留各自入口，不合并成单二进制。
- 第一阶段只兼容最小命令集和标准文本聊天，不接 `agentruntime`。

## Why This Direction

### 适合直接复用的部分
- [`pkg/xhandler/base.go`](/mnt/RapidPool/workspace/BetaGo_v2/pkg/xhandler/base.go)：`Processor[T, K]`、`Operator[T, K]`、`BaseMetaData` 已经是泛型。
- [`pkg/xcommand/base.go`](/mnt/RapidPool/workspace/BetaGo_v2/pkg/xcommand/base.go)：`Command[T]` 已经是泛型。
- [`pkg/xcommand/typed.go`](/mnt/RapidPool/workspace/BetaGo_v2/pkg/xcommand/typed.go)：`BindCLI`、`RegisterTool`、typed args 流程已经和平台数据类型解耦。
- [`internal/runtime/app.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/runtime/app.go)：运行时生命周期可直接复用。

### 当前不值得先动的部分
- [`internal/application/lark/handlers/*.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers)：大量 handler 直接依赖 `*larkim.P2MessageReceiveV1` 和 `larkmsg.*`。
- [`internal/application/lark/messages/handler.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/handler.go)：现有消息流水线整体是 Lark event 专用。
- [`internal/infrastructure/lark_dal/larkmsg/*`](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/lark_dal/larkmsg)：发送、卡片、patch、reaction、mention 语义均为 Lark 专属。

结论是：泛型框架可复用，但当前业务 handler 不能直接“无痛跨平台”。因此应复用框架，少量抽业务，不做一轮全局泛化。

## Minimal Compatible Scope

### P0 必做
- QQBot 配置与独立启动入口
- webhook 回调接入
- QQ 文本消息接收
- 文本发送/回复
- QQ 消息处理流水线
- QQ 命令树
- `/help`
- `/bb` 或等价文本聊天命令
- `/config list`
- `/feature list`
- `/mute`
- `send_message`

### P1 可选
- `/oneword`
- `/stock gold`
- `/stock zh_a`
- `/music` 的纯文本结果版

### 明确不做
- card action
- permission card
- schedule 管理卡
- agentic streaming card
- reaction
- 图片素材库管理
- 消息撤回/伪撤回

## Prerequisites
- 增加 `botgo` 依赖。
- 准备 QQ `app_id`、`app_secret`、回调地址与事件订阅。
- 接受第一阶段 QQ 回复以纯文本为主。

## Sprint 1: 新增 QQBot 入口与文本收发
**Goal**: 先把 QQBot 服务跑起来，并具备“收一条文本、回一条文本”的最小能力。

**Demo/Validation**:
- `cmd/qqbot` 可启动。
- 接收到 QQ C2C 消息后可回复固定文本。
- health 接口正常。

### Task 1.1: 增加 QQ 配置
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/config/configs.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml.example`
- **Description**: 新增 `qqbot_config` 与 webhook 监听配置。
- **Dependencies**: 无
- **Acceptance Criteria**:
  - QQ 配置独立于 `lark_config`。
  - 配置缺失时 QQ 模块显式 disabled。
- **Validation**:
  - 配置加载单测。

### Task 1.2: 增加 QQ DAL
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/qqbot_dal/client.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/qqbot_dal/outbound.go`
- **Description**: 封装 `token.NewQQBotTokenSource`、`token.StartRefreshAccessToken`、`botgo.NewOpenAPI`，并提供最小文本发送 API。
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - 业务层不直接散落调用 `botgo` SDK。
  - 文本发送方法可单独测试。
- **Validation**:
  - fake client / adapter 单测。

### Task 1.3: 新增 QQ webhook 模块
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/runtime/qq_webhook.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/interfaces/qqbot/handler.go`
- **Description**: 基于 `webhook.HTTPHandler` 将 QQ 回调接入运行时模块体系。
- **Dependencies**: Task 1.2
- **Acceptance Criteria**:
  - webhook 入口挂入 `App` 生命周期。
  - 支持 `C2C_MESSAGE_CREATE`。
- **Validation**:
  - 伪请求或集成测试验证事件回调执行。

### Task 1.4: 新增独立启动入口
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/cmd/qqbot/main.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/cmd/qqbot/bootstrap.go`
- **Description**: 新建 QQBot 主程序，复用现有 runtime、DB、Redis、Ark 初始化流程。
- **Dependencies**: Task 1.1, Task 1.2, Task 1.3
- **Acceptance Criteria**:
  - 能独立启动和关闭。
  - 不影响 `cmd/larkrobot`。
- **Validation**:
  - smoke test。

## Sprint 2: 用 xhandler 建一条 QQ 消息流水线
**Goal**: 不复用 Lark message processor，而是基于同一个泛型框架新建 QQ 版 processor。

**Demo/Validation**:
- QQ 消息进入后，能经过 `Processor -> Operator` 链路。
- `BaseMetaData` 在 QQ 下可正确注入。

### Task 2.1: 定义 QQ 事件上下文
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/model.go`
- **Description**: 定义一个轻量上下文，例如 `MessageEvent`，包装 `dto.WSC2CMessageData` 并补充：
  - `Text`
  - `AuthorID`
  - `ChatID`
  - `MessageID`
  - `IsDirect`
- **Dependencies**: Sprint 1
- **Acceptance Criteria**:
  - 下游 operator 不必直接依赖 SDK 原始结构的所有细节。
  - 但也不引入完整通用平台抽象。
- **Validation**:
  - adapter 单测。

### Task 2.2: 建立 QQ `Processor`
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/handler.go`
- **Description**: 基于 `xhandler.Processor[qqMessageEvent, xhandler.BaseMetaData]` 搭一条最小流水线。
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - 支持 `OnPanic`、`WithMetaDataProcess`、`WithFeatureChecker`。
  - 先只挂少量 operator。
- **Validation**:
  - 单测验证 metadata 初始化和 operator 调用顺序。

### Task 2.3: 实现最小 operator 集
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/ops/common.go`
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/ops/command_op.go`
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/ops/chat_op.go`
- **Description**: 第一阶段只做两个 operator：
  - 命令消息处理
  - 普通聊天处理
- **Dependencies**: Task 2.2
- **Acceptance Criteria**:
  - 命令与普通聊天的分流规则清晰。
  - 不复制 Lark 全套消息 operator。
- **Validation**:
  - 表驱动测试覆盖命令消息/普通消息。

## Sprint 3: 用 xcommand 建 QQ 命令树
**Goal**: 复用 typed args、命令分发和帮助生成能力，但不要求 QQ 命令树与 Lark 完全一致。

**Demo/Validation**:
- QQ 支持 `/help` 和至少 3 个真实命令。
- `ParseCLI -> Handle` typed 流程在 QQ 下可用。

### Task 3.1: 定义 QQ 命令根节点
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/command/command.go`
- **Description**: 基于 `xcommand.Command[*qqbotmessages.MessageEvent]` 新建 `QQRootCommand`。
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 不直接复用 `LarkRootCommand`。
  - 命令树规模控制在 MVP 必需范围。
- **Validation**:
  - 命令解析单测。

### Task 3.2: 接入 `/help`
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/command/help.go`
- **Description**: 复用 `xcommand` 的 usage 生成能力，为 QQ 输出纯文本帮助。
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - `help` 无需卡片也能读。
- **Validation**:
  - 快照测试。

### Task 3.3: 接入最小命令集
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/command/command.go`
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/handlers/*.go`
- **Description**: 第一阶段只接：
  - `config list`
  - `feature list`
  - `mute`
  - `send_message`
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - 每个命令都走 typed args + `BindCLI`。
  - 没有 Lark 卡片依赖。
- **Validation**:
  - 命令处理单测。

## Sprint 4: 从 Lark handler 中抽最小共享业务
**Goal**: 不重构全部 handler，只把 QQ MVP 需要的业务逻辑抽成共享 service/helper。

**Demo/Validation**:
- QQ handler 主要负责参数和文本输出，业务逻辑来自共享 helper。

### Task 4.1: 抽配置与功能开关查询
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/shared/configsvc/`
  - 参考 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/config_handler.go`
- **Description**: 把 `config list`、`feature list` 相关核心查询逻辑从 Lark 输出逻辑中剥离。
- **Dependencies**: Sprint 3
- **Acceptance Criteria**:
  - 共享层不依赖 `larkmsg`。
  - Lark 旧 handler 可逐步改为调用共享层。
- **Validation**:
  - service 单测。

### Task 4.2: 抽禁言与主动发消息
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/shared/messagesvc/`
  - 参考 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/mute_handler.go`
  - 参考 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/send_message_handler.go`
- **Description**: 把“写配置/设置状态/组装文本结果”的业务逻辑收口到共享层。
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - QQ 与 Lark 都可调用。
  - 平台差异只留在出站层。
- **Validation**:
  - service 单测。

### Task 4.3: 抽标准文本聊天
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/shared/chatsvc/`
  - 参考 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/chat_handler.go`
- **Description**: 只抽标准文本聊天生成逻辑，不抽 agentic card 路径。
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - QQ 可复用 Ark 调用与 prompt 组装。
  - 不引入 Lark message/card API 依赖。
- **Validation**:
  - 聊天生成单测或 smoke test。

## Sprint 5: 跑通 QQ MVP 并补回归测试
**Goal**: 完成一个可演示、可测试、可继续迭代的 QQBot 最小兼容版本。

**Demo/Validation**:
- QQ 用户可发送：
  - `/help`
  - `/config list`
  - `/feature list`
  - `/mute`
  - 普通聊天文本
- 机器人均能正确回复纯文本。

### Task 5.1: 端到端串联
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/interfaces/qqbot/handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/command/command.go`
- **Description**: 串起 webhook -> adapter -> processor -> operator -> command/chat -> outbound。
- **Dependencies**: Sprint 1-4
- **Acceptance Criteria**:
  - 主链路可用。
- **Validation**:
  - 集成 smoke test。

### Task 5.2: 补最关键回归测试
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/.../*_test.go`
  - 适量补充 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/.../*_test.go`
- **Description**: 覆盖最关键路径：
  - 命令解析
  - 文本发送
  - metadata 初始化
  - operator 分流
  - 聊天回复
- **Dependencies**: Task 5.1
- **Acceptance Criteria**:
  - 新增 QQ 路径有基础测试网。
  - 不引入 Lark 关键回归。
- **Validation**:
  - `go test` 跑通对应包。

## Testing Strategy
- 单元测试：
  - QQ webhook 入口
  - QQ event adapter
  - `xhandler` QQ processor 组装
  - `xcommand` QQ 命令树
  - 共享 service
- 集成测试：
  - QQ 消息进入后固定文本回复
  - `/help`
  - `/config list`
  - 标准文本聊天
- 回归测试：
  - Lark 现有关键 handler 不因共享 service 抽取而回归

## Potential Risks & Gotchas
- 如果试图让 QQ 直接复用现有 `internal/application/lark/handlers`，会很快陷入 `larkmsg.*` 依赖泥潭。
- `BaseMetaData` 里当前字段名仍偏 Lark，例如 `OpenID`、`ChatID`，QQ 第一阶段可以“借壳使用”，但不要把它误当成长期抽象终态。
- 第一阶段不要把 `agentruntime`、approval、card action 纳入范围，否则复杂度会立刻失控。
- 共享 service 抽取要极度克制，只抽 QQ MVP 真正需要的逻辑，不做“顺手大重构”。

## Rollback Plan
- 新功能全部落在 `cmd/qqbot`、`internal/application/qqbot`、`internal/infrastructure/qqbot_dal` 等新路径。
- Lark 原链路保持不动，仅允许少量 handler 调用共享 service。
- 任何共享逻辑抽取都要先保留 Lark 原行为的测试，再切换调用。

## Suggested First Implementation Slice
如果按最小可运行顺序推进，建议先做这 6 个原子任务：

1. 新增 `qqbot_config` 与 `cmd/qqbot`
2. 封装 `qqbot_dal` 文本发送
3. 建立 QQ webhook handler
4. 建立 QQ `xhandler.Processor`
5. 建立 QQ `xcommand` 根命令并接入 `/help`
6. 接入标准文本聊天和 `/config list`

完成这 6 步后，就已经有一个真正能演示的 QQBot MVP 了。

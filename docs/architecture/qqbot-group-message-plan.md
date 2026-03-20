# Plan: QQBot 群消息兼容接入

**Generated**: 2026-03-19
**Estimated Complexity**: Medium
**Target Phase**: Phase 1 MVP

## Overview
本计划的目标是为当前仓库补齐一个 **QQBot 群 @ 机器人消息** 的最小可用版本，并尽量复用现有的泛型能力：

- 复用 `pkg/xhandler` 作为消息处理流水线
- 复用 `pkg/xcommand` 作为 typed command 执行框架
- 复用 `internal/runtime` 作为生命周期管理框架
- 仅抽取少量共享业务逻辑，不先启动完整多平台重构

当前仓库是明确的 Lark 单平台结构。问题不在于“缺少框架”，而在于业务 handler 和出站层大量绑定了 `larkim.*` 与 `larkmsg.*`。因此，最稳妥的实现路径不是把 QQ 直接塞进现有 Lark handler，而是：

1. 为 QQ 新增独立入口与独立消息链路
2. 让 QQ 链路复用 `xhandler` 和 `xcommand`
3. 只把 QQ MVP 必需的业务逻辑从 Lark handler 中抽出来
4. 所有复杂富交互能力继续保留为 Lark-only

## Goal
交付一个可运行的 QQ 群消息机器人，支持：

- 群里 `@机器人` 触发
- 纯文本命令处理
- 纯文本聊天回复
- 最小管理命令
- 基础可观测性与回滚路径

## Non-Goals
本阶段明确不做：

- C2C 私聊
- QQ 频道消息
- 卡片消息与卡片回调
- reaction
- 消息撤回/伪撤回
- image key / 卡片 patch / 审批卡
- `agentruntime` / `approval` / `streaming card`
- 全量迁移 Lark 命令树
- 完整多平台统一领域模型

## Current Findings

### 可直接复用
- [`pkg/xhandler/base.go`](/mnt/RapidPool/workspace/BetaGo_v2/pkg/xhandler/base.go)
- [`pkg/xcommand/base.go`](/mnt/RapidPool/workspace/BetaGo_v2/pkg/xcommand/base.go)
- [`pkg/xcommand/typed.go`](/mnt/RapidPool/workspace/BetaGo_v2/pkg/xcommand/typed.go)
- [`internal/runtime/app.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/runtime/app.go)
- [`internal/runtime/module.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/runtime/module.go)

### 不应直接复用
- [`internal/application/lark/messages/handler.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/handler.go)
- [`internal/application/lark/command/command.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/command/command.go)
- [`internal/application/lark/handlers`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers)
- [`internal/infrastructure/lark_dal/larkmsg`](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/lark_dal/larkmsg)

### 核心判断
- 泛型框架已经够用
- 平台接入层需要新增
- 业务逻辑只能“少量抽取”，不能“整层复用”

## External API Notes
根据 `botgo` 官方 README 与示例：

- webhook 是推荐链路
- 群消息第一阶段应使用 `GroupATMessageEventHandler`
- 对应数据类型是 `dto.WSGroupATMessageData`
- token 生命周期使用 `token.NewQQBotTokenSource(...)` 与 `token.StartRefreshAccessToken(...)`
- OpenAPI 客户端使用 `botgo.NewOpenAPI(...)`

Context7 没有可用的 `botgo` 库索引，因此本计划以官方 GitHub README 和示例源码为准。

## Architecture

### 运行结构
- 新增 `cmd/qqbot`
- 新增 `internal/infrastructure/qqbot_dal`
- 新增 `internal/interfaces/qqbot`
- 新增 `internal/application/qqbot`
- 保留 `cmd/larkrobot` 和 `internal/application/lark` 原样

### 数据流
1. QQ webhook 回调进入 `internal/interfaces/qqbot`
2. 接口层把 `dto.WSGroupATMessageData` 转换为 QQ 侧轻量事件结构
3. 交给 `xhandler.Processor` 驱动的 QQ 消息流水线
4. 流水线根据规则分流到：
   - 命令 operator
   - 标准聊天 operator
5. 通过 `qqbot_dal` 发送文本消息

### 关键约束
- `BaseMetaData.ChatID` 先借用 `GroupID`
- `BaseMetaData.OpenID` 先借用 QQ 用户标识
- 这是阶段性映射，不是长期平台抽象

## Scope Matrix

### P0
- 群 @ 消息接入
- 文本发送/回复
- QQ message processor
- QQ root command
- `/help`
- `/config list`
- `/feature list`
- `/mute`
- `/send_message`
- 标准文本聊天

### P1
- `/oneword`
- `/stock gold`
- `/stock zh_a`
- `/music` 纯文本列表

### P2
- todo / schedule 文本版
- history search 文本版

### Lark-Only
- card action
- approval card
- streaming card
- reaction
- 图片素材管理
- 卡片调试与回归

## Prerequisites
- 配置 QQ 机器人应用
- 配置回调地址
- 配置固定公网出口 IP 白名单
- 勾选群 @ 机器人事件
- 接受 Phase 1 仅文本回复

## Sprint 1: Runtime And Ingress
**Goal**: 启动独立 QQBot 进程，并能接入群 @ 消息。

**Demo/Validation**:
- `cmd/qqbot` 可启动
- 管理面健康检查正常
- 能接收群 @ 机器人 webhook

### Task 1.1: Add QQ config
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/config/configs.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml.example`
- **Description**: 新增 `qqbot_config` 和 `qqbot_webhook_config`
- **Dependencies**: None
- **Acceptance Criteria**:
  - 与 `lark_config` 并列
  - 可通过空配置显式 disabled
- **Validation**:
  - 配置加载单测

### Task 1.2: Add QQ DAL bootstrap
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/qqbot_dal/client.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/qqbot_dal/token.go`
- **Description**: 封装 token source、自动刷新、OpenAPI client 初始化
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - DAL 提供统一 `Client()` / `API()` 获取方式
  - 可报告是否可用
- **Validation**:
  - 单测覆盖 nil/empty config

### Task 1.3: Add QQ outbound text sender
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/qqbot_dal/message.go`
- **Description**: 封装群消息文本发送与回复 helper
- **Dependencies**: Task 1.2
- **Acceptance Criteria**:
  - 业务层不直接操作 SDK 原始请求结构
  - 支持“按 group_id 发文本”
- **Validation**:
  - fake sender 单测

### Task 1.4: Add QQ webhook runtime module
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/runtime/qq_webhook.go`
- **Description**: 按照 `internal/runtime` 风格新增 QQ webhook module
- **Dependencies**: Task 1.2
- **Acceptance Criteria**:
  - 纳入 `App` 生命周期
  - 支持独立 ready / stop
- **Validation**:
  - module 初始化测试

### Task 1.5: Add QQ interface handler set
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/interfaces/qqbot/handler.go`
- **Description**: 注册 `event.GroupATMessageEventHandler`，接管 `dto.WSGroupATMessageData`
- **Dependencies**: Task 1.4
- **Acceptance Criteria**:
  - 只消费群 @ 机器人消息
  - 不处理 C2C / 频道
- **Validation**:
  - handler 接线测试

### Task 1.6: Add `cmd/qqbot`
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/cmd/qqbot/main.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/cmd/qqbot/bootstrap.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/cmd/qqbot/config.go`
- **Description**: 新建 QQBot 主程序并接入 runtime 模块装配
- **Dependencies**: Task 1.1, Task 1.2, Task 1.4, Task 1.5
- **Acceptance Criteria**:
  - 可独立启动
  - 不影响 `cmd/larkrobot`
- **Validation**:
  - 本地 smoke test

## Sprint 2: QQ Message Pipeline
**Goal**: 用 `xhandler` 建立 QQ 群消息处理主链路。

**Demo/Validation**:
- 群消息进入后经过 QQ processor
- metadata 正确注入

### Task 2.1: Define QQ message event wrapper
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/model.go`
- **Description**: 定义 `MessageEvent`，包装群消息原始数据并补充规范字段
- **Dependencies**: Sprint 1
- **Acceptance Criteria**:
  - 至少包含 `GroupID`、`AuthorID`、`MessageID`、`RawContent`、`Text`
  - 支持从原始消息中去除 @ 内容
- **Validation**:
  - adapter 单测

### Task 2.2: Define QQ message parsing helpers
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/parse.go`
- **Description**: 把群消息内容清洗为可用于命令/聊天的纯文本
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - 统一消息文本入口
  - 与 operator 解耦
- **Validation**:
  - 文本清洗测试

### Task 2.3: Create QQ processor
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/handler.go`
- **Description**: 基于 `xhandler.Processor[MessageEvent, xhandler.BaseMetaData]` 组装 QQ 流水线
- **Dependencies**: Task 2.1, Task 2.2
- **Acceptance Criteria**:
  - 支持 `OnPanic`
  - 支持 `WithMetaDataProcess`
  - 支持 `WithFeatureChecker`
- **Validation**:
  - handler 单测

### Task 2.4: Add command/chat operators
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/ops/common.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/ops/command_op.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/ops/chat_op.go`
- **Description**: 建立最小 operator 集，只做命令和标准聊天
- **Dependencies**: Task 2.3
- **Acceptance Criteria**:
  - 规则简单明确：`/` 开头是命令，否则走聊天
  - 不复制 Lark 的 repeat / react / card 等 operator
- **Validation**:
  - 表驱动测试

## Sprint 3: QQ Command Tree
**Goal**: 用 `xcommand` 建立 QQ 版命令系统。

**Demo/Validation**:
- 群里 @ 后输入 `/help`
- 帮助可正确展示

### Task 3.1: Create QQ root command
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/command/command.go`
- **Description**: 基于 `xcommand.Command[*messages.MessageEvent]` 创建 QQ 根命令
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 不复用 `LarkRootCommand`
  - 命令树仅包含 MVP 所需命令
- **Validation**:
  - root command 单测

### Task 3.2: Add help command
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/command/help.go`
- **Description**: 输出 QQ 纯文本帮助
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - 纯文本可读
  - 使用 `xcommand` 的 usage 能力
- **Validation**:
  - 快照测试

### Task 3.3: Add MVP command handlers
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/handlers/config_handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/handlers/feature_handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/handlers/mute_handler.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/handlers/send_message_handler.go`
- **Description**: 接入最小命令集
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - 使用 typed args + `BindCLI`
  - 输出纯文本
- **Validation**:
  - 命令处理测试

## Sprint 4: Shared Business Extraction
**Goal**: 把 QQ MVP 真正需要的业务逻辑从 Lark handler 中剥出来。

**Demo/Validation**:
- QQ handler 主要只负责参数绑定和文本输出

### Task 4.1: Extract config listing service
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/shared/configsvc/service.go`
- **Description**: 抽 `config list` 的核心查询逻辑
- **Dependencies**: Sprint 3
- **Acceptance Criteria**:
  - 不依赖 `larkmsg`
  - Lark 未来也可调用
- **Validation**:
  - service 单测

### Task 4.2: Extract feature listing service
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/shared/featuresvc/service.go`
- **Description**: 抽 `feature list` 核心逻辑
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - 共享层不依赖平台输出
- **Validation**:
  - service 单测

### Task 4.3: Extract mute service
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/shared/mutesvc/service.go`
- **Description**: 抽 `mute` 设置与结果文案拼装逻辑
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - QQ 和 Lark 都可以调用
- **Validation**:
  - service 单测

### Task 4.4: Extract send-message service
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/shared/messagesvc/service.go`
- **Description**: 抽主动发消息的参数和业务边界
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - 平台差异只留在 outbound 层
- **Validation**:
  - service 单测

## Sprint 5: Standard Chat
**Goal**: 接通标准文本聊天。

**Demo/Validation**:
- 群里 @ 机器人发送普通问题
- 机器人文本回复

### Task 5.1: Extract standard chat service
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/shared/chatsvc/service.go`
- **Description**: 从现有 Lark 聊天链路中抽出标准文本聊天生成逻辑
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 不引入 `larkmsg`
  - 不引入 agentic card 逻辑
- **Validation**:
  - service 测试

### Task 5.2: Wire QQ chat operator
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/ops/chat_op.go`
- **Description**: 在普通消息路径上调用共享聊天服务并通过 QQ sender 回复
- **Dependencies**: Task 5.1
- **Acceptance Criteria**:
  - 不触发 agent runtime
  - 以纯文本回复为准
- **Validation**:
  - chat smoke test

## Sprint 6: Verification And Rollout
**Goal**: 给 MVP 建立最基本的验证、观测和上线策略。

**Demo/Validation**:
- 关键路径均有测试
- 上线和回滚动作明确

### Task 6.1: Add package-level tests
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/.../*_test.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/qqbot_dal/.../*_test.go`
- **Description**: 为 ingress、adapter、processor、command、shared services 增加测试
- **Dependencies**: Sprint 1-5
- **Acceptance Criteria**:
  - 覆盖主链路
- **Validation**:
  - `go test` 对应包通过

### Task 6.2: Add smoke commands
- **Location**:
  - 文档与手工回归脚本
- **Description**: 形成一组最小人工回归步骤
- **Dependencies**: Task 6.1
- **Acceptance Criteria**:
  - 至少覆盖：
    - 群 @ 固定文本回复
    - `/help`
    - `/config list`
    - `/feature list`
    - `/mute`
    - 普通文本聊天
- **Validation**:
  - 手工回归

### Task 6.3: Add rollout checklist
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/docs/architecture/qqbot-group-message-plan.md`
- **Description**: 记录部署、白名单、事件勾选、回滚步骤
- **Dependencies**: Task 6.2
- **Acceptance Criteria**:
  - 运维动作明确
- **Validation**:
  - checklist review

## Testing Strategy
- 单元测试：
  - QQ config
  - QQ DAL
  - group message adapter
  - QQ processor
  - QQ root command
  - shared services
- 集成测试：
  - group @ message ingress
  - fixed text reply
  - `/help`
  - `/config list`
  - standard chat
- 回归测试：
  - Lark 现有关键包至少 smoke 通过

## Acceptance Checklist
- `cmd/qqbot` 可独立启动
- webhook 可接群 @ 机器人消息
- 机器人可回复文本
- `/help` 可用
- `/config list` 可用
- `/feature list` 可用
- `/mute` 可用
- 标准文本聊天可用
- 不影响现有 Lark 入口

## Potential Risks & Gotchas
- `BaseMetaData.ChatID/OpenID` 只是阶段性映射，不应被误当成最终平台抽象。
- 如果一开始就尝试兼容 Lark 全命令树，范围会迅速膨胀。
- 现有 Lark handler 中展示层和业务层耦合严重，抽 service 时要非常克制。
- QQ webhook 的公网出口 IP 白名单是首个常见阻塞点。
- 不要在 Phase 1 引入 `agentruntime`、approval、card action。

## Deployment Notes
- 新建单独进程 `cmd/qqbot`
- 先在测试群或沙箱群验证
- 先仅开放群 @ 机器人事件
- 监控 webhook 可达性、OpenAPI 调用错误、回复成功率

## Rollback Plan
- 停掉 `cmd/qqbot` 进程即可回滚
- QQ 相关代码全部落在新路径，不影响 `cmd/larkrobot`
- 共享 service 抽取必须保留 Lark 旧路径可工作

## Suggested Implementation Order
建议按这 8 个提交块推进：

1. QQ config + `cmd/qqbot`
2. `qqbot_dal` token/client/outbound
3. QQ webhook module + interface handler
4. QQ message event wrapper + parser
5. QQ `xhandler` processor + command/chat operators
6. QQ `xcommand` root + `/help`
7. shared `config/feature/mute/send_message` services
8. shared standard chat + QQ chat operator

## Sources
- BotGo README  
  https://github.com/tencent-connect/botgo
- BotGo `event/register.go`  
  https://github.com/tencent-connect/botgo
- BotGo `examples/receive-and-send/main.go`  
  https://github.com/tencent-connect/botgo/tree/master/examples/receive-and-send
- Current repo entrypoints and runtime:
  - [`cmd/larkrobot/bootstrap.go`](/mnt/RapidPool/workspace/BetaGo_v2/cmd/larkrobot/bootstrap.go)
  - [`internal/runtime/app.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/runtime/app.go)
  - [`internal/application/lark/messages/handler.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/handler.go)
  - [`internal/application/lark/command/command.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/command/command.go)

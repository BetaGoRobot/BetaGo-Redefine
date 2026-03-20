# Plan: QQBot 群消息最小兼容方案

**Generated**: 2026-03-19
**Estimated Complexity**: Medium

## Overview
第一阶段目标进一步收紧为：**只做 QQ 群消息，不做 C2C，不做频道，不做私聊**。

结合仓库现状和 `botgo` 官方示例，最合适的做法是：

1. 新增独立 `cmd/qqbot` 入口。
2. 使用 `botgo` 的 `GroupATMessageEventHandler` + `dto.WSGroupATMessageData` 接收群 @ 机器人消息。
3. 基于 `pkg/xhandler` 新建一条 QQ 群消息 processor。
4. 基于 `pkg/xcommand` 新建一套 QQ 群消息命令树。
5. 只兼容纯文本命令和纯文本聊天，不碰卡片、reaction、审批流。

这个方案的重点不是“先统一平台模型”，而是用最少改动把 QQ 群消息主链路跑通。

## Scope

### 本阶段只做
- QQ 群 @ 机器人消息接收
- 文本命令解析
- 纯文本回复
- 标准文本聊天
- 最小管理命令

### 本阶段不做
- C2C 私聊
- 频道消息
- 卡片回调
- reaction
- 图片素材库
- schedule 管理卡
- agent runtime / approval / streaming card

## Technical Direction

### 直接复用
- [`pkg/xhandler/base.go`](/mnt/RapidPool/workspace/BetaGo_v2/pkg/xhandler/base.go)
- [`pkg/xcommand/base.go`](/mnt/RapidPool/workspace/BetaGo_v2/pkg/xcommand/base.go)
- [`pkg/xcommand/typed.go`](/mnt/RapidPool/workspace/BetaGo_v2/pkg/xcommand/typed.go)
- [`internal/runtime/app.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/runtime/app.go)

### 不直接复用
- [`internal/application/lark/messages/handler.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/messages/handler.go)
- [`internal/application/lark/command/command.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/command/command.go)
- [`internal/application/lark/handlers/*.go`](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers)
- [`internal/infrastructure/lark_dal/larkmsg/*`](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/lark_dal/larkmsg)

原因很简单：泛型骨架是通用的，但 Lark 消息链路和 handler 已经深度绑定 `larkim.*` 与 `larkmsg.*`，直接复用只会让实现变脏。

## Prerequisites
- 增加 `github.com/tencent-connect/botgo` 依赖。
- 配置 QQ 机器人回调地址和 IP 白名单。
- 勾选群 @ 机器人事件。

## Sprint 1: 跑通群消息接入
**Goal**: 新增 QQBot 服务，收到群 @ 消息后可回复固定文本。

**Demo/Validation**:
- 启动 `cmd/qqbot`
- 群里 @ 机器人发送文本
- 机器人回一条固定文本

### Task 1.1: 增加 QQ 配置
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/config/configs.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml.example`
- **Description**: 增加 `qqbot_config` 和 webhook 配置。
- **Dependencies**: 无
- **Acceptance Criteria**:
  - 配置独立于 `lark_config`
  - 缺失配置时 QQ 模块可 disabled
- **Validation**:
  - 配置加载测试

### Task 1.2: 封装 QQ DAL
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/qqbot_dal/client.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/qqbot_dal/message.go`
- **Description**: 封装 `token.NewQQBotTokenSource`、`token.StartRefreshAccessToken`、`botgo.NewOpenAPI` 与群文本发送。
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - 业务层不直接裸用 SDK
  - 群文本发送有单独 helper
- **Validation**:
  - fake client 或 adapter 测试

### Task 1.3: 新增 webhook ingress
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/runtime/qq_webhook.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/interfaces/qqbot/handler.go`
- **Description**: 用 `webhook.HTTPHandler` 接回调，并注册 `event.GroupATMessageEventHandler`。
- **Dependencies**: Task 1.2
- **Acceptance Criteria**:
  - 只订阅群 @ 消息
  - 回调模块纳入 `App` 生命周期
- **Validation**:
  - 伪请求或集成 smoke test

### Task 1.4: 新增独立入口
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/cmd/qqbot/main.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/cmd/qqbot/bootstrap.go`
- **Description**: 新建 QQBot 主程序，复用 runtime、DB、Redis、Ark 初始化。
- **Dependencies**: Task 1.1, Task 1.2, Task 1.3
- **Acceptance Criteria**:
  - 能独立启动
  - 不影响 `cmd/larkrobot`
- **Validation**:
  - 启动 smoke test

## Sprint 2: 建立 QQ 群消息 processor
**Goal**: 用 `xhandler` 建立一条 QQ 群消息专用流水线。

**Demo/Validation**:
- 群消息进入后经过 processor
- metadata 正确填充

### Task 2.1: 定义 QQ 群消息模型
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/model.go`
- **Description**: 基于 `dto.WSGroupATMessageData` 封装一个轻量事件结构，至少包含：
  - `GroupID`
  - `AuthorID`
  - `MessageID`
  - `Content`
  - `CleanText`
- **Dependencies**: Sprint 1
- **Acceptance Criteria**:
  - 下游不需要反复直接解析 SDK 原始结构
- **Validation**:
  - adapter 单测

### Task 2.2: 新建 QQ `Processor`
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/handler.go`
- **Description**: 基于 `xhandler.Processor[MessageEvent, xhandler.BaseMetaData]` 建立 QQ 消息处理器。
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - `ChatID` 暂借用 `GroupID`
  - `OpenID` 暂借用 QQ 用户 ID 字段
  - 先只挂最少 operator
- **Validation**:
  - metadata 初始化测试

### Task 2.3: 最小 operator 集
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/ops/common.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/ops/command_op.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/ops/chat_op.go`
- **Description**: 只做命令和普通聊天两个 operator。
- **Dependencies**: Task 2.2
- **Acceptance Criteria**:
  - 明确规则：以 `/` 开头进命令，否则进聊天
- **Validation**:
  - 表驱动测试

## Sprint 3: 建立 QQ 群消息命令树
**Goal**: 用 `xcommand` 支撑 QQ 群消息命令处理。

**Demo/Validation**:
- 群里 @ 机器人后发送 `/help`
- 机器人返回纯文本帮助

### Task 3.1: 新建 QQ RootCommand
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/command/command.go`
- **Description**: 基于 `xcommand.Command[*qqmessages.MessageEvent]` 建立 `QQRootCommand`。
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 不直接复用 `LarkRootCommand`
  - 只注册 MVP 命令
- **Validation**:
  - 命令解析测试

### Task 3.2: 接入 `/help`
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/command/help.go`
- **Description**: 基于 `xcommand` usage 输出纯文本帮助。
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - 不依赖卡片
- **Validation**:
  - 快照测试

### Task 3.3: 接入最小命令集
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/command/command.go`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/handlers/*.go`
- **Description**: 第一阶段只接：
  - `config list`
  - `feature list`
  - `mute`
  - `send_message`
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - 都走 typed args + `BindCLI`
  - 输出纯文本
- **Validation**:
  - 命令处理测试

## Sprint 4: 接通标准文本聊天
**Goal**: 群 @ 机器人发普通文本时，可以走 Ark 标准聊天并文本回复。

**Demo/Validation**:
- 群 @ 机器人发普通问题
- 机器人文本回复

### Task 4.1: 抽标准聊天共享逻辑
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/shared/chatsvc/`
  - 参考 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/chat_handler.go`
- **Description**: 只抽标准文本聊天，不抽 agentic card 路径。
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - QQ 复用 Ark 调用逻辑
  - 不依赖 `larkmsg`
- **Validation**:
  - service 测试

### Task 4.2: 接入 QQ 聊天 operator
- **Location**:
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot/messages/ops/chat_op.go`
- **Description**: 在普通聊天路径调用共享聊天服务，并通过 QQ outbound 回文本。
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - 群消息聊天可用
  - 不启用 agent runtime
- **Validation**:
  - 聊天 smoke test

## Sprint 5: 抽最小共享管理能力
**Goal**: 不大重构，只抽 QQ MVP 真正需要的业务逻辑。

**Demo/Validation**:
- `config list`、`feature list`、`mute` 在 QQ 群里可用

### Task 5.1: 抽配置查询服务
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/shared/configsvc/`
  - 参考 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/config_handler.go`
- **Description**: 抽 `config list` 和 `feature list` 需要的核心查询逻辑。
- **Dependencies**: Sprint 3
- **Acceptance Criteria**:
  - 共享层不依赖 `larkmsg`
- **Validation**:
  - service 测试

### Task 5.2: 抽禁言与主动发消息
- **Location**:
  - 新建 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/shared/messagesvc/`
  - 参考 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/mute_handler.go`
  - 参考 `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/handlers/send_message_handler.go`
- **Description**: 把这两个命令的业务逻辑从 Lark 输出层拆出来。
- **Dependencies**: Task 5.1
- **Acceptance Criteria**:
  - QQ 与 Lark 都能调共享逻辑
- **Validation**:
  - service 测试

## Testing Strategy
- 单元测试：
  - QQ config / DAL
  - group message adapter
  - QQ processor
  - QQ command tree
  - shared chat/config/message services
- 集成测试：
  - 群 @ 固定文本回复
  - `/help`
  - `/config list`
  - 普通文本聊天

## Potential Risks & Gotchas
- `BaseMetaData.ChatID/OpenID` 目前是借壳使用，第一阶段可接受，但不要误当成长期平台抽象。
- 如果一开始就想兼容现有 Lark 全命令集，范围会迅速失控。
- QQ 群消息本阶段只做 @ 机器人消息，非 @ 消息不应进入处理链。
- 标准聊天可做，`agentic` 路径本阶段不要碰。

## Rollback Plan
- 所有新增能力放在新路径：
  - `/mnt/RapidPool/workspace/BetaGo_v2/cmd/qqbot`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/application/qqbot`
  - `/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/qqbot_dal`
- Lark 主链路不动，仅允许少量共享 service 抽取。

## Suggested First Slice
建议第一批只做这 5 个提交块：

1. `qqbot_config` + `cmd/qqbot`
2. `qqbot_dal` 文本发送
3. `GroupATMessageEventHandler` webhook 接入
4. QQ `xhandler` processor
5. QQ `xcommand` `/help` + 标准文本聊天

做到这里，就已经有一个可演示的 QQ 群消息 MVP。

## Sources
- BotGo README: webhook 模式、群 @ 机器人示例、token source、OpenAPI 初始化  
  https://github.com/tencent-connect/botgo
- BotGo `event/register.go`: `GroupATMessageEventHandler`、`WSGroupATMessageData`、`EventGroupAtMessageCreate`  
  https://github.com/tencent-connect/botgo
- BotGo `examples/receive-and-send/main.go`: 群 @ 机器人消息示例  
  https://github.com/tencent-connect/botgo/tree/master/examples/receive-and-send

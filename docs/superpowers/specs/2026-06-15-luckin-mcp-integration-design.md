# 瑞幸 MCP 通用集成设计

## 背景

瑞幸咖啡开放平台提供 CLI、MCP Server 和 Skill 能力。开放页面公开的 MCP 配置为 `streamablehttp`，server name 为 `my-coffee`，生产地址为 `https://gwmcp.lkcoffee.com/order/user/mcp`，通过 `Authorization: Bearer <token>` 认证。该 token 是瑞幸账号会话 token，MCP 与 CLI 共用，不区分飞书用户或群聊。

当前机器人能力通过 `xcommand.RegisterTool` 注册为 LLM 可调用工具，已有金融工具、日程工具和飞书消息工具。瑞幸能力应接入同一工具体系，但不能让模型直接执行真实下单动作。

## 目标

- 新增通用 MCP Client 层，支持 `streamablehttp` MCP server 的工具发现和工具调用。
- 将瑞幸 MCP 作为第一个接入方，提供门店、商品、订单预览、订单详情和半自动下单能力。
- 支持个人、群聊默认、环境默认三类机器人内部凭证作用域。
- `createOrder` 必须经过飞书确认卡片回调触发，LLM 不能直接裸调。
- 保留可审计的调用记录：server、tool、作用域、发起人、确认人、请求摘要、结果摘要和错误。
- 架构允许后续接入其他 MCP server，不把瑞幸逻辑写死到 MCP Client 内部。

## 非目标

- 不自动支付，只返回瑞幸创建订单后的支付链接或二维码。
- 不实现瑞幸账号登录流程。用户或管理员需要从瑞幸开放平台获得 token 后绑定。
- 不让模型执行任意 CLI 或任意 shell 命令。
- 第一版不做通用 MCP OAuth、多租户后台 UI 或跨平台 Skill 安装管理。
- 第一版不把所有第三方 MCP 自动暴露给模型；每个 provider 必须有显式策略和工具白名单。

## 架构

新增三层：

```text
internal/infrastructure/mcpclient
    通用 MCP streamablehttp client，负责协议、HTTP、工具发现、调用、超时和错误归一化。

internal/application/lark/mcpbridge
    把 MCP provider 的工具按策略包装成 xcommand 工具，并处理工具白名单、参数 schema 和结果编码。

internal/application/lark/luckin
    瑞幸 provider 策略：server 配置、工具白名单、凭证选择、下单确认、卡片回调和结果摘要。
```

`mcpclient` 不依赖飞书、瑞幸或机器人业务模型。它只接收 server endpoint、headers、tool name 和 JSON 参数，返回 JSON 结果或协议错误。

`mcpbridge` 负责把 MCP 工具接入当前 `ark_dal/tools.Impl`。桥接层只暴露经过策略允许的工具，并可把高风险工具替换成“创建待确认操作”的本地工具。

`luckin` 负责所有瑞幸特有行为，包括 token 作用域选择、下单确认卡片和 `createOrder` 的二阶段执行。

## MCP Client 契约

核心类型：

```go
type ServerConfig struct {
    Name    string
    URL     string
    Headers map[string]string
    Timeout time.Duration
}

type Tool struct {
    Name        string
    Description string
    InputSchema json.RawMessage
}

type CallRequest struct {
    Server ServerConfig
    ToolName string
    Arguments json.RawMessage
}

type CallResult struct {
    Content json.RawMessage
    Raw     json.RawMessage
}
```

第一版只要求支持瑞幸使用的 `streamablehttp` 形态。协议细节隐藏在 client 内部；上层不能拼接 MCP HTTP 请求体。

错误归一化：

- `ErrUnauthorized`: token 无效或过期。
- `ErrToolNotFound`: 工具不存在或未授权。
- `ErrInvalidArguments`: 参数不符合工具 schema。
- `ErrRemote`: 瑞幸服务返回业务错误。
- `ErrTimeout`: 调用超时或 context cancelled。
- `ErrProtocol`: MCP 响应无法解析或协议不符合预期。

## 瑞幸工具白名单

开放页面列出的瑞幸工具第一版按以下方式接入：

| MCP 工具 | 机器人工具 | 允许模型直接调用 |
|---|---|---|
| `queryShopList` | `luckin_shop_search` | 是 |
| `searchProductForMcp` | `luckin_product_search` | 是 |
| `queryProductDetailInfo` | `luckin_product_detail` | 是 |
| `switchProduct` | `luckin_product_switch` | 是 |
| `previewOrder` | `luckin_order_preview` | 是 |
| `queryOrderDetailInfo` | `luckin_order_detail` | 是 |
| `createOrder` | `luckin_order_prepare_create` + card callback | 否 |

`luckin_order_prepare_create` 不调用瑞幸 `createOrder`。它只保存待确认 payload，生成确认卡片，并返回“等待用户确认”的结果。

确认卡片回调验证通过后，后端才调用 MCP `createOrder`。

## 凭证模型

瑞幸 token 本质是瑞幸账号会话。机器人内部使用三类作用域保存 token：

```text
personal:  app_id + bot_open_id + lark_open_id
chat:      app_id + bot_open_id + chat_id
system:    environment variable LUCKIN_MCP_TOKEN
```

选择规则：

- 私聊优先使用发起人的 personal token。
- 群聊优先使用 chat token。
- 群聊没有 chat token 时使用发起人的 personal token。
- 如果仍没有 token，且配置了 `LUCKIN_MCP_TOKEN`，可以使用 system token。
- 如果没有任何可用 token，工具返回需要绑定 token 的提示。

所有订单预览、待确认卡片和创建订单结果都必须展示作用域标签：

- `个人瑞幸账号`
- `群聊默认瑞幸账号`
- `系统默认瑞幸账号`

群聊默认 token 不是瑞幸侧的群聊身份，只是机器人在该群保存的一份瑞幸账号 token。

## Token 绑定

第一版提供命令或工具级能力：

- 绑定个人 token。
- 解绑个人 token。
- 群管理员绑定群聊默认 token。
- 群管理员解绑群聊默认 token。
- 查看当前会使用的瑞幸账号作用域，不显示完整 token。

token 存储要求：

- 数据库中加密保存 token，不明文落库。
- 日志、错误、卡片和工具结果不输出完整 token。
- 只允许显示 token 尾号或作用域摘要，例如 `****1234`。
- 绑定和解绑操作写审计日志。

第一版可以先使用项目现有配置/权限体系实现操作入口；如果现有配置表不适合保存敏感值，则新增独立 `mcp_credentials` 表。

## 下单确认流程

```text
用户: 帮我点一杯生椰拿铁
    ↓
LLM 调用 luckin_shop_search / luckin_product_search / luckin_order_preview
    ↓
LLM 调用 luckin_order_prepare_create
    ↓
后端保存 PendingOrder，并发送飞书确认卡片
    ↓
发起人点击确认
    ↓
回调校验发起人、chat、pending id、过期时间和 payload hash
    ↓
后端调用 MCP createOrder
    ↓
返回支付链接/二维码和订单摘要
```

确认卡片必须展示：

- 凭证作用域标签。
- 门店名称、地址和距离。
- 商品名称、规格、数量。
- 预估实付金额、优惠信息、预计取餐时间。
- 风险提示：点击确认将创建瑞幸订单，但不会自动支付。

回调校验：

- 只有原发起人可以确认。
- pending order 默认 10 分钟过期。
- 确认时重新读取 pending payload，不能信任卡片回传的完整订单参数。
- payload hash 必须匹配，避免卡片内容和后端待执行参数不一致。
- 每个 pending order 只能执行一次。

## 数据模型

如果现有配置系统不适合敏感凭证，新增：

```sql
mcp_credentials
- id
- provider
- app_id
- bot_open_id
- scope_type
- scope_id
- encrypted_token
- token_hint
- created_by_open_id
- updated_by_open_id
- created_at
- updated_at
- deleted_at
```

`scope_type` 为 `personal` 或 `chat`。`system` 只来自环境变量，不落库。

待确认订单：

```sql
luckin_pending_orders
- id
- app_id
- bot_open_id
- chat_id
- requester_open_id
- credential_scope_type
- credential_scope_id
- mcp_server_name
- create_order_payload
- payload_hash
- preview_result
- status
- result_json
- error_text
- expires_at
- confirmed_by_open_id
- confirmed_at
- created_at
- updated_at
```

`status` 包括 `pending`、`confirmed`、`expired`、`cancelled`、`failed`。

## 注册到机器人

`BuildRuntimeCapabilityTools` 和常规 Lark tools 可以注册瑞幸只读工具与 `luckin_order_prepare_create`。实际注册入口应保持显式：

```go
func RegisterTools(ins *tools.Impl[larkim.P2MessageReceiveV1], opts RegisterOptions)
```

`RegisterOptions` 包含：

- provider 配置。
- credential resolver。
- pending order service。
- 是否启用系统默认 token。
- 是否启用高风险工具。

调度任务工具默认不注册瑞幸下单能力，避免后台任务绕过实时确认。后续如需支持定时点单，需要单独设计授权和确认。

## 可观测性

MCP 调用记录以下指标或日志字段：

- provider: `luckin`
- server: `my-coffee`
- tool_name
- credential_scope_type
- chat_id
- requester_open_id
- status: `success`、`error`、`unauthorized`、`timeout`
- duration_ms

审计日志记录：

- token 绑定、解绑。
- pending order 创建。
- pending order 确认、取消、过期。
- `createOrder` 调用结果。

日志不能包含完整 token、完整支付二维码内容或过长的远端响应。

## 测试策略

先写失败测试，再实现。

- `mcpclient` 请求构造和响应解析测试，使用 fake streamablehttp server。
- `mcpclient` 错误归一化测试：401、工具不存在、远端业务错误、超时、协议错误。
- 瑞幸工具白名单测试：`createOrder` 不会注册为 LLM 直接可调用工具。
- 凭证选择测试：私聊、群聊、群聊 fallback 个人、系统默认、无 token。
- token 脱敏测试：日志和工具结果只出现 hint。
- `luckin_order_prepare_create` 测试：只创建 pending order，不调用远端 `createOrder`。
- 确认回调测试：发起人校验、过期校验、重复确认校验、payload hash 校验。
- 成功确认测试：确认后调用 fake MCP `createOrder`，保存结果并返回支付信息摘要。
- 注册测试：瑞幸工具进入 `BuildRuntimeCapabilityTools`，调度工具默认不包含下单确认能力。

## 风险与处理

- 瑞幸 token 过期或失效：统一返回重新绑定提示，避免反复重试。
- 群聊默认账号归属不清：确认卡片明确展示“群聊默认瑞幸账号”，并限制管理员绑定。
- 真实下单风险：`createOrder` 只能由确认卡片回调执行，且 pending order 单次有效。
- MCP 协议或瑞幸工具 schema 变化：工具发现结果可缓存但需要短 TTL，调用失败时返回可诊断错误。
- 支付链接泄露：只在原会话回复，日志不记录完整支付 URL 或二维码 URL。
- 通用 MCP 抽象过度：第一版只实现瑞幸需要的 `streamablehttp` 子集，但包边界保持通用。

## 实施顺序

1. 实现 `mcpclient` fake server 测试和最小 client。
2. 实现 MCP credential resolver 和加密存储。
3. 实现 `mcpbridge` 工具包装与白名单注册。
4. 实现瑞幸只读工具。
5. 实现 pending order、确认卡片和回调执行。
6. 接入审计、metrics 和错误提示。
7. 增加集成测试和手工验收脚本。

# Plan: Finance Tools Akshare Unification

**Generated**: 2026-03-26
**Estimated Complexity**: High

## Overview
目标仍然是三件事一起完成：

1. 基于仓库现有 `akshareapi` catalog 和 `aktool` 适配层，整理并合并同类但不同数据源的金融/经济接口，统一为少量高层工具。
2. 提供一个给大模型调用的金融/经济工具发现器，让模型先拿到可用 `tool_name` 和 `schema` 约束，再决定调用哪一个高层工具。
3. 在 function call 的续轮链路中，把 discover 返回的高层金融工具动态补进下一轮 `ResponsesRequest.Tools`，而不是一开始把所有金融工具全部暴露出去。

和上次相比，当前仓库有一个关键变化：现有 `gold_price_get` 和 `stock_zh_a_get` 已被 `toolmeta/runtime_behavior.go` 视为 `chat_write + approval` 工具，因为它们会直接发飞书卡片。  
这意味着新的“大模型取数工具”不能直接复用现有 handler 作为最终形态，否则模型调用会被审批流打断，也不符合“只读金融 API 能力”的目标。

因此，推荐把方案调整为：

- 保留现有 `gold_price_get` / `stock_zh_a_get` 作为兼容展示型工具
- 新增一组只读的高层金融数据工具，返回结构化文本/JSON，不直接发卡片
- `finance_tool_discover` 只暴露这组新的只读高层工具
- 续轮动态注入只针对 discover 选出的只读高层工具

## Current Code Findings

### Relevant Runtime Entry Points
- `internal/application/lark/handlers/tools.go`
- `internal/application/lark/handlers/stock_handler.go`
- `internal/infrastructure/aktool/aktool.go`
- `internal/infrastructure/akshareapi/catalog_generated.go`
- `internal/application/lark/agentruntime/chatflow/turn.go`
- `internal/application/lark/agentruntime/reply_turn.go`
- `internal/application/lark/agentruntime/capability/tools.go`
- `internal/infrastructure/ark_dal/responses.go`
- `internal/infrastructure/ark_dal/responses_manual.go`

### What Exists Today
- `BuildLarkTools()` 仍是普通聊天路径的默认工具集合入口。
- 现有金融相关 tool 只有 `gold_price_get` 和 `stock_zh_a_get` 两个高层 handler。
- `aktool` 仍然只是几个非常薄的领域适配函数：
  - 黄金实时
  - 黄金历史
  - A 股分时
  - 股票简称
- `akshareapi` 已经维护完整 endpoint catalog，可用于后续做统一 provider 和 discover 元数据来源。
- 初轮/续轮工具循环已经收口到：
  - `chatflow.ExecuteInitialChatTurn(...)`
  - `reply_turn.BuildRuntimeInitialChatLoop(...)`
  - `ark_dal.ResponseTurnRequest`
  - `ResponsesImpl.buildTurnRequest(...)`

### Important Behavioral Change Since Last Review
- `toolmeta/runtime_behavior.go` 现在把 `gold_price_get` 和 `stock_zh_a_get` 标记为需要审批的 chat-write 工具。
- 因为它们会发送卡片，当前 runtime prompt 语义上也会把它们当“执行动作”而不是“只读取数”。
- 所以新的金融 discover / API 工具层应该和现有展示型工具分层，而不是直接把旧工具重命名。

## Prerequisites
- 不改动现有用户可见的 `/stock gold`、`/stock zh_a` 行为
- 不回退或覆盖当前工作区里的未提交修改
- 默认沿用上次已确认范围：
  - 只暴露合并后的高层工具
  - 不把底层 `akshare` endpoint 直接暴露给模型
  - 内部做 source fallback
  - 通过 discover 控制续轮工具注入

## Sprint 1: Separate Read-Only Finance Capability Layer
**Goal**: 从现有“发卡片型金融工具”里拆出新的只读金融数据层，建立后续 discover 和动态注入的稳定基础。
**Demo/Validation**:
- 可以在单测里直接调用新的 provider / tool handler，拿到结构化数据输出
- 旧的 `gold_price_get` / `stock_zh_a_get` 不受影响

### Task 1.1: Add Finance Domain Metadata and Source Fallback Model
- **Location**:
  - `internal/infrastructure/aktool/`
  - 可新建 `internal/infrastructure/aktool/finance_catalog.go`
  - 可新建 `internal/infrastructure/aktool/finance_provider.go`
- **Description**:
  - 为“高层金融工具”定义内部 catalog：
    - 逻辑工具名
    - 分类
    - 描述
    - 参数 schema
    - 候选数据源列表
    - fallback 顺序
  - 把底层 `akshareapi.Endpoint` 映射成更少量的高层能力。
- **Dependencies**: None
- **Acceptance Criteria**:
  - 可以用代码枚举出所有高层金融工具定义
  - 每个高层工具都有明确的 schema 和 source fallback 定义
  - 不直接向模型暴露底层 endpoint 名
- **Validation**:
  - 新增 catalog/provider 单测，校验工具定义完整性和 fallback 顺序

### Task 1.2: Implement Read-Only Finance Data Providers
- **Location**:
  - `internal/infrastructure/aktool/`
- **Description**:
  - 在现有 `GetRealtimeGoldPrice` / `GetHistoryGoldPrice` / `GetStockPriceRT` 之外，新增更通用 provider。
  - 首批建议覆盖的高层能力：
    - `finance_market_data_get`
    - `finance_news_get`
    - `economy_indicator_get`
  - 每个 provider 内部根据请求类型选择 endpoint，并在失败时按 source fallback。
- **Dependencies**:
  - Task 1.1
- **Acceptance Criteria**:
  - 市场数据类支持股票/指数/黄金/期货中的至少一批稳定能力
  - 资讯类至少支持个股新闻
  - 经济类至少支持常见宏观指标族
  - source fallback 失败时返回可诊断错误，而不是静默吞掉
- **Validation**:
  - `httptest` 覆盖主源失败、副源成功
  - 覆盖无可用源时的错误输出

### Task 1.3: Preserve Legacy Presentation Tools as Compatibility Wrappers
- **Location**:
  - `internal/application/lark/handlers/stock_handler.go`
  - 如有必要可拆新文件，例如 `internal/application/lark/handlers/finance_data_handler.go`
- **Description**:
  - 现有 `gold_price_get` / `stock_zh_a_get` 继续负责发卡片和兼容旧行为。
  - 它们底层可以逐步复用新的 provider，但对外行为不变。
- **Dependencies**:
  - Task 1.2
- **Acceptance Criteria**:
  - 兼容工具名称和旧入参保持不变
  - 兼容工具仍走当前 approval/compatible-output 逻辑
- **Validation**:
  - 现有相关 handler/toolmeta 测试继续成立

## Sprint 2: Add Finance Tool Discovery for the Model
**Goal**: 让模型可以先通过 discover 拿到“哪些金融工具可用、怎么调”。
**Demo/Validation**:
- `finance_tool_discover` 能返回稳定的高层工具清单
- 返回结果中包含 `tool_name` 和 schema 约束

### Task 2.1: Define Discover Output Contract
- **Location**:
  - 可新建 `internal/application/lark/handlers/finance_tool_discover_handler.go`
  - 可新建 `internal/application/lark/handlers/finance_tool_discover_types.go`
- **Description**:
  - 设计 discover 返回结构，建议包含：
    - `tool_name`
    - `description`
    - `schema`
    - `required`
    - `examples`
    - `categories`
  - discover 输入建议支持：
    - `query`
    - `category`
    - `tool_names`
    - `limit`
- **Dependencies**:
  - Task 1.1
- **Acceptance Criteria**:
  - 输出稳定、可被下一轮动态转为 `ResponsesTool`
  - 不输出底层 endpoint 名称
- **Validation**:
  - handler 单测校验返回结构和筛选逻辑

### Task 2.2: Register Discover Tool in Default Toolset
- **Location**:
  - `internal/application/lark/handlers/tools.go`
  - `internal/application/lark/toolmeta/runtime_behavior.go`
- **Description**:
  - 注册 `finance_tool_discover`
  - 在 `toolmeta` 中把它标为真正的只读工具：
    - `SideEffectLevelNone`
    - 无审批
    - 不走兼容发卡
- **Dependencies**:
  - Task 2.1
- **Acceptance Criteria**:
  - `BuildLarkTools()` 能拿到 discover tool
  - prompt 装饰后该工具仍然被描述为只读查询
- **Validation**:
  - 更新 `handlers/tools_test.go`
  - 更新 `toolmeta/runtime_behavior_test.go`

## Sprint 3: Dynamic Tool Injection Into Next Response Turn
**Goal**: discover 结果不只作为文本返回，而是能在下一轮真正注入对应高层金融工具供模型 function call。
**Demo/Validation**:
- 模型先调 `finance_tool_discover`
- 下一轮请求的 `ResponsesRequest.Tools` 中能看到 discover 选出的高层金融工具

### Task 3.1: Extend Turn Request With Dynamic Tool Overrides
- **Location**:
  - `internal/infrastructure/ark_dal/responses.go`
  - `internal/infrastructure/ark_dal/responses_manual.go`
  - `internal/infrastructure/ark_dal/responses_manual_test.go`
- **Description**:
  - 给 `ResponseTurnRequest` 增加续轮动态工具字段，建议是：
    - `AdditionalTools []*responses.ResponsesTool`
    - 或更高层的工具集合字段，由上层先合成为 `ResponsesTool`
  - `buildTurnRequest(...)` 在续轮和首轮都能合并静态工具与动态工具。
- **Dependencies**:
  - Task 2.1
- **Acceptance Criteria**:
  - 不破坏现有静态工具链路
  - 动态注入仅追加，不覆盖默认工具
  - 同名工具去重策略明确
- **Validation**:
  - 新增测试校验 `PreviousResponseId` 场景下工具追加成功

### Task 3.2: Thread Dynamic Tool State Through Runtime Loop
- **Location**:
  - `internal/application/lark/agentruntime/chatflow/runtime_types.go`
  - `internal/application/lark/agentruntime/chatflow/turn.go`
  - `internal/application/lark/agentruntime/reply_turn.go`
- **Description**:
  - 在 initial/reply turn request 中增加“本轮额外工具”状态。
  - 当执行 `finance_tool_discover` 后，将 discover 选出的金融工具加入下一次 `InitialChatTurnRequest`。
  - 保证这个状态在 tool loop 中可以连续传递。
- **Dependencies**:
  - Task 3.1
- **Acceptance Criteria**:
  - 初轮无 discover 时行为不变
  - discover 后仅下一轮开始可见新增高层金融工具
  - 可连续多轮保留或收缩动态工具，策略需要明确定义
- **Validation**:
  - `reply_turn.go` / `chatflow` 增加 loop 级测试

### Task 3.3: Define Discover-to-Tool Expansion Policy
- **Location**:
  - 可新建 `internal/application/lark/agentruntime/finance_tool_injection.go`
- **Description**:
  - 把 discover 返回结果转换为 runtime 可注入工具集合。
  - 推荐策略：
    - 默认只注入 discover 返回的工具子集
    - 限制单次注入数量，避免把过多 schema 塞进下一轮 prompt
    - 对未知 tool name 忽略并打日志
- **Dependencies**:
  - Task 2.1
  - Task 3.1
- **Acceptance Criteria**:
  - discover 结果和实际注入工具一一对应
  - 不允许 discover 注入非金融工具
- **Validation**:
  - expansion 逻辑单测

## Sprint 4: Expose High-Level Finance Tools
**Goal**: 让 discover 返回的高层工具真实可调用，并完成和 provider 的闭环。
**Demo/Validation**:
- discover 返回工具后，模型下一轮可以直接调用这些高层金融工具
- 调用结果返回只读数据，不触发审批

### Task 4.1: Add High-Level Read-Only Tool Handlers
- **Location**:
  - 可新建
    - `internal/application/lark/handlers/finance_market_data_handler.go`
    - `internal/application/lark/handlers/finance_news_handler.go`
    - `internal/application/lark/handlers/economy_indicator_handler.go`
- **Description**:
  - 为新的高层金融工具实现 `ToolSpec` / `ParseTool` / `Handle`
  - 输出应为结构化文本或 JSON 摘要，不发送卡片
- **Dependencies**:
  - Sprint 1
  - Sprint 2
- **Acceptance Criteria**:
  - 所有 discover 暴露的高层工具都有可执行 handler
  - handler 不依赖兼容发卡输出
- **Validation**:
  - 新增 handler 单测

### Task 4.2: Register High-Level Tools Without Exposing Them by Default
- **Location**:
  - `internal/application/lark/handlers/tools.go`
  - 或新增专门的 finance tool registry 文件
- **Description**:
  - 这些高层工具需要“可被动态注入”，但不应在默认首轮工具集中全部暴露。
  - 推荐：
    - 维护一个“默认工具集”
    - 维护一个“可注入金融工具集”
- **Dependencies**:
  - Task 4.1
- **Acceptance Criteria**:
  - `BuildLarkTools()` 默认不包含全部高层金融工具
  - discover expansion 能按名字拿到这些工具定义
- **Validation**:
  - registry 单测

## Sprint 5: Verification and Rollout Safety
**Goal**: 确保引入 discover 和动态注入后，不破坏现有聊天、审批、reply turn 流程。
**Demo/Validation**:
- 旧工具链路无回归
- 新金融工具链路可用

### Task 5.1: Regression Coverage for Existing Tool Loop
- **Location**:
  - `internal/application/lark/agentruntime/*_test.go`
  - `internal/infrastructure/ark_dal/*_test.go`
- **Description**:
  - 补 discover 前后 loop 行为测试：
    - 无 discover
    - discover 后单次注入
    - discover 后多轮调用
    - discover 返回空结果
- **Dependencies**:
  - Sprint 3
- **Acceptance Criteria**:
  - 现有 tool loop 测试仍通过
  - 新链路具备回归保护
- **Validation**:
  - 运行相关 `go test`

### Task 5.2: Add Observability for Discover and Injection
- **Location**:
  - `internal/application/lark/agentruntime/reply_turn.go`
  - `internal/infrastructure/ark_dal/responses_manual.go`
  - 如有必要补充 OTel/log fields
- **Description**:
  - 记录 discover 命中工具数量
  - 记录注入的 tool names
  - 记录 fallback 命中的 source
- **Dependencies**:
  - Sprint 3
- **Acceptance Criteria**:
  - 排查 discover 或注入失败时可从日志看到具体信息
- **Validation**:
  - 日志/trace 字段单测或最小验证

## Testing Strategy
- `internal/infrastructure/aktool`
  - provider/fallback 单测
- `internal/application/lark/handlers`
  - discover handler
  - 高层金融 handler
  - 默认工具集与可注入工具集边界
- `internal/infrastructure/ark_dal`
  - `ResponseTurnRequest` 动态工具合并
- `internal/application/lark/agentruntime`
  - discover -> 下一轮注入 -> 调用高层金融工具 的 loop 级测试
- 回归重点：
  - 旧 `gold_price_get` / `stock_zh_a_get` 兼容行为
  - 现有 approval 流
  - 现有 deep research / reply turn loop 不受影响

## Potential Risks & Gotchas
- **现有金融工具不是只读工具**
  - 当前 `gold_price_get` / `stock_zh_a_get` 会发卡并触发审批，不能直接作为“金融 API 工具”暴露给模型。
- **工具发现器返回的是工具文档还是可执行配置**
  - 需要统一 discover 输出和动态注入输入，否则会出现 discover 说得出来、runtime 注不进去。
- **高层工具 schema 可能过宽**
  - 如果一个工具同时覆盖股票/期货/黄金/指数，schema 会变大，模型误用概率会上升。需要控制首批覆盖范围。
- **底层 endpoint 参数并不完全同构**
  - 同类多源接口的参数不一致，例如 `stock_zh_a_hist` vs `stock_zh_a_hist_tx`，需要在 provider 层做归一化。
- **工具注入数量失控**
  - discover 一次返回过多工具会把下一轮 schema 撑大，影响模型选择质量。
- **工作区当前是脏的**
  - 规划和实施都需要避开已修改文件中的无关变更，尤其是 `reply_turn.go`、`responses.go`、`handlers/tools_test.go` 这些高碰撞文件。

## Rollback Plan
- 如果动态注入部分风险过高，可以先只上线：
  - 只读高层金融工具
  - `finance_tool_discover`
  - 静态注册但默认不暴露
- 然后把“discover -> 下一轮动态注入”作为第二阶段增量功能上线。

## Recommended Execution Order
1. 先做 Sprint 1，把只读金融数据层和旧展示型工具分层。
2. 再做 Sprint 2，定义 discover 的稳定 contract。
3. 再做 Sprint 3，把 discover 结果真的接到续轮动态注入。
4. 最后做 Sprint 4 和 Sprint 5，补高层工具实现和回归验证。

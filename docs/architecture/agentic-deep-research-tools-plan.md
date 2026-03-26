# Plan: Agentic Deep Research Tooling

**Generated**: 2026-03-23
**Estimated Complexity**: High

## Overview

当前仓库已经具备 `agentruntime`、`run/session/step`、resume worker、approval/waiting、初始轮和 continuation tool loop，但它更接近“可持续运行的群聊 agent runtime”，还不是“deep research agent”。

当前表现为“经常只跑一轮”的主要原因，不是 runtime 完全不支持多轮，而是：

- prompt 明确要求每轮只能“最终回答”或“只发起一个 function call”
- 缺少 research 专用工具，模型很容易在一次 web search 后直接收尾
- 没有显式 research plan / scratchpad / source ledger，模型没有被迫保留“未完成问题”
- multi-turn tool loop 仍然主要在单次 invocation 内收敛，不是 per-turn durable reasoning loop

本计划目标不是把当前机器人改成通用浏览器代理，而是优先让它在现有架构里获得“拆题 -> 搜索 -> 阅读 -> 记笔记 -> 交叉验证 -> 带引用输出 -> 必要时异步继续”的能力。

## Prerequisites

- 已有 `agentruntime` / `runtimewire` / `handlers.BuildLarkTools()` 装配链维持不变
- Redis / DB / resume worker 可用
- 允许为 deep research 新增只读 capability 和少量 runtime state
- 默认模型支持 Responses API 风格工具调用与 `previous_response_id`

## Sprint 1: 先把“为什么停一轮”修成可继续研究

**Goal**: 不改大架构，先让 agent 明确知道什么时候不该收尾。

**Demo/Validation**:

- 对“帮我深挖某个主题并给出处”类请求，首轮不会轻易直接总结
- 至少能连续执行 `search -> read -> read -> synthesize`
- reply 中能区分“还在研究”与“已收敛结论”

### Task 1.1: 收紧 agentic research prompt contract

- **Location**:
  - `internal/application/lark/agentruntime/agentic_chat_generation.go`
  - `internal/application/lark/agentruntime/default_continuation_reply_turn_executor.go`
- **Description**:
  - 保留“单轮最多一个 function call”的 transport 约束
  - 新增 research 行为约束：
    - 有 citation / 对比 / 最新资料需求时，不要在第一次查询后直接结束
    - 若证据不足，优先继续研究而不是给泛化总结
    - 只有满足“证据数量 + 来源多样性 + 关键问题已覆盖”时才收尾
- **Dependencies**: none
- **Acceptance Criteria**:
  - prompt 中显式表达“证据不足时继续调用工具”
  - prompt 中显式表达“研究型请求默认不是单次 search 后立即结束”
- **Validation**:
  - 补单测，断言 prompt 包含 research completion criteria

### Task 1.2: 给 runtime 引入研究态输出语义

- **Location**:
  - `internal/application/lark/agentruntime/reply_completion.go`
  - `internal/application/lark/agentruntime/runtimecutover/runtime_output.go`
- **Description**:
  - 区分：
    - `researching`
    - `waiting_external`
    - `completed`
  - 让用户可见回复不再只有“最终总结”这一种终态语义
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - research 中间态可以以 card/message patch 形式展示“继续查证中”
  - 终态收尾仍兼容现有 reply step
- **Validation**:
  - reply emitter 测试覆盖中间态 patch / 最终态完成

### Task 1.3: 增加 research budget guard

- **Location**:
  - `internal/application/lark/agentruntime/default_chat_generation_executor.go`
  - `internal/application/lark/agentruntime/default_capability_reply_turn_executor.go`
  - `internal/application/lark/agentruntime/default_continuation_reply_turn_executor.go`
- **Description**:
  - 在现有 `defaultInitialChatToolTurns = 8` 之外，显式增加 research budget 配置：
    - 最大 turn 数
    - 最大来源数
    - 最大搜索轮数
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - research budget 可配置
  - 超预算时模型收到明确收敛提示，而不是静默停机
- **Validation**:
  - 单测覆盖 turn limit / budget exhausted 分支

## Sprint 2: 补齐 deep research 最小只读工具集合

**Goal**: 让 agent 有真实的“搜 + 读 + 抽取 + 引用”能力，而不是只有模糊 web search。

**Demo/Validation**:

- 能批量搜索、读取页面正文、抽取关键段落、输出来源清单
- 能把多个来源的结论合并，并保留 URL / 标题 / 时间

### Task 2.1: 新增批量网页搜索工具

- **Location**:
  - `internal/application/lark/handlers/research_web_search_handler.go`
  - `internal/application/lark/handlers/tools.go`
- **Description**:
  - 新增 `research_web_search`
  - 输入支持：
    - `queries[]`
    - `top_k`
    - `domains[]`
    - `freshness_days`
  - 输出结构化结果：
    - title
    - url
    - snippet
    - source
- **Dependencies**: none
- **Acceptance Criteria**:
  - 相比内置 `web_search`，结果可被 runtime 明确持久化与二次消费
  - 支持一次返回多 query 结果
- **Validation**:
  - handler 测试覆盖参数解析和输出结构

### Task 2.2: 新增页面正文读取工具

- **Location**:
  - `internal/application/lark/handlers/research_read_url_handler.go`
  - `internal/infrastructure/xhttp` 或新增轻量 reader 适配层
  - `internal/application/lark/handlers/tools.go`
- **Description**:
  - 新增 `research_read_url`
  - 负责拉取 URL，转成 markdown/plain text，返回：
    - title
    - canonical_url
    - published_at
    - text_excerpt
    - full_text_truncated
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - 机器人可针对搜索结果做真正“打开阅读”
  - 返回内容可控截断，不把超长页面原文塞进上下文
- **Validation**:
  - 测试覆盖 HTML/Markdown/失败回退

### Task 2.3: 新增证据抽取工具

- **Location**:
  - `internal/application/lark/handlers/research_extract_evidence_handler.go`
  - `internal/application/lark/handlers/tools.go`
- **Description**:
  - 新增 `research_extract_evidence`
  - 输入：
    - `document_text`
    - `questions[]`
  - 输出：
    - answer snippets
    - quote candidates
    - uncertainty
- **Dependencies**: Task 2.2
- **Acceptance Criteria**:
  - agent 可以对已读页面做定向抽取，而不是每次重读全文
- **Validation**:
  - 单测覆盖证据结构和空结果处理

### Task 2.4: 新增来源账本工具

- **Location**:
  - `internal/application/lark/handlers/research_source_ledger_handler.go`
  - `internal/application/lark/agentruntime`
- **Description**:
  - 新增只读/轻写入 research state capability：
    - `research_source_add`
    - `research_source_list`
  - 将来源标题、URL、发布时间、可信度标签持久到 run 级 state
- **Dependencies**: Task 2.1, Task 2.2
- **Acceptance Criteria**:
  - final synthesis 不再只依赖模型短时上下文
  - sources 能在 run 内回看
- **Validation**:
  - step/output 持久化测试

## Sprint 3: 给 runtime 增加 research memory，而不是只靠上下文窗口

**Goal**: 让多轮研究的“计划、已读内容、未解决问题”在 run 内 durable 可恢复。

**Demo/Validation**:

- run 被打断后，能恢复未完成 research agenda
- 模型知道“还剩哪些子问题没查”

### Task 3.1: 引入 research scratchpad state

- **Location**:
  - `internal/application/lark/agentruntime/types.go`
  - `internal/application/lark/agentruntime/continuation_processor.go`
  - `internal/application/lark/agentruntime/reply_completion.go`
- **Description**:
  - 新增 run 级 scratchpad/state payload：
    - topic
    - subquestions
    - findings
    - open_questions
    - sources
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 每轮 tool 完成后可增量更新 scratchpad
  - continuation prompt 自动带入 scratchpad 摘要
- **Validation**:
  - processor 测试覆盖 scratchpad update 与恢复

### Task 3.2: 把 intermediate research plan 变成一等 durable step

- **Location**:
  - `internal/application/lark/agentruntime/initial_capability_trace_recorder.go`
  - `internal/application/lark/agentruntime/default_*reply_turn_executor.go`
- **Description**:
  - 当前已有中间 `plan` durable 化基础，继续扩展为 research plan step：
    - 当前目标
    - 本轮假设
    - 下一步查询
    - 完成条件
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - 研究过程不再只有 capability trace，没有“为什么继续查”的 durable 解释
- **Validation**:
  - step repo 测试覆盖 plan payload

### Task 3.3: 在最终回复中自动输出 citations

- **Location**:
  - `internal/application/lark/agentruntime/runtimecutover/runtime_output.go`
  - `internal/application/lark/agentruntime/agentic_chat_generation.go`
- **Description**:
  - synthesis 时自动把 source ledger 映射成：
    - `[1] 标题 - 域名 - 日期`
    - 或卡片里的来源区块
- **Dependencies**: Task 2.4, Task 3.1
- **Acceptance Criteria**:
  - research 类最终回复默认带来源
  - 没有来源时不允许伪装成 researched answer
- **Validation**:
  - output formatting 测试

## Sprint 4: 把 deep research 从“单次 invocation 多轮”推进到“可恢复的 durable loop”

**Goal**: 解决当前最关键的结构边界，即 multi-turn loop 仍然主要内聚在单次 invocation 内。

**Demo/Validation**:

- 某轮 research 中途失败或超时后，可从最近一步继续
- 每轮 model turn / tool turn 都可在 run steps 中看到

### Task 4.1: 把 initial/continuation 每轮 model turn durable step 化

- **Location**:
  - `internal/application/lark/agentruntime/run_processor.go`
  - `internal/application/lark/agentruntime/continuation_processor.go`
  - `internal/application/lark/agentruntime/default_chat_generation_executor.go`
- **Description**:
  - 不再只在 invocation 结束后集中落部分结果
  - 每轮都显式记录：
    - turn input
    - response_id
    - plan snapshot
    - selected capability
- **Dependencies**: Sprint 3
- **Acceptance Criteria**:
  - full research chain 可逐轮回放
  - 中断后可接着最近 turn 继续
- **Validation**:
  - run/step repo 集成测试

### Task 4.2: 新增 waiting_research / background_research contract

- **Location**:
  - `internal/application/lark/agentruntime/types.go`
  - `internal/application/lark/agentruntime/resume_event.go`
  - `internal/application/lark/schedule`
- **Description**:
  - 为长时间抓取或批量阅读引入：
    - `waiting_research`
    - `ResumeSourceResearch`
  - 让 research 可以分批推进，而不是必须在一次请求里完成
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - 大批量 research 可以异步恢复
  - 用户可收到“我继续查，稍后回你”的中间态
- **Validation**:
  - resume worker / dispatcher 测试覆盖 research source

### Task 4.3: 为 follow-up 对话附着 active research run

- **Location**:
  - `internal/application/lark/messages/ops/runtime_route.go`
  - `internal/application/lark/agentruntime/policy.go`
- **Description**:
  - 用户在同一话题追问“继续查”“换个角度再找”时，优先 attach 到当前 research run
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - follow-up 不会启动一条全新 run 丢失 research context
- **Validation**:
  - 路由与 attach 测试

## Testing Strategy

- 单元测试：
  - prompt contract
  - tool 参数解析与输出结构
  - source ledger / scratchpad update
  - reply/citation formatting
- 集成测试：
  - `search -> read -> extract -> synthesize`
  - `search -> read -> wait -> resume -> synthesize`
  - thread follow-up attach 到 active research run
- 人工回归：
  - 群聊里真实发起“调研型问题”
  - 检查是否能展示中间态、来源、最终结论

## Potential Risks & Gotchas

- 只加工具、不改 prompt 和 completion criteria，模型仍会第一次搜索后收尾
- 只加 prompt、不加 `read_url / source ledger`，所谓 deep research 仍然只是更长的 web search summary
- 如果一次返回全文过长，会把上下文窗口耗尽，必须做截断和摘录
- 如果来源账本只是文本拼接，不做结构化字段，最终 citation 质量会很差
- 若 per-turn durable 化没做，长 research 仍会受单次 invocation 生命周期限制

## Rollback Plan

- Sprint 1/2 的只读工具可以独立下线，不影响现有群聊主能力
- research scratchpad 可先做 feature flag，仅对 agentic mode 打开
- per-turn durable loop 若风险过高，可先保持现有 invocation 内循环，只保留 source ledger 与 citation 输出

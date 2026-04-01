# Plan: Chatflow User Context Augmentation

**Goal:** 在当前 `agentruntime/chatflow` 架构下，落地“索引隔离 + 冷启动回扫 + 按需画像融入”，在提升长期连续性的同时控制上下文污染。

**Architecture baseline:** `standard_plan.go` / `agentic_plan.go` 的 prompt 组装、`member_tools.go` 的实时成员能力、`toolmeta/runtime_behavior.go` 的副作用治理、`xchunk` 的历史与 topic 基座。

---

## Phase 1: Read Path First (低风险)

### Task 1.1: 增加 profile read resolver（仅只读）

- **Location**: `internal/application/lark/agentruntime/chatflow`
- **Action**:
  - 在 chatflow 层增加统一的 `profile context resolver`（可先空实现 + 接口）。
  - resolver 输入：`chat_id/open_id/user_request/history/reply_scope`。
  - resolver 输出：严格截断后的 profile lines（最多 N 条）。
- **Acceptance**:
  - 未命中条件时返回空，不改变现有 prompt。
  - 命中条件时只追加小片段，不影响原历史策略。

### Task 1.2: 接入 standard/agentic prompt builder

- **Location**:
  - `internal/application/lark/agentruntime/chatflow/standard_plan.go`
  - `internal/application/lark/agentruntime/chatflow/agentic_plan.go`
- **Action**:
  - 在 `ContextLines` 组装前后接入 profile lines（建议独立段落）。
  - 对 direct/ambient、reply-scoped 等场景设置不同注入阈值。
- **Acceptance**:
  - 每轮注入上限生效。
  - reply-scoped 情况下长期画像权重降低。

### Task 1.3: 配置开关与观测

- **Location**: `internal/application/config/*`, metrics/logging 现有模块
- **Action**:
  - 增加 read 开关、注入条数上限、置信度阈值。
  - 打点：命中率、平均注入条数、命中目标用户类型。
- **Acceptance**:
  - 开关关闭时行为与当前一致。
  - 打点可用于灰度观察。

---

## Phase 2: Cold-Start Backfill (评估驱动)

### Task 2.1: 回扫任务（dry-run 优先）

- **Location**: `internal/application/lark/profilememory`（新模块，建议）
- **Action**:
  - 从 chunk/topic 索引回扫候选画像。
  - 输出报告：facet 分布、冲突率、低置信比例。
- **Acceptance**:
  - dry-run 不写入线上索引。
  - 报告可用于人工抽样评审。

### Task 2.2: 小批写入与回滚验证

- **Action**:
  - 通过白名单 chat 或 chat_user 范围小批写入。
  - 验证 read path 可回退，不依赖写入链路可用性。
- **Acceptance**:
  - 写入异常不会影响主回复路径。
  - 回退开关可在分钟级生效。

---

## Phase 3: Online Incremental Write (受控增强)

### Task 3.1: 在线候选提取与 merge/gate

- **Location**: `xchunk` 后处理 or 独立异步消费者
- **Action**:
  - 从新 chunk 增量提取用户画像候选。
  - 做 facet 白名单、冲突处理、衰减更新。
- **Acceptance**:
  - 仅稳定信号入库。
  - 冲突写入具备 revision 或幂等保障。

### Task 3.2: 治理任务

- **Action**:
  - 周期性衰减过期画像。
  - 支持人工纠错或软删除。
- **Acceptance**:
  - 陈旧画像不再长期占据 prompt 配额。

---

## Phase 4: Quality Loop

### Task 4.1: 离线+在线评估

- **Offline**:
  - 抽样比对“仅历史” vs “历史+画像”回复质量。
- **Online**:
  - 观察误答率、skip/reply 比例、用户追问率。

### Task 4.2: 阈值调优

- 调整：注入上限、置信度阈值、衰减周期、facet 白名单。
- 原则：优先降低污染，再逐步提高融入度。

---

## Verification Checklist

- `standard_plan` 与 `agentic_plan` 都有单测覆盖“开/关 + 命中/未命中”。
- reply-scoped、ambient、direct 三种模式下注入行为可解释。
- 画像索引故障时系统自动降级到“无画像”路径。
- 灰度阶段可通过配置快速全量关闭。

## Rollback Strategy

1. 关闭 profile read 开关（立即生效）。
2. 停止回扫/在线写入任务。
3. 保留索引数据但不读取，待复盘后再恢复。

该策略保证：即使画像模块出现质量问题，`chatflow` 主链路仍可稳定运行。

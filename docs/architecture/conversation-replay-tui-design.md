# BetaGo_v2 Conversation Replay TUI Design

## Scope

本文定义一个本地 TUI，用于：

- 通过群聊名搜索并选择群聊。
- 从所选群聊中抽取近 `X` 天内、满足筛选条件的 `Y` 条消息。
- 对选中样本执行“只读整链路重放”：
  - runtime observation
  - intent / route
  - conversation generation
- 将批量结果写入本地明细报告目录。

本文基于现有单条 `intent replay` 能力，扩展到“批量样本选择 + 对话生成 replay + TUI 浏览 + 本地报告”。

本文不覆盖：

- 真实发消息
- 真实工具执行
- 真实审批 / callback / schedule continuation
- 群内 `/debug` 入口
- Web UI
- 跨群批量回放

Related docs:

- `docs/architecture/intent-decision-replay-design.md`
- `docs/architecture/intent-decision-replay-plan.md`

## Current Implementation Status

当前仓库内已经落地的能力：

- `chat catalog`：启动时从历史索引加载群聊目录，目录项包含 `chat_name/chat_id/近窗消息量/最近活跃时间`。
- `sample selector`：按时间窗、消息形态和内容特征筛样本。
- `conversation replay`：支持 baseline/augmented 的 standard/agentic 只读 replay。
- `batch runner + report writer`：支持整批执行并落盘到 `artifacts/replay-batches/...`。
- `replay tui`：已提供本地 TUI 入口和最小状态机。

当前 `replay tui` 走的是一版最小键盘流，优先验证闭环，不追求复杂交互组件：

- Chat Picker：启动即加载 chat catalog；输入框内做本地全文过滤；回车选中当前群。
- Filter Builder：当前版本默认使用启动参数里的 `--days/--limit`，回车加载样本。
- Sample Preview：`↑/↓` 移动，`space` 切换单条，`a` 全选，回车执行 batch。
- Report Viewer：回车进入 case 详情，`b` 或 `esc` 返回 summary。

Chat Picker 的当前视觉方向采用 panel-style：

- 顶部明确显示步骤、标题和本地 catalog 状态。
- 中部单独渲染有边框的输入框，降低“看不出哪里能输入”的问题。
- 下方用独立结果面板展示可选列表，并只渲染当前 cursor 附近窗口，避免大目录时刷屏。

后续如需更强交互，再在这条最小闭环上叠加更丰富的列表/表格组件。

## Goal

第一版目标不是“完全模拟线上 agent runtime”，而是给研发一个稳定、可批量复查的本地调试台，回答下面几个问题：

1. 某个群在最近一段时间内，哪些消息值得重放。
2. baseline / augmented 在 intent、route、generation 上分别怎么变化。
3. 哪些消息会从 `standard` 变成 `agentic`，或者反过来。
4. 哪些消息虽然 route 不变，但最终回复、引用、tool intent 发生了变化。
5. 能否把这些结果落成一份可以反复复查的本地报告，而不是只在终端里一闪而过。

## UX Summary

第一版 TUI 固定为五步流转：

1. `Chat Picker`
   - 启动时预加载本地 chat catalog。
   - 输入框对 `chat_name/chat_id` 做本地全文过滤，而不是每次回车重新远程搜索。
   - 展示匹配群聊列表：`chat_name`、`chat_id`、近 X 天消息量、最近活跃时间。
   - 选择一个群进入下一步。

2. `Filter Builder`
   - 配置采样窗口：近 `X` 天、样本上限 `Y`。
   - 配置消息形态筛选。
   - 配置内容特征筛选。

3. `Sample Preview`
   - 展示候选消息列表。
   - 支持勾选、取消勾选、全选。
   - 展示每条命中的筛选标签。

4. `Replay Runner`
   - 默认 `dry-run`。
   - 支持对选中样本执行批量 replay。
   - 显示当前阶段、总进度、成功数、失败数。

5. `Report Viewer`
   - 总览统计。
   - 单条样本详情。
   - 显示本地报告目录路径。

## Discovery Strategy

群聊发现第一版不直接读取 Lark 群列表，而是复用本地历史索引。

原因：

- 用户后续马上就要从该群抽历史消息，所以只看“本地已有历史”的群已经足够。
- 群名搜索、消息量统计、最近活跃时间都更适合在本地索引做。
- 避免第一版引入新的远程读接口、权限问题和额外延迟。

当前代码里，历史索引消息模型 `internal/xmodel/models.go` 已包含：

- `chat_id`
- `chat_name`
- `create_time`
- `user_id`
- `user_name`
- `raw_message`
- `message_type`

因此第一版群聊发现策略调整为两段：

- 启动阶段：通过聚合查询直接加载 `chat catalog`，拿到每个群最近可用的 `chat_name`、`chat_id`、近窗消息量、最近活跃时间。
- 交互阶段：TUI 在内存里对 catalog 做 `chat_name/chat_id` 的全文 contains 过滤。

内部仍以 `chat_id` 作为稳定标识。

如果某个群的历史数据里 `chat_name` 缺失或不稳定，则退化为：

- 仍允许通过 `chat_id` 进入。
- TUI 列表里显示 `<unknown>` 或最近可用的 `chat_name`。

## Filtering And Sampling

第一版采用“先过滤，再抽样”。

### Message-shape filters

为避免歧义，消息形态在 UI 和报告中统一使用以下枚举：

- `mention`
- `reply_to_bot`
- `command`
- `ambient_group_message`

其中 `ambient_group_message` 表示：

- 非 `@bot`
- 非 reply-to-bot
- 非命令
- 普通群聊自然流过的消息

### Content-feature filters

第一版支持：

- `question`
- `long_message`
- `has_link`
- `has_attachment`
- `keyword_contains`

说明：

- `question`：基于问号、疑问词和现有文本规则做轻量判定，不先调用模型。
- `long_message`：基于字符数或分词长度阈值。
- `has_link`：识别 URL。
- `has_attachment`：基于 `message_type` 或正文结构识别。
- `keyword_contains`：简单关键词包含匹配。

### Sampling rule

第一版默认采样策略：

- 先按 `X` 天窗口过滤。
- 再应用形态筛选和内容筛选。
- 最后按时间倒序取前 `Y` 条。

第一版不做：

- 随机抽样
- 分层抽样
- 权重抽样

## Replay Depth

用户明确选择“从 intent 到最终回复生成整条只读链路”，因此第一版 replay 深度如下。

### For every sample

每条样本依次执行：

1. `target loading`
2. `runtime observation`
3. `baseline intent case`
4. `augmented intent case`
5. `baseline route decision`
6. `augmented route decision`
7. `baseline conversation generation replay`
8. `augmented conversation generation replay`
9. `diff computation`

### Conversation generation replay contract

“对话生成 replay” 第一版只做到“生成请求构建 + 模型输出 + 工具意图记录”，不执行任何副作用。

标准链路：

- 复用 `chat_handler.go` / `chatflow/standard_plan.go` 的 prompt 组装和生成路径。
- 记录最终 `decision / thought / reply / reference_from_web / reference_from_history`。

agentic 链路：

- 复用 `agentruntime/chatflow` 的 plan builder。
- 允许模型进入“想调工具”的状态。
- 但在 replay executor 层统一拦截，不执行真实工具。
- 记录第一轮 tool intent：
  - `function_name`
  - `arguments`
  - `max_tool_turns`
  - `would_call_tools=true/false`

### Side-effect policy

第一版强制禁止：

- 真实消息发送
- 真实工具执行
- 真实审批
- 真实等待 callback
- 真实 schedule 延续

即使模型尝试发起工具调用，也只记录意图，不继续推进。

## Execution Modes

### Default: dry-run

默认模式为 `dry-run`。

含义：

- 不调用真实模型。
- 重点验证：
  - 样本选择是否合理
  - baseline / augmented 输入差异
  - route 前置信号
  - generation request build 是否合理

### Optional: live-model

用户可以在 TUI 内手动切到 `live-model`：

- 仅对选中的样本或当前批次开启。
- 用于真正获取 intent / generation 的模型返回。
- 成本和耗时显著高于 dry-run，因此不是默认值。

## Report Model

每次批量 replay 都生成一个批次报告目录：

```text
artifacts/replay-batches/<timestamp>-<chat_slug>/
  summary.json
  summary.md
  filters.json
  samples.json
  cases/
    <message_id>.json
    <message_id>.md
```

### Summary view

汇总至少包含：

- `chat_id`
- `chat_name`
- `time_window_days`
- `selected_sample_count`
- `success_count`
- `partial_count`
- `failed_count`
- `baseline_standard_count`
- `baseline_agentic_count`
- `augmented_standard_count`
- `augmented_agentic_count`
- `route_changed_count`
- `generation_changed_count`
- `tool_intent_changed_count`

### Per-case detail

单条样本至少包含：

- 原消息元信息
- 命中的筛选标签
- runtime observation
- baseline / augmented 的：
  - intent input
  - history/profile context
  - intent analysis
  - route decision
  - conversation plan / prompt / user input
  - tool intent
  - final generated output
- diff summary
- error / status

## Diff Layers

单条样本需要至少输出四层 diff：

1. `intent_diff`
   - `intent_input_changed`
   - `interaction_mode_changed`
   - `needs_history_changed`
   - `needs_web_changed`

2. `route_diff`
   - `standard -> agentic`
   - `agentic -> standard`

3. `generation_diff`
   - `decision changed`
   - `reply changed`
   - `reference_from_web/history changed`

4. `tool_intent_diff`
   - 是否尝试调工具发生变化
   - 第一工具名是否变化
   - 参数是否变化

## Failure Policy

批量跑必须容忍坏样本。

第一版规则：

- 单条失败不终止整批。
- 每条样本记录 `status`：
  - `success`
  - `partial`
  - `failed`
- 错误分层记录：
  - `sample_load_error`
  - `intent_error`
  - `route_error`
  - `generation_error`
  - `report_write_error`

## Recommended Implementation Shape

建议新增六个清晰模块：

- `chat_catalog`
  - 按群聊名发现候选群。
- `sample_selector`
  - 查询近 X 天消息并筛选、抽样。
- `conversation_replay`
  - 在现有 `intent replay` 上扩展 generation replay。
- `batch_runner`
  - 负责整批执行、进度、并发和失败隔离。
- `report_writer`
  - 负责 summary/case 落盘。
- `tui_app`
  - 页面流转、键盘交互、列表/详情展示。

## Library Choice

TUI 第一版建议使用：

- `github.com/charmbracelet/bubbletea`
- `github.com/charmbracelet/bubbles`
- `github.com/charmbracelet/lipgloss`

选择理由：

- 已是 Go 生态里成熟的终端状态机方案。
- 适合列表选择、表格、进度、明细钻取。
- 比手写 ANSI 控制流更稳，也比 Web UI 更符合本次目标。

实现时优先使用当前稳定版依赖，不追 prerelease 分支。

当前代码锁定为稳定版本：

- `github.com/charmbracelet/bubbletea v1.3.10`
- `github.com/charmbracelet/bubbles v1.0.0`
- `github.com/charmbracelet/lipgloss v1.1.0`

## First-Version Non-Goals

第一版明确不做：

- 真实工具执行
- 真实审批 / callback / schedule continuation
- 随机 / 分层抽样
- 跨群批量
- Web UI
- 群内 `/debug`
- 回归样本治理台

## Manual Workflow

当前推荐手工流程：

```bash
go run ./cmd/betago replay tui --days 3 --limit 5
```

建议先用 `dry-run` 默认模式做一轮：

1. 启动后先等待 Chat Picker 预加载本地 chat catalog。
2. 在输入框里输入群聊名或 `chat_id` 的任意片段，观察本地过滤后的结果列表。
3. 回车选中目标群后进入 Filter Builder。
4. 先用较小窗口，例如 `days=3`、`limit=5`。
5. 在 Sample Preview 先全选一小批样本验证闭环。
6. 在 Report Viewer 记录 `artifact_dir`，检查本地报告。

切到 `--live-model` 的时机：

- 已确认筛样逻辑合理。
- 需要观察真实 `intent_analysis / route / generation / tool intent` 差异。
- 接受更高耗时与模型波动。

报告目录布局：

```text
artifacts/replay-batches/<timestamp>-<chat_slug>/
  summary.json
  summary.md
  filters.json
  samples.json
  cases/
    <message_id>.json
    <message_id>.md
```

## Acceptance Criteria

做到以下几点即可认为第一版可用：

- 能按群名搜索并选择一个群。
- 能配置 `X 天 / Y 条 / 消息形态筛选 / 内容特征筛选`。
- 能看到候选样本并勾选。
- 能执行整批 `dry-run` replay。
- 能对选中的少量样本切到 `live-model`。
- 能在 TUI 内查看总览和单条明细。
- 能把完整批次报告落到本地目录。

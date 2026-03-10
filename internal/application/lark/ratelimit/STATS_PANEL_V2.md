# RateLimit Stats Panel V2

相关文档：
- [RateLimit 架构说明](./README.md)

## 背景

原始的 `ratelimit stats` 面板使用模板表格卡，把基础状态、诊断指标、最近发送记录全部压进四列表。

这个方案在字段较少时还能工作，但继续扩展会出现几个问题：

1. 信息分层弱
- 用户必须逐行扫描表格，才能拼出“当前是不是在冷却、为什么会被限流”

2. 分组表达差
- 原实现通过 `"---"` 伪行来插入分段，说明数据已经不适合继续塞进单表

3. 扩展成本高
- 如果后续增加刷新、切视角、筛选窗口、风险说明，模板表格会越来越别扭

## 改造目标

把 `ratelimit stats` 改造成 schema v2 原生卡，目标是：

1. 先展示结论
- 会话
- 当前状态
- 冷却剩余

2. 再展示核心指标
- 历史总发送
- 近24小时
- 近1小时
- 活跃度评分
- 爆发因子
- 冷却等级

3. 最后展示诊断和最近发送记录

## 实现方案

当前实现拆成四层：

1. Snapshot 层
- `BuildStatsSnapshot(ctx, chatID)`
- `GetDetailSnapshot(ctx, chatID)`
- 负责一次性收集：
  - `ChatStats`
  - `ChatMetrics`
  - `Config`
  - `Now`
- 并在这里把 cooldown 真值回填到详情快照

2. Policy 层
- `StatsDisplayPolicy`
- 负责：
  - 展示阈值
  - 风险等级颜色
  - overview 文案策略
- 小时/日阈值、活跃度阈值优先复用 `Config`

3. ViewModel 层
- `BuildStatsCardData(ctx, chatID)`
- `buildStatsCardData(snapshot)`
- 负责把 snapshot 转成适合 UI 渲染的 `StatsCardData`

4. Render 层
- `BuildStatsCardJSON(ctx, chatID)`
- `buildStatsRawCard(data)`
- 负责把 `StatsCardData` 组装成 schema v2 card JSON

对应代码：
- `internal/application/lark/ratelimit/stats_card.go`
- `internal/application/lark/ratelimit/stats_snapshot.go`
- `internal/application/lark/ratelimit/stats_policy.go`
- `internal/infrastructure/lark_dal/larkmsg/card_v2.go`

## 卡片结构

卡片分为五个区：

1. 结论区
- 顶部先给出一句“当前判断”
- 直接展示是否稳定、是否在冷却、是否需要关注拒绝率
- 同时展示会话 ID 和更新时间

2. Hero 指标区
- 当前状态
- 冷却等级
- 拒绝率
- 使用更大的字号和颜色突出主结论

3. 核心指标区
- 两列指标块
- 每个指标块包含：
  - 标签
  - 高亮值
  - 简短说明

4. 诊断指标区
- 只有存在 `ChatMetrics` 时显示
- 也是两列指标块

5. 最近发送区
- 只显示最近 5 条
- 没有数据时显示空态

## 视觉策略

这一版不再使用“纯平铺文本 + 连续表格行”的表达方式，而是改成：

1. 先结论，后细节
- 用户先看到“当前处于冷却窗口”或“频控状态稳定”

2. 重点值用字号和颜色区分
- 主结论使用更大的 `plain_text.text_size`
- 状态类字段使用 `green / orange / red`

3. 次级说明降权
- 标签与说明统一使用灰色小字
- 降低阅读噪音，让值更突出

4. 分区解释明确
- 核心指标和诊断指标分别附带一句解释
- 避免用户不知道某块数据是“决策数据”还是“观测数据”

5. 控制节点预算
- Feishu Card JSON 2.0 整卡最多 200 个组件
- 因此：
  - 结论区和 Hero 区保留少量 `plain_text` 高亮
  - 核心指标、诊断指标、最近发送优先压成 `markdown` 块
  - 避免每个字段都拆成多层 `div + plain_text`

6. 当前版本偏紧凑
- 已将 body spacing、column spacing 和 block padding 压小一档
- 目标是避免管理面板在桌面端显得过松

## Handler 调整

`internal/application/lark/handlers/ratelimit_handler.go`

`ratelimit stats` 已从模板卡切到原生 v2 卡：

1. 旧实现
- handler 内直接拼表格数据
- 通过 `larktpl.FourColSheetTemplate` 发卡

2. 新实现
- handler 只负责：
  - 确定目标 chat ID
  - 调 `ratelimit.BuildStatsCardJSON(...)`
  - 通过 `sendCompatibleCardJSON(...)` 发卡

这样 UI 和业务诊断数据解耦，后面要继续加交互时不会再把 handler 变胖。

## 验证

当前最小验证集：

1. Builder / 结构测试
- `go test ./internal/application/lark/ratelimit`

2. Handler 联动验证
- `go test ./internal/application/lark/handlers ./internal/interfaces/lark ./cmd/larkrobot`

3. 关键保护项
- schema v2 结构断言
- 非模板表格断言
- 卡片组件数量不超过 200 的预算测试
- snapshot 会回填 cooldown 真值的测试
- 关键指标会跟配置阈值联动的测试

## 下一轮优化建议

1. 空列占位可以继续收敛
- 当前双列布局在奇数指标时仍会生成一个空列占位
- 这不是功能问题，但会额外消耗一点组件预算
- 若后续要继续加字段，可以考虑把最后一行改成单列宽块

2. Metrics 写入仍然偏碎
- `Allow` 链路里仍会分多次更新 metrics：
  - `recordActivityScore`
  - `setCooldownActive`
  - `recordCheck`
- 当前频率下可以接受，但如果后续刷新和诊断查询变多，建议考虑 pipeline 或聚合写

3. 视觉表达还可以继续增强
- 目前为了控制 200 组件预算，核心指标区使用了不少 `markdown` 压缩表达
- 如果后续还想更强的字体层级，需要先做组件预算再分配，而不是直接继续堆 `plain_text`

4. `ratelimit list`
- 仍然是模板表格卡
- 建议下一步改成“概览列表”而不是继续扩展四列表

5. 面板交互
- 后续可增加：
  - 刷新
  - 跳转概览
  - 时间窗口切换

6. 面板统一
- `config` 与 `ratelimit stats` 已进入 schema v2 原生卡路线
- `feature/reply/word/image` 可逐步迁移

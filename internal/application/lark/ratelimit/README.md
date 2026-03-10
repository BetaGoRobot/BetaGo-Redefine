# RateLimit

相关文档：
- [Stats Panel V2 改造方案](./STATS_PANEL_V2.md)
- [Stats Panel V2 当前结构](./STATS_PANEL_V2.md#卡片结构)
- [Stats Panel V2 约束与优化点](./STATS_PANEL_V2.md#下一轮优化建议)

## 模块目标

`internal/application/lark/ratelimit` 提供一套面向群聊机器人的智能频控能力，目标不是简单限流，而是根据会话活跃度、发送密度、爆发情况和触发类型，动态决定机器人是否允许继续发言。

## 核心结构

### 1. SmartRateLimiter

主入口：
- `Get()`
- `Allow(ctx, chatID, triggerType)`
- `Record(ctx, chatID, triggerType)`
- `GetStats(ctx, chatID)`

职责：
- 读取会话统计
- 计算派生指标
- 决定是否允许发送
- 在实际发送后记录行为

### 2. Metrics

主入口：
- `GetMetrics()`
- `GetChatStats(chatID)`
- `GetAllChatStats()`

职责：
- 记录诊断维度的检查次数、允许次数、拒绝次数、最后更新时间
- 提供管理/监测面板读取

### 3. Decider

位于：
- `integration.go`

职责：
- 连接业务层触发逻辑和 `SmartRateLimiter`
- 给 intent/random 等回复链路提供统一的 allow/record 判定

## 数据来源

当前实现同时使用 Redis 和本地内存：

1. Redis
- 会话总发送统计
- 最近发送记录
- 小时活跃度
- 冷却状态
- 诊断 metrics

2. 本地内存
- `SmartRateLimiter.localCache`
- 作为 `ChatStats` 的进程内缓存，减少重复反序列化

## 关键数据模型

### ChatStats

核心字段：
- `TotalMessagesSent`
- `CooldownUntil`
- `CooldownLevel`
- `RecentSends`
- `TotalMessages24h`
- `TotalMessages1h`
- `CurrentActivityScore`
- `CurrentBurstFactor`

其中：
- 持久化主体在 Redis
- `RecentSends` / 派生统计在读取时补齐

### ChatMetrics

核心字段：
- `ChecksTotal`
- `AllowedTotal`
- `BlockedTotal`
- `MessagesSentTotal`
- `InCooldown`
- `LastActivityScore`
- `LastUpdated`

这部分更偏诊断/观测，不直接参与限流决策主逻辑。

## 执行逻辑

### Allow

执行顺序：

1. 检查当前冷却状态
- 若仍处于冷却时间内，直接拒绝

2. 读取统计数据
- `ChatStats`
- `HourlyStats`
- `RecentSends`

3. 计算派生指标
- `CurrentActivityScore`
- `CurrentBurstFactor`
- `TotalMessages1h`
- `TotalMessages24h`

4. 依次检查
- 最小发送间隔
- 1 小时上限
- 24 小时上限
- 爆发阈值

5. 如果命中限制
- 计算冷却时长
- 提升冷却等级
- 拒绝发送

6. 如果通过
- 记录一次 allow check
- 返回允许发送

### Record

执行顺序：

1. 追加最近发送记录
2. 增加总发送次数
3. 增加小时活跃度
4. 更新诊断 metrics 中的 `MessagesSentTotal`

### GetStats

执行顺序：

1. 读取基础统计
2. 读取小时活跃度
3. 读取最近发送记录
4. 计算派生指标
5. 返回带派生字段的统计快照

## 当前面板

### 1. ratelimit stats

当前已迁移到 schema v2 原生卡：
- builder: `stats_card.go`
- command handler: `handlers/ratelimit_handler.go`

这个面板的定位是“单会话详情诊断”。

当前实现特点：
- 详情读取先汇总成 `StatsSnapshot`
- 展示阈值统一收口到 `StatsDisplayPolicy`
- 再由 view model 渲染到 schema v2 raw card
- schema v2 基础 primitive 复用 `internal/infrastructure/lark_dal/larkmsg/card_v2.go`
- 顶部先给结论区和 3 个 Hero 指标
- 核心指标、诊断指标按两列块展示
- 最近发送只展示最近 5 条
- 当前版本已做过一次紧凑化，优先保证“信息层级 > 组件预算 > 视觉留白”

### 2. ratelimit list

当前仍是模板表格卡。

它的定位更接近“多会话概览”，和 `stats` 的信息结构不同，建议单独设计，不要直接复用 `stats` 面板布局。

## 修改时的注意点

1. 不要把诊断 metrics 和限流核心状态混为一谈
- `ChatMetrics` 更偏观测
- `ChatStats` 才是主决策数据

2. 新增指标时先判断属于哪一层
- 决策指标：优先进入 `ChatStats` / 派生计算
- 观测指标：优先进入 `ChatMetrics`

3. 修改面板时尽量先改 builder，不要把 handler 继续做胖

4. 如果引入卡片回调
- 先定义 action 协议
- 再决定 sync / async
- 不要回到模板卡私有刷新对象模式

5. 展示阈值统一修改 `StatsDisplayPolicy`
- 与小时/日上限、活跃度阈值相关的文案优先复用 `Config`
- 与纯视觉表达相关的阈值也集中放在 policy，不要散落回 builder

6. 详情面板涉及冷却状态时，优先使用 snapshot 中归一后的状态
- `cooldown` 真值来自独立 key
- 不要直接相信旧的 `ChatStats.Cooldown*` 或 `ChatMetrics.InCooldown`

## 建议的后续方向

1. 补 `ratelimit list` 的原生 v2 面板
2. 增加概览与详情之间的跳转
3. 根据拒绝原因增加更直接的风险解释
4. 如果后续面板继续增多，抽一层通用 card primitive，减少重复 builder 代码

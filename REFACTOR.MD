# REFACTOR

## 一句话结论

这是一个能工作的飞书机器人单体，但不是严格的 DDD 或 Clean Architecture。目录分层和真实依赖不一致，消息处理并发模型还有硬风险，不清理的话后续维护成本会持续上升。

## 1. 整体定位

- 主体是单进程 Go 服务，入口在 `cmd/larkrobot/main.go`。
- 启动后会一次性初始化 DB、OpenSearch、Ark、MinIO、检索、网易云、调度、飞书客户端等基础设施。
- 对外事件入口在 `internal/interfaces/lark`。
- 核心业务基本集中在 `internal/application/lark`。
- `pkg/xhandler` 和 `pkg/xcommand` 实际上是整个仓库的隐式框架层。

## 2. 架构判断

- 目录名使用了 `domain / application / infrastructure / interfaces`，但更像代码分组，不是严格架构边界。
- `application` 大量直接依赖 `infrastructure`，并不是依赖抽象。
- `domain` 很薄，目前几乎只有 `internal/domain/todo/model.go`。
- `infrastructure` 存在反向依赖 `application` 的情况，说明分层已经被打穿。
- 更准确的说法是：`internal/application/lark` 是核心业务层，其他目录是配套层，而不是标准分层架构。

## 3. 主执行链

1. 启动入口读取配置并初始化所有基础设施。
2. 飞书事件注册到消息、表情、卡片处理器。
3. 消息进入 `messages.Handler`，由 `Processor` 并发执行 operator 和 fetcher。
4. 命令由 `xcommand` 树解析并执行。
5. 聊天工具由 LLM function call 触发，调度器也复用同一批工具。

## 4. 关键问题

### 4.1 全局 Processor 并发复用

- 全局 `messages.Handler` 和 `reaction.Handler` 是可变对象。
- 每条消息会并发调用 `Clean().WithCtx().WithData().Run()`。
- `Processor` 把 `Context`、`data`、`metaData` 挂在实例字段上。
- 多消息并发时会互相覆盖，属于真实竞态问题，不是代码风格问题。

### 4.2 单条消息内部也共享无锁 metaData

- `RunParallelStages()` 会并发跑多个 fetcher 和 operator。
- `BaseMetaData` 没有同步保护。
- `Extra` 是普通 map。
- `IsCommand`、`SkipDone` 也会被不同 goroutine 读写。
- 行为会依赖调度时序，结果不稳定。

### 4.3 分层名义化，边界不真实

- `application` 直接依赖 infra repository、db model、config。
- `infrastructure` 甚至反向依赖 `application`。
- 这让后续重构、替换实现、补测试都越来越困难。

### 4.4 消息持久化逻辑重复且分散

- 入站消息记录在 `internal/application/lark/messages/handler.go`。
- 出站消息记录在 `internal/infrastructure/lark_dal/larkmsg/record.go`。
- `internal/application/lark/messages/ops/base.go` 里还有一份几乎重复的旧实现。
- 很难判断哪条链路才是现行主路径。

### 4.5 API 语义和实现不一致

- `history.Helper` 看起来是 builder。
- 但 `Index()` 记录了字段，`GetRaw()` 实际仍固定使用配置里的 `LarkMsgIndex`。
- `Query/Source/Size` 修改的是请求对象，却没有同步到 trace 展示字段。
- 这种 API 会误导调用者和后续维护者。

### 4.6 测试体系不稳定

- `go test ./...` 不能稳定通过。
- 部分测试依赖 `.dev/config.toml`，缺文件直接 panic。
- 部分测试依赖 `mockey`，在当前 Go 工具链下已经编不过。
- 说明测试体系对本地环境和版本耦合过深，不能作为稳定回归保障。

## 5. 不清晰的地方

### 5.1 domain 是否真的存在

- 如果要走领域建模，现在明显不够。
- 如果不打算走 DDD，保留这套目录命名会持续误导维护者。

### 5.2 消息生命周期的主责任人不清

- 到底是 `messages.Handler` 负责主编排。
- 还是 `larkmsg.Record*` 负责记录和扩展。
- 还是 `chunking` 在补充消息生命周期。
- 当前职责拆分不清楚。

### 5.3 工具体系有两套注册口径

- 一套给 LLM function call。
- 一套给 scheduler。
- 两套逻辑部分复用，但不是单一注册源，长期容易漂移。

### 5.4 schedule 的定位有点摇摆

- 一部分像业务模块。
- 一部分像通用工具执行器。
- 目前它既在做业务规则，也在做工具编排。

### 5.5 `script/neteaseapi/api_repo` 的仓库角色不清

- 它看起来像内嵌 Node 项目。
- 但仓库里没有说明它是 vendor、submodule、sidecar，还是部署依赖。

### 5.6 README 信息不足

- 当前 `README.md` 几乎没有解释架构、运行方式、依赖关系和开发约束。
- 新维护者很难快速建立正确心智模型。

## 6. 优先级建议

### P0

- 修正 `Processor` 全局共享实例的并发问题。
- 修正 `BaseMetaData` 并发访问问题。
- 统一消息处理主流程，删掉或隔离重复实现。

### P1

- 明确 `application` 与 `infrastructure` 的依赖策略。
- 给 `schedule`、`todo` 这类新模块划清边界。
- 修正 `history.Helper` 这类 API 语义不一致问题。

### P2

- 清理“向后兼容”全局变量和旧入口。
- 补 README 和开发/测试说明。
- 让测试摆脱 `.dev/config.toml` 和脆弱 monkey patch 依赖。

## 7. 建议目标结构

建议目标不是把仓库强行改造成教科书式 DDD，而是先收敛成一个边界清晰的模块化单体。

- `cmd`
  只负责启动、组装依赖、注册入口。
- `internal/interfaces`
  只负责飞书事件适配、请求解码、调用应用层。
- `internal/application`
  负责 use case、流程编排、工具注册、调度编排。
- `internal/domain`
  只保留真正有稳定业务规则的模型和规则。
- `internal/infrastructure`
  只保留数据库、搜索、对象存储、外部 API、消息发送等适配器。
- `pkg`
  只保留纯通用库，不依赖 `internal/application`。

推荐做法：

- 不要先做“大规模目录搬家”。
- 先修并发和主流程，再逐步收敛依赖方向。
- 如果短期内不会真正做领域建模，可以接受“弱化 domain，强化 application module”的方案。

## 8. 分阶段路线图

### Phase 0: 先止血

目标：先把会出错的地方修掉，让系统进入可控状态。

- 把全局 `Processor` 改成每次请求独立实例，或改造成无状态执行器。
- 移除 `BaseMetaData` 的并发共享写入。
- 明确消息处理主链，停止重复记录和重复 side effect。
- 给关键链路补最基本的回归测试。

阶段结果：

- 消息处理链路能在 `-race` 下跑通核心包。
- 不再依赖全局可变 handler 实例。
- 每条消息的上下文和状态不再互相污染。

### Phase 1: 收拢主流程

目标：让消息、命令、工具、调度的执行路径可以被人读懂。

- 确定唯一的消息持久化入口。
- 合并或明确两套工具注册口径。
- 修正 `history.Helper` 这类“看起来像 builder，但实际不是”的 API。
- 给主流程补一份架构说明和流程图。

阶段结果：

- 新人可以从入口一路追到消息处理、命令、工具、调度。
- 不再存在“旧实现还在，但不知道是否仍生效”的灰区。

### Phase 2: 收敛边界

目标：让目录名和依赖方向尽量一致。

- 新增一层 application-facing ports，逐步隔离 repo、config、search、message sender。
- 停止新增 `infrastructure -> application` 反向依赖。
- 逐步把 `todo`、`schedule` 这类模块改成“应用服务依赖接口，infra 提供实现”。
- 评估 `domain` 是否保留，以及保留哪些对象。

阶段结果：

- `application` 能以接口而不是具体 infra 类型工作。
- `infrastructure` 不再直接引用 application 常量或流程代码。

### Phase 3: 测试与开发体验

目标：让仓库变成“可稳定验证、可稳定接手”。

- 让配置加载支持测试专用入口，不再默认依赖 `.dev/config.toml`。
- 替换脆弱的 monkey patch 测试方式。
- 拆分 unit test 与 integration test。
- 补 README、启动说明、依赖说明、模块说明。

阶段结果：

- `go test ./...` 在约定环境下稳定通过。
- 本地和 CI 的行为一致。

## 9. 建议任务清单

### P0-1 消息处理器去全局状态

- 新建 `NewMessageProcessor()` 工厂型入口，避免复用全局可变实例。
- 检查 `messages.Handler`、`reaction.Handler`、其他全局 `Processor` 是否都有相同问题。
- 明确 `Processor.Run()` 输入输出，避免通过实例字段保存请求态数据。

交付标准：

- 不再有 `Clean().WithCtx().WithData().Run()` 这种共享实例调用方式。

### P0-2 清理并发共享 meta

- 重新设计 `BaseMetaData`。
- 能 immutable 就 immutable。
- 必须共享时显式加锁，或改成阶段结果汇总对象。
- 把 `Extra map[string]string` 改成受控结构，不再允许任意 goroutine 无锁写。

交付标准：

- `meta.IsCommand`、`meta.SkipDone`、`meta.Extra` 的写入规则明确且可测。

### P0-3 统一消息记录入口

- 选定一条主路径处理消息索引、检索入库、chunk 提交。
- 删除或冻结重复实现。
- 把“入站消息”和“出站消息”记录逻辑拆成明确服务，而不是分散在多个包里。

交付标准：

- 仓库里只能找到一套被正式使用的消息持久化主实现。

### P1-1 合并工具注册模型

- 明确哪些工具只能给 LLM 用。
- 明确哪些工具可以给 schedule 用。
- 注册入口尽量收敛成“单一源 + 可筛选能力”。

交付标准：

- 新增一个工具时，不需要同时修改多套容易漂移的注册逻辑。

### P1-2 修正误导性 API

- 重写 `history.Helper` 的状态保存逻辑。
- `Index/Query/Source/Size` 的链式调用必须和最终执行一致。
- trace 和 debug 输出必须反映真实查询。

交付标准：

- Helper 的字段、trace、真实请求三者一致。

### P1-3 明确 schedule 定位

- 决定它是业务模块，还是通用调度框架。
- 如果是业务模块，就保留 Lark 语义。
- 如果是通用调度框架，就把 Lark 事件类型从核心执行器里抽出去。

交付标准：

- `schedule/service.go` 和 `schedule/executor.go` 的职责边界清楚。

### P2-1 收敛依赖方向

- 新增 `application` 层接口，逐步替换对 infra 具体类型的直接引用。
- 先从 `todo`、`schedule`、`config` 开始。
- 再处理消息发送、消息记录、搜索与检索。

交付标准：

- 新模块默认不再直接依赖 `internal/infrastructure/*` 具体实现。

### P2-2 重建测试基线

- 给核心包加 race-safe 测试。
- 给依赖外部系统的测试打标签或移到 integration 目录。
- 去掉对本地私有配置文件的硬编码依赖。

交付标准：

- 开发者不需要准备一份神秘的 `.dev/config.toml` 才能跑最基本测试。

### P2-3 文档补齐

- README 补运行方式、依赖说明、环境要求。
- 增加架构文档，描述消息主流程和模块边界。
- 说明 `script/neteaseapi/api_repo` 的角色。

交付标准：

- 新维护者能在不读完整仓库的前提下建立基本心智模型。

## 10. 阶段验收标准

### 架构验收

- 不再出现新的 `infrastructure -> application` 反向依赖。
- 主流程图和代码路径一致。
- 新功能接入时能明确落在哪一层。

### 代码验收

- 不再复用全局可变 `Processor` 处理并发请求。
- `BaseMetaData` 不再被无锁并发写入。
- 消息记录逻辑只有一个正式入口。

### 测试验收

- 核心消息处理链路可执行 race 检测。
- 单元测试不依赖开发者本地私有配置。
- 集成测试与单元测试边界明确。

## 11. 执行原则

- 不做一次性大重构，按阶段推进。
- 每个阶段先控制风险，再做结构整理。
- 每个 PR 尽量只改变一条主线。
- 在 P0 和 P1 期间，尽量冻结会继续加重消息主流程复杂度的新功能。
- 不要先追求“纯净分层”，先追求“边界清晰、可稳定验证”。

## 12. 最终判断

这个仓库的问题不是完全失控，而是已经跑起来了，但架构表达和真实实现脱节。

- 短期最大风险：并发模型。
- 中期最大风险：职责分散。
- 长期最大风险：边界模糊导致重构成本持续放大。

如果要继续扩展功能，建议先完成 P0，再进入结构整理和文档补全。

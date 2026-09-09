# 当前架构审查与优化建议

审查日期：2026-09-08。审查基线：`58910c7`。用户已确认第 6 节方案，生命周期修复和装配拆分已实现；第 4 节保留基线发现并标记本批处理状态。瑞幸下单专项分析见 [独立审查报告](./2026-09-08-luckin-ordering-review.md)。

## 1. 审查范围与结论

扫描 `cmd`、`internal`、`pkg` 的 Go 文件和仓库内导入关系，阅读入口装配、运行时、消息处理、记录、存储契约及管理接口；前端仅检查工程结构，未做页面级审查。结合现有 ADR、治理文档和测试判断，不直接沿用旧重构报告的结论。

当前后端是模块化单体，前端独立构建。运行时治理已落地，应用契约与基础设施适配器也已在部分新模块建立；主要债务集中在启动失败清理、装配规模、旧业务副作用的归属，以及全局依赖造成的测试隔离困难。

没有运行负载测试或采集生产性能数据，因此本报告不承诺吞吐量、时延或内存收益。

## 2. 规模与模块职责

统计口径：仅上述三个目录；生产代码排除 `_test.go`，生成代码以文件头 `Code generated ... DO NOT EDIT` 标记识别，行数包含注释与空行。

- 共 771 个 Go 文件：547 个生产文件、224 个测试文件。
- 排除生成代码后为 439 个生产文件、100,946 行；含生成代码为 155,501 行。
- 108 个 Go 包目录中，79 个目录有测试文件。这是测试分布，不能作为覆盖率。
- 44,722 行的 `akshareapi/catalog_generated.go` 是生成目录，不应按手写大文件优先拆分。

| 模块 | 非生成生产文件数 | 行数 | 职责 |
| --- | ---: | ---: | --- |
| `cmd` | 11 | 3,662 | 机器人与运维工具入口、依赖装配 |
| `internal/runtime` | 9 | 1,643 | 模块生命周期、执行器、健康、入口模块 |
| `internal/interfaces` | 11 | 3,941 | 飞书事件适配、管理 REST API |
| `internal/application` | 210 | 54,240 | 业务编排、策略、部分领域模型与接口 |
| `internal/domain` | 1 | 120 | Todo 模型 |
| `internal/infrastructure` | 149 | 31,652 | 外部服务与存储适配，兼有历史业务逻辑 |
| `pkg` | 43 | 4,655 | 项目共享工具与框架 |

表格不包含 `internal/xmodel`、`internal/testsupport`、`internal/tools`，总数包含这些目录。

## 3. 应保留的架构成果

1. **统一生命周期**：`cmd/larkrobot/main.go` 已使用 `App.Start/Stop` 和信号上下文；`internal/runtime` 提供统一的模块及健康契约，遵循 ADR 0001。
2. **有界执行器**：入口、记录、分块、调度、会话续跑和投影有显式 worker、queue、timeout 参数；继续沿用现有背压语义。
3. **请求状态隔离**：`messages.MessageHandler.Run` 调用 `processor.NewExecution()`；`BaseMetaData` 已有同步访问方法。旧报告关于直接共享 Processor 请求状态的结论不能直接用于当前版本，这也不代表所有公开字段访问都已通过竞态审查。
4. **应用契约与存储适配**：`agentruntime.Store` 定义在应用包，`agentstore.Repository` 显式声明实现它。基础设施导入应用契约属于合理的依赖倒置，不应通过禁止全部 `infrastructure → application` 导入来治理。
5. **管理接口注入依赖**：`internal/interfaces/webui.Options` 显式接收配置管理器、查询能力等，并复用应用服务。

## 4. 发现与优先级

### P1：启动失败时资源所有权不完整

本批已处理：`internal/runtime/app.go` 清理当前失败模块并更新回滚状态，错误保留与取消隔离通过故障注入测试验证。以下描述为修改前证据。

证据：`internal/runtime/module.go` 明确允许 `Init` 分配客户端；`app.go` 却在 `module.Start` 成功之后才将模块加入 `started`。

因此，模块 `Init` 已分配资源、`Start` 返回错误时，`handleStartError` 的回滚列表不包含当前模块。可选模块在 Init/Start 失败后直接继续，也不会进入正常停止列表。失败模块自行清理可以规避，但生命周期契约没有统一要求或保证。

实际路径：`bootstrap.go` 中 Redis 模块的 `Start` 调用 `redis_dal.Init → Ping → GetRedisClient`。客户端可在 Ping 失败前创建，而启动失败后运行时不会调用该模块的 `Close`。生产进程随后退出会释放进程资源，但启动回滚、测试内嵌运行和未来恢复逻辑仍缺少完整清理保证。

另外，`stopStarted` 不更新被回滚模块的健康状态，且沿用启动上下文；若该上下文已取消，依赖上下文的清理可能无法完成。这些是静态路径发现，尚未新增故障注入测试。

### P1：装配根仍集中承担多个业务域

本批已处理：`bootstrap.go` 缩减至 37 行；组件、基础设施、搜索、应用和评估移入五个职责文件，scheduler 归入 `appComponents`。以下为修改前规模。

证据：`cmd/larkrobot/bootstrap.go` 1,386 行，含组件构造、基础设施、搜索 schema、执行器、应用服务、会话评估和辅助函数。`addApplicationModules` 自身跨越约 330 行，`scheduler` 仍是包级变量。

影响：增加一个模块需要理解大量无关装配细节；生命周期顺序和共享对象所有权难以局部审查。装配根有高依赖数量本身合理，问题在于职责组织和可变状态。

### P1：消息记录编排仍横跨应用层与 DAL

证据：入站逻辑位于 `application/lark/messages/recording/service.go`；出站逻辑位于 `infrastructure/lark_dal/larkmsg/record.go`。后者同时执行分词、embedding、搜索写入、检索写入及应用层 chunk 提交，并存在直接异步 trace 写入。

入站使用动态配置 accessor 选择索引，出站有读取静态 `config.Get().OpensearchConfig.LarkMsgIndex` 的路径；两者 embedding 失败策略也不同。两套策略是否有意区分尚需结合业务语义确认，不能直接合并后宣称等价。

方向：由应用层承担记录流程，DAL 提供发送结果；先建立统一记录输入和可注入依赖，再分入站、出站逐步迁移。保留隐私模式、去重、租户隔离、token 归属和回复链字段。

### P2：共享包和测试依赖边界不稳定

本批局部处理：runtime 日志改为 `AppOptions.Logger` 注入；三个搜索测试改为构造时注入 provisioner 工厂。消息处理与共享包的其余边界债务仍保留。

证据：`pkg/xhandler` 导入业务身份与意图类型；`pkg/xchunk` 导入 DB、Ark、搜索和缓存等；消息处理存在 `getChatName/getUserNameByID` 包级函数变量，runtime 测试重赋值 `optionalModuleErrorLogf`。

影响：泛型处理器与平台业务绑定，包级替换造成测试间共享状态。仓库 `Agents.md` 已要求 fake/stub 经显式注入，当前部分代码与该约定不一致。

方向：先修正在本次优化范围内触碰的可变依赖；按实际消费者提取小接口，不为所有 DAL 统一生成抽象，也不批量移动目录。

### P2：后台任务生命周期仍有遗漏

证据：`recording.CollectMessage` 未配置 submitter 时回退为裸 goroutine；出站记录直接 `go utils.AddTrace2DB`；`geocode.NewCached` 启动缓存清理循环而没有暴露关闭方法。

方向：让装配代码承担启动和停止；热点任务使用现有 Executor，缓存清理线程由所属模块管理。不能仅凭存在 `go` 关键字认定问题，需要判断其所有权和停止路径。

### P2：构建与架构文档漂移

README 原先列出 Go 1.25.1、Hertz 和完整领域分层，已与 `go.mod`、`net/http` 管理服务及实际模型分布不符，本次修订。

CI 的 `docker-image-lark.yaml` 仍设置 Go 1.25；实际编译在 Docker 中执行，镜像使用浮动 `golang:alpine`，不能据此断言生产二进制由 Go 1.25 编译。`merge_check.yaml` 做 Docker 构建，没有显式 Go 单测步骤。建议后续统一版本来源并增加不依赖真实服务的测试门禁。

## 5. 优化路线选择

| 路线 | 收益 | 代价与局限 |
| --- | --- | --- |
| A：先完善生命周期，再拆分装配（推荐） | 解决具体失败路径，减少装配变更范围 | 需要明确部分初始化失败时的清理契约 |
| B：先统一消息记录服务 | 收敛重复编排、索引策略和后台任务 | 涉及隐私、去重和副作用语义，回归范围更大 |
| C：全仓重排为严格领域分层 | 目录和依赖规范更统一 | 当前收益缺少证据，迁移量大，易将合理接口依赖误判为越层 |

## 6. 已确认并实施的首批设计

### 6.1 生命周期资源清理

涉及 `internal/runtime/app.go`、`module.go` 和 `app_test.go`，并审查现有 Module 的 Stop 是否适用于部分初始化状态。

- 明确 Init/Start 失败后的资源释放责任；进入 Init 的模块必须允许清理其部分初始化资源。
- Init 阶段显式禁用必须不持有资源；Start/Ready 阶段禁用时释放此前获得的资源。
- critical 失败先清理当前模块，再逆序清理此前成功模块；同一轮启动中的模块不重复停止。
- optional 在 Init/Start 失败时清理当前模块并保留 degraded 状态，后续模块继续启动；Ready 失败保留现有可降级运行语义，正常退出时仍清理。
- 回滚使用脱离启动取消信号、具有明确超时预算的清理上下文；保留启动错误与清理错误，并使健康状态反映回滚结果。
- 将可变日志入口改为实例级注入，用 fake/stub 验证行为。

验收使用故障注入覆盖 Init/Start/Ready 失败、disabled、optional 降级、逆序回滚、清理错误聚合、启动取消和防重复清理。测试不访问真实 Redis/PostgreSQL，不依靠包级函数重赋值。

### 6.2 装配职责拆分

保持 `cmd/larkrobot` 为装配根，把 `bootstrap.go` 按职责拆为组件构造、基础设施装配、搜索装配、应用装配和评估装配文件；共享 scheduler 归入 `appComponents`。

这一步保持模块名称、注册顺序、critical/optional 设置、默认预算和对外行为。已有 bootstrap 测试用于保护注册拓扑与配置拒绝条件；必要时补充缺失的行为断言。

本批不涉及数据库 schema、API、模型选择或依赖版本升级。消息记录统一作为独立后续批次，因为需要单独确认其失败与去重语义。

## 7. 本次完成与验证范围

已完成代码结构审查、README 修订，以及第 6 节的 Go 实现。工作分支为 `codex/runtime-cleanup-20260908`，变更保留在工作区，未部署或修改数据库 schema。

已读取 `.vscode/launch.json`、`.vscode/settings.json`，核对根 `AGENTS.md` 与 README 的测试参数快照。LaunchBot 和活动测试参数与记录一致；未启用注释中的 gcflags。未修改本地配置或测试基线。

宿主机 PATH 默认 Go 1.27.0，实际验证显式使用已安装的 Go 1.26.0，并禁止自动切换工具链：

```sh
GOTOOLCHAIN=local \
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
/root/.go/go1.26.0/bin/go test -v -tags=custom_skip_vips \
  ./internal/runtime ./pkg/xhandler
```

两个包均通过，命令退出码 0。该结果是现有基线验证，不证明未实现的优化正确；未运行全仓测试、race 检查、前端测试、生产集成或负载测试。

上段记录的是实现前基线。本批新增故障注入测试已先在原实现上失败，复现当前模块漏 Stop、回滚状态仍为 ready、disabled 资源滞留、清理继承取消信号、清理错误丢失；修复后 runtime、xhandler 和 cmd/larkrobot 组合测试通过。

新增覆盖还包括 optional 清理失败的状态/日志、禁用时清理失败的 critical/optional 分支、整个回滚共享截止时间、普通 Stop 错误聚合和避免重复清理。

实现后验证结果：三个目标包共 53 个顶层测试通过；`go test -v -race -tags=custom_skip_vips ./internal/runtime` 通过，未报告数据竞态。命令均使用上述 Go 1.26.0、`GOTOOLCHAIN=local` 和主工作区配置路径。未运行全仓测试、前端测试、真实服务集成或负载测试。独立代码审查未发现阻塞性回归；文档格式和差异空白检查通过。

保留的实际限制：scheduler、卡片 PatchReconciler、瑞幸 OrderPoller 的部分 Stop 路径使用无 context 的等待，因此 App 的 30 秒预算依赖模块协作，不能视为硬性进程退出上限；LarkWS 仍有既存关闭限制。本批未宣称解决这些独立生命周期问题。

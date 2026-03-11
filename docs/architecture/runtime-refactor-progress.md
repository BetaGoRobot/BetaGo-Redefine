# Runtime Refactor Progress

持续记录 `docs/architecture/runtime-refactor-plan.md` 的分阶段落地结果，避免“计划在文档里、实现散落在提交里”。

## 2026-03-11 · Milestone A · Sprint 2 / Task 2.1 主链路身份抽取收口

### 方案

- 在 `internal/application/lark/botidentity` 增加共享的 `UserIdentity` resolver，把 `OpenID` 优先、`UserId` 兼容 fallback 的规则收敛到一个入口。
- 在 `pkg/xhandler.BaseMetaData` 增加 `UserIDType`，把“当前 `UserID` 是 open_id 还是 legacy fallback”沿主链路传递下去，避免各层重复猜测。
- message / reaction / handler compat 三条主路径统一消费共享 extractor，不再各自手写 `OpenId -> UserId` fallback。
- legacy `UserId` fallback 改为显式记录：入口会保留兼容能力，但会在运行期打 warning，便于后续做 allowlist 清理。

### 修改

- 新增 `internal/application/lark/botidentity/user_identity.go`，提供：
  - `ResolveLarkUserID`
  - `ResolveMessageSender`
  - `ResolveReactionUser`
  - `ResolveStoredUserIdentity`
- 调整 `pkg/xhandler/base.go`：
  - `BaseMetaData` 新增 `UserIDType`
  - `Processor` 新增 `MetaData()`
- 收口消息主链路：
  - `internal/application/lark/messages/handler.go`
  - `internal/application/lark/messages/ops/common.go`
- 收口 reaction 主链路：
  - `internal/application/lark/reaction/base.go`
  - `internal/application/lark/reaction/follow_react.go`
  - `internal/application/lark/reaction/record_reaction.go`
- 收口兼容 handler：
  - `internal/application/lark/handlers/schedule_compat.go`
  - `internal/interfaces/lark/handler.go`

### 决策

- self-message 过滤只在拿到 canonical `OpenID` 时执行；如果事件只带 legacy `UserId`，不再冒险做跨语义比较。
- reaction 记录链路如果缺少 `OpenID`，会保留 warning 并跳过 `open_id` 专属的用户查询 / 统计入库，避免把 legacy `UserId` 混入 `OpenID` 语义字段。
- 旧 `UserId` 兼容仍然保留在 extractor 层，但不再允许散落在 handler / op / reaction helper 中重复实现。

## 2026-03-11 · Milestone B · Sprint 1 / Task 1.2 + Sprint 2 下游补齐

### 方案

- 对 message 下游消费链补齐统一 identity 语义，优先覆盖 recording / chunking 这两个仍直接读取 `SenderId.OpenId` 的分支。
- 能同步执行的轻量任务不再额外起匿名 goroutine；先把消息 trace 落库这类 helper 异步从默认路径里拿掉。

### 修改

- `internal/application/lark/messages/handler.go`
  - `utils.AddTrace2DB(...)` 从裸 goroutine 改为同步执行，减少一条默认 fire-and-forget 路径。
- `internal/application/lark/messages/recording/service.go`
  - 统一通过 shared identity resolver 取用户身份。
  - 缺少 `OpenID` 时跳过 `GetUserInfoCache(open_id)`，但仍保留消息索引里的主身份字段。
- `internal/application/lark/chunking/msg.go`
  - 统一通过 shared identity resolver 处理发送者身份与 bot self 判断。
  - 对仅 legacy `UserId` 的消息不再直接解引用 `OpenId`。

### 决策

- recording / chunking 下游允许继续消费“主身份字符串”，但凡是依赖 `open_id` 语义的外部查询，都必须先确认 `HasOpenID()`。
- 这一步先解决 panic / 直读 / 语义漂移问题，不在本次顺手重做 recording executor fallback；该问题仍按 Sprint 1 的 executor 收口路径继续推进。

## 2026-03-11 · 补充修正 · 权限管理错误语义稳定化

### 修改

- `internal/application/permission/authorization.go`
  - 未授权错误恢复为稳定错误文案。
  - 当前操作者和 bot namespace 改为打 warning 日志，而不是拼进用户可见错误字符串。

### 原因

- 便于测试与前端展示稳定匹配错误语义。
- 审计信息保留在日志层，避免把运行时上下文泄露到交互文案里。

## 验证

- `go test ./internal/application/lark/botidentity ./internal/application/lark/messages ./internal/application/lark/reaction ./internal/application/lark/handlers ./internal/application/lark/chunking ./internal/application/permission ./internal/application/config ./internal/interfaces/lark`

## 2026-03-11 · Milestone C · Sprint 2 / Task 2.2 主路径 `config.Get()` 收口

### 方案

- 给 `internal/application/config` 补上字符串配置与业务布尔开关读取能力，让业务代码不必再直接依赖 TOML struct。
- 对主消息链路、意图识别、音乐卡片、history/debug/word count 等高频业务包统一切到 accessor / manager。
- 机器人身份相关读取统一切到 `botidentity.Current()`，避免在业务包里直接读 `LarkConfig.BotOpenID/AppID`。
- 对 `Manager` 增加“DB 未初始化时 fail-soft 到 TOML/default”的语义，避免测试与早期初始化阶段因动态配置查询空指针而崩溃。

### 修改

- 扩展 `internal/application/config/manager.go`
  - 新增 `GetString`
  - 新增业务配置 key：
    - `chat_reasoning_model`
    - `chat_normal_model`
    - `intent_lite_model`
    - `lark_msg_index`
    - `lark_chunk_index`
    - `music_card_in_thread`
    - `with_draw_replace`
  - `getConfigByFullKeyWithOptions(...)` 在 DB 不可用时直接返回 miss
- 扩展 `internal/application/config/accessor.go`
  - 新增 `ChatReasoningModel`
  - 新增 `ChatNormalModel`
  - 新增 `IntentLiteModel`
  - 新增 `LarkMsgIndex`
  - 新增 `LarkChunkIndex`
  - 新增 `MusicCardInThread`
  - 新增 `WithDrawReplace`
- 新增 `internal/application/config/runtime_config.go`
  - 先作为过渡层承接 `ratelimit` 的静态配置读取，避免业务包直接碰 `config.Get()`

### 覆盖的业务包

- `internal/application/lark/handlers/chat_handler.go`
- `internal/application/lark/intent/recognizer.go`
- `internal/application/lark/handlers/music_handler.go`
- `internal/application/lark/card_handlers/handler.go`
- `internal/application/lark/messages/recording/service.go`
- `internal/application/lark/handlers/debug_handler.go`
- `internal/application/lark/handlers/word_count_handler.go`
- `internal/application/lark/history/search.go`
- `internal/application/lark/history/msg.go`
- `internal/application/lark/chunking/common.go`
- `internal/application/lark/handlers/image_handler.go`（通过 bot identity 收口）
- `internal/application/lark/ratelimit/rate_limiter.go`

### 决策

- 对 index 名、模型名这类“本质是静态 infra config，但被业务主路径频繁消费”的值，当前先统一通过 config package 暴露的 accessor / helper 读取；后续如需要可再演进成真正的动态配置项。
- `botidentity.Current()` 仍然是允许的静态 bootstrap 入口，因此仓库中保留 `internal/application/lark/botidentity/identity.go` 这一处 `config.Get()`。
- `Manager` 在 DB 缺失时 fail-soft，不把“动态配置系统尚未 ready”升级成调用侧 panic。

## 2026-03-11 · Milestone D · Sprint 2 / Task 2.3 配置治理回归守卫

### 修改

- 新增 `internal/application/config/governance_test.go`
  - 扫描 `internal/application/lark` 与 `internal/interfaces/lark`
  - 禁止新增直接 `config.Get()`
  - 禁止在业务路径散落新增 `.UserId` 读取
  - 禁止在业务路径散落新增裸 `go func()`
  - allowlist 当前仅保留 `internal/application/lark/botidentity/identity.go`

### 验证

- `go test ./internal/application/config ./internal/application/lark/ratelimit ./internal/application/lark/intent ./internal/application/lark/messages ./internal/application/lark/chunking ./internal/application/lark/history ./internal/application/lark/handlers ./internal/application/lark/card_handlers ./internal/interfaces/lark`
- `rg -n "config\\.Get\\(" internal/application internal/interfaces`

## 2026-03-11 · Milestone E · 移除 legacy identity 兼容层，主链路统一为 `OpenID`

### 方案

- 删除上一轮引入的 `UserIdentity` 兼容层，不再对 legacy `UserId` 做 fallback。
- runtime 主链路默认只读取事件里的 canonical `OpenID`；没有 `OpenID` 就视为缺失，而不是再推断成“兼容身份”。
- 把 runtime 边界上的核心 helper / metadata / method 命名改成 `OpenID`，减少“变量名是 userID，实际承载的是 OpenID”的认知噪音。

### 修改

- `internal/application/lark/botidentity/open_id.go`
  - 新增 `MessageSenderOpenID(...)`
  - 新增 `ReactionOpenID(...)`
  - 删除旧的 `user_identity.go`
- `pkg/xhandler/base.go`
  - 删除 `BaseMetaData.UserIDType`
  - `MetaDataWithUser` → `MetaDataWithOpenID`
  - `GetUserID()` → `GetOpenID()`
- `pkg/xhandler/tools.go`
  - `NewBaseMetaDataWithChatIDUID(...)` → `NewBaseMetaDataWithChatIDOpenID(...)`
- `internal/application/lark/messages/*`
  - `messageUserID(...)` → `messageOpenID(...)`
  - message meta 初始化仅写入 `OpenID`
- `internal/application/lark/reaction/*`
  - `reactionUserID(...)` → `reactionOpenID(...)`
  - reaction meta 初始化仅写入 `OpenID`
- `internal/application/lark/handlers/schedule_compat.go`
  - `currentUserID(...)` → `currentOpenID(...)`
- `internal/application/lark/cardaction/registry.go`
  - `Context.UserID()` → `Context.OpenID()`
- `internal/application/config/accessor.go`
  - `Accessor.userID` → `Accessor.openID`

### 决策

- 这一步只统一 runtime / application 主链路的命名与语义；数据库列名、JSON tag、卡片 action payload 字段仍保留 `user_id` 兼容命名，避免破坏外部契约。
- 也就是说：Go 运行时代码默认往 `OpenID` 靠齐，但存储/协议层字段名的彻底迁移另算一批兼容性工作。

### 验证

- `go test ./internal/application/config ./internal/application/lark/botidentity ./internal/application/lark/messages ./internal/application/lark/reaction ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/lark/history ./internal/application/lark/intent ./internal/application/lark/ratelimit ./internal/interfaces/lark ./pkg/xhandler`

## 2026-03-11 · Milestone F · 第二波 `OpenID` 命名收敛，清理 DTO / ViewModel / helper 遗留

### 方案

- 在不破坏外部契约的前提下，把“Go 代码里变量名/字段名叫 `UserID`，实际语义却是 `OpenID`”的残留继续向内收敛。
- 这一轮优先处理 application / history / permission / xmodel 以及少量 infrastructure helper 的 Go 命名，不碰数据库列名、OpenSearch 字段名、卡片 payload key、第三方 SDK struct 字段。
- 对纯协议兼容字段，如果必须保留 `json:\"user_id\"`，则优先改 Go 字段名或局部变量名，而不是动线上存储/协议格式。

### 修改

- `internal/application/config/*`
  - `ConfigItem.UserID` → `ConfigItem.OpenID`
  - `FeatureActionRequest.UserID` → `FeatureActionRequest.OpenID`
  - `ConfigActionRequest.UserID / ActorUserID` → `OpenID / ActorOpenID`
  - `ConfigViewRequest.UserID` → `ConfigViewRequest.OpenID`
  - `configLookupCandidate.userID` → `openID`
  - `Manager.ConfigEntry.UserID` → `ConfigEntry.OpenID`
  - `BuildConfigCard* / BuildFeatureCard / HandleConfigAction / Parse*Request` 等函数参数、局部变量统一改为 `openID`
- `internal/application/lark/history/*`
  - `HybridSearchRequest.UserID` → `OpenID`
  - `OpensearchMsgLog.UserID` → `OpenID`
  - 搜索过滤仍写入 `user_id` term，但请求对象和调用侧全部按 `OpenID` 命名
- `internal/xmodel/models.go`
  - `MessageIndex.UserID` → `OpenID`
  - `CardActionIndex.UserID` → `OpenID`
  - `MessageChunkLogV3.UserIDs` → `OpenIDs`
  - `User.UserID` → `User.OpenID`
- `internal/application/lark/handlers/*`
  - config / permission / history / chat / debug / word_count 等 handler 内部请求构造与局部变量统一改为 `OpenID`
  - 依赖 `xmodel.MessageIndex` 的消费侧全部切到 `OpenID`
- `internal/application/permission/*`
  - `TargetUserID / ActorUserID` → `TargetOpenID / ActorOpenID`
  - 权限面板与 action request 的 Go 侧语义统一为 `OpenID`
- `internal/infrastructure/ark_dal/responses.go`
  - `New(..., userID, ...)` → `New(..., openID, ...)`
- `internal/infrastructure/lark_dal/larkuser/user.go`
  - `GetUserInfo*` / cache helper 的局部参数统一改为 `openID`
- `internal/infrastructure/lark_dal/larkmsg/content.go`
  - `AtUser(userID, userName)` → `AtUser(openID, userName)`

### 决策

- 保留以下兼容层不动：
  - JSON tag / DB 列名 / OpenSearch 字段名里的 `user_id`
  - 卡片 action payload 字段常量，如 `cardaction.UserIDField`
  - 第三方 SDK / 模板协议结构体里已经约定好的 `UserID`
- 对 application 自己定义的协议辅助结构，如果仍需要承接 legacy `user_id`，优先通过更明确的 Go 字段名表达“仅兼容，不参与主链路语义”。

### 验证

- `go test ./internal/application/config ./internal/application/lark/history ./internal/application/lark/handlers ./internal/application/lark/cardaction ./internal/application/permission ./internal/application/lark/messages ./internal/xmodel ./pkg/xhandler ./internal/infrastructure/ark_dal ./internal/infrastructure/lark_dal/larkmsg ./internal/infrastructure/lark_dal/larkuser`

## 2026-03-11 · Milestone G · 手工 schema v2 卡片统一 refresh footer 与 view 回放

### 方案

- 给手工 schema v2 卡片统一补一个可选 refresh footer 能力，而不是每张卡片各自手写刷新按钮。
- footer 只负责渲染 `刷新` 按钮；具体 payload / parser / rebuild handler 仍保留在业务层，避免 `larkmsg` 反向依赖业务协议。
- 对已经具备“view”语义的卡片直接复用原有 action：
  - `config` 复用 `config.view_scope`
  - `permission` 复用 `permission.view`
- 对缺失 view 回调的卡片补齐显式 refresh action：
  - `feature.view`
  - `schedule.view`
  - `ratelimit.view`

### 修改

- `internal/infrastructure/lark_dal/larkmsg/*`
  - `AppendStandardCardFooter(...)` 新增 `StandardCardFooterOptions`
  - 支持可选 `RefreshPayload`
  - 文档与测试补齐
- `internal/application/config/*`
  - config / feature 卡片 footer 注入 refresh payload
  - 新增 `feature.view` payload builder / parser
- `internal/application/permission/*`
  - 权限面板 footer 注入 `permission.view`
- `internal/application/lark/schedule/*`
  - 新增 `schedule.view` payload builder / parser
  - schedule 卡片 footer 注入 refresh payload
- `internal/application/lark/ratelimit/*`
  - 新增 `ratelimit.view` payload builder / parser
  - stats 卡片 footer 注入 refresh payload
- `internal/application/lark/cardaction/builtin.go`
  - 注册 `feature.view` / `schedule.view` / `ratelimit.view`
  - 回调时直接按 payload 重建最新卡片
- `pkg/cardaction/action.go`
  - 增加上述新 action 常量

### 决策

- refresh 按钮统一放在 footer，避免破坏各卡片主体布局，也让人工卡片的交互 affordance 保持一致。
- `撤回` 继续使用 `danger_filled`，保持“高风险操作”视觉语义清晰；refresh 保持次级按钮样式。
- 这一步只统一“原生手工卡”的 refresh；模板卡已有自己的 `refresh_obj` 机制，暂不强行合流。

### 验证

- `go test ./internal/infrastructure/lark_dal/larkmsg ./internal/application/config ./internal/application/permission ./internal/application/lark/schedule ./internal/application/lark/ratelimit ./internal/application/lark/cardaction`

## 2026-03-11 · Milestone H · 手工管理卡视觉基线统一

### 方案

- 在不动模板卡的前提下，把手工 schema v2 管理卡收口到同一套视觉基线，避免 `config / permission / schedule / ratelimit` 各自演化。
- 视觉基线统一只收口“壳层”和“按钮语义”，不改业务布局：
  - 统一 panel header / padding
  - 统一主次按钮语义
  - 保留正文结构和业务指标排版

### 修改

- `internal/infrastructure/lark_dal/larkmsg/card_v2.go`
  - 新增 `StandardPanelCardV2Options()`
- `internal/application/config/card_view.go`
  - 配置 / feature 面板切到统一 panel options
  - `启用`、已选 scope、`取消屏蔽` 统一用 `primary_filled`
- `internal/application/permission/card.go`
  - 权限面板切到统一 panel options
  - `查看用户`、`授予` 统一用 `primary_filled`
- `internal/application/lark/schedule/card_view.go`
  - schedule 面板切到统一 panel options
  - `恢复` 统一用 `primary_filled`
- `internal/application/lark/ratelimit/stats_card.go`
  - ratelimit 面板 header 改为统一的 `wathet`
  - 状态颜色继续保留在 overview 文本，不再通过 header template 表达

### 决策

- `撤回` 仍然独立使用 `danger_filled`，因为它是全局高风险操作，不与业务按钮共用语义。
- `ratelimit` 去掉动态 header 后，状态信息仍由 overview block 的红/橙/绿文本承担，避免信息损失。
- 这一步只统一“面板壳层”，不强制把所有正文 section 抽成同一套通用组件，避免过度抽象。

### 验证

- `go test ./internal/infrastructure/lark_dal/larkmsg ./internal/application/config ./internal/application/permission ./internal/application/lark/schedule ./internal/application/lark/ratelimit`

## 2026-03-11 · Milestone I · 手工卡片公共壳层继续下沉

### 方案

- 继续把已经稳定的跨卡片重复逻辑下沉到 `larkmsg`，避免 `config / permission / schedule / ratelimit` 各自维护一份薄包装。
- 这一轮只抽“确定稳定”的公共壳层：
  - 标准 panel 发卡入口
  - `map[string]string -> map[string]any` payload 转换
  - footer 统一更新时间文案
- 暂不抽正文 section / metrics / form DSL，避免把不同业务强行揉成一套过度泛化接口。

### 修改

- `internal/infrastructure/lark_dal/larkmsg/card_v2.go`
  - 新增 `NewStandardPanelCard(...)`
  - 新增 `StringMapToAnyMap(...)`
- `internal/infrastructure/lark_dal/larkmsg/card_v2_footer.go`
  - footer 左侧新增灰色小号更新时间
- `internal/application/config/card_view.go`
  - 删除本地 `toAnyMap`
  - `newRawCard(...)` 改走 `NewStandardPanelCard(...)`
- `internal/application/permission/card.go`
  - 删除本地 `toAnyMap`
  - payload / 发卡全部改走 `larkmsg` helper
- `internal/application/lark/schedule/card_view.go`
  - 删除本地 `stringMapToAnyMap`
- `internal/application/lark/ratelimit/stats_card.go`
  - 删除本地 `stringMapToAnyMap`

### 决策

- 更新时间统一放 footer，而不是正文 header / hint，避免污染业务内容区域，并且所有手工卡片都能零配置继承。
- 时间格式先收口为 `MM-DD HH:MM:SS`，兼顾可读性与 footer 长度。
- `StringMapToAnyMap(...)` 放在 `larkmsg` 而不是 `cardaction`，因为它服务的是“卡片 payload 组装”，而不是 action 协议本身。

### 验证

- `go test ./internal/infrastructure/lark_dal/larkmsg ./internal/application/config ./internal/application/permission ./internal/application/lark/schedule ./internal/application/lark/ratelimit ./internal/application/lark/cardaction ./internal/application/lark/handlers ./internal/application/lark/command`

## 2026-03-11 · Milestone J · 继续下沉双栏布局与 section 拼接

### 方案

- 继续只抽“已经在多个手工卡片稳定重复”的布局壳，避免业务层继续手写相同的 `ColumnSet + Column + Divider loop`。
- 这一轮重点收口两类重复：
  - 双栏分栏壳
  - 多 section 之间插 divider 的拼接逻辑

### 修改

- `internal/infrastructure/lark_dal/larkmsg/card_v2.go`
  - 新增 `SplitColumns(...)`
  - 新增 `AppendSectionsWithDividers(...)`
- `internal/application/config/card_view.go`
  - config / feature 列表改走 `AppendSectionsWithDividers(...)`
  - 配置项左右栏改走 `SplitColumns(...)`
- `internal/application/permission/card.go`
  - 权限点列表 / 额外授权列表改走 `AppendSectionsWithDividers(...)`
  - 目标用户表单、权限点 section、scope 控制、额外授权 section 改走 `SplitColumns(...)`
- `internal/application/lark/schedule/card_view.go`
  - task 列表 section 改走 `AppendSectionsWithDividers(...)`
- `internal/application/lark/ratelimit/stats_card.go`
  - overview 区块改走 `SplitColumns(...)`

### 决策

- `SplitColumns(...)` 只处理“两列壳层”，列内具体内容和行级配置仍由业务层传入，避免把业务信息结构下沉过深。
- `AppendSectionsWithDividers(...)` 只负责“非空 section 之间插分割线”，不会猜测首尾 divider，首尾是否保留由业务层显式决定。

### 验证

- `go test ./internal/infrastructure/lark_dal/larkmsg ./internal/application/config ./internal/application/permission ./internal/application/lark/schedule ./internal/application/lark/ratelimit`

## 2026-03-11 · Milestone K · Schedule 触发来源回链

### 方案

- 给 `scheduled_tasks` 持久化一份 `source_message_id`，把“这个 schedule 是从哪条消息创建出来的”收口成显式数据，而不是运行时猜测。
- schedule 执行时构造最小消息上下文，把来源 `msgID` 和 `chatID` 透传给 schedulable tool，让已有的 `sendCompatible*` / `currentMessageID(...)` 逻辑自然复用“回复原消息”的路径。
- 对 reply 型发送增加 fail-soft：优先回复原消息，回复失败时再回落到当前 chat 直接发消息，避免因为来源消息失效导致整次任务对用户完全不可见。

### 修改

- `script/migrations/010_add_source_message_to_scheduled_tasks.sql`
  - 为 `betago.scheduled_tasks` 新增 `source_message_id`
- `internal/infrastructure/db/model/scheduled_tasks.gen.go`
  - `ScheduledTask` 新增 `SourceMessageID`
- `internal/application/lark/schedule/service.go`
  - `CreateTaskRequest` 新增 `SourceMessageID`
  - 创建任务时落库来源消息
  - 列表文本补充来源消息展示
- `internal/application/lark/handlers/schedule_handler.go`
  - `/schedule create` 记录当前消息 ID
- `internal/application/lark/schedule/func_call_tools.go`
  - `create_schedule` 记录当前消息 ID
  - tool 结果文本补充来源消息
- `internal/application/lark/schedule/executor.go`
  - 调度执行时构造最小 `P2MessageReceiveV1`，透传 `message_id/chat_id`
- `internal/application/lark/handlers/send_message_handler.go`
  - schedule 场景下的 `send_message` 优先 reply 原消息
- `internal/application/lark/handlers/schedule_compat.go`
  - reply 型兼容发送改为“先 reply，失败再 fallback create”
- `internal/application/lark/schedule/scheduler.go`
  - 错误通知优先 reply `source_message_id`
- `internal/application/lark/schedule/card_view.go`
  - schedule 卡片补充来源消息展示

### 决策

- `source_message_id` 只记录“创建/触发来源消息”，不额外复制消息内容，避免把消息快照膨胀进 schedule 表。
- 执行期不伪造完整消息事件，只补当前工具实际会消费的最小字段：`message_id`、`chat_id`。
- reply 失败时允许 fallback 到 chat 直发；这是可接受的降级，比静默失败更符合 schedule 的可观测性目标。

### 验证

- `go test ./internal/application/lark/schedule ./internal/application/lark/handlers`

## 2026-03-11 · Milestone L · Schedule 结果改为对象引用 + 卡片现签现短链

### 方案

- `schedule.last_result` 不再存短链 / presigned URL，而是存稳定的对象引用；卡片重建时再实时签发新的 presigned URL，并立即转成 short link。
- schedule 结果使用独立 bucket，避免继续和 `cloudmusic` 等历史对象混桶。
- 历史 `last_result` 中的纯文本和旧短链不再做兼容渲染；新链路只识别新的对象引用格式。

### 修改

- `internal/application/lark/schedule/task_result.go`
  - 新 bucket：`betago-schedule-results`
  - `last_result` 改存 `schedule-result://...` 引用
  - 卡片渲染时按对象引用实时签发 5 分钟 presigned URL
  - 签发后立即转成 short link
- `internal/infrastructure/miniodal/init.go`
  - 新增 `EnsureBucket(...)`
- `internal/infrastructure/miniodal/uploader.go`
  - 新增 `PresignGetObject(...)`
  - 新增 `PresignGetObjectShortURL(...)`

### 决策

- 卡片渲染时统一签发 5 分钟临时 URL，再交给 shortener；用户看到的是短链，但底层签名 URL 不再长期固化在数据库里。
- bucket 自动按需创建；如果运行环境没有建桶权限，结果归档会失败并退化为“仅在 DB 中保留原始结果字符串，但卡片不再渲染结果链接”。
- 这一步不处理旧 `last_result` 的迁移；旧值被视为历史噪音，直接忽略。

### 验证

- `go test ./internal/infrastructure/miniodal ./internal/application/config ./internal/application/lark/schedule ./internal/runtime`

## 2026-03-11 · Milestone M · Schedule 管理卡紧凑化

### 方案

- 把单个 schedule 条目的信息从“长 markdown + 独立动作行”改成左右双栏布局，减少单项高度。
- 左栏承载名称、ID、工具、时区、来源消息；右栏承载状态、执行时间、结果与动作按钮，优先利用横向空间。
- 保留 footer、刷新、撤回、Trace 语义不变，只压缩 task item 正文结构。

### 修改

- `internal/application/lark/schedule/card_view.go`
  - 单任务 section 改为 `SplitColumns(...)`
  - 动作按钮并入右栏，不再单独占一整行
  - 错误与来源消息做适度 preview，减少长文本撑高卡片
- `internal/application/lark/schedule/card_view_test.go`
  - 补充双栏权重与紧凑布局断言
- `internal/application/lark/schedule/card_action_test.go`
  - 查询过滤测试改为适配双栏后的嵌套 markdown 结构

### 决策

- 不动整体卡片壳层、footer 和 schema v2 风格，只对 task item 内部排版做紧凑化。
- task ID 继续完整展示，来源消息和错误信息允许 preview；前者主要用于来源识别，后者主要用于快速扫读。

### 验证

- `go test ./internal/application/lark/schedule ./internal/application/lark/cardaction ./internal/application/lark/handlers ./internal/application/lark/command`

## 2026-03-11 · Milestone N · Schedule 管理卡补充筛选与变更授权

### 方案

- 在 schedule 管理卡顶部补充轻量筛选控件，先支持 `状态` 与 `创建者 OpenID` 两个维度。
- 用户筛选不走“自由搜索用户”，而是直接根据当前任务集合里的创建者生成列表，避免再引入一层不稳定的人员检索交互。
- 对 pause / resume / delete 这类写操作补做真正的操作者授权校验，不能再只靠 `chat_id` 归属判断。

### 修改

- `internal/application/lark/schedule/card_view.go`
  - 顶部新增状态筛选按钮组
  - 顶部新增创建者筛选按钮组
  - 任务条目正文补充创建者信息
- `internal/application/lark/schedule/card_action.go`
  - view/action payload 增加 `schedule_view_creator_open_id`
  - card view state / query 同步支持创建者过滤
- `internal/application/lark/schedule/func_call_tools.go`
  - `TaskQuery` / `query_schedule` 支持 `creator_open_id`
  - `FilterTasks(...)` 增加按创建者过滤
  - pause / resume / delete 工具调用改为透传操作者 OpenID
- `internal/application/lark/handlers/schedule_handler.go`
  - `/schedule query` CLI 支持 `--creator_open_id`
  - 兼容 `--open_id` 作为同义参数
  - pause / resume / delete handler 改为透传操作者 OpenID
- `internal/application/lark/cardaction/builtin.go`
  - schedule 卡片回调改为使用 `actionCtx.OpenID()` 做变更授权
- `internal/application/lark/schedule/authorization.go`
  - 新增 `EnsureTaskMutationAllowed(...)`
  - 规则：创建者可操作；具备全局管理权限的用户可 override

### 决策

- 卡片里的“用户过滤”当前采用任务内创建者列表，而不是动态搜索用户；这样实现稳定、交互成本低，也不依赖额外的人员搜索接口。
- 变更授权下沉到 `schedule.Service`，而不是只在 card handler / command handler 上做 UI 层判断，避免后续再出现旁路写入口。
- 当前管理员 override 先复用现有全局管理权限语义；后续如果 schedule 需要更细的权限点，再单独拆 `schedule.manage`。

### 验证

- `go test ./internal/application/lark/schedule ./internal/application/lark/handlers ./internal/application/lark/cardaction`
- `go test ./internal/infrastructure/miniodal ./internal/infrastructure/lark_dal/larkmsg ./internal/application/config ./internal/application/permission ./internal/application/lark/schedule ./internal/application/lark/ratelimit ./internal/application/lark/cardaction ./internal/application/lark/handlers ./internal/application/lark/command ./internal/runtime`

## 2026-03-11 · Milestone O · 协作管理卡补最后修改人与人员组件基线

### 方案

- 对允许多人协作修改的手工 schema v2 管理卡，统一在 footer 展示：
  - 卡片更新时间
  - 最后一次业务变更操作者
- “最后修改人”优先使用飞书 JSON 2.0 的 `person` 组件，而不是继续显示裸 OpenID，降低识别成本。
- refresh / view 只回放当前 view state，不覆盖已有“最后修改人”；真正的写操作（如 config set/delete、feature toggle、permission grant/revoke、schedule pause/resume/delete）才更新该元数据。
- 这一步只覆盖手工卡：
  - `config`
  - `feature`
  - `permission`
  - `schedule`
- 模板卡暂不动，避免与既有 `refresh_obj` / 模板变量体系混线。

### 修改

- `internal/infrastructure/lark_dal/larkmsg/*`
  - 新增 `Person(...)` helper，封装飞书 JSON 2.0 `person` 组件
  - `StandardCardFooterOptions` 增加 `LastModifierOpenID`
  - footer 左侧新增“最后修改”元数据行
  - 补充 footer 测试，覆盖 `person` 组件渲染
- `internal/application/config/*`
  - config / feature 的 view/action payload 增加 `last_modifier_open_id`
  - 初次发卡与写操作回调都透传最后修改人
  - refresh / view 回调复用 payload 中已有的最后修改人
- `internal/application/permission/*`
  - permission view/action payload 增加 `last_modifier_open_id`
  - 目标用户切换表单、授权/回收动作统一保留该元数据
- `internal/application/lark/schedule/*`
  - schedule view/action payload 增加 `schedule_last_modifier_open_id`
  - 首次发卡、工具发卡与写操作回调统一维护最后修改人
- `internal/application/lark/cardaction/builtin.go`
  - config / feature / permission / schedule 卡片回调统一在 mutation 后写入当前操作者 OpenID

### 决策

- “最后修改人”定义为**最后一次业务变更操作者**，而不是最后一次 refresh 点击人；否则只刷新卡片也会污染协作语义。
- 飞书官方文档已确认 JSON 2.0 存在：
  - `person`
  - `person_list`
  - `single_select_user_picker`
  - `multi_select_user_picker`
- 本轮先把 `person` 用在稳定的只读展示位（footer）；后续如果还要增强用户筛选，优先继续走“卡片内显式筛选选项”而不是引入选人器 / 搜索式交互，避免把筛选协议和权限边界做复杂。
- 手工卡默认仍保持 `update_multi=true`，即多人共享同一张卡片实例；这一轮补的是“共享修改可追踪性”，不是改协作模型。

### 验证

- `GOCACHE=/tmp/gocache go test ./internal/infrastructure/lark_dal/larkmsg ./internal/application/config ./internal/application/permission ./internal/application/lark/schedule ./internal/application/lark/cardaction`

## 2026-03-11 · Milestone P · 卡片选人统一切到 Feishu user picker

### 方案

- 回看所有手工管理卡后，真正存在“需要选人”的卡片面只有两类：
  - `schedule`：按创建者筛选
  - `permission`：切换目标用户
- 这两类统一改用飞书 JSON 2.0 `select_person`，不再继续维护：
  - 手输 OpenID
  - 静态用户按钮列表
- picker 候选默认使用“当前会话成员”：
  - 不传 `options`
  - 由飞书前端回退到当前卡片所在会话成员集合

### 修改

- `internal/infrastructure/lark_dal/larkmsg/card_v2.go`
  - 新增 `SelectPerson(...)` helper
- `internal/application/lark/schedule/*`
  - 创建者筛选 row 改为 `select_person + 全部`
  - picker 回调继续复用 `schedule.view`
  - 通过 `action.option` 解析用户选择结果
  - 不再向 picker 注入静态 creator options，默认回退到本群聊成员
- `internal/application/permission/*`
  - 目标用户切换从 OpenID 输入框改为 `select_person`
  - 保留 `查看自己` 快捷按钮
  - picker 回调继续复用 `permission.view`
  - 通过 `action.option` 解析目标用户 OpenID
  - 目标用户区域补充 `当前目标` 的 `person` 展示，并给 picker 注入 `initial_option`

### 决策

- “默认本群聊内成员”通过不传 `options` 实现，而不是服务端先拉成员列表再塞回卡片；这样协议更简单，也避免额外成员查询依赖。
- 这一步只覆盖真正的“选人”交互，不给原本没有目标用户切换语义的卡片强行加 picker。
- 当前仍保留显式“全部/查看自己”按钮，用于清空筛选或快速回到 self 视角。

### 验证

- `GOCACHE=/tmp/gocache go test ./internal/infrastructure/lark_dal/larkmsg ./internal/application/permission ./internal/application/lark/schedule ./internal/application/lark/cardaction ./internal/application/config`

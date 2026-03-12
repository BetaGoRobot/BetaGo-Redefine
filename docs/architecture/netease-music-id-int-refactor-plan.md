# Plan: Netease Music ID Int Refactor

**Generated**: 2026-03-12
**Estimated Complexity**: High

## Overview
当前 `NeteaseAPI` 相关代码里，音乐 ID 同时以 `int`、`int64`、`string` 三种形式流转，已经导致接口定义、实现、卡片层、图片上传层之间发生编译断裂。除此之外，`NeteaseAPI` 模块还存在明显的重复逻辑、重复请求、重复上传、过早并发、字符串化 ID 作为核心模型字段等问题。

本次改造建议分三步推进：
- 先止血：恢复 `neteaseapi`、`card_handlers`、`cardaction` 的可编译和可回归状态
- 再统一：把“领域内的 song/track/music ID”统一为 `int`，只在 HTTP 参数、MinIO key、DB string 主键等 I/O 边界做字符串转换
- 最后收敛：去掉 NeteaseAPI 内部重复实现，合并共用流程，减少重复网络请求和无意义 goroutine

当前已确认的编译断裂点：
- `Provider` / `NetEaseContext` / `noopProvider` 的 `GetDetail`、`GetMusicURL`、`GetLyrics` 签名不一致
- `larkimg.UploadPicAllinOne` 已改为 `int`，但 `neteaseapi/lark_card.go`、`netease.go` 仍有 `string` 调用点
- `SearchMusicItem.ID` 仍是 `string`，但若改回 `int`，音乐卡片、列表卡片、评论按钮、刷新按钮都要一起调整
- `GetMusicURLByIDs` 里混用了单个歌曲请求和批量请求，还存在 `strings.Join([]int, ",")` 这类直接编译错误

## Prerequisites
- 明确本次“统一为 int”的范围：建议先限定为运行时领域模型与 handler/card 层，不立即做 DB schema 迁移
- 保留当前用户约束：网易云响应保持结构化解析，不使用 `map[string]any`
- 现有回归命令可用：
  - `go test -run '^$' ./internal/infrastructure/neteaseapi ./internal/application/lark/card_handlers ./internal/application/lark/cardaction`
  - `go test ./internal/infrastructure/neteaseapi ./internal/application/lark/handlers`

## Sprint 1: Restore Compile And Define ID Boundary
**Goal**: 先让 `NeteaseAPI` 相关链路恢复编译，并明确哪些层使用 `int`，哪些层允许 `string`
**Demo/Validation**:
- `go test -run '^$' ./internal/infrastructure/neteaseapi ./internal/application/lark/card_handlers ./internal/application/lark/cardaction`
- 音乐搜索、单曲卡、歌词卡、刷新卡编译通过

### Task 1.1: Freeze Canonical ID Rules
- **Location**: [neteasevar.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/neteasevar.go), [handler.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/card_handlers/handler.go), [img.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/lark_dal/larkimg/img.go)
- **Description**: 定义统一规则：
  - `Song/Track/Music ID` 在业务内统一为 `int`
  - `Playlist ID` 如接口天然以 query string 传递，可先保留外层 `string` 输入，再在进入 provider 后解析/校验
  - `int64` 仅保留给网易云返回结构体里真正可能超出 `int32` 的原始字段；进入业务层时统一收敛
  - `string` 只允许出现在对象存储 key、DB string 主键、HTTP query/form 参数
- **Dependencies**: 无
- **Acceptance Criteria**:
  - 计划内文件都有明确 ID 归属，不再“看心情传 string/int”
  - 写清楚 `SearchMusicItem.ID`、`MusicInfo.ID`、`MusicIDName.ID`、`Song.ID` 各自应该是什么
- **Validation**:
  - 评审确认边界表

### Task 1.2: Align Provider Interface And Noop Implementation
- **Location**: [neteasevar.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/neteasevar.go)
- **Description**: 统一 `Provider`、`NetEaseContext`、`noopProvider` 签名，优先修复以下方法：
  - `GetMusicURL`
  - `GetDetail`
  - `GetLyrics`
  - 以及与 `SearchMusicItem` / `MusicIDName` 强耦合的方法
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - `NetEaseGCtx Provider = ...` 可正常赋值
  - 不再出现接口方法签名不匹配
- **Validation**:
  - `go test -run '^$' ./internal/infrastructure/neteaseapi`

### Task 1.3: Repair Cross-Package Compile Breaks
- **Location**: [lark_card.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/lark_card.go), [handler.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/card_handlers/handler.go), [builtin.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/cardaction/builtin.go), [img.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/lark_dal/larkimg/img.go)
- **Description**: 修掉被 `musicID int` 改动波及的调用点，尤其是：
  - 卡片按钮 payload 仍需字符串化传输，但 handler 内立即解析回 `int`
  - `UploadPicAllinOne` 的所有调用点统一
  - `ReplyCard` / `WithID` / `_music` 这类字符串拼接只在最外层 `strconv.Itoa`
- **Dependencies**: Task 1.2
- **Acceptance Criteria**:
  - `neteaseapi`、`card_handlers`、`cardaction` compile check 通过
- **Validation**:
  - `go test -run '^$' ./internal/infrastructure/neteaseapi ./internal/application/lark/card_handlers ./internal/application/lark/cardaction`

## Sprint 2: Canonicalize Data Models
**Goal**: 把 Netease 相关核心模型中的 ID 语义统一下来，避免后续反复 `Atoi/Itoa`
**Demo/Validation**:
- 搜索歌曲、歌单详情、单曲详情、歌词卡路径上不再有来回字符串转换
- 新增的 helper 覆盖 song/playlist/detail/url 链路

### Task 2.1: Normalize Netease Domain Structs
- **Location**: [neteasevar.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/neteasevar.go)
- **Description**: 统一整理这些结构体：
  - `MusicInfo`
  - `MusicIDName`
  - `SearchMusicItem`
  - `Song`
  - `MusicDetail` 相关子结构
  - `PlaylistTrackIdentity`
  - `MusicDetailSongLite`
- **Dependencies**: Sprint 1
- **Acceptance Criteria**:
  - 领域内音乐 ID 统一为 `int`
  - 只在确有必要处保留 `int64`
  - 删除或合并重复结构
- **Validation**:
  - `go test -run '^$' ./internal/infrastructure/neteaseapi`

### Task 2.2: Introduce Small Typed Conversion Helpers
- **Location**: [netease.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/netease.go)
- **Description**: 用小 helper 统一处理边界转换，替代散落的 `strconv.Itoa/Atoi/FormatInt`：
  - `songIDString(id int) string`
  - `joinSongIDs(ids []int) string`
  - `parsePlaylistID(raw string) (int, error)` 或 `normalizePlaylistID(raw string) string`
  - `songObjectKey(id int, ext string) string`
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - 核心路径里不再直接散落低层字符串拼接
- **Validation**:
  - grep 检查 `neteaseapi` 内裸 `strconv` 使用显著下降

### Task 2.3: Decide DB Boundary Strategy
- **Location**: [lark_imgs.gen.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/db/model/lark_imgs.gen.go), [img.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/lark_dal/larkimg/img.go)
- **Description**: 明确 `LarkImg.SongID string` 是否本次迁移：
  - 建议本次不改 schema，运行时统一 `int -> string` 后存取
  - 如果要改 DB schema，需要单独列 migration 与兼容策略
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - 本次 PR 不会因为 schema 迁移而阻塞主线修复
- **Validation**:
  - 方案评审确认

## Sprint 3: Remove Redundant Netease Flows
**Goal**: 把 `NeteaseAPI` 中多套相似逻辑收敛为一套，避免多处维护
**Demo/Validation**:
- 同一种资源获取路径只有一套主流程
- 单曲搜索、歌单展开、专辑流程共享基础能力

### Task 3.1: Merge Song URL Retrieval Paths
- **Location**: [netease.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/netease.go)
- **Description**: 收敛以下重叠逻辑：
  - `GetMusicURL`
  - `GetMusicURLByIDs`
  - `GetMusicURLByID`
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 形成“批量 song url 获取 + MinIO 预热 + Presign 生成”的单一路径
  - 单曲接口只是批量接口的退化调用
- **Validation**:
  - `SearchMusicByKeyWord`、`SearchMusicByPlaylist`、`GetCardMusicByPage` 都走统一 helper

### Task 3.2: Merge Song Detail Retrieval Paths
- **Location**: [netease.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/netease.go)
- **Description**: 收敛以下重叠逻辑：
  - `GetDetail`
  - `getDetailByIDs`
  - `SearchMusicByPlaylist` 里的 detail 映射逻辑
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - 形成统一的 `getSongDetails(ctx, ids []int)` 主路径
  - `GetDetail(ctx, id int)` 只包装单 ID
- **Validation**:
  - 歌词卡、刷新卡、歌单展开都复用同一 detail helper

### Task 3.3: Merge Picture Upload Flows
- **Location**: [lark_card.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/lark_card.go), [netease.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/netease.go), [img.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/lark_dal/larkimg/img.go)
- **Description**: 统一图片上传策略，避免：
  - 搜索结果异步上传一套
  - 专辑卡单独上传一套
  - 单曲详情里再预热一套
- **Dependencies**: Task 3.2
- **Acceptance Criteria**:
  - 相同 song/album 图片不会在同一请求链路被重复上传
  - 所有调用点复用统一 helper
- **Validation**:
  - 加日志或测试确认重复上传次数下降

## Sprint 4: Performance Cleanup
**Goal**: 去掉“看起来并发，实际更慢/更乱”的实现，降低重复 I/O
**Demo/Validation**:
- 同样的搜索请求网络调用次数可数
- 无无界 goroutine 扩散

### Task 4.1: Remove Mixed Batch/Per-ID URL Fetching
- **Location**: [netease.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/netease.go)
- **Description**: `GetMusicURLByIDs` 当前同时：
  - 先对每个 ID 单独调 `GetMusicURL`
  - 又对整个批次调 `/song/url/v1`
  这会造成重复请求和重复上传。需要删成单一路径。
- **Dependencies**: Sprint 3
- **Acceptance Criteria**:
  - 每批 song IDs 只打一轮 URL 查询
  - 没有“单曲/批量双发”的行为
- **Validation**:
  - 为 helper 增加可观察日志/测试

### Task 4.2: Bound Concurrency For Uploads And Comments
- **Location**: [lark_card.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/lark_card.go), [netease.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/netease.go)
- **Description**: 现在有多处“每条结果一个 goroutine”，而且没有限流。改成：
  - 小范围 worker pool 或顺序处理
  - 评论获取失败不阻塞卡片主结构
  - 图片上传失败只降级当前行，不拖垮全局
- **Dependencies**: Task 4.1
- **Acceptance Criteria**:
  - 结果集很大时不会无限起 goroutine
  - goroutine 生命周期可预测
- **Validation**:
  - 压测或基准日志

### Task 4.3: Cache/Reuse Object Metadata
- **Location**: [netease.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/netease.go), [img.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/lark_dal/larkimg/img.go)
- **Description**: 对 MinIO 对象命名、文件扩展名、歌词 JSON 上传、详情 JSON 上传做统一 key 生成，减少重复 `ListObjectsIter` 和重复 `filepath.Ext(...)`
- **Dependencies**: Task 4.2
- **Acceptance Criteria**:
  - key 规则统一
  - 单曲详情卡不会重复扫描 bucket
- **Validation**:
  - grep 检查 key 构造统一入口

## Sprint 5: Regression And Test Repair
**Goal**: 用可靠测试替换当前无效或阻塞型测试，并补关键链路回归
**Demo/Validation**:
- `neteaseapi` 测试可快速执行
- 不再存在死循环、真实外网依赖测试

### Task 5.1: Delete Or Rewrite Broken Tests
- **Location**: [netease_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/netease_test.go)
- **Description**: 当前测试文件存在：
  - 死循环等时间触发测试
  - 直接打真实网络
  - 断言为空
 需要改成 mockable / table-driven / compile-safe 测试。
- **Dependencies**: Sprint 1-4
- **Acceptance Criteria**:
  - `go test ./internal/infrastructure/neteaseapi` 可稳定执行
  - 没有依赖当前时间和真人扫码登录的测试
- **Validation**:
  - CI 可运行

### Task 5.2: Add Playlist Expansion Regression Tests
- **Location**: [netease_test.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/infrastructure/neteaseapi/netease_test.go) 或新测试文件
- **Description**: 为 `SearchMusicByPlaylist` 增加回归，覆盖：
  - 空歌单
  - trackIds 存在但 detail 缺失
  - detail 返回顺序和 trackIds 不同
  - URL 缺失时卡片仍可返回
- **Dependencies**: Sprint 3
- **Acceptance Criteria**:
  - 结果顺序与 playlist 原始顺序一致
  - 结构化解析保持类型安全
- **Validation**:
  - 表驱动测试通过

### Task 5.3: Add End-To-End Compile Checks For Music Card Path
- **Location**: [handler.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/card_handlers/handler.go), [builtin.go](/mnt/RapidPool/workspace/BetaGo_v2/internal/application/lark/cardaction/builtin.go)
- **Description**: 增加最小 compile/behavior test，确保：
  - 卡片按钮传 string payload
  - handler 内解析回 int
  - `GetDetail/GetLyrics/GetMusicURL` 类型一致
- **Dependencies**: Sprint 1-5
- **Acceptance Criteria**:
  - 音乐卡点击播放、刷新、查看歌词三条路径不再因为 ID 类型波动断裂
- **Validation**:
  - 相关包测试通过

## Testing Strategy
- 编译止血阶段：
  - `go test -run '^$' ./internal/infrastructure/neteaseapi ./internal/application/lark/card_handlers ./internal/application/lark/cardaction`
- 模块回归阶段：
  - `go test ./internal/infrastructure/neteaseapi`
  - `go test ./internal/application/lark/handlers ./internal/application/lark/card_handlers ./internal/application/lark/cardaction`
- 静态检查：
  - `staticcheck ./internal/infrastructure/neteaseapi/... ./internal/application/lark/...`
- 功能验证：
  - 单曲搜索
  - 专辑搜索
  - playlist 展开
  - 单曲卡播放/刷新/歌词

## Potential Risks & Gotchas
- `SearchMusicItem.ID` 如果从 `string` 改成 `int`，按钮 payload、模板变量、评论查询会一起被影响，不能只改一处
- `Album.IDStr` 当前看起来仍被 UI/模板当字符串使用；专辑 ID 未必需要跟 song ID 同步改成 `int`
- `LarkImg.SongID` 仍是 string 主键，若硬做 DB migration，会把本次修复范围拉大很多
- `GetMusicURLByIDs` 当前逻辑不仅重复请求，而且批处理 goroutine 里还混入单曲 fallback，重构时最容易引入行为回退
- `BuildMusicListCard` 现在对评论和图片都起 goroutine；如果收敛并发策略，需要避免卡片顺序错乱

## Rollback Plan
- 保留“canonical int + 边界 stringify”作为最小回退方案，不做数据库 schema 改动
- 若 Sprint 3-4 的重构风险过高，可只合并 Sprint 1-2 先恢复稳定编译与类型一致性
- 每个 Sprint 都单独提交，确保可以按功能回退：
  - `fix/netease-id-compile`
  - `refactor/netease-id-model`
  - `refactor/netease-fetch-pipeline`
  - `test/netease-regression`

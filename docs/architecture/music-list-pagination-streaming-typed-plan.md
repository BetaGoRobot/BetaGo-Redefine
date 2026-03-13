# Plan: Music List Pagination, Batch Reveal, and Typed Template Migration

**Generated**: 2026-03-12
**Estimated Complexity**: Medium

## Overview
完成音乐列表卡片的三项收口工作：
1. 把当前“全量 loading 再逐项 patch”的流式体验改成“按批次出现”。
2. 为 `AlbumListTemplate` 增加分页协议，支持专辑/歌单/搜索结果翻页。
3. 继续沿音乐域推进 typed template vars，避免继续用 `AddVariable(map[string]any)` 拼装模板变量。

本次范围只覆盖音乐列表相关卡片与其回调链路，不扩展到 schedule/config/permission 等无关模块。

## Assumptions
- 用户会自行修改飞书 template，并接受新增分页按钮与页码展示区。
- 列表卡使用同一个 template：`AlbumListTemplate`。
- 翻页行为使用卡片回调触发后端重新 patch 当前卡片，不新发消息。
- 专辑/歌单/搜索结果的单页大小统一管理，默认值由后端控制。

## Prerequisites
- 已有的音乐搜索与卡片回调链路可复用。
- 已落地的 typed builder：`TemplateVersionV2[T]` / `NewCardContentWithData`。
- 已落地的兼容发送接口：`sendCompatibleCardWithMessageID(...)`。

## Sprint 1: Batch Reveal Streaming
**Goal**: 音乐列表不再先出现整页 loading，而是首批 ready 后发送，后续批次逐步追加。
**Demo/Validation**:
- 搜索歌曲/专辑/歌单时，卡片先出现第 1 批条目。
- 后续条目按批次 append，而不是整页先显示“加载中”。
- 当前卡片 message_id 不变，只做 patch。

### Task 1.1: 重写音乐列表流式策略
- **Location**: `internal/infrastructure/neteaseapi/lark_card.go`
- **Description**: 将 `StreamMusicListCard(...)` 从“先发全量骨架卡”改成“每批 resolve 完后再展示该批”。
- **Dependencies**: 无
- **Acceptance Criteria**:
  - 首次发送只包含首批已完成条目。
  - 后续每批完成后 patch 当前卡片，列表长度递增。
  - 不再向最终用户展示 `loading...` 占位行。
- **Validation**:
  - `go test ./internal/infrastructure/neteaseapi ./internal/application/lark/handlers`
  - 手动验证 5 条搜索结果时，看到 `2 -> 4 -> 5` 或类似逐批增长。

### Task 1.2: 调整列表 renderer 的可见性状态
- **Location**: `internal/infrastructure/neteaseapi/lark_card.go`
- **Description**: 给内部行状态增加“是否可见”语义，只序列化已解锁批次的数据。
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - `Card(...)` 输出只包含当前可见条目。
  - 同步构卡路径 `BuildMusicListCard(...)` 仍输出全量内容。
- **Validation**:
  - 新增/更新单测验证 visible lines 数量随批次增长。

## Sprint 2: Pagination Contract for AlbumListTemplate
**Goal**: 为超长专辑/歌单结果提供翻页能力，并明确模板所需变量与 action payload 协议。
**Demo/Validation**:
- 卡片底部出现上一页/下一页按钮与页码文案。
- 点击分页后 patch 当前卡片，内容切到目标页。
- 歌曲播放按钮与分页按钮互不干扰。

### Task 2.1: 定义模板变量协议
- **Location**: `internal/infrastructure/lark_dal/larkmsg/larktpl/template_card.go`
- **Description**: 扩展音乐列表 typed vars，新增分页相关字段。
- **Dependencies**: 无
- **Acceptance Criteria**:
  - 新增以下变量名，供 template 使用：
    - `page_info_text`: 当前页文案，例如 `第 2 / 7 页`
    - `has_prev`: 是否可翻上一页
    - `has_next`: 是否可翻下一页
    - `prev_page_val`: 上一页按钮 payload
    - `next_page_val`: 下一页按钮 payload
  - 保留现有：`object_list_1`、`query`
- **Validation**:
  - 新增单测校验 typed vars 能正常序列化。

### Task 2.2: 定义分页回调 payload 协议
- **Location**: `pkg/cardaction/action.go`
- **Description**: 增加音乐列表翻页 action 常量与字段约定。
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - 新增 action：`music.list_page`
  - payload 字段约定如下：
    - `action`: `music.list_page`
    - `type`: 搜索类型，值域 `song | album | playlist`
    - `keywords`: 搜索关键词；playlist 场景下仍传 playlistID
    - `page`: 目标页，从 `1` 开始
    - `page_size`: 当前页大小
    - `title`: 可选；playlist/album 用于卡片 query 展示
  - 统一使用 `cardaction.New(...).WithValue(...).Payload()` 构造
- **Validation**:
  - 新增 action parser / payload builder 测试。

### Task 2.3: 输出给模板的推荐按钮绑定方式
- **Location**: `docs/architecture/music-list-pagination-streaming-typed-plan.md` 与最终答复
- **Description**: 给用户模板侧一份明确映射：
  - 上一页按钮绑定 `prev_page_val`
  - 下一页按钮绑定 `next_page_val`
  - 页码文本绑定 `page_info_text`
  - 按钮禁用态由 `has_prev` / `has_next` 控制
- **Dependencies**: Task 2.2
- **Acceptance Criteria**:
  - 模板改造不需要猜字段名。
  - 后端字段与模板字段一一对应。
- **Validation**:
  - 由用户对照模板编辑器检查绑定项。

## Sprint 3: Pagination Handler and Data Slicing
**Goal**: 点击分页后能基于上下文重新取数、切页并 patch 当前卡片。
**Demo/Validation**:
- 专辑/歌单的长列表可以翻页。
- 页码切换不丢失 query/title。
- 翻页只更新当前卡片，不新发消息。

### Task 3.1: 为音乐搜索增加分页取数入口
- **Location**: `internal/application/lark/handlers/music_handler.go`, `internal/infrastructure/neteaseapi/lark_card.go`
- **Description**: 抽出“列表结果 -> 指定页 card”的构造函数，支持 song/album/playlist 三类分页。
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 统一入口输入：搜索类型、关键词、页码、页大小。
  - 输出：单页列表卡 typed vars。
  - query/title 展示与当前页匹配。
- **Validation**:
  - 针对 song/album/playlist 新增分页单测。

### Task 3.2: 注册 `music.list_page` 回调
- **Location**: `internal/application/lark/cardaction/builtin.go`
- **Description**: 解析分页 payload，根据 `type/keywords/page/page_size/title` 重建目标页卡片并 patch 当前消息。
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - 回调后 patch `actionCtx.MessageID()`。
  - 非法页码自动 clamp 到合法范围。
  - 错误时返回 toast，不 panic。
- **Validation**:
  - 新增 cardaction 层测试。

### Task 3.3: 专辑/歌单列表分页切片
- **Location**: `internal/infrastructure/neteaseapi/lark_card.go`, `internal/infrastructure/neteaseapi/netease.go`
- **Description**: 对超长列表先拿全量结构化结果，再在 card 层做分页切片；避免模板层一次渲染过长列表。
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - 页面展示只渲染单页数据。
  - 页内条目仍复用现有歌曲播放/专辑查看按钮。
- **Validation**:
  - 手动验证超长专辑/playlist 能翻页。

## Sprint 4: Continue Typed Migration in Music Scope
**Goal**: 把音乐域剩余仍使用 `AddVariable(...)` 的 template 卡收口到 typed vars。
**Demo/Validation**:
- 音乐域 template 卡不再直接依赖裸 `map[string]any` 组装变量。

### Task 4.1: 完成音乐列表分页变量的 typed 化
- **Location**: `internal/infrastructure/lark_dal/larkmsg/larktpl/template_card.go`
- **Description**: 将分页字段放入 `MusicListCardVars`，不在调用方散落 `AddVariable(...)`。
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 音乐列表卡全部由 typed vars 驱动。
- **Validation**:
  - 单测覆盖序列化结果。

### Task 4.2: 收口音乐域公共回复模板
- **Location**: `internal/infrastructure/lark_dal/larkmsg/card.go`, `internal/infrastructure/lark_dal/larkmsg/larktpl/template_card.go`
- **Description**: 把 `NormalCardReplyTemplate` 也纳入 typed vars 路径。
- **Dependencies**: 无
- **Acceptance Criteria**:
  - 不再用 `AddVariable("content", ...)`。
- **Validation**:
  - `go test ./internal/infrastructure/lark_dal/larkmsg`

## Testing Strategy
- 单元测试：
  - typed vars 序列化
  - 分页 payload 构造
  - 列表 renderer 的 visible batch 递增
  - 页码 clamp / 边界页
- 集成验证：
  - `/music 稻香`
  - `/music --type=album 范特西`
  - `/music --type=playlist 3778678`
- 人工验证重点：
  - 首屏是否按批次出现
  - 翻页是否 patch 当前卡
  - 播放/查看专辑按钮是否仍可用

## Template Variable Contract
建议你在 `AlbumListTemplate` 里新增以下绑定：
- `object_list_1`: 当前页条目数组
- `query`: 顶部查询文案
- `page_info_text`: 页码展示，例如 `第 1 / 5 页`
- `has_prev`: 上一页是否可点
- `has_next`: 下一页是否可点
- `prev_page_val`: 上一页按钮的 `value`
- `next_page_val`: 下一页按钮的 `value`

建议两个分页按钮的 payload 形状完全一致，只是 `page` 不同：
```json
{
  "action": "music.list_page",
  "type": "album",
  "keywords": "范特西",
  "page": "2",
  "page_size": "10",
  "title": "[专辑] 范特西"
}
```

playlist 场景示例：
```json
{
  "action": "music.list_page",
  "type": "playlist",
  "keywords": "3778678",
  "page": "3",
  "page_size": "10",
  "title": "我喜欢的音乐"
}
```

## Potential Risks & Gotchas
- `keywords` 对 playlist 场景其实是 playlistID，这个字段名语义偏弱，但它最兼容现有 handler。
- 如果 template 侧按钮不支持直接 disabled，需要额外准备 `prev_button_type` / `next_button_type` 或隐藏逻辑。
- 如果 album/playlist 数据量很大，全量拉取后再切页会增加一次后端内存占用，但当前规模通常可接受。
- 如果后续要支持“上一页保留已加载缓存”，需要再加 message-level cache；本次先不做。
- 当前全局 staticcheck 还有历史债，本次只要求新增/修改文件保持干净。

## Rollback Plan
- 如分页 template 未及时改完，可先仅保留 batch reveal，不输出分页按钮变量。
- 如分页回调不稳定，可暂时只开放第一页，保留现有播放/查看专辑行为。
- typed vars 如发现模板字段名不一致，可局部回退到旧 `AddVariable(...)` 路径，不影响其他音乐卡。

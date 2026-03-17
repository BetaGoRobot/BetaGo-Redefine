# Plan: Card Regression Debug and Scene Contract

**Generated**: 2026-03-17
**Estimated Complexity**: High

## Overview

本计划实现一套统一的卡片回归调试体系，使仓库内的卡片场景都能：

- 通过统一接口暴露生产构卡与测试构卡能力。
- 被注册到统一场景注册表。
- 被 `lark-card-debug` 以单场景或整组 suite 的方式发送到指定测试 `chat_id`。
- 通过守卫测试，逐步收敛“匿名构卡 + 直接发送”的历史路径。

对应设计文档：
- `docs/architecture/card-regression-debug-design.md`

## Success Criteria

- 存在统一的 `CardSceneProtocol`，强制每个卡片场景实现 `BuildTestCard(...)`。
- `cmd/lark-card-debug` 能列出 scene、执行单场景回归、执行 smoke suite。
- canonical scene key 与 legacy `--spec` alias 的映射被固定并有自动化验证。
- 第一批高价值场景完成迁移并注册。
- 自动化测试能验证：
  - scene key 无重复
  - 每个注册场景都有至少一个 case
  - smoke case 可 dry-run 构卡
  - `CardRequirementSet` 缺失依赖时返回显式 validation error
  - 新增 direct send 路径不会静默绕开协议

## Non-Goals

- 本轮不要求所有卡片场景都一次性切到统一生产 sender。
- 本轮不做视觉截图 diff。
- 本轮不把所有 live-data 场景都强行 sample 化。

## File Map

### New Files

- `internal/application/lark/cardregression/protocol.go`
- `internal/application/lark/cardregression/registry.go`
- `internal/application/lark/cardregression/runner.go`
- `internal/application/lark/cardregression/registry_test.go`
- `internal/application/lark/cardregression/runner_test.go`
- `internal/application/lark/cardregression/guard_test.go`
- `internal/application/lark/cardregression/direct_send_guard_test.go`
- `internal/application/lark/cardregression/README.md`
- `internal/application/lark/cardregression/testdata/direct_send_allowlist.txt`
- `internal/application/config/card_regression_scene.go`
- `internal/application/permission/card_regression_scene.go`
- `internal/application/lark/ratelimit/card_regression_scene.go`
- `internal/application/lark/schedule/card_regression_scene.go`
- `internal/application/lark/command/card_regression_scene.go`
- `internal/application/lark/handlers/wordcount_regression_scene.go`
- `internal/application/lark/handlers/wordchunk_regression_scene.go`
- `internal/application/lark/handlers/music_regression_scene.go`

### Modified Files

- `internal/application/lark/carddebug/card_debug.go`
- `internal/application/lark/carddebug/card_debug_test.go`
- `internal/application/lark/carddebug/README.md`
- `.codex/skills/lark-card-debug/SKILL.md`
- `cmd/lark-card-debug/main.go`
- `internal/application/config/card_view.go`
- `internal/application/lark/ratelimit/stats_card.go`
- `internal/application/permission/card_view.go`
- `internal/application/lark/schedule/card_view.go`
- `internal/application/lark/command/help.go`
- `internal/application/lark/command/form.go`
- `internal/application/lark/handlers/word_count_views.go`
- `internal/application/lark/handlers/word_chunk_card.go`
- `internal/application/lark/handlers/music_handler.go`
- `internal/infrastructure/neteaseapi/lark_card.go`

## Canonical Scene Key and Alias Map

实现时统一以下映射：

| Canonical scene key | Legacy alias / current spec |
| --- | --- |
| `config.list` | `config` |
| `feature.list` | `feature` |
| `permission.manage` | `permission` |
| `ratelimit.stats` | `ratelimit`, `ratelimit.sample` |
| `schedule.list` | `schedule.list`, `schedule.sample` |
| `schedule.query` | `schedule.task` |
| `help.view` | none |
| `command.form` | none |
| `wordcount.chunks` | `wordcount.sample` |
| `wordchunk.detail` | `chunk.sample` |
| `music.list` | none |

要求：
- registry 只使用 canonical scene key。
- CLI 继续接受 legacy alias，但必须映射到 canonical scene key。
- coverage manifest 与 guard test 一律使用 canonical scene key。

## Chunk 1: Core Regression Spine

### Task 1: Define the scene protocol

**Files:**
- Create: `internal/application/lark/cardregression/protocol.go`
- Test: `internal/application/lark/cardregression/registry_test.go`

- [ ] **Step 1: Write the failing tests**

覆盖以下断言：
- scene 必须有 `SceneKey()`。
- scene 必须暴露 `BuildCard(...)`、`BuildTestCard(...)`、`TestCases()`。
- `TestCases()` 为空时，registry smoke 校验失败。

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/cardregression -run 'TestSceneProtocol|TestRegistryRejectsSceneWithoutCases' -count=1
```
Expected:
- FAIL，因为包和接口尚不存在。

- [ ] **Step 3: Write minimal implementation**

在 `protocol.go` 中定义：
- `BuiltCard` 复用或别名策略
- `ReceiveTarget`
- `CardBusinessContext`
- `CardSceneMeta`
- `CardBuildRequest`
- `TestCardBuildRequest`
- `CardRequirementSet`
- `CardRegressionCase`
- `CardSceneProtocol`

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/cardregression -run 'TestSceneProtocol|TestRegistryRejectsSceneWithoutCases' -count=1
```
Expected:
- PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/cardregression/protocol.go internal/application/lark/cardregression/registry_test.go
git commit -m "feat: add card regression scene protocol"
```

### Task 2: Implement registry and smoke validation

**Files:**
- Create: `internal/application/lark/cardregression/registry.go`
- Create: `internal/application/lark/cardregression/registry_test.go`

- [ ] **Step 1: Write the failing tests**

测试点：
- duplicate scene key 会失败。
- `List()` 稳定排序。
- `ValidateScenes()` 会校验空 case、空 key、nil scene。

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/cardregression -run 'TestRegistry' -count=1
```
Expected:
- FAIL

- [ ] **Step 3: Write minimal implementation**

实现：
- `Registry`
- `MustRegister(...)`
- `Get(...)`
- `List()`
- `ValidateRegisteredScenes()`

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/cardregression -run 'TestRegistry' -count=1
```
Expected:
- PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/cardregression/registry.go internal/application/lark/cardregression/registry_test.go
git commit -m "feat: add card regression registry"
```

### Task 3: Implement runner and structured report

**Files:**
- Create: `internal/application/lark/cardregression/runner.go`
- Create: `internal/application/lark/cardregression/runner_test.go`

- [ ] **Step 1: Write the failing tests**

测试点：
- 单场景 dry-run 会调用 `BuildTestCard` 但不发送。
- suite 模式会遍历指定 tag 的 case。
- `--fail-fast` 语义正确。
- report 中有 `SceneKey`、`CaseName`、`Built`、`Sent`、`Error`。
- `CardRequirementSet` 缺依赖时返回显式 validation error。
- `smoke` 不会因为 live-only case 缺依赖而失败。

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/cardregression -run 'TestRunner' -count=1
```
Expected:
- FAIL

- [ ] **Step 3: Write minimal implementation**

实现：
- `RunScene(...)`
- `RunSuite(...)`
- `RegressionResult`
- sender interface stub
- requirement validation and precedence resolution

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/cardregression -run 'TestRunner' -count=1
```
Expected:
- PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/cardregression/runner.go internal/application/lark/cardregression/runner_test.go
git commit -m "feat: add card regression runner"
```

## Chunk 2: Bridge Existing Card Debug CLI

### Task 4: Make `carddebug` read from the registry

**Files:**
- Modify: `internal/application/lark/carddebug/card_debug.go`
- Modify: `internal/application/lark/carddebug/card_debug_test.go`
- Modify: `internal/application/lark/carddebug/README.md`

- [ ] **Step 1: Write the failing tests**

新增/调整测试，验证：
- `ListSpecs()` 兼容旧输出时仍可工作，或迁移为 `ListScenes()` 后有兼容映射。
- `Build(...)` 可通过 registry 找到 scene。
- 样例场景仍能构卡。
- legacy `--spec` alias 按完整映射表做 table-driven 兼容测试。

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/carddebug -count=1
```
Expected:
- FAIL

- [ ] **Step 3: Write minimal implementation**

重构方向：
- 保留当前 `BuiltCard` / `ReceiveTarget` 兼容 API，必要时 thin wrapper 到 `cardregression`。
- 先引入 `registry lookup -> fallback switch` 双路径。
- 只有当所有 current built-in specs 都已迁移后，才删除旧 `switch`。
- 旧 `spec` 名先作为 scene alias 保留，避免 CLI 断裂。

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/carddebug -count=1
```
Expected:
- PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/carddebug/card_debug.go internal/application/lark/carddebug/card_debug_test.go internal/application/lark/carddebug/README.md
git commit -m "refactor: back card debug with regression registry"
```

### Task 5: Extend `cmd/lark-card-debug` with regression mode

**Files:**
- Modify: `cmd/lark-card-debug/main.go`
- Test: existing CLI tests or add under `cmd/lark-card-debug`

- [ ] **Step 1: Write the failing tests**

覆盖：
- `--list-scenes`
- `--scene`
- `--case`
- `--suite`
- `--suite live-smoke`
- `--report-json`
- 兼容旧 `--spec`
- `--scene` 未传 `--case` 时默认选 `smoke-default`

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./cmd/lark-card-debug/... -count=1
```
Expected:
- FAIL

- [ ] **Step 3: Write minimal implementation**

要求：
- 优先兼容旧参数。
- `--spec` 内部映射到 scene alias。
- `--suite smoke --dry-run` 可直接用于本地回归。
- `--report-json` 输出结构必须可被 runner 测试校验。
- `live-smoke` 对 missing requirements 返回零退出码，但必须在 report 中写出 `validation_error`。

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./cmd/lark-card-debug/... -count=1
```
Expected:
- PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/lark-card-debug/main.go
git commit -m "feat: add regression suite mode to lark card debug cli"
```

## Chunk 3: Migrate the First Batch of Scenes

### Task 6: Migrate current built-in debug specs to scene implementations

**Files:**
- Create: `internal/application/config/card_regression_scene.go`
- Create: `internal/application/permission/card_regression_scene.go`
- Create: `internal/application/lark/ratelimit/card_regression_scene.go`
- Create: `internal/application/lark/schedule/card_regression_scene.go`
- Create: `internal/application/lark/handlers/wordcount_regression_scene.go`
- Create: `internal/application/lark/handlers/wordchunk_regression_scene.go`
- Modify: `internal/application/config/card_view.go`
- Modify: `internal/application/lark/ratelimit/stats_card.go`
- Modify: `internal/application/lark/schedule/card_view.go`
- Modify: `internal/application/permission/card_view.go`
- Modify: `internal/application/lark/handlers/word_count_views.go`
- Modify: `internal/application/lark/handlers/word_chunk_card.go`

- [ ] **Step 1: Write the failing tests**

至少增加：
- `config.list` live case validation
- `feature.list` scene smoke/live case validation
- `permission.manage` scene live case validation
- `ratelimit.stats` 的 `smoke-default` 与 `live-default`
- `schedule.list` 的 `smoke-default` 与 `live-default`
- `schedule.query` 的 `live-default`
- `wordcount.chunks` 的 `sample-default`
- `wordchunk.detail` 的 `sample-default`

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/config ./internal/application/lark/ratelimit ./internal/application/lark/schedule ./internal/application/permission ./internal/application/lark/handlers -count=1
```
Expected:
- FAIL

- [ ] **Step 3: Write minimal implementation**

要求：
- 每个场景都实现 `BuildTestCard`。
- 优先重用现有 builder，不复制构卡逻辑。
- sample 场景保持 deterministic。
- live 场景在缺少 `chat_id` / `actor_open_id` 时返回显式错误。
- alias `ratelimit.sample` / `schedule.sample` / `schedule.task` / `wordcount.sample` / `chunk.sample` 必须继续可用。

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/config ./internal/application/lark/ratelimit ./internal/application/lark/schedule ./internal/application/permission ./internal/application/lark/handlers -count=1
```
Expected:
- PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/config internal/application/lark/ratelimit internal/application/lark/schedule internal/application/permission internal/application/lark/handlers
git commit -m "feat: register first batch of card regression scenes"
```

### Task 7: Migrate high-value interactive cards

**Files:**
- Create: `internal/application/lark/command/card_regression_scene.go`
- Create: `internal/application/lark/handlers/music_regression_scene.go`
- Modify: `internal/application/lark/command/help.go`
- Modify: `internal/application/lark/command/form.go`
- Modify: `internal/application/lark/handlers/word_count_views.go`
- Modify: `internal/application/lark/handlers/word_chunk_card.go`
- Modify: `internal/application/lark/handlers/music_handler.go`
- Modify: `internal/infrastructure/neteaseapi/lark_card.go`

- [ ] **Step 1: Write the failing tests**

覆盖：
- `help.view` scene 可 dry-run。
- `command.form` scene 可对指定 raw command 构卡。
- `wordcount.chunks` scene 可补充 live case。
- `wordchunk.detail` scene 可补充 live case。
- `music.list` scene 至少支持 sample 或 live-request smoke。

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/command ./internal/application/lark/handlers ./internal/infrastructure/neteaseapi -count=1
```
Expected:
- FAIL

- [ ] **Step 3: Write minimal implementation**

要求：
- scene 和构卡逻辑共址。
- 不在 `carddebug` 里重写业务细节。
- 对依赖较重的场景先提供 sample smoke case，再补 live case。

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/command ./internal/application/lark/handlers ./internal/infrastructure/neteaseapi -count=1
```
Expected:
- PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/command internal/application/lark/handlers internal/infrastructure/neteaseapi
git commit -m "feat: add regression scenes for interactive cards"
```

## Chunk 4: Add Guardrails

### Task 8: Add scene smoke regression tests

**Files:**
- Create: `internal/application/lark/cardregression/guard_test.go`

- [ ] **Step 1: Write the failing tests**

测试名建议：
- `TestAllRegisteredScenesCanBuildSmokeCase`
- `TestSceneCoverageManifest`

manifest 第一批至少要求：
- `config.list`
- `feature.list`
- `permission.manage`
- `ratelimit.stats`
- `schedule.list`
- `schedule.query`
- `help.view`
- `command.form`
- `wordcount.chunks`
- `wordchunk.detail`
- `music.list`

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/cardregression -run 'TestAllRegisteredScenesCanBuildSmokeCase|TestSceneCoverageManifest' -count=1
```
Expected:
- FAIL

- [ ] **Step 3: Write minimal implementation**

要求：
- 所有 smoke case 走 `dry-run`。
- 输出清楚哪个 scene / case 构卡失败。
- manifest 和 CLI alias 表必须都基于 canonical scene key 校验。

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/cardregression -run 'TestAllRegisteredScenesCanBuildSmokeCase|TestSceneCoverageManifest' -count=1
```
Expected:
- PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/cardregression/guard_test.go
git commit -m "test: add card regression smoke guard"
```

### Task 9: Add direct send allowlist guard

**Files:**
- Create: `internal/application/lark/cardregression/direct_send_guard_test.go`
- Create: `internal/application/lark/cardregression/testdata/direct_send_allowlist.txt`

- [ ] **Step 1: Write the failing test**

测试逻辑：
- 扫描仓库中的直接发卡调用。
- 仅允许出现在 allowlist 文件列出的路径中。

第一阶段允许保留的兼容点要明确记录。

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/cardregression -run TestDirectSendAllowlist -count=1
```
Expected:
- FAIL，直到 allowlist 和扫描逻辑定稿。

- [ ] **Step 3: Write minimal implementation**

建议：
- 第一阶段直接调用 `rg` 或 Go 内部扫描文件内容。
- 匹配符号：
  - `sendCompatibleCard(`
  - `sendCompatibleCardJSON(`
  - `sendCompatibleRawCard(`
  - `larkmsg.CreateMsgCardWithResp(`
  - `larkmsg.CreateCardJSON(`
  - `larkmsg.CreateRawCard(`

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
GOCACHE=/tmp/betago-gocache go test ./internal/application/lark/cardregression -run TestDirectSendAllowlist -count=1
```
Expected:
- PASS

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/cardregression/direct_send_guard_test.go internal/application/lark/cardregression/testdata/direct_send_allowlist.txt
git commit -m "test: guard direct card sends with allowlist"
```

## Chunk 5: Documentation and Skill Integration

### Task 10: Update README and skill usage

**Files:**
- Modify: `internal/application/lark/carddebug/README.md`
- Modify: `.codex/skills/lark-card-debug/SKILL.md`
- Create: `internal/application/lark/cardregression/README.md`

- [ ] **Step 1: Write the failing docs/test expectation**

至少在文档中覆盖：
- `--list-scenes`
- `--scene`
- `--suite smoke`
- `--to-chat-id`
- `--dry-run`
- `--report-json`
- “发送目标”和“业务上下文”的区别

- [ ] **Step 2: Update the docs**

要求：
- skill 默认推荐先 `--dry-run` 再真实发送。
- 对 live-data scene 明确说明需要哪些上下文参数。

- [ ] **Step 3: Verify docs are consistent with CLI**

Run:
```bash
rg -n "list-scenes|suite smoke|report-json|scene" internal/application/lark/carddebug/README.md .codex/skills/lark-card-debug/SKILL.md internal/application/lark/cardregression/README.md
```
Expected:
- 所有新参数都有说明，且名称一致。

- [ ] **Step 4: Commit**

```bash
git add internal/application/lark/carddebug/README.md .codex/skills/lark-card-debug/SKILL.md internal/application/lark/cardregression/README.md
git commit -m "docs: document card regression workflow"
```

## Suggested Execution Order

1. 先做 Chunk 1，建立协议、registry、runner。
2. 再做 Chunk 2，让现有 CLI 能消费这套协议。
3. 然后优先迁移已有 debug spec，对现有能力零损迁移。
4. 再补 `help` / `command.form` / `wordcount` / `music` 这些高价值但尚未统一的卡片场景。
5. 最后加 guardrail，防止回退。

## Testing Strategy

- 单元测试：`cardregression` 包的协议、registry、runner、guard。
- 包级回归：`config`、`schedule`、`ratelimit`、`command`、`handlers`、`neteaseapi`。
- CLI 回归：`lark-card-debug --scene ... --dry-run`。
- CLI 语义回归：legacy `--spec` alias、`--suite smoke`、`--suite live-smoke`、`--report-json`。
- 人工回归：指定测试 `chat_id` 的 send-smoke suite。

## Manual Regression Flow

建议团队日常使用下面这条流：

1. 开发前：
```bash
go run ./cmd/lark-card-debug --suite smoke --dry-run
```

2. 修改单场景后：
```bash
go run ./cmd/lark-card-debug --scene schedule.list --case smoke-default --to-chat-id oc_test_xxx --chat-id oc_ctx_xxx
```

3. 发版前整组回归：
```bash
go run ./cmd/lark-card-debug --suite send-smoke --to-chat-id oc_test_xxx --report-json /tmp/card-regression.json
```

4. 需要真实业务上下文的场景：
```bash
go run ./cmd/lark-card-debug --suite live-smoke --to-chat-id oc_test_xxx --chat-id oc_ctx_xxx --actor-open-id ou_admin_xxx
```

## Risks & Gotchas

- 若第一批 manifest 拉得太大，迁移成本会失控；建议先锁高价值场景。
- direct send allowlist 会揭露不少历史路径，第一阶段要允许兼容名单存在，但不能继续新增。
- `music` 场景可能涉及资源上传和签名链路，需明确“列表卡 smoke”与“播放链路”是两个层次。
- `help` / `command.form` 这类卡片没有外部业务上下文，更适合先纳入 smoke，作为回归体系的低风险样板。

## Ready-to-Execute Definition

当以下条件满足时，可以进入实现：

- 设计文档中的协议命名和分包位置已确认。
- 第一批 manifest 场景名单已确认。
- `cmd/lark-card-debug` 是扩展现有 CLI 还是拆新二进制已确认；本计划默认扩展现有 CLI。

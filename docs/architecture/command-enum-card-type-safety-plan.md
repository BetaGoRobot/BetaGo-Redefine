# Plan: 命令枚举收口与卡片当前值回显改造

**Generated**: 2026-03-12
**Estimated Complexity**: High

## Overview
目标是把当前“看起来像枚举、实际还是 string”的命令参数体系，逐步收口为真正的闭集类型，并让同一份类型信息同时驱动以下几件事：
- handler 注册时的参数声明
- 命令卡片里的下拉选项和值校验
- handler 内部拿到的强类型参数
- 调试/发卡技能在构造 Value 时的安全赋值
- 卡片回调后二次渲染时的当前值回显

当前代码已经具备一条可复用主链路：`typed args struct -> ApplyCLIArgs -> CommandArg -> command form`。问题在于这条链路目前主要靠 `enum:"..."` tag 和裸 `string` 字段驱动，元数据存在，但类型边界并没有真正建立起来。

本次改造建议分两层推进：
1. 先建立“枚举描述与注册”的统一内核，并兼容现有 tag。
2. 再把稳定闭集参数逐步迁移到真正的 Go 枚举类型，避免一次性全量重构。

## Prerequisites
- 不改动动态值域的参数模型。人员、群、运行时查询结果等仍然视为动态输入，不强行枚举化。
- 保留现有 `enum` struct tag 的兼容路径，作为迁移期 fallback。
- 以现有卡片能力为基础实现，不额外引入新的前端协议。

## Sprint 1: 枚举域盘点与模型收敛
**Goal**: 明确哪些参数是真正闭集，哪些不是，并设计统一的枚举描述模型。

**Demo/Validation**:
- 输出一份参数分类表，能覆盖现有主要 handler。
- 在 `pkg/xcommand` 内拿到一份可表达“闭集枚举/布尔/动态/自由输入”的统一模型草案。

### Task 1.1: 盘点现有 handler 参数并分类
- **Location**: 
  - `internal/application/lark/handlers/config_handler.go`
  - `internal/application/lark/handlers/music_handler.go`
  - `internal/application/lark/handlers/schedule_handler.go`
  - `internal/application/lark/handlers/debug_handler.go`
- **Description**: 审核所有 typed args 字段，按四类归档：闭集枚举、布尔、动态选择、自由文本。
- **Dependencies**: 无
- **Acceptance Criteria**:
  - `scope`、`music type`、`schedule status/type` 这类稳定闭集被明确标注为优先迁移对象。
  - 人员选择、聊天对象、依赖后端查询结果的选择项被明确标注为动态域，不进入编译期枚举。
- **Validation**:
  - 形成 checklist，并与当前 `enum` tag 使用点逐一对应。

### Task 1.2: 设计统一的参数值域模型
- **Location**:
  - `pkg/xcommand/base.go`
  - `pkg/xcommand/cli_args.go`
- **Description**: 为 `CommandArg` 增加更明确的值域表达能力，例如区分 `Kind=Enum/Bool/Dynamic/Text`，并让 option 来源不再只依赖 struct tag 字符串。
- **Dependencies**: Task 1.1
- **Acceptance Criteria**:
  - `CommandArg` 能表达“这是闭集值域”以及“选项、默认值、解析器来源”。
  - 布尔不再作为特殊散落逻辑存在，而是纳入统一值域模型。
- **Validation**:
  - 通过设计稿或最小测试用例证明 `enum` 与 `bool` 都能落入同一套元数据结构。

### Task 1.3: 定义强类型枚举描述协议
- **Location**:
  - `pkg/xcommand/`
- **Description**: 引入泛型或接口式的枚举描述协议，例如让某类 `~string` 类型可注册自己的 label、合法值、默认值、解析逻辑。
- **Dependencies**: Task 1.2
- **Acceptance Criteria**:
  - 同一份描述可以导出 `[]CommandArgOption`。
  - 同一份描述可以做 `Parse(string) (T, error)`。
  - 同一份描述可以提供默认值与帮助文案。
- **Validation**:
  - 设计一个最小示例，例如 `ConfigScope`，证明 UI 选项与 parse 结果来自同一描述源。

### Task 1.4: 保留 enum tag 兼容层
- **Location**:
  - `pkg/xcommand/cli_args.go`
- **Description**: 明确迁移期策略。对于尚未迁移为强类型的字段，仍允许继续使用 `enum:"a:A,b:B"`。
- **Dependencies**: Task 1.3
- **Acceptance Criteria**:
  - 新旧两套方式可以并存。
  - 新方式优先级高于 `enum` tag，避免冲突时来源不清。
- **Validation**:
  - 测试覆盖“typed enum 优先，tag 兜底”的行为。

## Sprint 2: 在 handler 注册时解决枚举声明与解析
**Goal**: 让 handler 注册阶段就能自动拿到安全枚举元数据，并在 parse 阶段产出强类型参数。

**Demo/Validation**:
- 新注册的 typed handler 不需要手写 `enum` tag，也能在命令卡片里出现选项。
- handler 内部可以直接拿到类型化后的枚举值，而不是再自己判断字符串。

### Task 2.1: 扩展 `ApplyCLIArgs` / `describeCLIArgs` 的枚举探测能力
- **Location**:
  - `pkg/xcommand/cli_args.go`
- **Description**: 让 `describeCLIArgs` 优先从字段类型或显式注册表推导枚举信息，而不是先看 tag。
- **Dependencies**: Sprint 1
- **Acceptance Criteria**:
  - 字段类型若实现了枚举描述协议，`CommandArg.Options` 自动生成。
  - 字段类型若为 `bool`，自动生成 `true/false` 选项。
  - 只有普通 `string` 且无注册描述时，才退回 tag 或自由文本。
- **Validation**:
  - 针对 `describeCLIArgs` 增加表驱动测试。

### Task 2.2: 建立统一的原始值到强类型参数的解析链路
- **Location**:
  - `pkg/xcommand/`
  - 各 handler 的 typed parse 入口
- **Description**: 将目前基于 `argMap[string]string` 的默认值填充和字符串判断，逐步收口到统一 parse 流程里。
- **Dependencies**: Task 2.1
- **Acceptance Criteria**:
  - 非法枚举值在 parse 阶段被拒绝，而不是进入 handler 再 `if/else`。
  - 默认值在 parse 阶段落定，handler 不再重复写 `if parsed.Scope == "" { parsed.Scope = "chat" }`。
  - handler 获取到的字段类型与注册时一致。
- **Validation**:
  - 测试覆盖默认值、非法值、空值、未知值的行为。

### Task 2.3: 让命令卡片与调试入口直接消费注册时的枚举描述
- **Location**:
  - `internal/application/lark/command/form.go`
  - `cmd/lark-card-debug/`
  - `internal/application/lark/carddebug/`
  - `.codex/skills/lark-card-debug/SKILL.md`
- **Description**: 让命令表单与调试 skill 使用同一份枚举元数据，避免手工拼 option/value。
- **Dependencies**: Task 2.1, Task 2.2
- **Acceptance Criteria**:
  - 命令卡片下拉项完全来自注册时的枚举描述。
  - 调试入口在发送卡片时可以安全地设置枚举值，不再依赖魔法字符串。
- **Validation**:
  - 用一个枚举参数命令生成表单，确认 option/value 与 handler 类型定义一致。

### Task 2.4: 建立注册期与解析期测试基线
- **Location**:
  - `pkg/xcommand/typed_test.go`
  - 新增 `pkg/xcommand/*_test.go`
- **Description**: 为元数据推导、默认值、枚举 parse、兼容 tag 的行为补齐测试。
- **Dependencies**: Task 2.1, Task 2.2
- **Acceptance Criteria**:
  - 核心路径具备单元测试，而不是只靠联调验证。
- **Validation**:
  - 测试跑通，且至少覆盖 `enum type`、`bool`、`legacy tag` 三类字段。

## Sprint 3: 按域逐步把 handler 参数迁移为真正的枚举类型
**Goal**: 让重点 handler 摆脱裸字符串枚举参数，内部直接拿到可补全、可校验的 domain type。

**Demo/Validation**:
- 至少一个完整 handler 域完成迁移，卡片选项、parse、handler 执行全部走新模型。

### Task 3.1: 抽取核心闭集类型
- **Location**:
  - `internal/application/lark/handlers/config_handler.go`
  - `internal/application/lark/handlers/music_handler.go`
  - `internal/application/lark/handlers/schedule_handler.go`
  - 或新增 `internal/application/lark/handlers/types.go`
- **Description**: 为稳定闭集定义独立类型，例如 `ConfigScope`、`MusicSearchType`、`ScheduleStatus`、`ScheduleType`。
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 这些类型各自提供合法值、显示文案、默认值。
  - 不再要求 handler 自己维护一份重复的字符串常量。
- **Validation**:
  - 编译期能限制字段类型，IDE 补全可见枚举常量。

### Task 3.2: 迁移 config handler
- **Location**:
  - `internal/application/lark/handlers/config_handler.go`
- **Description**: 将 `scope` 全面改为强类型，并把默认值与合法值判断挪出业务逻辑。
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - config 相关命令不再在多个函数里重复写 `scope == "" => chat`。
  - 请求构造函数接收明确的 scope 类型或在边界层集中转换。
- **Validation**:
  - 覆盖 list/get/set/delete/feature enable/disable 等路径。

### Task 3.3: 迁移 schedule handler
- **Location**:
  - `internal/application/lark/handlers/schedule_handler.go`
- **Description**: 将 `status/type` 改为强类型，消除 handler 内部基于字符串的分支。
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - schedule 查询与创建参数都通过统一枚举类型声明。
  - 卡片过滤项与命令参数选项来源一致。
- **Validation**:
  - 覆盖查询过滤、创建、编辑等路径的参数 parse。

### Task 3.4: 迁移 music handler
- **Location**:
  - `internal/application/lark/handlers/music_handler.go`
- **Description**: 将 `type` 改为 `MusicSearchType`，统一 song/album/playlist 等枚举域的解析与默认值。
- **Dependencies**: Task 3.1
- **Acceptance Criteria**:
  - handler 内部不再通过裸字符串判断 `song/album/playlist`。
  - 新增的 `playlist` 支持走同一套强类型路径。
- **Validation**:
  - 覆盖 `song`、`album`、`playlist` 三种分支。

### Task 3.5: 保留动态域为动态，不过度枚举化
- **Location**:
  - 所有涉及人员/群/运行态数据的卡片与 handler
- **Description**: 对动态值域保持 string 或显式 provider，避免为了“类型安全”把运行时数据伪装成枚举。
- **Dependencies**: Sprint 1
- **Acceptance Criteria**:
  - 闭集与动态域边界明确。
  - 不把 user picker、chat_id、远端查询结果等错误纳入编译期枚举。
- **Validation**:
  - 设计审查即可，通过清单约束未来新增参数的归类方式。

## Sprint 4: 统一卡片 current/default value 回填
**Goal**: 所有可选择类卡片控件在用户操作后重建卡片时，都能看见当前选中值。

**Demo/Validation**:
- 在命令卡片、schedule 筛选卡片、权限卡片等场景里，完成一次选择后，卡片重建仍保留当前值显示。

### Task 4.1: 命令表单 `select_static` 增加 `InitialOption`
- **Location**:
  - `internal/application/lark/command/form.go`
- **Description**: 当前命令卡片只展示“当前值: xxx”提示，没有把当前值真正回填到下拉控件。需要将解析后的当前值传给 `SelectStaticOptions.InitialOption`。
- **Dependencies**: Sprint 2
- **Acceptance Criteria**:
  - 枚举参数在卡片重建时，下拉框直接显示已选项。
  - hint 文本可保留作为辅助，但不再承担唯一回显职责。
- **Validation**:
  - 对同一命令多次编辑参数，卡片 UI 始终显示当前选中项。

### Task 4.2: 修复 schedule 创建者筛选器的初始值回填
- **Location**:
  - `internal/application/lark/schedule/card_view.go`
- **Description**: `buildTaskCreatorPicker` 当前未设置 `InitialOption`，用户选完创建者后无法从卡片看出当前筛选值。
- **Dependencies**: 无
- **Acceptance Criteria**:
  - `CreatorOpenID` 被正确映射到 `SelectPersonOptions.InitialOption`。
- **Validation**:
  - 选择创建者后刷新/重绘卡片，人员框保留已选对象。

### Task 4.3: 全量审计各类下拉框、筛选框、人员框的回填状态
- **Location**:
  - `internal/application/permission/card.go`
  - `internal/application/lark/schedule/card_view.go`
  - `internal/application/lark/command/form.go`
  - `internal/application/config/card_view.go`
  - 其他 `SelectStatic` / `SelectPerson` 使用点
- **Description**: 以 `permission` 的 target picker 为正例，审计所有选择类控件是否在 rebuild 时带上当前值或 default 值。
- **Dependencies**: Task 4.1, Task 4.2
- **Acceptance Criteria**:
  - 每个选择类控件都明确属于“有当前值回填”或“本场景不需要回填”两类之一，不允许遗漏。
  - 文本输入类控件继续使用 `default_value` 或等价能力保持当前值。
- **Validation**:
  - 输出一份控件清单与处理结论，并补充对应测试或联调步骤。

### Task 4.4: 统一卡片回调后的状态再水化逻辑
- **Location**:
  - `pkg/cardaction/action.go`
  - `internal/application/lark/schedule/card_action.go`
  - `internal/application/permission/card_action.go`
  - 其他卡片 action 处理点
- **Description**: 当前 callback parser 已能拿到 `Option` / `Options` / `Checked`，但各模块重建卡片时并未统一使用。需要建立一致的“解析 -> 状态对象 -> 视图重建”路径。
- **Dependencies**: Task 4.1, Task 4.2
- **Acceptance Criteria**:
  - 选择类控件不会只在回调瞬间拿到值，随后重建时丢失。
  - 视图状态对象成为当前值的唯一来源。
- **Validation**:
  - 补充 action parse 与 view rebuild 的联动测试。

## Sprint 5: 收尾、测试与迁移策略
**Goal**: 让新旧模型可以平滑共存，并为后续新增 handler 提供明确规范。

**Demo/Validation**:
- 新增一个带强类型枚举参数的 handler，不需要手写散落的 tag、option、default 逻辑。
- 旧 handler 未迁移时仍可正常工作。

### Task 5.1: 制定迁移准则与代码规范
- **Location**:
  - `pkg/xcommand/`
  - `README.md` 或相关开发文档
  - `.codex/skills/lark-card-debug/SKILL.md`
- **Description**: 记录今后新增参数时的决策规则：何时使用强类型枚举，何时保持动态域，何时保留文本输入。
- **Dependencies**: Sprint 3, Sprint 4
- **Acceptance Criteria**:
  - 新代码不再默认用 `string + enum tag` 表达闭集参数。
  - 调试 skill 知道如何安全设置枚举值。
- **Validation**:
  - 文档示例与实际 API 一致。

### Task 5.2: 补齐测试矩阵
- **Location**:
  - `pkg/xcommand/*_test.go`
  - `internal/application/lark/command/form_test.go`
  - `internal/infrastructure/lark_dal/larkmsg/card_v2_test.go`
  - 相关 handler / card action tests
- **Description**: 建立三层测试：元数据推导、卡片渲染、回调再水化。
- **Dependencies**: 全部前置 sprint
- **Acceptance Criteria**:
  - 每种控件至少有一个“初始渲染 + 用户选择 + 重建后仍保留当前值”的测试。
  - 每种强类型枚举至少有一个“非法值拒绝”的测试。
- **Validation**:
  - CI 可稳定跑通，不依赖人工点击飞书卡片才能验证。

### Task 5.3: 分阶段切换与回滚策略
- **Location**:
  - 实施说明文档
- **Description**: 先迁移最稳定、收益最高的闭集类型，再逐步扩大覆盖面；期间保留 legacy tag 兼容。
- **Dependencies**: 全部前置 sprint
- **Acceptance Criteria**:
  - 任一阶段出问题时，可以回退到 tag 驱动元数据，不影响线上命令可用性。
- **Validation**:
  - 通过代码路径开关或兼容分支证明可回滚。

## Testing Strategy
- `pkg/xcommand` 单元测试
  - 枚举元数据推导
  - 默认值注入
  - 非法值拒绝
  - legacy `enum` tag 兼容
- 命令卡片渲染测试
  - `select_static` 是否写入 `initial_option`
  - 文本输入是否保留 `default_value`
- 卡片 action / 视图状态联动测试
  - `Parsed.Option` / `Parsed.Options` 是否正确再水化到 view state
  - `SelectPerson` 与 `SelectStatic` 回调后是否保留当前值
- 关键 handler 集成验证
  - config `scope`
  - schedule `status/type`
  - music `type(song/album/playlist)`

## Potential Risks & Gotchas
- 不是所有看起来像“可选项”的字段都应该做成编译期枚举。运行时查询、人员、群、远端资源都应保持动态域。
- 若直接把所有字段从 `string` 改成新类型，但默认值仍散落在 handler 内部，会造成双重真相。默认值必须跟 parse/descriptor 绑定。
- `InitialOption` 回填依赖当前值是稳定 ID，而不是展示文案。尤其人员选择器要用 open_id，而不是名称。
- 兼容期内，新枚举描述与旧 `enum` tag 可能同时存在，必须定义优先级，避免 option 来源不一致。
- 某些 handler 可能把空字符串当成“未指定”语义使用。迁移到强类型后，需要明确是否保留 `Unknown/Unset` 零值语义。

## Rollback Plan
- 保留 `enum` struct tag 的解析逻辑作为 fallback，不在第一阶段删除。
- 新的 typed enum descriptor 先以增量方式接入，先迁移 `config/schedule/music` 三类稳定闭集。
- 若某个 handler 迁移后出现兼容问题，可回退为 `string + legacy tag`，不影响命令卡片基础能力。

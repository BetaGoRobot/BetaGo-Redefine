# Lark Command

`internal/application/lark/command` 负责两件事：

1. 注册飞书侧可执行的命令树
2. 把命令帮助、参数表单、卡片回调接回统一命令执行链路

底层泛型框架在 `pkg/xcommand`，这里主要是 Lark 场景适配层。

## 当前能力

### 1. 命令树注册

入口：

- `command.go`
- `NewLarkRootCommand()`
- `RegisterLarkCommands(...)`

约定：

- canonical command 只注册一次
- shortcut / 短命令通过 `AddAliases(...)` 挂到同一个命令节点上
- 需要 default subcommand 的命令显式 `SetDefaultSubCommand(...)`

例如：

- `/wordcount` 是 canonical command
- `/wc` 是 alias
- help / form / execute / validate 都会统一按同一套命令树解析

### 2. 帮助卡

入口：

- `help.go`

当前行为：

- `/help` 返回命令总览
- `/help <path>` 返回 schema v2 帮助卡
- 帮助卡会展示：
  - usage
  - 参数
  - 示例
  - 子命令入口
- 如果命令参数很多，会把“可选参数”折叠起来
- 如果示例很多，也会折叠
- 对有子命令的命令，help 卡会提供“子命令入口”按钮，避免一律落到 default subcommand

别名兼容：

- `/help wc` 会解析到 `/wordcount`
- 但子命令快捷入口仍保留用户输入语义，例如 `/wc summary`

### 3. 参数表单卡

入口：

- `form.go`

当前行为：

- `command.open_form` 把某个 raw command 转成 schema v2 表单卡
- `command.submit_form` 读取 `form_value`，重建 raw command，再回到标准命令执行链路
- 如果用户已有部分参数，表单会尽量回填当前值
- 留空表示沿用当前 raw command 中同名参数；当前版本不支持“通过表单显式清空已有参数”

### 4. 有限枚举与类型安全

推荐优先使用 typed handler + enum 描述，而不是在 handler 里手写字符串判断。

当前约定：

- 有限枚举参数在命令注册阶段就定义
- 表单渲染时自动变成下拉
- 当前值会回填到下拉的 `InitialOption`
- handler 内部直接拿强类型参数，不再重复做裸字符串分支判断

## 扩展建议

### 新增命令

1. 在 `command.go` 注册命令
2. 补 `Description` 和 `Examples`
3. 如果有短命令，使用 `AddAliases(...)`
4. 如果有默认执行分支，使用 `SetDefaultSubCommand(...)`
5. 如果参数有有限枚举，优先做成强类型枚举

### 新增帮助卡 / 表单行为

1. 先改 `help.go` / `form.go`
2. 再补 `help_test.go` / `form_test.go`
3. 不要只改 UI，不改解析链路

### 飞书组件限制

- `form` 不能放进 `collapsible_panel`
- 所以：
  - help 卡可以折叠
  - form 卡要么平铺，要么拆成多步 / 分组

## 调试方式

命令链路建议优先验证下面几类场景：

```text
/help
/help wordcount
/help wc
/wc chunks --question_mode=question
/music --type=playlist 3778678
```

如果要把帮助卡或表单卡直接发到指定会话做人工验收，配合：

- `cmd/lark-card-debug`
- `.codex/skills/lark-card-debug`

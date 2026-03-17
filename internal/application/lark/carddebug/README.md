# Card Debug

`internal/application/lark/carddebug` 提供一套“脱离正常消息链路，直接发卡验证”的调试能力。

它的目标不是替代正式 handler，而是解决下面这类问题：

- 某张卡片很难靠自然消息触发
- 正在改模板变量或 schema v2 结构，需要快速人工验收
- 想把管理卡直接发到某个 `chat_id` / `open_id` 看效果

## 入口

### CLI

- `go run ./cmd/lark-card-debug ...`
- `go run ./cmd/lark-card-debug --list-scenes`
- `go run ./cmd/lark-card-debug --scene <scene-key> --case <case-name> --dry-run`
- `go run ./cmd/lark-card-debug --suite smoke --dry-run --report-json /tmp/regression.json`

### Codex skill

- `.codex/skills/lark-card-debug/scripts/send_card.sh ...`

skill 脚本本质上只是包装并调用同一个 CLI。

## 支持的输入

### 1. 内置 spec

当前内置：

- `config`
- `feature`
- `permission`
- `ratelimit`
- `ratelimit.sample`
- `schedule.list`
- `schedule.task`
- `schedule.sample`
- `wordcount.sample`
- `chunk.sample`

### 1.1 回归 scene

当前已接入统一回归协议的 canonical scene key：

- `help.view`
- `command.form`
- `config.list`
- `feature.list`
- `permission.manage`
- `ratelimit.stats`
- `schedule.list`
- `schedule.query`

说明：

- `--scene` 走 canonical scene key。
- `--spec` 保留兼容旧入口，但内部会优先桥接到 scene registry。
- `--suite smoke` 会跑所有带 `smoke` tag 的 case。
- `--suite live-smoke` 会跑所有带 `live` tag 的 case。

### 2. 模板卡

- `--template`
- `--vars-json`

### 3. 原生 schema v2 卡

- `--card-json`
- `--card-file`

## 常见参数

- `--to-open-id`: 把卡片发给某个用户
- `--to-chat-id`: 把卡片发到某个群
- `--chat-id`: 业务上下文 chat_id
- `--actor-open-id`: 以谁的上下文构卡
- `--target-open-id`: 卡片内部作用于谁
- `--id`: 业务对象 ID，例如 `schedule.task`
- `--dry-run`: 只构卡，不发送
- `--print-payload`: 输出最终 payload

## 常用示例

```bash
go run ./cmd/lark-card-debug --list-specs
go run ./cmd/lark-card-debug --list-scenes
go run ./cmd/lark-card-debug --spec ratelimit.sample --to-open-id ou_xxx
go run ./cmd/lark-card-debug --scene config.list --case smoke-default --dry-run --print-payload
go run ./cmd/lark-card-debug --scene permission.manage --case live-default --chat-id oc_xxx --actor-open-id ou_admin --to-open-id ou_xxx
go run ./cmd/lark-card-debug --suite smoke --dry-run --report-json /tmp/card-regression.json
go run ./cmd/lark-card-debug --spec config --to-open-id ou_xxx --chat-id oc_xxx --actor-open-id ou_admin
go run ./cmd/lark-card-debug --template NormalCardReplyTemplate --vars-json '{"title":"BetaGo","content":"调试卡片"}' --to-open-id ou_xxx
go run ./cmd/lark-card-debug --card-file /tmp/card.json --to-open-id ou_xxx
```

## 约束

1. 不要混淆“发送目标”和“构卡上下文”
- `to_open_id` / `to_chat_id` 决定发给谁
- `chat_id` / `actor_open_id` / `target_open_id` 决定卡片以什么业务上下文构建

2. 管理类卡片通常需要完整上下文
- `config`
- `feature`
- `permission`
- `schedule.list`

3. 如果只是想看 payload，优先 `--dry-run --print-payload`

## 代码结构

- `card_debug.go`: spec 注册、构卡、发送目标解析
- `internal/application/lark/cardregression`: scene 协议、registry、runner
- `card_debug_test.go`: 构卡与 spec 基础测试
- `cmd/lark-card-debug`: CLI 入口

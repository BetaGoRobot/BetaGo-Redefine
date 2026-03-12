---
name: lark-card-debug
description: 在 BetaGo-Redefine 仓库中开发、重构或验证飞书卡片时使用。适用于需要把模板卡片、schema v2 原生卡片或已生成的卡片 JSON 发送到指定 chat_id/open_id 做人工验收、联调或回归截图的场景。
---

# Lark Card Debug

这个 skill 只负责一件事：在开发过程中，把卡片发到指定飞书账号或会话做验证。

## 何时使用

- 你正在修改卡片布局、字段、按钮、footer、refresh payload。
- 你需要把卡片直接发到某个测试账号，而不是等线上消息链路自然触发。
- 你想验证模板卡片变量、schema v2 卡片 JSON，或仓库内置的卡片调试 spec。

## 入口

优先用脚本：

```bash
.codex/skills/lark-card-debug/scripts/send_card.sh --list-specs
.codex/skills/lark-card-debug/scripts/send_card.sh --list-templates
```

脚本内部会编译并执行仓库内的 `./cmd/lark-card-debug`。

## 常用流程

1. 先列出可用 spec 或模板，确认有没有现成入口。
2. 优先发仓库内置 `--spec`。
3. 如果是模板卡片，改用 `--template` + `--vars-json`。
4. 如果你手头已经有完整 schema v2 JSON，改用 `--card-file` 或 `--card-json`。
5. 目标是单个账号时优先 `--to-open-id`；目标是群或话题上下文时优先 `--to-chat-id`。
6. 管理类卡片如果发到私聊，通常还要额外传 `--chat-id` 作为业务上下文。

## 常用命令

发送到指定用户：

```bash
.codex/skills/lark-card-debug/scripts/send_card.sh \
  --spec ratelimit.sample \
  --to-open-id ou_xxx
```

发送模板卡片：

```bash
.codex/skills/lark-card-debug/scripts/send_card.sh \
  --template NormalCardReplyTemplate \
  --vars-json '{"content":"调试卡片","title":"BetaGo"}' \
  --to-open-id ou_xxx
```

发送本地 JSON：

```bash
.codex/skills/lark-card-debug/scripts/send_card.sh \
  --card-file /tmp/card.json \
  --to-open-id ou_xxx
```

发送管理类卡片到私聊，但保留群上下文：

```bash
.codex/skills/lark-card-debug/scripts/send_card.sh \
  --spec config \
  --to-open-id ou_xxx \
  --chat-id oc_xxx \
  --actor-open-id ou_admin \
  --scope chat
```

查看某个具体 schedule 任务卡：

```bash
.codex/skills/lark-card-debug/scripts/send_card.sh \
  --spec schedule.task \
  --id 20260312093000-debugA \
  --to-open-id ou_xxx
```

只看 payload 不发送：

```bash
.codex/skills/lark-card-debug/scripts/send_card.sh \
  --spec schedule.sample \
  --chat-id oc_xxx \
  --dry-run \
  --print-payload
```

## 参数约定

- `--to-open-id`: 把卡片直接发给某个用户。
- `--to-chat-id`: 把卡片直接发到某个群聊。
- `--chat-id`: 卡片业务上下文。对 config/feature/permission/ratelimit 这类管理卡很重要。
- `--id`: 业务对象 ID。当前主要用于 `schedule.task`。
- `--actor-open-id`: 用谁的身份构造卡片。管理卡默认应传管理员或 bootstrap admin。
- `--target-open-id`: 卡片内部的目标用户，例如权限面板要看谁。
- `--spec`: 使用仓库内置调试卡片。
- `--template`: 使用模板卡片名称或模板 ID。
- `--vars-json`: 模板变量 JSON。
- `--card-json` / `--card-file`: 直接发送现成 card JSON。

## 注意

- 不要假设 `--to-open-id` 等于 `--actor-open-id`。调试“发给谁看”和“以谁的上下文构卡”经常不是一个人。
- 如果 `config` / `permission` 一类卡片构建失败，先检查 `--chat-id` 和 `--actor-open-id`。
- 当当前仓库里没有你要的 spec 时，先补 CLI spec 或直接生成 JSON 再发，不要在 skill 里硬编码业务数据。

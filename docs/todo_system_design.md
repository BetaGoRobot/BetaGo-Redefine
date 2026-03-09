# 待办事项与提醒系统设计文档

## 概述

这是一个基于自然语言驱动的待办事项和提醒系统，完全集成到 BetaGo-Redefine 飞书机器人中。

## 架构设计

```
┌─────────────────────────────────────────────────────────────┐
│                   用户自然语言输入                          │
│   "明天下午3点提醒我开会" / "帮我记录一下要写代码"        │
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│              LLM (豆包大模型) + Function Call               │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│  │ create_todo  │  │update_todo   │  │list_todos    │    │
│  └──────────────┘  └──────────────┘  └──────────────┘    │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│  │delete_todo   │  │create_remind │  │list_reminders│    │
│  └──────────────┘  └──────────────┘  └──────────────┘    │
│  ┌──────────────┐                                          │
│  │delete_remind │                                          │
│  └──────────────┘                                          │
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│              Application Layer (应用层)                      │
│              internal/application/lark/todo/                │
├─────────────────────────────────────────────────────────────┤
│  - service.go: 业务逻辑服务                                 │
│  - func_call_tools.go: Function Call 工具注册               │
│  - scheduler.go: 提醒调度器                                 │
└────────────────────────┬────────────────────────────────────┘
                         │
         ┌───────────────┴───────────────┐
         │                               │
         ▼                               ▼
┌──────────────────┐        ┌──────────────────────────┐
│  Domain Layer    │        │ Infrastructure Layer     │
│  (领域层)        │        │  (基础设施层)            │
├──────────────────┤        ├──────────────────────────┤
│ - 领域模型       │        │ - todo/repository.go    │
│   Todo/Reminder  │        │ - todo/query.go        │
└──────────────────┘        │ - db/model/todo_items.go│
                            └──────────┬───────────────┘
                                       │
                                       ▼
                            ┌──────────────────────┐
                            │   PostgreSQL         │
                            │  - todo_items        │
                            │  - todo_reminders    │
                            └──────────────────────┘
```

## 核心功能

### 1. 待办事项管理

| 功能 | Function Call 工具 | 描述 |
|------|-------------------|------|
| 创建待办 | `create_todo` | 创建新的待办事项，支持标题、描述、优先级、截止时间、标签、负责人 |
| 更新待办 | `update_todo` | 更新待办的状态、标题、描述、截止时间等 |
| 列出待办 | `list_todos` | 列出当前群组的所有待办，支持按状态过滤 |
| 删除待办 | `delete_todo` | 删除指定的待办事项 |

### 2. 提醒管理

| 功能 | Function Call 工具 | 描述 |
|------|-------------------|------|
| 创建提醒 | `create_reminder` | 创建新的提醒，支持一次性/每日/每周/每月重复 |
| 列出提醒 | `list_reminders` | 列出当前群组的所有待触发提醒 |
| 删除提醒 | `delete_reminder` | 删除指定的提醒 |

### 3. 自动提醒调度

- 后台调度器每 30 秒检查一次待触发的提醒
- 到达触发时间自动发送飞书消息提醒
- 支持重复提醒（每日、每周、每月）

## 数据模型

### TodoItem (待办事项表)

| 字段 | 类型 | 说明 |
|------|------|------|
| id | VARCHAR(64) | 主键 |
| chat_id | VARCHAR(128) | 群组/单聊ID |
| creator_id | VARCHAR(128) | 创建者ID |
| creator_name | VARCHAR(256) | 创建者名称 |
| assignee_id | VARCHAR(128) | 负责人ID |
| title | VARCHAR(512) | 标题 |
| description | TEXT | 描述 |
| status | VARCHAR(32) | 状态: pending/doing/done/cancelled |
| priority | VARCHAR(32) | 优先级: low/medium/high/urgent |
| due_at | TIMESTAMPTZ | 截止时间 |
| completed_at | TIMESTAMPTZ | 完成时间 |
| tags | TEXT[] | 标签数组 |
| created_at | TIMESTAMPTZ | 创建时间 |
| updated_at | TIMESTAMPTZ | 更新时间 |

### TodoReminder (提醒表)

| 字段 | 类型 | 说明 |
|------|------|------|
| id | VARCHAR(64) | 主键 |
| todo_id | VARCHAR(64) | 关联的待办ID |
| chat_id | VARCHAR(128) | 群组/单聊ID |
| creator_id | VARCHAR(128) | 创建者ID |
| title | VARCHAR(512) | 标题 |
| content | TEXT | 内容 |
| type | VARCHAR(32) | 类型: once/daily/weekly/monthly |
| status | VARCHAR(32) | 状态: pending/triggered/cancelled |
| trigger_at | TIMESTAMPTZ | 触发时间 |
| repeat_rule | VARCHAR(256) | 重复规则 (CRON) |
| created_at | TIMESTAMPTZ | 创建时间 |
| updated_at | TIMESTAMPTZ | 更新时间 |

## 使用示例

### 自然语言交互示例

#### 创建待办
```
用户: 帮我记录一下明天要写周报
LLM: [调用 create_todo]
机器人: ✅ 待办创建成功！

       标题: 写周报
       ID: `xxx`
```

#### 设置提醒
```
用户: 明天下午3点提醒我开会
LLM: [调用 create_reminder]
机器人: ⏰ 提醒创建成功！

       标题: 开会
       触发时间: 2026-03-07 15:00:00
       ID: `xxx`
```

#### 查看待办
```
用户: 我有哪些待办？
LLM: [调用 list_todos]
机器人: 📋 待办事项列表（共 3 项）

       1. ⭕ 🟡 **写周报**
          截止: 2026-03-07 18:00:00
          ID: `xxx`

       2. ⭕ 🔴 **准备汇报材料**
          ID: `xxx`
```

#### 完成待办
```
用户: 把写周报那个任务标记完成
LLM: [调用 update_todo status=done]
机器人: 🎉 恭喜完成任务！

       标题: 写周报
```

## 部署说明

### 1. 数据库迁移

执行 SQL 迁移脚本：

```bash
psql -U postgres -d betago -f script/migrations/001_add_todo_tables.sql
```

或者手动执行：

```sql
-- 创建待办事项表
CREATE TABLE IF NOT EXISTS todo_items (...);

-- 创建提醒表
CREATE TABLE IF NOT EXISTS todo_reminders (...);
```

### 2. 初始化集成

系统已自动集成到主程序中：

- `cmd/larkrobot/main.go` 中初始化待办系统
- `internal/application/lark/handlers/tools.go` 中注册 Function Call 工具
- 提醒调度器在后台自动运行

### 3. 配置

无需额外配置，使用现有的数据库连接即可。

## 文件结构

```
BetaGo_v2/
├── internal/
│   ├── domain/
│   │   └── todo/
│   │       └── model.go              # 领域模型
│   ├── application/lark/
│   │   └── todo/
│   │       ├── service.go            # 应用服务
│   │       ├── func_call_tools.go    # Function Call 工具
│   │       └── scheduler.go          # 提醒调度器
│   ├── infrastructure/
│   │   ├── db/
│   │   │   └── model/
│   │   │       └── todo_items.go     # 数据库模型
│   │   └── todo/
│   │       ├── repository.go         # 仓储实现
│   │       └── query.go              # 数据库查询
│   └── ...
├── script/
│   └── migrations/
│       └── 001_add_todo_tables.sql   # 数据库迁移脚本
└── docs/
    └── todo_system_design.md          # 本文档
```

## 扩展建议

### 1. 卡片交互
- 为待办事项创建交互式飞书卡片
- 支持点击按钮切换状态、添加评论等

### 2. 高级提醒
- 支持自定义 CRON 表达式
- 支持提前提醒（如提前15分钟、提前1小时）
- 支持提醒方式选择（消息、@所有人等）

### 3. 统计分析
- 待办完成率统计
- 个人/团队 productivity 报告
- 周期回顾（周报、月报）

### 4. 更多集成
- 与飞书日历同步
- 与 GitHub Issues 集成
- 支持子任务/ checklist

### 5. 智能增强
- 自动识别对话中的待办意图
- 智能推荐优先级
- 基于历史数据的时间预估

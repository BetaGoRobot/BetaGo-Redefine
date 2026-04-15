# 历史记录挡板与 Chat 纠偏机制设计

## 1. 概述

本设计实现两个核心能力：
1. **历史记录挡板**：按 chat_id 维度的动态时间过滤，限制 AI 可获取的历史范围
2. **Chat 纠偏机制**：支持用户纠错（ReHF）和动态上下文注入

## 2. 历史记录挡板

### 2.1 配置机制

| 配置键 | 作用域 | 格式 | 说明 |
|--------|--------|------|------|
| `history_cutoff_time` | ScopeChat | RFC3339 时间字符串 | 该时间之前的消息不参与召回/检索 |

### 2.2 覆盖范围

所有历史消息召回路径均遵循此限制：

| 路径 | 文件 | 改动 |
|------|------|------|
| 聊天历史拉取 | `handlers/chat_handler.go` (`GenerateChatSeq`) | 读取配置并作为 StartTime |
| 混合搜索 | `history/search.go` (`HybridSearch`) | 读取配置并作为 StartTime |
| RAG 召回 | `retriever.Cli().RecallDocs` | 通过 `StartTime` 参数传递 |

### 2.3 Tool

**名称**: `set_history_cutoff`

**参数**:
```json
{
  "timestamp": "2024-01-01T00:00:00Z"  // RFC3339 格式
}
```

**触发示例**: "遗忘 2024 年以前的消息"

### 2.4 命令

走 xcommand 注册：
- 命令名：`forget`
- 示例：`/bb forget 2024-01-01`
- 解析：提取时间字符串并调用配置写入

## 3. Chat 纠偏机制

### 3.1 用户纠错（ReHF）

#### 触发识别

AI 通过自然语言识别纠错意图，无需固定 pattern。System prompt 中告知 AI：
> 当用户纠正你的回复时（如"不是的，应该是xxx"），调用 `store_correction` 工具记录纠正内容。

#### Tool

**名称**: `store_correction`

**参数**:
```json
{
  "original_context": "用户原始消息或对话摘要",
  "correction": "正确的回复内容",
  "reason": "可选，纠正原因"
}
```

#### 存储

| 配置键 | 作用域 | 格式 |
|--------|--------|------|
| `chat_corrections:{chat_id}` | ScopeChat | JSON 数组 |

**JSON 数组格式**:
```json
[
  {
    "timestamp": "2024-01-01T00:00:00Z",
    "user_id": "ou_xxx",
    "original_context": "用户问xxx，我回答yyy",
    "correction": "正确应该回答zzz",
    "reason": "可选"
  }
]
```

#### 注入

在 `buildStandardChatUserPrompt` 时，将纠错历史序列化并附加到 prompt 末尾。

### 3.2 动态上下文注入

#### 配置机制

| 配置键 | 作用域 | 格式 | 说明 |
|--------|--------|------|------|
| `chat_extra_context` | ScopeChat | 字符串 | 额外的上下文/规则，附加到 system prompt |
| `chat_persona` | ScopeChat | 字符串 | 群专属人设，完全覆盖默认 system prompt |

#### 注入时机

- `chat_persona`: 存在时完全替换 `buildStandardChatSystemPrompt` 的输出
- `chat_extra_context`: 附加在 system prompt 末尾

#### Tool

**名称**: `set_chat_context`

**参数**:
```json
{
  "context_type": "extra_context | persona",
  "content": "要设置的上下文内容"
}
```

## 4. 代码改动

### 4.1 config/manager.go

新增配置键：
```go
KeyHistoryCutoffTime  ConfigKey = "history_cutoff_time"
KeyChatCorrections   ConfigKey = "chat_corrections"
KeyChatExtraContext  ConfigKey = "chat_extra_context"
KeyChatPersona       ConfigKey = "chat_persona"
```

### 4.2 handlers/chat_handler.go

**GenerateChatSeq 改动**:
1. 读取 `history_cutoff_time` 配置
2. 传递给 `history.New(ctx).Query(...)` 的 `StartTime` 参数
3. 读取 `chat_corrections` 配置，序列化并附加到 prompt
4. 读取 `chat_persona` / `chat_extra_context`，注入 system prompt

### 4.3 history/search.go

**HybridSearch 改动**:
1. 接收 `cutoff_time` 参数
2. 在 `buildHybridSearchFilters` 中应用 `create_time_v2 >= cutoff_time` 过滤

### 4.4 handlers/tools.go

新增 tool handlers：
- `set_history_cutoff`: 写入 `history_cutoff_time`
- `store_correction`: 追加到 `chat_corrections` 数组
- `set_chat_context`: 写入 `chat_extra_context` 或 `chat_persona`

### 4.5 messages/ops/command_op.go 或新文件

新增 xcommand handler：
- 命令：`forget`
- 解析时间参数，调用 `set_history_cutoff`

## 5. 默认行为

- 无挡板设置时：**不限制**，加载全部历史（现有行为）
- 无纠错时：**无额外上下文**
- `chat_persona` 未设置时：使用默认 system prompt

## 6. 优先级

| 配置 | 优先级（高→低） |
|------|----------------|
| `chat_persona` | 覆盖默认 system prompt |
| `chat_extra_context` | 附加到 system prompt |
| `history_cutoff_time` | 过滤历史消息 |
| `chat_corrections` | 附加纠错历史 |

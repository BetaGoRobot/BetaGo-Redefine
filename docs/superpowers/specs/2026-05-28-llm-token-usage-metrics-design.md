# 大模型 Token 消耗统计设计

## 背景

当前项目的大模型调用主要集中在 `internal/infrastructure/ark_dal`，业务来源包括用户聊天、意图识别、消息 embedding、历史检索 embedding、chunking 后台归并、调试命令和工具调用续写。已有在线指标通过 VictoriaMetrics 暴露到管理面的 `/metrics`，离线统计目前没有统一明细表。

需求是对所有来源的大模型调用统计 token 消耗，分为在线 metrics 和离线统计，并至少支持以下维度：

- `chat_name`, `chat_id`
- 用户触发来源的 `user_name`, `open_id`
- 分钟级、小时级、天级聚合

## 目标

- 所有 Ark responses、stream responses、embedding 调用都进入统一统计路径。
- 调用元数据使用显式参数结构体传递，不通过 `context.Context` 隐式携带。
- 不保留旧的无统计 API；本次一次性改完所有调用点。
- 在线 metrics 可被现有 `/metrics` 暴露。
- 离线统计落 Postgres 明细表，支持按分钟、小时、天聚合查询。
- 后台或系统触发调用可以显式记录来源，用户字段允许为空。

## 非目标

- 本阶段不做费用换算。不同模型单价可能变化，离线可基于 token 明细另行计算。
- 本阶段不做独立报表 UI。
- 本阶段不把高基数 token 明细全部塞进 OTel span attributes。

## 显式调用契约

新增 `internal/infrastructure/llmusage` 包，定义显式 scope 和记录结构。

```go
type SourceType string

const (
    SourceTypeUser       SourceType = "user"
    SourceTypeBackground SourceType = "background"
    SourceTypeSystem     SourceType = "system"
    SourceTypeDebug      SourceType = "debug"
)

type Scope struct {
    ChatID     string
    ChatName   string
    OpenID     string
    UserName   string
    SourceType SourceType
    Source     string
}
```

`ark_dal` 对外 API 改为必须显式传 scope：

```go
func CreateResponses(ctx context.Context, body *responses.ResponsesRequest, scope llmusage.Scope) (*responses.ResponseObject, error)
func CreateResponsesStream(ctx context.Context, body *responses.ResponsesRequest, scope llmusage.Scope) (*arkutils.ResponsesStreamReader, error)
func ResponseWithCache(ctx context.Context, sysPrompt, userPrompt, modelID string, scope llmusage.Scope) (string, error)
func ResponseTextWithCache(ctx context.Context, req CachedResponseRequest, scope llmusage.Scope) (string, error)
func EmbeddingText(ctx context.Context, input string, scope llmusage.Scope) ([]float32, model.Usage, error)
func (r *ResponsesImpl[T]) Do(ctx context.Context, scope llmusage.Scope, sysPrompt, userPrompt string, files ...string) (iter.Seq[*ModelStreamRespReasoning], error)
func (r *ResponsesImpl[T]) StreamTurn(ctx context.Context, scope llmusage.Scope, req ResponseTurnRequest) (iter.Seq[*ModelStreamRespReasoning], func() ResponseTurnSnapshot, error)
```

旧签名不保留。所有入口必须构造 `llmusage.Scope`：

- 用户消息路径使用 `BaseMetaData.ChatID`, `ChatName`, `OpenID`，并在调用点补 `UserName`。
- p2p 或 chat name 取不到时使用现有约定值，例如 `p2p` 或 `unknown`。
- chunking、reindex、retriever 等后台任务显式传 `SourceTypeBackground` 或 `SourceTypeSystem`。
- debug 命令显式传 `SourceTypeDebug`，同时携带用户维度。

## 在线 Metrics

使用现有 VictoriaMetrics registry。新增计数器：

```text
betago_llm_requests_total{provider,model,kind,source_type,source,status,chat_id,chat_name,open_id,user_name}
betago_llm_token_usage_total{provider,model,kind,source_type,source,token_type,chat_id,chat_name,open_id,user_name}
```

字段说明：

- `provider`: 当前为 `ark`
- `kind`: `responses`, `responses_stream`, `embedding`
- `status`: `success`, `error`, `usage_missing`
- `token_type`: `prompt`, `completion`, `total`

为控制 label 值长度，`llmusage` 内部使用与 `xhandler` 类似的 UTF-8 安全截断。这里仍会有 `chat_id/open_id` 高基数，这是需求明确要求的查询维度；在线 metrics 用于短期观察，长期审计以 Postgres 明细为准。

## 离线明细表

新增 migration：`script/migrations/012_add_llm_token_usage_records.sql`。

表：`llm_token_usage_records`

核心字段：

- `id BIGSERIAL PRIMARY KEY`
- `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- `bucket_minute TIMESTAMPTZ NOT NULL`
- `bucket_hour TIMESTAMPTZ NOT NULL`
- `bucket_day DATE NOT NULL`
- `provider TEXT NOT NULL`
- `model TEXT NOT NULL`
- `kind TEXT NOT NULL`
- `source_type TEXT NOT NULL`
- `source TEXT NOT NULL DEFAULT ''`
- `chat_id TEXT NOT NULL DEFAULT ''`
- `chat_name TEXT NOT NULL DEFAULT ''`
- `open_id TEXT NOT NULL DEFAULT ''`
- `user_name TEXT NOT NULL DEFAULT ''`
- `status TEXT NOT NULL`
- `prompt_tokens BIGINT NOT NULL DEFAULT 0`
- `completion_tokens BIGINT NOT NULL DEFAULT 0`
- `total_tokens BIGINT NOT NULL DEFAULT 0`
- `response_id TEXT NOT NULL DEFAULT ''`
- `trace_id TEXT NOT NULL DEFAULT ''`
- `error TEXT NOT NULL DEFAULT ''`

索引：

- `(bucket_minute, chat_id)`
- `(bucket_hour, chat_id)`
- `(bucket_day, chat_id)`
- `(bucket_day, open_id)`
- `(created_at)`

离线查询示例：

```sql
SELECT bucket_minute, chat_id, chat_name, open_id, user_name,
       SUM(prompt_tokens) AS prompt_tokens,
       SUM(completion_tokens) AS completion_tokens,
       SUM(total_tokens) AS total_tokens
FROM llm_token_usage_records
GROUP BY bucket_minute, chat_id, chat_name, open_id, user_name;
```

小时级和天级把 `bucket_minute` 替换为 `bucket_hour` 或 `bucket_day`。

## Recorder

`llmusage.Recorder` 负责统一写入在线 metrics 和 Postgres 明细。

```go
type Record struct {
    Scope llmusage.Scope
    Provider string
    Model string
    Kind string
    Status string
    PromptTokens int64
    CompletionTokens int64
    TotalTokens int64
    ResponseID string
    TraceID string
    Error string
    CreatedAt time.Time
}
```

实现约束：

- DB 未初始化时不 panic；在线 metrics 仍记录，离线写入跳过并打 warn。
- token usage 为 0 但调用成功时允许写入，流式 usage 缺失时 `status=usage_missing`。
- bucket 统一在 recorder 内从 `CreatedAt` 计算，避免调用点重复实现。
- recorder 不从 context 读取业务维度。

## 流式 Usage

`CreateResponsesStream` 返回的是 stream reader，token usage 只有在 SDK 的 completed event 或 response object 暴露 usage 时才能精确记录。

实现策略：

- 在 stream 消费循环处理 completed event 时提取 usage 并记录。
- 如果 SDK 事件没有 usage 字段，流结束时记录一次 `status=usage_missing` 的 request 明细，token 为 0。
- 非流式 `CreateResponses` 和 `EmbeddingText` 直接使用响应对象的 usage。

## 入口改造范围

需要一次性替换所有旧 API 调用点：

- `internal/application/lark/handlers/chat_handler.go`
- `internal/application/lark/handlers/debug_handler.go`
- `internal/application/lark/intent/recognizer.go`
- `internal/application/lark/messages/recording/service.go`
- `internal/infrastructure/lark_dal/larkmsg/record.go`
- `internal/infrastructure/retriever/retriver.go`
- `pkg/xchunk/chunking.go`
- `cmd/reindex-embeddings/main.go` 和 `internal/tools/reindexembeddings/reindex.go`
- `internal/infrastructure/ark_dal/*_test.go`

## 测试策略

先写失败测试，再实现。

- `llmusage.Scope` 清洗和默认值测试。
- recorder bucket 计算测试：分钟、小时、天。
- recorder DB 写入测试：明细字段完整，用户字段为空时可写。
- recorder metrics 测试：request counter 和三类 token counter 都递增。
- `EmbeddingText` 测试：成功响应会记录 embedding token。
- `CreateResponses` 测试：成功响应记录 usage，错误响应记录 `status=error`。
- `ResponseTextWithCache` 测试：cache head 和 continuation 都必须带同一个显式 scope。
- 编译级测试确保旧签名全部删除；遗漏调用点会直接编译失败。

## 风险与处理

- 在线 metrics 高基数：保留需求要求的维度，同时截断 label 值；长期统计依赖 Postgres。
- 部分后台入口拿不到 chat/user：显式传空用户字段和 background/system source，保证数据可解释。
- 流式 token usage 可能不可得：记录 `usage_missing`，不伪造 token。
- 一次性替换旧 API 会扩大改动面：通过编译失败兜底，所有旧调用点必须迁移。

# Candidate Ark Stage Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a production Ark-backed four-stage Candidate engine whose only tool execution path is the side-effect-free `ShadowToolRegistry`.

**Architecture:** A strict JSON completion seam wraps `ark_dal.ResponseTextWithCache` and is injected for tests. The engine validates every stage, rebuilds context by stable IDs and original buckets, and runs a bounded structured Draft tool loop through `ShadowToolRegistry.Invoke`. `CandidateRunner` attaches one request-local usage collector and preserves the existing Draft token-usage fallback.

**Tech Stack:** Go, Ark Responses API, `llmusage`, existing `conversationeval` contracts and shadow tools.

---

### Task 1: Production completion seam and stage contracts

**Files:**
- Create: `internal/application/lark/conversationeval/candidate_ark.go`
- Create: `internal/application/lark/conversationeval/candidate_ark_test.go`

- [ ] **Step 1: Write failing constructor and four-stage contract tests**

Define tests around these public contracts:

```go
type ArkCandidateEngineConfig struct {
    ModelID       string
    Scope         llmusage.Scope
    MaxToolRounds int
}

type CandidateCompletionRequest struct {
    Stage        string
    CacheScene   string
    ModelID      string
    SystemPrompt string
    UserPrompt   string
    Scope        llmusage.Scope
}

type CandidateJSONCompletion func(context.Context, CandidateCompletionRequest) (json.RawMessage, error)
```

Assert missing model IDs fail, the production constructor selects
`completeCandidateJSONWithArk`, and an injected completion observes activation,
relevance, context, and draft in order with stage-specific cache scenes, model
IDs, and usage sources.

- [ ] **Step 2: Run RED**

```bash
BETAGO_CONFIG_PATH="$PWD/.dev/config.toml" go test -count=1 -tags custom_skip_vips \
  ./internal/application/lark/conversationeval -run 'TestArkCandidate'
```

Expected: build failure because the Ark Candidate constructors and contracts do
not exist.

- [ ] **Step 3: Implement the minimal production seam**

Implement:

```go
func completeCandidateJSONWithArk(
    ctx context.Context,
    request CandidateCompletionRequest,
) (json.RawMessage, error) {
    text, err := ark_dal.ResponseTextWithCache(ctx, ark_dal.CachedResponseRequest{
        CacheScene: request.CacheScene,
        SystemPrompt: request.SystemPrompt,
        UserPrompt: request.UserPrompt,
        ModelID: request.ModelID,
        Text: strictJSONObjectTextFormat(),
        Reasoning: minimalReasoning(),
        Thinking: disabledThinking(),
    }, request.Scope)
    return json.RawMessage(text), err
}
```

Construct an engine only when the model ID and completion function are present.
Generate stage requests whose cache scene and usage source include the Candidate
stage. Do not call `WithTools`.

- [ ] **Step 4: Run GREEN**

Run the Task 1 command and require PASS.

### Task 2: Context selection with original bucket identity

**Files:**
- Modify: `internal/application/lark/conversationeval/candidate_ark.go`
- Modify: `internal/application/lark/conversationeval/candidate_ark_test.go`

- [ ] **Step 1: Write failing context table tests**

Build candidates from all four buckets:

```go
type candidateContextChoice struct {
    ID         string    `json:"id"`
    Bucket     string    `json:"bucket"`
    TokenCount int       `json:"token_count"`
    OccurredAt time.Time `json:"occurred_at"`
    Content    string    `json:"content"`
}
```

Assert selected items return to their original `messages`, `retrieved`, or
`events` buckets; promoted excluded items use their recorded original bucket.
Assert unknown IDs, duplicates, post-anchor candidates, and selections over the
token budget each return a context-stage error without truncation.

- [ ] **Step 2: Run RED**

Run `go test` with `-run 'TestArkCandidateContext'`; expect the new rejection or
bucket-preservation assertions to fail.

- [ ] **Step 3: Implement strict selection**

Create an internal candidate record containing both the immutable item and its
original bucket. Parse `{"selected_ids":[...]}`, reject invalid selections,
sum selected tokens before mutation, and rebuild the snapshot and excluded list
from those records. Set selected items to `Selected=true` with no exclusion
reason; set all others to `Selected=false` with
`ExcludeReason="candidate_not_selected"`.

- [ ] **Step 4: Run GREEN**

Run the Task 2 command and require PASS.

### Task 3: Bounded Draft tool loop and usage aggregation

**Files:**
- Modify: `internal/application/lark/conversationeval/candidate_ark.go`
- Modify: `internal/application/lark/conversationeval/candidate_ark_test.go`
- Modify: `internal/application/lark/conversationeval/candidate.go`
- Modify: `internal/application/lark/conversationeval/candidate_test.go`

- [ ] **Step 1: Write failing Draft and Runner tests**

Use strict Draft responses:

```json
{"decision":"tool","tool_calls":[{"name":"finance_news_get","arguments":{"symbol":"AAPL"}}]}
{"decision":"reply","reply":"final"}
{"decision":"skip","reply":"","tool_calls":[]}
```

Assert tool calls run only through the injected `ShadowToolRegistry`, completed
observations appear in the next completion prompt, unsafe names fail with zero
handler calls, skip has no calls and empty reply, and another tool request after
the configured cap fails while retaining completed observations.

Emit `llmusage.RecordUsage` from the fake completion and assert the Runner
aggregates all stages. Also assert a fake engine that emits no usage keeps its
existing `CandidateDraft.TokenUsageJSON`.

- [ ] **Step 2: Run RED**

Run `go test` with
`-run 'Test(ArkCandidateDraft|CandidateRunnerAggregates|CandidateRunnerKeeps)'`;
expect Draft-loop and collector assertions to fail.

- [ ] **Step 3: Implement the bounded loop and collector**

Parse Draft tool-call names and object arguments strictly. Invoke calls
sequentially with:

```go
observation, err := input.Tools.Invoke(
    ctx, input.EpisodeID, call.Name, call.Arguments,
)
```

Append the structured observations to the next user prompt. Reject tools on a
skip decision and reject a tool decision at the round cap. In
`candidateRunner.Run`, wrap the stage context with one
`llmusage.WithObserver(ctx, collector)` and encode collector totals when at
least one usage record exists; otherwise retain the Draft-provided usage JSON.

- [ ] **Step 4: Run focused and race verification**

```bash
BETAGO_CONFIG_PATH="$PWD/.dev/config.toml" go test -count=1 -tags custom_skip_vips \
  ./internal/application/lark/conversationeval ./internal/application/lark/handlers
BETAGO_CONFIG_PATH="$PWD/.dev/config.toml" go test -race -count=1 -tags custom_skip_vips \
  ./internal/application/lark/conversationeval ./internal/application/lark/handlers
git diff --check
```

Expected: all packages PASS and no diff errors.

- [ ] **Step 5: Commit**

```bash
git add internal/application/lark/conversationeval
git -c commit.gpgsign=false commit -m "feat: add Ark candidate stage engine"
```

# Tool Error Feedback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make incomplete `create_schedule` calls recoverable by safely returning actionable validation feedback to the model instead of an empty tool output.

**Architecture:** Add a public-safe feedback wrapper in `pkg/xerror`, teach the Ark Responses continuation encoder to distinguish safe feedback from internal failures, and validate schedule argument combinations before execution. Successful tool outputs and existing raw error logs remain unchanged.

**Tech Stack:** Go 1.26, Volcengine Responses SDK, `gresult`, repository schedule typed tools, standard `errors/json/strings` packages.

---

### Task 1: Safe tool feedback error boundary

**Files:**
- Modify: `pkg/xerror/errors.go`
- Create: `pkg/xerror/errors_test.go`

- [ ] **Step 1: Write the failing feedback error tests**

Cover preservation of the cause and explicit safe feedback:

```go
func TestWithToolFeedbackPreservesCauseAndFeedback(t *testing.T) {
    cause := errors.New("database password=secret")
    err := WithToolFeedback(cause, "缺少 message 或 tool_name")
    if !errors.Is(err, cause) { t.Fatal("cause was not preserved") }
    got, ok := ToolFeedback(err)
    if !ok || got != "缺少 message 或 tool_name" { t.Fatalf("feedback = %q/%v", got, ok) }
}

func TestToolFeedbackRejectsPlainInternalError(t *testing.T) {
    if got, ok := ToolFeedback(errors.New("secret")); ok || got != "" {
        t.Fatalf("feedback = %q/%v", got, ok)
    }
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml /root/.go/go1.26.1/bin/go test -tags=custom_skip_vips -v ./pkg/xerror -count=1
```

Expected: compile failure because `WithToolFeedback` and `ToolFeedback` do not exist.

- [ ] **Step 3: Implement the feedback wrapper**

Add an unexported error type with `Error`, `Unwrap`, and `ToolFeedback` methods. `WithToolFeedback` trims feedback and returns the original error when feedback is empty. `ToolFeedback` uses `errors.As` against this method-only interface and returns only non-empty feedback.

- [ ] **Step 4: Run tests and verify GREEN**

Run the command from Step 2. Expected: PASS.

- [ ] **Step 5: Commit the error boundary**

```bash
git add pkg/xerror/errors.go pkg/xerror/errors_test.go
git commit -m "feat: add safe tool feedback errors"
```

### Task 2: Encode failed tool results for model continuation

**Files:**
- Modify: `internal/infrastructure/ark_dal/responses.go`
- Modify: `internal/infrastructure/ark_dal/responses_test.go`

- [ ] **Step 1: Write failing output encoding tests**

Add a table test around a wished-for `toolCallContinuationOutput` helper:

```go
safeCause := errors.New("database password=secret")
cases := []struct {
    name, want, forbidden string
    result gresult.R[string]
}{
    {name: "success", result: gresult.OK("found"), want: "found"},
    {name: "safe feedback", result: gresult.Err[string](xerror.WithToolFeedback(safeCause, "缺少 run_at")), want: "缺少 run_at", forbidden: "password=secret"},
    {name: "internal error", result: gresult.Err[string](safeCause), want: "工具执行失败", forbidden: "password=secret"},
}
```

For errors, decode the output JSON and assert `ok=false`, expected feedback, and absence of the forbidden raw cause.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml /root/.go/go1.26.1/bin/go test -tags=custom_skip_vips -v ./internal/infrastructure/ark_dal -run '^TestToolCallContinuationOutput$' -count=1
```

Expected: compile failure because the helper is missing.

- [ ] **Step 3: Implement and wire the encoder**

Implement:

```go
type toolCallFailureOutput struct {
    OK          bool   `json:"ok"`
    Error       string `json:"error"`
    Instruction string `json:"instruction"`
}
```

Successful results retain `utils.MustMarshalString(res.Value())`. Errors use `xerror.ToolFeedback`; absent feedback falls back to a fixed generic error. Both error paths instruct the model not to assume success and to correct arguments or ask the user. Replace `utils.MustMarshalString(res.Value())` in `OnCallArgs` with this helper.

- [ ] **Step 4: Run focused and existing continuation tests**

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml /root/.go/go1.26.1/bin/go test -tags=custom_skip_vips -v ./internal/infrastructure/ark_dal -run '^(TestToolCallContinuationOutput|TestResponsesImplDefersToolContinuationUntilCompletedAndAggregatesTurnUsage)$' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the continuation fix**

```bash
git add internal/infrastructure/ark_dal/responses.go internal/infrastructure/ark_dal/responses_test.go
git commit -m "fix(ark): return safe tool errors to model"
```

### Task 3: Reject incomplete schedule calls before execution

**Files:**
- Modify: `internal/application/lark/schedule/func_call_tools.go`
- Modify: `internal/application/lark/schedule/func_call_tools_test.go`

- [ ] **Step 1: Write failing schedule argument tests**

Test `CreateSchedule.ParseTool` with these cases:

```go
incomplete := `{"name":"洛克王国货单提醒","type":"once"}`
_, err := CreateSchedule.ParseTool(incomplete)
feedback, ok := xerror.ToolFeedback(err)
// assert ok, feedback contains "message 或 tool_name", "run_at", and "先询问用户"
```

Also assert a valid once message and a valid cron tool schedule parse successfully, while providing both `message` and `tool_name` returns safe feedback.

- [ ] **Step 2: Run tests and verify RED**

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml /root/.go/go1.26.1/bin/go test -tags=custom_skip_vips -v ./internal/application/lark/schedule -run '^TestCreateScheduleParseTool' -count=1
```

Expected: incomplete calls currently parse without error.

- [ ] **Step 3: Implement minimal combination validation**

After enum parsing, trim action fields, collect missing fields based on task type, and return `xerror.WithToolFeedback` when incomplete. Reject simultaneous `message` and `tool_name` the same way. Extend the tool description with: `信息不足时不要调用；先向用户询问缺失的执行时间、消息内容或工具目标，禁止猜测或只传 name/type。`

- [ ] **Step 4: Run schedule tests and verify GREEN**

Run the command from Step 2, then `TestRegisterToolsInfersTypedEnums`. Expected: PASS.

- [ ] **Step 5: Commit schedule validation**

```bash
git add internal/application/lark/schedule/func_call_tools.go internal/application/lark/schedule/func_call_tools_test.go
git commit -m "fix(schedule): reject incomplete model tool calls"
```

### Task 4: Verification and integration

**Files:**
- Verify all files above

- [ ] **Step 1: Run focused tests with race detection**

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml /root/.go/go1.26.1/bin/go test -race -tags=custom_skip_vips -v ./pkg/xerror ./internal/infrastructure/ark_dal ./internal/application/lark/schedule -run '^(TestWithToolFeedback|TestToolFeedback|TestToolCallContinuationOutput|TestResponsesImplDefersToolContinuation|TestCreateScheduleParseTool|TestRegisterToolsInfersTypedEnums)' -count=1
```

- [ ] **Step 2: Run vet and build**

```bash
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml /root/.go/go1.26.1/bin/go vet -tags=custom_skip_vips ./pkg/xerror ./internal/application/lark/schedule ./cmd/larkrobot
BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml /root/.go/go1.26.1/bin/go build -tags=custom_skip_vips ./cmd/larkrobot
```

- [ ] **Step 3: Review, merge, and push**

Require zero confirmed Critical/Important review findings, fast-forward the clean branch into `master`, rerun focused tests on the merged result, and push `origin/master`.

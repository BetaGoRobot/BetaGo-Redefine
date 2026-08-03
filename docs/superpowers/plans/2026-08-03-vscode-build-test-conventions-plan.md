# VS Code Build and Test Conventions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Document and enforce the repository rule that Go compilation and testing use the parameters defined by the local VS Code workspace configuration.

**Architecture:** `README.md` exposes the baseline to developers, while root `AGENTS.md` turns the same baseline into mandatory agent behavior. Both files record the current effective values because `.vscode` is intentionally ignored by Git, while still requiring future local `.vscode` changes to be synchronized.

**Tech Stack:** Markdown, VS Code Go configuration, Go CLI

---

### Task 1: Document the developer baseline

**Files:**
- Modify: `README.md`

- [x] **Step 1: Add the build and test baseline**

Under `## 开发调试`, add a subsection that states:

```markdown
### 编译与测试基线

本地 Go 编译和测试参数以 `.vscode/launch.json` 与 `.vscode/settings.json` 为准。当前等价 CLI 参数为：

- 编译、运行和测试 Go 包时使用 `-tags=custom_skip_vips`。
- 测试设置 `BETAGO_CONFIG_PATH=.dev/config.toml` 并启用 `-v`。
- 不启用 `.vscode/settings.json` 中已注释的 `-gcflags=all=-N -l`。
```

Also explain that `go.mod` selects the Go version, ignored `.vscode` changes must be synchronized to README and `AGENTS.md`, and alternate worktrees must point `BETAGO_CONFIG_PATH` at the real main-workspace config without copying or committing it.

- [x] **Step 2: Add equivalent CLI examples**

Add these examples:

```bash
go build -tags=custom_skip_vips ./...
BETAGO_CONFIG_PATH=.dev/config.toml go test -v -tags=custom_skip_vips ./...
```

### Task 2: Enforce the baseline for agents

**Files:**
- Create: `AGENTS.md`

- [x] **Step 1: Add repository-wide agent instructions**

Create a concise root instruction file requiring agents to:

- inspect available `.vscode/launch.json` and `.vscode/settings.json` before Go compilation or testing;
- use `go.mod`'s Go version, `custom_skip_vips`, `.dev/config.toml`, and active VS Code test flags;
- avoid commented flags such as `-gcflags=all=-N -l`;
- resolve the main-workspace config from alternate worktrees without copying or committing secrets;
- report missing or conflicting configuration instead of silently changing parameters or claiming blocked tests passed;
- synchronize README and `AGENTS.md` whenever the effective local VS Code baseline changes.

### Task 3: Verify and commit

**Files:**
- Verify: `README.md`
- Verify: `AGENTS.md`
- Verify: `docs/superpowers/specs/2026-08-03-vscode-build-test-conventions-design.md`

- [x] **Step 1: Compare documented values with VS Code configuration**

Run:

```bash
rg -n "custom_skip_vips|BETAGO_CONFIG_PATH|gcflags|go.testFlags" \
  /mnt/RapidPool/workspace/BetaGo_v2/.vscode README.md AGENTS.md
```

Expected: active build tag, config path, and `-v` agree; `gcflags` is documented as disabled.

- [x] **Step 2: Check Markdown diff hygiene**

Run:

```bash
git diff --check
```

Expected: exit code 0 with no output.

- [x] **Step 3: Sign the documentation commit**

Run:

```bash
git add README.md AGENTS.md \
  docs/superpowers/plans/2026-08-03-vscode-build-test-conventions-plan.md
git commit -S -m "docs: standardize VS Code verification parameters"
git verify-commit HEAD
```

Expected: the commit succeeds and `git verify-commit` reports a good signature.

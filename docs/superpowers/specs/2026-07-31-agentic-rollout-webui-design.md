# Agentic Rollout WebUI Design

**Date:** 2026-07-31

**Status:** Approved

**Scope:** Per-bot, per-chat rollout controls for Conversation Runtime, callback continuation, parallel evaluation, and Agent Card; visual refresh of the existing chat list and chat detail pages.

## 1. Context

The conversation runtime, callback continuation, parallel evaluation, and
Agent Card capabilities already have gradual-rollout concepts, but their
control surfaces are split between TOML allowlists and generic dynamic config.
Operators cannot reliably answer:

- whether a capability is actually effective for one chat;
- whether the state came from a static baseline or a dynamic override;
- whether the required runtime infrastructure is ready;
- how to enable the same capability for a controlled batch of chats.

The existing WebUI has chat list and chat detail views, plus a generic config
tab. A plain boolean switch in that tab cannot represent an absent override,
an explicit false override, and the resulting effective state at the same
time.

## 2. Goals

- Provide one authoritative policy resolver shared by the runtime and WebUI.
- Support three per-chat states: inherit, explicitly enabled, explicitly
  disabled.
- Keep every mutation scoped to the WebUI server's bound bot identity.
- Support single-chat and single-bot batch rollout.
- Keep existing TOML policies, dynamic config keys, and generic Config APIs
  compatible.
- Require no manual PostgreSQL or OpenSearch initialization.
- Refresh only the chat list and chat detail visual design.

## 3. Non-goals

- Redesigning the Dashboard, top-level navigation, or BotPicker.
- Supporting one mutation across multiple bots.
- Editing TOML from the browser.
- Replacing the existing dynamic config store with a rollout-specific table.
- Adding user accounts or replacing the existing WebUI Bearer authentication.
- Turning shadow-mode Agent Card delivery into live delivery through a chat
  override.

## 4. Product Decisions

### 4.1 Entry points

The selected design integrates rollout controls into existing chat surfaces:

- Chat detail gains an **Agentic 灰度** tab.
- Chat list gains selection controls and a **批量灰度** action.
- The aggregate "all bots" list remains read-only for rollout operations.
- Batch controls become available only after the operator selects one bot.

This keeps low-frequency single-chat work close to chat context without
introducing a new top-level rollout console.

### 4.2 State language

`inherit` is a configuration source, not an effective runtime state. Every
control displays both:

- configured override: inherit, enabled, or disabled;
- effective state: enabled or disabled.

The default presentation is normally `继承（当前关闭）`. If a global or TOML
baseline is enabled, the same absent chat override is displayed as
`继承（当前开启）`.

Resetting a capability to inherit deletes its chat-scoped dynamic config row.
It must never persist `false` as a substitute for inherit.

### 4.3 Full Agentic preset

The UI provides a convenience preset while preserving independent controls:

- **Enable Full Agentic** expands to four explicit `enabled` mutations.
- **Restore Full Agentic to Inherit** expands to four `inherit` mutations.
- Operators may adjust individual capabilities before submitting.
- There is no hidden cross-capability mutation.
- If any requested capability is unavailable, preflight blocks the whole
  atomic submission and explains the unavailable capability.

### 4.4 Batch safety

- Batch selection is limited to chats belonging to one selected bot.
- The all-bots view has no selection checkboxes or batch write action.
- The batch drawer previews selected chat count, overrides created or removed,
  effective state changes, and validation failures.
- A single request may mutate at most 200 chats.

## 5. Visual Design

The chosen direction is **温润 Agent 运营台**:

- deep pine green for primary framing;
- warm off-white surfaces;
- restrained fluorescent green for primary rollout actions and active states;
- teal and neutral semantic colors for status;
- denser information hierarchy than a generic settings page without adopting
  a dark monitoring-console aesthetic.

The design is local to ChatList and ChatDetail. Existing charts, global filter
behavior, navigation, and BotPicker remain functionally unchanged.

### 5.1 Chat list

- A compact operating summary shows active chats, rollout chats, and health
  context using data already available to the page.
- Rows show chat identity, bot identity, existing activity metrics, and a
  concise rollout summary such as `评测开启`, `完整 Agentic`, or `4 项继承关闭`.
- Rollout summaries load asynchronously after the main chat list so policy
  lookup cannot block existing analytics.
- Selecting one bot reveals row checkboxes and the batch action.
- Selection remains explicit across filtering changes; hidden selections are
  counted in the batch drawer.

### 5.2 Chat detail

The new tab shows four capability cards:

- capability name and concise behavior description;
- effective enabled/disabled status;
- baseline value and source;
- current override;
- three-state control;
- readiness and unavailability reason;
- a dependency hint where useful, without silently changing another switch.

The page header repeats bot and chat identity before mutation controls.

### 5.3 Responsive behavior

- At desktop widths, capability cards use a two-column grid.
- At tablet widths, controls remain two-column only while content fits without
  truncating state labels.
- Below 768 px, capability cards stack, the batch drawer becomes full-width,
  and table rows become touch-friendly cards.
- Interactive targets have a minimum 44 px touch area.
- Effective state is expressed with text and icons, never color alone.

## 6. Policy Model

### 6.1 Public state

Each capability resolves to:

```json
{
  "key": "parallel_evaluation",
  "override": "inherit",
  "baseline": "disabled",
  "effective": "disabled",
  "source": "toml",
  "available": true,
  "reason": ""
}
```

Allowed values:

- `override`: `inherit`, `enabled`, `disabled`;
- `baseline`: `enabled`, `disabled`;
- `effective`: `enabled`, `disabled`;
- `source`: `default`, `toml`, `global_config`, `chat_override`;
- `available`: whether this deployment can honor an enabled state.

When a requested override is `enabled` but `available` is false, the resolver
does not report an effective enabled state and the mutation validator rejects
the write.

### 6.2 Capability baselines

#### Conversation Runtime

- Default baseline: disabled.
- A bot-scoped global dynamic config may change the baseline.
- Chat-scoped dynamic config has highest priority.

#### Callback continuation

- Default baseline: disabled.
- A bot-scoped global dynamic config may change the baseline.
- Chat-scoped dynamic config has highest priority.
- It remains independently controllable. Existing pending interactions may
  still need callback continuation even when creation of new runtime
  interactions is disabled.

#### Parallel evaluation

- TOML baseline comes from `evaluation_mode` and `evaluation_chat_ids`.
- A bot-scoped global dynamic override takes precedence over TOML.
- A chat-scoped dynamic override takes precedence over both.
- When effective state first becomes enabled, the existing tenant-aware
  OpenSearch provisioner lazily ensures the evaluation index.

#### Agent Card

- TOML baseline comes from `[agent_card].enabled`, `mode`, and
  `allow_chat_ids`.
- Add `agent_card_enabled` as a dynamic boolean config key.
- A bot-scoped global dynamic override takes precedence over the TOML sending
  decision.
- A chat-scoped dynamic override has highest priority.
- Live delivery is available only when Agent Card infrastructure has been
  initialized and the static mode permits live delivery.
- `off` and `shadow` modes cannot be promoted to live delivery by WebUI.
  Their controls show unavailable with a specific reason.
- `allowlist` with an empty static allowlist is the recommended ready-but-off
  baseline for WebUI-managed rollout.

### 6.3 Shared resolver

Introduce an application-level Agentic Rollout service. It owns:

- capability definitions and labels;
- baseline providers;
- dynamic override resolution;
- effective-state calculation;
- readiness checks;
- revision calculation;
- validation and mutation planning.

Message handling and callback runtime gates consume this service rather than
reimplementing priority rules. The WebUI receives the same service through its
dependency options. The runtime and UI therefore cannot disagree about an
effective state.

## 7. API Design

All routes remain under the existing WebUI server and middleware.

### 7.1 Read one chat

```http
GET /api/chats/{chatID}/agentic-rollout
```

Response:

```json
{
  "bot": {
    "id": "bot_45b24867",
    "name": "Production Assistant"
  },
  "chat_id": "oc_example",
  "revision": "sha256:...",
  "capabilities": [
    {
      "key": "conversation_runtime",
      "label": "Conversation Runtime",
      "override": "inherit",
      "baseline": false,
      "effective": false,
      "source": "default",
      "available": true,
      "reason": ""
    }
  ]
}
```

The returned bot identity is informational. It is not accepted as mutation
input.

### 7.2 Read multiple chats

```http
GET /api/agentic-rollouts?chat_ids=oc_1,oc_2
```

- The client chunks reads to at most 100 chat IDs per request.
- Empty, duplicate, and malformed IDs are rejected or normalized before
  resolution.
- Results preserve the requested chat order.

### 7.3 Mutate one chat

```http
PUT /api/chats/{chatID}/agentic-rollout
Content-Type: application/json
Authorization: Bearer ...
```

```json
{
  "expected_revision": "sha256:...",
  "changes": {
    "conversation_runtime": "enabled",
    "callback_continuation": "inherit"
  }
}
```

Only listed capabilities change. A stale revision returns `409 Conflict`
without mutation.

### 7.4 Preview or mutate a batch

```http
POST /api/agentic-rollouts/batch
Content-Type: application/json
Authorization: Bearer ...
```

```json
{
  "dry_run": true,
  "chat_ids": ["oc_1", "oc_2"],
  "expected_revisions": {
    "oc_1": "sha256:...",
    "oc_2": "sha256:..."
  },
  "changes": {
    "conversation_runtime": "enabled",
    "callback_continuation": "enabled",
    "parallel_evaluation": "enabled",
    "agent_card": "enabled"
  }
}
```

The preview and commit responses contain per-chat before/after states plus
aggregate counts. Validation is all-or-nothing.

### 7.5 Error contract

- `400`: invalid state, unknown capability, empty target set, or limit
  violation;
- `401`: missing or invalid Bearer token when auth is configured;
- `409`: one or more stale revisions;
- `422`: a requested enabled state is unavailable in this deployment;
- `503`: config store or policy dependency unavailable;
- `500`: unexpected persistence failure.

Errors return a stable machine code and a human-readable message. Batch errors
identify affected chat IDs without leaking another bot's resources.

## 8. Persistence and Consistency

### 8.1 Storage

Continue using `dynamic_configs`. No rollout table or migration is needed.
Namespaced keys already include `app_id` and `bot_open_id`, followed by scope,
chat ID, and config key.

The four dynamic keys are:

- `conversation_runtime_enabled`;
- `conversation_callback_continuation_enabled`;
- `conversation_parallel_evaluation_enabled`;
- `agent_card_enabled` (new).

### 8.2 Atomic mutation API

Extend Config Manager with a validated batch mutation method:

- serialize Config Manager writers with a dedicated write mutex;
- acquire the writer lock before checking expected revisions;
- apply upserts and deletes in one GORM/PostgreSQL transaction;
- update the in-memory and trace caches only after commit;
- leave caches unchanged after rollback;
- route existing single-key Set/Delete operations through the same serialized
  writer path.

This provides atomicity for all writes made through Config Manager, including
the existing generic Config API.

### 8.3 Revision

The revision is a stable hash over:

- bot namespace;
- chat ID;
- ordered explicit chat overrides;
- ordered resolved baseline values and sources;
- readiness state.

Static TOML is immutable for the process lifetime. Global dynamic config writes
are serialized by the same manager writer path. Revision comparison occurs
inside that serialization boundary.

## 9. Multi-tenancy and Security

- One WebUI server is bound to one bot identity and one Config Manager
  namespace.
- Mutation bodies accept chat IDs only. They never accept `tenant_id`,
  `app_id`, `bot_open_id`, or `bot_id`.
- The frontend creates requests through the selected bot's existing `BotApi`
  base URL.
- The all-bots aggregate UI cannot submit a rollout mutation.
- Dynamic config persistence remains namespaced by App ID and Bot Open ID.
- Evaluation OpenSearch resources continue using the canonical tenant owner
  and per-tenant index naming.
- Existing WebUI auth behavior is preserved: GET and OPTIONS are public to the
  configured network; mutations require Bearer auth only when
  `webui_config.auth_token` is non-empty.
- CORS behavior is unchanged.

## 10. Generic Config Compatibility

- Existing Config API routes remain supported.
- Existing rollout config values remain valid and are immediately visible in
  the dedicated rollout surface.
- Config definitions gain optional presentation metadata so rollout-owned keys
  can be hidden from the generic config tab without being removed from the
  Config API.
- Unknown legacy keys and unrelated config UI behavior are unchanged.

## 11. Frontend Architecture

### 11.1 API and types

Extend `BotApi` with:

- `getAgenticRollout(chatID)`;
- `getAgenticRollouts(chatIDs)`;
- `updateAgenticRollout(chatID, request)`;
- `batchAgenticRollout(request)`.

Add explicit TypeScript unions for capability keys, override states, effective
states, and sources. Do not use untyped boolean maps for three-state data.

### 11.2 Components

Create reusable components scoped to chat views:

- `AgenticStatusBadge`;
- `AgenticCapabilityCard`;
- `AgenticRolloutPanel`;
- `AgenticBatchDrawer`;
- `ChatOpsSummary`.

ChatDetail owns single-chat loading and mutation. ChatList owns row selection,
chunked status loading, and the batch drawer.

### 11.3 Mutation UX

- Controls edit a local draft before submission.
- Dirty state is visually explicit.
- Single-chat submission uses the last loaded revision.
- Batch submission always performs server dry-run immediately before commit.
- On `409`, reload state and preserve the operator's intended draft for
  comparison.
- Successful commits reload authoritative server state rather than assuming
  the local draft is effective.
- Network and readiness failures use actionable text, not generic "save
  failed" messages.

## 12. Deployment

No manual SQL is required.

Recommended Agent Card baseline:

```toml
[agent_card]
enabled = true
mode = "allowlist"
allow_chat_ids = []
```

Recommended evaluation baseline:

```toml
[runtime_config]
evaluation_mode = "allowlist"
evaluation_chat_ids = []
```

Runtime and callback continuation default to disabled when no global or chat
override exists. With these baselines, every chat initially displays
`继承（关闭）`, and operators can selectively enable chats in WebUI.

The evaluation tenant index is created automatically on the first effective
evaluation enable. Existing PostgreSQL schema bootstrap remains automatic.

The shipped deployment example uses these ready-but-empty allowlists. This is
intentional: `allowlist + []` initializes each optional capability without
enabling any chat. The WebUI then writes an explicit tenant-scoped chat
override. No tenant identity is accepted from the browser request.

## 12.1 Implemented compatibility contract

- Existing TOML allowlists remain baselines and continue to work.
- Existing global dynamic values remain higher-priority baselines.
- Missing chat overrides remain `inherit`; they do not create database rows.
- Restoring `inherit` deletes the chat override instead of persisting a third
  sentinel value.
- The generic Config API still returns all keys, now with
  `management_surface="agentic_rollout"` so the refreshed WebUI can avoid
  rendering duplicate controls.
- Rollout reads remain unauthenticated for compatibility with existing WebUI
  reads. PUT/POST use the existing Bearer middleware when
  `webui_config.auth_token` is configured.

## 13. Observability

Add metrics for:

- rollout read requests and resolved capability counts by effective state;
- preview and commit request counts;
- mutated chat and key counts;
- revision conflicts;
- unavailable capability rejections;
- persistence failures and transaction latency.

Logs include request ID, bot namespace hash, chat count, capability names,
before/after states, and error codes. They exclude auth tokens and full card or
message content.

## 14. Testing

### 14.1 Go

- Resolver table tests for every source priority and explicit false.
- Agent Card readiness tests for off, shadow, allowlist, and on modes.
- Runtime integration tests proving all four gates use the shared resolver.
- Config Manager transaction tests for all-or-nothing changes, rollback cache
  safety, delete-as-inherit, and writer serialization.
- API handler tests for authentication, tenant-bound requests, request limits,
  dry-run parity, revision conflicts, and stable errors.
- Multi-tenant tests proving identical chat IDs in two bot namespaces do not
  share state.
- Evaluation tests proving first effective enable invokes tenant-aware lazy
  provisioning.

### 14.2 WebUI

- Type-level build and production bundle validation.
- Component tests for three-state rendering, unavailable state, and dirty
  drafts if the existing frontend test stack supports them.
- Interaction tests for all-bots read-only behavior, single-bot selection,
  dry-run preview, conflict recovery, and reset-to-inherit.
- Responsive inspection at desktop, tablet, and mobile widths.

### 14.3 Baseline limitation

Repository verification must use the project-defined environment rather than
the host's default Go command:

```bash
BETAGO_CONFIG_PATH=/absolute/path/to/.dev/config.toml \
  /root/.go/go1.26.1/bin/go test -count=1 \
  -tags=custom_skip_vips -gcflags='all=-N -l' ./...
```

The host default is Go 1.27rc2, while this repository declares Go 1.26 and
`mockey v1.4.6` supplies runtime offsets only through Go 1.26. The
`custom_skip_vips` tag is also required by `.vscode/launch.json` and
`Agents.md` on hosts without the libvips development packages.

With the correct toolchain and flags, the baseline compiles. Two unrelated
tests currently fail because they read the shared development Redis with fixed
keys rather than isolated fakes:

- `internal/infrastructure/geocode.TestCachedFallsBackAndCaches`;
- `internal/infrastructure/mcpstore.TestSessionStoreSeen`.

Previously persisted values make their "initially absent" assertions false.
Implementation verification must record these pre-existing isolation failures,
run directly affected packages with the correct flags, and run
`npm run build`. New rollout tests must use explicit injected fakes and must
not depend on shared development Redis.

## 15. Rollout Sequence

1. Land the shared policy model and read-only resolver without changing
   effective behavior.
2. Switch runtime gates to the resolver and verify parity.
3. Add atomic Config Manager mutations and WebUI API handlers.
4. Add the dedicated ChatDetail rollout surface.
5. Add single-bot ChatList summaries and batch rollout.
6. Apply the approved visual refresh to ChatList and ChatDetail.
7. Deploy with empty static allowlists.
8. Enable one internal chat, verify effective state and runtime metrics, then
   expand through WebUI batches.

## 16. Decision Summary

- Dedicated rollout façade over the existing dynamic config store.
- Three states with inherit as the default.
- Independent capabilities plus an explicit Full Agentic preset.
- Single-bot atomic batch operations.
- No cross-bot mutation from aggregate views.
- Existing TOML and Config APIs remain compatible.
- No manual database or index initialization.
- Warm Agent operations visual direction, limited to chat pages.

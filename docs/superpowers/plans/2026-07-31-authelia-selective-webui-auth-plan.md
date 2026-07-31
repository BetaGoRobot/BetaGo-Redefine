# Authelia Selective WebUI Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep BetaGo operational reads public while requiring an Authelia `one_factor` session for all writes and sensitive reads, without exposing Bot credentials to the browser.

**Architecture:** Traefik uses a high-priority protected router and a low-priority public router. In secure mode the WebUI container splits runtime Bot configuration into a public allowlisted projection and a private Caddy upstream configuration; Caddy injects Bot bearer credentials only server-side and rejects protected traffic without a trusted Authelia identity header. The Vue application exposes an explicit read-only/management mode with popup login and never stores or sends Bot tokens.

**Tech Stack:** Traefik Docker labels, Authelia ForwardAuth, Caddy 2, POSIX shell + jq, Go `net/http`, Vue 3, Pinia, Axios, Vitest.

---

### Task 1: Secure runtime projection and private Caddy credentials

**Files:**
- Create: `script/webui/docker-entrypoint_test.sh`
- Modify: `script/webui/docker-entrypoint.sh`
- Modify: `script/webui/Caddyfile`
- Modify: `script/webui/Dockerfile`
- Modify: `webui/src/env.d.ts`

- [ ] **Step 1: Write the failing shell regression test**

The test starts the entrypoint with sentinel values:

```sh
WEBUI_AUTH_MODE=authelia \
BACKEND_AUTH_TOKEN=default-secret-sentinel \
VITE_BOTS='[{"id":"bot-one","name":"Bot One","baseURL":"http://bot-one:8090","token":"bot-secret-sentinel","api_key":"api-key-sentinel","unknown":"unknown-sentinel"}]' \
CONFIG_DIR="$tmp/public" \
BOTS_CADDY_FILE="$tmp/private/bots.caddy" \
AUTH_GATE_CADDY_FILE="$tmp/private/auth-gate.caddy" \
sh script/webui/docker-entrypoint.sh true
```

It asserts that `config.js` contains only public Bot metadata and
`authMode:"authelia"`, that none of the five sentinel private values occur in
`config.js`, and that the private Caddy file contains the upstream and bearer
header.

- [ ] **Step 2: Run the shell test and verify RED**

Run:

```bash
sh script/webui/docker-entrypoint_test.sh
```

Expected: FAIL because secure projection and `AUTH_GATE_CADDY_FILE` do not
exist.

- [ ] **Step 3: Implement secure and legacy projections**

Add an allowlist projection in secure mode:

```sh
PUBLIC_BOTS_JSON=$(printf '%s' "$VITE_BOTS_VAL" | jq -c '
  try fromjson? // .
  | if type == "array" then . else [] end
  | map(select(type == "object" and (.id | type == "string"))
      | {id, name: (.name // .id), remark, color}
      | with_entries(select(.value != null)))
')
```

Render `authMode`, `sessionPath`, and `loginPath` into `config.js`. Generate
private Caddy routes from base64-encoded jq objects so tabs, newlines, and
quotes cannot alter Caddy structure. Validate Bot IDs against
`^[A-Za-z0-9_-]+$` and quote upstream/header values through `jq -Rs .`.

Generate `auth-gate.caddy` only in secure mode with unauthenticated matchers for
write methods, sensitive GET paths, `/auth/session`, and `/auth/login`. Add
Caddy handlers that return no-store session JSON and a minimal opener-notifying
login bridge page.

Legacy mode must preserve the current `VITE_BOTS` browser projection and
browser-supplied bearer behavior.

- [ ] **Step 4: Verify GREEN and validate the container configuration**

Run:

```bash
sh script/webui/docker-entrypoint_test.sh
docker build -f script/webui/Dockerfile --target runner -t betago-webui-auth-test .
docker run --rm --entrypoint caddy betago-webui-auth-test \
  validate --config /etc/caddy/Caddyfile --adapter caddyfile
```

Expected: shell assertions pass and Caddy reports a valid configuration.

- [ ] **Step 5: Commit**

```bash
git add script/webui webui/src/env.d.ts
git commit -m "feat: keep webui bot credentials server-side"
```

### Task 2: Traefik selective ForwardAuth deployment

**Files:**
- Create: `script/webui/traefik_auth_rules_test.sh`
- Modify: `deploy/docker-compose.yaml`
- Modify: `deploy/.env.example`
- Modify: `deploy/README.md`
- Modify: `webui/README.md`

- [ ] **Step 1: Write the failing deployment contract test**

The shell test renders Compose with a temporary `.env`, then asserts:

```sh
docker compose -f deploy/docker-compose.yaml config >"$rendered"
```

The rendered model must contain:

- the existing public router with priority `10`;
- a protected router with priority `100`;
- `Method(PUT|POST|PATCH|DELETE)` coverage;
- all sensitive path families from the design;
- the configured `authelia@docker` middleware;
- a header-stripping middleware before ForwardAuth;
- no public host port for the Bot backend.

- [ ] **Step 2: Run the deployment test and verify RED**

Run:

```bash
sh script/webui/traefik_auth_rules_test.sh
```

Expected: FAIL because the protected router is absent.

- [ ] **Step 3: Add public/protected routers and secure-mode environment**

Keep router `betago-webui` public and add
`betago-webui-protected` with an explicit higher priority. The protected rule
matches API writes, sensitive GET paths, and the two auth bridge paths. Both
routers first strip caller-supplied `Remote-User`, `Remote-Groups`,
`Remote-Email`, and `Remote-Name`; the protected router then invokes
`${AUTHELIA_MIDDLEWARE:-authelia@docker}`.

Set:

```yaml
WEBUI_AUTH_MODE: "${WEBUI_AUTH_MODE:-legacy}"
BACKEND_AUTH_TOKEN: "${BOT_WEBUI_AUTH_TOKEN:-}"
```

Remove the default published `8090` mapping. Document a loopback-only local
debug override and the Authelia `one_factor` domain rule.

- [ ] **Step 4: Verify GREEN**

Run:

```bash
sh script/webui/traefik_auth_rules_test.sh
docker compose -f deploy/docker-compose.yaml config --quiet
```

Expected: all assertions pass and Compose validates.

- [ ] **Step 5: Commit**

```bash
git add deploy script/webui/traefik_auth_rules_test.sh webui/README.md
git commit -m "feat: gate webui management routes with authelia"
```

### Task 3: Backend defense-in-depth for sensitive reads

**Files:**
- Modify: `internal/interfaces/webui/server.go`
- Modify: `internal/interfaces/webui/server_test.go`
- Modify: `internal/interfaces/webui/handlers_evaluations.go`

- [ ] **Step 1: Write failing route-classification tests**

Add a table covering:

```go
tests := []struct {
    method, path string
    protected    bool
}{
    {http.MethodGet, "/api/health", false},
    {http.MethodGet, "/api/chats", false},
    {http.MethodGet, "/api/chats/oc_1/stats", false},
    {http.MethodGet, "/api/chats/oc_1/members", true},
    {http.MethodGet, "/api/chats/oc_1/configs", true},
    {http.MethodGet, "/api/chats/oc_1/features", true},
    {http.MethodGet, "/api/chats/oc_1/agentic-rollout", true},
    {http.MethodGet, "/api/chats/oc_1/insights/top_senders", true},
    {http.MethodGet, "/api/chats/oc_1/insights/top_mentions", true},
    {http.MethodGet, "/api/agentic-rollouts", true},
    {http.MethodGet, "/api/evaluations/episode-1", true},
    {http.MethodPut, "/api/chats/oc_1/configs/key", true},
    {http.MethodOptions, "/api/chats/oc_1/configs/key", false},
}
```

For each protected path, assert missing/invalid bearer returns `401` when a
token is configured and valid bearer reaches the handler.

- [ ] **Step 2: Run the focused Go test and verify RED**

Run:

```bash
go test ./internal/interfaces/webui -run 'TestWebUIAuthRouteClassification|TestSensitiveRoutesRequireBearer' -count=1
```

Expected: FAIL because GET routes other than evaluations bypass middleware
authentication.

- [ ] **Step 3: Implement one route classifier**

Add:

```go
func requiresWebUIAuth(method, path string) bool
```

It returns false for `GET` public paths and `OPTIONS`, true for every non-GET
method, and true for the explicit sensitive GET path families. `withAuth`
consults this classifier. Preserve legacy empty-token behavior except the
evaluation handlers, which continue to fail closed when no token exists.

- [ ] **Step 4: Verify GREEN and the package suite**

Run:

```bash
go test ./internal/interfaces/webui -count=1
```

Expected: package passes.

- [ ] **Step 5: Commit**

```bash
git add internal/interfaces/webui
git commit -m "fix: authenticate sensitive webui reads"
```

### Task 4: Frontend auth session and credential-free clients

**Files:**
- Create: `webui/src/auth/runtime.ts`
- Create: `webui/src/auth/session.ts`
- Create: `webui/src/auth/runtime.test.ts`
- Create: `webui/src/auth/session.test.ts`
- Modify: `webui/src/api/client.ts`
- Modify: `webui/src/api/agentic.test.ts`
- Modify: `webui/src/stores/filter.ts`
- Modify: `webui/src/components/BotPicker.vue`

- [ ] **Step 1: Write failing secure-mode unit tests**

Tests set:

```ts
window.__BETAGO_CONFIG__ = {
  authMode: 'authelia',
  sessionPath: '/auth/session',
  loginPath: '/auth/login',
}
```

They assert:

- `createBotClient({ id: 'one', token: 'sentinel' })` does not add an
  `Authorization` header;
- persisted Bot objects are projected without `token`;
- the auth session starts locked, becomes unlocked after a 200 JSON probe, and
  returns to locked on the `betago:auth-required` event;
- a popup login never replays a pending write.

- [ ] **Step 2: Run focused Vitest and verify RED**

Run:

```bash
cd webui
npx vitest run src/auth src/api/agentic.test.ts
```

Expected: FAIL because auth modules do not exist and the client still sends
browser tokens.

- [ ] **Step 3: Implement runtime/session modules**

`runtime.ts` exposes pure readers for auth mode and configured paths.
`session.ts` owns singleton refs `authenticated`, `checking`, and `loginBusy`;
uses `fetch(..., {credentials:"include", cache:"no-store",
redirect:"manual"})`; opens the login bridge from a direct user click; polls
with a bounded interval; listens for the login bridge `postMessage`; and never
stores credentials.

The Axios response interceptor dispatches `betago:auth-required` on secure-mode
`401/403`. The request interceptor sends Bot bearer tokens only in legacy mode.
The filter store strips tokens before loading and persisting secure-mode Bot
objects. BotPicker hides token controls in secure mode.

- [ ] **Step 4: Verify GREEN**

Run:

```bash
cd webui
npx vitest run src/auth src/api/agentic.test.ts
```

Expected: focused tests pass.

- [ ] **Step 5: Commit**

```bash
git add webui/src/auth webui/src/api webui/src/stores/filter.ts webui/src/components/BotPicker.vue
git commit -m "feat: add credential-free webui management sessions"
```

### Task 5: Explicit read-only and management-mode UI

**Files:**
- Create: `webui/src/components/ManagementGate.vue`
- Create: `webui/src/components/management-gate.test.ts`
- Modify: `webui/src/App.vue`
- Modify: `webui/src/views/ChatDetail.vue`
- Modify: `webui/src/views/chat-detail-agentic.test.ts`
- Modify: `webui/src/styles/theme.css`

- [ ] **Step 1: Write failing UI behavior tests**

Mount `ManagementGate` with the session singleton locked and assert it renders
`登录后管理`, does not mount its default slot, and calls `beginLogin` on the
button. Set the singleton authenticated and assert the slot mounts.

Add a ChatDetail contract test proving `initAll` loads only public endpoints in
secure mode and that members, feature/config state, rollout state, top senders,
and top mentions are loaded only after management mode unlocks.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```bash
cd webui
npx vitest run src/components/management-gate.test.ts src/views/chat-detail-agentic.test.ts
```

Expected: FAIL because the gate and management-mode behavior are absent.

- [ ] **Step 3: Implement management-mode UX**

Add a compact status control to the app header:

```text
只读模式 · 登录管理
管理模式
```

Wrap member, feature, Agentic rollout, and configuration panes with
`ManagementGate`. Do not mount the protected components or call protected APIs
while locked. In the statistics pane, replace top-sender and top-mention cards
with the same gate while leaving public charts visible.

Watch authentication and active tab. On unlock, load only the protected data
needed by the current view; on session expiry, preserve `drafts` and hide
protected response data. Failed writes remain uncommitted and are never
automatically replayed.

- [ ] **Step 4: Verify GREEN and responsive rendering**

Run:

```bash
cd webui
npx vitest run src/components/management-gate.test.ts src/views/chat-detail-agentic.test.ts
npm run build
```

Expected: tests and TypeScript/Vite build pass.

- [ ] **Step 5: Commit**

```bash
git add webui/src/App.vue webui/src/components webui/src/views webui/src/styles/theme.css
git commit -m "feat: add webui read-only management mode"
```

### Task 6: Security audit and end-to-end verification

**Files:**
- Modify: `docs/operations/agentic-rollout-webui.md`
- Modify: `docs/superpowers/specs/2026-07-31-authelia-selective-webui-auth-design.md`

- [ ] **Step 1: Run secret-leak regression checks**

Use unique sentinels and assert they are absent from the public runtime output,
frontend build, HTML, JavaScript, and repository diff:

```bash
sh script/webui/docker-entrypoint_test.sh
rg -n 'bot-secret-sentinel|api-key-sentinel|default-secret-sentinel' webui/dist /tmp/betago-auth-public
```

Expected: `rg` returns no matches for public artifacts.

- [ ] **Step 2: Run all automated verification**

```bash
go test ./internal/interfaces/webui ./cmd/larkrobot -count=1
cd webui && npm test && npm run build
cd .. && sh script/webui/docker-entrypoint_test.sh
sh script/webui/traefik_auth_rules_test.sh
git diff --check
```

Expected: every command exits zero.

- [ ] **Step 3: Run browser verification**

In secure-mode local fixtures verify:

- public dashboard and chat analytics render anonymously;
- management status is `只读模式`;
- protected tabs do not issue their API requests;
- login popup preserves the current route and closes on bridge notification;
- authenticated mode loads protected data;
- dispatching `betago:auth-required` locks management mode without deleting a
  configuration draft;
- desktop and 390px mobile pages have no horizontal overflow or browser errors.

- [ ] **Step 4: Update operations documentation**

Document the exact Compose variables, Authelia `one_factor` domain policy,
middleware name, secure Bot credential format, login behavior, legacy fallback,
and rollback procedure. Explicitly state that `/config.js` must never contain a
token in Authelia mode.

- [ ] **Step 5: Commit**

```bash
git add docs script/webui webui internal/interfaces/webui deploy
git commit -m "docs: operate selective authelia webui auth"
```

### Task 7: Completion audit

**Files:**
- Inspect all files changed since `2c49361`.

- [ ] **Step 1: Map every design requirement to evidence**

Create an inline checklist covering public reads, protected writes, sensitive
GETs, popup UX, session expiry, server-only credentials, legacy compatibility,
multi-Bot isolation, backend defense in depth, and deployment defaults.

- [ ] **Step 2: Verify committed scope**

```bash
git status --short
git log --oneline 2c49361..HEAD
git diff --stat 2c49361..HEAD
git diff --check 2c49361..HEAD
```

Expected: only the pre-existing untracked `.superpowers/` remains; all feature
changes are committed.

- [ ] **Step 3: Run the final verification command once more**

```bash
go test ./internal/interfaces/webui ./cmd/larkrobot -count=1 &&
cd webui &&
npm test &&
npm run build
```

Expected: zero failures and a successful production build.

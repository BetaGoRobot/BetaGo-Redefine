# Luckin MCP Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a reusable MCP Streamable HTTP client and expose Luckin Coffee MCP tools to the Lark robot with credential scoping and confirmation-gated order creation.

**Architecture:** Implement a protocol-only `mcpclient`, a Lark-facing `mcpbridge`, and a Luckin policy package. The LLM can call read/preview tools directly, while `createOrder` is replaced with a pending-order card flow that only the original requester can confirm.

**Tech Stack:** Go 1.26, standard `net/http`, JSON-RPC over MCP Streamable HTTP, existing `xcommand` tool framework, existing cardaction registry, GORM Gen models generated from SQL.

---

## Reference Context

- Design spec: `docs/superpowers/specs/2026-06-15-luckin-mcp-integration-design.md`
- DB change SOP: `script/AGENT_DB_CHANGE_SOP.md`
- Tool registration pattern: `internal/application/lark/handlers/tools.go`
- Typed tool pattern: `internal/application/lark/handlers/finance_data_handler.go`
- Card action registry: `internal/application/lark/cardaction/registry.go`
- Built-in card action registration: `internal/application/lark/cardaction/builtin.go`
- MCP official concepts: MCP data layer uses JSON-RPC, and tools are exposed through tool metadata and invocation methods.

## File Structure

Create these packages:

- `internal/infrastructure/mcpclient`: protocol client, errors, fake test server helpers.
- `internal/application/lark/mcpbridge`: generic bridge from allowlisted MCP tools to `xcommand` handlers.
- `internal/application/lark/luckin`: Luckin provider policy, tool specs, credential resolution, pending orders, cards, card actions.
- `internal/infrastructure/mcpstore`: DB repositories for encrypted MCP credentials and pending Luckin orders.

Modify these existing files:

- `internal/application/lark/handlers/tools.go`: register Luckin tools in regular/runtime capability tool sets, not schedulable tools.
- `internal/application/lark/cardaction/builtin.go`: register Luckin confirm/cancel card actions.
- `pkg/cardaction/action.go`: add Luckin action constants and field constants.
- `cmd/generate/gorm-gen.go`: only if generated field types need explicit overrides after SQL generation.

Create DB SQL first:

- `script/sql/20260615_luckin_mcp_tables.sql`

Generated files after SQL/codegen:

- `internal/infrastructure/db/model/mcp_credentials.gen.go`
- `internal/infrastructure/db/model/luckin_pending_orders.gen.go`
- `internal/infrastructure/db/query/mcp_credentials.gen.go`
- `internal/infrastructure/db/query/luckin_pending_orders.gen.go`
- Updates to `internal/infrastructure/db/query/gen.go`

## Execution Gate

This feature changes the database schema. Per `script/AGENT_DB_CHANGE_SOP.md`, Task 1 must stop after writing SQL. The engineer must ask the user to execute the SQL and run `go run ./cmd/generate` before Tasks 2 and later touch repositories or generated DB models.

## Task 1: Database Schema SQL

**Files:**
- Create: `script/sql/20260615_luckin_mcp_tables.sql`

- [ ] **Step 1: Write the SQL file**

Create `script/sql/20260615_luckin_mcp_tables.sql` with:

```sql
create schema if not exists betago;

create table if not exists betago.mcp_credentials (
    id bigserial primary key,
    provider text not null,
    app_id text not null default '',
    bot_open_id text not null default '',
    scope_type text not null,
    scope_id text not null,
    encrypted_token text not null,
    token_hint text not null default '',
    created_by_open_id text not null default '',
    updated_by_open_id text not null default '',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    deleted_at timestamptz,
    constraint mcp_credentials_scope_type_chk check (scope_type in ('personal', 'chat')),
    constraint mcp_credentials_provider_scope_unique unique (provider, app_id, bot_open_id, scope_type, scope_id)
);

create index if not exists idx_mcp_credentials_scope
    on betago.mcp_credentials (provider, app_id, bot_open_id, scope_type, scope_id)
    where deleted_at is null;

create table if not exists betago.luckin_pending_orders (
    id text primary key,
    app_id text not null default '',
    bot_open_id text not null default '',
    chat_id text not null default '',
    requester_open_id text not null default '',
    credential_scope_type text not null,
    credential_scope_id text not null default '',
    mcp_server_name text not null default 'my-coffee',
    create_order_payload jsonb not null,
    payload_hash text not null,
    preview_result jsonb not null default '{}'::jsonb,
    status text not null default 'pending',
    result_json jsonb not null default '{}'::jsonb,
    error_text text not null default '',
    expires_at timestamptz not null,
    confirmed_by_open_id text not null default '',
    confirmed_at timestamptz,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint luckin_pending_orders_status_chk check (status in ('pending', 'confirmed', 'expired', 'cancelled', 'failed'))
);

create index if not exists idx_luckin_pending_orders_requester
    on betago.luckin_pending_orders (requester_open_id, created_at desc);

create index if not exists idx_luckin_pending_orders_status_expires
    on betago.luckin_pending_orders (status, expires_at);
```

- [ ] **Step 2: Verify SQL is present**

Run:

```bash
rg -n "mcp_credentials|luckin_pending_orders" script/sql/20260615_luckin_mcp_tables.sql
```

Expected: matches for both table names and index names.

- [ ] **Step 3: Commit SQL only**

Run:

```bash
git add script/sql/20260615_luckin_mcp_tables.sql
git -c commit.gpgsign=false commit -m "db: add mcp credential and luckin order tables"
```

Expected: one commit containing only the SQL file.

- [ ] **Step 4: Stop for DB generation**

Tell the user exactly:

```text
SQL 已保存到 script/sql/20260615_luckin_mcp_tables.sql。请先执行该 SQL，再运行 go run ./cmd/generate。完成后告诉我，我再继续改代码。
```

Do not continue to Task 2 until the generated files exist and include `McpCredential` and `LuckinPendingOrder`.

## Task 2: MCP Client Core

**Files:**
- Create: `internal/infrastructure/mcpclient/types.go`
- Create: `internal/infrastructure/mcpclient/errors.go`
- Create: `internal/infrastructure/mcpclient/client.go`
- Create: `internal/infrastructure/mcpclient/client_test.go`

- [ ] **Step 1: Write failing tests for tool list, tool call, and error normalization**

Create `internal/infrastructure/mcpclient/client_test.go`:

```go
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientListToolsAndCallTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q", got)
		}
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "tools/list":
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: json.RawMessage(`{"tools":[{"name":"queryShopList","description":"shops","inputSchema":{"type":"object"}}]}`),
			})
		case "tools/call":
			_ = json.NewEncoder(w).Encode(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":true}"}]}`),
			})
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	client := New(ClientOptions{HTTPClient: server.Client()})
	cfg := ServerConfig{
		Name:    "my-coffee",
		URL:     server.URL,
		Headers: map[string]string{"Authorization": "Bearer token-1"},
		Timeout: time.Second,
	}

	tools, err := client.ListTools(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ListTools error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "queryShopList" {
		t.Fatalf("tools = %+v", tools)
	}

	res, err := client.CallTool(context.Background(), CallRequest{
		Server:    cfg,
		ToolName:  "queryShopList",
		Arguments: json.RawMessage(`{"longitude":118.1,"latitude":24.1}`),
	})
	if err != nil {
		t.Fatalf("CallTool error = %v", err)
	}
	if string(res.Content) == "" || !json.Valid(res.Raw) {
		t.Fatalf("invalid result: %+v", res)
	}
}

func TestClientNormalizesUnauthorizedAndRemoteErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
	}))
	defer server.Close()

	client := New(ClientOptions{HTTPClient: server.Client()})
	_, err := client.ListTools(context.Background(), ServerConfig{Name: "my-coffee", URL: server.URL})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./internal/infrastructure/mcpclient
```

Expected: failure because `mcpclient` types and client functions are not defined.

- [ ] **Step 3: Implement types and errors**

Create `internal/infrastructure/mcpclient/types.go`:

```go
package mcpclient

import (
	"encoding/json"
	"net/http"
	"time"
)

type ServerConfig struct {
	Name    string
	URL     string
	Headers map[string]string
	Timeout time.Duration
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type CallRequest struct {
	Server    ServerConfig
	ToolName  string
	Arguments json.RawMessage
}

type CallResult struct {
	Content json.RawMessage
	Raw     json.RawMessage
}

type ClientOptions struct {
	HTTPClient *http.Client
}

type Client struct {
	http *http.Client
}

func New(opts ClientOptions) *Client {
	hc := opts.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{http: hc}
}
```

Create `internal/infrastructure/mcpclient/errors.go`:

```go
package mcpclient

import "errors"

var (
	ErrUnauthorized     = errors.New("mcp unauthorized")
	ErrToolNotFound     = errors.New("mcp tool not found")
	ErrInvalidArguments = errors.New("mcp invalid arguments")
	ErrRemote           = errors.New("mcp remote error")
	ErrTimeout          = errors.New("mcp timeout")
	ErrProtocol         = errors.New("mcp protocol error")
)
```

- [ ] **Step 4: Implement the minimal Streamable HTTP JSON-RPC client**

Create `internal/infrastructure/mcpclient/client.go`:

```go
package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type listToolsResult struct {
	Tools []Tool `json:"tools"`
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type callToolResult struct {
	Content json.RawMessage `json:"content"`
}

var nextID atomic.Int64

func (c *Client) ListTools(ctx context.Context, server ServerConfig) ([]Tool, error) {
	raw, err := c.do(ctx, server, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var out listToolsResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%w: decode tools/list: %v", ErrProtocol, err)
	}
	return out.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, req CallRequest) (CallResult, error) {
	params, err := json.Marshal(callToolParams{Name: req.ToolName, Arguments: req.Arguments})
	if err != nil {
		return CallResult{}, fmt.Errorf("%w: encode call params: %v", ErrInvalidArguments, err)
	}
	raw, err := c.do(ctx, req.Server, "tools/call", params)
	if err != nil {
		return CallResult{}, err
	}
	var out callToolResult
	_ = json.Unmarshal(raw, &out)
	if len(out.Content) == 0 {
		out.Content = raw
	}
	return CallResult{Content: out.Content, Raw: raw}, nil
}

func (c *Client) do(ctx context.Context, server ServerConfig, method string, params json.RawMessage) (json.RawMessage, error) {
	if strings.TrimSpace(server.URL) == "" {
		return nil, fmt.Errorf("%w: empty server url", ErrProtocol)
	}
	if server.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, server.Timeout)
		defer cancel()
	}
	body, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      nextID.Add(1),
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode request: %v", ErrProtocol, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrProtocol, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range server.Headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %v", ErrTimeout, ctx.Err())
		}
		return nil, fmt.Errorf("%w: %v", ErrRemote, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrUnauthorized
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrProtocol, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d: %s", ErrRemote, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var rpc jsonRPCResponse
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return nil, fmt.Errorf("%w: decode response: %v", ErrProtocol, err)
	}
	if rpc.Error != nil {
		return nil, classifyRPCError(rpc.Error)
	}
	if len(rpc.Result) == 0 {
		return nil, fmt.Errorf("%w: empty result", ErrProtocol)
	}
	return rpc.Result, nil
}

func classifyRPCError(e *jsonRPCError) error {
	msg := strings.ToLower(e.Message)
	switch {
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "token"):
		return fmt.Errorf("%w: %s", ErrUnauthorized, e.Message)
	case strings.Contains(msg, "not found"):
		return fmt.Errorf("%w: %s", ErrToolNotFound, e.Message)
	case strings.Contains(msg, "invalid"):
		return fmt.Errorf("%w: %s", ErrInvalidArguments, e.Message)
	default:
		return fmt.Errorf("%w: %s", ErrRemote, e.Message)
	}
}
```

- [ ] **Step 5: Run tests**

Run:

```bash
go test ./internal/infrastructure/mcpclient
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/infrastructure/mcpclient
git -c commit.gpgsign=false commit -m "feat: add streamable mcp client"
```

## Task 3: Credential Store and Resolver

**Files:**
- Create: `internal/application/lark/luckin/credentials.go`
- Create: `internal/application/lark/luckin/credentials_test.go`
- Create: `internal/infrastructure/mcpstore/credentials.go`
- Create: `internal/infrastructure/mcpstore/crypto.go`
- Create: `internal/infrastructure/mcpstore/credentials_test.go`

- [ ] **Step 1: Verify generated DB models exist**

Run:

```bash
rg -n "type McpCredential|type LuckinPendingOrder" internal/infrastructure/db/model internal/infrastructure/db/query
```

Expected: matches in generated model and query files. If there is no match, stop and ask the user to run the SQL and `go run ./cmd/generate`.

- [ ] **Step 2: Write resolver tests**

Create `internal/application/lark/luckin/credentials_test.go`:

```go
package luckin

import (
	"context"
	"testing"
)

type fakeCredentialStore struct {
	values map[CredentialLookup]string
}

func (f fakeCredentialStore) FindToken(ctx context.Context, lookup CredentialLookup) (Credential, error) {
	token := f.values[lookup]
	if token == "" {
		return Credential{}, ErrCredentialNotFound
	}
	return Credential{Provider: "luckin", Scope: lookup.Scope, Token: token, TokenHint: MaskToken(token)}, nil
}

func TestResolverPrefersChatThenPersonalThenSystem(t *testing.T) {
	store := fakeCredentialStore{values: map[CredentialLookup]string{
		{Provider: "luckin", AppID: "app", BotOpenID: "bot", Scope: CredentialScope{Type: ScopeChat, ID: "chat"}}:       "chat-token",
		{Provider: "luckin", AppID: "app", BotOpenID: "bot", Scope: CredentialScope{Type: ScopePersonal, ID: "user"}}: "user-token",
	}}
	resolver := NewCredentialResolver(store, "system-token")
	cred, err := resolver.Resolve(context.Background(), CredentialRequest{
		AppID: "app", BotOpenID: "bot", ChatID: "chat", OpenID: "user", ChatType: ChatTypeGroup,
	})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if cred.Token != "chat-token" || cred.Scope.Type != ScopeChat {
		t.Fatalf("credential = %+v", cred)
	}
}

func TestResolverPrivateUsesPersonal(t *testing.T) {
	store := fakeCredentialStore{values: map[CredentialLookup]string{
		{Provider: "luckin", AppID: "app", BotOpenID: "bot", Scope: CredentialScope{Type: ScopePersonal, ID: "user"}}: "user-token",
	}}
	resolver := NewCredentialResolver(store, "")
	cred, err := resolver.Resolve(context.Background(), CredentialRequest{
		AppID: "app", BotOpenID: "bot", ChatID: "chat", OpenID: "user", ChatType: ChatTypePrivate,
	})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if cred.Scope.Type != ScopePersonal {
		t.Fatalf("scope = %s", cred.Scope.Type)
	}
}
```

- [ ] **Step 3: Run resolver tests and verify failure**

Run:

```bash
go test ./internal/application/lark/luckin -run 'TestResolver'
```

Expected: failure because credential types are missing.

- [ ] **Step 4: Implement credential resolver**

Create `internal/application/lark/luckin/credentials.go`:

```go
package luckin

import (
	"context"
	"errors"
	"strings"
)

type ScopeType string

const (
	ScopePersonal ScopeType = "personal"
	ScopeChat     ScopeType = "chat"
	ScopeSystem   ScopeType = "system"
)

type ChatType string

const (
	ChatTypePrivate ChatType = "private"
	ChatTypeGroup   ChatType = "group"
)

var ErrCredentialNotFound = errors.New("luckin credential not found")

type CredentialScope struct {
	Type ScopeType
	ID   string
}

type CredentialLookup struct {
	Provider  string
	AppID     string
	BotOpenID string
	Scope     CredentialScope
}

type Credential struct {
	Provider  string
	Scope     CredentialScope
	Token     string
	TokenHint string
}

type CredentialRequest struct {
	AppID     string
	BotOpenID string
	ChatID    string
	OpenID    string
	ChatType  ChatType
}

type CredentialStore interface {
	FindToken(context.Context, CredentialLookup) (Credential, error)
}

type CredentialResolver struct {
	store       CredentialStore
	systemToken string
}

func NewCredentialResolver(store CredentialStore, systemToken string) CredentialResolver {
	return CredentialResolver{store: store, systemToken: strings.TrimSpace(systemToken)}
}

func (r CredentialResolver) Resolve(ctx context.Context, req CredentialRequest) (Credential, error) {
	if req.ChatType == ChatTypeGroup && req.ChatID != "" {
		if cred, err := r.find(ctx, req, CredentialScope{Type: ScopeChat, ID: req.ChatID}); err == nil {
			return cred, nil
		}
	}
	if req.OpenID != "" {
		if cred, err := r.find(ctx, req, CredentialScope{Type: ScopePersonal, ID: req.OpenID}); err == nil {
			return cred, nil
		}
	}
	if r.systemToken != "" {
		return Credential{Provider: "luckin", Scope: CredentialScope{Type: ScopeSystem}, Token: r.systemToken, TokenHint: MaskToken(r.systemToken)}, nil
	}
	return Credential{}, ErrCredentialNotFound
}

func (r CredentialResolver) find(ctx context.Context, req CredentialRequest, scope CredentialScope) (Credential, error) {
	if r.store == nil {
		return Credential{}, ErrCredentialNotFound
	}
	return r.store.FindToken(ctx, CredentialLookup{
		Provider: "luckin", AppID: req.AppID, BotOpenID: req.BotOpenID, Scope: scope,
	})
}

func MaskToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 4 {
		return "****" + token
	}
	return "****" + token[len(token)-4:]
}
```

- [ ] **Step 5: Run resolver tests**

Run:

```bash
go test ./internal/application/lark/luckin -run 'TestResolver'
```

Expected: PASS.

- [ ] **Step 6: Write mcpstore crypto tests**

Create `internal/infrastructure/mcpstore/credentials_test.go`:

```go
package mcpstore

import "testing"

func TestEncryptDecryptToken(t *testing.T) {
	codec, err := NewTokenCodec("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewTokenCodec error = %v", err)
	}
	encrypted, err := codec.Encrypt("token-secret")
	if err != nil {
		t.Fatalf("Encrypt error = %v", err)
	}
	if encrypted == "token-secret" {
		t.Fatalf("token stored in plaintext")
	}
	decrypted, err := codec.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt error = %v", err)
	}
	if decrypted != "token-secret" {
		t.Fatalf("decrypted = %q", decrypted)
	}
}
```

- [ ] **Step 7: Implement token codec**

Create `internal/infrastructure/mcpstore/crypto.go`:

```go
package mcpstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

type TokenCodec struct {
	aead cipher.AEAD
}

func NewTokenCodec(key string) (TokenCodec, error) {
	if len(key) != 32 {
		return TokenCodec{}, fmt.Errorf("mcp token encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return TokenCodec{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return TokenCodec{}, err
	}
	return TokenCodec{aead: aead}, nil
}

func (c TokenCodec) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c TokenCodec) Decrypt(ciphertext string) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	if len(raw) < c.aead.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce := raw[:c.aead.NonceSize()]
	body := raw[c.aead.NonceSize():]
	opened, err := c.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", err
	}
	return string(opened), nil
}
```

- [ ] **Step 8: Implement repository skeleton using generated query**

Create `internal/infrastructure/mcpstore/credentials.go`:

```go
package mcpstore

import (
	"context"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	infraDB "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/model"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/db/query"
	"gorm.io/gorm"
)

type CredentialRepository struct {
	q     *query.Query
	codec TokenCodec
}

func NewCredentialRepository(db *gorm.DB, codec TokenCodec) *CredentialRepository {
	return &CredentialRepository{q: query.Use(infraDB.WithoutQueryCache(db)), codec: codec}
}

func (r *CredentialRepository) FindToken(ctx context.Context, lookup luckin.CredentialLookup) (luckin.Credential, error) {
	ins := r.q.McpCredential
	rows, err := ins.WithContext(ctx).
		Where(ins.Provider.Eq(lookup.Provider)).
		Where(ins.AppID.Eq(lookup.AppID)).
		Where(ins.BotOpenID.Eq(lookup.BotOpenID)).
		Where(ins.ScopeType.Eq(string(lookup.Scope.Type))).
		Where(ins.ScopeID.Eq(lookup.Scope.ID)).
		Where(ins.DeletedAt.IsNull()).
		Limit(1).
		Find()
	if err != nil {
		return luckin.Credential{}, err
	}
	if len(rows) == 0 {
		return luckin.Credential{}, luckin.ErrCredentialNotFound
	}
	token, err := r.codec.Decrypt(rows[0].EncryptedToken)
	if err != nil {
		return luckin.Credential{}, err
	}
	return luckin.Credential{
		Provider: lookup.Provider,
		Scope:    lookup.Scope,
		Token:    token,
		TokenHint: rows[0].TokenHint,
	}, nil
}

func (r *CredentialRepository) UpsertToken(ctx context.Context, lookup luckin.CredentialLookup, token, actorOpenID string) error {
	encrypted, err := r.codec.Encrypt(token)
	if err != nil {
		return err
	}
	now := time.Now()
	row := &model.McpCredential{
		Provider: lookup.Provider, AppID: lookup.AppID, BotOpenID: lookup.BotOpenID,
		ScopeType: string(lookup.Scope.Type), ScopeID: lookup.Scope.ID,
		EncryptedToken: encrypted, TokenHint: luckin.MaskToken(token),
		CreatedByOpenID: actorOpenID, UpdatedByOpenID: actorOpenID,
		CreatedAt: now, UpdatedAt: now,
	}
	return r.q.McpCredential.WithContext(ctx).Save(row)
}
```

- [ ] **Step 9: Run tests**

Run:

```bash
go test ./internal/application/lark/luckin ./internal/infrastructure/mcpstore
```

Expected: PASS, or compile failure pointing at generated field names. If generated names differ, adjust the repository field names to match generated models.

- [ ] **Step 10: Commit**

Run:

```bash
git add internal/application/lark/luckin internal/infrastructure/mcpstore
git -c commit.gpgsign=false commit -m "feat: add mcp credential resolver"
```

## Task 4: MCP Bridge and Luckin Read Tools

**Files:**
- Create: `internal/application/lark/mcpbridge/bridge.go`
- Create: `internal/application/lark/mcpbridge/bridge_test.go`
- Create: `internal/application/lark/luckin/tools.go`
- Create: `internal/application/lark/luckin/tools_test.go`

- [ ] **Step 1: Write whitelist test**

Create `internal/application/lark/luckin/tools_test.go`:

```go
package luckin

import "testing"

func TestToolPoliciesDoNotExposeCreateOrderDirectly(t *testing.T) {
	policies := ToolPolicies()
	for _, p := range policies {
		if p.MCPToolName == "createOrder" && p.DirectLLM {
			t.Fatalf("createOrder must not be directly exposed")
		}
	}
	if _, ok := PolicyByRobotTool("luckin_order_prepare_create"); !ok {
		t.Fatalf("missing prepare-create policy")
	}
}
```

- [ ] **Step 2: Implement Luckin tool policies**

Create `internal/application/lark/luckin/tools.go`:

```go
package luckin

import "time"

const (
	ProviderName = "luckin"
	ServerName   = "my-coffee"
	ServerURL    = "https://gwmcp.lkcoffee.com/order/user/mcp"
)

type ToolPolicy struct {
	RobotToolName string
	MCPToolName   string
	Description   string
	DirectLLM     bool
	HighRisk      bool
}

func ToolPolicies() []ToolPolicy {
	return []ToolPolicy{
		{RobotToolName: "luckin_shop_search", MCPToolName: "queryShopList", Description: "查询瑞幸咖啡门店列表", DirectLLM: true},
		{RobotToolName: "luckin_product_search", MCPToolName: "searchProductForMcp", Description: "按用户查询文本搜索瑞幸商品", DirectLLM: true},
		{RobotToolName: "luckin_product_detail", MCPToolName: "queryProductDetailInfo", Description: "查询瑞幸商品详情", DirectLLM: true},
		{RobotToolName: "luckin_product_switch", MCPToolName: "switchProduct", Description: "切换瑞幸商品规格属性", DirectLLM: true},
		{RobotToolName: "luckin_order_preview", MCPToolName: "previewOrder", Description: "预览瑞幸订单价格和取餐信息", DirectLLM: true},
		{RobotToolName: "luckin_order_detail", MCPToolName: "queryOrderDetailInfo", Description: "查询瑞幸订单详情", DirectLLM: true},
		{RobotToolName: "luckin_order_prepare_create", MCPToolName: "createOrder", Description: "创建待确认瑞幸订单草稿，不直接下单", DirectLLM: true, HighRisk: true},
	}
}

func PolicyByRobotTool(name string) (ToolPolicy, bool) {
	for _, p := range ToolPolicies() {
		if p.RobotToolName == name {
			return p, true
		}
	}
	return ToolPolicy{}, false
}

func DefaultTimeout() time.Duration {
	return 15 * time.Second
}
```

- [ ] **Step 3: Write bridge registration test**

Create `internal/application/lark/mcpbridge/bridge_test.go`:

```go
package mcpbridge

import (
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestRegisterAddsAllowedTools(t *testing.T) {
	ins := arktools.New[larkim.P2MessageReceiveV1]()
	Register(ins, RegisterOptions{Policies: luckin.ToolPolicies()})
	specs := ins.GetTools()
	foundCreateOrder := false
	foundPrepare := false
	for _, spec := range specs {
		if spec.Name == "createOrder" {
			foundCreateOrder = true
		}
		if spec.Name == "luckin_order_prepare_create" {
			foundPrepare = true
		}
	}
	if foundCreateOrder {
		t.Fatalf("raw createOrder was registered")
	}
	if !foundPrepare {
		t.Fatalf("prepare-create tool missing")
	}
}
```

- [ ] **Step 4: Implement the bridge**

Create `internal/application/lark/mcpbridge/bridge.go`:

```go
package mcpbridge

import (
	"context"
	"encoding/json"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/mcpclient"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type RegisterOptions struct {
	Policies []luckin.ToolPolicy
	Client   *mcpclient.Client
}

type rawArgs struct {
	JSON json.RawMessage
}

type handler struct {
	policy luckin.ToolPolicy
	client *mcpclient.Client
}

func Register(ins *arktools.Impl[larkim.P2MessageReceiveV1], opts RegisterOptions) {
	for _, policy := range opts.Policies {
		if !policy.DirectLLM {
			continue
		}
		xcommand.RegisterTool(ins, handler{policy: policy, client: opts.Client})
	}
}

func (h handler) ParseTool(raw string) (rawArgs, error) {
	if raw == "" {
		raw = "{}"
	}
	return rawArgs{JSON: json.RawMessage(raw)}, nil
}

func (h handler) ToolSpec() xcommand.ToolSpec {
	return xcommand.ToolSpec{
		Name: h.policy.RobotToolName,
		Desc: h.policy.Description,
		Params: arktools.NewParams("object"),
		Result: func(metaData *xhandler.BaseMetaData) string {
			result, _ := metaData.GetExtra(h.policy.RobotToolName + "_result")
			return result
		},
	}
}

func (h handler) Handle(ctx context.Context, data *larkim.P2MessageReceiveV1, metaData *xhandler.BaseMetaData, arg rawArgs) error {
	metaData.SetExtra(h.policy.RobotToolName+"_result", string(arg.JSON))
	return nil
}
```

This task intentionally leaves remote calling out of the bridge until Task 5 wires credential resolution and pending-order behavior.

- [ ] **Step 5: Run tests**

Run:

```bash
go test ./internal/application/lark/luckin ./internal/application/lark/mcpbridge
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/application/lark/luckin internal/application/lark/mcpbridge
git -c commit.gpgsign=false commit -m "feat: add luckin mcp tool policies"
```

## Task 5: Pending Orders and Confirmation Cards

**Files:**
- Create: `internal/application/lark/luckin/pending.go`
- Create: `internal/application/lark/luckin/pending_test.go`
- Create: `internal/application/lark/luckin/card.go`
- Create: `internal/application/lark/luckin/card_test.go`
- Create: `internal/infrastructure/mcpstore/pending_orders.go`
- Modify: `pkg/cardaction/action.go`

- [ ] **Step 1: Add card action constants**

Modify `pkg/cardaction/action.go` by adding constants:

```go
const (
	ActionLuckinOrderConfirm = "luckin_order_confirm"
	ActionLuckinOrderCancel  = "luckin_order_cancel"
	PendingOrderIDField      = "pending_order_id"
	PayloadHashField         = "payload_hash"
)
```

- [ ] **Step 2: Write pending order tests**

Create `internal/application/lark/luckin/pending_test.go`:

```go
package luckin

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewPendingOrderComputesHashAndExpiry(t *testing.T) {
	payload := json.RawMessage(`{"deptId":1,"productList":[{"amount":1,"productId":2,"skuCode":"s"}]}`)
	order := NewPendingOrder(NewPendingOrderRequest{
		ChatID: "chat", RequesterOpenID: "user",
		Credential: Credential{Scope: CredentialScope{Type: ScopePersonal, ID: "user"}},
		CreateOrderPayload: payload,
		PreviewResult: json.RawMessage(`{"discountPrice":9.9}`),
		Now: time.Unix(100, 0),
	})
	if order.ID == "" || order.PayloadHash == "" {
		t.Fatalf("missing id or hash: %+v", order)
	}
	if !order.ExpiresAt.Equal(time.Unix(100, 0).Add(10 * time.Minute)) {
		t.Fatalf("expires_at = %s", order.ExpiresAt)
	}
}
```

- [ ] **Step 3: Implement pending order domain**

Create `internal/application/lark/luckin/pending.go`:

```go
package luckin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type PendingStatus string

const (
	PendingStatusPending   PendingStatus = "pending"
	PendingStatusConfirmed PendingStatus = "confirmed"
	PendingStatusExpired   PendingStatus = "expired"
	PendingStatusCancelled PendingStatus = "cancelled"
	PendingStatusFailed    PendingStatus = "failed"
)

type PendingOrder struct {
	ID                  string
	ChatID              string
	RequesterOpenID     string
	CredentialScope     CredentialScope
	MCPServerName       string
	CreateOrderPayload  json.RawMessage
	PayloadHash         string
	PreviewResult       json.RawMessage
	Status              PendingStatus
	ExpiresAt           time.Time
	ConfirmedByOpenID   string
	ConfirmedAt         *time.Time
}

type NewPendingOrderRequest struct {
	ChatID             string
	RequesterOpenID    string
	Credential         Credential
	CreateOrderPayload json.RawMessage
	PreviewResult      json.RawMessage
	Now                time.Time
}

func NewPendingOrder(req NewPendingOrderRequest) PendingOrder {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	hash := sha256.Sum256(req.CreateOrderPayload)
	return PendingOrder{
		ID: uuid.NewString(), ChatID: req.ChatID, RequesterOpenID: req.RequesterOpenID,
		CredentialScope: req.Credential.Scope, MCPServerName: ServerName,
		CreateOrderPayload: req.CreateOrderPayload, PayloadHash: hex.EncodeToString(hash[:]),
		PreviewResult: req.PreviewResult, Status: PendingStatusPending,
		ExpiresAt: now.Add(10 * time.Minute),
	}
}
```

- [ ] **Step 4: Write card tests**

Create `internal/application/lark/luckin/card_test.go`:

```go
package luckin

import "testing"

func TestBuildPendingOrderCardContainsScopeAndActions(t *testing.T) {
	card := BuildPendingOrderCard(PendingOrder{
		ID: "po_1", PayloadHash: "hash_1",
		CredentialScope: CredentialScope{Type: ScopeChat, ID: "chat"},
	})
	text := MustMarshalForTest(card)
	if !containsAll(text, "群聊默认瑞幸账号", "luckin_order_confirm", "luckin_order_cancel") {
		t.Fatalf("card missing required content: %s", text)
	}
}
```

- [ ] **Step 5: Implement card builder**

Create `internal/application/lark/luckin/card.go`:

```go
package luckin

import (
	"encoding/json"
	"strings"

	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func ScopeLabel(scope CredentialScope) string {
	switch scope.Type {
	case ScopePersonal:
		return "个人瑞幸账号"
	case ScopeChat:
		return "群聊默认瑞幸账号"
	case ScopeSystem:
		return "系统默认瑞幸账号"
	default:
		return "未知瑞幸账号"
	}
}

func BuildPendingOrderCard(order PendingOrder) map[string]any {
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"wide_screen_mode": true},
		"body": map[string]any{
			"elements": []any{
				map[string]any{"tag": "markdown", "content": "**瑞幸订单确认**"},
				map[string]any{"tag": "markdown", "content": "账号作用域：" + ScopeLabel(order.CredentialScope)},
				map[string]any{"tag": "markdown", "content": "点击确认将创建瑞幸订单，但不会自动支付。"},
				map[string]any{
					"tag": "button", "text": map[string]any{"tag": "plain_text", "content": "确认下单"},
					"type": "primary",
					"behaviors": []any{map[string]any{"type": "callback", "value": map[string]any{
						"action": cardactionproto.ActionLuckinOrderConfirm,
						cardactionproto.PendingOrderIDField: order.ID,
						cardactionproto.PayloadHashField: order.PayloadHash,
					}}},
				},
				map[string]any{
					"tag": "button", "text": map[string]any{"tag": "plain_text", "content": "取消"},
					"type": "default",
					"behaviors": []any{map[string]any{"type": "callback", "value": map[string]any{
						"action": cardactionproto.ActionLuckinOrderCancel,
						cardactionproto.PendingOrderIDField: order.ID,
					}}},
				},
			},
		},
	}
}

func MustMarshalForTest(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
```

- [ ] **Step 6: Implement pending repository mapper**

Create `internal/infrastructure/mcpstore/pending_orders.go` with methods `CreatePendingOrder`, `FindPendingOrder`, and `MarkConfirmed`. Use generated `query.LuckinPendingOrder` and map JSON fields as strings or `datatypes.JSON` according to generated model types. Preserve the domain names from `luckin.PendingOrder`.

- [ ] **Step 7: Run tests**

Run:

```bash
go test ./internal/application/lark/luckin ./internal/infrastructure/mcpstore
```

Expected: PASS, or generated model field-name compile errors to fix in `pending_orders.go`.

- [ ] **Step 8: Commit**

Run:

```bash
git add pkg/cardaction/action.go internal/application/lark/luckin internal/infrastructure/mcpstore
git -c commit.gpgsign=false commit -m "feat: add luckin pending order confirmation"
```

## Task 6: Remote Tool Execution and Prepare-Create Behavior

**Files:**
- Modify: `internal/application/lark/mcpbridge/bridge.go`
- Modify: `internal/application/lark/mcpbridge/bridge_test.go`
- Create: `internal/application/lark/luckin/register.go`
- Create: `internal/application/lark/luckin/register_test.go`

- [ ] **Step 1: Extend bridge options**

Modify `RegisterOptions` in `internal/application/lark/mcpbridge/bridge.go`:

```go
type CredentialResolver interface {
	Resolve(context.Context, luckin.CredentialRequest) (luckin.Credential, error)
}

type PendingOrderService interface {
	CreatePendingOrder(context.Context, luckin.PendingOrder) error
}

type RegisterOptions struct {
	Policies   []luckin.ToolPolicy
	Client     *mcpclient.Client
	Resolver   CredentialResolver
	Pending    PendingOrderService
	SystemURL  string
}
```

- [ ] **Step 2: Implement read tool remote calls**

In `handler.Handle`, branch on `h.policy.HighRisk`. For non-high-risk tools:

```go
cred, err := h.resolver.Resolve(ctx, credentialRequestFromMessage(data, metaData))
if err != nil {
	return err
}
res, err := h.client.CallTool(ctx, mcpclient.CallRequest{
	Server: mcpclient.ServerConfig{
		Name: luckin.ServerName,
		URL: luckin.ServerURL,
		Headers: map[string]string{"Authorization": "Bearer " + cred.Token},
		Timeout: luckin.DefaultTimeout(),
	},
	ToolName: h.policy.MCPToolName,
	Arguments: arg.JSON,
})
if err != nil {
	return err
}
metaData.SetExtra(h.policy.RobotToolName+"_result", string(res.Content))
return nil
```

Add helper `credentialRequestFromMessage` in the same file. Use `metaData.ChatID` and `metaData.OpenID` as fallbacks when the Lark event does not expose fields cleanly.

- [ ] **Step 3: Implement prepare-create**

For `h.policy.HighRisk`, do not call MCP. Build a `luckin.PendingOrder` with the raw create-order JSON payload and empty preview result when no preview was provided, persist it through `h.pending`, and set result:

```go
metaData.SetExtra(h.policy.RobotToolName+"_result", "瑞幸订单确认卡片已发送，请由发起人确认后再创建订单")
```

The card send can be wired in Task 7 if direct sending helpers require Lark message context.

- [ ] **Step 4: Run tests**

Run:

```bash
go test ./internal/application/lark/mcpbridge
```

Expected: PASS with fake client/resolver tests covering direct read call and high-risk no-remote-call behavior.

- [ ] **Step 5: Commit**

Run:

```bash
git add internal/application/lark/mcpbridge internal/application/lark/luckin
git -c commit.gpgsign=false commit -m "feat: execute luckin mcp read tools"
```

## Task 7: Card Actions and Tool Registration

**Files:**
- Modify: `internal/application/lark/cardaction/builtin.go`
- Create: `internal/application/lark/luckin/card_action.go`
- Create: `internal/application/lark/luckin/card_action_test.go`
- Modify: `internal/application/lark/handlers/tools.go`
- Create: `internal/application/lark/handlers/luckin_tools_test.go`

- [ ] **Step 1: Implement Luckin card action handlers**

Create `internal/application/lark/luckin/card_action.go` with exported handlers:

```go
package luckin

import (
	"context"
	"time"

	appcardaction "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/cardaction"
	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

type ConfirmationService interface {
	Confirm(context.Context, ConfirmRequest) (map[string]any, error)
	Cancel(context.Context, CancelRequest) error
}

type ConfirmRequest struct {
	PendingOrderID string
	PayloadHash    string
	OperatorOpenID string
	ChatID         string
	Now            time.Time
}

type CancelRequest struct {
	PendingOrderID string
	OperatorOpenID string
	ChatID         string
}

func HandleConfirm(service ConfirmationService) appcardaction.SyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		id, err := actionCtx.Action.RequiredString(cardactionproto.PendingOrderIDField)
		if err != nil {
			return nil, err
		}
		hash, err := actionCtx.Action.RequiredString(cardactionproto.PayloadHashField)
		if err != nil {
			return nil, err
		}
		card, err := service.Confirm(ctx, ConfirmRequest{
			PendingOrderID: id, PayloadHash: hash, OperatorOpenID: actionCtx.OpenID(), ChatID: actionCtx.ChatID(), Now: time.Now(),
		})
		if err != nil {
			return appcardaction.ErrorToast(err.Error()), nil
		}
		return appcardaction.InfoToastWithRawCardPayload("瑞幸订单已创建", card), nil
	}
}
```

Add `HandleCancel` to the same file:

```go
func HandleCancel(service ConfirmationService) appcardaction.SyncHandler {
	return func(ctx context.Context, actionCtx *appcardaction.Context) (*callback.CardActionTriggerResponse, error) {
		id, err := actionCtx.Action.RequiredString(cardactionproto.PendingOrderIDField)
		if err != nil {
			return nil, err
		}
		if err := service.Cancel(ctx, CancelRequest{
			PendingOrderID: id, OperatorOpenID: actionCtx.OpenID(), ChatID: actionCtx.ChatID(),
		}); err != nil {
			return appcardaction.ErrorToast(err.Error()), nil
		}
		return appcardaction.InfoToast("瑞幸订单已取消"), nil
	}
}
```

- [ ] **Step 2: Register built-in card actions**

Modify `internal/application/lark/cardaction/builtin.go` inside `RegisterBuiltins`:

```go
RegisterSync(cardactionproto.ActionLuckinOrderConfirm, luckin.HandleConfirm(luckin.DefaultConfirmationService()))
RegisterSync(cardactionproto.ActionLuckinOrderCancel, luckin.HandleCancel(luckin.DefaultConfirmationService()))
```

If dependency construction cannot be done without cycles, register thin handlers in `cardaction/builtin.go` that call package-level functions in `luckin` initialized during bootstrap.

- [ ] **Step 3: Register Luckin tools**

Modify `internal/application/lark/handlers/tools.go`:

```go
func BuildRuntimeCapabilityTools() *tools.Impl[larkim.P2MessageReceiveV1] {
	ins := BuildLarkTools()
	registerInjectableFinanceTools(ins)
	luckin.RegisterTools(ins, luckin.RegisterOptionsFromEnv())
	return ins
}
```

Also register in `BuildLarkTools` if regular chat should use Luckin tools. Do not register in `BuildSchedulableTools`.

- [ ] **Step 4: Write registration test**

Create `internal/application/lark/handlers/luckin_tools_test.go`:

```go
package handlers

import "testing"

func TestSchedulableToolsDoNotIncludeLuckinCreate(t *testing.T) {
	tools := BuildSchedulableTools().GetTools()
	for _, tool := range tools {
		if tool.Name == "luckin_order_prepare_create" {
			t.Fatalf("schedulable tools include luckin_order_prepare_create")
		}
	}
}
```

- [ ] **Step 5: Run tests**

Run:

```bash
go test ./internal/application/lark/cardaction ./internal/application/lark/luckin ./internal/application/lark/handlers
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add internal/application/lark/cardaction internal/application/lark/luckin internal/application/lark/handlers pkg/cardaction/action.go
git -c commit.gpgsign=false commit -m "feat: wire luckin tools and confirmation actions"
```

## Task 8: Verification and Documentation

**Files:**
- Modify: `docs/superpowers/specs/2026-06-15-luckin-mcp-integration-design.md` only if implementation deliberately differs from the approved design.
- Create: `docs/luckin_mcp_usage.md`

- [ ] **Step 1: Write usage documentation**

Create `docs/luckin_mcp_usage.md`:

```markdown
# 瑞幸 MCP 使用说明

## 凭证

瑞幸 Token 来自瑞幸开放平台，绑定瑞幸账号会话。机器人内部支持个人、群聊默认和系统默认三类作用域。

## 下单安全

机器人可以查询门店、商品和订单预览。创建订单必须点击飞书确认卡片，确认后只创建订单并返回支付链接或二维码，不会自动支付。

## 环境变量

- `LUCKIN_MCP_TOKEN`: 可选系统默认瑞幸 Token。
- `MCP_CREDENTIALS_KEY`: 32 字节 token 加密 key。
```

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./internal/infrastructure/mcpclient ./internal/infrastructure/mcpstore ./internal/application/lark/luckin ./internal/application/lark/mcpbridge ./internal/application/lark/handlers
```

Expected: PASS.

- [ ] **Step 3: Run broad compile test**

Run:

```bash
go test ./...
```

Expected: PASS. If unrelated existing worktree changes cause failures, record exact package and failure output before deciding whether the failure belongs to this feature.

- [ ] **Step 4: Commit docs and any verification fixes**

Run:

```bash
git add docs/luckin_mcp_usage.md docs/superpowers/specs/2026-06-15-luckin-mcp-integration-design.md
git -c commit.gpgsign=false commit -m "docs: add luckin mcp usage"
```

## Self-Review Checklist

- MCP client exists below `internal/infrastructure/mcpclient` and does not import Lark or Luckin packages.
- Raw `createOrder` is never registered as an LLM tool.
- `luckin_order_prepare_create` creates a pending order and card flow instead of calling MCP.
- Confirmation verifies pending ID, payload hash, chat, operator, status, and expiry.
- Token storage encrypts tokens and only displays masked hints.
- Schedulable tools do not include Luckin order creation.
- Tests cover read calls, credential fallback, no direct high-risk calls, and card action confirmation.

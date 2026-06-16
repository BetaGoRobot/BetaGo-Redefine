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

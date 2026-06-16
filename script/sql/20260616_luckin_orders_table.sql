create schema if not exists betago;

create table if not exists betago.luckin_orders (
    id bigserial primary key,
    order_id text not null,
    app_id text not null default '',
    bot_open_id text not null default '',
    chat_id text not null default '',
    requester_open_id text not null default '',
    credential_scope_type text not null,
    credential_scope_id text not null default '',
    message_id text not null default '',
    status text not null default 'active',
    last_remote_status bigint not null default 0,
    need_pay boolean not null default false,
    pay_url text not null default '',
    qr_url text not null default '',
    discount_price double precision not null default 0,
    unpaid_reminded boolean not null default false,
    next_poll_at timestamptz not null default now(),
    poll_deadline timestamptz not null,
    fail_count bigint not null default 0,
    stopped_reason text not null default '',
    placed_at timestamptz,
    making_at timestamptz,
    ready_at timestamptz,
    completed_at timestamptz,
    cancelled_at timestamptz,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint luckin_orders_order_unique unique (app_id, bot_open_id, order_id)
);

create index if not exists idx_luckin_orders_poll
    on betago.luckin_orders (status, next_poll_at)
    where status = 'active';

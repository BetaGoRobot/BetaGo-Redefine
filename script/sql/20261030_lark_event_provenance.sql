create table if not exists betago.lark_event_audits (
    app_id text not null,
    bot_open_id text not null,
    event_id text not null,
    event_type text not null,
    message_id text not null,
    operator_type text not null default '',
    actor_open_id text not null default '',
    actor_user_id text not null default '',
    actor_app_id text not null default '',
    reaction_type text not null default '',
    action_time text not null default '',
    occurred_at timestamptz,
    received_at timestamptz not null,
    trace_id text not null default '',
    chat_id text not null default '',
    status text not null,
    resolution_source text not null default '',
    error_code integer not null default 0,
    error text not null default '',
    payload jsonb not null,
    primary key (app_id, bot_open_id, event_id)
);
create index if not exists idx_lark_event_message
    on betago.lark_event_audits (app_id, bot_open_id, message_id);
create index if not exists idx_lark_event_actor_time
    on betago.lark_event_audits (app_id, bot_open_id, actor_open_id, occurred_at);
create index if not exists idx_lark_event_retention
    on betago.lark_event_audits (app_id, bot_open_id, received_at);

create table if not exists betago.lark_message_locations (
    app_id text not null,
    bot_open_id text not null,
    message_id text not null,
    chat_id text not null,
    recalled boolean not null default false,
    observed_at timestamptz not null,
    last_seen_at timestamptz not null,
    primary key (app_id, bot_open_id, message_id)
);
create index if not exists idx_lark_location_retention
    on betago.lark_message_locations (app_id, bot_open_id, last_seen_at);

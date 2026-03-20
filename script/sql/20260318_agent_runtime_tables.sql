create schema if not exists betago;

create table if not exists betago.agent_sessions (
    id text primary key,
    app_id text not null,
    bot_open_id text not null,
    chat_id text not null,
    scope_type text not null,
    scope_id text not null,
    status text not null,
    active_run_id text not null default '',
    last_message_id text not null default '',
    last_actor_open_id text not null default '',
    memory_version bigint not null default 0,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create unique index if not exists idx_agent_sessions_scope_unique
    on betago.agent_sessions (app_id, bot_open_id, scope_type, scope_id);

create index if not exists idx_agent_sessions_status_updated
    on betago.agent_sessions (status, updated_at desc);

create table if not exists betago.agent_runs (
    id text primary key,
    session_id text not null references betago.agent_sessions(id) on delete cascade,
    trigger_type text not null,
    trigger_message_id text not null default '',
    trigger_event_id text not null default '',
    actor_open_id text not null default '',
    parent_run_id text not null default '',
    status text not null,
    goal text not null default '',
    input_text text not null default '',
    current_step_index integer not null default 0,
    waiting_reason text not null default '',
    waiting_token text not null default '',
    last_response_id text not null default '',
    result_summary text not null default '',
    error_text text not null default '',
    revision bigint not null default 0,
    started_at timestamptz null,
    finished_at timestamptz null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create unique index if not exists idx_agent_runs_session_trigger_unique
    on betago.agent_runs (session_id, trigger_message_id);

create index if not exists idx_agent_runs_status_updated
    on betago.agent_runs (status, updated_at desc);

create index if not exists idx_agent_runs_session_updated
    on betago.agent_runs (session_id, updated_at desc);

create table if not exists betago.agent_steps (
    id text primary key,
    run_id text not null references betago.agent_runs(id) on delete cascade,
    index integer not null,
    kind text not null,
    status text not null,
    capability_name text not null default '',
    input_json jsonb not null default '{}'::jsonb,
    output_json jsonb not null default '{}'::jsonb,
    error_text text not null default '',
    external_ref text not null default '',
    started_at timestamptz null,
    finished_at timestamptz null,
    created_at timestamptz not null default now()
);

create unique index if not exists idx_agent_steps_run_index_unique
    on betago.agent_steps (run_id, index);

create index if not exists idx_agent_steps_run_created
    on betago.agent_steps (run_id, created_at asc);

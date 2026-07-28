-- 本脚本包含 CREATE INDEX CONCURRENTLY，不得包在单个显式事务中。

create schema if not exists betago;

alter table if exists betago.agent_runs
    add column if not exists activation_source text not null default '';

alter table if exists betago.agent_runs
    add column if not exists topic_fingerprint text not null default '';

alter table if exists betago.agent_runs
    add column if not exists last_relevant_at timestamptz null;

create index concurrently if not exists idx_agent_runs_session_last_relevant
    on betago.agent_runs (session_id, last_relevant_at desc)
    where status in ('queued', 'running');

alter table if exists betago.agent_steps
    add column if not exists dedupe_key text not null default '';

alter table if exists betago.agent_steps
    add column if not exists attempt_count integer not null default 0;

alter table if exists betago.agent_steps
    add column if not exists worker_id text not null default '';

alter table if exists betago.agent_steps
    add column if not exists lease_expires_at timestamptz null;

alter table if exists betago.agent_steps
    add column if not exists retry_of_step_id text not null default '';

create unique index concurrently if not exists idx_agent_steps_run_dedupe_unique
    on betago.agent_steps (run_id, dedupe_key)
    where dedupe_key <> '';

create index concurrently if not exists idx_agent_steps_queue_claim
    on betago.agent_steps (created_at, id)
    where status = 'queued';

create index concurrently if not exists idx_agent_steps_running_reclaim
    on betago.agent_steps (lease_expires_at, id)
    where status = 'running';

create table if not exists betago.agent_capability_executions (
    idempotency_key text primary key,
    run_id text not null references betago.agent_runs(id) on delete cascade,
    step_id text not null references betago.agent_steps(id) on delete cascade,
    capability_name text not null,
    status text not null,
    input_json jsonb not null default '{}'::jsonb,
    output_json jsonb not null default '{}'::jsonb,
    error_text text not null default '',
    started_at timestamptz null,
    finished_at timestamptz null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create index if not exists idx_agent_capability_run_created
    on betago.agent_capability_executions (run_id, created_at);

create index if not exists idx_agent_capability_step
    on betago.agent_capability_executions (step_id);

create table if not exists betago.agent_projection_outbox (
    id text primary key,
    step_id text not null references betago.agent_steps(id) on delete cascade,
    index_alias text not null,
    document_id text not null,
    payload_json jsonb not null,
    status text not null default 'pending',
    attempt_count integer not null default 0,
    next_attempt_at timestamptz not null default now(),
    worker_id text not null default '',
    lease_expires_at timestamptz null,
    last_error text not null default '',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (step_id)
);

create index if not exists idx_agent_projection_outbox_claim
    on betago.agent_projection_outbox (next_attempt_at, id)
    where status = 'pending';

create index if not exists idx_agent_projection_outbox_reclaim
    on betago.agent_projection_outbox (lease_expires_at, id)
    where status = 'running';

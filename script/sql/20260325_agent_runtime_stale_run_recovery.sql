create schema if not exists betago;

alter table if exists betago.agent_runs
    add column if not exists worker_id text not null default '';

alter table if exists betago.agent_runs
    add column if not exists heartbeat_at timestamptz null;

alter table if exists betago.agent_runs
    add column if not exists lease_expires_at timestamptz null;

alter table if exists betago.agent_runs
    add column if not exists repair_attempts bigint not null default 0;

create index if not exists idx_agent_runs_active_lease_expires
    on betago.agent_runs (lease_expires_at asc, updated_at asc)
    where status in ('queued', 'running', 'waiting_approval', 'waiting_schedule', 'waiting_callback');

create index if not exists idx_agent_runs_worker_updated
    on betago.agent_runs (worker_id, updated_at desc);

-- rollback reference (manual, execute only after impact review):
-- drop index if exists betago.idx_agent_runs_worker_updated;
-- drop index if exists betago.idx_agent_runs_active_lease_expires;
-- alter table betago.agent_runs drop column if exists repair_attempts;
-- alter table betago.agent_runs drop column if exists lease_expires_at;
-- alter table betago.agent_runs drop column if exists heartbeat_at;
-- alter table betago.agent_runs drop column if exists worker_id;

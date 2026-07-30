alter table betago.evaluation_episodes
    add column if not exists post_window_reason text not null default '';

create table if not exists betago.evaluation_episode_messages (
    id text primary key,
    episode_id text not null references betago.evaluation_episodes(id) on delete cascade,
    position text not null,
    event_id text not null,
    message_id text not null,
    sequence integer not null,
    occurred_at timestamptz not null,
    payload_json jsonb not null default '{}'::jsonb,
    created_at timestamptz not null default now(),
    unique (episode_id, position, event_id)
);

create table if not exists betago.evaluation_candidate_tasks (
    id text primary key,
    episode_id text not null references betago.evaluation_episodes(id) on delete cascade,
    status text not null,
    payload_json jsonb not null default '{}'::jsonb,
    attempt_count integer not null default 0,
    next_attempt_at timestamptz not null,
    worker_id text not null default '',
    lease_expires_at timestamptz null,
    last_error text not null default '',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (episode_id)
);

create index if not exists idx_eval_episode_message_timeline
    on betago.evaluation_episode_messages (episode_id, position, occurred_at, event_id);

create index if not exists idx_eval_cohort_chat_ids
    on betago.evaluation_cohorts using gin (chat_ids jsonb_path_ops);

create index if not exists idx_eval_candidate_task_claim
    on betago.evaluation_candidate_tasks (next_attempt_at, id)
    where status = 'queued';

create index if not exists idx_eval_candidate_task_reclaim
    on betago.evaluation_candidate_tasks (lease_expires_at, id)
    where status = 'running';

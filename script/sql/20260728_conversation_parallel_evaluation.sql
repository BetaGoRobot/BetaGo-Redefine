create schema if not exists betago;

create table if not exists betago.evaluation_cohorts (
    id text primary key,
    app_id text not null,
    bot_open_id text not null,
    chat_ids jsonb not null default '[]'::jsonb,
    start_at timestamptz not null,
    end_at timestamptz not null,
    status text not null,
    serving_lane text not null,
    control_version text not null,
    candidate_version text not null,
    judge_config_json jsonb not null default '{}'::jsonb,
    sampling_policy_json jsonb not null default '{}'::jsonb,
    result_version bigint not null default 0,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table if not exists betago.evaluation_episodes (
    id text primary key,
    cohort_id text not null references betago.evaluation_cohorts(id) on delete cascade,
    chat_id text not null,
    run_id text not null default '',
    anchor_event_id text not null,
    anchor_message_id text not null,
    topic_id text not null default '',
    serving_lane text not null,
    status text not null,
    pre_window_start timestamptz not null,
    anchor_at timestamptz not null,
    post_window_end timestamptz null,
    post_window_reason text not null default '',
    late_feedback_until timestamptz not null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (cohort_id, anchor_event_id)
);

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

create table if not exists betago.evaluation_lane_outputs (
    id text primary key,
    episode_id text not null references betago.evaluation_episodes(id) on delete cascade,
    lane text not null,
    output_mode text not null,
    activation_json jsonb not null default '{}'::jsonb,
    relevance_json jsonb not null default '{}'::jsonb,
    join_decision text not null,
    topic_relation text not null,
    context_snapshot_json jsonb not null default '{}'::jsonb,
    excluded_context_json jsonb not null default '[]'::jsonb,
    tool_plan_json jsonb not null default '{}'::jsonb,
    reply_text text not null default '',
    latency_ms bigint not null default 0,
    token_usage_json jsonb not null default '{}'::jsonb,
    error_json jsonb not null default '{}'::jsonb,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (episode_id, lane)
);

create table if not exists betago.evaluation_feedback (
    id text primary key,
    episode_id text not null references betago.evaluation_episodes(id) on delete cascade,
    target_lane text not null,
    target_message_id text not null default '',
    feedback_event_id text not null,
    feedback_type text not null,
    explicitness text not null,
    content_json jsonb not null default '{}'::jsonb,
    attribution_confidence integer not null,
    occurred_at timestamptz not null,
    created_at timestamptz not null default now(),
    unique (episode_id, feedback_event_id)
);

create table if not exists betago.evaluation_judgments (
    id text primary key,
    episode_id text not null references betago.evaluation_episodes(id) on delete cascade,
    version bigint not null,
    source text not null,
    evaluator_id text not null,
    winner text not null,
    scores_json jsonb not null default '{}'::jsonb,
    problem_tags_json jsonb not null default '[]'::jsonb,
    rationale text not null default '',
    confidence integer not null default 0,
    needs_review boolean not null default false,
    supersedes_id text not null default '',
    created_at timestamptz not null default now(),
    unique (episode_id, source, version)
);

create index if not exists idx_eval_cohort_time
    on betago.evaluation_cohorts (start_at, end_at);

create index if not exists idx_eval_episode_filter
    on betago.evaluation_episodes (cohort_id, chat_id, anchor_at desc);

create index if not exists idx_eval_episode_status
    on betago.evaluation_episodes (status, post_window_end);

create index if not exists idx_eval_episode_message_timeline
    on betago.evaluation_episode_messages (episode_id, position, occurred_at, event_id);

create index if not exists idx_eval_candidate_task_claim
    on betago.evaluation_candidate_tasks (next_attempt_at, id)
    where status = 'queued';

create index if not exists idx_eval_candidate_task_reclaim
    on betago.evaluation_candidate_tasks (lease_expires_at, id)
    where status = 'running';

create index if not exists idx_eval_feedback_message
    on betago.evaluation_feedback (target_message_id, occurred_at);

create index if not exists idx_eval_judgment_episode
    on betago.evaluation_judgments (episode_id, created_at desc);

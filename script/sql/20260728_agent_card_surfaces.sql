-- Agent Card surface lifecycle.
--
-- IMPORTANT:
-- 1. This file contains CREATE INDEX CONCURRENTLY statements.
-- 2. Do not execute the whole file inside BEGIN/COMMIT or another transaction.
-- 3. The callback token is never stored here in plaintext. Its hash and the
--    trusted action descriptor remain on the durable interaction wait step.

create schema if not exists betago;

create table if not exists betago.agent_card_surfaces (
    id text primary key,
    run_id text not null
        references betago.agent_runs(id) on delete cascade,
    wait_step_id text not null
        references betago.agent_steps(id) on delete cascade,
    interaction_id text not null,
    chat_id text not null,
    reply_to_message_id text not null default '',
    message_id text not null default '',
    spec_version text not null,
    spec_json jsonb not null,
    compiled_json_redacted jsonb not null,
    status text not null,
    revision bigint not null,
    expected_actor_open_id text not null default '',
    interaction_kind text not null,
    expires_at timestamptz not null,
    submitted_at timestamptz null,
    processing_at timestamptz null,
    resolved_at timestamptz null,
    cancelled_at timestamptz null,
    failed_at timestamptz null,
    last_action_id text not null default '',
    last_source_ref text not null default '',
    patch_status text not null default 'idle',
    patch_attempt_count integer not null default 0,
    next_patch_at timestamptz not null default now(),
    patch_worker_id text not null default '',
    patch_lease_expires_at timestamptz null,
    last_error text not null default '',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint agent_card_surfaces_run_interaction_unique
        unique (run_id, interaction_id),
    constraint agent_card_surfaces_wait_step_unique
        unique (wait_step_id),
    constraint agent_card_surfaces_revision_positive
        check (revision > 0),
    constraint agent_card_surfaces_status_valid
        check (
            status in (
                'draft',
                'sent',
                'submitted',
                'processing',
                'resolved',
                'cancelled',
                'expired',
                'failed'
            )
        ),
    constraint agent_card_surfaces_patch_status_valid
        check (patch_status in ('idle', 'pending', 'running', 'failed')),
    constraint agent_card_surfaces_patch_attempt_count_nonnegative
        check (patch_attempt_count >= 0)
);

-- Each statement below must run outside an explicit transaction block.

create unique index concurrently if not exists idx_agent_card_surfaces_message
    on betago.agent_card_surfaces (message_id)
    where message_id <> '';

create index concurrently if not exists idx_agent_card_surfaces_expiry
    on betago.agent_card_surfaces (status, expires_at)
    where status in ('sent', 'submitted', 'processing');

create index concurrently if not exists idx_agent_card_surfaces_patch_claim
    on betago.agent_card_surfaces
       (patch_status, next_patch_at, patch_lease_expires_at)
    where patch_status in ('pending', 'running');

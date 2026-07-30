alter table if exists betago.agent_sessions
    add column if not exists tenant_id text;

alter table if exists betago.agent_runs
    add column if not exists tenant_id text;

alter table if exists betago.agent_steps
    add column if not exists tenant_id text;

alter table if exists betago.agent_capability_executions
    add column if not exists tenant_id text;

alter table if exists betago.agent_projection_outbox
    add column if not exists tenant_id text;

alter table if exists betago.agent_card_surfaces
    add column if not exists tenant_id text;

alter table if exists betago.evaluation_cohorts
    add column if not exists tenant_id text;

alter table if exists betago.evaluation_episodes
    add column if not exists tenant_id text;

alter table if exists betago.evaluation_episode_messages
    add column if not exists tenant_id text;

alter table if exists betago.evaluation_candidate_tasks
    add column if not exists tenant_id text;

alter table if exists betago.evaluation_lane_outputs
    add column if not exists tenant_id text;

alter table if exists betago.evaluation_feedback
    add column if not exists tenant_id text;

alter table if exists betago.evaluation_judgments
    add column if not exists tenant_id text;

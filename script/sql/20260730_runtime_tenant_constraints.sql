alter table betago.agent_sessions alter column tenant_id set not null;
alter table betago.agent_runs alter column tenant_id set not null;
alter table betago.agent_steps alter column tenant_id set not null;
alter table betago.agent_capability_executions alter column tenant_id set not null;
alter table betago.agent_projection_outbox alter column tenant_id set not null;
alter table betago.agent_card_surfaces alter column tenant_id set not null;
alter table betago.evaluation_cohorts alter column tenant_id set not null;
alter table betago.evaluation_episodes alter column tenant_id set not null;
alter table betago.evaluation_episode_messages alter column tenant_id set not null;
alter table betago.evaluation_candidate_tasks alter column tenant_id set not null;
alter table betago.evaluation_lane_outputs alter column tenant_id set not null;
alter table betago.evaluation_feedback alter column tenant_id set not null;
alter table betago.evaluation_judgments alter column tenant_id set not null;

create unique index if not exists idx_agent_sessions_tenant_id
    on betago.agent_sessions (tenant_id, id);
create unique index if not exists idx_agent_runs_tenant_id
    on betago.agent_runs (tenant_id, id);
create unique index if not exists idx_agent_steps_tenant_id
    on betago.agent_steps (tenant_id, id);
create unique index if not exists idx_eval_cohorts_tenant_id
    on betago.evaluation_cohorts (tenant_id, id);
create unique index if not exists idx_eval_episodes_tenant_id
    on betago.evaluation_episodes (tenant_id, id);

create unique index if not exists idx_agent_sessions_tenant_scope
    on betago.agent_sessions (tenant_id, scope_type, scope_id);
create unique index if not exists idx_agent_runs_tenant_trigger
    on betago.agent_runs (tenant_id, session_id, trigger_message_id);
create unique index if not exists idx_agent_steps_tenant_index
    on betago.agent_steps (tenant_id, run_id, index);
create unique index if not exists idx_agent_steps_tenant_dedupe
    on betago.agent_steps (tenant_id, run_id, dedupe_key)
    where dedupe_key <> '';
create unique index if not exists idx_agent_capability_tenant_idempotency
    on betago.agent_capability_executions (tenant_id, idempotency_key);
create unique index if not exists idx_agent_outbox_tenant_step
    on betago.agent_projection_outbox (tenant_id, step_id);
create unique index if not exists idx_agent_card_tenant_interaction
    on betago.agent_card_surfaces (tenant_id, run_id, interaction_id);
create unique index if not exists idx_agent_card_tenant_wait_step
    on betago.agent_card_surfaces (tenant_id, wait_step_id);
create unique index if not exists idx_agent_card_tenant_message
    on betago.agent_card_surfaces (tenant_id, message_id)
    where message_id <> '';

create unique index if not exists idx_eval_episode_tenant_anchor
    on betago.evaluation_episodes (tenant_id, cohort_id, anchor_event_id);
create unique index if not exists idx_eval_message_tenant_event
    on betago.evaluation_episode_messages (tenant_id, episode_id, position, event_id);
create unique index if not exists idx_eval_task_tenant_episode
    on betago.evaluation_candidate_tasks (tenant_id, episode_id);
create unique index if not exists idx_eval_lane_tenant_episode
    on betago.evaluation_lane_outputs (tenant_id, episode_id, lane);
create unique index if not exists idx_eval_feedback_tenant_event
    on betago.evaluation_feedback (tenant_id, episode_id, feedback_event_id);
create unique index if not exists idx_eval_judgment_tenant_version
    on betago.evaluation_judgments (tenant_id, episode_id, source, version);

do $constraints$
begin
    if not exists (
        select 1 from pg_constraint
        where conname = 'agent_runs_tenant_session_fk'
          and conrelid = 'betago.agent_runs'::regclass
    ) then
        alter table betago.agent_runs
            add constraint agent_runs_tenant_session_fk
            foreign key (tenant_id, session_id)
            references betago.agent_sessions (tenant_id, id)
            on delete cascade;
    end if;

    if not exists (
        select 1 from pg_constraint
        where conname = 'agent_steps_tenant_run_fk'
          and conrelid = 'betago.agent_steps'::regclass
    ) then
        alter table betago.agent_steps
            add constraint agent_steps_tenant_run_fk
            foreign key (tenant_id, run_id)
            references betago.agent_runs (tenant_id, id)
            on delete cascade;
    end if;

    if not exists (
        select 1 from pg_constraint
        where conname = 'agent_capability_tenant_run_fk'
          and conrelid = 'betago.agent_capability_executions'::regclass
    ) then
        alter table betago.agent_capability_executions
            add constraint agent_capability_tenant_run_fk
            foreign key (tenant_id, run_id)
            references betago.agent_runs (tenant_id, id)
            on delete cascade;
    end if;

    if not exists (
        select 1 from pg_constraint
        where conname = 'agent_capability_tenant_step_fk'
          and conrelid = 'betago.agent_capability_executions'::regclass
    ) then
        alter table betago.agent_capability_executions
            add constraint agent_capability_tenant_step_fk
            foreign key (tenant_id, step_id)
            references betago.agent_steps (tenant_id, id)
            on delete cascade;
    end if;

    if not exists (
        select 1 from pg_constraint
        where conname = 'agent_outbox_tenant_step_fk'
          and conrelid = 'betago.agent_projection_outbox'::regclass
    ) then
        alter table betago.agent_projection_outbox
            add constraint agent_outbox_tenant_step_fk
            foreign key (tenant_id, step_id)
            references betago.agent_steps (tenant_id, id)
            on delete cascade;
    end if;

    if not exists (
        select 1 from pg_constraint
        where conname = 'agent_card_tenant_run_fk'
          and conrelid = 'betago.agent_card_surfaces'::regclass
    ) then
        alter table betago.agent_card_surfaces
            add constraint agent_card_tenant_run_fk
            foreign key (tenant_id, run_id)
            references betago.agent_runs (tenant_id, id)
            on delete cascade;
    end if;

    if not exists (
        select 1 from pg_constraint
        where conname = 'agent_card_tenant_step_fk'
          and conrelid = 'betago.agent_card_surfaces'::regclass
    ) then
        alter table betago.agent_card_surfaces
            add constraint agent_card_tenant_step_fk
            foreign key (tenant_id, wait_step_id)
            references betago.agent_steps (tenant_id, id)
            on delete cascade;
    end if;

    if not exists (
        select 1 from pg_constraint
        where conname = 'evaluation_episode_tenant_cohort_fk'
          and conrelid = 'betago.evaluation_episodes'::regclass
    ) then
        alter table betago.evaluation_episodes
            add constraint evaluation_episode_tenant_cohort_fk
            foreign key (tenant_id, cohort_id)
            references betago.evaluation_cohorts (tenant_id, id)
            on delete cascade;
    end if;

    if not exists (
        select 1 from pg_constraint
        where conname = 'evaluation_message_tenant_episode_fk'
          and conrelid = 'betago.evaluation_episode_messages'::regclass
    ) then
        alter table betago.evaluation_episode_messages
            add constraint evaluation_message_tenant_episode_fk
            foreign key (tenant_id, episode_id)
            references betago.evaluation_episodes (tenant_id, id)
            on delete cascade;
    end if;

    if not exists (
        select 1 from pg_constraint
        where conname = 'evaluation_task_tenant_episode_fk'
          and conrelid = 'betago.evaluation_candidate_tasks'::regclass
    ) then
        alter table betago.evaluation_candidate_tasks
            add constraint evaluation_task_tenant_episode_fk
            foreign key (tenant_id, episode_id)
            references betago.evaluation_episodes (tenant_id, id)
            on delete cascade;
    end if;

    if not exists (
        select 1 from pg_constraint
        where conname = 'evaluation_lane_tenant_episode_fk'
          and conrelid = 'betago.evaluation_lane_outputs'::regclass
    ) then
        alter table betago.evaluation_lane_outputs
            add constraint evaluation_lane_tenant_episode_fk
            foreign key (tenant_id, episode_id)
            references betago.evaluation_episodes (tenant_id, id)
            on delete cascade;
    end if;

    if not exists (
        select 1 from pg_constraint
        where conname = 'evaluation_feedback_tenant_episode_fk'
          and conrelid = 'betago.evaluation_feedback'::regclass
    ) then
        alter table betago.evaluation_feedback
            add constraint evaluation_feedback_tenant_episode_fk
            foreign key (tenant_id, episode_id)
            references betago.evaluation_episodes (tenant_id, id)
            on delete cascade;
    end if;

    if not exists (
        select 1 from pg_constraint
        where conname = 'evaluation_judgment_tenant_episode_fk'
          and conrelid = 'betago.evaluation_judgments'::regclass
    ) then
        alter table betago.evaluation_judgments
            add constraint evaluation_judgment_tenant_episode_fk
            foreign key (tenant_id, episode_id)
            references betago.evaluation_episodes (tenant_id, id)
            on delete cascade;
    end if;
end
$constraints$;

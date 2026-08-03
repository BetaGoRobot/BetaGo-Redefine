create table if not exists betago.llm_token_usage_records (
    id bigserial primary key,
    created_at timestamptz not null default now(),
    bucket_minute timestamptz not null,
    bucket_hour timestamptz not null,
    bucket_day timestamptz not null,
    provider text not null,
    model text not null,
    kind text not null,
    source_type text not null,
    source text not null,
    chat_id text not null default '',
    chat_name text not null default '',
    open_id text not null default '',
    user_name text not null default '',
    status text not null,
    prompt_tokens bigint not null default 0,
    completion_tokens bigint not null default 0,
    total_tokens bigint not null default 0,
    response_id text not null default '',
    trace_id text not null default '',
    error text not null default '',
    bot_id text not null default '',
    business_scene text not null default 'unknown',
    business_operation text not null default 'unknown',
    attribution_mode text not null default 'unknown',
    tool_call_count bigint not null default 0,
    tool_success_count bigint not null default 0,
    tool_error_count bigint not null default 0
);

alter table betago.llm_token_usage_records
    add column if not exists business_scene text not null default 'unknown',
    add column if not exists business_operation text not null default 'unknown',
    add column if not exists attribution_mode text not null default 'unknown',
    add column if not exists tool_call_count bigint not null default 0,
    add column if not exists tool_success_count bigint not null default 0,
    add column if not exists tool_error_count bigint not null default 0;

create index if not exists idx_llm_usage_business_scene
    on betago.llm_token_usage_records (bot_id, chat_id, created_at, business_scene);

create index if not exists idx_llm_usage_business_operation
    on betago.llm_token_usage_records (bot_id, chat_id, created_at, business_operation);

create table if not exists betago.llm_tool_call_records (
    id bigserial primary key,
    usage_record_id bigint not null references betago.llm_token_usage_records(id) on delete cascade,
    bot_id text not null default '',
    chat_id text not null default '',
    business_scene text not null,
    business_operation text not null,
    tool_name text not null,
    status text not null,
    duration_ms bigint not null default 0,
    error_kind text not null default '',
    trace_id text not null default '',
    called_at timestamptz not null,
    created_at timestamptz not null default now()
);

create index if not exists idx_llm_tool_call_chat_time
    on betago.llm_tool_call_records (bot_id, chat_id, called_at);

create index if not exists idx_llm_tool_call_name_time
    on betago.llm_tool_call_records (bot_id, chat_id, tool_name, called_at);

create index if not exists idx_llm_tool_call_usage
    on betago.llm_tool_call_records (usage_record_id);

update betago.llm_token_usage_records
set business_scene = case source
        when 'chat' then 'conversation'
        when 'intent' then 'routing'
        when 'history_search' then 'retrieval'
        when 'topic_recall' then 'retrieval'
        when 'retriever_embedding' then 'retrieval'
        when 'retriever_recall' then 'retrieval'
        when 'retriever_answer' then 'retrieval'
        when 'message_recording' then 'background'
        when 'outbound_message_recording' then 'background'
        when 'chunking' then 'background'
        when 'chunking_embedding' then 'background'
        when 'reindex_embeddings' then 'background'
        when 'conversation_evaluation_candidate' then 'evaluation'
        when 'agent_callback_continuation' then 'agent_runtime'
        when 'debug_image' then 'debug'
        else business_scene
    end,
    business_operation = case source
        when 'chat' then 'chat_reply'
        when 'intent' then 'intent_recognition'
        when 'history_search' then 'history_search'
        when 'topic_recall' then 'topic_recall'
        when 'retriever_embedding' then 'retriever_embedding'
        when 'retriever_recall' then 'retriever_recall'
        when 'retriever_answer' then 'retriever_answer'
        when 'message_recording' then 'message_embedding'
        when 'outbound_message_recording' then 'outbound_embedding'
        when 'chunking' then 'chunk_merge'
        when 'chunking_embedding' then 'chunk_embedding'
        when 'reindex_embeddings' then 'reindex_embedding'
        when 'conversation_evaluation_candidate' then 'candidate_generation'
        when 'agent_callback_continuation' then 'callback_continuation'
        when 'debug_image' then 'debug_image'
        else business_operation
    end,
    attribution_mode = case
        when source in (
            'chat', 'intent', 'history_search', 'topic_recall',
            'retriever_embedding', 'retriever_recall', 'retriever_answer',
            'message_recording', 'outbound_message_recording', 'chunking',
            'chunking_embedding', 'reindex_embeddings',
            'conversation_evaluation_candidate', 'agent_callback_continuation',
            'debug_image'
        ) then 'legacy_mapping'
        else attribution_mode
    end
where attribution_mode = 'unknown';

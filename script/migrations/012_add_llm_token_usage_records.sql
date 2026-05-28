CREATE TABLE IF NOT EXISTS llm_token_usage_records (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    bucket_minute TIMESTAMPTZ NOT NULL,
    bucket_hour TIMESTAMPTZ NOT NULL,
    bucket_day TIMESTAMPTZ NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    kind TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    chat_id TEXT NOT NULL DEFAULT '',
    chat_name TEXT NOT NULL DEFAULT '',
    open_id TEXT NOT NULL DEFAULT '',
    user_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    response_id TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_llm_token_usage_bucket_minute_chat
    ON llm_token_usage_records (bucket_minute, chat_id);

CREATE INDEX IF NOT EXISTS idx_llm_token_usage_bucket_hour_chat
    ON llm_token_usage_records (bucket_hour, chat_id);

CREATE INDEX IF NOT EXISTS idx_llm_token_usage_bucket_day_chat
    ON llm_token_usage_records (bucket_day, chat_id);

CREATE INDEX IF NOT EXISTS idx_llm_token_usage_bucket_day_open_id
    ON llm_token_usage_records (bucket_day, open_id);

CREATE INDEX IF NOT EXISTS idx_llm_token_usage_created_at
    ON llm_token_usage_records (created_at);

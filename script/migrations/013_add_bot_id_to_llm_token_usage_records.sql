-- Add bot_id column to betago.llm_token_usage_records so per-bot aggregations are
-- accurate when multiple bot processes share the same database.
--
-- New rows always carry the running bot's identity (Lark AppID:BotOpenID,
-- see internal/application/lark/botidentity). Historical rows default to ''
-- and need a one-shot backfill via cmd/tools/backfill_token_bot_id.

ALTER TABLE betago.llm_token_usage_records
    ADD COLUMN IF NOT EXISTS bot_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_llm_token_usage_bot_chat_time
    ON betago.llm_token_usage_records (bot_id, chat_id, created_at);

CREATE INDEX IF NOT EXISTS idx_llm_token_usage_bot_bucket_day
    ON betago.llm_token_usage_records (bot_id, bucket_day);

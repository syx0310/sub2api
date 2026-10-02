-- Keep execution purpose separate from transport and actual token usage.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS prewarm BOOLEAN NOT NULL DEFAULT FALSE;

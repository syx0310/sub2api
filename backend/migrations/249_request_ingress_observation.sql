-- Optional diagnostic observations only: no rewrite/backfill of usage history.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS request_ingress JSONB;
ALTER TABLE ops_error_logs ADD COLUMN IF NOT EXISTS request_ingress JSONB;
-- Legacy codex_turn_state_*_bytes columns intentionally remain for rollback.

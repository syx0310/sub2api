-- Passive diagnostics only: no token contents, indexes, defaults or backfill.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS codex_turn_state_request_header_bytes INTEGER,
    ADD COLUMN IF NOT EXISTS codex_turn_state_request_metadata_bytes INTEGER,
    ADD COLUMN IF NOT EXISTS codex_turn_state_response_header_bytes INTEGER,
    ADD COLUMN IF NOT EXISTS codex_turn_state_response_metadata_bytes INTEGER;

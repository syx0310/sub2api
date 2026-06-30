-- Track logical request/response body payload sizes for usage diagnostics.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS request_body_bytes BIGINT,
    ADD COLUMN IF NOT EXISTS response_body_bytes BIGINT;

-- This fork defaults OpenAI OAuth Codex fingerprint convergence to "device".
-- Upstream migration 225 only seeds explicitly enabled modes, so accounts with
-- no mode (or an invalid legacy value) would otherwise resolve to device while
-- lacking the system-managed seed required to apply it.
--
-- Keep upstream migration 225 immutable and fill the fork-specific default in a
-- follow-up migration. Explicit "off" remains the only opt-out. Idempotent:
-- valid canonical non-nil UUID seeds are preserved on rerun.
UPDATE accounts
SET extra = jsonb_set(
    COALESCE(extra, '{}'::jsonb),
    '{codex_fingerprint_seed}',
    to_jsonb(gen_random_uuid()::text),
    true
)
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND type = 'oauth'
  AND COALESCE(extra->>'codex_fingerprint_mode', '') <> 'off'
  AND (
      extra->>'codex_fingerprint_seed' IS NULL
      OR btrim(extra->>'codex_fingerprint_seed') = ''
      OR NOT (
          extra->>'codex_fingerprint_seed' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
          AND extra->>'codex_fingerprint_seed' <> '00000000-0000-0000-0000-000000000000'
      )
  );

-- Persist whether the effective billing multiplier for this request came from a
-- dynamic rate expression ($up-based) rather than a static multiplier.
--
-- Nullable with no default: on PostgreSQL 11+ this is a metadata-only change, so
-- it does NOT rewrite the (potentially large) usage_logs table. Existing rows stay
-- NULL because their dynamic-ness is unknown: it must never be inferred from the
-- current (mutable) group configuration, which may have changed since the row was
-- written. Newly billed rows record true/false explicitly.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS is_dynamic_rate BOOLEAN;

COMMENT ON COLUMN usage_logs.is_dynamic_rate IS
    'Whether the effective rate multiplier was produced by a dynamic rate expression. NULL = historical row, unknown.';
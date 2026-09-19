-- Custom patch: dynamic group rate multiplier expression.
-- Kept outside Ent schema on purpose so the patch remains small and easy to rebase.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS rate_multiplier_expr TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN groups.rate_multiplier_expr IS
    'Optional arithmetic expression for the effective user billing multiplier. $up is the selected upstream/account rate multiplier.';

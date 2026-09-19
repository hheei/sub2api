package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageLogDynamicRateMigration(t *testing.T) {
	content, err := FS.ReadFile("241_usage_log_dynamic_rate.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	// Nullable with no default: existing rows must stay NULL (unknown) instead of
	// being backfilled from the current, mutable group configuration.
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS is_dynamic_rate BOOLEAN")
	require.NotContains(t, sql, "is_dynamic_rate BOOLEAN NOT NULL")
	require.NotContains(t, sql, "DEFAULT FALSE")
	require.Contains(t, sql, "COMMENT ON COLUMN usage_logs.is_dynamic_rate")
}

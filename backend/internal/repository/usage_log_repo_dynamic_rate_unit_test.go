//go:build unit

package repository

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newDynamicRateUsageLog(isDynamicRate *bool) *service.UsageLog {
	return &service.UsageLog{
		UserID:         1,
		APIKeyID:       2,
		AccountID:      3,
		RequestID:      "req-is-dynamic-rate",
		Model:          "claude-3",
		InputTokens:    10,
		OutputTokens:   5,
		RateMultiplier: 0.63,
		IsDynamicRate:  isDynamicRate,
		CreatedAt:      time.Now().UTC(),
	}
}

// TestPrepareUsageLogInsert_IsDynamicRateArgWiring pins is_dynamic_rate to the arg
// slice / arg-type table so the five INSERT column lists stay in sync, and pins it
// between session_id and native_compaction_v2.
func TestPrepareUsageLogInsert_IsDynamicRateArgWiring(t *testing.T) {
	flagTrue := true
	flagFalse := false

	prepared := prepareUsageLogInsert(newDynamicRateUsageLog(&flagTrue))
	require.Len(t, prepared.args, len(usageLogInsertArgTypes))

	idx := len(prepared.args) - 3
	require.Equal(t, "boolean", usageLogInsertArgTypes[idx], "is_dynamic_rate arg type must be boolean")
	arg, ok := prepared.args[idx].(sql.NullBool)
	require.True(t, ok, "is_dynamic_rate arg should be sql.NullBool, got %T", prepared.args[idx])
	require.True(t, arg.Valid)
	require.True(t, arg.Bool)

	// Explicit false must be persisted as a value, never as NULL: the column is
	// tri-state and false is a real observation.
	falsePrepared := prepareUsageLogInsert(newDynamicRateUsageLog(&flagFalse))
	falseArg, ok := falsePrepared.args[idx].(sql.NullBool)
	require.True(t, ok)
	require.True(t, falseArg.Valid, "explicit false must not be persisted as NULL")
	require.False(t, falseArg.Bool)

	// Absent value (historical/manual rows) stays NULL rather than defaulting to false.
	absentPrepared := prepareUsageLogInsert(newDynamicRateUsageLog(nil))
	absentArg, ok := absentPrepared.args[idx].(sql.NullBool)
	require.True(t, ok)
	require.False(t, absentArg.Valid, "unknown is_dynamic_rate must be NULL")
}

// TestUsageLogInsertQueries_IncludeIsDynamicRate guards that every generated INSERT
// path and the SELECT column list reference is_dynamic_rate.
func TestUsageLogInsertQueries_IncludeIsDynamicRate(t *testing.T) {
	require.Contains(t, usageLogSelectColumns, "is_dynamic_rate",
		"SELECT column list must include is_dynamic_rate")

	log := newDynamicRateUsageLog(nil)
	prepared := prepareUsageLogInsert(log)
	key := usageLogBatchKey(log.RequestID, log.APIKeyID)

	batchQuery, batchArgs := buildUsageLogBatchInsertQuery([]string{key},
		map[string]usageLogInsertPrepared{key: prepared})
	require.Contains(t, batchQuery, "is_dynamic_rate")
	// CTE input column list + INSERT column list + SELECT ... FROM input column list.
	require.GreaterOrEqual(t, strings.Count(batchQuery, "is_dynamic_rate"), 3)
	require.Len(t, batchArgs, len(prepared.args)+1,
		"batch args include the synthetic input_index before usage-log values")

	bestEffortQuery, bestEffortArgs := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
	require.Contains(t, bestEffortQuery, "is_dynamic_rate")
	require.GreaterOrEqual(t, strings.Count(bestEffortQuery, "is_dynamic_rate"), 3)
	require.Len(t, bestEffortArgs, len(prepared.args))
}

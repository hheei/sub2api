package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 用户专属倍率的动态表达式列必须可空：NULL 表示该行没有倍率覆盖，
// 这样仅 rpm_override 的行不会被当成"有倍率"，仅表达式的行也无需伪造数值。
func TestUserGroupRateMultiplierExprMigration(t *testing.T) {
	content, err := FS.ReadFile("240_user_group_rate_multiplier_expr.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE user_group_rate_multipliers ADD COLUMN IF NOT EXISTS rate_multiplier_expr TEXT NULL")
	require.Contains(t, sql, "COMMENT ON COLUMN user_group_rate_multipliers.rate_multiplier_expr")
}

// 批量生图作业冻结自己提交时的动态倍率标记；结算阶段没有其它可信来源。
func TestBatchImageJobsRateIsDynamicMigration(t *testing.T) {
	content, err := FS.ReadFile("242_batch_image_jobs_rate_is_dynamic.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE batch_image_jobs ADD COLUMN IF NOT EXISTS is_dynamic_rate BOOLEAN NOT NULL DEFAULT false")
	require.Contains(t, sql, "COMMENT ON COLUMN batch_image_jobs.is_dynamic_rate")
}

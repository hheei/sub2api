package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newUserGroupRateRepoMock(t *testing.T) (*userGroupRateRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := newSQLMock(t)
	return &userGroupRateRepository{sql: db}, mock
}

// 表达式必须落库：插入语句写入 rate_multiplier_expr 列，且 upsert 同时更新两列。
func TestUserGroupRateRepo_SyncUserGroupRates_PersistsExpression(t *testing.T) {
	repo, mock := newUserGroupRateRepoMock(t)

	mock.ExpectExec(`(?s)INSERT INTO user_group_rate_multipliers .*rate_multiplier_expr.*unnest\(\$3::bigint\[\], \$4::double precision\[\], \$5::text\[\]\).*DO UPDATE SET rate_multiplier = EXCLUDED\.rate_multiplier, rate_multiplier_expr = EXCLUDED\.rate_multiplier_expr`).
		WithArgs(int64(7), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.SyncUserGroupRates(context.Background(), 7, map[int64]*service.UserGroupRate{
		11: {RateMultiplier: 0, RateMultiplierExpr: "  $up * 1.05  "},
		12: {RateMultiplier: 1.5},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 整用户清空：两列同时归 NULL，且整行删除只针对倍率部分与 rpm 皆空的行
// （仅 rpm_override 的行必须保留）。
func TestUserGroupRateRepo_SyncUserGroupRates_EmptyMapKeepsRPMOnlyRows(t *testing.T) {
	repo, mock := newUserGroupRateRepoMock(t)

	mock.ExpectExec(regexp.QuoteMeta(`
			UPDATE user_group_rate_multipliers
			SET rate_multiplier = NULL, rate_multiplier_expr = NULL, updated_at = NOW()
			WHERE user_id = $1
		`)).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 2))

	mock.ExpectExec(`(?s)DELETE FROM user_group_rate_multipliers WHERE user_id = \$1 AND \(rate_multiplier IS NULL AND COALESCE\(rate_multiplier_expr, ''\) = ''\) AND rpm_override IS NULL`).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	require.NoError(t, repo.SyncUserGroupRates(context.Background(), 7, nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 单分组清空（nil 值）：表达式一并清空，绝不只清数值。
func TestUserGroupRateRepo_SyncUserGroupRates_NilValueClearsExpression(t *testing.T) {
	repo, mock := newUserGroupRateRepoMock(t)

	mock.ExpectExec(`(?s)UPDATE user_group_rate_multipliers SET rate_multiplier = NULL, rate_multiplier_expr = NULL, updated_at = NOW\(\) WHERE user_id = \$1 AND group_id = ANY\(\$2\)`).
		WithArgs(int64(7), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec(`(?s)DELETE FROM user_group_rate_multipliers WHERE user_id = \$1 AND group_id = ANY\(\$2\) AND .*rate_multiplier_expr.*rpm_override IS NULL`).
		WithArgs(int64(7), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	require.NoError(t, repo.SyncUserGroupRates(context.Background(), 7, map[int64]*service.UserGroupRate{
		11: nil,
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 批量替换分组倍率：未列入的用户两列都清空；删除只清理"倍率部分 + rpm 皆空"的行。
func TestUserGroupRateRepo_SyncGroupRateMultipliers_ReplacesExpressionToo(t *testing.T) {
	repo, mock := newUserGroupRateRepoMock(t)

	mock.ExpectExec(`(?s)UPDATE user_group_rate_multipliers SET rate_multiplier = NULL, rate_multiplier_expr = NULL, updated_at = NOW\(\) WHERE group_id = \$1 AND user_id <> ALL\(\$2\)`).
		WithArgs(int64(10), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec(`(?s)DELETE FROM user_group_rate_multipliers WHERE group_id = \$1 AND \(rate_multiplier IS NULL AND COALESCE\(rate_multiplier_expr, ''\) = ''\) AND rpm_override IS NULL`).
		WithArgs(int64(10)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	mock.ExpectExec(`(?s)INSERT INTO user_group_rate_multipliers .*rate_multiplier_expr.*ON CONFLICT \(user_id, group_id\) DO UPDATE SET rate_multiplier = EXCLUDED\.rate_multiplier, rate_multiplier_expr = EXCLUDED\.rate_multiplier_expr`).
		WithArgs(int64(10), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.SyncGroupRateMultipliers(context.Background(), 10, []service.GroupRateMultiplierInput{
		{UserID: 1, RateMultiplier: 1, RateMultiplierExpr: "$up + 0.2"},
		{UserID: 2, RateMultiplier: 0.5},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// RPM 部分同步/清空不得误删仅表达式覆盖的行：删除语句必须判定表达式列。
func TestUserGroupRateRepo_RPMClearingKeepsExpressionOnlyRows(t *testing.T) {
	repo, mock := newUserGroupRateRepoMock(t)
	ctx := context.Background()

	// 显式 clear + 未列入清空 + 删除保护 + upsert。
	mock.ExpectExec(`(?s)UPDATE user_group_rate_multipliers SET rpm_override = NULL, updated_at = NOW\(\) WHERE group_id = \$1 AND user_id <> ALL\(\$2\)`).
		WithArgs(int64(10), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)UPDATE user_group_rate_multipliers SET rpm_override = NULL, updated_at = NOW\(\) WHERE group_id = \$1 AND user_id = ANY\(\$2\)`).
		WithArgs(int64(10), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)DELETE FROM user_group_rate_multipliers WHERE group_id = \$1 AND \(rate_multiplier IS NULL AND COALESCE\(rate_multiplier_expr, ''\) = ''\) AND rpm_override IS NULL`).
		WithArgs(int64(10)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`(?s)INSERT INTO user_group_rate_multipliers .*rpm_override.*DO UPDATE SET rpm_override = EXCLUDED\.rpm_override`).
		WithArgs(int64(10), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	override := 30
	require.NoError(t, repo.SyncGroupRPMOverrides(ctx, 10, []service.GroupRPMOverrideInput{
		{UserID: 1, RPMOverride: nil},
		{UserID: 2, RPMOverride: &override},
	}))
	require.NoError(t, mock.ExpectationsWereMet())

	repo, mock = newUserGroupRateRepoMock(t)
	mock.ExpectExec(`(?s)UPDATE user_group_rate_multipliers SET rpm_override = NULL, updated_at = NOW\(\) WHERE group_id = \$1`).
		WithArgs(int64(10)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(`(?s)DELETE FROM user_group_rate_multipliers WHERE group_id = \$1 AND \(rate_multiplier IS NULL AND COALESCE\(rate_multiplier_expr, ''\) = ''\) AND rpm_override IS NULL`).
		WithArgs(int64(10)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	require.NoError(t, repo.ClearGroupRPMOverrides(ctx, 10))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 读取路径：仅表达式覆盖的行算作有倍率覆盖（数值回退为 0），仅 rpm 的行不算。
func TestUserGroupRateRepo_GetByUserIDAndGroup(t *testing.T) {
	repo, mock := newUserGroupRateRepoMock(t)

	mock.ExpectQuery(`(?s)SELECT group_id, rate_multiplier, rate_multiplier_expr FROM user_group_rate_multipliers WHERE user_id = \$1 AND \(rate_multiplier IS NOT NULL OR COALESCE\(rate_multiplier_expr, ''\) <> ''\)`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "rate_multiplier", "rate_multiplier_expr"}).
			AddRow(int64(11), 0.0, nil).
			AddRow(int64(12), nil, "$up * 2").
			AddRow(int64(13), 1.25, " $up + 0.1 "))

	rates, err := repo.GetByUserID(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, rates, 3)
	require.Equal(t, 0.0, rates[11].RateMultiplier)
	require.False(t, rates[11].IsDynamic())
	require.True(t, rates[12].IsDynamic())
	require.Zero(t, rates[12].RateMultiplier)
	require.Equal(t, "$up + 0.1", rates[13].RateMultiplierExpr)
	require.Equal(t, 1.25, rates[13].RateMultiplier)

	// 仅 rpm 的行：两列皆 NULL → nil（无倍率覆盖）
	mock.ExpectQuery(`(?s)SELECT rate_multiplier, rate_multiplier_expr FROM user_group_rate_multipliers WHERE user_id = \$1 AND group_id = \$2`).
		WithArgs(int64(7), int64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"rate_multiplier", "rate_multiplier_expr"}).AddRow(nil, nil))
	got, err := repo.GetByUserAndGroup(context.Background(), 7, 11)
	require.NoError(t, err)
	require.Nil(t, got)

	mock.ExpectQuery(`(?s)SELECT rate_multiplier, rate_multiplier_expr FROM user_group_rate_multipliers WHERE user_id = \$1 AND group_id = \$2`).
		WithArgs(int64(7), int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"rate_multiplier", "rate_multiplier_expr"}).AddRow(0.0, ""))
	got, err = repo.GetByUserAndGroup(context.Background(), 7, 12)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, 0.0, got.RateMultiplier)
	require.False(t, got.IsDynamic())

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserGroupRateRepo_GetByUserIDsAndGroupID(t *testing.T) {
	repo, mock := newUserGroupRateRepoMock(t)
	ctx := context.Background()

	mock.ExpectQuery(`(?s)SELECT user_id, group_id, rate_multiplier, rate_multiplier_expr FROM user_group_rate_multipliers WHERE user_id = ANY\(\$1\) AND \(rate_multiplier IS NOT NULL OR COALESCE\(rate_multiplier_expr, ''\) <> ''\)`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "group_id", "rate_multiplier", "rate_multiplier_expr"}).
			AddRow(int64(7), int64(11), nil, "$up / 2"))

	rates, err := repo.GetByUserIDs(ctx, []int64{7})
	require.NoError(t, err)
	require.Equal(t, "$up / 2", rates[7][11].RateMultiplierExpr)

	mock.ExpectQuery(`(?s)SELECT ugr\.user_id, u\.username, .*ugr\.rate_multiplier_expr .*WHERE ugr\.group_id = \$1`).
		WithArgs(int64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "email", "notes", "status", "rate_multiplier", "rpm_override", "rate_multiplier_expr"}).
			AddRow(int64(1), "u1", "u1@test.com", "", "active", nil, 30, "$up * 3"))

	entries, err := repo.GetByGroupID(ctx, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Nil(t, entries[0].RateMultiplier)
	require.Equal(t, 30, *entries[0].RPMOverride)
	require.Equal(t, "$up * 3", entries[0].RateMultiplierExpr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNullableExprAndRateFromNullable(t *testing.T) {
	require.Nil(t, nullableExpr("   "))
	require.Equal(t, "$up", nullableExpr(" $up "))
	require.Nil(t, nullableExpr(""))

	// 两列皆 NULL = 无倍率覆盖；仅表达式 = 有覆盖（数值回退 0）。
	require.Nil(t, rateFromNullable(sql.NullFloat64{}, sql.NullString{}))

	exprOnly := rateFromNullable(sql.NullFloat64{}, sql.NullString{String: " $up ", Valid: true})
	require.NotNil(t, exprOnly)
	require.True(t, exprOnly.IsDynamic())
	require.Zero(t, exprOnly.RateMultiplier)

	// 仅数值（含 0）= 有覆盖。
	staticZero := rateFromNullable(sql.NullFloat64{Valid: true}, sql.NullString{})
	require.NotNil(t, staticZero)
	require.False(t, staticZero.IsDynamic())
	require.Zero(t, staticZero.RateMultiplier)
}

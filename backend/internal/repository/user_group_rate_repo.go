package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type userGroupRateRepository struct {
	sql sqlExecutor
}

// NewUserGroupRateRepository 创建用户专属分组倍率/RPM 仓储
func NewUserGroupRateRepository(sqlDB *sql.DB) service.UserGroupRateRepository {
	return &userGroupRateRepository{sql: sqlDB}
}

// rateOverridePredicate 判定一行是否含倍率覆盖（静态值或动态表达式）。
const rateOverridePredicate = `(rate_multiplier IS NOT NULL OR COALESCE(rate_multiplier_expr, '') <> '')`

// emptyRatePredicate 判定一行的倍率部分为空（可随 rpm 一起被清理）。
const emptyRatePredicate = `(rate_multiplier IS NULL AND COALESCE(rate_multiplier_expr, '') = '')`

// nullableExpr 空表达式写入 NULL，保持"未设置"与空字符串同一语义。
func nullableExpr(expr string) any {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}
	return expr
}

// rateFromNullable 由可空列还原倍率配置；无倍率覆盖时返回 nil。
func rateFromNullable(rate sql.NullFloat64, expr sql.NullString) *service.UserGroupRate {
	if !rate.Valid && strings.TrimSpace(expr.String) == "" {
		return nil
	}
	out := service.UserGroupRate{RateMultiplierExpr: strings.TrimSpace(expr.String)}
	if rate.Valid {
		out.RateMultiplier = rate.Float64
	}
	return &out
}

// GetByUserID 获取用户所有专属分组倍率（仅返回含倍率覆盖的条目）
func (r *userGroupRateRepository) GetByUserID(ctx context.Context, userID int64) (map[int64]service.UserGroupRate, error) {
	query := `SELECT group_id, rate_multiplier, rate_multiplier_expr FROM user_group_rate_multipliers
		WHERE user_id = $1 AND ` + rateOverridePredicate
	rows, err := r.sql.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64]service.UserGroupRate)
	for rows.Next() {
		var groupID int64
		var rate sql.NullFloat64
		var expr sql.NullString
		if err := rows.Scan(&groupID, &rate, &expr); err != nil {
			return nil, err
		}
		entry := service.UserGroupRate{RateMultiplierExpr: strings.TrimSpace(expr.String)}
		if rate.Valid {
			entry.RateMultiplier = rate.Float64
		}
		result[groupID] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// GetByUserIDs 批量获取多个用户的专属分组倍率（仅返回含倍率覆盖的条目）
func (r *userGroupRateRepository) GetByUserIDs(ctx context.Context, userIDs []int64) (map[int64]map[int64]service.UserGroupRate, error) {
	result := make(map[int64]map[int64]service.UserGroupRate, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}

	uniqueIDs := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		uniqueIDs = append(uniqueIDs, userID)
		result[userID] = make(map[int64]service.UserGroupRate)
	}
	if len(uniqueIDs) == 0 {
		return result, nil
	}

	rows, err := r.sql.QueryContext(ctx, `
		SELECT user_id, group_id, rate_multiplier, rate_multiplier_expr
		FROM user_group_rate_multipliers
		WHERE user_id = ANY($1) AND `+rateOverridePredicate, pq.Array(uniqueIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var userID int64
		var groupID int64
		var rate sql.NullFloat64
		var expr sql.NullString
		if err := rows.Scan(&userID, &groupID, &rate, &expr); err != nil {
			return nil, err
		}
		entry := service.UserGroupRate{RateMultiplierExpr: strings.TrimSpace(expr.String)}
		if rate.Valid {
			entry.RateMultiplier = rate.Float64
		}
		if _, ok := result[userID]; !ok {
			result[userID] = make(map[int64]service.UserGroupRate)
		}
		result[userID][groupID] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// GetByGroupID 获取指定分组下所有用户的专属配置（rate 与 rpm_override 任一非 NULL 即返回）
func (r *userGroupRateRepository) GetByGroupID(ctx context.Context, groupID int64) ([]service.UserGroupRateEntry, error) {
	query := `
		SELECT ugr.user_id, u.username, u.email, COALESCE(u.notes, ''), u.status, ugr.rate_multiplier, ugr.rpm_override, ugr.rate_multiplier_expr
		FROM user_group_rate_multipliers ugr
		JOIN users u ON u.id = ugr.user_id AND u.deleted_at IS NULL
		WHERE ugr.group_id = $1
		ORDER BY ugr.user_id
	`
	rows, err := r.sql.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []service.UserGroupRateEntry
	for rows.Next() {
		var entry service.UserGroupRateEntry
		var rate sql.NullFloat64
		var rpm sql.NullInt32
		var expr sql.NullString
		if err := rows.Scan(&entry.UserID, &entry.UserName, &entry.UserEmail, &entry.UserNotes, &entry.UserStatus, &rate, &rpm, &expr); err != nil {
			return nil, err
		}
		if rate.Valid {
			v := rate.Float64
			entry.RateMultiplier = &v
		}
		if rpm.Valid {
			v := int(rpm.Int32)
			entry.RPMOverride = &v
		}
		entry.RateMultiplierExpr = strings.TrimSpace(expr.String)
		result = append(result, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// GetByUserAndGroup 获取用户在特定分组的专属倍率（无倍率覆盖时返回 nil）
func (r *userGroupRateRepository) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*service.UserGroupRate, error) {
	query := `SELECT rate_multiplier, rate_multiplier_expr FROM user_group_rate_multipliers WHERE user_id = $1 AND group_id = $2`
	var rate sql.NullFloat64
	var expr sql.NullString
	err := scanSingleRow(ctx, r.sql, query, []any{userID, groupID}, &rate, &expr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rateFromNullable(rate, expr), nil
}

// GetRPMOverrideByUserAndGroup 获取用户在特定分组的 rpm_override（NULL 返回 nil）
func (r *userGroupRateRepository) GetRPMOverrideByUserAndGroup(ctx context.Context, userID, groupID int64) (*int, error) {
	query := `SELECT rpm_override FROM user_group_rate_multipliers WHERE user_id = $1 AND group_id = $2`
	var rpm sql.NullInt32
	err := scanSingleRow(ctx, r.sql, query, []any{userID, groupID}, &rpm)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !rpm.Valid {
		return nil, nil
	}
	v := int(rpm.Int32)
	return &v, nil
}

// SyncUserGroupRates 同步用户的分组专属倍率。
//   - 传入空 map：清空该用户所有行的倍率部分（数值与表达式）；若 rpm_override 也为 NULL 则整行删除。
//   - 值为 nil：清空对应行的倍率部分（保留 rpm_override）。
//   - 值非 nil：upsert 数值与表达式（保留已有 rpm_override）。
func (r *userGroupRateRepository) SyncUserGroupRates(ctx context.Context, userID int64, rates map[int64]*service.UserGroupRate) error {
	if len(rates) == 0 {
		if _, err := r.sql.ExecContext(ctx, `
			UPDATE user_group_rate_multipliers
			SET rate_multiplier = NULL, rate_multiplier_expr = NULL, updated_at = NOW()
			WHERE user_id = $1
		`, userID); err != nil {
			return err
		}
		_, err := r.sql.ExecContext(ctx,
			`DELETE FROM user_group_rate_multipliers WHERE user_id = $1 AND `+emptyRatePredicate+` AND rpm_override IS NULL`,
			userID)
		return err
	}

	var clearGroupIDs []int64
	upsertGroupIDs := make([]int64, 0, len(rates))
	upsertRates := make([]float64, 0, len(rates))
	upsertExprs := make([]any, 0, len(rates))
	for groupID, rate := range rates {
		if rate == nil {
			clearGroupIDs = append(clearGroupIDs, groupID)
		} else {
			normalized := rate.Normalize()
			upsertGroupIDs = append(upsertGroupIDs, groupID)
			upsertRates = append(upsertRates, normalized.RateMultiplier)
			upsertExprs = append(upsertExprs, nullableExpr(normalized.RateMultiplierExpr))
		}
	}

	if len(clearGroupIDs) > 0 {
		if _, err := r.sql.ExecContext(ctx, `
			UPDATE user_group_rate_multipliers
			SET rate_multiplier = NULL, rate_multiplier_expr = NULL, updated_at = NOW()
			WHERE user_id = $1 AND group_id = ANY($2)
		`, userID, pq.Array(clearGroupIDs)); err != nil {
			return err
		}
		if _, err := r.sql.ExecContext(ctx,
			`DELETE FROM user_group_rate_multipliers WHERE user_id = $1 AND group_id = ANY($2) AND `+emptyRatePredicate+` AND rpm_override IS NULL`,
			userID, pq.Array(clearGroupIDs)); err != nil {
			return err
		}
	}

	if len(upsertGroupIDs) > 0 {
		now := time.Now()
		_, err := r.sql.ExecContext(ctx, `
			INSERT INTO user_group_rate_multipliers (user_id, group_id, rate_multiplier, rate_multiplier_expr, created_at, updated_at)
			SELECT
				$1::bigint,
				data.group_id,
				data.rate_multiplier,
				data.rate_multiplier_expr,
				$2::timestamptz,
				$2::timestamptz
			FROM unnest($3::bigint[], $4::double precision[], $5::text[]) AS data(group_id, rate_multiplier, rate_multiplier_expr)
			ON CONFLICT (user_id, group_id)
			DO UPDATE SET
				rate_multiplier = EXCLUDED.rate_multiplier,
				rate_multiplier_expr = EXCLUDED.rate_multiplier_expr,
				updated_at = EXCLUDED.updated_at
		`, userID, now, pq.Array(upsertGroupIDs), pq.Array(upsertRates), pq.Array(upsertExprs))
		if err != nil {
			return err
		}
	}

	return nil
}

// SyncGroupRateMultipliers 同步分组的倍率部分（不触动 rpm_override）。
// 语义：
//   - 未出现在 entries 中的用户行：倍率部分归 NULL；若 rpm_override 也为 NULL 则整行删除。
//   - 出现的用户行：upsert 数值与表达式；表达式为空时回落为纯数值覆盖。
func (r *userGroupRateRepository) SyncGroupRateMultipliers(ctx context.Context, groupID int64, entries []service.GroupRateMultiplierInput) error {
	keepUserIDs := make([]int64, 0, len(entries))
	for _, e := range entries {
		keepUserIDs = append(keepUserIDs, e.UserID)
	}

	// 未在 entries 列表中的行：清空倍率部分（数值与表达式）。
	if len(keepUserIDs) == 0 {
		if _, err := r.sql.ExecContext(ctx, `
			UPDATE user_group_rate_multipliers
			SET rate_multiplier = NULL, rate_multiplier_expr = NULL, updated_at = NOW()
			WHERE group_id = $1
		`, groupID); err != nil {
			return err
		}
	} else {
		if _, err := r.sql.ExecContext(ctx, `
			UPDATE user_group_rate_multipliers
			SET rate_multiplier = NULL, rate_multiplier_expr = NULL, updated_at = NOW()
			WHERE group_id = $1 AND user_id <> ALL($2)
		`, groupID, pq.Array(keepUserIDs)); err != nil {
			return err
		}
	}

	// 清空后若整行无覆盖则删除。
	if _, err := r.sql.ExecContext(ctx, `
		DELETE FROM user_group_rate_multipliers
		WHERE group_id = $1 AND `+emptyRatePredicate+` AND rpm_override IS NULL
	`, groupID); err != nil {
		return err
	}

	if len(entries) == 0 {
		return nil
	}

	userIDs := make([]int64, len(entries))
	rates := make([]float64, len(entries))
	exprs := make([]any, len(entries))
	for i, e := range entries {
		userIDs[i] = e.UserID
		rates[i] = e.RateMultiplier
		exprs[i] = nullableExpr(e.RateMultiplierExpr)
	}
	now := time.Now()
	_, err := r.sql.ExecContext(ctx, `
		INSERT INTO user_group_rate_multipliers (user_id, group_id, rate_multiplier, rate_multiplier_expr, created_at, updated_at)
		SELECT data.user_id, $1::bigint, data.rate_multiplier, data.rate_multiplier_expr, $2::timestamptz, $2::timestamptz
		FROM unnest($3::bigint[], $4::double precision[], $5::text[]) AS data(user_id, rate_multiplier, rate_multiplier_expr)
		ON CONFLICT (user_id, group_id)
		DO UPDATE SET
			rate_multiplier = EXCLUDED.rate_multiplier,
			rate_multiplier_expr = EXCLUDED.rate_multiplier_expr,
			updated_at = EXCLUDED.updated_at
	`, groupID, now, pq.Array(userIDs), pq.Array(rates), pq.Array(exprs))
	return err
}

// SyncGroupRPMOverrides 同步分组的 rpm_override 部分（不触动倍率部分）。
// 语义：
//   - 未出现的用户行：rpm_override 归 NULL；若倍率部分也为空则整行删除。
//   - 出现的用户行：若 RPMOverride 为 nil 则清空；非 nil 则 upsert。
func (r *userGroupRateRepository) SyncGroupRPMOverrides(ctx context.Context, groupID int64, entries []service.GroupRPMOverrideInput) error {
	keepUserIDs := make([]int64, 0, len(entries))
	var clearUserIDs []int64
	upsertUserIDs := make([]int64, 0, len(entries))
	upsertValues := make([]int32, 0, len(entries))
	for _, e := range entries {
		keepUserIDs = append(keepUserIDs, e.UserID)
		if e.RPMOverride == nil {
			clearUserIDs = append(clearUserIDs, e.UserID)
		} else {
			upsertUserIDs = append(upsertUserIDs, e.UserID)
			upsertValues = append(upsertValues, int32(*e.RPMOverride))
		}
	}

	// 未在 entries 列表中的行：清空 rpm_override。
	if len(keepUserIDs) == 0 {
		if _, err := r.sql.ExecContext(ctx, `
			UPDATE user_group_rate_multipliers
			SET rpm_override = NULL, updated_at = NOW()
			WHERE group_id = $1
		`, groupID); err != nil {
			return err
		}
	} else {
		if _, err := r.sql.ExecContext(ctx, `
			UPDATE user_group_rate_multipliers
			SET rpm_override = NULL, updated_at = NOW()
			WHERE group_id = $1 AND user_id <> ALL($2)
		`, groupID, pq.Array(keepUserIDs)); err != nil {
			return err
		}
	}

	// 显式 clear 的行。
	if len(clearUserIDs) > 0 {
		if _, err := r.sql.ExecContext(ctx, `
			UPDATE user_group_rate_multipliers
			SET rpm_override = NULL, updated_at = NOW()
			WHERE group_id = $1 AND user_id = ANY($2)
		`, groupID, pq.Array(clearUserIDs)); err != nil {
			return err
		}
	}

	// 清空后若整行无覆盖则删除（仅表达式覆盖的行必须保留）。
	if _, err := r.sql.ExecContext(ctx, `
		DELETE FROM user_group_rate_multipliers
		WHERE group_id = $1 AND `+emptyRatePredicate+` AND rpm_override IS NULL
	`, groupID); err != nil {
		return err
	}

	if len(upsertUserIDs) > 0 {
		now := time.Now()
		_, err := r.sql.ExecContext(ctx, `
			INSERT INTO user_group_rate_multipliers (user_id, group_id, rpm_override, created_at, updated_at)
			SELECT data.user_id, $1::bigint, data.rpm_override, $2::timestamptz, $2::timestamptz
			FROM unnest($3::bigint[], $4::integer[]) AS data(user_id, rpm_override)
			ON CONFLICT (user_id, group_id)
			DO UPDATE SET rpm_override = EXCLUDED.rpm_override, updated_at = EXCLUDED.updated_at
		`, groupID, now, pq.Array(upsertUserIDs), pq.Array(upsertValues))
		if err != nil {
			return err
		}
	}

	return nil
}

// ClearGroupRPMOverrides 清空指定分组所有行的 rpm_override（保留倍率覆盖，含仅表达式行）。
func (r *userGroupRateRepository) ClearGroupRPMOverrides(ctx context.Context, groupID int64) error {
	if _, err := r.sql.ExecContext(ctx, `
		UPDATE user_group_rate_multipliers
		SET rpm_override = NULL, updated_at = NOW()
		WHERE group_id = $1
	`, groupID); err != nil {
		return err
	}
	_, err := r.sql.ExecContext(ctx, `
		DELETE FROM user_group_rate_multipliers
		WHERE group_id = $1 AND `+emptyRatePredicate+` AND rpm_override IS NULL
	`, groupID)
	return err
}

// DeleteByGroupID 删除指定分组的所有用户专属条目
func (r *userGroupRateRepository) DeleteByGroupID(ctx context.Context, groupID int64) error {
	_, err := r.sql.ExecContext(ctx, `DELETE FROM user_group_rate_multipliers WHERE group_id = $1`, groupID)
	return err
}

// DeleteByUserID 删除指定用户的所有专属条目
func (r *userGroupRateRepository) DeleteByUserID(ctx context.Context, userID int64) error {
	_, err := r.sql.ExecContext(ctx, `DELETE FROM user_group_rate_multipliers WHERE user_id = $1`, userID)
	return err
}

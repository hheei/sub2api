package service

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// UserGroupRate 用户在某个分组的专属计费倍率配置。
// RateMultiplierExpr 非空时优先于 RateMultiplier（动态倍率，见 EvalRateMultiplierExpr）；
// nil *UserGroupRate 表示没有倍率覆盖；数值 0 同样有效，不得折叠为“无覆盖”。
type UserGroupRate struct {
	RateMultiplier float64 `json:"rate_multiplier"`
	// RateMultiplierExpr 可选动态倍率表达式，$up 为所选上游账号的计费倍率。
	RateMultiplierExpr string `json:"rate_multiplier_expr"`
}

// Normalize 归一化表达式空白。
func (r UserGroupRate) Normalize() UserGroupRate {
	r.RateMultiplierExpr = strings.TrimSpace(r.RateMultiplierExpr)
	return r
}

// Validate 校验数值与表达式（保存前调用）。
func (r UserGroupRate) Validate() error {
	if math.IsNaN(r.RateMultiplier) || math.IsInf(r.RateMultiplier, 0) || r.RateMultiplier < 0 {
		return fmt.Errorf("rate_multiplier must be finite and non-negative")
	}
	return ValidateRateMultiplierExpr(r.RateMultiplierExpr)
}

// IsDynamic 表示该配置是否使用动态表达式。
func (r UserGroupRate) IsDynamic() bool {
	return strings.TrimSpace(r.RateMultiplierExpr) != ""
}

// Evaluate 返回 $up 下的有效倍率：表达式优先，否则为静态数值。
// 表达式求值失败时返回错误，调用方沿用既有回退语义。
func (r UserGroupRate) Evaluate(upstream float64) (float64, error) {
	if !r.IsDynamic() {
		return r.RateMultiplier, nil
	}
	return EvalRateMultiplierExpr(r.RateMultiplierExpr, upstream)
}

// UserGroupRateEntry 分组下用户专属倍率/RPM 条目。
// RateMultiplier 与 RPMOverride 均为指针以支持"未设置"语义（NULL）。
type UserGroupRateEntry struct {
	UserID         int64    `json:"user_id"`
	UserName       string   `json:"user_name"`
	UserEmail      string   `json:"user_email"`
	UserNotes      string   `json:"user_notes"`
	UserStatus     string   `json:"user_status"`
	RateMultiplier *float64 `json:"rate_multiplier,omitempty"`
	RPMOverride    *int     `json:"rpm_override,omitempty"`
	// RateMultiplierExpr 非空时为该用户在此分组的动态倍率表达式。
	RateMultiplierExpr string `json:"rate_multiplier_expr,omitempty"`
}

// GroupRateMultiplierInput 批量设置分组倍率的输入条目。
// RateMultiplierExpr 非空时表达优先，RateMultiplier 作为其求值失败时的回退值。
type GroupRateMultiplierInput struct {
	UserID             int64   `json:"user_id"`
	RateMultiplier     float64 `json:"rate_multiplier"`
	RateMultiplierExpr string  `json:"rate_multiplier_expr"`
}

// GroupRPMOverrideInput 批量设置分组 RPM override 的输入条目。
// RPMOverride 为 *int 以支持清除（nil）语义。
type GroupRPMOverrideInput struct {
	UserID      int64 `json:"user_id"`
	RPMOverride *int  `json:"rpm_override"`
}

// UserGroupRateDisplay 面向普通用户展示的专属倍率条目。
// 只暴露静态回退数值与动态标记，绝不下发原始表达式。
type UserGroupRateDisplay struct {
	RateMultiplier float64 `json:"rate_multiplier"`
	IsDynamic      bool    `json:"is_dynamic,omitempty"`
}

// UserGroupRateRepository 用户专属分组倍率/RPM 仓储接口。
// 允许管理员为特定用户设置分组的专属计费倍率与 RPM 上限，覆盖分组默认值。
type UserGroupRateRepository interface {
	// GetByUserID 获取用户所有专属分组倍率（仅返回含倍率覆盖的条目）
	GetByUserID(ctx context.Context, userID int64) (map[int64]UserGroupRate, error)

	// GetByUserAndGroup 获取用户在特定分组的专属倍率（无倍率覆盖时返回 nil）
	GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*UserGroupRate, error)

	// GetRPMOverrideByUserAndGroup 获取用户在特定分组的 rpm_override（NULL 返回 nil）
	GetRPMOverrideByUserAndGroup(ctx context.Context, userID, groupID int64) (*int, error)

	// GetByGroupID 获取指定分组下所有用户的专属配置（rate 与 rpm_override 任一非 NULL 即返回）
	GetByGroupID(ctx context.Context, groupID int64) ([]UserGroupRateEntry, error)

	// SyncUserGroupRates 同步用户的分组专属倍率；nil 表示清空该分组的倍率覆盖
	SyncUserGroupRates(ctx context.Context, userID int64, rates map[int64]*UserGroupRate) error

	// SyncGroupRateMultipliers 批量同步分组的用户专属倍率（替换整组 rate 部分）
	SyncGroupRateMultipliers(ctx context.Context, groupID int64, entries []GroupRateMultiplierInput) error

	// SyncGroupRPMOverrides 批量同步分组的用户专属 RPM（替换整组 rpm_override 部分）。
	// 条目中 RPMOverride 为 nil 时清空对应行的 rpm_override；非 nil 时 upsert。
	SyncGroupRPMOverrides(ctx context.Context, groupID int64, entries []GroupRPMOverrideInput) error

	// ClearGroupRPMOverrides 清空指定分组的所有 rpm_override（整组 rpm 部分归 NULL）
	ClearGroupRPMOverrides(ctx context.Context, groupID int64) error

	// DeleteByGroupID 删除指定分组的所有用户专属条目（分组删除时调用）
	DeleteByGroupID(ctx context.Context, groupID int64) error

	// DeleteByUserID 删除指定用户的所有专属条目（用户删除时调用）
	DeleteByUserID(ctx context.Context, userID int64) error
}

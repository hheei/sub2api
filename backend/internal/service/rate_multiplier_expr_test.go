package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvalRateMultiplierExpr(t *testing.T) {
	tests := []struct {
		name string
		expr string
		up   float64
		want float64
	}{
		{name: "passthrough", expr: "$up", up: 0.6, want: 0.6},
		{name: "markup", expr: "$up * 1.05", up: 0.8, want: 0.84},
		{name: "arithmetic", expr: "($up + 0.1) / 2", up: 0.6, want: 0.35},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvalRateMultiplierExpr(tt.expr, tt.up)
			require.NoError(t, err)
			require.InDelta(t, tt.want, got, 1e-12)
		})
	}
}

func TestEvalRateMultiplierExprRejectsUnsafeSyntax(t *testing.T) {
	for _, expr := range []string{
		"foo($up)",
		"$up > 1",
		"$up / 0",
		"unknown + 1",
		"-$up",
	} {
		t.Run(expr, func(t *testing.T) {
			_, err := EvalRateMultiplierExpr(expr, 0.5)
			require.Error(t, err)
		})
	}
}

// 存量脏数据绕过保存期校验时，表达式求值失败必须回退到同一来源自己的静态值，
// 绝不跨来源回退（用户覆盖存在时分组的表达式与静态值都不参与）。
func TestResolveRateForUpstream_InvalidExpressionFallsBackWithinSameSource(t *testing.T) {
	group := &Group{ID: 42, RateMultiplier: 1.1, RateMultiplierExpr: "$up / ($up - $up)"}

	got, dynamic := resolveRateForUpstream(nil, group, 0.4, "test")
	require.Equal(t, 1.1, got)
	require.False(t, dynamic)

	custom := &UserGroupRate{RateMultiplier: 0.7, RateMultiplierExpr: "$up / ($up - $up)"}
	got, dynamic = resolveRateForUpstream(custom, group, 0.4, "test")
	require.Equal(t, 0.7, got)
	require.False(t, dynamic)
}

func TestResolveStaticRate_EmptyExpressionUsesStaticValue(t *testing.T) {
	group := &Group{RateMultiplier: 0.7}
	got, dynamic := resolveStaticRate(nil, group)
	require.Equal(t, 0.7, got)
	require.False(t, dynamic)

	got, dynamic = resolveStaticRate(&UserGroupRate{RateMultiplier: 0.3}, group)
	require.Equal(t, 0.3, got)
	require.False(t, dynamic)
}

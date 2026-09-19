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
		{name: "actual alias", expr: "$actual", up: 0.6, want: 0.6},
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

func TestResolveRateMultiplierExprPreservesLegacyBehavior(t *testing.T) {
	got, err := ResolveRateMultiplierExpr(&Group{}, 0.7, 0.4)
	require.NoError(t, err)
	require.Equal(t, 0.7, got)
}

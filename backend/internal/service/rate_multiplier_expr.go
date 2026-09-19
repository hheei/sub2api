package service

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"math"
	"strconv"
	"strings"
)

const maxRateMultiplierExprLen = 128

// EvalRateMultiplierExpr evaluates a deliberately small arithmetic expression.
// Supported variables:
//
//	$up selected upstream/account billing rate multiplier
//
// Supported operators are +, -, *, / and parentheses. No functions, calls,
// property access, or arbitrary code are allowed.
func EvalRateMultiplierExpr(expr string, upstream float64) (float64, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return 0, fmt.Errorf("rate multiplier expression is empty")
	}
	if len(expr) > maxRateMultiplierExprLen {
		return 0, fmt.Errorf("rate multiplier expression exceeds %d characters", maxRateMultiplierExprLen)
	}
	if math.IsNaN(upstream) || math.IsInf(upstream, 0) || upstream < 0 {
		return 0, fmt.Errorf("upstream rate multiplier must be finite and non-negative")
	}

	normalized := strings.ReplaceAll(expr, "$up", "up")
	node, err := parser.ParseExpr(normalized)
	if err != nil {
		return 0, fmt.Errorf("invalid rate multiplier expression: %w", err)
	}
	value, err := evalRateMultiplierAST(node, upstream)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, fmt.Errorf("rate multiplier expression must produce a finite non-negative value")
	}
	return value, nil
}

func ValidateRateMultiplierExpr(expr string) error {
	if strings.TrimSpace(expr) == "" {
		return nil
	}
	_, err := EvalRateMultiplierExpr(expr, 1)
	return err
}

func evalRateMultiplierAST(node ast.Expr, upstream float64) (float64, error) {
	switch n := node.(type) {
	case *ast.ParenExpr:
		return evalRateMultiplierAST(n.X, upstream)
	case *ast.Ident:
		if n.Name == "up" {
			return upstream, nil
		}
		return 0, fmt.Errorf("unsupported variable %q; use $up", n.Name)
	case *ast.BasicLit:
		if n.Kind != token.INT && n.Kind != token.FLOAT {
			return 0, fmt.Errorf("unsupported literal %q", n.Value)
		}
		v, err := strconv.ParseFloat(n.Value, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number %q", n.Value)
		}
		return v, nil
	case *ast.UnaryExpr:
		v, err := evalRateMultiplierAST(n.X, upstream)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return v, nil
		case token.SUB:
			return -v, nil
		default:
			return 0, fmt.Errorf("unsupported unary operator %q", n.Op)
		}
	case *ast.BinaryExpr:
		left, err := evalRateMultiplierAST(n.X, upstream)
		if err != nil {
			return 0, err
		}
		right, err := evalRateMultiplierAST(n.Y, upstream)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return left / right, nil
		default:
			return 0, fmt.Errorf("unsupported operator %q", n.Op)
		}
	default:
		return 0, fmt.Errorf("unsupported expression element %T", node)
	}
}

// ResolveRateMultiplierExpr applies the group's optional dynamic expression.
// Empty expression preserves the existing resolved group/user multiplier.
func ResolveRateMultiplierExpr(group *Group, resolved, upstream float64) (float64, error) {
	if group == nil || strings.TrimSpace(group.RateMultiplierExpr) == "" {
		return resolved, nil
	}
	return EvalRateMultiplierExpr(group.RateMultiplierExpr, upstream)
}

// resolveRateMultiplierExprOrLegacy keeps billing available if persisted data
// is invalid despite save-time validation. The already-resolved legacy
// multiplier is always the fallback.
func resolveRateMultiplierExprOrLegacy(group *Group, resolved, upstream float64, component string) float64 {
	value, err := ResolveRateMultiplierExpr(group, resolved, upstream)
	if err == nil {
		return value
	}
	groupID := int64(0)
	if group != nil {
		groupID = group.ID
	}
	slog.Warn("billing.rate_multiplier_expr_invalid",
		"component", component,
		"group_id", groupID,
		"error", err,
	)
	return resolved
}

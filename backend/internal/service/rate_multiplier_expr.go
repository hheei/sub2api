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

// resolveRateForUpstream 按「用户专属 > 分组」求解 $up 下的有效计费倍率，并报告
// 该倍率是否来自成功求值的动态表达式。
//
// 每个来源内部又是「表达式 > 静态数值」：表达式求值失败（存量脏数据绕过保存期
// 校验）时回退到该来源自己的静态数值并告警，绝不跨来源回退——用户覆盖存在时
// 分组表达式永远不参与。custom 为 nil 表示该 (user, group) 没有任何倍率覆盖。
//
// 返回的 dynamic 仅在该次调用真正求出表达式时（$up 已知且表达式合法）为 true；
// 静态数值、回退值与无覆盖一律为 false。
func resolveRateForUpstream(custom *UserGroupRate, group *Group, upstream float64, component string) (float64, bool) {
	if custom != nil {
		if custom.IsDynamic() {
			value, err := EvalRateMultiplierExpr(custom.RateMultiplierExpr, upstream)
			if err == nil {
				return value, true
			}
			logRateMultiplierExprInvalid(component, rateExprSourceUser, group, err)
		}
		return custom.RateMultiplier, false
	}
	if group != nil && strings.TrimSpace(group.RateMultiplierExpr) != "" {
		value, err := EvalRateMultiplierExpr(group.RateMultiplierExpr, upstream)
		if err == nil {
			return value, true
		}
		logRateMultiplierExprInvalid(component, rateExprSourceGroup, group, err)
	}
	return groupStaticRate(group), false
}

// resolveStaticRate 返回不含 $up 的静态口径倍率（用户专属静态 > 分组静态），
// 以及该口径的胜出来源是否为动态表达式。动态表达式无法在没有上游账号的场景
// 下求值，因此只报告 dynamic=true 而不编造任何 $up 结果。
func resolveStaticRate(custom *UserGroupRate, group *Group) (float64, bool) {
	if custom != nil {
		return custom.RateMultiplier, custom.IsDynamic()
	}
	if group != nil && strings.TrimSpace(group.RateMultiplierExpr) != "" {
		return group.RateMultiplier, true
	}
	return groupStaticRate(group), false
}

// groupStaticRate 返回分组静态倍率；无分组配置时等价于系统默认 1.0。
func groupStaticRate(group *Group) float64 {
	if group == nil {
		return 1
	}
	return group.RateMultiplier
}

const (
	rateExprSourceUser  = "user"
	rateExprSourceGroup = "group"
)

func logRateMultiplierExprInvalid(component, source string, group *Group, err error) {
	groupID := int64(0)
	if group != nil {
		groupID = group.ID
	}
	slog.Warn("billing.rate_multiplier_expr_invalid",
		"component", component,
		"source", source,
		"group_id", groupID,
		"error", err,
	)
}

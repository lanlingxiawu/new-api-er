package billingexpr

import (
	"fmt"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"
	"github.com/tidwall/gjson"
	"math"
	"strings"
	"time"
)

// PreviewProgram is request-scoped and never enters the relay compilation cache.
type PreviewProgram struct {
	program  *vm.Program
	UsedVars map[string]bool
}

func CompilePreview(source string) (*PreviewProgram, error) {
	if len(source) > 32768 {
		return nil, fmt.Errorf("expression length exceeded")
	}
	version, body := ParseExprVersion(strings.TrimSpace(source))
	tree, err := parser.Parse(body)
	if err != nil {
		return nil, err
	}
	count := 0
	if err := validatePreviewAST(tree.Node, 0, &count); err != nil {
		return nil, err
	}
	program, err := expr.Compile(body, expr.Env(getCompileEnv(version)), expr.AsFloat64())
	if err != nil {
		return nil, err
	}
	return &PreviewProgram{program: program, UsedVars: extractUsedVars(program)}, nil
}
func (p *PreviewProgram) RunAt(params TokenParams, request RequestInput, at time.Time) (float64, TraceResult, error) {
	trace := TraceResult{}
	if at.IsZero() {
		return 0, trace, fmt.Errorf("scenario time is required")
	}
	zone := func(tz string) time.Time {
		loc, err := time.LoadLocation(strings.TrimSpace(tz))
		if err != nil {
			return at.UTC()
		}
		return at.In(loc)
	}
	headers := normalizeHeaders(request.Headers)

	env := map[string]interface{}{
		"p":     params.P,
		"c":     params.C,
		"len":   params.Len,
		"cr":    params.CR,
		"cc":    params.CC,
		"cc1h":  params.CC1h,
		"img":   params.Img,
		"img_o": params.ImgO,
		"ai":    params.AI,
		"ao":    params.AO,
		"tier": func(name string, value float64) float64 {
			trace.MatchedTier = name
			trace.Cost = value
			return value
		},
		"header": func(key string) string {
			return headers[strings.ToLower(strings.TrimSpace(key))]
		},
		"param": func(path string) interface{} {
			path = strings.TrimSpace(path)
			if path == "" || len(request.Body) == 0 {
				return nil
			}
			result := gjson.GetBytes(request.Body, path)
			if !result.Exists() {
				return nil
			}
			return result.Value()
		},
		"has": func(source interface{}, substr string) bool {
			if source == nil || substr == "" {
				return false
			}
			return strings.Contains(fmt.Sprint(source), substr)
		},
		"hour":    func(tz string) int { return zone(tz).Hour() },
		"minute":  func(tz string) int { return zone(tz).Minute() },
		"weekday": func(tz string) int { return int(zone(tz).Weekday()) },
		"month":   func(tz string) int { return int(zone(tz).Month()) },
		"day":     func(tz string) int { return zone(tz).Day() },
		"max":     math.Max,
		"min":     math.Min,
		"abs":     math.Abs,
		"ceil":    math.Ceil,
		"floor":   math.Floor,
	}

	out, err := expr.Run(p.program, env)
	if err != nil {
		return 0, trace, fmt.Errorf("expr run error: %w", err)
	}
	f, ok := out.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, trace, fmt.Errorf("invalid expression cost")
	}
	return f, trace, nil
}

// validatePreviewAST restricts previews to bounded scalar evaluation. The relay
// compiler remains unchanged; unsupported management previews fail explicitly.
func validatePreviewAST(node ast.Node, depth int, count *int) error {
	*count++
	if depth > 64 || *count > 4096 {
		return fmt.Errorf("preview complexity exceeded")
	}
	var children []ast.Node
	switch n := node.(type) {
	case *ast.IdentifierNode, *ast.IntegerNode, *ast.FloatNode, *ast.StringNode, *ast.BoolNode, *ast.NilNode:
	case *ast.UnaryNode:
		children = []ast.Node{n.Node}
	case *ast.BinaryNode:
		switch n.Operator {
		case "+", "-", "*", "/", "%", "**", "^", "==", "!=", "<", "<=", ">", ">=", "and", "or", "&&", "||", "contains", "startsWith", "endsWith":
		default:
			return fmt.Errorf("unsupported preview operator")
		}
		children = []ast.Node{n.Left, n.Right}
	case *ast.ConditionalNode:
		children = []ast.Node{n.Cond, n.Exp1, n.Exp2}
	case *ast.BuiltinNode:
		// The raw parser recognizes these names as builtins before compilation
		// resolves them against our scalar math functions in the environment.
		// Never admit collection/predicate builtins through this branch.
		switch n.Name {
		case "max", "min", "abs", "ceil", "floor":
		default:
			return fmt.Errorf("unsupported preview function")
		}
		if n.Map != nil || n.Threshold != nil {
			return fmt.Errorf("unsupported preview construct")
		}
		children = n.Arguments
	case *ast.CallNode:
		id, ok := n.Callee.(*ast.IdentifierNode)
		if !ok {
			return fmt.Errorf("unsupported preview call")
		}
		switch id.Value {
		case "max", "min", "abs", "ceil", "floor", "tier", "header", "param", "has", "hour", "minute", "weekday", "month", "day":
		default:
			return fmt.Errorf("unsupported preview function")
		}
		children = n.Arguments
	default:
		return fmt.Errorf("unsupported preview construct")
	}
	for _, child := range children {
		if err := validatePreviewAST(child, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}

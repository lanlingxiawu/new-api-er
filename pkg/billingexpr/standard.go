package billingexpr

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// StandardSegment is a closed integer context-length interval. Nil Max is unbounded.
type StandardSegment struct {
	Min    int64              `json:"min"`
	Max    *int64             `json:"max,omitempty"`
	Name   string             `json:"name"`
	Prices map[string]float64 `json:"prices"`
}
type StandardAnalysis struct {
	SchemaVersion int               `json:"schema_version"`
	Segments      []StandardSegment `json:"segments"`
	UsedVars      map[string]bool   `json:"used_vars"`
}

// AnalyzeStandard proves the complete expression is piecewise nonnegative linear
// pricing over len. It never fills the relay's shared compilation cache.
func AnalyzeStandard(source string) (*StandardAnalysis, error) {
	if len(source) > 32768 || strings.Contains(source, "|||") {
		return nil, fmt.Errorf("unsupported expression")
	}
	source = strings.TrimSpace(source)
	_, body := ParseExprVersion(source)
	tree, err := parser.Parse(body)
	if err != nil {
		return nil, err
	}
	count := 0
	ast.Find(tree.Node, func(ast.Node) bool { count++; return false })
	if count > 4096 {
		return nil, fmt.Errorf("expression node limit exceeded")
	}
	a := &StandardAnalysis{SchemaVersion: 1, UsedVars: map[string]bool{}}
	err = a.visitStandard(tree.Node, 0, math.MaxInt64, 0)
	if err != nil {
		return nil, err
	}
	sort.Slice(a.Segments, func(i, j int) bool { return a.Segments[i].Min < a.Segments[j].Min })
	return a, nil
}

func (a *StandardAnalysis) visitStandard(n ast.Node, lo, hi int64, depth int) error {
	if depth > 64 || len(a.Segments) >= 128 {
		return fmt.Errorf("expression limit exceeded")
	}
	if cond, ok := n.(*ast.ConditionalNode); ok {
		b, ok := cond.Cond.(*ast.BinaryNode)
		if !ok {
			return fmt.Errorf("unsupported condition")
		}
		id, ok := b.Left.(*ast.IdentifierNode)
		if !ok || id.Value != "len" {
			return fmt.Errorf("condition must use len")
		}
		v, ok := standardNumber(b.Right)
		if !ok || v < 0 || v > 1e15 {
			return fmt.Errorf("unsupported boundary")
		}
		var boundary int64
		var lowFirst bool
		switch b.Operator {
		case "<=":
			boundary = int64(math.Floor(v))
			lowFirst = true
		case "<":
			boundary = int64(math.Ceil(v)) - 1
			lowFirst = true
		case ">":
			boundary = int64(math.Floor(v))
		case ">=":
			boundary = int64(math.Ceil(v)) - 1
		default:
			return fmt.Errorf("unsupported boundary operator")
		}
		first, second := cond.Exp1, cond.Exp2
		if !lowFirst {
			first, second = second, first
		}
		// Validate unreachable branches too: source edits must never hide unsupported code.
		if err := a.visitStandard(first, lo, min(hi, boundary), depth+1); err != nil {
			return err
		}
		return a.visitStandard(second, max(lo, boundary+1), hi, depth+1)
	}
	name := ""
	if call, ok := n.(*ast.CallNode); ok {
		id, ok := call.Callee.(*ast.IdentifierNode)
		if !ok || id.Value != "tier" || len(call.Arguments) != 2 {
			return fmt.Errorf("unsupported call")
		}
		label, ok := call.Arguments[0].(*ast.StringNode)
		if !ok {
			return fmt.Errorf("invalid tier name")
		}
		name = label.Value
		n = call.Arguments[1]
	}
	prices := map[string]float64{}
	if err := standardLinear(n, prices, depth+1); err != nil {
		return err
	}
	for key := range prices {
		a.UsedVars[key] = true
	}
	if lo <= hi {
		var upper *int64
		if hi != math.MaxInt64 {
			v := hi
			upper = &v
		}
		a.Segments = append(a.Segments, StandardSegment{Min: lo, Max: upper, Name: name, Prices: prices})
	}
	return nil
}

func standardNumber(n ast.Node) (float64, bool) {
	switch x := n.(type) {
	case *ast.IntegerNode:
		return float64(x.Value), true
	case *ast.FloatNode:
		return x.Value, !math.IsNaN(x.Value) && !math.IsInf(x.Value, 0)
	}
	return 0, false
}

func standardLinear(n ast.Node, prices map[string]float64, depth int) error {
	if depth > 64 {
		return fmt.Errorf("expression depth exceeded")
	}
	b, ok := n.(*ast.BinaryNode)
	if ok && b.Operator == "+" {
		if err := standardLinear(b.Left, prices, depth+1); err != nil {
			return err
		}
		return standardLinear(b.Right, prices, depth+1)
	}
	if !ok || b.Operator != "*" {
		if v, ok := standardNumber(n); ok && v == 0 {
			return nil
		}
		return fmt.Errorf("unsupported price term")
	}
	id, ok := b.Left.(*ast.IdentifierNode)
	number := b.Right
	if !ok {
		id, ok = b.Right.(*ast.IdentifierNode)
		number = b.Left
	}
	if !ok {
		return fmt.Errorf("price must multiply a token variable")
	}
	switch id.Value {
	case "p", "c", "cr", "cc", "cc1h", "img_cr", "img", "img_o", "ai", "ao":
	default:
		return fmt.Errorf("unsupported price variable")
	}
	v, ok := standardNumber(number)
	if !ok || v < 0 {
		return fmt.Errorf("invalid coefficient")
	}
	prices[id.Value] += v
	if math.IsInf(prices[id.Value], 0) {
		return fmt.Errorf("coefficient overflow")
	}
	return nil
}

// EffectivePrices resolves global token exclusion, including variables used only
// in another branch. An absent variable is not synonymous with explicit zero.
func (a *StandardAnalysis) EffectivePrices(s StandardSegment) map[string]float64 {
	result := map[string]float64{"p": s.Prices["p"], "c": s.Prices["c"]}
	for _, key := range []string{"cr", "cc", "cc1h", "img_cr", "img", "ai", "img_o", "ao"} {
		base := "p"
		if key == "img_o" || key == "ao" {
			base = "c"
		}
		result[key] = s.Prices[key]
		if !a.UsedVars[key] {
			result[key] = s.Prices[base]
		}
	}
	return result
}

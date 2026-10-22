package billingexpr

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// EditorDescription is a management-only capability description bound to one
// complete expression. Clients must discard it after arbitrary source edits.
type EditorDescription struct {
	SchemaVersion     int            `json:"schema_version"`
	Kind              string         `json:"kind"`
	Source            string         `json:"source"`
	SourceHash        string         `json:"source_hash"`
	ExpressionVersion int            `json:"expression_version"`
	UsedVars          []string       `json:"used_vars"`
	Branches          []EditorBranch `json:"branches"`
	CanScale          bool           `json:"can_scale"`
	Reason            string         `json:"reason,omitempty"`
}

// EditorBranch follows source order, including unreachable leaves. Prices only
// contains explicitly mentioned variables; omission must not be treated as zero.
type EditorBranch struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Label      string             `json:"label"`
	Min        int64              `json:"min"`
	Max        *int64             `json:"max,omitempty"`
	Reachable  bool               `json:"reachable"`
	Wrapped    bool               `json:"wrapped"`
	Unit       string             `json:"unit,omitempty"`
	Price      *float64           `json:"price,omitempty"`
	Quantity   string             `json:"quantity,omitempty"`
	Prices     map[string]float64 `json:"prices"`
	Body       string             `json:"body"`
	Conditions []EditorCondition  `json:"conditions"`
}

type EditorCondition struct {
	Variable string  `json:"variable"`
	Operator string  `json:"operator"`
	Value    float64 `json:"value"`
	Matches  bool    `json:"matches"`
}

// DescribeEditor uses the same bounded compiler as previews and the complete
// standard AST proof as comparison. It never touches the relay compile cache.
func DescribeEditor(source string) (*EditorDescription, error) {
	program, err := CompilePreview(source)
	if err != nil {
		return nil, err
	}
	version, body := ParseExprVersion(strings.TrimSpace(source))
	editor := &EditorDescription{SchemaVersion: 1, Kind: "custom", Source: source, SourceHash: ExprHashString(source), ExpressionVersion: version, Branches: []EditorBranch{}, UsedVars: []string{}, CanScale: true}
	for _, variable := range []string{"p", "c", "len", "cr", "cc", "cc1h", "img_cr", "img", "img_o", "ai", "ao", "image_count"} {
		if program.UsedVars[variable] {
			editor.UsedVars = append(editor.UsedVars, variable)
		}
	}
	sort.Strings(editor.UsedVars)
	if program.UsedVars["img_cr"] {
		editor.Reason = "custom_rule"
		editor.CanScale = false
		return editor, nil
	}
	tree, err := parser.Parse(body)
	if err != nil {
		return nil, err
	}
	branches := 1
	ast.Find(tree.Node, func(node ast.Node) bool {
		if _, ok := node.(*ast.ConditionalNode); ok {
			branches++
		}
		return false
	})
	fixedPricing := ast.Find(tree.Node, func(node ast.Node) bool {
		identifier, ok := node.(*ast.IdentifierNode)
		return ok && identifier.Value == "fixed"
	}) != nil
	if fixedPricing {
		if branches > 128 {
			editor.Reason = "branch_limit"
			return editor, nil
		}
		if !editor.appendFixedBranches(tree.Node, 0, math.MaxInt64, nil, "request", "") {
			editor.Branches = []EditorBranch{}
			editor.Reason = "custom_rule"
			return editor, nil
		}
		editor.Kind = "fixed"
		return editor, nil
	}
	if _, err := AnalyzeStandard(source); err != nil {
		editor.Reason = "custom_rule"
		return editor, nil
	}
	if branches > 128 {
		editor.Reason = "branch_limit"
		return editor, nil
	}
	editor.Kind = "flat"
	editor.appendBranches(tree.Node, 0, math.MaxInt64, nil)
	if len(editor.Branches) > 1 {
		editor.Kind = "tiered"
	}
	return editor, nil
}

// appendBranches only accepts trees already proven standard by AnalyzeStandard.
func (editor *EditorDescription) appendBranches(node ast.Node, lo, hi int64, conditions []EditorCondition) {
	if conditional, ok := node.(*ast.ConditionalNode); ok {
		comparison := conditional.Cond.(*ast.BinaryNode)
		threshold, _ := standardNumber(comparison.Right)
		boundary := int64(math.Floor(threshold))
		lowFirst := comparison.Operator == "<=" || comparison.Operator == "<"
		if comparison.Operator == "<" || comparison.Operator == ">=" {
			boundary = int64(math.Ceil(threshold)) - 1
		}
		yesLo, yesHi, noLo, noHi := lo, min(hi, boundary), max(lo, boundary+1), hi
		if !lowFirst {
			yesLo, yesHi, noLo, noHi = noLo, noHi, yesLo, yesHi
		}
		condition := EditorCondition{Variable: "len", Operator: comparison.Operator, Value: threshold, Matches: true}
		yesConditions := append(append([]EditorCondition(nil), conditions...), condition)
		condition.Matches = false
		noConditions := append(append([]EditorCondition(nil), conditions...), condition)
		editor.appendBranches(conditional.Exp1, yesLo, yesHi, yesConditions)
		editor.appendBranches(conditional.Exp2, noLo, noHi, noConditions)
		return
	}
	branch := EditorBranch{ID: "branch:" + strconv.Itoa(len(editor.Branches)), Min: lo, Reachable: lo <= hi, Prices: map[string]float64{}, Conditions: conditions}
	if branch.Conditions == nil {
		branch.Conditions = []EditorCondition{}
	}
	if hi != math.MaxInt64 {
		upper := hi
		branch.Max = &upper
	}
	if call, ok := node.(*ast.CallNode); ok {
		branch.Wrapped = true
		branch.Name = call.Arguments[0].(*ast.StringNode).Value
		branch.Label = branch.Name
		node = call.Arguments[1]
	}
	branch.Body = node.String()
	// This cannot fail: the complete tree was validated before describing it.
	_ = standardLinear(node, branch.Prices, 0)
	editor.Branches = append(editor.Branches, branch)
}

// appendFixedBranches describes fixed-pricing trees. Unlike appendBranches the
// tree is not pre-proven by AnalyzeStandard, so every assertion is comma-ok and
// any unsupported shape reports false instead of panicking.
func (editor *EditorDescription) appendFixedBranches(node ast.Node, lo, hi int64, conditions []EditorCondition, unit, quantity string) bool {
	switch part := node.(type) {
	case *ast.ConditionalNode:
		comparison, ok := part.Cond.(*ast.BinaryNode)
		if !ok {
			return false
		}
		left, ok := comparison.Left.(*ast.IdentifierNode)
		if !ok || left.Value != "len" {
			return false
		}
		threshold, ok := standardNumber(comparison.Right)
		if !ok || threshold < 0 || threshold > 1e15 {
			return false
		}
		switch comparison.Operator {
		case "<", "<=", ">", ">=":
		default:
			return false
		}
		boundary := int64(math.Floor(threshold))
		lowFirst := comparison.Operator == "<=" || comparison.Operator == "<"
		if comparison.Operator == "<" || comparison.Operator == ">=" {
			boundary = int64(math.Ceil(threshold)) - 1
		}
		yesLo, yesHi, noLo, noHi := lo, min(hi, boundary), max(lo, boundary+1), hi
		if !lowFirst {
			yesLo, yesHi, noLo, noHi = noLo, noHi, yesLo, yesHi
		}
		condition := EditorCondition{Variable: "len", Operator: comparison.Operator, Value: threshold, Matches: true}
		yesConditions := append(append([]EditorCondition(nil), conditions...), condition)
		condition.Matches = false
		noConditions := append(append([]EditorCondition(nil), conditions...), condition)
		return editor.appendFixedBranches(part.Exp1, yesLo, yesHi, yesConditions, unit, quantity) &&
			editor.appendFixedBranches(part.Exp2, noLo, noHi, noConditions, unit, quantity)
	case *ast.BinaryNode:
		if part.Operator != "*" {
			return false
		}
		leftID, leftIsID := part.Left.(*ast.IdentifierNode)
		rightID, rightIsID := part.Right.(*ast.IdentifierNode)
		leftCount := leftIsID && leftID.Value == "image_count"
		rightCount := rightIsID && rightID.Value == "image_count"
		if leftCount == rightCount {
			return false
		}
		next := part.Left
		if leftCount {
			next = part.Right
		}
		return editor.appendFixedBranches(next, lo, hi, conditions, "image", "image_count")
	case *ast.CallNode:
		callee, ok := part.Callee.(*ast.IdentifierNode)
		if !ok || callee.Value != "tier" || len(part.Arguments) != 2 {
			return false
		}
		name, ok := part.Arguments[0].(*ast.StringNode)
		if !ok {
			return false
		}
		call, ok := part.Arguments[1].(*ast.CallNode)
		if !ok {
			return false
		}
		function, ok := call.Callee.(*ast.IdentifierNode)
		if !ok || function.Value != "fixed" || len(call.Arguments) != 1 {
			return false
		}
		amount, ok := requestRuleNumber(call.Arguments[0])
		if !ok {
			return false
		}
		branch := EditorBranch{ID: "branch:" + strconv.Itoa(len(editor.Branches)), Name: name.Value, Label: name.Value, Min: lo, Reachable: lo <= hi, Wrapped: true, Unit: unit, Price: &amount, Quantity: quantity, Prices: map[string]float64{}, Body: call.String(), Conditions: conditions}
		if branch.Conditions == nil {
			branch.Conditions = []EditorCondition{}
		}
		if hi != math.MaxInt64 {
			upper := hi
			branch.Max = &upper
		}
		editor.Branches = append(editor.Branches, branch)
		return true
	}
	return false
}

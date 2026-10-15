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
	for _, variable := range []string{"p", "c", "len", "cr", "cc", "cc1h", "img", "img_o", "ai", "ao"} {
		if program.UsedVars[variable] {
			editor.UsedVars = append(editor.UsedVars, variable)
		}
	}
	sort.Strings(editor.UsedVars)
	if _, err := AnalyzeStandard(source); err != nil {
		editor.Reason = "custom_rule"
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

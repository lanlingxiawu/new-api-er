package billingexpr

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// RuleDocument is an ephemeral management representation, never a second
// persisted billing contract. SourceHash binds edits to the complete source.
type RuleDocument struct {
	SchemaVersion     int       `json:"schema_version"`
	ExpressionVersion int       `json:"expression_version"`
	SourceHash        string    `json:"source_hash"`
	Root              *RuleNode `json:"root"`
}

// RuleNode uses strings for numeric literals to avoid a JSON/JavaScript number
// round trip changing large integer constants. Fields not used by a kind must
// be empty; no node can inject source text into the generated expression.
type RuleNode struct {
	ID       string      `json:"id"`
	Kind     string      `json:"kind"`
	Value    string      `json:"value,omitempty"`
	Name     string      `json:"name,omitempty"`
	Operator string      `json:"operator,omitempty"`
	Args     []*RuleNode `json:"args,omitempty"`
}

func DescribeRuleDocument(source string) (*RuleDocument, error) {
	if _, err := CompilePreview(source); err != nil {
		return nil, err
	}
	version, body := ParseExprVersion(strings.TrimSpace(source))
	tree, err := parser.Parse(body)
	if err != nil {
		return nil, err
	}
	root, err := describeRuleNode(tree.Node, "n")
	if err != nil {
		return nil, err
	}
	return &RuleDocument{SchemaVersion: 1, ExpressionVersion: version, SourceHash: ExprHashString(source), Root: root}, nil
}

func describeRuleNode(node ast.Node, id string) (*RuleNode, error) {
	r := &RuleNode{ID: id}
	var children []ast.Node
	switch n := node.(type) {
	case *ast.IntegerNode:
		r.Kind = "number"
		r.Value = strconv.FormatInt(int64(n.Value), 10)
	case *ast.FloatNode:
		r.Kind = "number"
		r.Value = strconv.FormatFloat(n.Value, 'e', -1, 64)
	case *ast.StringNode:
		r.Kind = "string"
		r.Value = n.Value
	case *ast.BoolNode:
		r.Kind = "boolean"
		r.Value = strconv.FormatBool(n.Value)
	case *ast.NilNode:
		r.Kind = "null"
	case *ast.IdentifierNode:
		r.Kind = "usage"
		r.Name = n.Value
	case *ast.UnaryNode:
		r.Kind = "unary"
		r.Operator = n.Operator
		children = []ast.Node{n.Node}
	case *ast.BinaryNode:
		r.Kind = "binary"
		r.Operator = n.Operator
		children = []ast.Node{n.Left, n.Right}
	case *ast.ConditionalNode:
		r.Kind = "conditional"
		children = []ast.Node{n.Cond, n.Exp1, n.Exp2}
	case *ast.CallNode:
		callee, ok := n.Callee.(*ast.IdentifierNode)
		if !ok {
			return nil, fmt.Errorf("unsupported rule call")
		}
		r.Kind = "call"
		r.Name = callee.Value
		children = n.Arguments
	case *ast.BuiltinNode:
		r.Kind = "call"
		r.Name = n.Name
		children = n.Arguments
	default:
		return nil, fmt.Errorf("unsupported rule node")
	}
	for index, child := range children {
		arg, err := describeRuleNode(child, id+"/"+strconv.Itoa(index))
		if err != nil {
			return nil, err
		}
		r.Args = append(r.Args, arg)
	}
	return r, nil
}

// CompileRuleDocument compares against a freshly validated baseline. Only a
// proven unchanged tree may reuse the original source bytes.
// Modified trees are emitted with explicit parentheses and compiled in an
// isolated preview; the relay's compilation cache is never touched.
func CompileRuleDocument(source, sourceHash string, document *RuleDocument) (string, error) {
	if document == nil || document.SchemaVersion != 1 || document.ExpressionVersion != 1 || sourceHash == "" || sourceHash != ExprHashString(source) || document.SourceHash != sourceHash {
		return "", fmt.Errorf("rule document source or version mismatch")
	}
	baseline, err := DescribeRuleDocument(source)
	if err != nil {
		return "", err
	}
	if reflect.DeepEqual(document.Root, baseline.Root) {
		return source, nil
	}
	state := ruleDocumentWriter{ids: make(map[string]bool)}
	generated, err := state.write(document.Root, 0)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(strings.TrimSpace(source), "v1:") {
		generated = "v1:" + generated
	}
	if _, err := CompilePreview(generated); err != nil {
		return "", err
	}
	return generated, nil
}

var ruleNumberLiteral = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

type ruleDocumentWriter struct {
	count int
	ids   map[string]bool
}

func (w *ruleDocumentWriter) write(node *RuleNode, depth int) (string, error) {
	w.count++
	if node == nil || depth > 64 || w.count > 4096 || len(node.ID) == 0 || len(node.ID) > 512 || w.ids[node.ID] || len(node.Value) > 32768 {
		return "", fmt.Errorf("invalid rule node or complexity exceeded")
	}
	w.ids[node.ID] = true
	argc := 0
	allowValue, allowName, allowOperator := false, false, false
	switch node.Kind {
	case "number", "string", "boolean":
		allowValue = true
	case "null":
	case "usage":
		allowName = true
	case "unary":
		allowOperator = true
		argc = 1
	case "binary":
		allowOperator = true
		argc = 2
	case "conditional":
		argc = 3
	case "call":
		allowName = true
		switch node.Name {
		case "max", "min", "tier", "has":
			argc = 2
		case "abs", "ceil", "floor", "fixed", "header", "param", "hour", "minute", "weekday", "month", "day":
			argc = 1
		default:
			return "", fmt.Errorf("unsupported rule function")
		}
	default:
		return "", fmt.Errorf("unsupported rule kind")
	}
	if (!allowValue && node.Value != "") || (!allowName && node.Name != "") || (!allowOperator && node.Operator != "") || len(node.Args) != argc {
		return "", fmt.Errorf("invalid fields for rule kind")
	}
	args := make([]string, 0, argc)
	for _, arg := range node.Args {
		text, err := w.write(arg, depth+1)
		if err != nil {
			return "", err
		}
		args = append(args, text)
	}
	var result string
	switch node.Kind {
	case "number":
		value, err := strconv.ParseFloat(node.Value, 64)
		if len(node.Value) > 64 || !ruleNumberLiteral.MatchString(node.Value) || err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return "", fmt.Errorf("invalid rule number")
		}
		result = node.Value
		if strings.HasPrefix(result, "-") || strings.HasPrefix(result, "+") {
			// A signed number is one operand even next to exponentiation.
			result = "(" + result + ")"
		}
	case "string":
		result = strconv.Quote(node.Value)
	case "boolean":
		if node.Value != "true" && node.Value != "false" {
			return "", fmt.Errorf("invalid rule boolean")
		}
		result = node.Value
	case "null":
		result = "nil"
	case "usage":
		switch node.Name {
		case "p", "c", "len", "cr", "cc", "cc1h", "img_cr", "img", "img_o", "ai", "ao", "image_count":
		default:
			return "", fmt.Errorf("unsupported usage value")
		}
		result = node.Name
	case "unary":
		switch node.Operator {
		case "+", "-", "!", "not":
		default:
			return "", fmt.Errorf("unsupported rule unary operation")
		}
		result = "(" + node.Operator + " " + args[0] + ")"
	case "binary":
		switch node.Operator {
		case "+", "-", "*", "/", "%", "**", "^", "==", "!=", "<", "<=", ">", ">=", "and", "or", "&&", "||", "contains", "startsWith", "endsWith":
		default:
			return "", fmt.Errorf("unsupported rule operation")
		}
		result = "(" + args[0] + " " + node.Operator + " " + args[1] + ")"
	case "conditional":
		result = "(" + args[0] + " ? " + args[1] + " : " + args[2] + ")"
	case "call":
		result = node.Name + "(" + strings.Join(args, ", ") + ")"
	}
	if len(result) > 32768 {
		return "", fmt.Errorf("generated rule length exceeded")
	}
	return result, nil
}

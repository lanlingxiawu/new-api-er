package billingexpr

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuleDocumentRoundTrip(t *testing.T) {
	for _, source := range []string{
		"  v1: tier('original label', p * 2 + c*4)  ",
		`param("quality") == "高\"\\\n" ? tier("special",max(p*2,c*4)) : min(abs(p-c),ceil(p/3)+floor(c/2))`,
		`not (header("x") contains "a") && has(param("m"),"fast") ? p**2 + hour("UTC")%3 : +p / 2 - -c`,
		`hour("UTC")>=9 and minute("UTC")<30 or weekday("UTC")==0 ? month("UTC") + day("UTC") : p`,
		`param("missing") == nil ? (true ? cr*0 + img*2 + cc*3 + cc1h*4 + img_o*5 + ai*6 + ao*7 : 0) : p`,
		`header("x") startsWith "ab" || header("x") endsWith "z" ? p^2 : p != c ? p : c`,
		`!false && len>1 && len<=20 ? (p<100 ? p : c) : (3e20 + 9007199254740993)`,
	} {
		t.Run(source, func(t *testing.T) {
			doc, err := DescribeRuleDocument(source)
			require.NoError(t, err)
			got, err := CompileRuleDocument(source, ExprHashString(source), doc)
			require.NoError(t, err)
			require.Equal(t, source, got)
			// Changing an ID forces serialization without changing the calculation.
			doc.Root.ID = "changed"
			generated, err := CompileRuleDocument(source, doc.SourceHash, doc)
			require.NoError(t, err)
			before, err := CompilePreview(source)
			require.NoError(t, err)
			after, err := CompilePreview(generated)
			require.NoError(t, err)
			require.Equal(t, before.UsedVars, after.UsedVars)
			for _, request := range []RequestInput{{}, {Headers: map[string]string{"x": "abc"}, Body: []byte(`{"m":"fast","quality":"high"}`)}} {
				params := TokenParams{P: 10, C: 3, CR: 2, Len: 15}
				at := time.Date(2026, 10, 13, 9, 15, 0, 0, time.UTC)
				x, xt, xe := before.RunAt(params, request, at)
				y, yt, ye := after.RunAt(params, request, at)
				require.Equal(t, xe == nil, ye == nil)
				require.Equal(t, x, y)
				require.Equal(t, xt, yt)
			}
		})
	}
}

func TestRuleDocumentEscapesStringsAndPreservesBranches(t *testing.T) {
	source := `len<0?tier("unreachable",cr*0):tier("base",p*2)`
	doc, err := DescribeRuleDocument(source)
	require.NoError(t, err)
	label := "quote\"\\\n\t\x00中文\"); p * 999 //"
	doc.Root.Args[2].Args[0].Value = label
	generated, err := CompileRuleDocument(source, doc.SourceHash, doc)
	require.NoError(t, err)
	program, err := CompilePreview(generated)
	require.NoError(t, err)
	require.True(t, program.UsedVars["cr"])
	cost, trace, err := program.RunAt(TokenParams{P: 3}, RequestInput{}, time.Now())
	require.NoError(t, err)
	require.Equal(t, 6.0, cost)
	require.Equal(t, label, trace.MatchedTier)
	after, err := DescribeRuleDocument(generated)
	require.NoError(t, err)
	require.Equal(t, "conditional", after.Root.Kind)
	require.Equal(t, "unreachable", after.Root.Args[1].Args[0].Value)
}

func TestRuleDocumentComplexityLimits(t *testing.T) {
	doc, err := DescribeRuleDocument(`p*2`)
	require.NoError(t, err)
	root := &RuleNode{ID: "end", Kind: "number", Value: "1"}
	for i := 0; i < 66; i++ {
		root = &RuleNode{ID: strconv.Itoa(i), Kind: "unary", Operator: "+", Args: []*RuleNode{root}}
	}
	doc.Root = root
	_, err = CompileRuleDocument(`p*2`, doc.SourceHash, doc)
	require.ErrorContains(t, err, "complexity")
	var build func(int, string) *RuleNode
	build = func(depth int, id string) *RuleNode {
		if depth == 0 {
			return &RuleNode{ID: id, Kind: "number", Value: "1"}
		}
		return &RuleNode{ID: id, Kind: "binary", Operator: "+", Args: []*RuleNode{build(depth-1, id+"a"), build(depth-1, id+"b")}}
	}
	doc.Root = build(12, "n")
	_, err = CompileRuleDocument(`p*2`, doc.SourceHash, doc)
	require.ErrorContains(t, err, "complexity")
	doc.Root = &RuleNode{ID: "n", Kind: "binary", Operator: "+", Args: []*RuleNode{{ID: "same", Kind: "number", Value: "1"}, {ID: "same", Kind: "number", Value: "2"}}}
	_, err = CompileRuleDocument(`p*2`, doc.SourceHash, doc)
	require.Error(t, err)
}

func TestRuleDocumentSignedNumberAndUnchangedLongString(t *testing.T) {
	source := `p**2`
	doc, err := DescribeRuleDocument(source)
	require.NoError(t, err)
	doc.Root.Args[0] = &RuleNode{ID: "negative", Kind: "number", Value: "-2"}
	generated, err := CompileRuleDocument(source, doc.SourceHash, doc)
	require.NoError(t, err)
	program, err := CompilePreview(generated)
	require.NoError(t, err)
	cost, _, err := program.RunAt(TokenParams{}, RequestInput{}, time.Now())
	require.NoError(t, err)
	require.Equal(t, 4.0, cost)
	source = "tier(`" + strings.Repeat(`"`, 17000) + "`,p*2)"
	doc, err = DescribeRuleDocument(source)
	require.NoError(t, err)
	generated, err = CompileRuleDocument(source, doc.SourceHash, doc)
	require.NoError(t, err)
	require.Equal(t, source, generated)
}

func TestRuleDocumentRejectsUnsafeChanges(t *testing.T) {
	base := `p*2`
	for _, node := range []*RuleNode{
		{ID: "x", Kind: "number", Value: `2); repeat("x",999999999)`},
		{ID: "x", Kind: "number", Value: "NaN"},
		{ID: "x", Kind: "number", Value: "1e999"},
		{ID: "x", Kind: "call", Name: "repeat", Args: []*RuleNode{}},
		{ID: "x", Kind: "usage", Name: "secret"},
		{ID: "x", Kind: "binary", Operator: ".."},
		{ID: "x", Kind: "number", Value: "1", Name: "p"},
		{ID: "x", Kind: "raw", Value: "p*3"},
		{ID: "x", Kind: "null", Value: "surprise"},
		{ID: "x", Kind: "boolean", Value: "TRUE"},
		{ID: "x", Kind: "conditional", Args: []*RuleNode{nil}},
	} {
		doc, err := DescribeRuleDocument(base)
		require.NoError(t, err)
		doc.Root = node
		_, err = CompileRuleDocument(base, doc.SourceHash, doc)
		require.Error(t, err, node)
	}
	doc, err := DescribeRuleDocument(base)
	require.NoError(t, err)
	_, err = CompileRuleDocument(base, "stale", doc)
	require.Error(t, err)
	doc.SchemaVersion = 2
	_, err = CompileRuleDocument(base, doc.SourceHash, doc)
	require.Error(t, err)
	doc, err = DescribeRuleDocument(base)
	require.NoError(t, err)
	doc.Root.Args[1].Value = "4"
	got, err := CompileRuleDocument(base, doc.SourceHash, doc)
	require.NoError(t, err)
	p, err := CompilePreview(got)
	require.NoError(t, err)
	cost, _, err := p.RunAt(TokenParams{P: 3}, RequestInput{}, time.Now())
	require.NoError(t, err)
	require.Equal(t, 12.0, cost)
	doc.Root = &RuleNode{ID: "x", Kind: "string", Value: strings.Repeat("x", 32769)}
	_, err = CompileRuleDocument(base, doc.SourceHash, doc)
	require.Error(t, err)
	doc.Root = &RuleNode{ID: "cycle", Kind: "unary", Operator: "-"}
	doc.Root.Args = []*RuleNode{doc.Root}
	_, err = CompileRuleDocument(base, doc.SourceHash, doc)
	require.Error(t, err)
}
